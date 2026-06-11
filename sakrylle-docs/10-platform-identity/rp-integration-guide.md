---
title: 06 · OIDC RP 接入指南
status: canonical
scope: platform
last_verified: 2026-06-10
---

# 06 · OIDC RP 接入指南

> **文档类型**：技术参考文档（非规划文档）
> **更新日期**：2026-06-05
> **Issuer**：`https://sub.sakrylle.com`
> **关联文档**：`02`（OAuth 现状）、`03`（OIDC 架构）、`04`（商业边界）、`05`（配置隔离）、`51`（Image 升级方案）
>
> 本文档是面向 RP（Relying Party）开发者的 **OIDC 端点协议参考**。5 个客户端产品（Image / CLI / Studio / Web / Chat）的开发 Agent 应能仅凭本文完成 OIDC 接入。

---

## 1. 概述

Sakrylle API（sub2api fork）是一个完整的 **OpenID Connect Provider**，同时兼任 AI API 网关。RP 通过标准 OIDC 流程获取 `access_token`（opaque，前缀 `sk_oauth_`），用其调用 `/v1/*` 网关端点。

**关键架构约束**：
- `access_token` 是 opaque token，不签名，写入 `api_keys` 表，与手动 API Key 共享计费/缓存/限流路径
- `id_token` 仅用于身份证明（"你是谁"），**绝不**用作 `/v1/*` 的 Bearer
- 余额、配额、模型权限等可变商业状态**绝不**进入 `id_token`，必须通过 `/v1/me` 实时查询
- 所有公共 client 强制 PKCE S256

---

## 2. 端点总览

| # | 方法 | 路径 | 用途 | 认证方式 |
|---|---|---|---|---|
| 1 | GET | `/.well-known/openid-configuration` | OIDC Discovery | 无 |
| 2 | GET | `/.well-known/jwks.json` | 公钥发布（JWKS） | 无 |
| 3 | GET/POST | `/oauth/authorize` | 授权端点（渲染同意页） | 无（浏览器） |
| 4 | POST | `/oauth/token` | Token 端点 | client_id + client_secret（可选） |
| 5 | POST | `/oauth/revoke` | Token 吊销（RFC 7009） | client_id + client_secret（可选） |
| 6 | POST | `/oauth/introspect` | Token 内省（RFC 7662） | 仅机密 client |
| 7 | GET/POST | `/oauth/logout` | RP-Initiated Logout | 无 |
| 8 | GET | `/oauth/frontchannel-logout` | Front-Channel Logout | 无 |
| 9 | POST | `/oauth/device/code` | Device Authorization（RFC 8628） | 无 |
| 10 | GET | `/oauth/device` | 设备验证页 | 无 |
| 11 | GET/POST | `/userinfo` | OIDC UserInfo | Bearer（`sk_oauth_`） |
| 12 | GET | `/v1/me` | UserInfo（兼 API） | Bearer（`sk_oauth_` 或 API Key） |

---

## 3. Discovery（`/.well-known/openid-configuration`）

### 请求

```bash
curl https://sub.sakrylle.com/.well-known/openid-configuration
```

### 响应（完整字段）

```json
{
  "issuer": "https://sub.sakrylle.com",
  "authorization_endpoint": "https://sub.sakrylle.com/oauth/authorize",
  "token_endpoint": "https://sub.sakrylle.com/oauth/token",
  "userinfo_endpoint": "https://sub.sakrylle.com/userinfo",
  "jwks_uri": "https://sub.sakrylle.com/.well-known/jwks.json",
  "end_session_endpoint": "https://sub.sakrylle.com/oauth/logout",
  "frontchannel_logout_supported": true,
  "frontchannel_logout_session_supported": true,
  "backchannel_logout_supported": true,
  "backchannel_logout_session_supported": true,
  "response_types_supported": ["code"],
  "response_modes_supported": ["query"],
  "subject_types_supported": ["public", "pairwise"],
  "id_token_signing_alg_values_supported": ["RS256", "ES256"],
  "userinfo_signing_alg_values_supported": ["RS256", "ES256"],
  "request_parameter_supported": true,
  "request_uri_parameter_supported": true,
  "claims_parameter_supported": true,
  "grant_types_supported": [
    "authorization_code",
    "refresh_token",
    "urn:ietf:params:oauth:grant-type:device_code"
  ],
  "code_challenge_methods_supported": ["S256"],
  "scopes_supported": [
    "openid", "profile", "email", "offline_access",
    "account:read", "account:balance:read", "models:read",
    "chat.completions:create", "responses:create", "messages:create",
    "images:create", "profile:read", "email:read", "usage:read"
  ],
  "token_endpoint_auth_methods_supported": ["none", "client_secret_basic", "client_secret_post"],
  "claims_supported": [
    "iss", "sub", "aud", "exp", "iat", "nonce",
    "name", "preferred_username", "email", "email_verified",
    "auth_time", "sid", "at_hash", "c_hash"
  ],
  "prompt_values_supported": ["none", "login", "consent", "select_account"],
  "service_documentation": "https://doc.sakrylle.com/developers/oauth/"
}
```

**Cache-Control**: `public, max-age=60`

> **注意**：`/.well-known/oauth-authorization-server`（RFC 8414）也同时存在，字段基本相同但不含 OIDC 特有字段（`subject_types_supported`、`id_token_signing_alg_values_supported` 等）。RP 应优先使用 `/.well-known/openid-configuration`。

---

## 4. JWKS（`/.well-known/jwks.json`）

### 请求

```bash
curl https://sub.sakrylle.com/.well-known/jwks.json
```

### 响应格式

```json
{
  "keys": [
    {
      "kty": "RSA",
      "use": "sig",
      "kid": "<base64url-jwk-thumbprint>",
      "alg": "RS256",
      "n": "<base64url-modulus>",
      "e": "AQAB"
    },
    {
      "kty": "EC",
      "use": "sig",
      "kid": "<base64url-jwk-thumbprint>",
      "alg": "ES256",
      "crv": "P-256",
      "x": "<base64url-x-coordinate>",
      "y": "<base64url-y-coordinate>"
    }
  ]
}
```

**关键行为**：
- **双算法**：同时发布 RS256（RSA-2048）和 ES256（EC P-256）公钥
- **双 kid**：密钥轮换期间，每种算法同时发布 current + previous 两个 kid，确保轮换窗口内旧 `id_token` 仍可验签
- **kid 生成**：RFC 7638 JWK Thumbprint（SHA-256 哈希，base64url 编码）
- **Cache-Control**: `public, max-age=3600`
- **自动轮换**：默认 90 天轮一次，grace period 24h

### RP 验签流程

1. 从 `id_token` header 取 `kid` 和 `alg`
2. 从 JWKS `keys` 数组中找 `kid` 匹配且 `alg` 一致的公钥
3. 用对应算法验签（RS256 用 RSA 公钥，ES256 用 EC 公钥）
4. **绝不**接受 `alg=none` 或不在 `id_token_signing_alg_values_supported` 中的算法

---

## 5. 授权端点（`/oauth/authorize`）

### 请求参数

| 参数 | 类型 | 必需 | 说明 |
|---|---|---|---|
| `response_type` | string | ✅ | 固定 `code` |
| `client_id` | string | ✅ | 注册的 client_id |
| `redirect_uri` | string | ✅ | 必须在 client 注册的 `redirect_uris` 白名单内 |
| `scope` | string | ✅ | 空格分隔的 scope 列表，必须含 `openid` 才触发 id_token |
| `state` | string | ✅**推荐** | 随机字符串，防 CSRF，回调时原样返回 |
| `code_challenge` | string | ✅ | PKCE code_challenge（S256） |
| `code_challenge_method` | string | ✅ | 固定 `S256`（服务器强制） |
| `nonce` | string | 推荐 | 防 id_token 重放，回填到 id_token |
| `prompt` | string | 可选 | `none`（静默认证）/ `login` / `consent` / `select_account` |
| `claims` | string | 可选 | OIDC Core §5.5 Claims Parameter（JSON） |
| `request` | string | 可选 | OIDC Core §6 Request Object（JWT） |
| `request_uri` | string | 可选 | OIDC Core §6.3 远程 Request Object URL |

### PKCE 生成（各语言参考）

**JavaScript / TypeScript**：
```typescript
import crypto from 'crypto'

function generatePKCE() {
  const verifier = crypto.randomBytes(32)
    .toString('base64url')  // 43 chars, no padding
  const challenge = crypto.createHash('sha256')
    .update(verifier)
    .digest('base64url')
  return { verifier, challenge, method: 'S256' }
}
```

**Dart / Flutter**：
```dart
import 'dart:convert';
import 'dart:math';
import 'package:crypto/crypto.dart';

({String verifier, String challenge}) generatePKCE() {
  final random = Random.secure();
  final bytes = List<int>.generate(32, (_) => random.nextInt(256));
  final verifier = base64Url.encode(bytes).replaceAll('=', '');
  final digest = sha256.convert(utf8.encode(verifier));
  final challenge = base64Url.encode(digest.bytes).replaceAll('=', '');
  return (verifier: verifier, challenge: challenge);
}
```

**Python**：
```python
import base64, hashlib, secrets

def generate_pkce():
    verifier = secrets.token_urlsafe(32)[:43]
    challenge = base64.urlsafe_b64encode(
        hashlib.sha256(verifier.encode()).digest()
    ).rstrip(b'=').decode()
    return verifier, challenge
```

**Rust**：
```rust
use base64::engine::{general_purpose::URL_SAFE_NO_PAD, Engine};
use rand::Rng;
use sha2::{Sha256, Digest};

fn generate_pkce() -> (String, String) {
    let verifier: Vec<u8> = (0..32).map(|_| rand::thread_rng().gen()).collect();
    let verifier_b64 = URL_SAFE_NO_PAD.encode(&verifier);
    let challenge = URL_SAFE_NO_PAD.encode(Sha256::digest(verifier_b64.as_bytes()));
    (verifier_b64, challenge)
}
```

### 成功响应

浏览器 302 重定向到：
```
{redirect_uri}?code={authorization_code}&state={state}
```

### 错误响应

**redirect_uri 有效时**（302 回调）：
```
{redirect_uri}?error={error_code}&error_description={desc}&state={state}
```

**redirect_uri 无效时**（内联 HTML 错误页，防 open redirect）：
```html
HTTP 400
Content-Type: text/html; charset=utf-8
<!-- 显示 Sakrylle 品牌错误页，不跳转 -->
```

---

## 6. Token 端点（`/oauth/token`）

### 6.1 Authorization Code 换 Token

```bash
curl -X POST https://sub.sakrylle.com/oauth/token \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=authorization_code" \
  -d "code={authorization_code}" \
  -d "redirect_uri={redirect_uri}" \
  -d "client_id={client_id}" \
  -d "code_verifier={code_verifier}"
```

**机密 client 认证方式**（二选一）：
```bash
# 方式 1: HTTP Basic Auth（推荐）
curl -X POST https://sub.sakrylle.com/oauth/token \
  -u "{client_id}:{client_secret}" \
  -d "grant_type=authorization_code&code=..."

# 方式 2: POST body
curl -X POST https://sub.sakrylle.com/oauth/token \
  -d "grant_type=authorization_code" \
  -d "client_id={client_id}" \
  -d "client_secret={client_secret}" \
  -d "code=..."
```

### 成功响应

```json
{
  "access_token": "sk_oauth_aBcDeFgHiJkLmNoPqRsTuVwXyZ012345",
  "token_type": "Bearer",
  "expires_in": 86400,
  "scope": "openid profile email models:read",
  "refresh_token": "rt_xYzAbCdEfGhIjKlMnOpQrStUvWxYz012345",
  "refresh_token_expires_in": 2592000,
  "id_token": "eyJhbGciOiJSUzI1NiIsImtpZCI6Ii4uLiJ9.eyJpc3MiOiJodHRwczovL3N1Yi5zYWtyeWxsZS5jb20iLCJzdWIiOiIxMjMiLCJhdWQiOlsic2FrcnlsbGUtaW1hZ2UtcGxheWdyb3VuZCJdLCJleHAiOjE3MTc2ODAwMDAsImlhdCI6MTcxNzU5MzYwMCwibm9uY2UiOiJhYmMxMjMiLCJuYW1lIjoiYXJpZWwiLCJwcmVmZXJyZWRfdXNlcm5hbWUiOiJhcmllbCIsImVtYWlsIjoiYXJpZWxAc2FrcnlsbGUuY29tIiwiZW1haWxfdmVyaWZpZWQiOnRydWUsImF0X2hhc2giOiIuLi4ifQ.signature",
  "group": {
    "id": 5,
    "name": "GPT-Image"
  }
}
```

**字段说明**：

| 字段 | 说明 |
|---|---|
| `access_token` | opaque token，前缀 `sk_oauth_`，32 字节 base64url |
| `token_type` | 固定 `Bearer` |
| `expires_in` | access_token 有效期（秒），默认 86400（24h） |
| `scope` | 实际授予的 scope（空格分隔） |
| `refresh_token` | 前缀 `rt_`，仅当 scope 含 `offline_access` 时返回 |
| `refresh_token_expires_in` | refresh_token 有效期（秒），默认 2592000（30 天） |
| `id_token` | **仅当 scope 含 `openid` 时返回**，RS256 或 ES256 签名 JWT |
| `group` | 当前绑定的 group 信息（仅当 client 配置了 `default_group_id`） |
| `additional_tokens` | 多 group token（可选，当 client 请求多 group 时） |

### 6.2 Refresh Token 续期

```bash
curl -X POST https://sub.sakrylle.com/oauth/token \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=refresh_token" \
  -d "refresh_token={refresh_token}" \
  -d "client_id={client_id}"
```

**关键行为**：
- **Rotation**：每次刷新返回**新的** refresh_token，旧的立即失效
- **Replay 检测**：如果旧 refresh_token 被重复使用，**整个 token 家族**全部撤销（`token_family_id` 级联撤销）
- **RP 必须**：用响应中的新 refresh_token 覆盖本地存储的旧值
- **可选参数**：`group_id`（切换 group，仅 refresh grant 支持）

### 6.3 错误响应

```json
{
  "error": "invalid_grant",
  "error_description": "authorization code has expired",
  "error_uri": "https://doc.sakrylle.com/developers/oauth/errors#invalid_grant"
}
```

**常见错误码**：

| HTTP | error | 触发条件 |
|---|---|---|
| 400 | `invalid_request` | 缺少必需参数、Content-Type 不对、group_id 用在 auth_code grant |
| 400 | `invalid_grant` | code 过期/已用/不匹配、refresh_token 无效/已轮换、PKCE 验证失败 |
| 400 | `invalid_client` | client_id 不存在或已禁用 |
| 401 | `invalid_client` | client_secret 不匹配（机密 client） |
| 400 | `unsupported_grant_type` | grant_type 不是 `authorization_code`/`refresh_token`/`device_code` |
| 400 | `invalid_scope` | 请求的 scope 不在 client 的 `allowed_scopes` 内 |
| 503 | `temporarily_unavailable` | OAuth provider 被禁用 |

---

## 7. Token 吊销（`/oauth/revoke`）

```bash
curl -X POST https://sub.sakrylle.com/oauth/revoke \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "token={refresh_token_or_access_token}" \
  -d "client_id={client_id}"
```

| 参数 | 必需 | 说明 |
|---|---|---|
| `token` | ✅ | 要吊销的 token（access_token 或 refresh_token 均可） |
| `token_type_hint` | 可选 | `access_token` 或 `refresh_token`（加速查找） |
| `client_id` | 可选 | 公共 client 需要 |
| `client_secret` | 可选 | 机密 client 需要 |

**响应**：成功返回 `200`（即使 token 已不存在也返回 200，幂等）。

---

## 8. Token 内省（`/oauth/introspect`）

仅**机密 client**（`client_confidential=true`）可调用。

```bash
curl -X POST https://sub.sakrylle.com/oauth/introspect \
  -u "{client_id}:{client_secret}" \
  -d "token={access_token}"
```

**活跃 token 响应**：
```json
{
  "active": true,
  "scope": "openid profile email models:read",
  "client_id": "sakrylle-web",
  "token_type": "Bearer",
  "sub": "123",
  "exp": 1717680000,
  "iat": 1717593600,
  "iss": "https://sub.sakrylle.com"
}
```

**非活跃 token 响应**：
```json
{
  "active": false
}
```

---

## 9. id_token 结构

### 9.1 完整 Claims 列表

| Claim | 来源 | 条件 | 说明 |
|---|---|---|---|
| `iss` | `settings.oauth_issuer` | 必含 | 固定 `https://sub.sakrylle.com` |
| `sub` | `user.ID` 字符串 | 必含 | pairwise client 时为 SHA-256 伪名 |
| `aud` | `client_id` | 必含 | **单元素 JSON 数组** `["sakrylle-image-playground"]` |
| `exp` | 签发时间 + TTL | 必含 | 默认 TTL = 1 小时 |
| `iat` | 签发时间 | 必含 | Unix 时间戳 |
| `nonce` | authorize 请求 | 当请求带 nonce 时 | 原样回填 |
| `auth_time` | 用户最近认证时间 | 当非零时 | Unix 时间戳 |
| `sid` | OIDC session ID | 当 client 配置 backchannel_logout_session_required 时 | 用于 logout token 关联 |
| `name` | `user.Username` | 当 scope 含 `profile` 时 | |
| `preferred_username` | `user.Username` | 当 scope 含 `profile` 时 | |
| `email` | `user.Email` | 当 scope 含 `email` 时 | |
| `email_verified` | `user.EmailVerified` | 当 scope 含 `email` 时 | 布尔值，来自 `users.email_verified` 列 |
| `at_hash` | access_token 哈希 | 当同时签发 access_token 时 | `left_half(Base64URL(SHA-256(access_token)))` |
| `c_hash` | authorization_code 哈希 | 当同时签发 authorization_code 时 | `left_half(Base64URL(SHA-256(code)))` |

### 9.2 解码后的 id_token payload 示例

```json
{
  "iss": "https://sub.sakrylle.com",
  "sub": "123",
  "aud": ["sakrylle-image-playground"],
  "exp": 1717680000,
  "iat": 1717593600,
  "nonce": "n-0S6_WzA2Mj",
  "auth_time": 1717593600,
  "name": "ariel",
  "preferred_username": "ariel",
  "email": "ariel@sakrylle.com",
  "email_verified": true,
  "at_hash": "MTIzNDU2Nzg5MDEyMzQ1Ng",
  "c_hash": "ODkwMTIzNDU2Nzg5MDEyMw"
}
```

### 9.3 Claims 安全护栏

`BuildIDTokenClaims` 有 fail-closed 纵深防御：`assertNoForbiddenClaims` 会检查 id_token 中是否出现以下商业 claim，一旦发现**拒绝签发**：

```
balance, group, group_id, rate_multiplier, quota, quota_used,
daily_limit_usd, model_mapping, models, restrict_models,
capabilities, allowed_groups
```

这些字段只能通过 `/v1/me` 实时查询，**绝不**进入 id_token。

### 9.4 签名算法选择

- 每个 client 在 `oauth_clients.signing_algorithm` 配置（`RS256` 或 `ES256`）
- 默认 `RS256`
- 空值/未知值安全回退 `RS256`
- `id_token` header 包含 `kid`，RP 按 kid 从 JWKS 取对应公钥

---

## 10. UserInfo 端点

### 10.1 `/userinfo`（OIDC 标准端点）

```bash
curl https://sub.sakrylle.com/userinfo \
  -H "Authorization: Bearer sk_oauth_xxx"
```

**关键行为**：
- 只接受 `sk_oauth_` 前缀的 OAuth token，手动 API Key 返回 401
- **不检查**余额、配额、订阅状态、group 分配（身份端点，非计费端点）
- scope 含 `openid` 时返回顶层 `sub`
- 支持 `Accept: application/jwt` 返回签名 JWT 响应（RS256/ES256，TTL 5 分钟）

**JSON 响应**（当 scope 含 `openid` 时）：
```json
{
  "sub": "123",
  "name": "ariel",
  "preferred_username": "ariel",
  "email": "ariel@sakrylle.com",
  "email_verified": true
}
```

### 10.2 `/v1/me`（兼 API 的 UserInfo）

```bash
curl https://sub.sakrylle.com/v1/me \
  -H "Authorization: Bearer sk_oauth_xxx"
```

**双模式行为**：

| 模式 | token 类型 | 行为 |
|---|---|---|
| OAuth | `sk_oauth_` 前缀 | 按 scope 裁剪字段 |
| 手动 API Key | 无前缀 | 返回完整账户/group 视图 |

**OAuth 模式下的 scope → 字段映射**：

| Scope | 返回的字段 |
|---|---|
| `openid` | `sub`（顶层，用户 ID 字符串） |
| `profile` | `name`、`preferred_username`（顶层） |
| `email` | `email`、`email_verified`（顶层） |
| `profile:read` | `user.{id, username, display_name, avatar_url, locale}` |
| `email:read` | `user.email` |
| `account:balance:read` | `account.credit_remaining`、`currency_display`、`currency_symbol` |
| `account:read` | `account` + `current_group` + `allowed_groups` + `granted_scopes` + `effective_capabilities` |

**完整 OAuth 响应示例**：
```json
{
  "auth_type": "oauth",
  "sub": "123",
  "name": "ariel",
  "preferred_username": "ariel",
  "email": "ariel@sakrylle.com",
  "email_verified": true,
  "user": {
    "id": 123,
    "username": "ariel",
    "display_name": "ariel",
    "avatar_url": null,
    "locale": "zh-CN"
  },
  "account": {
    "credit_remaining": 8.94,
    "currency_display": "CNY",
    "currency_symbol": "￥"
  },
  "oauth": {
    "client_id": "sakrylle-image-playground",
    "app_type": "image",
    "grant_id": "uuid-...",
    "device_id": null,
    "device_name": null,
    "expires_at": "2026-06-06T12:00:00Z"
  },
  "granted_scopes": ["openid", "profile", "email", "images:create", "models:read"],
  "effective_capabilities": {
    "images_create": true
  }
}
```

---

## 11. Device Authorization Flow（RFC 8628）

适用于无浏览器环境（CLI、SSH 远程）。

### 步骤 1：请求设备码

```bash
curl -X POST https://sub.sakrylle.com/oauth/device/code \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "client_id=sakrylle-cli" \
  -d "scope=openid profile email models:read responses:create offline_access"
```

**响应**：
```json
{
  "device_code": "GmRhmhcxhwAzkoEqiMEg_DnyEysNkuNhszIySk9eS",
  "user_code": "SKRY-NEQF-WLGXN",
  "verification_uri": "https://sub.sakrylle.com/oauth/device",
  "verification_uri_complete": "https://sub.sakrylle.com/oauth/device?user_code=SKRY-NEQF-WLGXN",
  "expires_in": 600,
  "interval": 5
}
```

### 步骤 2：显示给用户

CLI 打印：
```
请在浏览器中打开以下链接完成授权：

  https://sub.sakrylle.com/oauth/device?user_code=SKRY-NEQF-WLGXN

用户码：SKRY-NEQF-WLGXN
（5 分钟内有效）
```

### 步骤 3：轮询 Token

```bash
# 每 {interval} 秒轮询一次
curl -X POST https://sub.sakrylle.com/oauth/token \
  -d "grant_type=urn:ietf:params:oauth:grant-type:device_code" \
  -d "device_code={device_code}" \
  -d "client_id=sakrylle-cli"
```

**轮询状态**：

| 响应 | 含义 | 处理 |
|---|---|---|
| `200 + token JSON` | 授权成功 | 存储 token，停止轮询 |
| `400 + error=authorization_pending` | 用户尚未授权 | 继续轮询 |
| `400 + error=slow_down` | 轮询太频繁 | 增加 interval + 5 秒 |
| `400 + error=expired_token` | 设备码过期 | 重新请求设备码 |
| `400 + error=access_denied` | 用户拒绝 | 终止流程 |

---

## 12. RP-Initiated Logout

```bash
# GET 方式
curl "https://sub.sakrylle.com/oauth/logout?\
id_token_hint={id_token}&\
post_logout_redirect_uri=https://image.sakrylle.com/"

# POST 方式
curl -X POST https://sub.sakrylle.com/oauth/logout \
  -d "id_token_hint={id_token}" \
  -d "post_logout_redirect_uri=https://image.sakrylle.com/"
```

| 参数 | 必需 | 说明 |
|---|---|---|
| `id_token_hint` | 推荐 | 之前签发的 id_token，用于标识用户会话 |
| `post_logout_redirect_uri` | 可选 | 必须在 client 的 `logout_redirect_uris` 白名单内 |

**行为**：
- `post_logout_redirect_uri` 在白名单内 → 302 重定向
- `post_logout_redirect_uri` 不在白名单内 → 内联 HTML（不当 open redirector）
- 无 `post_logout_redirect_uri` → 内联 HTML 确认页

---

## 13. Scope 参考

### OIDC 标准 Scope

| Scope | 说明 | 触发的 id_token claims |
|---|---|---|
| `openid` | **必需**，触发 id_token 签发 | `sub` |
| `profile` | 用户资料 | `name`、`preferred_username` |
| `email` | 用户邮箱 | `email`、`email_verified` |
| `offline_access` | 签发 refresh_token | — |

### Sakrylle 商业 Scope

| Scope | 说明 | 可调用的端点 |
|---|---|---|
| `profile:read` | 用户基本信息 | `GET /v1/me` |
| `email:read` | 用户邮箱 | `GET /v1/me` |
| `account:read` | 账户信息 + group | `GET /v1/me` |
| `account:balance:read` | 余额 | `GET /v1/me`、`GET /v1/account/balance` |
| `models:read` | 模型列表 | `GET /v1/models` |
| `chat.completions:create` | 聊天补全 | `POST /v1/chat/completions` |
| `responses:create` | Responses API | `POST /v1/responses`、`POST /v1/codex/responses` |
| `messages:create` | Messages API | `POST /v1/messages` |
| `images:create` | 图像生成 | `POST /v1/images/generations`、`POST /v1/images/edits` |
| `usage:read` | 用量统计 | `GET /v1/usage` |

### Legacy Alias

| 旧名 | 新名 |
|---|---|
| `image_generation` | `images:create` |
| `balance:read` | `account:balance:read` |

### Scope Enforcement

- `settings.oauth_scope_enforcement_enabled` 控制（默认 `false`）
- `false` = `sk_oauth_` token 跳过 scope 检查（过渡态）
- `true` = 严格按 scope 矩阵校验
- 开启是**生产决策**，需审批

---

## 14. Client 注册参考

### 当前已注册 Client（2026-06-11 更新）

> **2026-06-11 客户端清理**：生产 OIDC 仅保留 `sakrylle-image-playground` 与 `sakrylle-web`。`sakrylle-cli` / `sakrylle-desktop` / `sakrylle-chat` / `sakrylle-image-playground-v2` 的注册（及其历史 access/refresh/code token）已删除，待对应产品实际开发时按下方「未来待注册」形状重建。

| 字段 | sakrylle-image-playground | sakrylle-web |
|---|---|---|
| **client_type** | public | **confidential** |
| **app_type** | image | web |
| **pkce_required** | true | true（机密 client 仍强制 PKCE S256） |
| **client_confidential** | false | true |
| **trusted_first_party** | true | true |
| **device_flow_enabled** | false | false |
| **redirect_uris** | `image.sakrylle.com/oauth/callback`, `localhost:5173` | `https://chat.sakrylle.com/oauth/oidc/login/callback`(+ `/oauth/oidc/callback`)；`http://localhost:3080/...`（两变体，遗留）；`http://localhost:8080/oauth/oidc/login/callback` |
| **allowed_scopes** | `images:create`, `chat.completions:create`, `account:balance:read`, `models:read`, `offline_access` | `openid`, `email`, `profile`, `models:read`, `chat.completions:create`, `responses:create`, `messages:create`, `usage:read`, `account:read`, `offline_access` |
| **logout_redirect_uris** | — | `https://chat.sakrylle.com/` |
| **signing_algorithm** | RS256 | RS256 |
| **subject_type** | public | public |

> `sakrylle-web` 已注册并启用：机密 client，token endpoint auth `client_secret_post`（`client_secret_basic` 亦可），PKCE S256 强制。client_secret 于 2026-06-11 轮换，明文仅交付部署方、不入库/不入仓库。RP 接入形状见 §15「Sakrylle Web」。

### 未来待注册 Client（产品开发期重建，需审批）

> 2026-06-11 清理后已删除，待对应产品开发时按此形状重新注册：

| 产品 | client_id | 类型 | redirect_uris | scope |
|---|---|---|---|---|
| CLI | `sakrylle-cli` | public | `http://127.0.0.1`（任意端口 `/callback`）+ Device Flow | `openid profile email models:read responses:create messages:create usage:read offline_access` |
| Studio | 首发复用 CLI 凭据 | — | — | — |
| Chat | `sakrylle-chat` | public | `sakrylle-chat://oauth/callback`（自定义 scheme）；`http://127.0.0.1`（loopback，RFC 8252 §7.3 端口无关匹配） | `openid profile email models:read chat.completions:create offline_access` |

---

## 15. 各产品接入速查表

### Sakrylle Image（SPA，已有 OAuth PKCE）

| 项 | 值 |
|---|---|
| client_id | `sakrylle-image-playground` |
| client_type | public（SPA） |
| grant_type | `authorization_code` + `refresh_token` |
| redirect_uri | `https://image.sakrylle.com/oauth/callback` |
| scope | `openid profile email images:create account:balance:read models:read offline_access` |
| 签名算法 | RS256（默认） |
| 身份来源 | id_token claims（`sub`/`name`/`email`）+ `/v1/me` 实时取 balance |
| 特殊注意 | SPA 无法安全验签 id_token，仅解析 payload；refresh_token 存 localStorage（已知风险） |

### Sakrylle CLI（Rust + Node，无浏览器环境）

| 项 | 值 |
|---|---|
| client_id | `sakrylle-cli` |
| client_type | public |
| grant_type | `authorization_code`（loopback 主）+ `device_code`（降级）+ `refresh_token` |
| redirect_uri | `http://127.0.0.1:{random_port}/callback`（通配 loopback） |
| scope | `openid profile email models:read responses:create messages:create usage:read offline_access` |
| 签名算法 | RS256（默认） |
| 特殊注意 | Device Flow 端点路径与上游 codex 不兼容，需改；`requireGroupAnthropic` 限制 `/v1/responses` 需 Anthropic group key |

### Sakrylle Studio（Tauri 桌面，首发复用 CLI 凭据）

| 项 | 值 |
|---|---|
| 首发认证 | 只读 `~/.sakrylle-cli/auth.json`，不独立持有 OIDC token |
| 增强（后置） | 独立 OIDC loopback 登录，client_id = `sakrylle-studio` |
| 特殊注意 | 依赖 CLI app-server JSON-RPC 协议兼容性 |

### Sakrylle Web（open-webui，FastAPI 后端）

| 项 | 值 |
|---|---|
| client_id | `sakrylle-web` |
| client_type | **confidential**（有后端，可安全存储 client_secret） |
| grant_type | `authorization_code` + `refresh_token` |
| redirect_uri | `https://chat.sakrylle.com/oauth/oidc/login/callback` |
| scope | `openid profile email models:read offline_access ...` |
| 签名算法 | RS256 |
| 特殊注意 | open-webui authlib 用 `server_metadata_url` 做 discovery；email/username claim 需 flat `get('email')` 匹配 |

### Sakrylle Chat（Flutter 移动/桌面）

| 项 | 值 |
|---|---|
| client_id | `sakrylle-chat` |
| client_type | public |
| grant_type | `authorization_code` + `refresh_token` |
| redirect_uri | `sakrylle-chat://oauth/callback`（Android/iOS/macOS 自定义 scheme）；`http://127.0.0.1`（Windows/Linux loopback，端口运行时动态分配，须 RFC 8252 §7.3 端口无关匹配） |
| scope | `openid profile email models:read chat.completions:create offline_access` |
| 签名算法 | RS256 |
| 特殊注意 | 需 `flutter_web_auth_2`；iOS/Android/macOS 需注册自定义 URL scheme；token 存 `flutter_secure_storage` |

---

## 16. 实现检查清单

每个 RP 的开发 Agent 应按以下清单逐项实现：

### 基础（所有 RP 共通）

- [ ] 从 `/.well-known/openid-configuration` 拉取 discovery（带超时 + 缓存 + fail-safe 回退）
- [ ] 实现 PKCE：生成 `code_verifier`（32 字节随机）+ `code_challenge`（S256）
- [ ] 生成 `state`（16+ 字节随机，防 CSRF）
- [ ] 生成 `nonce`（可选但推荐，防 id_token 重放）
- [ ] 构造 authorize URL 并跳转浏览器
- [ ] 处理回调：校验 `state`，用 `code` + `code_verifier` 换 token
- [ ] 存储 `access_token` + `refresh_token`（平台安全存储）
- [ ] 解析 id_token payload（base64url 解码第二段）取 `sub`/`name`/`email`
- [ ] 校验 id_token 的 `iss` == `https://sub.sakrylle.com`、`aud` 含本 client_id、`exp` 未过期
- [ ] 校验 `nonce`（如果发了的话）

### Token 生命周期

- [ ] `refresh_token` 续期：检测响应中新的 `refresh_token` 并覆盖本地旧值
- [ ] 401 `invalid_token` 时触发 `forceRefreshToken`
- [ ] 刷新失败 → 自动 logout（清本地 token）
- [ ] `logout`：调 `/oauth/revoke` 吊销 + 清本地存储

### 安全

- [ ] PKCE S256 强制（不要用 plain）
- [ ] `state` 不匹配 → 拒绝整个回调
- [ ] 不接受 `alg=none` 或未注册的签名算法
- [ ] access_token 存平台安全存储（Keychain / Keystore / flutter_secure_storage）
- [ ] 不把 access_token 或 refresh_token 写入 console.log / 日志

### 降级

- [ ] discovery 失败 → 回退到硬编码端点
- [ ] server 未返回 id_token → 静默降级到 `/v1/me` 取身份
- [ ] nonce 校验失败 → `console.warn` + 降级，不阻断登录

---

## 17. curl 端到端示例

### 完整 Authorization Code + PKCE 流程

```bash
# 1. 生成 PKCE
VERIFIER=$(openssl rand -base64 32 | tr -d '=' | tr '/+' '_-')
CHALLENGE=$(echo -n "$VERIFIER" | openssl dgst -sha256 -binary | base64 | tr -d '=' | tr '/+' '_-')
STATE=$(openssl rand -hex 16)

# 2. 浏览器打开 authorize URL（手动）
echo "打开浏览器访问："
echo "https://sub.sakrylle.com/oauth/authorize?\
response_type=code&\
client_id=sakrylle-image-playground&\
redirect_uri=https://image.sakrylle.com/oauth/callback&\
scope=openid+profile+email+images:create+account:balance:read+models:read+offline_access&\
state=$STATE&\
code_challenge=$CHALLENGE&\
code_challenge_method=S256"

# 3. 用户授权后，从回调 URL 取 code（手动）
CODE="从回调 URL 的 ?code= 参数中复制"

# 4. 用 code 换 token
curl -X POST https://sub.sakrylle.com/oauth/token \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=authorization_code" \
  -d "code=$CODE" \
  -d "redirect_uri=https://image.sakrylle.com/oauth/callback" \
  -d "client_id=sakrylle-image-playground" \
  -d "code_verifier=$VERIFIER"

# 5. 用 access_token 调用 API
curl https://sub.sakrylle.com/v1/models \
  -H "Authorization: Bearer sk_oauth_xxx"

# 6. 用 access_token 获取 UserInfo
curl https://sub.sakrylle.com/v1/me \
  -H "Authorization: Bearer sk_oauth_xxx"

# 7. 刷新 token
curl -X POST https://sub.sakrylle.com/oauth/token \
  -d "grant_type=refresh_token" \
  -d "refresh_token=rt_xxx" \
  -d "client_id=sakrylle-image-playground"

# 8. 登出
curl -X POST https://sub.sakrylle.com/oauth/revoke \
  -d "token=rt_xxx" \
  -d "client_id=sakrylle-image-playground"
```

---

> **本文档基于 sub2api 源码实际实现编写**（2026-06-05）。所有端点路径、参数名、响应格式均来自 `backend/internal/handler/oauth_provider_handler.go`、`oauth_provider_account_handler.go`、`backend/internal/service/oauth_scopes.go`、`backend/internal/service/oidc_id_token.go` 的实际代码。如有疑问，以源码为准。
