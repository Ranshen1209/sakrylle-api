---
title: 03 · Sakrylle API OIDC 基座架构
status: canonical
scope: platform
last_verified: 2026-06-06
---

# 03 · Sakrylle API OIDC 基座架构

> **🎉 更新（2026-06-05）**：OIDC Core 1.0 + 大量 OIDC 可选能力已完整实现！本文档保留作为架构参考，实际实现状态见 [02-sakrylle-api-oauth-current-state.md](./02-sakrylle-api-oauth-current-state.md)。
> 
> ~~规划文档（planning only）。~~ Sakrylle API（sub2api fork）与 Sakrylle Image **已上线生产**，本文不含任何"直接改生产配置/破坏性迁移/删用户数据"的指令；所有触及生产的步骤均标注「需额外审批」。
> 兄弟文档：见 `04-oauth-oidc-commercial-capabilities.md`（claims 与计费/权限关系）、`05-configuration-isolation-standard.md`（客户端配置隔离）。

---

## 1. 调研范围

- 在 sub2api fork（Go 1.23 / Gin / ent / PostgreSQL 18 / Redis 8）现有 **OAuth 2.0 provider** 之上，规划符合 **OpenID Connect Core 1.0** 的身份层基座。
- 覆盖：discovery（`/.well-known/openid-configuration`）、JWKS、`id_token` 签发与签名密钥管理、UserInfo、标准 claims、`scope=openid profile email`、Authorization Code + PKCE（已有）、Refresh Token（已有）、Device Authorization Flow（已有 RFC 8628 基础）、多 client / 多 redirect_uri、客户端类型矩阵、token 吊销/刷新、logout/session、与 API Key 的关系。
- 6 个产品成员中，本文聚焦 Sakrylle API 作为 **IdP（identity provider）**，其余 5 个成员（CLI / Studio / Web / Chat / Image）作为 **RP（relying party）** 接入。

## 2. 关键结论

> **🎉 更新（2026-06-04）**：所有关键差距已消除！

1. **OAuth 2.0 层已相当完整**：Authorization Code + PKCE（S256 强制）、Refresh Token（rotation + 家族撤销）、RFC 8628 Device Grant、RFC 7009 Revocation、RFC 8414 discovery 全部就位。
2. ✅ **OIDC Core 1.0 已完整实现**（2026-06-04）：`id_token` 签发、`/.well-known/openid-configuration`、JWKS 端点、`scope=openid/profile/email`、标准 claims（`sub/iss/aud/nonce/auth_time`）全部就位。
3. ✅ **签名密钥基础设施已完成**：RS256 + ES256 双算法支持，私钥加密存储于 `security_secrets` 表（AES-256-GCM），公钥通过 `/.well-known/jwks.json` 发布，支持密钥轮换。
4. **`access_token` 是 opaque（不签名）token**，前缀 `sk_oauth_`，写入 `api_keys` 表；OIDC 改造**不应**改变 access_token 形态——只新增 `id_token`，access_token 仍走现有网关计费/缓存路径（与 `04` 文档一致）。✅ **已实现**。
5. ✅ **改造已完成，完全增量、零破坏**：所有新增都是"叠加"（新端点、新 scope、新 claim），不破坏现有 OAuth RP（Sakrylle Image 已在用 `sk_oauth_`）。

## 3. 相关文件路径（path:line，引用自调研）

| 关注点 | 路径:行 |
|---|---|
| 全路由注册（含 RFC 8414 discovery） | `backend/internal/server/routes/oauth.go:57` |
| Device flow 路由 | `backend/internal/server/routes/oauth_device.go:54` |
| OAuth provider 核心实现（3000+ 行） | `backend/internal/service/oauth_provider_service.go` |
| refresh token SHA-256 存储 | `backend/internal/service/oauth_provider_service.go:1901` |
| PKCE S256 常量时间比较 | `backend/internal/service/oauth_provider_service.go:1929` |
| CSRF token 常量时间比较 | `backend/internal/service/oauth_provider_service.go:601` |
| replay 容灾 legacy 非原子路径 | `backend/internal/service/oauth_provider_service.go:1696` |
| 规范 scope 定义（11 个 + alias） | `backend/internal/service/oauth_scopes.go:25` |
| Device Grant 实现 | `backend/internal/service/oauth_device_service.go` |
| discovery 文档构造 | `backend/internal/handler/oauth_provider_handler.go:625`（line 642 = `service_documentation`） |
| `prompt=none` 直接 `interaction_required` | `backend/internal/handler/oauth_provider_handler.go:97` |
| UserInfo 当前实现（`/v1/me`） | `backend/internal/handler/oauth_provider_account_handler.go:76`（路由 `:90`） |
| consent 页 XSS 防护 | `backend/internal/handler/oauth_provider_consent.go:326` |
| SPA fallback bypass（`/oauth/`、`/.well-known/`） | `backend/internal/web/embed_on.go:315` |
| 初始 schema（clients/codes/refresh） | `backend/migrations/143_oauth_provider.sql` |
| v2 扩展（authorize_transactions/access_tokens/device_codes） | `backend/migrations/145_oauth_v2.sql` |
| Sakrylle image-playground seed | `backend/migrations/144_oauth_seed_sakrylle.sql` |
| v2 clients seed（cli/desktop/image-v2） | `backend/migrations/148_oauth_v2_sakrylle_seed.sql` |
| JWTConfig（Secret/Expire） | `backend/internal/config/config.go:1195` |
| 用户会话 JWT 签发（HS256） | `backend/internal/service/auth_service.go:1172` |
| JWTClaims 定义 | `backend/internal/service/auth_service.go:55` |

## 4. 当前实现摘要

- **双轨认证**：
  - 用户会话 JWT —— `jwt.NewWithClaims(jwt.SigningMethodHS256)`（`auth_service.go:1172`），payload 含 `UserID/Email/Role/TokenVersion`，对称 secret `jwt.secret`（≥32 字节）。
  - OAuth access_token —— **不签名 opaque**，`sk_oauth_` 前缀（32 字节 `crypto/rand` → base64url），写 `api_keys` 表带 `expires_at`，中间件做 DB/Redis 查询验证。
- **refresh_token**：`rt_` 前缀，SHA-256 hex 存 `oauth_refresh_tokens.token_hash`；rotation + replay containment（`FOR UPDATE` 原子 + `rotated_to_hash`）。
- **PKCE**：S256 强制，`subtle.ConstantTimeCompare`。
- **discovery**：`/.well-known/oauth-authorization-server`（RFC 8414），含 `authorization_endpoint/token_endpoint/revocation_endpoint/userinfo_endpoint`，但**无 OIDC 字段**。
- **UserInfo（事实上的）**：`/v1/me` 返回 `profile + balance + group`（按 scope 裁剪），是 **plain JSON**，不是 `sub`-标准 claims、不签名。
- **运行时开关**（`settings` 表，直接 SQL 管理）：`oauth_provider_enabled`、`oauth_issuer`、`oauth_default_group_id`、`oauth_scope_enforcement_enabled`、`oauth_device_flow_enabled`、`oauth_v2_ui_enabled`。
- **已注册 client（seed 现状）**：`sakrylle-image-playground`（144）、`sakrylle-cli`/`sakrylle-desktop`/`sakrylle-image-playground-v2`（148）。注：目标方案（§9，已确认 2026-06-03）**沿用 `sakrylle-image-playground` 不用 `-v2`**，Studio 首发复用 CLI 凭据故 `sakrylle-desktop` 不单独注册 —— seed 替换时按 §9 总表对齐。

## 5. 差距分析（现状 → 目标）

> **🎉 更新（2026-06-05）**：所有 OIDC Core 差距（G1-G10）已消除，且大量 OIDC 可选能力（G12-G23）也已实现！

### 5.1 OIDC Core 强制要求

| # | 维度 | 现状 | OIDC 目标 | 状态 |
|---|---|---|---|---|
| G1 | discovery | 仅 `/.well-known/oauth-authorization-server` | 新增 `/.well-known/openid-configuration`，含 `issuer/jwks_uri/id_token_signing_alg_values_supported/subject_types_supported/claims_supported/scopes_supported(含 openid)` | ✅ **完成** |
| G2 | JWKS | 无 | `/.well-known/jwks.json`，同时发布 RS256 + ES256 两套公钥（各自 `kid`） | ✅ **完成** |
| G3 | 签名密钥 | 仅 HS256 对称 secret（登录 session 用）；`security_secrets` 表已存在（migration 053） | RS256（主）+ ES256（P-256，第二算法）非对称密钥对 + kid 轮换 + 安全存储（**复用 `security_secrets` 表，新增 key 行、加密 at-rest，无需建表**） | ✅ **完成** |
| G4 | `id_token` | token 响应无该字段 | `authorization_code`/`refresh_token` 授含 `openid` 时签发 JWT id_token（默认 RS256，header 带 kid） | ✅ **完成** |
| G5 | `scope=openid` | `canonicalScopes`（`oauth_scopes.go:25`）不含，`NormalizeScopes` 静默丢弃 | 注册 `openid`/`profile`/`email` 为规范 scope | ✅ **完成**（migrations 149-150） |
| G6 | 标准 claims | 无 `sub/iss/aud/nonce`；`/v1/me` 是 plain JSON | id_token + UserInfo 输出标准 claims | ✅ **完成** |
| G7 | UserInfo 规范 | `/v1/me` 非 OIDC 格式 | 增 OIDC 分支：`sub` 必返，`Accept: application/jwt` 可选签名响应 | ✅ **完成**（含签名 JWT 支持） |
| G8 | `nonce` | 无 | authorize 接收 `nonce`，回填 id_token | ✅ **完成** |
| G9 | `prompt=none` SSO | 直接 `interaction_required`（`handler:97`） | silent auth（已有有效会话则免交互） | ✅ **完成** |
| G10 | RP-Initiated Logout | `oauth_clients.logout_redirect_uris` 字段在，无端点 | `/oauth/logout` 端点，消费 `logout_redirect_uris` | ✅ **完成** |
| G11 | `client_credentials` | Token handler 无该分支 | 服务账号场景（见 `04`），可选 | ❌ 未实现（非 OIDC 核心） |

### 5.2 OIDC 可选能力（2026-06-05 实现）

| # | 维度 | 实现文件 | 规范 | 状态 |
|---|---|---|---|---|
| G12 | Pairwise Subject Identifier | `oidc_pairwise.go` + migration 153 | OIDC Core §8 | ✅ **完成** |
| G13 | sector_identifier_uri 获取 | `oidc_pairwise.go:150-218` | OIDC Core §8.1 | ✅ **完成** |
| G14 | Request Object 验证 | `oidc_request_object.go` + migration 155 | OIDC Core §6.1 | ✅ **完成** |
| G15 | request_uri 远程获取 | `oidc_request_object.go:455-530` | OIDC Core §6.3 | ✅ **完成** |
| G16 | Claims Parameter | `oidc_claims_enforcement.go` + migration 154 | OIDC Core §5.5 | ✅ **完成** |
| G17 | Back-Channel Logout | `oidc_backchannel_logout.go` + migration 156 | OIDC Back-Channel Logout 1.0 | ✅ **完成** |
| G18 | Front-Channel Logout | `oauth_provider_handler.go` + migration 161 | OIDC Front-Channel Logout 1.0 | ✅ **完成** |
| G19 | Token Introspection | `oauth_provider_handler.go` + migration 160 | RFC 7662 | ✅ **完成** |
| G20 | Session ID (sid) | migration 158 + `oidc_id_token.go` | OIDC Session Management | ✅ **完成** |
| G21 | at_hash / c_hash | `oidc_id_token.go:48-82` | OIDC Core §3.1.3.8 / §3.3.2.11 | ✅ **完成** |
| G22 | per-user email_verified | migration 157 | OIDC Core §5.1 | ✅ **完成** |
| G23 | Signed UserInfo JWT | `oidc_userinfo_jwt.go` | OIDC Core §5.3.2 | ✅ **完成** |
| G24 | Consent Grant 跟踪 | migration 159 | 第三方客户端授权记录 | ✅ **完成** |
| G25 | 自动密钥轮换调度器 | `oidc_key_rotation.go` | 运维自动化 | ✅ **完成** |

## 6. 目标端点蓝图

> **🎉 更新（2026-06-05）**：所有 OIDC 端点均已实现，含高级特性端点！

| 端点 | 方法 | 状态 | OIDC 角色 |
|---|---|---|---|
| `/.well-known/openid-configuration` | GET | ✅ **已实现**（G1） | OIDC discovery |
| `/.well-known/jwks.json` | GET | ✅ **已实现**（G2） | 公钥发布 |
| `/.well-known/oauth-authorization-server` | GET | 已有（`oauth.go:57`） | RFC 8414，保留 |
| `/oauth/authorize` | GET/POST | 已有（`oauth.go:80-81`） | 接收 `scope=openid`、`nonce`、`prompt`、`request`、`request_uri`、`claims` ✅ |
| `/oauth/token` | POST | 已有（`oauth.go:83`） | ✅ **响应新增 `id_token`**（G4） |
| `/oauth/revoke` | POST | 已有（`oauth.go:92`） | RFC 7009 |
| `/oauth/introspect` | POST | ✅ **已实现**（G19） | RFC 7662 Token Introspection（仅 confidential clients） |
| `/oauth/device/code` | POST | 已有（`oauth_device.go:54`） | RFC 8628，CLI 优先 |
| `/oauth/device` | GET | 已有（`oauth_device.go:60`） | 设备验证页 |
| `/oauth/logout` | GET/POST | ✅ **已实现**（G10） | RP-Initiated Logout |
| `/oauth/frontchannel-logout` | GET | ✅ **已实现**（G18） | OIDC Front-Channel Logout 1.0 |
| `/userinfo` | GET/POST | ✅ **已实现**（G7） | OIDC UserInfo 端点（含签名 JWT 支持） |
| `/v1/me`（UserInfo） | GET | 已有（`account_handler:90`） | ✅ **已增 OIDC claims**：返回 `sub` claim（G7） |
| `/api/v1/oauth/authorize/approve` | POST | 已有（`oauth.go:118`） | JWT 保护 consent 决策 |
| `/api/v1/oauth/authorized-apps` | GET/DELETE | 已有（`oauth.go:128-133`） | 用户自助撤权 |

**Issuer（已确认 2026-06-03）**：`oauth_issuer = https://sub.sakrylle.com`（单一 issuer，与 migration 148 seed 一致；`api.sakrylle.com` 仅作 `/v1` 反代，**不作 issuer**）。
理由：(a) issuer **一经发布不可轻易更改**（已嵌入所有 RP 缓存的 discovery、已签发 id_token 的 `iss` claim）；(b) `sub.sakrylle.com` 是 app 主域、已是登录/consent 页所在，浏览器型 RP（Web/Image）回跳天然同域；(c) `api.sakrylle.com` 是纯 Nginx 反代 `/v1/*` 的**网关域**，职责是数据面，不宜承载 IdP 控制面。
**`jwks_uri` / `userinfo_endpoint` 必须用 issuer 同源绝对 URL**：`https://sub.sakrylle.com/.well-known/jwks.json`、`https://sub.sakrylle.com/v1/me`。

## 7. 签名密钥管理方案（G2/G3 核心）

> **🎉 更新（2026-06-04）**：密钥基础设施已完整实现！

**现状**：`jwt.secret`（HS256 对称）只签登录 session（`auth_service.go:1172`），泄漏即全线失陷且无法发布公钥给 RP 验签 —— 不可复用于 id_token。

**✅ 已实现设计**：
- **算法**：✅ **RS256 为主 + ES256（P-256）为第二算法**。RS256 OIDC 互操作性最广（所有成熟 RP 库支持），ES256 提供更短密钥/签名。`jwks.json` **同时暴露两套 key、各自 kid**；discovery 的 `id_token_signing_alg_values_supported` 同时列出 `["RS256","ES256"]`。
- **密钥基建**：✅ **复用现有 `security_secrets` 表**（`backend/internal/service/oidc_key_store_impl.go`）：每套密钥新增一行（命名如 `oidc_signing_key_rsa_<kid>` / `oidc_signing_key_ec_<kid>`，value 存私钥），私钥**使用 AES-256-GCM 加密 at-rest**，KEK = **共享的 `TOTP_ENCRYPTION_KEY`**（`cfg.Totp.EncryptionKey`，64 hex = 32 bytes）。代码经 `repository.NewAESEncryptor` 解码此 env 得到 KEK，**不存在** `OIDC_KEY_ENCRYPTION_KEY` 变量；同一 KEK 还保护 TOTP / channel-monitor / backup secrets。多副本天然共享、轮换写一行即可（但**更换该 KEK 会同时作废上述所有密文**）。
- **`kid`（key id）轮换**：✅ 已实现，**RSA 与 EC（ES256）完全对等**（EC 对称轮换于 2026-06-04 补齐）
  - JWKS 同时发布 **当前 + 上一个** 公钥（双 kid 并存），且 RSA + EC 两种算法**各自**同时发布 current + previous（dual-kid），保证轮换窗口内旧 id_token 跨两种算法仍可验签。
  - id_token header 带 `kid`，RP 按 kid 取公钥。
  - 轮换接口：`OIDCKeyService.RotateKey`（RSA）/ `RotateECKey`（EC）——生成新 key、把旧 key 移入对应 previous-kids 列表、更新指针；两者写入顺序一致，确保 mid-rotation 存储失败不留悬空指针。`CleanupExpiredKeys` **同时**清理过期 RSA + EC key 并返回合并删除计数（TTL = `oidc_grace_period_ttl_seconds`，默认 24h）。
  - **自动调度器**（2026-06-05 实现）：`OIDCKeyRotationScheduler` 双 goroutine 架构——rotation goroutine 按 `oidc_key_rotation_interval_hours`（默认 90 天）自动轮换，cleanup goroutine 按 `oidc_key_cleanup_interval_hours`（默认 24h）自动清理过期 key。配置项：`oidc_auto_rotation_enabled`（**默认 true**，即自动轮换默认开启）、`oidc_grace_period_ttl_seconds`。两个 goroutine 均含 panic-recovery。手动触发接口 `RotateKey`/`RotateECKey`/`CleanupExpiredKeys` 仍保留。
  - 轮换节奏：建议 90 天；新 key 生成后先进 JWKS（仅发布、不签发）一个传播窗口（≥discovery/JWKS 缓存 TTL），再切为签发 key。
  - 轮换脚本：`backend/scripts/oidc-key-rotate.sh`（三阶段密钥轮换）
- **缓存**：✅ JWKS 端点设 `Cache-Control: max-age=3600`；签名私钥进程内缓存。

**部署工具**：
- ✅ `backend/scripts/oidc-setup.sh` — KEK 生成 + 密钥初始化
- ✅ `backend/scripts/oidc-verify.sh` — 7 项完整验证测试
- ✅ `backend/scripts/oidc-key-rotate.sh` — 密钥轮换流程

## 8. id_token 与 claims 设计（G4/G6/G8）

> **🎉 更新（2026-06-05）**：id_token 签发已完整实现，含高级 claims 支持！

**✅ 签发触发（已实现）**：仅当授权 scope 含 `openid` 时，在 `mintTokensFromCode` / `RefreshAccessToken`（`oauth_provider_service.go`）额外签发 id_token。**签名算法按 client 选择**（2026-06-04 完成）：`oauth_clients.signing_algorithm`（migration 151，CHECK `RS256|ES256`，默认 `RS256`）映射进 ent schema，`maybeSignIDToken` 解析该 client 的算法（空值/未知值安全回退 RS256），经 `OIDCKeyService.Sign` 用 RS256 或 ES256 签发，header 带 `kid`。**access_token 形态不变**（仍 `sk_oauth_` opaque）。

**✅ id_token claims（已实现）**：

| claim | 来源 | 条件 |
|---|---|---|
| `iss` | `oauth_issuer` 设置 | 必含 |
| `sub` | 用户稳定唯一标识（=`user.ID` 字符串）；pairwise client 时为 SHA-256 伪名 | 必含 |
| `aud` | `client_id` | 必含（以**单元素 JSON 数组**发出） |
| `exp` / `iat` | 签发时间 + 短 TTL | 必含 |
| `nonce` | authorize 请求回填 | 当请求带 `nonce` ✅ |
| `auth_time` | 用户最近认证时间 | 可选（`max_age`/`prompt` 时建议）✅ |
| `sid` | OIDC session ID（migration 158） | 当 client 配置 `backchannel_logout_session_required` 时 ✅ |
| `email` / `email_verified` | 用户 email | 当授 `email`（OIDC `email` scope）✅；`email_verified` 来自 `users.email_verified` 列（migration 157，per-user 标志） |
| `name` / `preferred_username` | username | 当授 `profile` ✅ |
| `at_hash` | access_token 的 SHA-256 左半 | 当 id_token 与 access_token 同时签发时 ✅ |
| `c_hash` | authorization_code 的 SHA-256 左半 | 当 id_token 与 authorization_code 同时签发时 ✅ |

> **claims 安全护栏（已实现）**：`BuildIDTokenClaims` 仅允许上表 14 个 claim（`iss/sub/aud/exp/iat/nonce/auth_time/sid/name/preferred_username/email/email_verified/at_hash/c_hash`）。纵深防御 allowlist 守卫 `assertNoForbiddenClaims` **fail-closed**：任何商业 claim（`balance`、`group`、`group_id`、`rate_multiplier`、`quota`、`quota_used`、`daily_limit_usd`、`model_mapping`、`models`、`restrict_models`、`capabilities`、`allowed_groups`）一旦出现即拒绝签发（与 `04` claims 边界、`91` R-TOKEN-03 一致）。

**✅ scope → claims 映射（已实现）**（`scope=openid profile email`）：
- `openid`（必需，触发 id_token）→ `sub`
- `profile` → `name`、`preferred_username`
- `email` → `email`、`email_verified`

> **Pairwise Subject（G12，2026-06-05 实现）**：`sub` 在 `subject_types_supported=["public"]` 下对所有 client 一致（public sub = user ID 字符串）。当 client 配置 `subject_type="pairwise"` 时，`sub` 为 SHA-256(`issuer + "\x00" + userID + "\x00" + sectorIdentifier`) 的 base64url 编码，不同 client 得到不同 sub，防跨 RP 用户关联。sector identifier 优先从 `sector_identifier_uri` 获取（HTTPS-only，1小时缓存），回退到 redirect_uris 的 host 列表。

**✅ UserInfo（`/v1/me` + `/userinfo`，G7，已实现）**：
- 当 access_token 授权含 `openid` 时，响应必须含 `sub`（与 id_token 一致），`email`/`name` 按 scope 裁剪。
- 支持 `Accept: application/jwt` 返回签名的 UserInfo JWT（RS256/ES256，TTL 5分钟）（G23）。
- `balance`/`group` 仍按现有 scope 返（与 `04` 文档计费 claims 原则一致：余额绝不进 id_token，只在 UserInfo 实时返）。

**✅ Claims Parameter（G16，2026-06-05 实现）**：authorize 请求支持 `claims` 参数（OIDC Core §5.5），`ApplyClaimsConstraints` 按 essential/value/values 约束过滤 id_token 和 UserInfo 响应中的 claims。

## 9. 客户端注册建议（多 client / 多 redirect_uri / 客户端类型）

`oauth_clients` 表已支持：`client_id`、`client_secret_hash`（公共 client 留空）、`redirect_uris`（jsonb 精确白名单）、`allowed_scopes`、`pkce_required`、`default_group_id`、`access_token_ttl_seconds`、`refresh_token_ttl_seconds`、`logout_redirect_uris`、`disabled`。

**客户端注册总表（已确认 2026-06-03）**：

| 产品 | client_id | 客户端类型 | grant 类型 | PKCE | redirect_uri | scope |
|---|---|---|---|---|---|---|
| Sakrylle Web（open-webui fork，域名 `chat.sakrylle.com`） | `sakrylle-web` | 机密 | authorization_code + refresh_token | pkce + client_secret | `https://chat.sakrylle.com/oauth/oidc/login/callback` | `openid profile email` + 调用 `/v1` 所需 |
| Sakrylle Image（已上线） | `sakrylle-image-playground`（**沿用现有 client_id，不新建 `-v2`**） | 公共（SPA） | authorization_code + refresh_token | pkce | `https://image.sakrylle.com/oauth/callback`、`http://localhost:5173/oauth/callback` | 在原 `image_generation`/`balance:read`/`models:read` 基础上**追加 `openid profile email`** |
| Sakrylle CLI（codex fork） | `sakrylle-cli` | 公共 | authorization_code + device_code + refresh_token | pkce_required | `http://127.0.0.1`（任意端口）`/callback` + `http://localhost`（loopback 白名单）；device flow 无需 redirect | `openid profile email` + 调用 `/v1` 所需 |
| Sakrylle Studio（CodexMonitor fork，桌面 Tauri） | `sakrylle-studio` | 公共（native） | authorization_code + refresh_token | pkce_required，仅 S256 | `http://127.0.0.1:{random_port}/callback`；兼容 `[::1]` / `localhost` loopback 任意端口，路径必须 `/callback` | `openid profile email offline_access` + 调用 `/v1` 所需 |
| Sakrylle Chat（kelivo fork，移动/跨端） | `sakrylle-chat` | 公共 | authorization_code + PKCE | pkce | `sakrylle-chat://oauth/callback`（scheme 实现期核实） | `openid profile email` + 调用 `/v1` 所需 |

**原则**：Web 这类有后端、可安全保存 secret 的服务端 RP 使用机密 client；SPA、CLI、桌面、移动等公共 client 使用 `pkce_required=true`。redirect_uri 走**精确白名单**（Web 固定 `https://chat.sakrylle.com/oauth/oidc/login/callback`；CLI/Studio loopback 用 `http://127.0.0.1` / `http://[::1]` / `http://localhost` 任意端口并精确匹配路径）；CLI 支持 Device Authorization Flow（无浏览器/无回调端口依赖，已有 RFC 8628 基础）。`email:read` 对第一方 client（Image/CLI/Web/Chat/Studio）默认授予（已确认 2026-06-03）。
> bundle/包标识统一 `com.sakrylle.*`；CLI 二进制 `sakrylle`（短别名 `skl`）；遥测默认关闭（已确认 2026-06-03，详见 `05`）。
> 仅 **Chat 回调 scheme** 保留「实现期核实」注记；Web 回调路径已按 open-webui `oidc` provider 路由确认为 `/oauth/oidc/login/callback`。

## 10. 分阶段实施计划

> 串行/并行已标注。**所有触及生产数据库/`settings`/密钥的步骤均「需额外审批」**，本地/预览环境先行。

### Phase 0 · 调研与保护（串行，前置）
- **目标**：建立密钥基座、确认表结构、建立回滚护栏。
- [x] 确认 `security_secrets` 表已存在（migration 053）/ 但无非对称密钥 → **复用现有表存放 OIDC 签名密钥**（已确认 2026-06-03）
  - 结论：Phase 1 **无需建表 migration**，只需在 `security_secrets` 表新增 key 行 + 密钥服务（见 §7）；私钥加密 at-rest；`JWT_SECRET`=HS256 仅登录 session，不复用
  - 验收标准：已给出"复用 `security_secrets` 表、无需建表"结论
- [x] 客户端注册总表已锁定（已确认 2026-06-03，见 §9）
  - 涉及文件：`backend/migrations/148_oauth_v2_sakrylle_seed.sql`（seed 替换时按 §9 总表对齐：Image 沿用现有 `sakrylle-image-playground` 不建 `-v2`；Studio 首发复用 CLI 凭据不单独注册）
  - 验收标准：seed 与 §9 总表一致
- [x] 冻结 `oauth_issuer = https://sub.sakrylle.com` 决策（已确认 2026-06-03，见 §6）
  - 验收标准：决策已写入文档；生产 `settings.oauth_issuer` 现值核对（只读，不改）

### Phase 1 · 最小可用 OIDC 集成（依赖 Phase 0；G3/G2/G5 串行，G1 可并行）✅ **[已完成 2026-06-04]**
- **目标**：能签发可验签的 id_token，RP 可发现。
- **依赖项**：本 Phase 全部 OIDC 改造**依赖 RS256 密钥基座（G3）先落地**。
- [x] [依赖 OIDC 密钥基座] RS256 + ES256 密钥对生成 + 加密存储 + kid（G3，复用 `security_secrets` 表）— **[✓ 2026-06-04]**
  - 涉及文件：**复用现有 `security_secrets` 表（migration 053，无需新建表）**、新 `oidc_key_service`、`config.go`
  - 实施说明：RSA-2048（主）+ ES256/P-256（第二算法）私钥加密 at-rest（参考 TOTP 加密密钥模式）后以新增 key 行写入 `security_secrets`；进程内缓存
  - 验收标准：能加载两套私钥、各导出 JWK 公钥（各自 kid）
  - 标注：触及密钥/env → **需额外审批**（生产注入）
- [x] [依赖上一步] `/.well-known/jwks.json` 端点（G2）— **[✓ 2026-06-04]**
  - 涉及文件：`oauth.go` 路由、新 handler、`embed_on.go:315` bypass 已含 `/.well-known/`
  - 验收标准：返回合法 JWKS，**同时含 RS256 + ES256 两套 key（各自 kid，含当前 + 上一）**，`Cache-Control` 合理
- [x] 注册 `openid`/`profile`/`email` 规范 scope（G5）— **[✓ 2026-06-04，migrations 149-150]**
  - 涉及文件：`backend/internal/service/oauth_scopes.go:25`
  - 实施说明：加入 `canonicalScopes`，确保 `NormalizeScopes` 不丢弃 `openid`
  - 验收标准：授权请求带 `openid` 不被静默丢
- [x] [依赖密钥基座] id_token 签发（G4/G6/G8）— **[✓ 2026-06-04]**
  - 涉及文件：`oauth_provider_service.go`（`mintTokensFromCode`/`RefreshAccessToken`）、`oidc_id_token.go`（`BuildIDTokenClaims`）
  - 实施说明：scope 含 `openid` 时按 client `signing_algorithm` 签 RS256/ES256 JWT，claims 见 §8；接收并回填 `nonce`；`assertNoForbiddenClaims` fail-closed 护栏
  - 验收标准：token 响应含 `id_token`，第三方 OIDC 库可用 JWKS 验签通过
- [x] `/.well-known/openid-configuration` 端点（G1）— **[✓ 2026-06-04]**
  - 涉及文件：`oauth_provider_handler.go`（参照 `:625` discovery 构造）
  - 实施说明：含 `issuer/jwks_uri/userinfo_endpoint/id_token_signing_alg_values_supported=["RS256","ES256"]/subject_types_supported=["public"]/claims_supported/scopes_supported`
  - 验收标准：标准 OIDC discovery 校验工具通过

### Phase 2 · 品牌与配置隔离（与 Phase 1 部分并行；不依赖密钥基座）
- **目标**：consent/device 页与 client 元数据完成 Sakrylle 品牌化；与 `05` 隔离规范对齐。
- [ ] consent 页品牌化（Monet 紫 `#9181bd`、樱花 logo、￥ 规则不变）
  - 涉及文件：`backend/internal/handler/oauth_provider_consent.go`
  - 实施说明：保留 `json.Encoder.SetEscapeHTML(true)` XSS 防护（`:326`）
  - 验收标准：consent 页配色/品牌符合 §品牌；无 XSS 回归
- [ ] client 注册补全（§9 总表，含 redirect_uri 白名单、scope、pkce）
  - 涉及文件：替换 `144`/`148` seed（**fork 部署前替换**）
  - 验收标准：client 注册项与 §9 总表一致（Image 沿用 `sakrylle-image-playground`、追加 `openid profile email`；CLI 含 device_code + loopback 白名单；Studio 复用 CLI 凭据不单独注册；Chat scheme 实现期核实）
  - 标注：写生产 `oauth_clients` → **需额外审批**

### Phase 3 · 完整 OIDC / 权限 / 审计（依赖 Phase 1）
- **目标**：UserInfo 合规、logout、SSO silent、（可选）服务账号。
- [x] UserInfo OIDC 分支（G7）— **[✓ 2026-06-04]**
  - 涉及文件：`oauth_provider_account_handler.go:76`
  - 实施说明：授 `openid` 时必返顶层 `sub`，与 id_token 一致；商业状态留在 scoped account/group 块，绝不作顶层 OIDC claim
  - 验收标准：OIDC RP 取 UserInfo 得标准 claims；余额仍实时（绝不进 id_token，见 `04`）
- [x] RP-Initiated Logout `/oauth/logout`（G10）— **[✓ 2026-06-04]**
  - 涉及文件：`oauth.go` 新路由、消费 `oauth_clients.logout_redirect_uris`
  - 验收标准：`post_logout_redirect_uri` 在白名单内才 302，否则内联 HTML（不当 open redirector）
- [x] `prompt=none` silent auth（G9）— **[✓ 2026-06-04]**
  - 涉及文件：`oauth_provider_handler.go:97`
  - 验收标准：有有效会话免交互返 code；无则 `interaction_required`
- [ ] （可选）`client_credentials` grant（G11，服务账号，见 `04` 权限模型）—— 非 OIDC 核心，暂不实现
  - 涉及文件：`oauth_provider_service.go` token handler switch
  - 验收标准：机密 client 凭 `client_secret` 取 token；公共 client 拒绝

### Phase 4 · 测试 / 发布 / 回滚
- **目标**：OIDC 一致性测试 + 灰度 + 回滚预案。
- [ ] OIDC 一致性测试（discovery/JWKS/id_token/UserInfo/nonce/PKCE）
  - 验收标准：覆盖 §5 各 Gap；80%+ 覆盖（遵循项目测试规范）
- [ ] 灰度发布（先 Sakrylle Image 走 `openid`，再 Web/CLI）
  - 标注：生产灰度 → **需额外审批**
- [ ] 回滚预案
  - 实施说明：OIDC 全为叠加，回滚 = `oauth_provider_enabled` 不变、RP 暂不请求 `openid`、必要时下线新 `/.well-known/openid-configuration`；access_token 路径不受影响
  - 验收标准：回滚不影响现有 `sk_oauth_` 调用与计费

## 11. 优先级

P0：Phase 0 + Phase 1（G3→G2→G4→G5→G1，OIDC 能跑通）。
P1：Phase 3 的 UserInfo（G7）+ Logout（G10）+ Phase 2 品牌化。
P2：`prompt=none`（G9）、`client_credentials`（G11）。

## 12. 风险

- **R1（高）**：RS256 私钥泄漏 = 可伪造任意用户 id_token。必须加密存储 + 最小读权限 + 轮换。
- **R2（中）**：issuer 一经发布难改；`api.sakrylle.com` vs `sub.sakrylle.com` 选错会迫使全 RP 重配。已在 §6 锁定 `sub.sakrylle.com`。
- **R3（中，开放）**：`oauth_scope_enforcement_enabled` 默认 `false`（`migrations/145_oauth_v2.sql:348`）—— 当前 `sk_oauth_` token 可访问所有路由。开启是**生产决策，需审批**：开启后开始拒绝 scope 不足的 token，须先全量验证各 RP（尤其 Sakrylle Image）scope 覆盖。
- **R4（已缓解）**：id_token 签发走生产 `mintTokensFromCode` / `RefreshAccessToken` 路径（非测试 fake legacy 路径）；replay 防护 `FOR UPDATE` 行锁保留。
- **R5（低）**：JWKS 缓存 TTL 与 kid 轮换窗口不匹配会致验签失败 —— 轮换务必"先发布后签发"。

## 13. 验收标准（整体）

1. `/.well-known/openid-configuration` + `/.well-known/jwks.json` 可被标准 OIDC RP 库自动发现并验签。
2. `scope=openid profile email` 授权后，token 响应含可验签 `id_token`，claims 符合 §8。
3. UserInfo（`/v1/me`）授 `openid` 必返 `sub`，余额/group 仍实时查询、**绝不进 id_token**。
4. 5 个 RP（§9）按各自类型成功完成首登（CLI 走 device flow）。
5. access_token（`sk_oauth_`）形态、网关计费、auth 缓存路径**零回归**。
6. 所有生产写操作经审批；OIDC 改造可回滚不影响现网。

## 14. 后续问题

- ~~`security_secrets` 表是否存在？~~ **已确认 2026-06-03：已存在（migration 053:2），OIDC 私钥复用该表、加密 at-rest、无需新建表**。
- ~~`oauth_scope_enforcement_enabled=false` 是否有意？~~ **已确认 2026-06-03：分阶段开启**（先观测/告警，后强制；见 R3）。
- ~~是否需要 ES256 作为第二 alg？~~ **已确认 2026-06-03：是，RS256 主 + ES256(P-256) 第二算法**。
- ~~`pairwise` sub 是否需要？~~ **已实现 2026-06-05：完整实现**（`oidc_pairwise.go` + migration 153），`subject_types_supported` 已返回 `["public","pairwise"]`。
- `oauth_v2_ui_enabled` 控制哪些前端 UI，是否影响 OIDC consent 流程？（**不确定**）
- `sakrylle-cli` device flow 是否已有生产真实用户在用？（**不确定**，影响灰度顺序）
- Chat 回调 scheme（**实现期核实**）；open-webui（Web）回调路径已确认为 `https://chat.sakrylle.com/oauth/oidc/login/callback`。
