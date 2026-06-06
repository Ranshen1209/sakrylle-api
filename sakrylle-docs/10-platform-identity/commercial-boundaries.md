---
title: 04 · OAuth/OIDC 与商业能力的边界
status: canonical
scope: platform
last_verified: 2026-06-06
---

# 04 · OAuth/OIDC 与商业能力的边界

> 规划文档（planning only）。Sakrylle API 与 Sakrylle Image **已上线生产**，本文不含任何"直接改生产配置/破坏性迁移/删用户数据"的指令；触及生产处标注「需额外审批」。
> 兄弟文档：见 `03-sakrylle-api-oidc-architecture.md`（OIDC 端点/密钥/id_token）、`05-configuration-isolation-standard.md`（客户端隔离）。

---

## 1. 调研范围

界定 sub2api fork 的商业能力（用户额度 / 计费 / API Key / 团队组织 / 多租户 / 模型权限 / 使用量统计 / 审计日志）与 OAuth2/OIDC 身份层的**边界**：哪些信息适合放进 token claims（稳定、低频、非敏感），哪些**必须服务端实时查询**（余额、额度、实时计费、模型权限）。并明确 API Key 与 OAuth access_token 的共存策略、各客户端认证选型、权限模型现状与演进。

## 2. 关键结论

1. **核心授权单元是 `group`（分组）**，不是 org/team。一个 API Key 绑一个 group，group 绑一个 channel，channel 持 `channel_model_pricing` 定价表。**系统当前无 org/team/workspace 多租户概念，且明确决策不引入（已确认 2026-06-03）** —— token claims 不含 org/tenant。
2. **认证三轨均"每次请求实时校验"**，无纯 token-only 放行路径——这是把"易变商业状态"挡在 token 外的天然护栏。
3. **token 只放稳定身份，绝不放余额/实时计费**：余额每次调用后扣减，放进短 TTL 的 JWT/id_token 一定过期或不同步。
4. **API Key 与 OAuth access_token 已在 `api_keys` 表统一存储**（OAuth token 用 `sk_oauth_` 前缀），网关计费/缓存/限流路径**对两者无差别**——这是最大的架构红利。
5. **OIDC id_token 是"登录身份证明"，不是"授权/计费凭证"**：id_token 给 RP 确认"你是谁"，access_token（opaque）才进网关花钱。二者职责必须分离。

## 3. 相关文件路径（path:line，引用自调研）

| 关注点 | 路径:行 |
|---|---|
| API Key 主鉴权中间件（balance/quota/subscription 入口） | `backend/internal/server/middleware/api_key_auth.go:32`（balance 检查 `:211`） |
| JWT 鉴权（每请求实时读 DB + 比对 TokenVersion） | `backend/internal/server/middleware/jwt_auth.go:28`（DB 读 `:62`，version `:76`） |
| Admin 鉴权（常量时间比较 admin_key） | `backend/internal/server/middleware/admin_auth.go:31`（`:132`） |
| auth 快照结构（含 User.Balance/Group.RateMultiplier） | `backend/internal/service/api_key_auth_cache.go:6` |
| 双层缓存（L1 ristretto + L2 Redis，version=11） | `backend/internal/service/api_key_auth_cache_impl.go` |
| 缓存失效（ByKey/ByUserID/ByGroupID） | `backend/internal/service/api_key_auth_cache_invalidate.go` |
| Redis key=`apikey:auth:<sha256>`，Pub/Sub=`auth:cache:invalidate` | `backend/internal/repository/api_key_cache.go` |
| APIKey struct（Quota/QuotaUsed/RateLimit*） | `backend/internal/service/api_key.go` |
| User struct（Balance/TokenVersion/GroupRates） | `backend/internal/service/user.go` |
| Channel/ChannelModelPricing/BillingMode | `backend/internal/service/channel.go` |
| channel cache（TTL 10min）/restrict_models 校验 | `backend/internal/service/channel_service.go:136`（checkRestricted `:546`） |
| GetAvailableModels / postUsageBilling / DeductBalance | `backend/internal/service/gateway_service.go:9714` / `:8191` / `:8207` |
| BillingCache 接口（GetUserBalance/DeductUserBalance） | `backend/internal/service/billing_cache_service.go` |
| user_group_rate_multipliers 30s TTL 解析 | `backend/internal/service/user_group_rate_resolver.go:44` |
| UsageLog struct（BillingType/ActualCost/RateMultiplier） | `backend/internal/service/usage_log.go` |
| JWTClaims / GenerateToken（仅 UserID/Email/Role/TokenVersion） | `backend/internal/service/auth_service.go:55` / `:1150` |
| api_keys schema（group_id 单值 nullable） | `backend/ent/schema/api_key.go` |
| groups schema（platform/rate_multiplier/daily_limit_usd/rpm_limit） | `backend/ent/schema/group.go` |
| usage_logs schema（append-only，only created_at） | `backend/ent/schema/usage_log.go` |
| payment_audit_logs schema（支付事件审计，硬删除） | `backend/ent/schema/payment_audit_log.go` |

## 4. 当前实现摘要

- **鉴权三轨（均实时校验）**：
  1. **API Key**：`Bearer`/`x-api-key`/`x-goog-api-key` → SHA-256 → L1 ristretto → L2 Redis `apikey:auth:<sha256>` → DB fallback。快照 `APIKeyAuthSnapshot`（`api_key_auth_cache.go:6`）含 `User.Balance/User.Status/Group.*/APIKey.Status/Quota/RateLimits`；恢复后**仍实时检查** `balance > 0`（`api_key_auth.go:211`）、quota/expire、订阅限额。
  2. **JWT**：验签后**立即从 DB 读用户**（`jwt_auth.go:62`），比对 `TokenVersion`（`:76`）。claims 仅 `UserID/Email/Role/TokenVersion`，**无 balance/group**。
  3. **Admin**：`x-api-key` 常量时间比对 `settings.admin_key`（`admin_auth.go:132`）或 JWT+role。
- **余额/扣费**：余额实时在 Redis（`billingCacheService.GetUserBalance`），请求完成后异步 worker 写 DB；`usage_logs` 追加写（append-only）。
- **定价/权限**：`group.platform → channel_model_pricing.models`（非空）或 `accounts.model_mapping` keys（fallback）决定 `/v1/models` 与可调用模型；`restrict_models` 在网关拦截（`channel_service.go:546`）。
- **费率**：最终账单 `tokens × price × group.rate_multiplier`；`user_group_rate_multipliers`（30s TTL，`user_group_rate_resolver.go:44`）支持 per-user 覆盖。
- **多租户现状**：扁平用户，仅 `user_allowed_groups`（专属分组白名单）+ `user_group_rate_multipliers`，**无父子账户/组织层级**。

## 5. 差距分析（商业能力 × 身份层）

| 维度 | 现状 | 与 OAuth/OIDC 的关系 | 差距 |
|---|---|---|---|
| 用户额度/余额 | Redis 实时 + DB 异步扣 | **绝不进 token**，UserInfo 实时返 | 无（设计已正确） |
| 实时计费 | `postUsageBilling`/`DeductBalance` | token 无关，access_token 调用后计费 | 无 |
| API Key | `api_keys` 表，含 `sk_oauth_` | 与 OAuth token 统一存储 | 无 |
| 模型权限 | group→channel pricing/restrict | **必须实时查**（channel cache 10min） | 不宜进 token（随 admin 变） |
| 团队/组织 | **无** org/team | OIDC 无 `org`/`groups` claim；token claims 不含 org/tenant | **明确不引入（已确认 2026-06-03）**，见 §9 |
| 服务账号 | **无** client_credentials | OIDC `client_credentials` 缺失 | 见 `03` G11 |
| 管理员审计 | 仅结构化日志 `system_logs`（无专表） | 与身份无强耦合 | **需补审计表**（§10） |
| 登录审计 | `users.last_login_at`，无 login_history 表 | OIDC `auth_time` 可记 | 缺逐次登录 IP/时间表 |

## 6. token claims 放 / 不放清单（核心结论）

**判据**：进 token 的信息必须满足 ——（a）稳定/低频变化、（b）非敏感、（c）即使陈旧也不造成越权或资损。

### ✅ 适合放（id_token / 短 TTL session JWT）
| 字段 | 理由 | 来源 |
|---|---|---|
| `sub`（=`user.ID` 字符串，已确认 2026-06-03） | 用户唯一身份，永不变 | `user.go` |
| `iss` / `aud` / `exp` / `iat` / `nonce` | OIDC 协议必需 | id_token |
| `email` / `email_verified` | 授 `email` scope（`email:read` 对第一方 client Image/CLI/Web/Chat 默认授予，已确认 2026-06-03）；变更低频 | `user.go` |
| `name` / `preferred_username` | 授 `profile`；低频 | `user.go` |
| `role` | 仅用于路由粗判，**且 jwt_auth 仍会实时复核**（`jwt_auth.go:76` TokenVersion + IsActive），陈旧也不放行越权 | `JWTClaims`（`auth_service.go:55`） |
| `token_version` | 用于全局失效（改密/封禁 bump 即作废所有旧 token） | 现已有 |

### ❌ 不放（必须服务端实时查询）
| 字段 | 反例理由 |
|---|---|
| **`balance`（余额）** | 每次 API 调用后扣减，短 TTL 内一定漂移。放进 token → 用户余额已 ≤0 仍显示有钱（资损）；或封号后旧 token 仍"看着有余额"。**余额检查热路径已读 Redis 实时值，token 化纯属反模式。** |
| `quota_used` / 配额 | 高频递增，Redis incr 实时；放 token 即过期 |
| `group_id` / `platform` | **粒度错位**：一个用户可有多个 API Key 绑多个 group；JWT 是**用户级**不是 **key 级**，放 group_id 会错绑 |
| `rate_multiplier` | group 维度，admin 随时改；放 token → 改价后旧 token 仍按旧费率（计费错误） |
| 模型权限 / `restrict_models` | channel 级、admin 可变（cache 10min）；放 token → 权限调整后旧 token 越权调用 |
| `subscription`/订阅限额 | 到期即变；token TTL 内会"续命"已过期订阅 |
| admin `admin_key` 等机密 | 绝不进任何 token |

> **一句话原则**：token 回答"你是谁（who）"，不回答"你现在有多少钱、能调什么、按什么价（how much / what / at what rate）"。后者全部实时查，已是现状（这是项目的正确设计，OIDC 改造必须维持）。

## 7. API Key vs OAuth access_token 共存策略

- **统一存储**：两者都在 `api_keys` 表。OAuth access_token 用 `sk_oauth_` 前缀、带 `expires_at`（短 TTL，默认 24h）；长期 API Key 无前缀、长期有效。
- **统一网关路径**：`/v1/*` 走 `APIKeyAuthMiddleware`（`api_key_auth.go`），对 `sk_oauth_` 与普通 key **无差别**——同一套 SHA-256 → L1/L2 缓存 → balance/quota/subscription 实时校验。**OIDC 改造不碰这条路径。**
- **列表过滤**：`/api/v1/keys` 用 `APIKeyListFilters.ExcludeKeyPrefix=sk_oauth_` 在 ent 查询层过滤，不在前端二次隐藏（避免跨页飘）。
- **选型建议**：
  - **长期机器对机器集成 / 用户手动粘贴到第三方客户端** → 长期 **API Key**（用户在控制台自助创建，绑定单一 group）。
  - **交互式登录 / 第三方 webapp 代表用户调用** → **OAuth access_token**（短 TTL + refresh，走 PKCE/device flow）。
- **id_token 不进网关**：id_token 仅供 RP 本地验证用户身份，**绝不**用作 `/v1/*` 的 Bearer。网关只认 access_token / API Key。

## 8. 各客户端认证选型

| 客户端 | 推荐认证 | 理由 | grant |
|---|---|---|---|
| **Sakrylle Web**（open-webui，`chat.sakrylle.com`） | OIDC 登录（机密 client + PKCE + server-side token storage）+ 代表用户的 access_token | 用户登录态由 OIDC 建立；回调路径 `https://chat.sakrylle.com/oauth/oidc/login/callback` | authorization_code + refresh + `openid profile email` + `/v1` 所需 scope |
| **Sakrylle CLI**（codex） | authorization_code+PKCE + **Device Authorization Flow** | 无浏览器/无回调端口；loopback `127.0.0.1` 任意端口 + device flow（RFC 8628 基础） | authorization_code + device_code + refresh；`openid profile email` + `/v1` 所需 |
| **Sakrylle Studio**（CodexMonitor，桌面） | 首发**复用 CLI 凭据**（后续独立登录再注册 public+loopback） | 减少首发注册面 | 复用 `sakrylle-cli` |
| **Sakrylle Chat**（kelivo，移动/跨端） | authorization_code + PKCE（自定义 scheme，实现期核实） | 移动原生回跳 `sakrylle-chat://oauth/callback` | `openid profile email` + `/v1` 所需 |
| **Sakrylle Image**（已上线） | 公共 SPA + PKCE | 现状即如此（`sakrylle-image-playground`，沿用现有 client_id） | 原 `image_generation balance:read models:read` + 追加 `openid profile email` |
| **服务器对服务器**（如未来后台任务） | **client_credentials**（待补，`03` G11）或长期 API Key | 无用户上下文 | client_credentials |

> client_id / redirect_uri / scope / pkce 详表见 `03` §9。

## 9. 权限模型：现状与演进路径

### 现状（扁平 + group 粒度）
- **角色**：`role`（admin / 普通用户），admin 路由走 `AdminAuthMiddleware`。
- **资源访问**：API Key 绑 group；`user_allowed_groups` 控专属分组白名单；`user_group_rate_multipliers` 控 per-user 费率。
- **无 org/team/workspace**：用户扁平，无父子账户。

### 演进路径（分阶段，均「需额外审批」落生产）
1. **服务账号（短期，低成本）**：补 `client_credentials`（`03` G11）。机密 client 凭 `client_secret` 取 token，绑定一个"服务用户"+ group。无需新表，复用 `oauth_clients` + `api_keys`。
2. **团队/组织（明确不引入，已确认 2026-06-03）**：不新增 `organizations`/`org_members` 表、不引入 org/team/workspace 多租户；token claims 不含 org/tenant。下方"演进后"列仅为假设性参考，**非当前路线图**。若未来产品确有团队计费/共享额度需求，须重开独立设计文档 + 审批。
3. **多租户隔离（同上，不引入）**：org 级共享余额池 / 配额需把计费从 user-balance 重构到 org-balance，风险高 —— 当前决策不做。

| 主体 | 现状能力 | 演进后（org 行仅假设性参考，已确认 2026-06-03 不引入） |
|---|---|---|
| 管理员 | 全局 admin 路由 | 不变（不引入 org 级 admin） |
| 普通用户 | 自有 key/group/余额 | 不变 |
| 团队成员 | **无** | **不引入** org/team |
| 服务账号 | **无**（只能用长期 API Key） | client_credentials（短期可补） |

## 10. 审计与统计差距

- **使用量统计**：`usage_logs`（append-only，`usage_log.go`）已完整记 `BillingType/ActualCost/RateMultiplier` —— 充分。
- **支付审计**：`payment_audit_logs` 已有（硬删除）。
- **缺口**：
  - **无专用管理员操作审计表**：仅 `ops_system_log_sink` 把 WARN/ERROR + `component=audit` + `http.access` 写 `system_logs`（结构化日志），无 `admin_audit_logs` 专表。OIDC 上线后 admin 改 client/scope/key 等敏感操作建议补专表。
  - **无登录审计表**：仅 `users.last_login_at`，无逐次登录 IP/时间。OIDC `auth_time` 可作为补充信号；建议补 `login_history`。
  - 「不确定」：`system_logs` 是否已含 login 类事件（`OpsSystemLogSink.shouldIndex` 仅索引 WARN/ERROR + audit + http.access）—— 需确认。

## 11. 风险

- **R1（中）**：auth 快照含 `User.Balance`（`api_key_auth_cache.go:6`），与 `billingCacheService` Redis 实时余额**两路独立**，TTL 内存在短暂不一致窗口（snapshot 显示有钱、Redis 已 ≤0）。中间件 `api_key_auth.go:211` 读的是 snapshot 值 —— 建议余额判断统一以 Redis 实时值为准（这恰好印证"余额绝不进 token"原则）。「不确定」两者优先级，需确认。
- **R2（中）**：channel cache 无主动失效（TTL 10min，`channel_service.go:136`），admin 改 `restrict_models`/pricing 后最长 10 分钟生效 —— 再次说明**模型权限不能进 token**（token TTL 通常更长，漂移更久）。
- **R3（低）**：`APIKeyAuthSnapshot.Version=11` 升级会全量缓存 miss，高峰升级有 DB 压力峰值。
- **R4（中）**：若误把 group_id/role 当"授权决策唯一依据"放进 token 并跳过实时校验，将破坏现有"每请求实时校验"护栏。**OIDC 改造严禁引入 token-only 放行路径。**

## 12. 验收标准

1. id_token / session JWT 的 claims 严格限于 §6 ✅ 清单，**无 balance/group_id/rate_multiplier/模型权限**。
2. `/v1/*` 网关对 `sk_oauth_` access_token 与普通 API Key **零差别**处理，余额/额度/模型权限**全部实时查询**。
3. id_token 不能作为网关 Bearer（被拒）。
4. 余额判断以 Redis 实时值为准（消除 R1 不一致窗口）。
5. 任何权限模型演进（org/team/服务账号）落生产前经审批，且不破坏"每请求实时校验"。

## 13. 后续问题

- `billingCacheService.GetUserBalance` 与 `APIKeyAuthSnapshot.User.Balance` 在 balance≤0 判断上的优先级？（**不确定**，R1）
- ~~是否有真实团队/组织/共享额度需求？~~ **已确认 2026-06-03：不引入 org/team/多租户**（§9）。
- `system_logs` 是否已含 login 事件？是否需 `login_history` / `admin_audit_logs` 专表？（**不确定**）
- `user_group_rate_multipliers` 完整 schema（无 ent 文件，需从 SQL repo 推断）。
