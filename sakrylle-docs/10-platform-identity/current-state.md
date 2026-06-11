---
title: 02 · Sakrylle API — OAuth 2.0 现状报告
status: canonical
scope: platform
last_verified: 2026-06-06
---

# 02 · Sakrylle API — OAuth 2.0 现状报告

> 本文是 Sakrylle API（sub2api fork）OAuth 2.0 provider 能力的现状调研报告。
> 定位：面向开发者的技术参考，重点记录"已实现什么"和"距 OIDC 差什么"。
> OIDC 改造方案见 [03-sakrylle-api-oidc-architecture.md](./03-sakrylle-api-oidc-architecture.md)。

---

## 1. 调研范围

| 维度 | 说明 |
|---|---|
| 代码库 | `/Volumes/APFS_HD/Documents/Github/sub2api/` — Go 1.23，Gin，ent ORM |
| 数据库 | PostgreSQL 18，Redis 8 |
| 前端 | Vue 3 + Vite（嵌入为 SPA，embed_on.go 管理） |
| 核心文件 | 见第 3 节完整路径列表 |
| 已上线服务 | `https://sub.sakrylle.com`（主站）、`https://api.sakrylle.com`（网关） |
| 调研截止版本 | branch `theme/monet-purple`，最新 commit：`fb2a27a5` |
| 更新日期 | **2026-06-05** — OIDC Core 1.0 + 14 项 OIDC 可选能力已实现（含 Pairwise、Request Object、Back/Front-Channel Logout、Token Introspection、Claims Parameter 等） |

---

## 2. 关键结论

> **🎉 更新（2026-06-04）**：OIDC Core 1.0 已完整实现！以下结论已更新为最新状态。

1. **OAuth 2.0 Authorization Server 能力完整**：支持 Authorization Code + PKCE（S256 强制）、Refresh Token（rotation + 家族撤销）、RFC 8628 Device Flow、RFC 7009 Revocation、RFC 8414 Discovery（`/.well-known/oauth-authorization-server`）。

2. **access_token 是 opaque token，写入 `api_keys` 表**：前缀 `sk_oauth_`，与手动 API Key 共享同一条计费/限流/Redis 缓存链路，OAuth 层不需要改网关核心逻辑。

3. **用户会话 JWT 使用 HS256 对称签名**：密钥来自 `jwt.secret`（≥32 字节），适合服务内部验证。**OIDC id_token 使用 RS256/ES256 非对称签名**（独立密钥基础设施，见下文）。

4. ✅ **OIDC Core 1.0 已完整实现**（2026-06-04）：
   - ✅ `id_token` 签发（RS256 + ES256 双算法支持）
   - ✅ `/.well-known/openid-configuration` Discovery
   - ✅ `/.well-known/jwks.json` 公钥端点
   - ✅ `scope=openid/profile/email` 注册（migrations 149-151）
   - ✅ RS256/ES256 密钥基础设施（加密存储于 `security_secrets`）
   - ✅ `/v1/me` UserInfo 端点（返回 `sub` claim）
   - ✅ Nonce 支持（防重放攻击）
   - ✅ RP-Initiated Logout（`/oauth/logout`）
   - ✅ `prompt=none` 静默认证

5. ✅ **Scope enforcement 已开启（2026-06-05）**：`oauth_scope_enforcement_enabled` 已从 `false` 切为 `true`，sub2api 已重启。`sk_oauth_` token 现在严格按 scope 矩阵校验。新 RP 需确保 `allowed_scopes` 覆盖所有需调用的端点。

6. ✅ **`security_secrets` 表存储 OIDC 签名密钥**（migration 053）：RS256 和 ES256 私钥使用 AES-256-GCM 加密存储，KEK = **共享的 `TOTP_ENCRYPTION_KEY`**（`cfg.Totp.EncryptionKey`）；代码经 `repository.NewAESEncryptor` 解码此 env，**不读** `OIDC_KEY_ENCRYPTION_KEY`。该 KEK 同时保护 TOTP / channel-monitor / backup secrets，未设则启动自动生成（重启即失效）。

---

## 3. 相关文件路径

### 3.1 路由注册

| 文件 | 关键行 | 说明 |
|---|---|---|
| `backend/internal/server/routes/oauth.go` | 57 | `GET /.well-known/oauth-authorization-server` |
| `backend/internal/server/routes/oauth.go` | 59-60 | ✅ **`GET /.well-known/openid-configuration` + `/.well-known/jwks.json`**（OIDC Discovery + JWKS） |
| `backend/internal/server/routes/oauth.go` | 64–65 | `GET/POST /oauth/authorize` |
| `backend/internal/server/routes/oauth.go` | 66-67 | ✅ **`GET/POST /oauth/logout`**（RP-Initiated Logout） |
| `backend/internal/server/routes/oauth.go` | 69 | `POST /oauth/token` |
| `backend/internal/server/routes/oauth.go` | 78 | `POST /oauth/revoke` |
| `backend/internal/server/routes/oauth.go` | 96–97 | `POST /api/v1/oauth/authorize/begin|approve`（JWT 保护） |
| `backend/internal/server/routes/oauth.go` | 106–114 | `GET/DELETE /api/v1/oauth/authorized-apps` |
| `backend/internal/server/routes/oauth_device.go` | 54, 60 | RFC 8628 Device 路由 |
| `backend/internal/server/routes/oauth_device.go` | 72, 79 | Device approve/deny（JWT 保护） |

### 3.2 核心服务

| 文件 | 关键行 | 说明 |
|---|---|---|
| `backend/internal/service/oauth_provider_service.go` | 32–50 | `IssuedToken` / `AdditionalToken` 结构体定义 |
| `backend/internal/service/oauth_provider_service.go` | 117–119 | `oauthAccessTokenPrefix = "sk_oauth_"`, `oauthRefreshTokenPrefix = "rt_"` |
| `backend/internal/service/oauth_provider_service.go` | 1577–1636 | `mintTokensFromCode` — v2 原子写入路径 |
| `backend/internal/service/oauth_provider_service.go` | 812–820 | `RefreshAccessToken` 函数签名 |
| `backend/internal/service/oauth_provider_service.go` | 1922–1929 | `verifyPKCES256` — `subtle.ConstantTimeCompare` |
| `backend/internal/service/oauth_provider_service.go` | 601 | CSRF token `ConstantTimeCompare` |
| `backend/internal/service/oauth_provider_service.go` | 236–246 | `IsScopeEnforcementEnabled` — kill-switch 读取 |
| `backend/internal/service/oauth_scopes.go` | 10–22 | 11 个规范 scope 常量 |
| `backend/internal/service/oauth_scopes.go` | 25–37 | ✅ `canonicalScopes` map（**含 `openid`、`profile`、`email`** — migrations 149-151） |
| `backend/internal/service/oidc_key_service.go` | — | ✅ **OIDC 密钥服务**：RS256 + ES256 签名、密钥轮换、加密存储 |
| `backend/internal/service/oidc_id_token.go` | — | ✅ **id_token 构建**：OIDC 标准 claims + nonce + auth_time |
| `backend/internal/service/oauth_scopes.go` | 41–44 | `legacyScopeAliases`（`image_generation` → `images:create`，`balance:read` → `account:balance:read`） |
| `backend/internal/service/oauth_scopes.go` | 244–286 | `oauthScopePolicies` — §7.3 端点 scope 矩阵 |
| `backend/internal/service/oauth_device_service.go` | — | RFC 8628 Device Authorization Grant 实现 |
| `backend/internal/service/auth_service.go` | 1148–1178 | `GenerateToken`，`jwt.SigningMethodHS256`，HS256 签发 |
| `backend/internal/service/setting_service.go` | 703–774 | `GetOAuthIssuer`，issuer 解析优先级逻辑 |

### 3.3 Handler

| 文件 | 关键行 | 说明 |
|---|---|---|
| `backend/internal/handler/oauth_provider_handler.go` | 93–102 | `prompt=none` → `interaction_required` |
| `backend/internal/handler/oauth_provider_handler.go` | 362–376 | Token 端点 grant_type switch（仅 3 种） |
| `backend/internal/handler/oauth_provider_handler.go` | 615–647 | `Metadata()` — RFC 8414 discovery 文档内容 |
| `backend/internal/handler/oauth_provider_handler.go` | 628–642 | discovery JSON 字段，含 `userinfo_endpoint": "https://sub.sakrylle.com/userinfo`、`service_documentation` |
| `backend/internal/handler/oauth_provider_handler.go` | 649–674 | `discoveryIssuer()` — issuer 解析（3 级 fallback） |
| `backend/internal/handler/oauth_provider_account_handler.go` | 76–119 | `Me()` — `/v1/me` handler，双模式（OAuth/手动 key） |
| `backend/internal/handler/oauth_provider_account_handler.go` | 152–155 | scope → 响应字段映射注释 |
| `backend/internal/handler/oauth_provider_consent.go` | 326 | consent 页 `json.Encoder.SetEscapeHTML(true)` XSS 防护 |

### 3.4 中间件

| 文件 | 关键行 | 说明 |
|---|---|---|
| `backend/internal/server/middleware/oauth_scope.go` | 1–23 | Scope enforcement 总体说明，kill-switch 语义 |
| `backend/internal/server/middleware/oauth_scope.go` | 46 | `RequireOAuthScope` middleware |
| `backend/internal/web/embed_on.go` | 315–316 | SPA fallback bypass：`/oauth/` 和 `/.well-known/` |

### 3.5 数据库 Migrations

| 文件 | 说明 |
|---|---|
| `backend/migrations/143_oauth_provider.sql` | 初始 schema：`oauth_clients`、`oauth_codes`、`oauth_refresh_tokens` |
| `backend/migrations/145_oauth_v2.sql` | v2 扩展：`oauth_access_tokens`、`oauth_device_codes`、`oauth_authorize_transactions`；扩展 `oauth_clients` 字段；scope enforcement kill-switch seed |
| `backend/migrations/144_oauth_seed_sakrylle.sql` | `sakrylle-image-playground` client seed（legacy） |
| `backend/migrations/148_oauth_v2_sakrylle_seed.sql` | v2 client seed：`sakrylle-cli`、`sakrylle-desktop`、`sakrylle-image-playground-v2`；`oauth_issuer` 设置 seed |
| `backend/migrations/149_oidc_scopes.sql` | ✅ **OIDC scope 注册**：`openid`、`profile`、`email`、`offline_access` |
| `backend/migrations/150_grant_oidc_scopes.sql` | ✅ 授予第一方 clients OIDC scopes |
| `backend/migrations/151_oauth_client_signing_algorithm.sql` | ✅ `oauth_clients.signing_algorithm`（RS256/ES256 per-client 选择） |
| `backend/migrations/152_oidc_client_consistency.sql` | ✅ 第一方 client 一致性修复（`trusted_first_party`、OIDC scopes、logout URIs） |
| `backend/migrations/153_oidc_pairwise_subject.sql` | ✅ `oauth_clients.subject_type` + `sector_identifier_uri`（OIDC Core §8 Pairwise Subject） |
| `backend/migrations/154_oidc_claims_parameter.sql` | ✅ `oauth_authorize_transactions.claims`（OIDC Core §5.5 Claims Parameter） |
| `backend/migrations/155_oidc_request_uris.sql` | ✅ `oauth_clients.request_uris`（OIDC Core §6 Request Object / request_uri） |
| `backend/migrations/156_oidc_backchannel_logout.sql` | ✅ `oauth_clients.backchannel_logout_uri` + `backchannel_logout_session_required`（OIDC Back-Channel Logout 1.0） |
| `backend/migrations/157_oidc_email_verified.sql` | ✅ `users.email_verified`（per-user email 验证标志） |
| `backend/migrations/158_oidc_session_id.sql` | ✅ `oauth_authorize_transactions.sid` + `oauth_codes.sid`（OIDC Session ID） |
| `backend/migrations/159_oidc_consent_grants.sql` | ✅ `oauth_grants` 表（第三方客户端 consent 跟踪） |
| `backend/migrations/160_oidc_introspect_client_confidential.sql` | ✅ `oauth_clients.client_confidential`（RFC 7662 Token Introspection 权限控制） |
| `backend/migrations/161_oidc_frontchannel_logout.sql` | ✅ `oauth_clients.frontchannel_logout_uri`（OIDC Front-Channel Logout 1.0） |
| `backend/migrations/162_oauth_grant_chat_completions.sql` | ✅ 授予 `sakrylle-image-playground` 的 `chat.completions:create` scope（固化此前 psql 直改，幂等） |
| `backend/migrations/053_add_security_secrets.sql` | ✅ `security_secrets` 表（存储加密的 OIDC 签名密钥） |

---

## 4. 当前实现摘要

### 4.1 支持的 Grant Flow

| Grant | 规范 | 状态 | 备注 |
|---|---|---|---|
| Authorization Code + PKCE S256 | RFC 6749 + RFC 7636 | ✅ 完整 | PKCE 对所有 client 强制（`pkce_required=true` 默认） |
| Refresh Token | RFC 6749 §6 | ✅ 完整 | rotation + 家族撤销 + replay 检测（`reuse_detected_at`） |
| Device Authorization | RFC 8628 | ✅ 完整 | `device_flow_enabled` 开关控制 |
| Token Revocation | RFC 7009 | ✅ 完整 | `POST /oauth/revoke` |
| RP-Initiated Logout | OIDC Session §5 | ✅ **完整**（2026-06-04） | `GET/POST /oauth/logout` + redirect 验证 |
| Client Credentials | RFC 6749 §4.4 | ❌ 未实现 | Token handler switch 无此分支 |

### 4.2 Token 形态

```
access_token  = "sk_oauth_" + base64url(32字节 crypto/rand)
              → 写入 api_keys 表，expires_at 由 client.AccessTokenTTLSeconds 决定
              → 与手动 API Key 共享计费/Redis 缓存/限流管道

refresh_token = "rt_" + base64url(N字节 crypto/rand)
              → SHA-256 hex 写入 oauth_refresh_tokens.token_hash（明文仅返回一次）
              → rotation：每次刷新换新 token，旧 token 标记 rotated_to_hash
              → replay：检测到 token_family_id 复用 → 全家族撤销 + slog.Warn

用户会话 JWT = jwt.SigningMethodHS256（HS256）
              → payload：UserID, Email, Role, TokenVersion
              → 密钥：jwt.secret（env 或 security_secrets 表）
              → 仅用于服务内部验证（/api/v1/* 管理端点），不对外发布

id_token      = ✅ **RS256 或 ES256 签名的 JWT**（2026-06-04）
              → payload：iss, sub, aud, exp, iat, nonce（可选）, auth_time（可选）
              → 密钥：RSA-2048 或 EC-P256，加密存储于 security_secrets
              → 仅当 scope 包含 `openid` 时签发
              → 公钥通过 `/.well-known/jwks.json` 发布
```

### 4.3 oauth_clients Schema（完整字段）

**初始字段（migration 143）：**

| 字段 | 类型 | 说明 |
|---|---|---|
| `client_id` | VARCHAR(128) UNIQUE | 客户端标识符 |
| `name` | VARCHAR(200) | 显示名称 |
| `client_secret_hash` | TEXT | bcrypt hash，公共 client 为空 |
| `redirect_uris` | JSONB | 精确白名单数组 |
| `allowed_scopes` | JSONB | 该 client 允许请求的 scope 列表 |
| `pkce_required` | BOOLEAN DEFAULT TRUE | 是否强制 PKCE |
| `default_group_id` | BIGINT | 默认绑定的 API group |
| `access_token_ttl_seconds` | INT DEFAULT 86400 | access token 有效期（秒） |
| `refresh_token_ttl_seconds` | INT DEFAULT 2592000 | refresh token 有效期（秒，30天） |
| `disabled` | BOOLEAN DEFAULT FALSE | 是否禁用 |

**v2 新增字段（migration 145）：**

| 字段 | 类型 | 说明 |
|---|---|---|
| `client_type` | VARCHAR(32) DEFAULT 'public' | `public` / `confidential` |
| `app_type` | VARCHAR(32) DEFAULT 'unknown' | `cli` / `desktop` / `image` / `web` / `unknown` |
| `trusted_first_party` | BOOLEAN DEFAULT FALSE | 是否为 Sakrylle 自有第一方 client |
| `default_scopes` | JSONB DEFAULT '[]' | 授权时默认请求的 scope |
| `allowed_group_ids` | JSONB NULLABLE | client 级别 group 限制（NULL = 不限制） |
| `allowed_origins` | JSONB DEFAULT '[]' | CORS 允许的 origin |
| `logout_redirect_uris` | JSONB DEFAULT '[]' | RP-Initiated Logout 回调 URI（`/oauth/logout` 端点已实现，2026-06-04） |
| `device_flow_enabled` | BOOLEAN DEFAULT FALSE | 是否允许 Device Flow |
| `allow_refresh_without_offline_access` | BOOLEAN DEFAULT FALSE | 不要求 offline_access scope 也签发 refresh token |
| `icon_url` / `homepage_url` / `privacy_url` / `terms_url` | TEXT | 应用元信息（用于 consent 页展示） |

### 4.4 已注册的 Sakrylle 第一方 Client

> **2026-06-11 客户端清理**：生产仅保留 `sakrylle-image-playground` + `sakrylle-web`。`sakrylle-cli` / `sakrylle-desktop` / `sakrylle-chat` / `sakrylle-image-playground-v2` 注册及历史 token 已删除，待产品开发期重建。

| client_id | app_type | client_type | PKCE | Device Flow | redirect_uris | 关键 scope |
|---|---|---|---|---|---|---|
| `sakrylle-image-playground` | `image` | `public` | 强制 | 否 | `image.sakrylle.com/oauth/callback`, `localhost:5173` | `images:create`, `chat.completions:create`, `account:balance:read`, `models:read`, `offline_access`（`chat.completions:create` 见 migration 162） |
| `sakrylle-web` | `web` | `confidential` | 强制 | 否 | `https://chat.sakrylle.com/oauth/oidc/login/callback`(+`/oauth/oidc/callback`), `http://localhost:3080/...`（遗留）, `http://localhost:8080/oauth/oidc/login/callback` | `openid`, `email`, `profile`, `models:read`, `chat.completions:create`, `responses:create`, `messages:create`, `usage:read`, `account:read`, `offline_access`（`client_confidential=true`；secret 于 2026-06-11 轮换） |

> `default_group_id = NULL` for all clients — 由运营在 admin UI 或直接 SQL 按部署设置。

### 4.5 Scope 体系

**14 个规范 scope**（含 OIDC 标准 scope，2026-06-04 更新）：

```
# OIDC 标准 scope（migrations 149-150）
openid                    # 启用 OIDC，触发 id_token 签发
profile                   # OIDC 标准 profile claims（name, preferred_username）
email                     # OIDC 标准 email claims（email, email_verified）

# Sakrylle 商业 scope
profile:read              # 用户基本信息（username, display_name, avatar_url）
email:read                # 用户邮箱（email, email_verified）
account:read              # 账户信息 + 当前 group + 允许的 group 列表
account:balance:read      # 余额 + 货币显示
models:read               # /v1/models 模型列表
chat.completions:create   # /v1/chat/completions
responses:create          # /v1/responses/**
messages:create           # /v1/messages/**
images:create             # /v1/images/generations + /v1/images/edits
usage:read                # /v1/usage 用量统计
offline_access            # 签发 refresh token（RFC 8628 同义）
```

**Legacy alias（`oauth_scopes.go:41–44`）：**
- `image_generation` → `images:create`
- `balance:read` → `account:balance:read`

**Scope enforcement kill-switch：**
- `settings.oauth_scope_enforcement_enabled` 默认 `false`（migration 145 seed）
- `false` = `sk_oauth_` token 跳过 scope 检查，可访问所有路由
- `middleware/oauth_scope.go` 的 3 个 middleware 均检查此 kill-switch

### 4.6 Discovery 文档

#### 4.6.1 OAuth 2.0 Discovery（`/.well-known/oauth-authorization-server`）

handler 实际返回的字段（`oauth_provider_handler.go:643–665`）：

```json
{
  "issuer": "https://sub.sakrylle.com",
  "authorization_endpoint": "https://sub.sakrylle.com/oauth/authorize",
  "token_endpoint": "https://sub.sakrylle.com/oauth/token",
  "revocation_endpoint": "https://sub.sakrylle.com/oauth/revoke",
  "device_authorization_endpoint": "https://sub.sakrylle.com/oauth/device/code",
  "userinfo_endpoint": "https://sub.sakrylle.com/userinfo",
  "response_types_supported": ["code"],
  "response_modes_supported": ["query"],
  "ui_locales_supported": ["zh-CN", "en"],
  "grant_types_supported": ["authorization_code", "refresh_token", "urn:ietf:params:oauth:grant-type:device_code"],
  "code_challenge_methods_supported": ["S256"],
  "token_endpoint_auth_methods_supported": ["none", "client_secret_basic", "client_secret_post"],
  "scopes_supported": ["account:balance:read", "account:read", "chat.completions:create", "email:read", "images:create", "messages:create", "models:read", "offline_access", "profile:read", "responses:create", "usage:read"],
  "service_documentation": "https://doc.sakrylle.com/developers/oauth/"
}
```

#### 4.6.2 OIDC Discovery（`/.well-known/openid-configuration`）✅ 新增（2026-06-04，2026-06-05 扩展）

handler 实际返回的字段（`oauth_provider_handler.go`）：

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
  "subject_types_supported": ["public", "pairwise"],
  "id_token_signing_alg_values_supported": ["RS256", "ES256"],
  "userinfo_signing_alg_values_supported": ["RS256", "ES256"],
  "request_parameter_supported": true,
  "request_uri_parameter_supported": true,
  "claims_parameter_supported": true,
  "scopes_supported": ["openid", "profile", "email", "offline_access", "account:read", "account:balance:read", "models:read", ...],
  "token_endpoint_auth_methods_supported": ["none", "client_secret_basic", "client_secret_post"],
  "claims_supported": ["sub", "iss", "aud", "exp", "iat", "auth_time", "nonce", "sid", "name", "preferred_username", "email", "email_verified", "at_hash", "c_hash"]
}
```

### 4.7 Issuer 解析优先级

`discoveryIssuer()` 按以下顺序解析（`setting_service.go:713–730`）：

1. `settings.oauth_issuer`（规范值，migration 148 中 seed 为 `https://sub.sakrylle.com`）
2. `settings.frontend_url`（legacy fallback）
3. 请求的 `scheme://Host`（最后兜底，部署异常时不稳定）

X-Forwarded-Proto allowlist 限制为 `{http, https}`，防止恶意 proxy header 注入 issuer URL。

### 4.8 `/v1/me` 端点（OIDC UserInfo）

**路由**：`GET /v1/me`（`gateway.go:84`，handler：`oauth_provider_account_handler.go:90`）

**双模式行为（`oauth_provider_account_handler.go:76–119`）：**

- **OAuth token（`sk_oauth_` 前缀）**：✅ **OIDC 合规**（2026-06-04）— 当 scope 包含 `openid` 时返回 `sub` claim（用户 ID 字符串），并按 OIDC 标准 scope（`profile`、`email`）和商业 scope（`profile:read`、`email:read`、`account:read`、`account:balance:read`）裁剪响应字段。
- **手动 API Key**：不做 scope 限制，返回完整账户/group 视图（等同旧 `/v1/account/balance`），不返回 `sub` claim。

**scope → 响应字段映射（`handler:198–207` 更新）：**

```
openid                    → sub（用户 ID 字符串，OIDC 必需）
profile（OIDC 标准）       → name, preferred_username
email（OIDC 标准）         → email, email_verified
profile:read              → user.{id, username, display_name, avatar_url, locale}
email:read                → user.email（叠加在 profile 之上）
account:balance:read      → account.credit_remaining + 货币显示
account:read              → account + current_group + allowed_groups + capability flags
```

### 4.9 安全要点（已实现）

| 机制 | 实现位置 | 说明 |
|---|---|---|
| PKCE S256 | `oauth_provider_service.go:1922–1929` | `subtle.ConstantTimeCompare`，全 client 强制 |
| CSRF token | `oauth_provider_service.go:601` | SHA-256 后存表，approve 时 `ConstantTimeCompare` 比对 |
| Refresh token rotation | `oauth_provider_service.go:892` | 每次刷新换新 token，旧 token 写入 `rotated_to_hash` |
| Replay 检测 + 家族撤销 | `oauth_provider_service.go:1015–1032` | 检测到复用 → 全 `token_family_id` 撤销 + `reuse_detected_at` 打点 + `slog.Warn` |
| Consent XSS 防护 | `oauth_provider_consent.go:326` | `json.Encoder.SetEscapeHTML(true)` 阻止 `</script>` 逃逸 |
| Redirect URI 精确匹配 | `oauth_clients.redirect_uris JSONB` | 白名单精确比对，不做前缀/正则匹配 |
| `prompt=none` 静默认证 | `oauth_provider_handler.go` | 有效 session + `trusted_first_party` → 自动授权；无 session → `login_required` |
| Request Object 验证 | `oidc_request_object.go` | 非对称签名（RS/ES/PS）+ 对称签名（HS），`iss==client_id`、`aud` 含 issuer |
| request_uri SSRF 防护 | `oidc_request_object.go:455-530` | HTTPS-only、host 白名单、5s 超时、64KB 限制 |
| sector_identifier_uri 验证 | `oidc_pairwise.go:150-218` | HTTPS-only、redirect_uri 子集校验、1小时缓存 |
| Token Introspection 权限 | `oauth_provider_handler.go` | 仅 `client_confidential=true` 的机密 clients 可调用 |
| SPA fallback bypass | `embed_on.go:315–316` | `/oauth/` 和 `/.well-known/` 不被 Vue SPA 截胡 |
| client_secret bcrypt | `oauth_clients.client_secret_hash` | 公共 client 留空，机密 client bcrypt hash |

---

## 5. 差距分析（距 OIDC Core 1.0 —— 已于 2026-06-04 全部消除，2026-06-05 扩展高级特性）

> **更新（2026-06-05）**：OIDC Core 1.0 已完整实现并通过测试。2026-06-05 额外实现了 14 项 OIDC 可选能力。保留原条目作为历史对照。OIDC 架构见 [03-sakrylle-api-oidc-architecture.md](./03-sakrylle-api-oidc-architecture.md)。

### 5.1 OIDC Core 强制要求（原「必须实现」，现已实现）

| 原缺口 | 实现状态（2026-06-05） |
|---|---|
| **`id_token` 签发** | ✅ 已实现：`mintTokensFromCode` / `RefreshAccessToken` 在授权 scope 含 `openid` 时签发 id_token（RS256/ES256），含 `sid`/`at_hash`/`c_hash` 高级 claims |
| **`scope=openid` 未定义** | ✅ 已实现：`canonicalScopes` 含 `openid`/`profile`/`email`（migrations 149-150），`NormalizeScopes` 不再丢弃 |
| **`/.well-known/openid-configuration`** | ✅ 已实现并挂载，正常 serving |
| **`/.well-known/jwks.json`（JWKS 端点）** | ✅ 已实现：同时发布 RS256 + ES256 公钥（各自 kid） |
| **RS256 非对称签名基础设施** | ✅ 已实现：RSA-2048 + EC-P256 私钥加密存 `security_secrets`，独立于 HS256 session secret |
| **OIDC 标准 claims（`sub`/`iss`/`aud`/`nonce`）** | ✅ 已实现：`BuildIDTokenClaims` 输出标准 claims；`aud` 以**单元素 JSON 数组**发出；`iss` 固定 `https://sub.sakrylle.com` |

> **claims 安全护栏**：`BuildIDTokenClaims` 仅允许 `iss/sub/aud/exp/iat/nonce/auth_time/sid/name/preferred_username/email/email_verified/at_hash/c_hash`。纵深防御 allowlist 守卫 `assertNoForbiddenClaims` **fail-closed**：一旦任何商业 claim（`balance`、`group`、`group_id`、`rate_multiplier`、`quota`、`quota_used`、`daily_limit_usd`、`model_mapping`、`models`、`restrict_models`、`capabilities`、`allowed_groups`）出现即拒绝签发。
> **`email_verified`**：来自 `users.email_verified` 列（migration 157，per-user 标志，默认 `false`）。

### 5.2 OIDC 推荐/辅助能力（原「应当补齐」，现已实现）

| 原缺口 | 实现状态（2026-06-05） |
|---|---|
| **UserInfo 端点规范化** | ✅ 已实现：`/v1/me` + `/userinfo` 在含 `openid` scope 时返回顶层 `sub`；支持 `Accept: application/jwt` 签名响应 |
| **discovery 文档补齐 OIDC 字段** | ✅ 已实现：`/.well-known/openid-configuration` 含 `jwks_uri`、`subject_types_supported=["public","pairwise"]`、`claims_parameter_supported`、`request_parameter_supported`、`request_uri_parameter_supported`、`userinfo_signing_alg_values_supported`、`frontchannel_logout_supported`、`backchannel_logout_supported` |
| **`nonce` 参数处理** | ✅ 已实现：authorize 接收 `nonce` 并回填 id_token |
| **RP-Initiated Logout** | ✅ 已实现：`/oauth/logout` 端点 + redirect 白名单校验，消费 `oauth_clients.logout_redirect_uris` |

### 5.2.1 ES256 per-client 签发（2026-06-04 完成）

- `OIDCKeyService` 同时支持 RS256/ES256；`oauth_clients.signing_algorithm`（migration 151，CHECK `RS256|ES256`，默认 `RS256`）按 client 选择算法。
- ent schema 字段已加并跑 codegen，repo 映射进服务模型；`maybeSignIDToken` 解析 client 算法（空值/未知值安全回退 RS256），经 `OIDCKeyService.Sign` 签发。
- **密钥轮换 + grace-period 清理已实现，RSA 与 EC（ES256）完全对等**（2026-06-04）：`RotateKey`（RSA）/ `RotateECKey`（EC）生成新 key、把旧 key 移入对应 previous-kids 列表、更新指针；两者写入顺序一致，确保 mid-rotation 存储失败不会留下悬空指针。`CleanupExpiredKeys` **同时**清理过期 RSA + EC key 并返回合并删除计数；TTL 由 `oidc_grace_period_ttl_seconds` 配置（默认 24h）。`/.well-known/jwks.json` 在宽限期内同时发布 RSA + EC 的 current + previous 公钥（dual-kid），RP 可验证轮换前后两种算法的 id_token。
- **自动密钥轮换调度器**（2026-06-05 实现）：`OIDCKeyRotationScheduler` 双 goroutine（rotation + cleanup），配置项 `oidc_auto_rotation_enabled`/`oidc_key_rotation_interval_hours`/`oidc_key_cleanup_interval_hours`。手动触发接口仍保留。

### 5.3 OIDC 可选能力（2026-06-05 实现）

| 能力 | 实现文件 | 规范 |
|---|---|---|
| **Pairwise Subject Identifier** | `oidc_pairwise.go` + migration 153 | OIDC Core §8 |
| **sector_identifier_uri 获取** | `oidc_pairwise.go:150-218` | OIDC Core §8.1 |
| **Request Object 验证** | `oidc_request_object.go` + migration 155 | OIDC Core §6.1 |
| **request_uri 远程获取** | `oidc_request_object.go:455-530` | OIDC Core §6.3 |
| **Claims Parameter** | `oidc_claims_enforcement.go` + migration 154 | OIDC Core §5.5 |
| **Back-Channel Logout** | `oidc_backchannel_logout.go` + migration 156 | OIDC Back-Channel Logout 1.0 |
| **Front-Channel Logout** | `oauth_provider_handler.go` + migration 161 | OIDC Front-Channel Logout 1.0 |
| **Token Introspection** | `oauth_provider_handler.go` + migration 160 | RFC 7662 |
| **Session ID (sid)** | migration 158 + `oidc_id_token.go` | OIDC Session Management |
| **at_hash / c_hash** | `oidc_id_token.go:48-82` | OIDC Core §3.1.3.8 / §3.3.2.11 |
| **per-user email_verified** | migration 157 | OIDC Core §5.1 |
| **Signed UserInfo JWT** | `oidc_userinfo_jwt.go` | OIDC Core §5.3.2 |
| **Consent Grant 跟踪** | migration 159 | 第三方客户端授权记录 |

### 5.4 暂不需要（当前架构决定不实现）

| 项目 | 理由 |
|---|---|
| Client Credentials grant | 无 server-to-server 用例；Token handler switch 有意不包含此分支 |
| Hybrid Flow（`response_type=code id_token`） | 复杂度高，当前 RP 均可用 Authorization Code Flow |

### 5.5 安全风险（OIDC 实现前后关注）

| 风险 | 当前状态 | 优先级 |
|---|---|---|
| RS256/ES256 私钥泄漏 = 可伪造任意用户 id_token | 已缓解：AES-256-GCM 加密存 `security_secrets`，KEK = 共享 `TOTP_ENCRYPTION_KEY`（非 `OIDC_KEY_ENCRYPTION_KEY`，代码不读后者）；RSA + EC 对称 kid 轮换 + grace-period 清理（含自动调度器） | 高（结构性，持续关注） |
| `oauth_scope_enforcement_enabled = false` | **开放（过渡态）**：默认 `false`，`sk_oauth_` token 当前无 scope 粒度限制。开启是**生产决策，需审批**——开启后会开始拒绝 scope 不足的 token，须先全量验证各 RP scope 覆盖 | 中 |
| HS256 session secret 泄漏 = 全量会话失陷 | 对称 secret 单点，与 id_token 的 RS256/ES256 独立；轮换 `JWT_SECRET` 须强制全量重登（`TokenVersion` 递增） | 中 |
| Replay 保护依赖 `FOR UPDATE` 行锁 | 单节点部署，当前可接受 | 低 |

---

## 6. 后续事项（OIDC 已实现，余项为运营与客户端接入）

OIDC Core 1.0 + ES256 per-client 签发 + 14 项 OIDC 可选能力已于 2026-06-05 实现并通过测试。以下为剩余待办：

1. **`oauth_scope_enforcement_enabled` 开启** —— 默认 `false`（有意过渡态）。开启是**生产决策，需审批**：开启后开始拒绝 scope 不足的 `sk_oauth_` token，须先全量验证各 RP scope 覆盖。

2. **客户端产品 OIDC 接入** —— IdP 侧已就绪；Image / Web / CLI / Chat 仍需实际采用 OIDC 登录流程（加 `openid` scope、解析 id_token / `sub`）。

3. **migration 151 生产上线 + per-client `signing_algorithm` 配置** —— 属运营步骤，按项目惯例以直接 SQL 管理。

4. **`sakrylle-cli` Device Flow 是否有真实生产用户** —— 影响灰度顺序，留待 CLI 接入期确认。

5. **`oauth_v2_ui_enabled` 控制的前端 UI 范围** —— 未完全查明，可能影响 consent 页接入体验。

---

*文档生成时间：2026-06-03 | 更新：2026-06-05（OIDC Core 1.0 + ES256 per-client 签发 + 14 项 OIDC 可选能力已实现并测试通过）*
