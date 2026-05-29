# Sakrylle API OAuth 应用接入指南 (v1 存档)

> ## DEPRECATED — 仅供存档参考
>
> **新接入请直接阅读 [`OAUTH_V2_INTEGRATION.md`](./OAUTH_V2_INTEGRATION.md)**。
>
> 本文档保留的唯一目的:记录 v1 合约,供仍在使用 `image_generation` /
> `balance:read` scope 别名的遗留客户端在迁移窗口期内参考。
> 迁移时间线与别名映射见 [`OAUTH_V2_SCOPE_MIGRATION.md`](./OAUTH_V2_SCOPE_MIGRATION.md)。
>
> **v2 新增能力**(本文档不覆盖):
> - 规范化 scope 体系(11 个 canonical scope)
> - 服务端 authorize transaction + CSRF(防 consent 篡改)
> - RFC 8628 Device Flow(CLI / headless 设备)
> - RFC 7009 Token Revocation 端点(`POST /oauth/revoke`)
> - RFC 8414 Discovery(`GET /.well-known/oauth-authorization-server`)
> - Refresh rotation reuse detection(家族级撤销)
> - 每设备授权管理(`/api/v1/oauth/authorized-apps`)
> - `/v1/me` scope-cropped 用户信息端点
> - Endpoint scope enforcement(`sk_oauth_` token 仅能访问 §7.3 矩阵内路由)
>
> **v1 → v2 关键行为变更**(影响遗留客户端):
> 1. **Scope 别名自动重写**:`image_generation` → `images:create`,`balance:read` → `account:balance:read`。Token 响应的 `scope` 字段返回 canonical 名称。
> 2. **Scope enforcement 生效后**:`sk_oauth_` token 只能访问 §7.3 矩阵列出的端点。未列出的路由返回 403 `insufficient_scope`。遗留客户端如果调用了矩阵外的端点,需要申请新 scope 或改用手动 API key。
> 3. **Refresh reuse detection**:同一 refresh token 提交两次会触发整个 token family 撤销(v1 仅标记旧 token 失效)。客户端必须严格单次使用 refresh token。
> 4. **Authorize 流程**:v2 引入 `POST /api/v1/oauth/authorize/begin` + `POST /api/v1/oauth/authorize/approve` 两步事务。旧的 consent 页 JS 直接 POST approve 的方式仍兼容,但内部已走 transaction 路径。
> 5. **Token 响应新增字段**:`refresh_token_expires_in`(秒)。遗留客户端应忽略未知字段。
> 6. **错误响应新增字段**:`error_uri` 指向 `https://doc.sakrylle.com/developers/oauth/errors#<code>`。
>
> ---
>
> **原始交付对象**:开发"接入 Sakrylle API"的第三方 Web 应用。
> **服务方**:`sub.sakrylle.com`(本仓库,sub2api fork)。
> **示例消费方**:`image.sakrylle.com`(已上线)。
>
> 本文档基于代码反推,与 `backend/internal/handler/oauth_provider_*.go`、`backend/internal/service/oauth_provider_service.go`、`backend/migrations/143_oauth_provider.sql` 一致。如代码与本文档不符,以代码为准。

---

## 0. 30 秒概览

Sakrylle 实现了 **RFC 6749 §4.1 Authorization Code + RFC 7636 PKCE**,自己当 OAuth provider。第三方 webapp 走标准 PKCE 流程拿到一个 `sk_oauth_<...>` 形式的 Bearer token,这个 token 同时也是合法的 Sakrylle API Key —— 直接拿去调 `/v1/chat/completions`、`/v1/images/generations` 等 `/v1/*` 端点,鉴权/计费/限速/Redis 缓存全部走原有路径,不需要任何特殊处理。

> **v2 注意**:scope enforcement 生效后,`sk_oauth_` token 仅能访问 §7.3 scope 矩阵中列出的端点(覆盖所有常用 `/v1/*` 路由)。未列出的路由对 OAuth token 返回 403。手动 API key(`sk-...`)不受影响。

```
浏览器(your-app.example.com)
    ├── 1. 重定向到 https://sub.sakrylle.com/oauth/authorize?...
    ├── 2. 用户在 sub.sakrylle.com 登录并同意授权
    ├── 3. 302 回 redirect_uri?code=...&state=...
    ├── 4. POST /oauth/token (form-encoded) → access_token + refresh_token
    └── 5. Authorization: Bearer sk_oauth_... → /v1/* 直接用
```

---

## 1. 接入前置:注册 OAuth client

OAuth client 不是消费方自助创建,**必须找运维在 `oauth_clients` 表插一行**。提供以下信息:

| 字段 | 说明 | 示例 |
|---|---|---|
| `client_id` | 全局唯一,kebab-case | `your-app-name` |
| `name` | 用户授权页显示的应用名 | `Your App Name` |
| `redirect_uris` | **精确白名单**,不支持通配符/路径前缀 | `["https://your-app.example.com/oauth/callback", "http://localhost:5173/oauth/callback"]` |
| `allowed_scopes` | 允许申请的 scope 集合 | `["image_generation", "balance:read", "models:read"]` |
| `pkce_required` | **设为 `true`**(SPA/移动端公开 client 必须) | `true` |
| `client_secret_hash` | PKCE 公开 client 留空;后端机密 client 才设 bcrypt hash | (空) |
| `default_group_id` | 颁发的 token 绑定到哪个 group(影响可调用模型与计费倍率) | `5`(GPT-Image)、`3`(GPT-Pro)、`2`(Claude-Kiro)... |
| `access_token_ttl_seconds` | access_token 有效期(秒),默认 86400 | `86400` |
| `refresh_token_ttl_seconds` | refresh_token 有效期(秒),默认 2592000(30 天) | `2592000` |

参考 seed:`backend/migrations/143_oauth_provider.sql`(v1 schema)、`backend/migrations/148_oauth_v2_sakrylle_seed.sql`(v2 seed 示例)。

> **v2 新增注册字段**(详见 [`OAUTH_V2_INTEGRATION.md` §2](./OAUTH_V2_INTEGRATION.md#client-registration)):`client_type`、`app_type`、`default_scopes`、`allowed_group_ids`、`device_flow_enabled`、`icon_url`、`homepage_url`、`privacy_url`、`terms_url`。

### 1.1 PKCE-only vs 机密 client

- **PKCE-only(推荐 SPA)**:`pkce_required=true`,`client_secret_hash=NULL`。`code_challenge` + `code_verifier` 替代 client secret。
- **机密 client(后端服务)**:可两者都填。`/oauth/token` 用 HTTP Basic 或 form 字段传 `client_secret`,bcrypt 校验。

> 任何 client 都**至少需要 PKCE 或 secret 之一**。两个都没有的 client 会被服务端拒绝(`ErrOAuthClientMisconfigured`)。

### 1.2 group binding 的影响

颁发的 access_token 落到 `api_keys` 表,`group_id` 取自 `oauth_clients.default_group_id`(回退到 `settings.oauth_default_group_id`)。**这个 group 决定**:

- 用户调 `/v1/models` 看到哪些模型(只列出该 group `channel_model_pricing` 中的模型)
- 计费倍率(group `rate_multiplier` × 用户级 `user_group_rate_multipliers` override)
- 是否能调图像生成(group `allow_image_generation`)

**消费方应用通常应申请专属 group**(避免与现有用户的多 key 场景串扰)。

---

## 2. OAuth 端点契约

### 2.1 端点表

| 端点 | 方法 | 鉴权 | 限速 | Content-Type |
|---|---|---|---|---|
| `/oauth/authorize` | GET/POST | 无(由 sub2api 内部 SPA 处理用户登录) | 30/min/IP **fail-close** | (HTML 响应) |
| `/oauth/token` | POST | client_id (+ optional client_secret) | 20/min/IP **fail-close** | `application/x-www-form-urlencoded` |
| `/oauth/revoke` | POST | client_id (+ optional client_secret) | 30/min/IP **fail-close** | `application/x-www-form-urlencoded` |
| `/oauth/device/code` | POST | client_id | 10/min/IP **fail-close** | `application/x-www-form-urlencoded` |
| `/.well-known/oauth-authorization-server` | GET | 无 | 无 | (JSON 响应) |
| `/v1/*` | * | `Authorization: Bearer sk_oauth_...` | 同 API Key | 同各端点 |

> **fail-close** = Redis 不可用时拒绝请求(不放行)。设计上对齐 `/api/v1/auth/*`。
> **路径根**:`/oauth/*` **不在** `/api/v1` 下,这是有意的 —— 避免被前端 SPA fallback 抢路由。详见 `backend/internal/web/embed_on.go` bypass 列表。
> **v2 新增**:`POST /oauth/authorize`(form-encoded consent)、`/oauth/revoke`、`/oauth/device/code`、`/.well-known/oauth-authorization-server`。

### 2.2 GET /oauth/authorize

**Query 参数**(全部必填):

| 参数 | 说明 |
|---|---|
| `client_id` | 注册时分配 |
| `redirect_uri` | 必须**完全匹配** `redirect_uris` 白名单中某一项 |
| `response_type` | 固定 `code` |
| `scope` | 空格分隔,如 `image_generation balance:read models:read`。必须是 `allowed_scopes` 子集 |
| `state` | **必填**(实现强制),前端生成的随机串,原样回传 |
| `code_challenge` | `BASE64URL-NOPAD(SHA256(code_verifier))` |
| `code_challenge_method` | 固定 `S256`(`plain` 不支持) |

**用户同意**(302):
```
Location: <redirect_uri>?code=<authcode>&state=<state>
```

**用户拒绝**:服务端返回 200 + JSON `{"redirect_to": "<redirect_uri>?error=access_denied&error_description=...&state=..."}`,SPA 跟随。

**校验失败的两种处理**(关键安全语义):

1. **`client_id` 不存在 / 被禁用 / `redirect_uri` 不在白名单** → 渲染**站内 HTML 错误页**,**不**重定向(避免 sub2api 变成 open redirector)。
2. 其它错误(scope/PKCE/state 等) → 302 回 `redirect_uri?error=...&state=...`。

`code` 有效期 **10 分钟,单次使用**。重放会触发 RFC 6749 §10.5 的同 (user, client) 全局 token 清扫,所有 active access/refresh token 立即失效。

### 2.3 POST /oauth/token —— 授权码兑换

**Content-Type**: `application/x-www-form-urlencoded`(标准 OAuth,非 JSON)

```http
POST /oauth/token HTTP/1.1
Host: sub.sakrylle.com
Content-Type: application/x-www-form-urlencoded

grant_type=authorization_code
&code=<authcode>
&redirect_uri=<必须与 authorize 时一致>
&client_id=your-app-name
&code_verifier=<PKCE verifier 原文>
```

机密 client 还可用 HTTP Basic auth 传 `client_id:client_secret`(优先于 form 字段)。

**成功响应**:
```json
{
  "access_token": "sk_oauth_AbCdEfGh...",
  "token_type": "Bearer",
  "expires_in": 86400,
  "refresh_token": "rt_XyZwVuTs...",
  "refresh_token_expires_in": 2592000,
  "scope": "images:create account:balance:read models:read"
}
```

> **v2 变更**:`scope` 字段返回 canonical 名称(即使请求时用了 legacy 别名)。新增 `refresh_token_expires_in` 字段(秒,family-anchored 绝对过期)。遗留客户端应忽略未知字段。

响应头 `Cache-Control: no-store`、`Pragma: no-cache`。

### 2.4 POST /oauth/token —— refresh_token 续期

```http
POST /oauth/token HTTP/1.1
Content-Type: application/x-www-form-urlencoded

grant_type=refresh_token
&refresh_token=<rt_...>
&client_id=your-app-name
```

响应格式同 2.3。**每次都会轮换 refresh_token**(返回新的 `rt_...`,旧值立即失效)。

> **关键**:任何 `/oauth/token` 续期错误都是 **terminal**。客户端**不可重试同一个 refresh_token** —— 旧 refresh 在 rotation 第一步就被原子标记为 revoked,即使后续步骤失败也不会"恢复"。错误处理路径:**清空本地 token → 跳回 `/oauth/authorize` 重走完整流程**。

### 2.5 错误响应(标准 OAuth 4xx)

```json
{
  "error": "invalid_grant",
  "error_description": "authorization code expired",
  "error_uri": "https://doc.sakrylle.com/developers/oauth/errors#invalid_grant"
}
```

> **v2 新增**:`error_uri` 字段指向 Sakrylle 文档对应错误码锚点。遗留客户端应忽略未知字段。

可能的 `error` 值与 HTTP 状态码:

| `error` | HTTP | 触发场景 |
|---|---|---|
| `invalid_request` | 400 | 缺字段/格式错/state 缺失/`code_challenge_method != S256` |
| `invalid_client` | 401 | `client_id` 不存在/被禁用/`client_secret` 错 |
| `invalid_grant` | 400 | code 不存在/过期/已使用、`code_verifier` 不匹配、`refresh_token` 失效、`redirect_uri`/`client_id` 与 authorize 时不一致 |
| `invalid_scope` | 400 | 申请的 scope 不在 `allowed_scopes` 里 |
| `unsupported_grant_type` | 400 | `grant_type` 不是 `authorization_code`、`refresh_token` 或 `urn:ietf:params:oauth:grant-type:device_code` |
| `unsupported_response_type` | 400 | `response_type != code` |
| `insufficient_scope` | 403 | (v2) `sk_oauth_` token 缺少端点所需 scope |
| `temporarily_unavailable` | 503 | OAuth provider 被管理员禁用 |

> **PKCE 失败**(`code_verifier` 与 `code_challenge` 不匹配)使用 `subtle.ConstantTimeCompare` 做常数时间比较,防 timing attack。

---

## 3. 使用 access_token 调 /v1/*

`sk_oauth_<...>` 在 `api_keys` 表里就是一行普通 API Key(`name="OAuth <client_id>"`、`expires_at` = 颁发时间 + `access_token_ttl_seconds`)。直接当 Bearer 用:

```http
GET /v1/models HTTP/1.1
Host: sub.sakrylle.com
Authorization: Bearer sk_oauth_AbCdEfGh...
```

中间件识别 `sk_oauth_` 前缀后,**错误响应改为 RFC 6750 §3.1 的 OAuth 标准外壳**(其它鉴权/计费逻辑完全一致):

```json
{ "error": "invalid_token", "error_description": "Invalid API key" }
```

非 OAuth(`sk-...` API Key)走原有 envelope:
```json
{ "code": "INVALID_API_KEY", "message": "Invalid API key" }
```

> 这一区别让消费方前端可以根据响应外壳形状判断要不要触发 refresh:看到 OAuth 外壳的 401 → 调 refresh;看到普通外壳的 401 → 让用户重新登录。

### 3.1 GET /v1/account/balance

读取当前 token 绑定的用户余额与 group 信息。

> **v2 变更**:v2 scope enforcement 生效后,`sk_oauth_` token 调用此端点需持有 `account:balance:read` 或 `account:read` scope。遗留 `balance:read` 别名会被自动重写为 `account:balance:read`,因此已有客户端无需改动。

```http
GET /v1/account/balance
Authorization: Bearer sk_oauth_...
```

**成功响应**(200):
```json
{
  "user_id": 123,
  "username": "alice",
  "credit_remaining": 12.34,
  "currency_display": "CNY",
  "rate_multiplier": 1.0,
  "group_id": 5,
  "group_name": "GPT-Image",
  "allow_image_generation": true
}
```

字段说明:

- `credit_remaining`:**数值未做 FX 转换**。Sakrylle 内部数值是"USD-equivalent units",但 UI 用 `￥` 渲染(参见 `CLAUDE.md` Currency policy)。消费方应直接根据 `currency_display` 选符号渲染,**不要再除以汇率**。
- `currency_display`:目前固定 `"CNY"`。
- `rate_multiplier`:用户在该 group 的实际计费倍率(已合并 user-level override)。
- `allow_image_generation`:该 group 是否支持图像生成。前端用此判断要不要露出图像 UI。

> 旧版 `docs/SAKRYLLE_API_SPEC.md` 描述的 `credit_remaining_cny` / `credit_remaining_usd` 双字段**未实现**,以本文档为准。

### 3.2 GET /v1/models

OpenAI 兼容 list,返回**当前 token 绑定 group 实际可用的模型**。

```json
{
  "object": "list",
  "data": [
    {
      "id": "gpt-image-2",
      "object": "model",
      "owned_by": "sakrylle",
      "allow_image_generation": true,
      "billing_mode": "per_request",
      "per_request_price_usd": 0.15
    }
  ]
}
```

> Sakrylle **从不**用上游 `claude.DefaultModels` / `openai.DefaultModels` 作为 fallback —— 没配的模型不会出现。空列表说明 group 没有任何 `channel_model_pricing` 行,应让运维检查。

### 3.3 业务端点(/v1/chat/completions、/v1/messages、/v1/images/*)

请求/响应与上游(Anthropic / OpenAI)完全一致,Sakrylle 网关在 entry-protocol 与 upstream-protocol 之间双向 transform。**消费方只需把 `Authorization: Bearer sk_oauth_...` 加上**,其它代码与对接 OpenAI/Anthropic SDK 没区别(把 baseURL 指到 `https://sub.sakrylle.com/v1` 或 `https://api.sakrylle.com/v1` 即可)。

### 3.4 业务错误(OpenAI 兼容外壳)

```json
{ "error": { "code": "image_generation_not_enabled", "message": "..." } }
```

| HTTP | `code` | 触发 | 前端建议 |
|---|---|---|---|
| 401 | `invalid_token` | token 过期/无效 | 触发 refresh,失败则跳登录 |
| 403 | `image_generation_not_enabled` | group 不允许图像生成 | 提示并隐藏图像 UI |
| 403 | `insufficient_quota` | 余额不足 | 链接到 `https://sub.sakrylle.com/recharge` |
| 429 | (-) | 限速 | 指数退避或提示稍后重试 |

---

## 4. Token 生命周期与轮换

### 4.1 时序

```
authorize → code (10min, 单次)
           └→ /oauth/token (auth_code)
              ├→ access_token (默认 24h)
              └→ refresh_token (默认 30d)

refresh_token + /oauth/token (refresh_token grant)
              ├→ 旧 refresh: 立即 revoked, rotated_to_hash 指向新值
              ├→ 旧 access_token (api_keys 行): status=disabled + Redis 缓存失效
              ├→ 新 access_token (新 api_keys 行)
              └→ 新 refresh_token
```

### 4.2 撤销语义

**v2 变更**:v2 引入了 token family reuse detection。重放已轮换的 refresh token 会触发**整个 token family**(所有 access + refresh)立即撤销,而非仅清扫 (user, client) 级别。客户端必须严格保证 refresh token 单次使用。

撤销触发路径:

1. **Code 重放**(`/oauth/token` 收到已使用的 code) → 服务端自动清扫(RFC 6749 §10.5)
2. **Refresh token 重放**(v2) → 整个 token family 撤销,`slog.Warn` 记账
3. **用户在 sub.sakrylle.com 个人中心点"撤销授权"** → `DELETE /api/v1/oauth/grants/:client_id` 或 `DELETE /api/v1/oauth/authorized-apps/:grant_id`
4. **客户端主动撤销**(v2) → `POST /oauth/revoke`(RFC 7009,幂等)
5. **运维直接 SQL** → `UPDATE oauth_refresh_tokens SET revoked_at=now() WHERE user_id=? AND client_id=?` + `UPDATE api_keys SET status='disabled' WHERE id IN (...)` + Redis Pub/Sub `auth:cache:invalidate <plaintext>`

> **Redis 鉴权缓存**:`apikey:auth:<sha>` TTL 60s。撤销路径都会发 Pub/Sub invalidate;**SQL 路径必须手发**(`docker exec sub2api-redis redis-cli PUBLISH auth:cache:invalidate '<full-plaintext>'`),否则 60s 内仍可调用。

### 4.3 客户端最小状态机

```
idle ──(redirect to /oauth/authorize)──→ awaiting_callback
awaiting_callback ──(callback?code=)──→ exchanging
                  ──(callback?error=)──→ idle (show error)
exchanging ──(success)──→ active
exchanging ──(failure)──→ idle

active ──(API 401: invalid_token)──→ refreshing
refreshing ──(success)──→ active
refreshing ──(任何错误)──→ idle (清 token, 重新走 authorize)
```

**关键不变量**:`refreshing` 失败**永不重试同一 refresh_token**。

---

## 5. 用户侧已授权应用管理(消费方不直接调,但需了解)

sub.sakrylle.com 自己的 SPA 在用户中心提供"已授权应用"页面,后端端点:

**v1 兼容端点**(仍可用):
- `GET /api/v1/oauth/grants` —— 列当前用户的所有 grant(每个 `client_id` 一行)
- `DELETE /api/v1/oauth/grants/:client_id` —— 撤销该 client 的全部 token

**v2 新增端点**(per-device 粒度):
- `GET /api/v1/oauth/authorized-apps` —— 列每设备的 grant 详情
- `DELETE /api/v1/oauth/authorized-apps/:grant_id` —— 撤销单个设备 grant(需 step-up auth)
- `DELETE /api/v1/oauth/authorized-apps/client/:client_id` —— 撤销某 client 所有设备(需 step-up auth)

**这些端点全部是 JWT 鉴权**(用户在 sub.sakrylle.com 已登录的 session),**不能用 `sk_oauth_` token 调**。第三方应用如果想提供"在我这里登出"功能,正确做法是**自己丢弃本地 token + 提示用户也可以去 sub.sakrylle.com 撤销**;不要尝试从消费方应用代为撤销。

> v2 新增 `POST /oauth/revoke`(RFC 7009)允许客户端主动撤销自己的 token,无需 JWT。详见 [`OAUTH_V2_INTEGRATION.md` §9](./OAUTH_V2_INTEGRATION.md#9-token-revocation)。

---

## 6. 实现样板

### 6.1 PKCE 生成(浏览器端,Web Crypto API)

```typescript
// src/oauth/pkce.ts
function base64UrlEncode(bytes: Uint8Array): string {
  const b64 = btoa(String.fromCharCode(...bytes));
  return b64.replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export async function generatePKCE(): Promise<{ verifier: string; challenge: string }> {
  const randomBytes = new Uint8Array(32);
  crypto.getRandomValues(randomBytes);
  const verifier = base64UrlEncode(randomBytes);

  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier));
  const challenge = base64UrlEncode(new Uint8Array(digest));

  return { verifier, challenge };
}

export function generateState(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return base64UrlEncode(bytes);
}
```

### 6.2 SPA 完整流程(TypeScript)

```typescript
// src/oauth/client.ts
const CONFIG = {
  authorizeURL: "https://sub.sakrylle.com/oauth/authorize",
  tokenURL: "https://sub.sakrylle.com/oauth/token",
  clientId: "your-app-name",
  redirectUri: `${window.location.origin}/oauth/callback`,
  scope: "image_generation balance:read models:read",
};

// === Step 1: 发起授权 ===
export async function startLogin() {
  const { verifier, challenge } = await generatePKCE();
  const state = generateState();

  // 持久化以便回调时校验
  sessionStorage.setItem("oauth_verifier", verifier);
  sessionStorage.setItem("oauth_state", state);

  const params = new URLSearchParams({
    client_id: CONFIG.clientId,
    redirect_uri: CONFIG.redirectUri,
    response_type: "code",
    scope: CONFIG.scope,
    state,
    code_challenge: challenge,
    code_challenge_method: "S256",
  });

  window.location.href = `${CONFIG.authorizeURL}?${params}`;
}

// === Step 2: 处理回调 ===
export async function handleCallback(): Promise<TokenResponse> {
  const url = new URL(window.location.href);
  const code = url.searchParams.get("code");
  const state = url.searchParams.get("state");
  const error = url.searchParams.get("error");

  if (error) throw new Error(`oauth: ${error} - ${url.searchParams.get("error_description")}`);
  if (!code) throw new Error("oauth: missing code");

  const expectedState = sessionStorage.getItem("oauth_state");
  if (state !== expectedState) throw new Error("oauth: state mismatch (CSRF?)");

  const verifier = sessionStorage.getItem("oauth_verifier");
  if (!verifier) throw new Error("oauth: missing verifier");

  sessionStorage.removeItem("oauth_state");
  sessionStorage.removeItem("oauth_verifier");

  return exchangeCodeForToken(code, verifier);
}

async function exchangeCodeForToken(code: string, verifier: string): Promise<TokenResponse> {
  const body = new URLSearchParams({
    grant_type: "authorization_code",
    code,
    redirect_uri: CONFIG.redirectUri,
    client_id: CONFIG.clientId,
    code_verifier: verifier,
  });

  const res = await fetch(CONFIG.tokenURL, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body,
  });

  if (!res.ok) {
    const err = await res.json().catch(() => ({}));
    throw new Error(`oauth: token exchange failed - ${err.error}: ${err.error_description}`);
  }
  return res.json();
}

// === Step 3: refresh ===
export async function refreshToken(refreshTokenValue: string): Promise<TokenResponse> {
  const body = new URLSearchParams({
    grant_type: "refresh_token",
    refresh_token: refreshTokenValue,
    client_id: CONFIG.clientId,
  });

  const res = await fetch(CONFIG.tokenURL, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body,
  });

  if (!res.ok) {
    // CRITICAL: refresh 错误是 terminal,清 token 重走 authorize
    throw new Error("oauth: refresh failed (terminal)");
  }
  return res.json();
}

interface TokenResponse {
  access_token: string;   // sk_oauth_...
  token_type: "Bearer";
  expires_in: number;     // 秒
  refresh_token: string;  // rt_...
  refresh_token_expires_in?: number; // 秒 (v2 新增,family-anchored)
  scope: string;          // v2 返回 canonical 名称
}
```

### 6.3 带自动 refresh 的 fetch wrapper

```typescript
// src/api/sakrylle.ts
import { refreshToken } from "../oauth/client";

interface TokenStore {
  getAccessToken(): string | null;
  getRefreshToken(): string | null;
  setTokens(access: string, refresh: string, expiresAt: number): void;
  clear(): void;
}

let refreshInFlight: Promise<void> | null = null;

export async function sakrylleFetch(
  store: TokenStore,
  path: string,
  init: RequestInit = {},
): Promise<Response> {
  const access = store.getAccessToken();
  if (!access) throw new Error("not authenticated");

  const headers = new Headers(init.headers);
  headers.set("Authorization", `Bearer ${access}`);
  const res = await fetch(`https://sub.sakrylle.com${path}`, { ...init, headers });

  // OAuth 外壳的 401 → 触发 refresh
  if (res.status === 401) {
    const body = await res.clone().json().catch(() => ({}));
    if (body.error === "invalid_token") {
      await dedupedRefresh(store);
      const retried = new Headers(init.headers);
      retried.set("Authorization", `Bearer ${store.getAccessToken()}`);
      return fetch(`https://sub.sakrylle.com${path}`, { ...init, headers: retried });
    }
  }
  return res;
}

async function dedupedRefresh(store: TokenStore): Promise<void> {
  if (refreshInFlight) return refreshInFlight;
  refreshInFlight = (async () => {
    try {
      const rt = store.getRefreshToken();
      if (!rt) throw new Error("no refresh token");
      const resp = await refreshToken(rt);
      store.setTokens(
        resp.access_token,
        resp.refresh_token,
        Date.now() + resp.expires_in * 1000,
      );
    } catch (err) {
      // refresh 任何失败都是 terminal —— 清 token,让上层重走 authorize
      store.clear();
      throw err;
    } finally {
      refreshInFlight = null;
    }
  })();
  return refreshInFlight;
}
```

### 6.4 Token 存储建议

| 存储位置 | 适用场景 | 安全性 |
|---|---|---|
| `localStorage` | SPA,容忍 XSS 风险换 UX | XSS 可窃取 |
| `sessionStorage` | 单标签页,关闭即失效 | XSS 可窃取 |
| **`HttpOnly` cookie + 后端 BFF** | 生产推荐 | JS 不可读 |
| In-memory only | 高安全场景,接受刷新页面后重新登录 | 最安全 |

如果一定要 `localStorage`,**严格 CSP**(`default-src 'self'`、禁 inline script)+ Subresource Integrity 是底线。

### 6.5 cURL 端到端验证脚本

```bash
#!/usr/bin/env bash
# 仅用于联调 —— 真实流程需走浏览器
set -euo pipefail

CLIENT_ID="your-app-name"
REDIRECT_URI="http://localhost:5173/oauth/callback"
HOST="https://sub.sakrylle.com"

# PKCE
VERIFIER=$(openssl rand -base64 32 | tr -d '=+/' | cut -c1-43)
CHALLENGE=$(printf '%s' "$VERIFIER" | openssl dgst -sha256 -binary | openssl base64 -A | tr -d '=' | tr '+/' '-_')
STATE=$(openssl rand -hex 16)

echo "→ 浏览器打开:"
echo "$HOST/oauth/authorize?client_id=$CLIENT_ID&redirect_uri=$(jq -rn --arg v "$REDIRECT_URI" '$v|@uri')&response_type=code&scope=image_generation+balance:read+models:read&state=$STATE&code_challenge=$CHALLENGE&code_challenge_method=S256"
echo ""
read -rp "粘贴回调 URL 中的 code: " CODE

# 兑换
curl -sS -X POST "$HOST/oauth/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  --data-urlencode "grant_type=authorization_code" \
  --data-urlencode "code=$CODE" \
  --data-urlencode "redirect_uri=$REDIRECT_URI" \
  --data-urlencode "client_id=$CLIENT_ID" \
  --data-urlencode "code_verifier=$VERIFIER" | jq .
```

---

## 7. 边界 case 与已踩坑

### 7.1 redirect_uri 必须**完全匹配**

代码层面是字符串相等(`uri == target`),不是前缀匹配。`https://your-app.example.com/oauth/callback` ≠ `https://your-app.example.com/oauth/callback/` ≠ `https://your-app.example.com/oauth/callback?foo=bar`。**回调 URL 不要带查询串作为常量**;`code` 与 `state` 是服务端追加的,客户端处理时拆开用。

本地开发建议 `http://localhost:5173/oauth/callback` 与生产 URL 都注册到 `redirect_uris`。

### 7.2 state 必填且必须校验

服务端强制 state 非空(`ErrOAuthMissingState`)。客户端必须自己存 state 并在回调时严格相等比较 —— 这是 CSRF 防御,不是装饰。

### 7.3 CORS

`/oauth/token`、`/v1/account/balance`、`/v1/models`、`/v1/chat/completions`、`/v1/images/*` 等需要在 sub2api CORS allow-origin 包含消费方源(运维需更新配置)。`/oauth/authorize` 是浏览器导航,**不**走 CORS。

### 7.4 公开 client 不发 client_secret

PKCE-only client 的 `/oauth/token` 请求**不要**带 `client_secret`(也不要 Basic auth)。代码逻辑:`client_secret_hash` 为空 → 跳过 secret 校验;非空 → 必须匹配。意外发了非空 secret 给 PKCE-only client 不会报错(`client_secret_hash=""` 直接 return nil),但仍是反模式。

### 7.5 expires_in 是秒不是毫秒

`expires_in: 86400` = 24 小时。计算到期时间:`Date.now() + expires_in * 1000`。

### 7.6 access_token 与 refresh_token TTL 解耦

默认 access 24h、refresh 30d。**不要假定 refresh 总比 access 长**(运维可调);客户端应同时记录两者的到期时间,refresh 也过期就只能重走 authorize。

### 7.7 同一用户对同一 client 多设备

每次 `/oauth/authorize` 都会发新 code → 新 access + 新 refresh。**不会**踢掉其它设备的 token —— 多设备并行存在,各自独立 rotation。`/api/v1/oauth/grants` 用 `active_token_count` 字段告诉用户当前有几个活跃 session。

### 7.8 group binding 决定能调什么

如果消费方 client 绑到 group_id=5 (GPT-Image),拿到的 token 调 `/v1/chat/completions` 会被拒(group 没有 chat 模型)。需要跨 group 能力的 webapp 应当注册多个 client 或申请专属 group。

### 7.9 access_token 出现在日志/截图风险

`sk_oauth_<...>` 是明文 Bearer。**禁止**:
- console.log 打全文(只打前缀 `sk_oauth_***`)
- 提交到 git(`.env`/`localStorage` 备份导出时小心)
- 通过 query string 传(用 Authorization header 或 form body)

### 7.10 限速反应

`/oauth/authorize` 30/min/IP、`/oauth/token` 20/min/IP。fail-close 意味着 Redis 抖一下就 5xx —— 客户端遇到 5xx 时**不要疯狂重试**,指数退避(初始 1s,最多 4 次)。

---

## 8. 联调 checklist

接入完成前确认:

- [ ] 已与运维确认 client 在 `oauth_clients` 表里(`client_id`、`redirect_uris`、`allowed_scopes`、`default_group_id` 都对)
- [ ] 本地 `http://localhost:<port>/oauth/callback` 已加入 `redirect_uris`
- [ ] PKCE 流程跑通:用 6.5 的 cURL 脚本能拿到 `access_token` 与 `refresh_token`
- [ ] `sk_oauth_<...>` 调 `/v1/account/balance` 返回 200 + 期望字段
- [ ] `sk_oauth_<...>` 调 `/v1/models` 列出预期模型
- [ ] `sk_oauth_<...>` 调业务端点(chat/images)真实扣费(在 sub.sakrylle.com 后台 `usage_logs` 看到行)
- [ ] Refresh 流程跑通:`grant_type=refresh_token` 拿到新 access + **新** refresh
- [ ] state 不匹配的回调被前端拒绝
- [ ] 错误的 `client_id` / `redirect_uri` / `scope` 返回 OAuth 标准错误
- [ ] OAuth 401(`invalid_token`)触发自动 refresh,refresh 失败时清 token 跳登录
- [ ] CSP/CORS 配置:消费方 origin 在 sub2api CORS allow-list

---

## 9. 引用

- 代码:
  - `backend/internal/handler/oauth_provider_handler.go`(主端点:authorize, token, revoke, grants, authorized-apps)
  - `backend/internal/handler/oauth_device_handler.go`(RFC 8628 device flow 端点)
  - `backend/internal/handler/oauth_provider_consent.go`(同意页 HTML + XSS 防御)
  - `backend/internal/handler/oauth_provider_account_handler.go`(`/v1/account/balance`、`/v1/me`)
  - `backend/internal/service/oauth_provider_service.go`(核心逻辑、PKCE、rotation、reuse detection)
  - `backend/internal/service/oauth_provider_types.go`(域类型、错误清单)
  - `backend/internal/service/oauth_scopes.go`(canonical scope 注册表、legacy 别名映射)
  - `backend/internal/repository/oauth_provider_repo.go`(DB 层)
  - `backend/internal/server/routes/oauth.go`(路由 + 限速)
  - `backend/internal/server/routes/oauth_device.go`(device flow 路由)
  - `backend/internal/server/middleware/oauth_scope.go`(§7.3 scope enforcement)
  - `backend/internal/server/middleware/api_key_auth.go`(`sk_oauth_` 前缀识别)
  - `backend/migrations/143_oauth_provider.sql`(v1 表结构)
  - `backend/migrations/144_oauth_seed_sakrylle.sql`(seed 示例)
  - `backend/migrations/145_oauth_v2.sql`(v2 schema 扩展)
  - `backend/migrations/146_oauth_authorize_transaction_user_id.sql`(transaction 补丁)
  - `backend/migrations/148_oauth_v2_sakrylle_seed.sql`(v2 seed)
- v2 文档:
  - [`docs/OAUTH_V2_INTEGRATION.md`](./OAUTH_V2_INTEGRATION.md)(v2 集成指南,新接入首选)
  - [`docs/OAUTH_V2_DESIGN.md`](./OAUTH_V2_DESIGN.md)(完整设计契约)
  - [`docs/OAUTH_V2_ERROR_REFERENCE.md`](./OAUTH_V2_ERROR_REFERENCE.md)(错误码全表)
  - [`docs/OAUTH_V2_DEVICE_FLOW_CLI_GUIDE.md`](./OAUTH_V2_DEVICE_FLOW_CLI_GUIDE.md)(CLI 设备流指南)
  - [`docs/OAUTH_V2_SCOPE_MIGRATION.md`](./OAUTH_V2_SCOPE_MIGRATION.md)(scope 迁移时间线)
  - [`docs/OAUTH_V2_FIRST_PARTY_SECURITY_CHECKLIST.md`](./OAUTH_V2_FIRST_PARTY_SECURITY_CHECKLIST.md)(第一方安全清单)
- 历史:`docs/SAKRYLLE_API_SPEC.md`(消费方早期契约,部分字段名与本文不一致,以本文为准)
- 项目背景:`CLAUDE.md` § OAuth 2.0 provider、Currency policy
- 标准:[RFC 6749](https://datatracker.ietf.org/doc/html/rfc6749)、[RFC 7636 PKCE](https://datatracker.ietf.org/doc/html/rfc7636)、[RFC 6750 Bearer](https://datatracker.ietf.org/doc/html/rfc6750)、[RFC 7009 Revocation](https://datatracker.ietf.org/doc/html/rfc7009)、[RFC 8414 Discovery](https://datatracker.ietf.org/doc/html/rfc8414)、[RFC 8628 Device Flow](https://datatracker.ietf.org/doc/html/rfc8628)
