---
title: 91 · Sakrylle 生态风险登记册
status: canonical
scope: program
last_verified: 2026-06-06
---

# 91 · Sakrylle 生态风险登记册

> 规划文档（planning only）。Sakrylle API / Sakrylle Image **已上线生产**，凡涉及生产的缓解措施均标注「需额外审批」。
> 兄弟文档：`03-sakrylle-api-oidc-architecture.md`（OIDC 密钥方案）、`05-configuration-isolation-standard.md`（隔离规范）、`90-roadmap.md`（路线图）、`92-open-questions.md`（待确认问题）、`93-implementation-checklist.md`（实施 checklist）。

---

## 1. 风险评级标准

| 维度 | 说明 |
|---|---|
| **影响（Impact）** | 极高 = 生产中断/数据泄露/全量用户受影响；高 = 功能完全失效/安全漏洞/部分用户受影响；中 = 功能降级/用户体验损坏；低 = 开发效率/轻微不一致 |
| **概率（Probability）** | 高 = 不处理必然发生；中 = 可能发生；低 = 特定条件才触发 |
| **综合优先级** | 极高 × 高 = P0；高 × 高 = P0；高 × 中 = P1；中 × 中 = P2；低 × 任意 = P3 |

---

## 2. 生产安全风险

### R-PROD-01：RS256 私钥泄露导致全量 id_token 伪造

| 字段 | 内容 |
|---|---|
| **风险描述** | OIDC id_token 采用 RS256 非对称签名，私钥若泄露，攻击者可伪造任意用户的 id_token，绕过所有 RP 认证 |
| **影响** | 极高——全量用户身份被冒充，波及所有接入 OIDC 的产品 |
| **概率** | 中——私钥存储方案若不当（明文文件、弱权限）则概率升高 |
| **综合优先级** | **P0** |
| **缓解措施** | 私钥 AES-256-GCM 加密存 DB（`security_secrets` 表，KEK = **共享的 `TOTP_ENCRYPTION_KEY`**（`cfg.Totp.EncryptionKey`），≥32 字节；代码**不读** `OIDC_KEY_ENCRYPTION_KEY`，该 env 名无效；同一 KEK 还保护 TOTP / channel-monitor / backup secrets，**轮换即同时作废以上所有密文**，故视作单一共享根密钥、不可单独轮换；未设则启动时自动生成临时密钥，重启即失效，生产必须固定）；进程内缓存、最小读权限；kid 轮换："先发布后签发"，双 kid 并存 90 天轮换窗口，**RSA 与 EC（ES256）轮换 + grace-period 清理已对等实现（2026-06-04）**；私钥绝不出现在日志/debug output；泄露应急：生成新密钥→更新 JWKS→所有旧 id_token 失效（RP 端重新授权）；**密钥注入生产需额外审批**。**自动调度器**（2026-06-05）：`OIDCKeyRotationScheduler` 双 goroutine（rotation + cleanup），配置项 `oidc_auto_rotation_enabled`/`oidc_key_rotation_interval_hours`/`oidc_key_cleanup_interval_hours`；手动触发接口仍保留 |
| **负责产品** | Sakrylle API |
| **相关文件** | `backend/internal/service/auth_service.go:1172`（HS256 参考）；`backend/migrations/`（确认 security_secrets 表，见 `92` Q-01）；`backend/internal/config/config.go:1195` |

### R-PROD-02：`oauth_scope_enforcement_enabled` 默认 false，sk_oauth_ token 可访问全部路由

| 字段 | 内容 |
|---|---|
| **风险描述** | `settings.oauth_scope_enforcement_enabled` 当前为 `false`（migration 145 seed），中间件 scope 校验处于 kill-switch 关闭状态，任何合法 `sk_oauth_` token 均可调用所有 `/v1/*` 端点，无 scope 粒度限制 |
| **影响** | 高——低权限 token（如仅 `image_generation`）可调用 `/v1/chat/completions`，破坏授权边界 |
| **概率** | 高——当前线上状态，风险始终存在 |
| **综合优先级** | **P0**（开放——OIDC 基座已完成后，这是 IdP 侧主要遗留风险，需有意识的生产决策） |
| **缓解措施** | OIDC Core + ES256 已于 2026-06-04 实现，但 scope enforcement 仍默认 `false`。**开启是生产决策，需审批**：开启前对所有已注册 RP（尤其 Sakrylle Image）验证 scope 覆盖完整性；与 Image 团队对齐所需 scope 列表；预览环境全量回归；开启操作「需额外审批」；开启后监控 403 错误率 |
| **负责产品** | Sakrylle API |
| **相关文件** | `backend/internal/service/oauth_scopes.go:25`；`backend/migrations/145_oauth_v2.sql`；`backend/internal/server/routes/oauth.go`；`92` Q-02 |

### R-PROD-03：生产数据库写操作未经审批导致不可逆变更

| 字段 | 内容 |
|---|---|
| **风险描述** | OIDC 基座涉及多处直接 SQL 操作（`settings` 表、`oauth_clients` 表、密钥写入），文档流程若不完善，开发者可能绕过审批直接操作生产 DB |
| **影响** | 极高——破坏生产 OAuth client 注册、误覆盖 issuer 设置、密钥写入错误 |
| **概率** | 低——需人为操作失误 |
| **综合优先级** | **P1** |
| **缓解措施** | 所有生产 DB 写操作、`settings` 表变更、密钥注入均标注「需额外审批」，执行前发送 PR 审批；`CLAUDE.md` 审批流（`ssh-tokyo + docker compose`）严格遵守；SQL 变更前在本地/预览环境先演练；密钥注入使用原子更新（`INSERT ... ON CONFLICT DO UPDATE`，避免残留旧值）；issuer 变更须视同 major breaking change |
| **负责产品** | Sakrylle API |
| **相关文件** | `CLAUDE.md`（审批流程）；`/opt/stack/sub2api/.env`（KEK 注入位置）|

### R-PROD-04：Sakrylle Image OIDC 升级破坏现有生产登录流程

| 字段 | 内容 |
|---|---|
| **风险描述** | Image 已在生产用 OAuth PKCE，OIDC 升级（加 `openid` scope、解析 id_token）若服务端未返回 id_token，客户端行为退化；若 scope 注册不完整，现有 `sk_oauth_*` token 可能被 scope enforcement 误拦 |
| **影响** | 高——生产用户登录中断 |
| **概率** | 低——OIDC 基座已于 2026-06-04 完成（服务端含 `openid` 时返回 id_token、`/v1/me` 返回 `sub`）；`scope=openid` 为增量，服务端未返回 id_token 也只静默降级 |
| **综合优先级** | **P1**（触生产，但基座就绪后概率降低） |
| **缓解措施** | 服务端 OIDC 基座 M1 已完成；Image 升级前在预览环境验证完整登录流程；`scope=openid` 是增量（服务端若未返回 id_token 不报错，客户端静默降级）；生产上线「需额外审批」；上线后即时监控 401/登录失败率 |
| **负责产品** | Sakrylle Image / Sakrylle API |
| **相关文件** | `gpt_image_playground/src/lib/sakrylleAuth.ts:16`（scope 字符串）；`gpt_image_playground/deploy/Dockerfile`；`gpt_image_playground/src/vite-env.d.ts` |

---

## 3. OIDC 密钥管理风险

### R-KEY-01：issuer 域名决策错误导致全量 RP 重配

| 字段 | 内容 |
|---|---|
| **风险描述** | OIDC issuer（`oauth_issuer` setting）一经发布即嵌入所有 RP 缓存的 discovery 和已签发 id_token 的 `iss` claim，**不可轻易更改**——更改即令所有已签发 token 失效，RP 需重新配置 |
| **影响** | 极高——全量 RP 需重新发现，所有在途 token 失效 |
| **概率** | 低——如在 Phase 0 锁定决策则不会发生 |
| **综合优先级** | **P1**（已有缓解，低概率触发）|
| **缓解措施** | issuer 已在 `03` §6 锁定为 `https://sub.sakrylle.com`，与 migration 148 seed 一致；生产 `settings.oauth_issuer` 只读核对（Phase 0 任务，见 `92` Q-03）；issuer 变更须视同 major breaking change，需独立 ADR（Architecture Decision Record）且全 RP 同步重配 |
| **负责产品** | Sakrylle API |
| **相关文件** | `backend/migrations/148_oauth_v2_sakrylle_seed.sql`；`backend/internal/handler/oauth_provider_handler.go:642`；`92` Q-03 |

### R-KEY-02：JWKS 缓存 TTL 与 kid 轮换窗口不匹配导致验签失败

| 字段 | 内容 |
|---|---|
| **风险描述** | RP 侧缓存 JWKS（按 `Cache-Control` TTL），若 JWKS 端点 TTL 过长，在 kid 轮换期间 RP 持有旧 JWKS，新 kid 签发的 id_token 验签失败；若 TTL 过短，RP 每次验签都请求 JWKS 影响性能 |
| **影响** | 高——用户登录验签失败 |
| **概率** | 中——轮换窗口设计不当即触发 |
| **综合优先级** | **P1** |
| **缓解措施** | 轮换策略"先发布后签发"：新 key 进 JWKS 但暂不签发，等待 ≥ JWKS Cache-Control TTL（建议 3600s）后再切为签发 key；JWKS 同时发布当前 + 上一个 kid（双 kid 并存），**RSA + EC（ES256）各自发布 current + previous，RP 可跨两种算法验证轮换前后的 id_token（2026-06-04）**；`Cache-Control: max-age=3600` 平衡性能与轮换敏感度；过期旧 key 由 `CleanupExpiredKeys` 在 grace-period TTL（`oidc_grace_period_ttl_seconds`，默认 24h）后清理（**自动调度器** 2026-06-05 实现：`OIDCKeyRotationScheduler` 双 goroutine，配置 `oidc_auto_rotation_enabled`/`oidc_key_cleanup_interval_hours`）；见 `03` §7 |
| **负责产品** | Sakrylle API |
| **相关文件** | `03-sakrylle-api-oidc-architecture.md` §7 |

### R-KEY-03：HS256 对称 session secret 泄露令全量用户会话失陷

| 字段 | 内容 |
|---|---|
| **风险描述** | 用户会话 JWT（登录 session）使用 HS256 对称 `jwt.secret`（`auth_service.go:1172`），secret 泄露即可伪造任意用户会话；与 RS256 id_token 是独立问题，但同样严重 |
| **影响** | 高——全量用户会话被伪造 |
| **概率** | 低——`.env` 严格保护则概率低 |
| **综合优先级** | **P1** |
| **缓解措施** | `JWT_SECRET` ≥ 32 字节，仅存 `/opt/stack/sub2api/.env`（mode 600，gitignore）；绝不出现在日志；泄露应急：轮换 JWT_SECRET → 强制全量用户重新登录（`TokenVersion` 递增）；与 RS256 私钥 KEK（**共享的 `TOTP_ENCRYPTION_KEY`**，非 `OIDC_KEY_ENCRYPTION_KEY`——后者代码不读）分开存储，分开轮换 |
| **负责产品** | Sakrylle API |
| **相关文件** | `backend/internal/service/auth_service.go:1172`；`/opt/stack/sub2api/.env` |

---

## 4. 配置冲突风险

### R-CONF-01：CLI 与上游 codex 争 `~/.codex` 目录

| 字段 | 内容 |
|---|---|
| **风险描述** | codex 所有路径集中在 `CODEX_HOME`（默认 `~/.codex`，`utils/home-dir/src/lib.rs:13`），包含 auth.json、sessions/、memories/、SQLite 数据库（state_5.sqlite 等）、UNIX socket、PID 文件；若 Sakrylle CLI fork 未改默认路径，两者共用同一目录，auth.json 被覆盖、socket 竞争、SQLite 写冲突 |
| **影响** | 高——破坏用户已有的 openai codex 配置；造成不可逆的本地状态污染 |
| **概率** | 高——不改 `find_codex_home()` 默认值则必然发生 |
| **综合优先级** | **P0** |
| **缓解措施** | CLI fork 第一个 commit 修改 `codex-rs/utils/home-dir/src/lib.rs:13`，优先读 `SAKRYLLE_CLI_HOME` 环境变量，默认值改为 `~/.sakrylle-cli`；同步修改 `/etc/codex` → `/etc/sakrylle`；绝不复用 `CODEX_*` 环境变量名；安装文档明确说明隔离机制；需在 Q-09 核查后确认无残留 `.codex` 硬编码漏改 |
| **负责产品** | Sakrylle CLI |
| **相关文件** | `codex-rs/utils/home-dir/src/lib.rs:13,59`；`codex-rs/config/src/loader/mod.rs:52`；`05-configuration-isolation-standard.md` §7；`92` Q-09 |

### R-CONF-02：Studio daemon 端口 4732 与上游 CodexMonitor 冲突

| 字段 | 内容 |
|---|---|
| **风险描述** | CodexMonitor daemon 监听 `0.0.0.0:4732`（`daemonctl.rs:28`），若上游 CodexMonitor 和 Sakrylle Studio 同时运行，端口冲突导致 Studio daemon 启动失败 |
| **影响** | 中——Studio daemon 模式（远程 backend）不可用 |
| **概率** | 中——开发者常同时安装多个版本 |
| **综合优先级** | **P2** |
| **缓解措施** | Studio fork 将 daemon 默认端口改为 4733（`src-tauri/src/bin/codex_monitor_daemonctl.rs:28`）；同时支持通过环境变量覆盖端口；安装文档说明端口配置 |
| **负责产品** | Sakrylle Studio |
| **相关文件** | `src-tauri/src/bin/codex_monitor_daemonctl.rs:28`；`05-configuration-isolation-standard.md` §6 |

### R-CONF-03：open-webui `WEBUI_NAME` 强制追加 `(Open WebUI)` 后缀

| 字段 | 内容 |
|---|---|
| **风险描述** | `open-webui/backend/open_webui/env.py:772-773` 硬编码逻辑：若 `WEBUI_NAME != 'Open WebUI'` 则自动追加 ` (Open WebUI)` 后缀，导致设置 `WEBUI_NAME=Sakrylle Web` 时实际显示 `Sakrylle Web (Open WebUI)`，品牌无法完全替换 |
| **影响** | 中——品牌一致性破坏，用户可见 |
| **概率** | 高——不改代码必然触发 |
| **综合优先级** | **P1** |
| **缓解措施** | fork 时删除 `env.py:772-773` 的追加逻辑；同时修改 `constants.ts:4`（`APP_NAME='Sakrylle Web'`）和 `app.html:118` 中硬编码的 title；需重新 docker build（不能仅通过 volume mount 解决）；`site.webmanifest` 同步修改 `name`/`short_name` |
| **负责产品** | Sakrylle Web |
| **相关文件** | `open-webui/backend/open_webui/env.py:771-773`；`open-webui/src/lib/constants.ts:4`；`open-webui/src/app.html:118`；`open-webui/static/static/site.webmanifest` |

### R-CONF-04：kelivo SharedPreferences key 无命名空间前缀

| 字段 | 内容 |
|---|---|
| **风险描述** | kelivo 全部 SharedPreferences key（`settings_provider.dart:44-101`）无命名空间前缀（如 `providers_order_v1` 直接存），理论上与同宿主其他 Flutter 应用共享同一存储命名空间时可碰撞；实际隔离依赖 bundle id 驱动的存储路径 |
| **影响** | 低——bundle id 修改后路径已隔离，碰撞概率极低 |
| **概率** | 低——bundle id 改了即路径隔离 |
| **综合优先级** | **P3** |
| **缓解措施** | 优先完成 bundle id 四处替换（`com.sakrylle.chat`），路径隔离后碰撞问题自动消除；可选后续：为 key 加 `sakrylle.chat.` 前缀（breaking change，需数据迁移，成本较高，Phase 3 再评估）|
| **负责产品** | Sakrylle Chat |
| **相关文件** | `kelivo/lib/core/providers/settings_provider.dart:44-101`；`kelivo/android/app/build.gradle.kts:27` |

---

## 5. Token 过大 / 额度不同步风险

### R-TOKEN-01：auth cache 快照余额与实时 Redis 余额不一致

| 字段 | 内容 |
|---|---|
| **风险描述** | `APIKeyAuthSnapshot`（`api_key_auth_cache.go`）含 `User.Balance`，缓存 TTL 内可能持有过时余额；`api_key_auth.go:211` 的余额检查读的是 snapshot 值，而非 Redis 实时余额；用户余额恰好在缓存有效期内耗尽，系统可能在短暂窗口内继续放行请求 |
| **影响** | 中——短暂超额消费（分钟级别），不是无限漏洞 |
| **概率** | 中——余额接近 0 时每次请求均可能触发 |
| **综合优先级** | **P2** |
| **缓解措施** | 热路径实时余额走 `billingCacheService.GetUserBalance`（Redis HGET），snapshot 余额仅作初步过滤；关键扣费路径（`gateway_service.go postUsageBilling`）独立于 snapshot，走 Redis HINCRBYFLOAT；缓存 TTL 不应过长；监控余额异常扣负值告警 |
| **负责产品** | Sakrylle API |
| **相关文件** | `backend/internal/service/api_key_auth_cache.go`；`backend/internal/server/middleware/api_key_auth.go:211`；`backend/internal/service/billing_cache_service.go`；`92` Q-07 |

### R-TOKEN-02：channel cache 无主动失效，pricing 变更最多 10 分钟后生效

| 字段 | 内容 |
|---|---|
| **风险描述** | `channel_service.go:136` channel cache TTL 10 分钟，无主动失效机制；admin 修改 `restrict_models`、定价行、model_mapping 后，最多 10 分钟内仍按旧配置运行，可能导致错误放行或拦截 |
| **影响** | 中——pricing 错误或模型被错误放行/拦截 |
| **概率** | 中——每次 admin 改 channel 配置均发生 |
| **综合优先级** | **P2** |
| **缓解措施** | 已知问题，`CLAUDE.md` 记录：变更后手动 `docker compose restart sub2api` 立即清除缓存；Phase 3 可考虑 admin 操作触发 Redis Pub/Sub 失效信号（参照 auth cache 失效机制 `api_key_auth_cache_invalidate.go`）；当前运维靠流程控制 |
| **负责产品** | Sakrylle API |
| **相关文件** | `backend/internal/service/channel_service.go:136`；`backend/internal/service/api_key_auth_cache_invalidate.go` |

### R-TOKEN-03：OIDC id_token claims 中携带可变状态导致客户端缓存旧值

| 字段 | 内容 |
|---|---|
| **风险描述** | 若 id_token 携带 `group_id`、`rate_multiplier`、`balance` 等可变字段，RP 端解析后缓存 claims，实际配置变更后 RP 仍持有旧值，导致权限错误判断；`exp` 期内无法撤销 |
| **影响** | 高——RP 端错误展示或应用已失效的权限状态 |
| **概率** | 低——已由代码护栏强制（2026-06-04）：`assertNoForbiddenClaims` fail-closed，违反即拒绝签发 |
| **综合优先级** | **P1**（已有代码级缓解） |
| **缓解措施** | 已实现并强制：`BuildIDTokenClaims` 仅允许 `iss/sub/aud/exp/iat/nonce/auth_time/name/preferred_username/email/email_verified`；纵深防御 allowlist 守卫 `assertNoForbiddenClaims` **fail-closed**——`balance`/`group`/`group_id`/`rate_multiplier`/`quota`/`quota_used`/`daily_limit_usd`/`model_mapping`/`models`/`restrict_models`/`capabilities`/`allowed_groups` 任一出现即拒签。这些字段由 RP 通过 `/v1/me` 实时查询；网关路径继续实时校验（`api_key_auth.go`），OIDC 严禁 token-only 放行 |
| **负责产品** | Sakrylle API / 各 RP |
| **相关文件** | `04-oauth-oidc-commercial-capabilities.md`；`backend/internal/handler/oauth_provider_account_handler.go:76`；`backend/internal/service/oauth_provider_service.go`（`mintTokensFromCode`） |

---

## 6. 上游 rebase 冲突风险

### R-REBASE-01：Sakrylle CLI 上游 codex 频繁更新 SQLite 文件名版本号

| 字段 | 内容 |
|---|---|
| **风险描述** | codex-rs SQLite 文件名含版本尾缀（`state_5.sqlite`、`logs_2.sqlite` 等，`state/src/lib.rs:81-84`），上游升级时文件名变更，fork 若直接 rebase 需处理数据迁移；旧 SQLite 文件被遗留，新版本重建空 DB |
| **影响** | 中——用户会话历史/memories/goals 丢失 |
| **概率** | 中——上游已有多次版本迭代 |
| **综合优先级** | **P2** |
| **缓解措施** | rebase 前检查 `codex-rs/state/src/lib.rs` 中常量变更；若文件名版本升级，在 fork 中提供迁移脚本（旧文件 rename 或数据 copy）；维护 rebase CHANGELOG 记录此类 breaking change |
| **负责产品** | Sakrylle CLI |
| **相关文件** | `codex-rs/state/src/lib.rs:79-84` |

### R-REBASE-02：relay-pulse（Sakrylle Status）上游 rebase 冲突集中区

| 字段 | 内容 |
|---|---|
| **风险描述** | `CLAUDE.md` 明确记录 relay-pulse rebase 冲突集中在 8 个文件：前端 i18n locales、`internal/api/meta.go`、主题 CSS、`Header.tsx`、`StatusTable/StatusCard/Footer/RefreshButton/ChannelTypeIcon.tsx`、`useTheme.ts`、`router.tsx`、`Dockerfile`；上游每次更新都会触发冲突；删除的页面（ContactPage/OnboardingPage/ChangeRequestPage）会在 rebase 时重新出现 |
| **影响** | 中——rebase 人工干预成本高，延误更新 |
| **概率** | 高——每次 rebase 必然触发 |
| **综合优先级** | **P2** |
| **缓解措施** | 维护冲突文件清单（`CLAUDE.md` 已记录）；考虑将 Sakrylle 品牌改动提取为独立 patch 集，rebase 后 `git apply` 重新应用；删除的页面/主题每次 rebase 后需重新确认已删除 |
| **负责产品** | Sakrylle API（relay-pulse 子服务）|
| **相关文件** | `CLAUDE.md` §status.sakrylle.com；`frontend/src/i18n/locales/*.json`；`internal/api/meta.go` |

### R-REBASE-03：sub2api 上游 rebase 冲突文件，OIDC 新增后扩大冲突面积

| 字段 | 内容 |
|---|---|
| **风险描述** | `CLAUDE.md` 记录已知冲突文件：`frontend/tailwind.config.js`、`frontend/src/views/HomeView.vue`、`frontend/src/components/layout/AppSidebar.vue`、`backend/internal/service/setting_service.go`；OIDC 基座新增后，`oauth.go`/`oauth_provider_handler.go`/`oauth_provider_service.go`/`oauth_scopes.go` 将成为新的冲突热点 |
| **影响** | 中——上游 bugfix/feature 无法干净合并 |
| **概率** | 中——依上游活跃程度 |
| **综合优先级** | **P2** |
| **缓解措施** | 遵循 `CLAUDE.md` 同步流程：`git fetch upstream && git rebase upstream/main`；OIDC 相关改动尽量**新增文件**而非大幅修改现有文件（如新建 `oidc_key_service.go`、`oidc_handler.go`），降低冲突面积；rebase 前更新 `CLAUDE.md` 补充 OIDC 冲突热点文件列表 |
| **负责产品** | Sakrylle API |
| **相关文件** | `CLAUDE.md` §同步上游；`backend/internal/server/routes/oauth.go`；`backend/internal/handler/oauth_provider_handler.go` |

---

## 7. 凭据安全风险

### R-CRED-01：kelivo API key 明文存 SharedPreferences

| 字段 | 内容 |
|---|---|
| **风险描述** | kelivo `settings_provider.dart:44-101` 用 SharedPreferences（JSON 序列化）存 API key，Android root 设备/iOS 越狱设备/macOS 开发模式可读取；Sakrylle Chat 若沿用此方案，用户 Sakrylle API key 暴露 |
| **影响** | 高——API key 泄露，用户账户余额被滥用 |
| **概率** | 中——root/越狱设备比例取决于用户群 |
| **综合优先级** | **P1** |
| **缓解措施** | fork 时迁移至 `flutter_secure_storage`（利用 iOS Keychain、Android Keystore、macOS Keychain、Windows Credential Manager）；OAuth access_token（短 TTL 24h）比静态 API key 风险低，优先推动 OAuth 登录路径；迁移时提供 SharedPreferences→FlutterSecureStorage 一次性数据迁移逻辑 |
| **负责产品** | Sakrylle Chat |
| **相关文件** | `kelivo/lib/core/providers/settings_provider.dart:44-101`；`kelivo/pubspec.yaml`（需加 `flutter_secure_storage` 依赖） |

### R-CRED-02：Sakrylle Image refresh_token 存 localStorage（XSS 可读）

| 字段 | 内容 |
|---|---|
| **风险描述** | `gpt_image_playground/src/lib/sakrylleAuth.ts:307` 将 `access_token + refresh_token` 序列化存 localStorage，XSS 攻击可读取 refresh_token 持久控制账号；SPA 不建议持有 refresh_token（`OAUTH_V2_INTEGRATION.md` §11 已标注）|
| **影响** | 高——refresh_token 泄露后攻击者可持续获取新 access_token |
| **概率** | 低——需 XSS 漏洞配合，当前 CSP 和 XSS 防护到位则概率低 |
| **综合优先级** | **P2** |
| **缓解措施** | 短期：review CSP 配置，确保 XSS 向量最小化；中期：考虑 BFF（Backend for Frontend）模式持有 refresh_token（SPA 只持有短 TTL access_token），但会增加架构复杂度；OIDC 升级不恶化也不改善此问题，当前维持现状并标注为已知风险 |
| **负责产品** | Sakrylle Image |
| **相关文件** | `gpt_image_playground/src/lib/sakrylleAuth.ts:295-308`；`gpt_image_playground/docs/OAUTH_V2_INTEGRATION.md` §11 |

### R-CRED-03：Studio Sentry DSN 硬编码，崩溃报告发送上游第三方

| 字段 | 内容 |
|---|---|
| **风险描述** | CodexMonitor `src/main.tsx:9` 硬编码 Dimillian 的 Sentry DSN，fork 后崩溃报告（含设备信息、用户行为上下文）发送到第三方 Sentry 项目，泄露用户隐私 |
| **影响** | 中——隐私合规风险，用户数据发送到与 Sakrylle 无关的第三方 |
| **概率** | 高——不修改必然触发（每次崩溃） |
| **综合优先级** | **P1** |
| **缓解措施** | fork 时将 `src/main.tsx:9` 的硬编码 DSN 改为只读 `VITE_SENTRY_DSN` 环境变量（不提供默认值，即 disabled）；在 `.env.production` 中填入 Sakrylle 自己的 Sentry DSN；或完全注释掉 Sentry 初始化（如不需要崩溃上报） |
| **负责产品** | Sakrylle Studio |
| **相关文件** | `CodexMonitor/src/main.tsx:9` |

---

## 8. Responses API 风险

### R-RESP-01：Responses API 兼容性未充分验证

| 字段 | 内容 |
|---|---|
| **风险描述** | `backend/internal/handler/gateway_handler_responses.go` 已实现 `/v1/responses`，但 codex 客户端（Rust reqwest）对响应格式有严格期望（streaming SSE、tool_call、文件引用等），若实现不完整，CLI 功能降级或 panic |
| **影响** | 高——Sakrylle CLI 功能不完整或频繁崩溃 |
| **概率** | 中——Responses API 规范较复杂，sub2api 实现细节差异可能存在 |
| **综合优先级** | **P1** |
| **缓解措施** | Phase 2 CLI 对接前，在 dev 环境运行 codex 对接测试（完整 TUI 流程：发消息、file edit、shell exec）；重点测试 streaming SSE 和 tool_call 格式；记录已知不兼容项，明确是否需要 sub2api 侧 patch |
| **负责产品** | Sakrylle CLI / Sakrylle API |
| **相关文件** | `backend/internal/handler/gateway_handler_responses.go`；`backend/internal/server/routes/gateway.go:91-105`；`codex-rs/model-provider-info/src/lib.rs:46` |

---

## 9. 风险汇总矩阵

| ID | 风险名称 | 影响 | 概率 | 优先级 | 负责产品 |
|---|---|---|---|---|---|
| R-PROD-01 | RS256 私钥泄露 | 极高 | 中 | **P0** | Sakrylle API |
| R-PROD-02 | scope enforcement 默认关闭（开放，开启需生产审批） | 高 | 高 | **P0** | Sakrylle API |
| R-PROD-03 | 生产 DB 未经审批直接变更 | 极高 | 低 | **P1** | Sakrylle API |
| R-PROD-04 | Image OIDC 升级破坏生产登录（基座已就绪，概率降低） | 高 | 低 | **P1** | Image / API |
| R-KEY-01 | issuer 域名决策错误 | 极高 | 低 | **P1** | Sakrylle API |
| R-KEY-02 | JWKS 缓存与 kid 轮换不匹配 | 高 | 中 | **P1** | Sakrylle API |
| R-KEY-03 | HS256 session secret 泄露 | 高 | 低 | **P1** | Sakrylle API |
| R-CONF-01 | CLI 争 `~/.codex` 目录 | 高 | 高 | **P0** | Sakrylle CLI |
| R-CONF-02 | Studio daemon 端口 4732 冲突 | 中 | 中 | **P2** | Sakrylle Studio |
| R-CONF-03 | open-webui 品牌后缀 bug | 中 | 高 | **P1** | Sakrylle Web |
| R-CONF-04 | kelivo SharedPreferences 无前缀 | 低 | 低 | **P3** | Sakrylle Chat |
| R-TOKEN-01 | auth cache 余额与 Redis 不一致 | 中 | 中 | **P2** | Sakrylle API |
| R-TOKEN-02 | channel cache 无主动失效 | 中 | 中 | **P2** | Sakrylle API |
| R-TOKEN-03 | id_token 携带可变状态（已由 fail-closed 护栏强制） | 高 | 低 | **P1** | API / 各 RP |
| R-REBASE-01 | CLI SQLite 文件名版本升级 | 中 | 中 | **P2** | Sakrylle CLI |
| R-REBASE-02 | relay-pulse rebase 冲突集中 | 中 | 高 | **P2** | Sakrylle API |
| R-REBASE-03 | sub2api rebase 冲突面扩大 | 中 | 中 | **P2** | Sakrylle API |
| R-CRED-01 | kelivo API key 明文存储 | 高 | 中 | **P1** | Sakrylle Chat |
| R-CRED-02 | Image refresh_token 存 localStorage | 高 | 低 | **P2** | Sakrylle Image |
| R-CRED-03 | Studio Sentry DSN 硬编码 | 中 | 高 | **P1** | Sakrylle Studio |
| R-RESP-01 | Responses API 兼容性未验证 | 高 | 中 | **P1** | CLI / API |
