# Sakrylle OAuth 2.0 与 OpenID Connect 工业级平台设计

| 字段 | 值 |
| --- | --- |
| 状态 | 目标架构与实施契约 |
| 版本 | 2026-08-15 |
| 适用仓库 | Sakrylle API |
| 规范 issuer | `https://oidc1.sakrylle.com` |
| 运维事实来源 | [`docs/ops/identity-email.md`](ops/identity-email.md) |

本文档是 Sakrylle 作为 OAuth 2.0 Authorization Server、OpenID Connect Provider 和 API Resource Server 的长期技术契约。它同时记录当前实现、已知缺口、目标架构、迁移顺序和发布门禁。

本文档不是生产配置快照。域名、客户端实际分组、部署命令和现场故障处理仍以 `docs/ops/` 为准。实现变更如果影响 issuer、scope、claim、客户端注册、密钥或 rollout 状态，必须同步更新相应运维文档。

文中的关键字“必须”“禁止”“应”“可以”分别对应 RFC 语义中的 MUST、MUST NOT、SHOULD、MAY。

## 1. 执行摘要

Sakrylle 已经不是 OAuth/OIDC 绿地项目。当前代码以不同完成度具备 Authorization Code、强制 S256 PKCE、Refresh Token Rotation、Device Authorization Grant、Discovery、JWKS、ID Token、UserInfo、pairwise subject、introspection、revocation、同意授权和 OIDC logout 等主要能力。

工业级演进不采用一次性重写。目标策略是：

1. 保留现有用户、分组、计费、API gateway、Redis 鉴权缓存和已上线客户端契约。
2. 固定现有 issuer，禁止因入口域名变化而更换 issuer。
3. 优先消除 access token 明文持久化、单 client secret、共享加密根密钥和协议文档漂移。
4. 将协议解析、标准校验和错误序列化逐客户端迁移到成熟 OAuth/OIDC 引擎；Sakrylle 自有 service 继续负责用户、同意、分组、权限和计费政策。
5. 建设 GitHub 风格的开发者应用控制台、应用审核、凭据轮换、授权管理和安全审计。
6. 将普通用户 OAuth/OIDC、应用安装身份和 CI 工作负载 OIDC 分成独立产品与信任域。

### 1.1 不变的兼容性原则

```text
manual API key -> 现有 API key 鉴权与计费路径
legacy sk_oauth_* -> 兼容读取，按现有元数据执行 scope/group/revocation
v3 OAuth token -> hash-only OAuth credential -> 加载 billing principal -> 现有 gateway/计费
```

只有 OAuth token 受 OAuth scope、audience、grant 和 token-family 约束。普通 API key 不因本项目改变现有权限语义。

### 1.2 优先级

优先级从高到低为：

1. 凭据与密钥泄露半径。
2. issuer、redirect、consent、session 和 token 生命周期正确性。
3. 开发者及管理员治理闭环。
4. conformance、可观测性和应急处置。
5. PAR、DPoP、`private_key_jwt`、JAR/JARM 等增强安全配置。
6. 独立的工作负载 OIDC。

## 2. 标准基线与安全配置档

### 2.1 规范性基线

| 标准 | 用途 | 本项目状态 |
| --- | --- | --- |
| RFC 6749 | OAuth 2.0 核心 | 已实现子集，逐步由现代 BCP 收紧 |
| RFC 6750 | Bearer Token | 已实现；v3 继续使用 opaque bearer token |
| RFC 7636 | PKCE | 已实现；仅允许 S256 |
| RFC 7009 | Token Revocation | 已实现 |
| RFC 7662 | Token Introspection | 协议代码已实现；当前 HTTP/Ent 链路存在上线阻断缺陷，见 3.3、3.6 |
| RFC 8414 | Authorization Server Metadata | 已实现 |
| RFC 8252 | Native Apps | 已实现 loopback 端口例外；需继续收紧自定义 scheme |
| RFC 8628 | Device Authorization Grant | 已实现 |
| RFC 8707 | Resource Indicators | 目标能力 |
| RFC 9126 | Pushed Authorization Requests | 增强配置档必需 |
| RFC 9207 | Authorization Server Issuer Identification | 目标能力，防 mix-up |
| RFC 9449 | DPoP | 增强配置档目标能力 |
| RFC 9700 | OAuth 2.0 Security BCP | 所有新实现的安全基线 |
| RFC 7523 | JWT Client Authentication | `private_key_jwt` 目标能力 |
| RFC 8725 | JWT BCP | 所有 JWT 解析与签名必须遵循 |
| RFC 9101 | JAR | FAPI/消息签名配置档能力 |
| OpenID Connect Core 1.0 Errata 2 | 身份层 | 已实现核心 code-flow 子集 |
| OIDC Discovery 1.0 | OP Discovery | 已实现 |
| OIDC RP-Initiated Logout 1.0 | RP logout | 部分实现；缺少真正 OP session 语义 |
| OIDC Front-/Back-Channel Logout 1.0 | 会话传播 | 部分实现；缺少 session-to-RP 映射与可靠投递 |
| FAPI 2.0 Security Profile | 高价值 API 安全配置 | 仅适用于独立 FAPI 逻辑 AS；canonical issuer 的 Enhanced 档不宣称合规 |
| FAPI 2.0 Message Signing | JAR/JARM/签名 introspection | 按客户需求启用 |

OAuth 2.1 在本文档日期仍是 Internet-Draft。项目可以遵循其方向，但在标准正式发布且完成 conformance 之前，不对外宣称“OAuth 2.1 certified”。

### 2.2 Sakrylle 安全配置档

#### Standard

面向 Web、Desktop、Mobile 和 CLI：

- Authorization Code。
- 所有客户端强制 S256 PKCE。
- exact redirect URI；native loopback 仅端口可变。
- opaque short-lived access token。
- refresh rotation 与 family replay detection。
- OIDC code flow、nonce、UserInfo 和标准 logout。

#### Enhanced（FAPI-inspired）

面向高价值 confidential client：

- Standard 的全部要求。
- PAR。
- `private_key_jwt` 或 mTLS client authentication。
- DPoP 或 mTLS sender-constrained access token。
- Resource Indicators 与严格 audience。
- 可选 JAR/JARM。

Enhanced 复用 canonical issuer，仍与 public client、Device Flow 和普通 bearer token 共存，因此只能描述为“FAPI-inspired”。即使单项控制与 FAPI 2.0 相同，也禁止把该配置档宣传为“FAPI compliant”或“FAPI certified”。

#### FAPI 2.0

如果业务确实需要 FAPI 2.0 合规，必须建设独立逻辑 Authorization Server：

- 使用独立 issuer、discovery metadata、signing keys、client registration policy 和 conformance 环境；可以复用底层用户目录，但不得与 canonical issuer 共享协议能力广告。
- 只接受 confidential client；所有授权请求必须先通过 client-authenticated PAR。
- client authentication 只允许 `private_key_jwt` 或 mTLS；access token 必须通过 DPoP 或 mTLS sender-constrained。
- authorization code 生命周期不超过 60 秒，PAR `expires_in` 小于 600 秒。
- JWT 算法只允许 `PS256`、`ES256` 或 Ed25519 `EdDSA`；禁止把 Standard 档的 `RS256` 带入 FAPI profile。
- 不执行日常 refresh token rotation；refresh token 必须 sender-constrained，并按 FAPI 2.0 的 replay、撤销和异常轮换规则处理。该规则与 Standard 档的每次轮换策略分开实现。
- 只有独立 issuer 通过 FAPI 2.0 Final 对应 conformance profile 后，产品、文档和 discovery 周边材料才可以使用合规字样。

#### Workload

面向 CI/CD、自动化 runner 和云身份交换：

- 独立 issuer、独立 signing keys、独立 audience policy。
- 5 至 10 分钟 JWT assertion。
- 不存在用户 consent、refresh token 或 UserInfo。
- `sub` 使用不可变项目、工作流、环境和运行实例标识。
- 不复用 Standard、Enhanced 或 FAPI access token。

### 2.3 明确禁止

- Implicit Grant。
- Resource Owner Password Credentials Grant。
- public native/mobile/CLI 客户端内置静态 client secret。
- `code_challenge_method=plain`。
- redirect URI wildcard。
- 未注册的任意 post-logout redirect。
- 在 URL query 或 fragment 中传递 access token/refresh token。
- ID Token 携带余额、分组、配额、角色、内部权限或其他可变业务状态。
- 记录 plaintext access token、refresh token、authorization code、device code、PKCE verifier、client secret 或私钥。
- 对公网开放无审核、无所有权约束的 Dynamic Client Registration。

## 3. 当前实现基线

本节描述 2026-08-15 仓库状态。它是迁移输入，不代表所有行为已经达到目标态。

### 3.1 代码所有权

| 层 | 主要文件/目录 | 职责 |
| --- | --- | --- |
| Route | `backend/internal/server/routes/oauth.go`、`oauth_device.go` | 公共协议端点与用户授权管理路由 |
| Handler | `backend/internal/handler/oauth_provider*.go`、`oauth_device_handler.go` | HTTP 解析、OAuth 错误、consent/device 页面 |
| Service | `backend/internal/service/oauth_provider_service.go`、`oauth_device_service.go` | 授权、token mint/rotate/revoke、分组策略 |
| OIDC | `backend/internal/service/oidc_*.go` | ID Token、claims、pairwise、request object、logout、key rotation |
| Repository | `backend/internal/repository/oauth_provider_repo.go` 等 | Ent/PostgreSQL 原子读写和锁 |
| Schema | `backend/ent/schema/oauth_*.go` | 客户端、code、token、transaction、device 状态 |
| Middleware | `backend/internal/server/middleware/oauth_*.go` | OAuth token 元数据、scope 和 resource error |
| Frontend | `frontend/src/views/user/AuthorizedAppsView.vue` | 用户查看和撤销已授权应用 |
| Reconcile | `backend/cmd/oauth-reconcile` | legacy access metadata 检查和回填 |
| Ops | `backend/scripts/oidc-*.sh`、`docs/ops/identity-email.md` | 初始化、轮换、验证和生产事实 |

Wire provider 顺序不得破坏。任何 provider 变化都必须重新生成 `backend/cmd/server/wire_gen.go`，并确认 `OIDCKeyRotationScheduler` 仍被 cleanup 持有和停止。

### 3.2 当前公共端点

```text
GET  /.well-known/oauth-authorization-server
GET  /.well-known/openid-configuration
GET  /.well-known/jwks.json
GET  /userinfo
POST /userinfo

GET  /oauth/authorize
POST /oauth/authorize
POST /oauth/token
POST /oauth/revoke
POST /oauth/introspect

POST /oauth/device/code
GET  /oauth/device

GET  /oauth/logout
POST /oauth/logout
GET  /oauth/frontchannel-logout
```

当前用户管理端点：

```text
POST   /api/v1/oauth/authorize/begin
POST   /api/v1/oauth/authorize/approve
POST   /api/v1/oauth/device/approve
POST   /api/v1/oauth/device/deny
GET    /api/v1/oauth/authorized-apps
DELETE /api/v1/oauth/authorized-apps/:grant_id
DELETE /api/v1/oauth/authorized-apps/client/:client_id
```

`/api/v1/oauth/grants` 是兼容接口，不再作为新客户端契约。

### 3.3 当前能力矩阵

| 能力 | 状态 | 说明 |
| --- | --- | --- |
| Authorization Code | 已实现 | server-side authorize transaction，code hash-only、单次消费 |
| S256 PKCE | 已实现 | public/confidential 均强制 |
| Refresh Rotation | 已实现 | hash-only refresh token，family reuse detection |
| Device Flow | 已实现 | pending/approved/denied/consumed/expired 状态机 |
| Revocation | 已实现 | access/refresh 与 cache invalidation |
| Introspection | 阻断缺陷 | handler 强制 `client_confidential=true`，但 Ent schema/mapper 不提供该值，真实 HTTP + Ent 链路会把客户端视为非 confidential |
| OAuth/OIDC Discovery | 部分实现 | issuer 可由设置解析，但仍有 Host fallback 且 prompt/claims 广告超前 |
| RS256/ES256 JWKS | 部分实现 | 当前和 grace-period 旧 key 同时发布；初始化失败与多实例轮换需修复 |
| ID Token | 部分实现 | nonce、auth_time、sid、at_hash/c_hash、claim allowlist 已有；当前 auth_time/sid 不对应真实 OP session |
| UserInfo | 已实现 | 依据 OIDC scope 裁剪 |
| Pairwise Subject | 部分实现 | client 可配置；当前无密钥 SHA-256 派生可枚举且会随 redirect sector 变化 |
| Claims Parameter | 部分实现 | 局部解析存在，但 begin/transaction/token/UserInfo 未端到端贯穿 |
| Request Object | 部分实现 | fetch/parse 代码存在，但 discovery 正确地不宣称支持 |
| Front-/Back-Channel Logout | 部分实现 | 缺少 OP session 到 RP session 的权威映射；back-channel delivery 仍需安全队列 |
| Consent Reuse | 部分实现 | 第三方 `prompt=none` 尚未正确复用历史 consent；scope 省略时 UI 与实际 default scopes 可能不一致 |
| Developer App Portal | 未实现 | 当前注册依赖 migration、SQL 和 reconcile |
| 多 Client Secret | 未实现 | 当前每 client 单一 bcrypt hash |
| Hash-only Access Token | 未实现 | plaintext 存在 `api_keys.key` |
| PAR | 未实现 | Enhanced 档必需 |
| DPoP/mTLS | 未实现 | Enhanced 档目标 |
| `private_key_jwt` | 未实现 | Enhanced 档目标 |
| Resource/Audience Binding | 未实现 | 当前主要依赖 scope + group |
| Resource Scope Enforcement | 配置风险 | 新部署默认关闭，OAuth token 可能退化为同 group 的广权限 API key |
| 标准 conformance 流水线 | 未实现 | 现有测试丰富，但不是认证套件 |

### 3.4 当前持久化模型

- `oauth_clients` 保存技术客户端、URI/scopes、secret hash、TTL、group、OIDC 和 logout 配置。
- `oauth_codes` 保存 authorization code SHA-256、PKCE、nonce、sid 和 grant 信息。
- `oauth_authorize_transactions` 保存用户绑定的 authorize 请求、CSRF hash 和 consent 页面快照。
- `oauth_refresh_tokens` 保存 refresh hash、rotation/family、device、group 和 replay 信息。
- `oauth_access_tokens` 只保存元数据，真正 plaintext access token 仍在 `api_keys.key`。
- `oauth_device_codes` 保存 device/user code hash 和设备流状态机。
- migration 和 service interface 已为 `oauth_grants` 预留 user/client consent，但缺少 Ent schema、repository、Wire 注入和 approve 写入，当前并未形成可复用的 consent 权威存储；它也不应与每设备 token grant 混为一个概念。
- OIDC 私钥以 AES-256-GCM ciphertext 保存于 `security_secrets`，运行时解密进内存。

### 3.5 生产兼容约束

- canonical issuer 必须保持 `https://oidc1.sakrylle.com`。
- `platform.sakrylle.com` 可以暴露相同路由，但 discovery 和 ID Token `iss` 仍返回 canonical issuer。
- Sakrylle Studio 使用随机 loopback port，但 callback path 必须精确匹配 `/callback`。
- Sakrylle Web 使用用户自己的 OAuth token 调 gateway，不允许回退到共享静态 API key。
- v2 客户端必须配置 `default_group_id`；不得重新依赖全局 fallback。
- scope/group 变更后，旧 token 不自动获得更大权限，用户需要重新授权或 refresh 到允许范围内。

### 3.6 当前最高优先级缺口

1. 数据库或备份泄露会暴露仍有效的 plaintext OAuth access token；当前默认 access token TTL 又是 24 小时。
2. 交互式授权依赖 SPA JWT/localStorage 桥接，缺少独立、HttpOnly 的 OP browser session；XSS 可以直接读取长期登录凭据。
3. code exchange 在完成 client、redirect、PKCE 校验前就可能消费 code，token rows 又可能在 ID Token 签名前写入；失败会留下已烧毁 code 或客户端从未收到的 active token。
4. refresh rotation 的旧 refresh 消费、旧 access 撤销和新 token mint 不在同一事务；并发或中途失败不能保证单一结果。
5. `/oauth/introspect` 的 handler 要求 `client_confidential=true`，migration 和 service DTO 虽有该字段，Ent `oauth_client` schema 与 repository mapper 却未映射，导致生产 HTTP 链路基本不可用；现有测试只覆盖 service，未覆盖 handler + Ent。service 声称支持 refresh token introspection，实际 lookup 只覆盖 plaintext `api_keys`，discovery 也未发布 `introspection_endpoint`。
6. authorize 请求省略 scope 时，consent 页面硬编码展示 `image_generation`，而 begin/approve 服务会回退并签发 `client.default_scopes`；用户看到的权限可能少于真正授权的权限。这是上线阻断项，页面必须展示 server canonical scope snapshot。
7. 当前 `auth_time` 取 authorization code 创建时间而非真实认证时间，普通 Sakrylle JWT refresh 又会刷新 `iat`；recent-auth、`max_age` 和 step-up 判断可以被错误延长。
8. client registration 不具备所有者、审批、审计、多 credential 和自助轮换闭环。
9. discovery 宣称的 `prompt=login/consent/select_account` 和 claims 能力没有全部端到端实现，存在 capability advertisement 漂移；第三方 prior consent 也不能正确支持 `prompt=none`。
10. pairwise `sub` 当前是公开 sector 与可枚举 user ID 的无密钥散列，并可能在 redirect URI 变更后改变，不满足长期稳定和抗枚举目标。
11. OIDC signing key 与其他 secret 复用同一 SecretEncryptor 根配置；key 初始化失败仍可继续启动，多实例轮换缺少 leader/CAS，爆炸半径和错签风险都过大。
12. issuer 仍有基于请求 Host/forwarded header 的 fallback；生产必须改为显式配置缺失即 readiness 失败。
13. remote request URI、sector URI、JWKS URI 和 back-channel logout 是 SSRF 面；当前 logout 使用普通 HTTP client 和无界 goroutine，缺少出站网络策略与背压。
14. `sid`/`jti` 等安全标识在 CSPRNG 失败时存在时间值 fallback；随机源失败必须失败关闭。
15. OAuth Provider 默认开启，但资源端 scope enforcement 在新部署默认关闭；这会让 OAuth token 退化为同 group 的广权限 API key。生产启用顺序、reconcile 前置检查和 fail-closed runbook 尚未形成文档闭环。
16. 动态 CORS 忽略 client 的 `allowed_origins`，从全部 enabled client 的 redirect URI 推导并注入全局中间件；必须改为只在必要协议端点按 exact registered origin 决策。
17. cleanup 只覆盖 authorize transaction/device code；expired code、access/refresh metadata、browser/consent PII 缺少实际执行的 retention lifecycle。
18. Device verification 的公开限流缺少稳定的 `client_id` bucket，对任意错误 user code 也无法做 row-level 计数，暴力猜测防护主要依赖 IP。
19. Authorized Apps 当前只聚合仍有 active access token 的 grant，不能作为完整授权历史；UI flag `oauth_v2_ui_enabled` 也只有 migration 默认值而无读取方。
20. 现有大量协议代码为自研校验，HTTP“E2E”主要使用内存 stub，部分 prompt/logout 测试被跳过；必须用真实 PostgreSQL/Redis、标准 conformance 与成熟引擎降低长期风险。

## 4. 目标与验收结果

### 4.1 产品目标

- 第三方开发者可以创建应用和多个平台客户端。
- 用户在 consent 页面清晰看到开发者、验证状态、权限、资源、分组和设备。
- 用户可以按设备、grant 或整个 client 撤销授权。
- 管理员可以审核、暂停、紧急吊销应用及其所有 token。
- first-party 客户端保持兼容且可逐客户端迁移到新协议引擎。
- confidential client 可以无停机轮换多个 secret/JWK。
- 高价值客户可以选择 PAR、`private_key_jwt` 和 DPoP。

### 4.2 安全目标

- 数据库、Redis dump 或普通备份泄露不直接产生可使用的 OAuth credential。
- redirect、code、refresh、device 和 logout 流程可抵御 replay、mix-up、open redirect、CSRF 和 client impersonation。
- access token 权限恒等于用户当前权限、consent、client allowlist、resource/audience 和 group policy 的交集。
- 单个 credential、token family、client、用户或 signing key 均可独立吊销。
- signing key 轮换期间旧 ID Token 可验证，新 token 只使用 active key。
- 所有安全事件有结构化、去敏、可关联的审计记录。

### 4.3 可运维目标

- 变更可逐 client 灰度，不需要改变 issuer。
- 新旧 token 可双读；新发行可独立关闭。
- Redis、DB、KMS、clock skew 和 key rotation 故障均有明确失败模式与告警。
- conformance、负向安全测试、迁移检查和前端关键流程纳入 CI/release gate。

## 5. 非目标

本阶段不包含：

- 通用 IAM、企业目录或完整 SCIM 生命周期。
- SAML Identity Provider。
- 任意第三方可匿名调用的 RFC 7591 Dynamic Client Registration。
- 把 access token 改成自包含 JWT；Sakrylle 需要即时撤销和实时 group/billing policy，继续使用 opaque token。
- 在 ID Token 中镜像所有用户或业务字段。
- 为每个模型创建 scope；模型访问继续由 group、channel 和 runtime policy 控制。
- 在第一阶段开放 `client_credentials` 代表任意用户。机器身份由后续 Sakrylle Apps/installation token 设计承载。
- 将 GitHub Actions 风格 workload OIDC 与用户 OIDC 共用 issuer。

## 6. 术语、产品边界和信任域

### 6.1 OAuth Application

面向用户展示和治理的产品对象。包含名称、描述、图标、开发者、主页、隐私政策、条款、验证状态和审核状态。

一个 application 可以包含多个 client，例如 Web、Desktop、Mobile 和 CLI。用户授权的是 application 的某个 client，但 UI 应聚合展示 application 身份。

### 6.2 OAuth Client

协议技术对象，拥有唯一 `client_id`、client type、redirect URI、allowed scopes、token profile 和认证方法。

Client type 创建后不可原地从 public 改成 confidential，反之亦然。需要改变类型时创建新 client 并显式迁移。

### 6.3 Consent

用户对一个 client 的一组 scope、resource 和授权细节作出的长期许可。Consent 不等于某个 access token，也不等于某台设备的 token family。

### 6.4 Authorization Grant

一次完成的用户授权实例。它绑定 user、client、consent version、device、group/resource snapshot，并拥有稳定 `grant_id`。

### 6.5 Token Family

某个 grant 下 refresh token 的 rotation chain。refresh reuse 将 family 标记为 compromised，并撤销该 family 下全部 access/refresh token。

### 6.6 Browser SSO Session

OP 域上的交互式登录会话。它独立于 API JWT、OAuth grant 和 RP 自己的 session，保存 `sid`、真实 `authenticated_at`、`acr/amr`、绝对/空闲过期及撤销状态；OIDC `auth_time` claim 直接由 `authenticated_at` 生成。

### 6.7 Sakrylle App

类似 GitHub App 的安装型应用，可代表 installation 或代表用户执行操作，使用细粒度权限和短期 token。它是 OAuth Application 之上的后续产品，不通过扩大传统 OAuth scope 来模拟。

### 6.8 Workload Identity

CI/CD 或自动化运行实例的短期身份断言。它不是用户 token，不能调用 UserInfo，也不能获得 refresh token。

## 7. Scope、权限和 consent 模型

### 7.1 当前 canonical scope registry

| Scope | 语义 | 风险等级 |
| --- | --- | --- |
| `openid` | 请求 OIDC 身份层和 ID Token | 低 |
| `profile` | OIDC 基本资料 claims | 低 |
| `email` | OIDC email/email_verified claims | 中 |
| `profile:read` | Sakrylle API 用户档案 | 低 |
| `email:read` | Sakrylle API 邮箱字段 | 中 |
| `account:read` | 账户、当前/可用分组和能力摘要 | 中 |
| `account:balance:read` | 余额与展示币种 | 中 |
| `models:read` | 当前授权范围内模型列表 | 低 |
| `chat.completions:create` | 调用 Chat Completions | 高/计费 |
| `responses:create` | 调用 Responses/Codex | 高/计费 |
| `messages:create` | 调用 Messages | 高/计费 |
| `images:create` | 生成和编辑图片 | 高/计费 |
| `usage:read` | 读取用量 | 中 |
| `offline_access` | 允许发行 refresh token | 高/持久访问 |

OIDC `profile`/`email` 与业务 API 的 `profile:read`/`email:read` 必须继续保持独立，禁止通过 alias 暗中扩权。

Legacy alias：

| Legacy | Canonical |
| --- | --- |
| `image_generation` | `images:create` |
| `balance:read` | `account:balance:read` |

新 authorize 请求最终应只接受 canonical scope；历史 token 在 family 自然过期前继续按 alias 兼容。

### 7.2 有效权限公式

```text
effective scopes = requested scopes
                 ∩ client allowed scopes
                 ∩ user consented scopes
                 ∩ application review policy
                 ∩ resource-server route policy

effective resources = requested resources
                    ∩ client allowed resources
                    ∩ user-selected resources/groups
                    ∩ current user entitlement
```

任何一个集合不可解析、元数据不一致或 policy repository 不可用时必须 fail closed。

### 7.3 Endpoint scope policy

当前 `OAuthScopePolicyForRequest` 通过 method/path matrix 默认拒绝未列出的 OAuth route。目标态将 policy 绑定到 route registration，而不是依赖一份容易漂移的全局 regex 表。

目标要求：

- 每个接受 OAuth token 的 gateway route 在注册时声明 `resource_id` 和至少一个 required scope。
- 每个 API-key-authenticated route 在启动测试中被枚举；未声明 OAuth policy 的 route 对 OAuth token 默认拒绝。
- 新 route 的测试必须同时证明 manual API key 行为和 OAuth scope 行为。
- alias route 与 canonical route 使用同一个 policy object。
- `openid` 本身不授权业务 API，只授权身份层端点。

### 7.4 风险分级和 step-up

以下变化必须重新 consent：

- 新增 scope。
- scope 从 read 升级为 create/write。
- 新增 resource/group。
- 开启 `offline_access`。
- application publisher、所有者或隐私政策发生安全相关变化。
- consent policy version 更新。

高风险 scope、组织资源或长期离线访问可以要求近期 MFA/passkey。Step-up 结果以真实 `authenticated_at`、`acr`、`amr` 写入 browser session，不信任客户端自报，也不从 API token `iat` 推导。

### 7.5 Consent 复用

- third-party client 只有在现存 consent 覆盖全部 requested scope/resource 且 policy version 未过期时才能跳过 consent。
- `prompt=consent` 永远显示 consent。
- `prompt=none` 缺少 login 返回 `login_required`，缺少 consent 返回 `consent_required`，需要选分组或 step-up 返回 `interaction_required`。
- `trusted_first_party` 不是无限扩权开关。它最多允许审核过的稳定 baseline bundle 自动 consent；新增高风险权限仍需交互确认。
- 权限缩减可以静默生效，但必须反映在新 token 中。

## 8. Application、Client 与开发者治理

### 8.1 Application 所有权

第一阶段支持：

- `system`：Sakrylle first-party application，由管理员和 migration 管理。
- `user`：由已验证邮箱的普通用户拥有。

`organization` 作为保留类型，只有仓库拥有正式组织/团队权限模型后才能启用，禁止先用字符串 owner 绕过授权。

### 8.2 生命周期

Application：

```text
draft -> pending_review -> active -> suspended -> active
  |            |             |
  +----------> rejected      +-> deleted
```

Client：

```text
active -> disabled -> active
active -> retired
```

Credential：

```text
active -> grace -> revoked
active -> expired
```

状态转换必须通过 service 执行并写安全审计；不得允许 handler 直接更新 Ent entity。

### 8.3 开发态与公开态

- `draft` application 只能由 owner 和显式 test user 授权。
- 回调仅允许 loopback 或已验证 owner domain 的 HTTPS 地址。
- 申请公开第三方使用时进入 `pending_review`。
- 审核至少检查名称/图标仿冒、主页、隐私政策、redirect、scope 风险、token profile 和开发者联系方式。
- `active` 后的高风险变更重新进入 review；旧配置在审批前继续生效或由管理员选择立即冻结。
- `suspended` 立即禁止新 authorize/token refresh，并按处置选项撤销现有 token。

### 8.4 Client 类型

| 类型 | 认证 | Redirect | Refresh 存储 |
| --- | --- | --- | --- |
| Web confidential/BFF | `client_secret_basic` 或 `private_key_jwt` + PKCE | HTTPS exact | 服务端加密存储 |
| Browser public SPA | none + PKCE | HTTPS exact | 默认不发行；推荐 BFF |
| Desktop/CLI public | none + PKCE | loopback exact path，随机端口 | OS keychain/权限受限文件 |
| Mobile public | none + PKCE | claimed HTTPS 或已审核 custom scheme | Keychain/Keystore |
| Service/installation | asymmetric auth | 无用户 redirect | 独立 Sakrylle Apps 设计 |

### 8.5 Redirect URI 规则

- Web client 只允许 `https`，localhost 开发例外必须处于 draft/test client。
- 完整字符串 exact match，包括 scheme、host、path、query 和大小写语义。
- native loopback 仅允许 `127.0.0.1`、`[::1]` 或明确兼容的 `localhost`，且只有 port 可变化，path 必须 exact。
- 禁止 fragment、userinfo、wildcard、非默认隐式端口重写和 open redirect parameter。
- custom scheme 必须满足反向域名命名并通过平台归属审核；优先 claimed HTTPS/app link/universal link。
- redirect 变更必须使正在进行的 authorize transaction 失效或在消费时重新校验 registration version。

### 8.6 品牌与外部 URL

- icon 不直接引用任意第三方 URL。上传后由 Sakrylle 存储/代理，避免 consent 页面 tracking 和恶意内容替换。
- homepage、privacy、terms 必须 HTTPS，进行 SSRF 安全校验和域名所有权检查。
- consent 页面展示 verified publisher，不把 application name 当作可信身份。

## 9. Group、Resource、Audience 与计费

### 9.1 Group 仍是运行时业务约束

Access token 可以绑定一个 primary group，同时保存用户 consent 的 `allowed_groups_snapshot`。请求中的 `<group_id>:<model>` 只能在以下条件全部满足时切换：

- token scope 允许该 API。
- group 位于 consent snapshot。
- client 当前允许该 group。
- 用户当前仍可使用该 group。
- group active、未删除，且符合 subscription/exclusive policy。

Snapshot 防止客户端获得用户未同意的 group；runtime intersection 防止用户失权后旧 token 继续扩权。

### 9.2 Resource Indicators

目标态采用 RFC 8707：

- authorization 和 token 请求可以携带 `resource`。
- 每个 client 注册 allowed resource URI。
- access token 保存单一 primary resource；默认不发行 broad multi-audience token。
- resource server 检查 token resource/audience 后才执行 route scope。
- 未请求 resource 时仅对 first-party legacy client 使用受控默认值；第三方 client 必须显式或由 registration 唯一推导。

正式 resource URI 由部署配置注册。示例 `https://platform.sakrylle.com/` 不是可在代码中硬编码的跨环境常量。

### 9.3 计费不进入 OIDC claims

- 余额、倍率、group、quota 和模型能力只通过受 scope 保护的 API 返回。
- ID Token 和标准 UserInfo 不作为计费状态缓存。
- access token 验证后构造 billing principal，继续走现有 group、channel、rate limit、usage log 和利润控制。
- currency 仍是展示语义 `￥`，不转换数值。

## 10. 目标数据模型

所有 schema 变更使用 next available migration number，forward-only、可重复执行，并同步生成/提交 Ent 代码。下面是逻辑模型，不提前锁死 migration 编号。

### 10.1 `oauth_applications` 新表

| 字段 | 约束/用途 |
| --- | --- |
| `id` | bigint PK |
| `public_id` | 随机稳定外部 ID，unique |
| `slug` | 展示用途，unique，可审计变更 |
| `owner_type` | `system`、`user`，未来可加 `organization` |
| `owner_id` | owner_type 对应 ID；system 可空 |
| `name`、`description` | consent/developer UI |
| `icon_asset_id` | 受控媒体资产，不存任意 URL |
| `homepage_url`、`privacy_url`、`terms_url` | HTTPS、审核 |
| `publisher_name`、`publisher_verified_at` | 发布者身份 |
| `status` | draft/pending_review/active/rejected/suspended/deleted |
| `review_version` | 防止审核后配置偷换 |
| `created_at`、`updated_at`、`deleted_at` | 生命周期 |

索引至少覆盖 owner、status、slug。软删除 application 不得复用原 public ID。

### 10.2 扩展 `oauth_clients`

保留当前 `client_id` 和已上线字段，新增：

| 字段 | 用途 |
| --- | --- |
| `application_id` | 关联 application；legacy client 回填 system app |
| `status` | active/disabled/retired，逐步替代单一 bool |
| `protocol_engine` | `legacy` 或 `fosite`，用于逐 client 迁移 |
| `security_profile` | standard/enhanced |
| `token_endpoint_auth_method` | none/basic/private_key_jwt/tls_client_auth |
| `require_par` | enhanced client 强制 |
| `require_dpop` | 是否 sender-constrain |
| `allowed_resources` | 过渡期 JSON；后续可规范化 |
| `registration_version` | redirect/scope/credential 配置版本 |
| `consent_policy_version` | 触发重新 consent |
| `first_party_baseline_version` | 限制 trusted auto-consent |

现有 `client_type` 必须成为 write-once invariant。`pkce_required` 对新 client 永远为 true，保留字段只为 legacy 读取。

### 10.3 `oauth_client_credentials` 新表

| 字段 | 用途 |
| --- | --- |
| `id`、`public_id` | 内部/外部 credential 标识 |
| `client_id` | FK/逻辑绑定 |
| `type` | secret/private_jwk/jwks_uri/mtls_certificate |
| `selector` | secret 格式中的非敏感查找 ID |
| `verifier_hash` | HMAC-SHA-256 或版本化 verifier；不存 plaintext |
| `hash_key_id` | verifier pepper/key 版本 |
| `public_jwk`、`jwks_uri`、`certificate_thumbprint` | 非对称认证材料 |
| `status` | active/grace/revoked/expired |
| `created_by`、`created_at`、`expires_at` | 生命周期 |
| `last_used_at`、`last_used_ip_hash` | 检测陈旧 credential |
| `revoked_at`、`revoked_by`、`revoke_reason` | 审计 |

每个 client 同时 active/grace secret 数量设上限，默认 2。创建 secret 只返回一次 plaintext。

Legacy `client_secret_hash` 以 `legacy_bcrypt` credential 兼容读取，用户轮换后撤销并最终清空旧列。

### 10.4 URI registrations

短期可继续读取 `oauth_clients` JSON 数组，但 developer portal 目标态使用规范化表：

- `oauth_client_redirect_uris`
- `oauth_client_logout_uris`
- `oauth_client_origins`
- `oauth_client_request_uris`

共同字段包括 `client_id`、normalized/exact URI、type、verified_at、registration_version、created_by 和 timestamps。

迁移期写操作双写规范化表和 JSON snapshot；读取先新后旧。完成两个最大 refresh lifetime 后才删除旧写路径。

### 10.5 `oauth_authorize_transactions`

继续作为安全边界，新增或确认：

- `application_id`、`client_registration_version`。
- `resource`/authorization details。
- `consent_policy_version`。
- `browser_session_id`、`authenticated_at`、`acr`、`amr`。
- `dpop_jkt`。
- `request_uri_id`（PAR 时使用）。

消费时必须在同一数据库事务和 row lock 下重新检查 user、client、redirect、registration version、CSRF、PKCE、scope/resource 和过期时间。

### 10.6 `oauth_consents`

当前 migration 和 service interface 把 `oauth_grants` 定义为 consent 记录，但持久化与调用链尚未接通。目标可以补齐后安全迁移/重命名，或创建新表，字段至少包括：

- `user_id`、`client_id`、`application_id`。
- normalized scopes 与 stable scope hash。
- resources/authorization details。
- consent policy/application review version。
- `granted_at`、`last_used_at`、`expires_at`、`revoked_at`。
- `authenticated_at`、`acr`、`amr`。

唯一约束不能只使用 `(user_id, client_id)`，必须能表达 scope/resource version 或由 service 原子 upsert 最新有效 consent。

### 10.7 `oauth_authorization_grants` 新表

将当前散落在 access/refresh 元数据中的 `grant_id` 提升为一等对象：

- `grant_id` UUID/public ID。
- user/client/application/consent IDs。
- device ID/name、primary group、allowed groups/resources snapshot。
- `status` active/revoked/expired。
- created、last_used、revoked metadata。

用户“撤销一台设备”操作以此表为 ownership anchor，并级联撤销 family 和 access token。

### 10.8 `oauth_token_families` 新表

- `family_id`、`grant_id`、client/user。
- status active/compromised/revoked/expired。
- absolute expiry、idle expiry、last rotated/used。
- DPoP thumbprint 或 mTLS certificate binding。
- reuse detected 时间、IP/UA 风险摘要。

Refresh row 保留每一代 token hash 和 `rotated_to_id`，family 表负责 O(1) family 状态检查。

### 10.9 Hash-only v3 access token

目标 token 格式：

```text
sk_oauth_v3_<token_id>_<secret>
```

- `token_id` 至少 128 bit 随机、base64url，无保密要求，用于索引查找。
- `secret` 至少 256 bit CSPRNG，base64url。
- 数据库保存 `token_id` 和 `HMAC-SHA-256(verifier_key, canonical_token)`，不保存 plaintext。
- `hash_key_id` 标明 verifier key 版本，比较使用 constant-time。
- token response 是 plaintext 唯一可见位置。

扩展 `oauth_access_tokens`：

- `token_version`、`token_id`、`verifier_hash`、`hash_key_id`。
- `resource`/audience、`dpop_jkt`。
- `authorization_grant_id`、`token_family_id`。
- current scope/group snapshot、issued/expires/revoked/last-used。

为兼容现有 gateway，OAuth token 仍可以关联一个 `api_key_id` 作为 billing principal，但 `api_keys.key` 不得再存可使用的 bearer secret。目标实现有两个可接受路径：

1. 增加 `api_keys.credential_kind=oauth_shadow`，其内部 key 永远不能走 manual key lookup；OAuth authenticator 验证 v3 token 后按 `api_key_id` 加载 principal。
2. 抽取通用 `BillingPrincipal`，OAuth 不再依赖 APIKey entity。

第一阶段采用路径 1，降低 gateway/计费改造范围；长期演进到路径 2。

### 10.10 Browser sessions

新增 `oidc_browser_sessions`：

- opaque session ID 的 hash，不存 cookie plaintext。
- user ID、`sid`、`authenticated_at`、acr/amr。
- created/last_seen/idle_expires/absolute_expires/revoked。
- IP/UA 风险摘要和 session version。

浏览器只持有 `Secure; HttpOnly; SameSite=Lax` cookie。OAuth authorize 不再依赖把长期 API JWT 暴露给 consent 页面 JavaScript。

### 10.11 PAR requests

新增 `oauth_pushed_authorization_requests`：

- 随机 `request_uri` handle 的 hash/ID。
- client、canonical request parameters、registration version。
- created/expires/consumed。
- optional request-object JTI、DPoP binding。

TTL 默认 90 秒且单次消费。禁止通过 PAR 绕过 client redirect/scope/resource 校验。

### 10.12 Security audit

新增 append-only `oauth_security_events`：

- event ID/type/version/occurred_at。
- actor type/id、target application/client/grant/family/credential/key IDs。
- outcome/reason、trace ID、request ID。
- source IP 的受控加密或前缀化值、UA 摘要。
- allowlisted JSON metadata。

禁止存 credential、authorization code、完整 token、私钥、完整 claims request 或任意请求 body。

### 10.13 Pairwise subject mapping

新增 `oidc_pairwise_subjects`，或使用等价的持久化映射：

- 主键/唯一约束绑定 `issuer_id + user_id + sector_id`。
- `subject` 是首次签发时生成的至少 128 bit 随机 opaque 值；数据库只向 claims service 暴露映射，不把内部 user ID 编入结果。
- application 改名、redirect URI 增删、client credential/key 轮换以及 signing key 轮换都不得改变已签发 `subject`。
- sector 变更属于受审核的数据迁移，不能在普通 client update 中隐式发生。

带独立、永久、不可轮换语义的 pairwise HMAC key 也可以作为实现方案，但必须证明迁移、备份恢复和灾难恢复后输出稳定。默认选择持久随机映射，避免 user ID 可枚举和 key 丢失导致全量 `sub` 改变。

## 11. Service 架构与契约

### 11.1 分层

```text
Gin Handler
  -> OAuthProtocolEngine (标准解析、校验、错误和 response)
  -> OAuthPolicyService   (用户、consent、scope、resource、group、step-up)
  -> OAuthTokenService    (code/token/family 原子状态机)
  -> OIDCClaimsService    (sub、claims、ID Token、UserInfo、session/logout)
  -> ClientManagementService
  -> Ent repositories / Redis cache / KMS signer
```

Handler 只负责 HTTP boundary，不直接执行 bcrypt、查 client secret 或拼装 JWT。所有状态转换由 service 控制。

### 11.2 协议引擎决策

推荐使用 ORY Fosite 作为嵌入式 OAuth 2.0/OIDC 协议引擎，而不是继续扩大自研 parser/validator，也不在第一阶段拆出 Hydra/Keycloak 独立服务。

原因：

- Fosite 可以嵌入当前 Go/Gin/Ent 单体，保留 Sakrylle 的用户、group、billing 和 consent UI。
- 外置 Authorization Server 会引入双向 identity、session、group 和 single-token gateway 同步。
- 协议引擎可以提供经过广泛测试的 authorize/token/client auth/PKCE/PAR 错误语义。
- Sakrylle 仍需实现 prompt、登录、claims、consent、group 和部分 logout policy；使用库不等于免除 conformance。

迁移规则：

- 抽象 `AuthorizationServerEngine`，实现 `legacy` 和 `fosite` adapter。
- 通过 `oauth_clients.protocol_engine` 逐 client 切换。
- 同一个 client 在一个环境只能由一个 engine 处理，不做请求级随机双写。
- 切换前在 shadow test 中把相同 canonical request 输入两个 engine，只比较决策/错误，不发行第二套 token。
- Fosite storage adapter 复用目标 Ent repositories，不建立平行 token 数据库。
- engine 迁移失败时可以关闭新发行，但必须继续验证和撤销已发行 token。

### 11.3 原子性

以下操作必须在一个 PostgreSQL transaction 中完成：

- authorize transaction consume + code create + consent/grant update。
- code consume + access/refresh/family create。
- refresh consume + replacement refresh/access create + old access revoke。
- device approved consume + token mint。
- grant/family/client revoke + access/refresh 状态变更 + outbox event。

Redis invalidation 使用 transactional outbox 或提交后可靠重试。数据库是 authoritative source；不得因为 Redis 删除失败而回滚已完成的安全撤销，也不得静默忽略失败。

code exchange 必须先完成 client authentication、redirect、PKCE、resource/scope 和 policy 校验，再进入带 code row lock 的 mint transaction。所有随机 token material 与需要返回的 ID Token 必须在 transaction 提交前成功生成/签名；签名或任何 insert 失败时整体回滚，不得留下 consumed code、部分 group token、active shadow API key 或客户端从未收到的 active token。Refresh 和 Device Flow 使用同一原则。

外部 KMS 签名会延长持锁时间，因此实现需要独立的短超时、容量预算和故障注入测试；不能为了缩短 transaction 而先提交 bearer token，再尝试签 ID Token。

### 11.4 Client authentication

- public client：`none`，必须 PKCE。
- legacy confidential：`client_secret_basic`；`client_secret_post` 仅兼容并可按 client 禁止。
- enhanced confidential：优先 `private_key_jwt`，其次 mTLS。
- 同一请求出现重复或多种 client credential 时拒绝，不采用 first-wins；不能用“值恰好相同”绕过 credential-source ambiguity。
- JWT assertion 校验 `iss=sub=client_id`、audience、短 `exp`、合理 `iat`、JTI uniqueness、algorithm allowlist 和 registered key。JTI replay cache 必须跨实例一致。
- FAPI profile 的 assertion `aud` 必须是 issuer 的单一字符串；算法固定为 client registration 中的允许值，禁止从 assertion 的 `jku`/`x5u` 动态获取 key。
- secret/JWT 验证失败统一返回 `invalid_client`，不得泄露 client 是否存在或哪个 credential 失效。

### 11.5 Token 状态机

Authorization code：

```text
active -> consumed
active -> expired
consumed replay -> revoke derived grant/family when identifiable
```

Refresh token：

```text
active -> rotated -> replacement active
active -> revoked/expired
rotated replay -> family compromised -> revoke family
```

Access token：

```text
active -> expired
active -> revoked
family/grant/client/user revoke -> revoked
```

Device code：

```text
pending -> approved -> consumed
pending -> denied
pending/approved -> expired
```

所有 compare-and-set 和状态转换必须在 row lock 或数据库原子条件更新下完成。

### 11.6 Token 生命周期

目标默认值：

| 对象 | 默认 | 可配置边界 |
| --- | --- | --- |
| Authorization code | 5 分钟 | 2 至 10 分钟 |
| Authorize transaction | 10 分钟 | 不超过 15 分钟 |
| PAR request URI | 90 秒 | 60 至 120 秒 |
| Access token | 15 分钟 | 5 至 60 分钟；仅管理员可放宽 |
| ID Token | 5 分钟 | 不超过 15 分钟 |
| Refresh family absolute | 30 天 | first-party native 可审批准许至 90 天 |
| Refresh idle | 7 天 | 1 至 30 天 |
| Device code | 10 分钟 | 5 至 15 分钟 |
| Browser session idle | 12 小时 | 按安全策略 |
| Browser session absolute | 30 天 | 高风险 client 更短 |

Rotation 不延长 refresh family absolute expiry。Legacy client 的 24 小时 access token 在迁移窗口内保留，迁移到 v3 时缩短。

上表适用于 Standard/Enhanced。独立 FAPI issuer 的 authorization code 硬上限是 60 秒；refresh token 必须 sender-constrained，且不执行 Standard 的日常逐次 rotation。两套 lifecycle policy 使用不同 profile 代码和测试，禁止用同一个全局开关混合。

### 11.7 OIDC claims

- `sub` 对现有 first-party client 保持兼容。
- 新 third-party client 默认 `subject_type=pairwise`。
- 新 pairwise `sub` 从 10.13 的持久随机映射读取；一旦签发，不因 redirect、应用名、credential 或 signing key 变化而改变。现有弱散列只作为迁移输入，不再签发给新 client。
- standard claims 使用 fail-closed allowlist。
- `email_verified` 来自真实用户状态。
- `claims` parameter 必须从 authorize transaction 贯穿 code/access-token context 后再对 UserInfo 生效；完成前 discovery 应将 `claims_parameter_supported` 调整为 false，或明确只支持 ID Token section。
- `essential=true` 的语义要按 OIDC conformance 明确实现，不能永久 best-effort 却宣称完整支持。
- `aud` 必须精确为 client ID；多 audience 时按 OIDC 处理 `azp`。

### 11.8 Browser SSO

- 新增短期 one-time bridge：登录后的 Sakrylle SPA 用受 CSRF 和 step-up 保护的 API 在 issuer 域创建 HttpOnly OP session。
- `/oauth/authorize` 只读取 OP session cookie，不读取 localStorage token。
- session rotation、logout、密码/MFA 变更和管理员全局撤销通过 session version/outbox 传播。
- browser session 独立保存真实 `authenticated_at`、`amr`、`acr`；token refresh 只改变 token `iat`，绝不更新真实认证时间。
- `prompt=login` 强制重新认证；`max_age` 检查真实 `authenticated_at`；`select_account` 在单账号系统中可以返回交互页或明确定义行为。
- `sid` 与 browser session/RP session 建立可审计关联，但不在日志中暴露 cookie handle。

## 12. HTTP API 契约

### 12.1 Discovery

必须继续提供：

```text
GET /.well-known/oauth-authorization-server
GET /.well-known/openid-configuration
GET /.well-known/jwks.json
```

要求：

- 三者使用相同 canonical issuer resolver。
- `oauth_issuer` 缺失时生产环境启动健康检查失败；request Host fallback 只允许本地开发。
- 只广告实际可用能力。Capability 未端到端实现时不得先写 discovery 字段。
- discovery `Cache-Control: public, max-age=60`。
- JWKS `Cache-Control` 不得超过 key prepublish/grace 策略允许值，并支持 ETag。
- 新增 RFC 9207 后 advertising `authorization_response_iss_parameter_supported=true`。
- Enhanced client 上线 PAR 后广告 `pushed_authorization_request_endpoint`。

### 12.2 Authorization endpoint

```text
GET  /oauth/authorize
POST /oauth/authorize
```

核心参数：`client_id`、`redirect_uri`、`response_type=code`、`scope`、`state`、`code_challenge`、`code_challenge_method=S256`；OIDC 使用 `nonce`、`prompt`、`max_age`、`claims`。

要求：

- 在确认 `client_id` 和 redirect 合法前，错误不得 redirect。
- 合法 redirect 上的协议错误携带原始 state 和 RFC 9207 `iss`。
- parser 必须保留参数基数；`client_id`、`redirect_uri`、`response_type`、`response_mode`、`scope`、`state`、`nonce`、`code_challenge`、`request`、`request_uri` 等安全参数出现重复实例时返回 `invalid_request`，禁止 first-wins/last-wins。
- Request Object 与外层请求同时携带同一参数时必须按规范要求完全一致，否则拒绝；不能用外层值覆盖已签名内容。
- authorize 参数规范化后写 server-side transaction；approve 只提交 transaction ID、CSRF、decision 和用户可选资源。
- consent 页面只展示 transaction 中的 server-canonical scopes/resources/groups；approve 后签发内容必须是该快照的子集。请求省略 scope 时也必须先解析 client defaults，再渲染页面。
- consent HTML 设置 `Cache-Control: no-store`、`Referrer-Policy: no-referrer`、严格 CSP、`frame-ancestors 'none'`。
- 登录返回只携带一次性 server transaction handle，不把完整 authorize 请求复制到浏览器 URL。

### 12.3 PAR

目标端点：

```text
POST /oauth/par
Content-Type: application/x-www-form-urlencoded
```

- confidential/enhanced client 必须认证。
- 先完整验证 client、redirect、scope、resource、PKCE 和 request object，再存 canonical request。
- 返回 `request_uri` 和 `expires_in`，不回显敏感参数。
- Standard/Enhanced 默认 `expires_in=90` 秒；独立 FAPI profile 必须小于 600 秒。
- `/oauth/authorize?client_id=...&request_uri=...` 消费 handle；front-channel 重复参数只能按规范做严格一致性检查，不能覆盖 canonical PAR request。独立 FAPI profile 的 authorize 请求只接受 `client_id + request_uri`。

### 12.4 Token endpoint

```text
POST /oauth/token
Content-Type: application/x-www-form-urlencoded
```

支持：

- `authorization_code`
- `refresh_token`
- `urn:ietf:params:oauth:grant-type:device_code`

成功示例：

```json
{
  "access_token": "sk_oauth_v3_<id>_<secret>",
  "token_type": "Bearer",
  "expires_in": 900,
  "refresh_token": "rt_oauth_v3_<id>_<secret>",
  "refresh_token_expires_in": 2592000,
  "scope": "openid profile models:read responses:create offline_access",
  "id_token": "<signed-jwt>"
}
```

DPoP token 的 `token_type` 为 `DPoP`，并绑定 `cnf.jkt`。Refresh 时必须证明同一 key possession，除非通过显式 key rotation protocol。

所有 token response：

```text
Cache-Control: no-store
Pragma: no-cache
Content-Type: application/json
```

### 12.5 Device endpoints

```text
POST /oauth/device/code
GET  /oauth/device
POST /api/v1/oauth/device/approve
POST /api/v1/oauth/device/deny
```

- `verification_uri_complete` 应在 client 支持时返回。
- 页面必须显示 application、publisher、设备和 scope，不只显示 user code。
- 用户确认页应重新显示输入的 code，降低 device phishing。
- 限流同时覆盖 IP、client_id、user_code 和用户；达到失败阈值锁定 code。
- poll 严格返回 `authorization_pending`、`slow_down`、`access_denied`、`expired_token`。

### 12.6 UserInfo、introspection 和 revocation

- `/userinfo` 只接受有效 OAuth access token，不受余额/计费 gate 影响，但受 token scope/resource/revocation 影响。
- `/oauth/introspect` 默认只允许 owning confidential client；内部 resource server 使用单独 mTLS/private network credential，不伪装普通 client。
- `client_confidential` 必须成为 authoritative client schema/mapper 字段；HTTP + Ent integration test 同时覆盖 access token、refresh token、错误 client 和 disabled client，修复前不得把 introspection 标记为 production-ready。
- `/oauth/revoke` 对未知、跨 client 或已撤销 token 返回幂等 200；client authentication 错误仍返回 `invalid_client`。
- revocation 成功后目标传播时间小于 5 秒，正常目标小于 1 秒。

### 12.7 Logout

- `post_logout_redirect_uri` 必须 exact registration match，并优先要求合法 `id_token_hint` 或显式 client/session context。
- RP-Initiated Logout 明确区分“结束一个 RP session”和“全局退出全部 RP”。
- back-channel logout 使用短期、单用途 `logout_token`，包含 `events`、`jti`、`iat`、`aud`，按 client 配置包含 `sid` 或 `sub`。
- delivery 使用队列/outbox、超时和重试，不在用户响应路径无限等待。
- front-channel iframe 只作为兼容补充，不作为唯一会话撤销机制。

### 12.8 Developer API

目标路由：

```text
GET    /api/v1/developer/oauth-applications
POST   /api/v1/developer/oauth-applications
GET    /api/v1/developer/oauth-applications/:app_id
PATCH  /api/v1/developer/oauth-applications/:app_id
DELETE /api/v1/developer/oauth-applications/:app_id

POST   /api/v1/developer/oauth-applications/:app_id/submit-review
GET    /api/v1/developer/oauth-applications/:app_id/audit-events

POST   /api/v1/developer/oauth-applications/:app_id/clients
GET    /api/v1/developer/oauth-clients/:client_id
PATCH  /api/v1/developer/oauth-clients/:client_id
DELETE /api/v1/developer/oauth-clients/:client_id

POST   /api/v1/developer/oauth-clients/:client_id/credentials
DELETE /api/v1/developer/oauth-clients/:client_id/credentials/:credential_id
POST   /api/v1/developer/oauth-clients/:client_id/validate
```

约束：

- owner-scoped authorization，禁止只凭 app/client ID 访问。
- credential 创建、撤销、redirect 高风险变更和 application 删除要求 recent auth/step-up。
- create/credential 操作支持 `Idempotency-Key`。
- update 使用 ETag/`If-Match` 或显式 version，避免覆盖并发编辑。
- secret response 只返回一次 plaintext；列表只显示 prefix/last characters、状态和时间。
- API DTO 不直接暴露 Ent entity，不接受 `trusted_first_party`、review status、TTL 上限等管理员字段。

### 12.9 Admin API

```text
GET  /api/v1/admin/oauth-applications/review-queue
POST /api/v1/admin/oauth-applications/:app_id/approve
POST /api/v1/admin/oauth-applications/:app_id/reject
POST /api/v1/admin/oauth-applications/:app_id/suspend
POST /api/v1/admin/oauth-clients/:client_id/emergency-revoke
GET  /api/v1/admin/oauth-security-events
GET  /api/v1/admin/oauth-health
```

管理员 mutation 必须经过 admin auth、step-up、audit middleware 和 request body limit。紧急撤销必须支持 client、user、grant、family、credential 和 signing key 精确范围。

### 12.10 用户 Authorized Apps API

保留当前 per-grant API，并扩展：

- application/publisher/verified status。
- consented scopes/resources 与最近权限变更。
- device、首次授权、最近使用、粗粒度位置/IP 提示。
- token/session count。
- revoke one grant、one family、all grants for application。
- 可疑访问上报入口。

返回不得包含 raw token、refresh hash、内部 policy、完整 UA 或其他用户的资源信息。

### 12.11 错误与缓存

- OAuth protocol endpoint 使用 RFC error shape：`error`、可选 `error_description`、`error_uri`。
- 对外 description 不含 SQL、Ent、KMS、host、stack trace 或 credential 状态细节。
- API management endpoint 使用仓库统一 error envelope，但保留 stable machine-readable reason。
- authorize HTML、token、userinfo、introspection、revocation、device code 和 logout response 使用正确 no-store policy。
- 任何包含 user-specific authorization state 的 CDN route 都必须 bypass cache。

### 12.12 出站 URL 与投递安全

`request_uri`、`sector_identifier_uri`、`jwks_uri`、logo/homepage 验证和 back-channel logout 都经过统一 `SafeOutboundTransport`：

- 注册阶段只接受明确允许的 HTTPS URI；PAR 自有 URN 不触发远程 fetch。loopback、private、link-local、multicast、metadata 和保留地址默认拒绝。
- DNS 解析结果全量校验，并在每次连接时重新检查实际目标，防止 DNS rebinding；不得直接复用系统默认 transport。
- 默认禁止 redirect；确需 redirect 时只允许同 origin、有限次数并对每一跳重新做网络策略校验。
- 设置连接/总超时、响应体上限、内容类型、JWK 数量和 JSON 深度上限；禁止 assertion 的任意 `jku`/`x5u` 驱动即时 fetch。
- back-channel logout 通过 transactional outbox 和有界 durable worker 投递，具备并发上限、指数退避、circuit breaker、dead-letter 和审计；禁止每个 RP 启动无界 goroutine。

## 13. Middleware、缓存和资源服务器

### 13.1 认证顺序

```text
extract Authorization header
  -> detect credential family/prefix
  -> OAuth v3 verifier OR legacy OAuth/API-key verifier
  -> load authoritative token metadata
  -> validate status/expiry/client/grant/family/user
  -> validate DPoP/mTLS/resource
  -> build AuthSubject + BillingPrincipal
  -> enforce route scope/group/current entitlement
  -> handler
```

不能先把任意 bearer 当 manual API key 查询，再在后面补 OAuth 检查；这会使内部 shadow key 或格式降级成为 credential。

### 13.2 Redis cache

- cache key 使用 token digest/ID，不使用 plaintext token。
- cache value 不保存 client secret、refresh token 或私钥。
- TTL 不超过 access token 剩余寿命和 policy cache 上限。
- family/client/user revocation 通过 version key 或 outbox invalidation 立即失效。
- cache miss 回源 DB；cache/DB 元数据不一致 fail closed 并产生 security event。
- token、authorize、device 等公开 mutation endpoint 的 rate limit 默认 fail closed。
- Redis 故障模式和应急 bypass 必须是受审计、短期、仅管理员启用的配置，不能代码内自动 fail open。

### 13.3 DPoP

- 验证 `typ=dpop+jwt`、alg allowlist、public JWK、`htm`、canonical `htu`、`iat`、`jti` 和 token `ath`。
- `jti` replay cache 按 thumbprint + jti 保存至少 proof 有效窗口。
- reverse proxy 必须提供可信 canonical scheme/host，不能直接信任任意 forwarded header。
- nonce challenge 在高风险或 replay cache 不稳定场景启用。
- refresh token 与授权 code 可以绑定 `dpop_jkt`，防止 token endpoint 处替换 key。
- PAR、authorization-code exchange、refresh、HTTP resource request 和 WebSocket upgrade handshake 都必须执行对应 proof/key-binding 校验。
- `token_type=DPoP` 的 token 禁止降级为 Bearer 使用；resource server 必须核对 access token 的 `cnf.jkt`。
- DPoP proof 不签 HTTP body，不提供业务交易完整性或不可否认性；需要交易签名时必须采用独立、明确的 application-level protocol。

### 13.4 CORS

- Authorization endpoint 不需要 permissive CORS。
- Token endpoint 只对登记的 public browser client exact origin 开启 CORS。
- 决策只读取请求 client 的 authoritative `allowed_origins`，不从 redirect URI 推导，也不把全部 enabled client 的 origin 合并为全局 allowlist。
- 禁止 `*` 与 credentials 组合。
- Origin 规范化只允许 scheme + host + explicit port，不允许 path、wildcard 或 `null`，除非单独审核沙箱场景。
- Native/CLI 不依赖 CORS。

## 14. 前端与交互设计

每个新增 backend feature 必须在同一 change 中交付对应 Vue surface、API/types、validation、i18n 和测试；除非任务明确声明 backend-only。

### 14.1 Developer Settings

新增路由建议：

```text
/developer/apps
/developer/apps/new
/developer/apps/:app_id
/developer/apps/:app_id/clients/:client_id
```

页面结构：

- Applications 列表：状态、owner、客户端数、最近使用、风险警告。
- Overview：品牌、主页、隐私和发布者验证。
- Clients：Web/Native/CLI 类型、redirect、origin、logout URI。
- Permissions：allowed/default scope、resource/group 限制和风险等级。
- Credentials：secret/JWK 创建、一次性显示、grace/revoke、last used。
- Audit：过滤后的 security/config event。
- Danger Zone：disable、retire、delete 和全量 revoke。

表单必须把 public/confidential、PKCE、redirect 和 secret 的不变量前置验证，但后端仍是权威校验者。

### 14.2 Admin Review

- 审核队列支持配置 diff，不只展示最终值。
- 高风险 scope、custom scheme、remote request URI、DPoP/PAR 状态有明确标记。
- approve/reject/suspend 使用确认对话框和 step-up。
- 管理员不可从 UI 读取 plaintext secret 或私钥。
- emergency revoke 显示预计影响的 grant/family 数量，但执行保持幂等。

### 14.3 Consent

Consent 页面必须显示：

- application 名称、受控图标、publisher 和 verified 状态。
- 请求权限的可理解描述及 read/create/offline 风险。
- 新增权限与历史 consent 的 diff。
- 资源/group 选择。
- device/application type。
- privacy/terms 链接。
- approve/deny 明确操作。

禁止只显示 scope identifier，禁止把 first-party 样式当作可信证明，禁止嵌套 iframe。

### 14.4 Authorized Apps

在现有页面基础上增加 application 聚合和 grant 展开。用户最常用操作保持直接：查看最近使用、撤销当前设备、撤销整个应用。

IP/location 只作为安全提示，不制造精确定位假象；文案说明可能受代理/CDN 影响。

### 14.5 Device Verification

- 输入 code 与确认 application 分两步或在同屏清晰分区。
- 支持粘贴规范化，不因连字符/大小写造成不必要失败。
- approve 前显示 scopes/resources。
- 失败尝试不泄露 code 是否属于其他用户/client。

### 14.6 i18n 和可访问性

- 中文与英文同时交付。
- scope 描述由共享 registry 驱动，前后端 ID 保持一致。
- keyboard focus、错误关联、screen reader label 和 reduced motion 纳入测试。
- secret 一次性展示不能只靠颜色表达状态。

## 15. 迁移与向后兼容

### 15.1 总体原则

- additive schema first。
- dual read before new write。
- new write before legacy retirement。
- issuer、client_id、redirect 契约和 active JWKS 不在同一发布中同时变化。
- 不对现有 plaintext token 做可逆“加密回填”；v3 只影响新发行，legacy token 自然过期/撤销。
- 删除旧列/代码至少等待两个最大 refresh lifetime，并验证生产计数为零。

### 15.2 阶段 M0：基线冻结

- 恢复本文档并让代码注释引用重新有效。
- 导出 production client contract 的去敏快照测试。
- 记录 discovery、JWKS、错误、token response 和 logout golden contract。
- 接入 OpenID conformance baseline，不改生产行为。
- 在继续扩展协议前修复 introspection schema/mapper、consent canonical scope 展示、scope enforcement 上线检查、CSPRNG fallback 和 discovery over-advertising。

### 15.3 阶段 M1：目标 schema

- 新增 application、credential、authorization grant、family、browser session、audit/outbox 表。
- 扩展 client/access-token 元数据。
- 生成 Ent、migration tests 和 repository stubs。
- 为现有 client 创建 `system` applications 并保持 client_id 不变。

### 15.4 阶段 M2：v3 token 双读

- Resource middleware 识别 v3 prefix，hash-only 校验后加载 shadow API key/billing principal。
- legacy `sk_oauth_` 继续原路径。
- first-party canary client 开启 v3 issuance。
- 监控 v3/legacy 验证、latency、invalid token 和 cache mismatch。
- 出现问题时关闭 v3 issuance；已发行 v3 继续验证到过期或撤销。

### 15.5 阶段 M3：多 credential 和开发者平台

- 将现有 bcrypt secret 映射为 legacy credential。
- 新 secret 使用 selector + hash、一次显示和 grace rotation。
- 上线 developer/admin API 和前端。
- 注册变更通过 version 和 audit，不再要求直接 SQL/restart。

### 15.6 阶段 M4：Browser session 和 consent v2

- 建立 issuer HttpOnly session。
- 修复 third-party `prompt=none` prior consent。
- consent version/resource/group diff 上线。
- 保留旧 localStorage bridge 一个受控窗口，只允许 first-party client，并给出移除日期。

### 15.7 阶段 M5：Fosite engine

- 先迁移测试 client，再迁移 Studio、CLI、Chat、Web。
- 每个 client 切换前跑 legacy/fosite decision parity 和端到端 flow。
- 迁移后 legacy engine 仍处理未切 client，不接受未知 client fallback。
- 全部目标 client 稳定两个 release 后才删除重复协议校验代码。

### 15.8 阶段 M6：Enhanced profile（FAPI-inspired）

- PAR + RFC 9207。
- `private_key_jwt`。
- Resource Indicators。
- DPoP。
- 按需 JAR/JARM。
- 对 canonical issuer 运行选定的 FAPI 安全负向测试作为工程参考，但不申请、不宣称 FAPI certification。

### 15.9 阶段 M7：独立 FAPI 2.0 AS（按业务需求）

- 在独立 issuer 上实现 2.2 的 confidential-only profile、独立 metadata/keys/policy 和 client-authenticated PAR。
- 单独实现 FAPI refresh 生命周期、60 秒 code 上限、允许算法和 sender-constrained token，禁止复用 Standard 的全局默认值。
- 通过 FAPI 2.0 Final conformance 与外部安全评审后，才发布合规声明。

### 15.10 回滚

- 所有 feature flag 按 client 控制，global kill switch 只作为紧急手段。
- schema 不在回滚 release 中 drop。
- v3 issuance 可停，v3 validation 不可先停。
- 新 credential plaintext 无法重新显示，回滚时继续保留 hash verifier。
- key rotation 回滚不得让已签发 ID Token 的 kid 从 JWKS 提前消失。
- client registration 更新保留历史 version，可恢复上一安全配置但仍需审计和必要的重新 consent。

## 16. 实施计划与验收

以下估算基于 2 名 backend、1 名 frontend、1 名 QA/security 并行投入。单人实施 Standard/Enhanced 通常需要 4 至 6 个月；独立 FAPI AS 和 Workload OIDC 不包含在该估算内。

### Phase 0：基线、ADR 和 conformance，1 至 2 周

交付：

- 本文档、threat model、Fosite/外置 AS ADR。
- current endpoint/client/discovery contract tests。
- OpenID conformance 本地环境。
- capability advertisement audit。
- introspection Ent/mapper/HTTP 链路修复，并明确 access/refresh token 支持范围。
- consent canonical default scopes 修复、scope enforcement reconcile/runbook 与 CSPRNG fail-closed。

完成条件：

- 明确当前 conformance fail list。
- issuer/redirect/token/claim 兼容清单经评审。
- 不再有代码注释引用不存在的设计文档。
- consent 展示与签发 scope 完全一致；真实 HTTP + PostgreSQL introspection contract 通过。
- Provider 启用时 resource scope enforcement 不会静默关闭，discovery 不宣称未实现能力。

### Phase 1：凭据与密钥底座，2 至 3 周

交付：

- v3 hash-only access/refresh token 和 dual verifier。
- 15 分钟默认 access token profile。
- 多 client credential 和 one-time secret UI。
- OAuth/ID token signing/verifier/TOTP/backup secret 分离的 KEK/KMS policy。
- code exchange、refresh、device token mint 的单事务状态机。
- multi-instance signing-key leader/CAS、readiness 和故障恢复。
- third-party consent reuse 与 device client/risk rate limit。

完成条件：

- 新 token/secret 在 DB、Redis、日志、trace 中均无 plaintext。
- legacy token 无回归。
- family replay 在并发测试中只产生一次 replacement 并撤销 family。
- ID Token/KMS 签名失败不消费 code，也不留下 active token 或 partial multi-group result。

### Phase 2：开发者与管理员平台，3 至 4 周

交付：

- application/client/credential/review/audit backend。
- Developer Settings 与 Admin Review 前端。
- owner policy、step-up、i18n、frontend tests。
- registration reconcile/validation 工具。

完成条件：

- 第三方应用无需直接 SQL 即可完成 draft 到 active 流程。
- 管理员可以暂停和 emergency revoke。
- 所有 mutation 有审计事件。

### Phase 3：Consent、session 和权限治理，2 至 3 周

交付：

- HttpOnly OP browser session。
- scope/resource/group diff consent。
- `prompt`/`max_age`/prior consent 正确语义。
- Authorized Apps application 聚合。

完成条件：

- authorize 不依赖长期 localStorage JWT。
- third-party silent auth 仅在有效 login + consent 下成功。
- password/MFA/session revoke 能传播到 RP logout。

### Phase 4：协议引擎和增强档，3 至 4 周

交付：

- Fosite adapters 与 per-client rollout。
- PAR、RFC 9207、Resource Indicators。
- `private_key_jwt` 与 DPoP。
- enhanced client portal controls。

完成条件：

- Standard OIDC OP conformance 通过目标 profile。
- Enhanced 配置通过选定的 FAPI-inspired 安全负向测试，但所有产品材料均不宣称 FAPI 合规。
- DPoP replay、htu normalization、key binding 负向测试通过。

### Phase 5：生产认证与收尾，2 周

交付：

- Studio/CLI/Chat/Web canary 和全量迁移。
- load/chaos/pentest 结果。
- runbook、dashboard、alert 和 incident drills。
- legacy retirement report。

完成条件：

- SLO 达标且一个完整 refresh lifetime 无 P0/P1 incident。
- legacy plaintext token 数量按计划归零。

### Phase 6（可选）：独立 FAPI 2.0 AS，4 至 8 周

交付：

- 独立 issuer、keys、metadata、client registry policy 和部署单元。
- confidential-only PAR、`private_key_jwt`/mTLS、DPoP/mTLS、FAPI lifecycle policy。
- FAPI 2.0 Final conformance、外部渗透测试和独立 runbook。

完成条件：

- 对应 conformance profile 全部通过且无 capability over-advertising。
- 安全评审批准后才在对外材料中使用“FAPI 2.0 compliant/certified”。
- OpenID 自认证材料或等价 conformance 证据归档。

## 17. 工作流与文件所有权

并行开发时按边界分工，避免多人同时改同一核心文件。

### Workstream A：Schema/Repository

- `backend/ent/schema/oauth_*.go`
- next migrations
- `backend/internal/repository/oauth_*`
- migration/repository/concurrency tests

### Workstream B：Protocol Engine/Service

- `backend/internal/service/oauth_provider*`
- Fosite adapter
- token/consent/session state machine
- service tests

### Workstream C：Handler/Middleware

- `backend/internal/handler/oauth_*`
- `backend/internal/server/routes/oauth*`
- `backend/internal/server/middleware/oauth*`
- protocol HTTP/error/cache tests

### Workstream D：OIDC/Crypto

- `backend/internal/service/oidc_*`
- KMS/key rotation/client JWT/DPoP
- conformance helpers and crypto tests

### Workstream E：Frontend

- developer/admin/authorized apps views
- API/types/router/i18n
- Vitest/Playwright

### Workstream F：Ops/Security/Docs

- conformance、load、chaos、security review
- `docs/ops/identity-email.md` 与 runbooks
- deployment flags、dashboards、alerts

共享接口先通过小型 contract change 合并。Wire provider 变化后立即重新生成 `wire_gen.go`，不要等所有 workstream 完成后手工合并。

## 18. 测试矩阵和发布门禁

### 18.1 Unit/Property/Fuzz

- redirect URI exact/loopback/custom scheme normalization。
- duplicate query/form parameters、Request Object 外层参数一致性和 first/last-wins 拒绝。
- scope/resource canonicalization、subset 和 default deny。
- PKCE charset/length/S256 constant-time compare。
- CSPRNG fault injection；随机源失败时 code/token/sid/jti 创建必须失败关闭。
- JWT alg confusion、`none`、kid collision、aud/iss/sub/exp/nbf/jti。
- pairwise subject stability 与 sector isolation。
- token/secret parser 不接受 prefix downgrade、截断或额外分隔符。
- request URI/sector/JWKS fetch SSRF、DNS rebinding、redirect、size/time limits。
- DPoP htm/htu/jti/iat/ath/key binding。

Go parser/validator 必须加入 `go test -fuzz` corpus，覆盖历史安全 bug。

### 18.2 Repository/Concurrency

- 同一 code 并发 exchange 只成功一次。
- 同一 refresh 并发 rotation 只产生一个合法 replacement；其余触发明确 replay/race policy。
- approve/deny race 不双发 code。
- device approve/poll race 不双发 token。
- revoke 与 refresh/mint race 最终状态 fail closed。
- transaction rollback 不留下 active shadow API key 或孤儿 token metadata。
- ID Token/KMS 签名失败不消费 code；multi-group mint 任一失败不留下部分 active token。
- outbox 重试幂等。

使用真实 PostgreSQL/Redis integration tests，不只依赖 in-memory stub。

### 18.3 HTTP Contract

- form content type、Basic/form credential 冲突、body limit。
- OAuth redirect error 和 inline error 安全边界。
- no-store/Pragma/CSP/Referrer headers。
- discovery 字段与实际 route/algorithm/feature flag 一致。
- RFC 7009 幂等和 RFC 7662 client ownership。
- introspection 使用真实 Ent/PostgreSQL 路径覆盖 access/refresh、confidential/public、disabled client；不能只 mock service DTO。
- CORS exact origin 和 preflight。

### 18.4 OIDC Conformance

至少运行：

- Authorization Code OP profile。
- public/confidential clients。
- nonce、max_age/auth_time、claims、UserInfo。
- public/pairwise subject。
- key rotation/JWKS cache。
- RP-initiated 和 front/back-channel logout 的可用 profile。

canonical issuer 的 Enhanced 档只运行适用的安全测试，不把结果描述为 FAPI conformance。只有 15.9 的独立 FAPI issuer 运行 FAPI 2.0 Final Security Profile；启用 JAR/JARM 时再运行 Message Signing 对应 profile。

### 18.5 Browser/Frontend E2E

- 未登录 authorize -> login -> consent -> callback。
- prior consent + `prompt=none`。
- consent deny/error/retry/expired transaction。
- device code approve/deny/expired/slow_down。
- create app/client、secret 一次显示、rotation grace、review。
- revoke one device/all app、step-up required。
- desktop/mobile viewport 无溢出、遮挡或不可达控件。

### 18.6 Chaos/Resilience

- Redis unavailable/latency/eviction。
- DB failover、transaction timeout、deadlock retry。
- KMS unavailable/throttled/permission revoked。
- clock skew ±2 分钟。
- signing key rotation 中实例滚动重启。
- 在 prepublish、activate、retire 各写入点 kill leader，验证 CAS 接管与 JWKS 连续性。
- outbox backlog、back-channel logout RP timeout。
- CDN 错误缓存配置检测。

### 18.7 Security Negative Tests

- open redirect、mix-up、code injection、CSRF、session fixation。
- stolen authorization code without verifier。
- refresh reuse、wrong client、wrong DPoP key。
- client secret stuffing 和 timing signal。
- user A 操作 user B 的 transaction/grant/application。
- disabled client 继续 refresh/introspect/revoke 的预期行为。
- malformed JWT/JWK、algorithm substitution、duplicate JSON keys。
- device phishing/brute force/rate-limit bypass。
- DB dump 中不存在可直接使用的新 credential。

### 18.8 CI 门禁

每个相关 change 至少运行：

```bash
make test-backend
make test-frontend
make security-audit
```

Schema 变化额外运行 Ent generation diff 和 migration integration tests。Release candidate 额外运行 conformance、Playwright、load、chaos 和 secret scan。

## 19. 威胁模型与安全不变量

### 19.1 受保护资产

- 用户身份与登录 session。
- OAuth access/refresh/code/device credential。
- client credentials 和 signing private keys。
- 用户 consent、scope、resource、group 和 billing authority。
- application publisher/owner identity。
- audit evidence 和 incident controls。

### 19.2 信任边界

- Browser/native/CLI 与 Sakrylle edge。
- CDN/reverse proxy 与 application server。
- application server 与 PostgreSQL/Redis/KMS。
- Authorization Server 与 Resource Server。
- Sakrylle 与第三方 client/RP/logout endpoint/JWKS URI。
- developer/admin UI 与管理 API。

### 19.3 攻击者

- 恶意第三方 client。
- 被攻陷的合法 client 或用户设备。
- 可读取 DB backup/Redis/log 的攻击者。
- 能控制 DNS、redirect target、request URI 或 logout endpoint 的攻击者。
- 拥有普通用户、developer 或低权限管理员账号的内部攻击者。
- 网络层 replay、credential stuffing 和资源耗尽攻击者。

### 19.4 必须始终成立的不变量

1. 未验证 redirect URI 时不 redirect；redirect 使用 exact string match，只有 native loopback port 使用 RFC 8252 例外。
2. 安全参数重复时拒绝；Request Object/PAR 与外层参数不能被 first-wins、last-wins 或覆盖规则改变。
3. Authorization code、refresh/device code、PAR 和 consent approve 的单次状态转换在并发下最多成功一次。
4. code/token mint 必须在同一权威事务中完成；完整 response material 未生成前不得提交 consumed code 或 active token。
5. 所有 authorization-code client 使用 S256 PKCE。
6. 所有 token、code、device code、`sid`、`jti` 和 CSRF secret 至少使用 128 bit CSPRNG；随机源失败即失败关闭。
7. 新 access/refresh/client secret 不以 plaintext 落 DB、Redis、日志、trace、metrics、audit 或 backup。
8. Access token 的 user/client/grant/family/resource/audience/scope/group/sender-key 元数据必须一致；不一致即 `invalid_token`。
9. Standard/Enhanced refresh reuse 撤销整个 family；FAPI refresh 采用独立 sender-constrained lifecycle，不能意外继承 Standard rotation。
10. consent 展示、用户确认和最终签发使用同一 server-canonical scope/resource/group snapshot，最终结果只能缩权。
11. 第三方 client 没有有效 OP session、足够 consent 和满足 `max_age/acr` 的认证时不能 silent authorize。
12. `authenticated_at`、`amr`、`acr` 独立于 token `iat`；refresh 不更新真实认证时间。
13. issuer 只来自显式、只读的 canonical trusted configuration；生产缺失或不一致时 readiness 失败。
14. ID Token claim 只来自 fail-closed allowlist，不含余额、group、计费或其他可变业务状态。
15. pairwise `sub` 不可由公开 user ID 枚举，且首次签发后不因 redirect、应用名、credential 或 signing key 变化。
16. public client 不被当作能够保密 secret；`trusted_first_party` 只由安全管理员设置且不能绕过高风险 step-up。
17. client disabled/suspended 后不能新 authorize、exchange 或 refresh；introspect/revoke 行为按标准且有 contract test。
18. 用户/管理员 revoke 必须让 DB authoritative state 失效，缓存失败不能恢复权限。
19. current signing key 私钥不通过 JWKS、API、日志或错误暴露；signer 不可用时 OIDC readiness 失败。
20. signing key 从 JWKS 移除时间晚于所有可能由其签名的 token 验证窗口，除非执行明确的紧急泄露撤销。
21. application/client/credential 的管理操作都检查 owner/admin scope、recent auth 并写审计。
22. OAuth token 不能因为 route 未配置 policy 或 scope enforcement 配置漂移而获得默认访问。
23. group/resource 当前失权后不能通过 refresh 获得新 token；短期 access token 的风险由短 TTL 限制。
24. raw request/claims metadata 进入审计前经过字段 allowlist 和大小限制。
25. 所有远程 URI fetch/delivery 使用 SSRF-safe transport；back-channel logout 只能通知与当前 OP session 有关联的 RP session。
26. discovery 只广告通过端到端实现和当前 feature/config gate 实际可用的能力。

## 20. 运维、SLO、监控和事件响应

### 20.1 SLO 建议

| 指标 | 目标 |
| --- | --- |
| Discovery/JWKS availability | 99.99% |
| Token endpoint availability | 99.95% |
| Token endpoint p95 | < 250 ms，不含外部 KMS 严重降级 |
| UserInfo/introspection p95 | < 100 ms cache hit |
| Revocation propagation | 正常 < 1 秒，SLO < 5 秒 |
| Key rotation verification continuity | 100% 已发行有效 token 可验证 |
| Plaintext v3 credentials at rest | 0 |

SLO 按环境和容量验证后固化，不以牺牲安全校验换取 latency。

### 20.2 Metrics

建议指标：

- authorize/token/device/introspect/revoke 请求数、latency、error reason。
- code/refresh/device replay 和 client auth failures。
- active grants/families、legacy/v3 token issuance/validation。
- cache hit/miss/mismatch、outbox lag、cleanup backlog。
- signing key age、JWKS key count、rotation success/failure。
- back-channel logout delivery/retry/dead-letter。
- application review queue age 和 credential expiry horizon。

Prometheus label 禁止使用 user ID、grant ID、token ID 或无界 client_id。细粒度调查进入结构化日志/审计系统。

### 20.3 Alerts

P0/P1 告警：

- current signing key 不可用或 JWKS 无 current kid。
- refresh reuse 突增或同 client 多地区异常。
- token issuance 成功但 metadata/shadow principal 不完整。
- revocation outbox 超过 5 秒。
- issuer/discovery 与签发 token `iss` 不一致。
- KMS decrypt/sign permission 异常。
- plaintext credential 检测命中。

### 20.4 Key management

- OIDC signing、token verifier、client-secret verifier、TOTP 和 backup encryption 使用不同 KMS key/KEK 和 IAM policy。
- production 首选 KMS/HSM 内部非导出私钥直接签名；降级到 envelope encryption 时使用独立 OIDC KEK，并把 `issuer + kid + alg + key_version` 作为 AEAD AAD。
- signing key 状态：`prepublished -> active -> retiring -> retired -> revoked`。
- 先发布 public JWK，等待至少一个 JWKS cache TTL，再切换 signing。
- retiring key 保留到最长 ID/logout token lifetime + clock skew + JWKS cache safety margin。
- 多实例只允许 leader lease + database CAS 完成状态转换；其他实例订阅 outbox/event 立即 reload。每一步都必须幂等，进程死亡后新 leader 可以安全接管。
- OIDC 开启但 current signer/KMS 不可用、issuer 未配置或 active key 与 JWKS 不一致时 readiness 失败；不得只打印 warning 后继续对外提供看似可用的 discovery/token endpoint。
- emergency revoke 需要双人审批或 break-glass 审计，并明确会使哪些 token 无法验证。
- 紧急泄露 runbook 必须能立即撤除旧 JWK、批量撤销相关 browser session/grant，并与普通 grace rotation 明确分开。
- 数据库只保存 KMS key reference/public metadata，或经过上述独立 KEK 保护的 ciphertext；OIDC 不再复用 TOTP/支付等业务 secret 的根密钥。

### 20.5 Cleanup 和 retention

- expired code/transaction/device/PAR 高频清理。
- revoked/expired token metadata 保留足够 incident/replay 窗口后再删除。
- security events 在线保留建议 180 天，归档期限按合规政策配置。
- IP/UA 等个人数据最小化、分级访问并有删除策略。
- cleanup scheduler 必须有 leader lock 或幂等 SQL，不得多实例产生错误状态转换。

### 20.6 Runbooks

必须具备：

- client secret 泄露与无停机轮换。
- access/refresh token 泄露和 family/client/user revoke。
- signing key 正常与紧急轮换。
- issuer/discovery 错配。
- Redis outage、DB failover、KMS outage。
- compromised application suspend 和用户通知。
- conformance regression rollback。

生产入口、防火墙、443 stream 或 SSH 变更仍遵循 `docs/ops/infrastructure.md` 的 fallback session 约束。

## 21. 已确定的架构决策

1. 保持 `https://oidc1.sakrylle.com` 为 canonical issuer。
2. 使用 opaque access token，不改为 self-contained JWT access token。
3. 所有 authorization-code client 强制 S256 PKCE。
4. 新 access token 使用 hash-only v3 格式；legacy token 只兼容读取。
5. Standard/Enhanced Refresh token 每次使用轮换，reuse 撤销 family；独立 FAPI issuer 使用其 sender-constrained lifecycle，不复用该决策。
6. 第三方 client 默认 pairwise subject，目标实现采用不可变持久随机映射。
7. Fosite 是推荐嵌入式协议引擎，按 client 渐进迁移；不立即拆分外置 IdP。
8. Application 与 Client 分层，一个 application 可以有多个平台 client。
9. Client credential 独立成多行并支持 grace rotation。
10. Developer portal 是受认证的注册与治理入口，不直接开放匿名 RFC 7591。
11. Group 保持业务授权和计费边界，不塞进 ID Token。
12. Resource Indicators/audience 在增强阶段补齐。
13. 普通 OAuth、安装型 Sakrylle Apps、workload OIDC 使用不同身份模型。
14. 每个 backend feature 同 change 交付 frontend、API/types、validation、i18n 和 tests。
15. 标准能力只在端到端实现并通过测试后写入 discovery。
16. canonical issuer 的 Enhanced 档只称为 FAPI-inspired；真正 FAPI 2.0 使用独立逻辑 AS/issuer 并单独认证。

## 22. Workload OIDC 后续设计边界

Workload OIDC 需要单独设计文档。此处只冻结边界：

- 独立 issuer，例如专用 workload issuer 域；最终域名由基础设施设计决定。
- token endpoint 只对受证明的 runner/job 开放，不复用用户 `/oauth/token`。
- JWT TTL 5 至 10 分钟，无 refresh token。
- `sub` 使用不可变 owner/project/workflow/environment/run ID，不只使用可重命名名称。
- `aud` 由 workload 明确请求，并受 repository/environment policy allowlist。
- claims 至少包含 `iss`、`sub`、`aud`、`iat`、`nbf`、`exp`、`jti` 和不可变 workload 元数据。
- 云端信任策略必须至少约束 subject/audience/environment，禁止只信任 issuer。
- 用户 OAuth scope、balance、group 和 consent 不进入 workload assertion。
- signing keys、audit、rate limit 和 incident response 与用户 OP 隔离。

## 23. 参考资料

- OAuth 2.0 Authorization Framework: https://www.rfc-editor.org/rfc/rfc6749.html
- OAuth 2.0 Bearer Token Usage: https://www.rfc-editor.org/rfc/rfc6750.html
- OAuth 2.0 PKCE: https://www.rfc-editor.org/rfc/rfc7636.html
- OAuth 2.0 Token Revocation: https://www.rfc-editor.org/rfc/rfc7009.html
- OAuth 2.0 Token Introspection: https://www.rfc-editor.org/rfc/rfc7662.html
- OAuth 2.0 Security BCP: https://www.rfc-editor.org/rfc/rfc9700.html
- OAuth 2.0 Authorization Server Metadata: https://www.rfc-editor.org/rfc/rfc8414.html
- OAuth 2.0 for Native Apps: https://www.rfc-editor.org/rfc/rfc8252.html
- OAuth 2.0 Device Authorization Grant: https://www.rfc-editor.org/rfc/rfc8628.html
- OAuth 2.0 PAR: https://www.rfc-editor.org/rfc/rfc9126.html
- OAuth AS Issuer Identification: https://www.rfc-editor.org/rfc/rfc9207.html
- OAuth 2.0 DPoP: https://www.rfc-editor.org/rfc/rfc9449.html
- OAuth 2.0 Resource Indicators: https://www.rfc-editor.org/rfc/rfc8707.html
- OAuth JWT Client Authentication: https://www.rfc-editor.org/rfc/rfc7523.html
- JWT Best Current Practices: https://www.rfc-editor.org/rfc/rfc8725.html
- OAuth 2.0 JAR: https://www.rfc-editor.org/rfc/rfc9101.html
- OAuth 2.1 Internet-Draft: https://datatracker.ietf.org/doc/draft-ietf-oauth-v2-1/
- OpenID Connect Core 1.0 Errata 2: https://openid.net/specs/openid-connect-core-1_0-errata2.html
- OpenID Connect Discovery 1.0: https://openid.net/specs/openid-connect-discovery-1_0.html
- OIDC RP-Initiated Logout 1.0 Final: https://openid.net/specs/openid-connect-rpinitiated-1_0-final.html
- OIDC Front-Channel Logout 1.0 Final: https://openid.net/specs/openid-connect-frontchannel-1_0-final.html
- OIDC Back-Channel Logout 1.0 Final: https://openid.net/specs/openid-connect-backchannel-1_0-final.html
- OpenID Connect Certification: https://openid.net/certification/
- FAPI 2.0 Security Profile Final: https://openid.net/specs/fapi-security-profile-2_0-final.html
- FAPI 2.0 Message Signing: https://openid.net/specs/fapi-message-signing-2_0-final.html
- ORY Fosite: https://github.com/ory/fosite
- GitHub Apps 与 OAuth Apps 对比: https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/differences-between-github-apps-and-oauth-apps
- GitHub Actions OIDC reference: https://docs.github.com/en/actions/reference/security/oidc
