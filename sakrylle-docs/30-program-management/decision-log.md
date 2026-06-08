---
title: 92 · Sakrylle 生态待确认问题清单
status: canonical
scope: program
last_verified: 2026-06-06
---

> **定位说明（2026-06-06）**：本文件由原 `92-open-questions.md` 重定位为决策记录与实现期核查项。若需判断当前平台能力，请优先阅读 [`../10-platform-identity/current-state.md`](../10-platform-identity/current-state.md)。

# 92 · Sakrylle 生态待确认问题清单

> 规划文档（planning only）。本文汇总各调研文档（api-oauth、api-commercial、cli、studio、web、chat、image、isolation）中标注为「不确定」的问题，去重后分类列出。每个问题标注：确认方式（代码核查 / 用户决策 / 生产只读查询）、阻塞哪个任务、优先级。
> 兄弟文档：问题确认后，结论应回填到对应原始文档并同步更新 `90-roadmap.md` 里程碑。**生产只读查询均不改任何数据。**
>
> **2026-06-03 分诊状态**：30 问全部分诊完毕——13 个 ✅ RESOLVED（用户拍板 9 + 代码核实 4），17 个 🔧 CODE-CHECK（实现期 Phase 0 自查，无开放决策），0 个 ❓ 待确认。用户拍板值与代码核实结论已回填 `90`/`93`/`00`。详见文末「问题数量统计」。

---

## 问题状态图例

| 图标 | 含义 |
|---|---|
| ❓ | 待确认 |
| ✅ RESOLVED | 已确认（用户拍板或代码核实，记录结论与依据） |
| 🔧 CODE-CHECK | 留待实现期自查（各产品 Phase 0 开工时核实，无需用户决策）|
| 🚫 | 已确认为"不做" |

---

## 类别 A：OIDC 基座 / 密钥管理（阻塞 Phase 1）

### Q-01：`security_secrets` 表是否已存在？
- **来源**：api-oauth 调研 uncertainties；`03` §7，§10
- **问题**：`backend/migrations/` 中是否有建表 `security_secrets`（用于加密存储 RS256 私钥）的 migration？若已存在，Phase 1 可直接复用该表存放 OIDC 私钥，无需建表。
- **确认方式**：代码核查——`grep -r "security_secrets" /Volumes/APFS_HD/Documents/Github/sub2api/backend/migrations/` 和 `backend/ent/schema/`
- **阻塞任务**：`03` Phase 1 任务 G3（RS256 密钥对生成 + 加密存储）
- **优先级**：P0（Phase 1 开始前必须确认）
- **状态**：✅ RESOLVED — **`security_secrets` 表已存在**（`backend/migrations/053_add_security_secrets.sql:2`，schema = `key VARCHAR(100) UNIQUE / value TEXT / created_at / updated_at`，首行注释"存储系统级密钥（如 JWT 签名密钥、TOTP 加密密钥）"，且已有 ent 模型 `ent/securitysecret*`），仅缺非对称签名密钥；现有 `JWT_SECRET` 仅用于 HS256 session token。RS256（主）+ ES256（备）私钥**复用该表**（新增 key 行如 `oidc_signing_key_rs256_<kid>` / `oidc_signing_key_es256_<kid>`，value 存私钥、加密 at-rest，参考现有 TOTP 加密密钥模式）+ kid 轮换。**结论：G3 复用 `security_secrets` 表、加密 at-rest、无需新建表。**（代码核实于 2026-06-03）

### Q-02：`oauth_scope_enforcement_enabled=false` 是否有意为之？开启后已知兼容问题？
- **来源**：api-oauth uncertainties；`03` §12 R3；`91` R-PROD-02
- **问题**：migration 145 seed 将该开关默认置为 `false`。这是上线前的临时状态（等待所有 RP 完成 scope 配置后再开启），还是有意的运营决策？开启后 Sakrylle Image 现有 token 的哪些 scope 会被拒绝？
- **确认方式**：用户决策（与运营/产品确认意图）+ 代码核查（扫描 Image token 的 scope 与中间件校验逻辑）
- **阻塞任务**：Phase 3 scope enforcement 开启；`91` R-PROD-02 缓解
- **优先级**：P0（持续存在的安全状态，需有意识决策）
- **状态**：✅ RESOLVED — 代码核实 `oauth_scope_enforcement_enabled` 默认 `false`（`migrations/145_oauth_v2.sql:348`）。**决策：分阶段开启**——先让所有 RP 完成 scope 配置（Phase 2/3），再在 Phase 4 有意识切 `true`（需额外审批，先全量验各 RP scope 覆盖，见 `93` A11）。当前默认 false 为有意的过渡状态，非疏漏。（代码核实于 2026-06-03）

### Q-03：生产 `settings.oauth_issuer` 当前值是否为 `https://sub.sakrylle.com`？
- **来源**：`03` §6，Phase 0 任务；`90` §3.2
- **问题**：migration 148 seed 写入了 `oauth_issuer = https://sub.sakrylle.com`，但生产 DB 可能经手动覆盖或旧 seed。需只读核对生产值与文档决策一致。
- **确认方式**：生产只读查询（**勿改**）——通过 `ssh ssh-tokyo 'docker exec sub2api-postgres psql -U sub2api sub2api -c "SELECT value FROM settings WHERE key='\''oauth_issuer'\'';"'`
- **阻塞任务**：Phase 1 发布 `/.well-known/openid-configuration`（issuer 字段必须与生产 DB 值一致）
- **优先级**：P0
- **状态**：✅ RESOLVED — issuer 决策冻结为 `https://sub.sakrylle.com`（用户拍板）。生产值与 148 seed 一致性仍需 Phase 1.A 开工前做一次只读核对（`ssh ssh-tokyo` SELECT，**勿改**），但目标值已确定，不再是开放决策。（确认于 2026-06-03）

### Q-04：`oauth_v2_ui_enabled` 开关控制哪些前端 UI？是否影响 OIDC consent 流程？
- **来源**：api-oauth uncertainties；`03` §14
- **问题**：`settings.oauth_v2_ui_enabled` 的前端消费逻辑在哪些 Vue 组件中？若关闭，OIDC 新流程（device flow 确认页、授权 app 列表页等）是否受影响？
- **确认方式**：代码核查——`grep -r "oauth_v2_ui_enabled" /Volumes/APFS_HD/Documents/Github/sub2api/frontend/`
- **阻塞任务**：Phase 2 consent 页品牌化
- **优先级**：P1
- **状态**：🔧 CODE-CHECK — 留待 Phase 2 consent 页品牌化开工时 grep 核实，无需用户决策。

### Q-05：`sakrylle-cli` Device Flow 是否已有生产用户在用？
- **来源**：api-oauth uncertainties；`03` §9，§14
- **问题**：migration 148 seed 注册了 `sakrylle-cli` client（含 device flow），是否有真实用户通过 Device Flow 登录？若有，Phase 2 CLI Device Flow 变更需格外谨慎，避免现有用户 token 失效。
- **确认方式**：生产只读查询——`SELECT COUNT(*) FROM oauth_device_codes WHERE client_id='sakrylle-cli'`；`SELECT COUNT(*) FROM oauth_access_tokens WHERE client_id='sakrylle-cli'`
- **阻塞任务**：Phase 2 CLI OIDC Device Flow 接入的灰度策略
- **优先级**：P1
- **状态**：🔧 CODE-CHECK — 留待 CLI Phase 2 开工时做生产只读计数（`oauth_device_codes`/`oauth_access_tokens` WHERE client_id='sakrylle-cli'），决定灰度策略，无需用户决策。

---

## 类别 B：Sakrylle API 架构（影响 Phase 1-3 实现决策）

### Q-06：replay 防护生产路径——`mintRepo` 是否已正确 wire？
- **来源**：api-oauth risks；`03` §12 R4
- **问题**：`oauth_provider_service.go:1696` 存在 legacy 非原子 fake 路径，测试可能走该路径；生产路径是否已经通过 `mintRepo`（`FOR UPDATE` 原子 refresh token rotation + `rotated_to_hash` replay 检测）正确 wire？
- **确认方式**：代码核查——读取 `oauth_provider_service.go` 中 `mintTokensFromCode` 和 `RefreshAccessToken` 的依赖注入路径，确认 production wiring
- **阻塞任务**：Phase 1 安全性确认（不能在 replay 防护有漏洞的情况下签发 id_token）
- **优先级**：P0
- **状态**：🔧 CODE-CHECK — 留待 Phase 1.A 开工时精读 `oauth_provider_service.go` 的 `mintTokensFromCode`/`RefreshAccessToken` 依赖注入路径，确认生产走 `mintRepo`（原子）而非 legacy fake 路径，无需用户决策。

### Q-07：`billingCacheService.GetUserBalance` 与 `APIKeyAuthSnapshot.User.Balance` 哪个优先？
- **来源**：api-commercial uncertainties
- **问题**：`api_key_auth.go:211` 的 `balance <= 0` 检查读的是 `apiKey.User.Balance`（snapshot 值），还是有额外读 `billingCacheService.GetUserBalance`（Redis 实时值）？两路是否合并？
- **确认方式**：代码核查——精读 `backend/internal/server/middleware/api_key_auth.go:190-230` 的余额检查逻辑
- **阻塞任务**：`91` R-TOKEN-01 缓解方案确定
- **优先级**：P2
- **状态**：🔧 CODE-CHECK — 留待实现期精读 `api_key_auth.go:190-230` 余额检查逻辑，无需用户决策。

### Q-08：`user_group_rate_multipliers` 表完整 schema 是什么？
- **来源**：api-commercial uncertainties；`04-oauth-oidc-commercial-capabilities.md`
- **问题**：`user_group_rate_multipliers` 不在 ent 管理范围（无 `backend/ent/schema/` 对应文件），只能从 SQL 推断。字段是否为 `user_id, group_id, multiplier`？是否有 `expires_at` 或有效期字段？
- **确认方式**：代码核查——`grep -r "user_group_rate_multipliers" /Volumes/APFS_HD/Documents/Github/sub2api/backend/migrations/`
- **阻塞任务**：OIDC claims 设计（确认不需要放入 claims）；Phase 3 per-user rate 相关展示功能
- **优先级**：P2
- **状态**：🔧 CODE-CHECK — 留待实现期 grep migrations 推断完整 schema，无需用户决策。

---

## 类别 C：Sakrylle CLI fork 实施（影响 Phase 2 CLI）

### Q-09：codex 所有子目录是否真随 `CODEX_HOME`？是否有残留硬编码？
- **来源**：isolation uncertainties；`05` §13
- **问题**：`rg` 扫描输出中出现 `n_home`、`confign_home` 等疑似被遮蔽的变量名。`thread-store`、`connectors` 等模块是否有 `~/.codex` 残留硬编码，不受 `CODEX_HOME` 控制？
- **确认方式**：代码核查——`grep -rn '\.codex' /Volumes/APFS_HD/Documents/Github/codex/codex-rs/ --include="*.rs"` 确认所有 `.codex` 字符串出现位置
- **阻塞任务**：`05` Phase 1 CLI 隔离任务（确保改 `find_codex_home()` 后没有残留硬编码漏改）；`91` R-CONF-01 缓解确认
- **优先级**：P0（CLI 隔离的完整性基础）
- **状态**：🔧 CODE-CHECK — 留待 CLI Phase 0/1 开工时全量 grep `\.codex`/`find_codex_home`，无需用户决策。

### Q-10：codex app-server `sqlite_home` 是否独立于 `CODEX_HOME`？
- **来源**：isolation uncertainties；`05` §13
- **问题**：`state/src/lib.rs:79` 的 `CODEX_SQLITE_HOME` 可独立覆盖 SQLite 路径。`app-server/src/config_manager.rs` 的实际配置是否走 `CODEX_HOME` 还是单独路径？若独立，`SAKRYLLE_CLI_HOME` 隔离后 SQLite 文件是否仍可能在 `~/.codex`？
- **确认方式**：代码核查——读取 `codex-rs/app-server/src/config_manager.rs` 中 SQLite 路径构造逻辑
- **阻塞任务**：`05` Phase 1 CLI 隔离（SQLite 路径隔离完整性）
- **优先级**：P1
- **状态**：🔧 CODE-CHECK — 留待 CLI Phase 1 开工时读 `app-server/src/config_manager.rs` SQLite 路径构造，无需用户决策。

### Q-11：Sakrylle CLI 最终二进制文件名是什么？
- **来源**：调研综合（studio research §uncertainties，cli research §recommendations）
- **问题**：Sakrylle CLI 可执行文件名决定：(a) `sakrylle`（品牌一致，但改变用户习惯）；(b) `sakrylle-cli`（避免与其他 `sakrylle` 命令混淆）；(c) 保留 `codex`（兼容用户习惯，但混淆品牌）。此决策影响 Studio 的 `codexBin` 默认值（`app_server.rs:654`）、安装文档、PATH 配置。
- **确认方式**：用户决策
- **阻塞任务**：Studio `codexBin` 默认值配置；CLI 安装脚本；`05` §8 bundle 命名
- **优先级**：P1（Phase 2 CLI 开始前需锁定）
- **状态**：✅ RESOLVED — **主二进制名 = `sakrylle`，并提供短别名 `skl`**（用户拍板）。Studio `codexBin` 默认探测顺序 `sakrylle`→`skl`；daemon 名 `sakrylle-cli-daemon`；bundle/originator 统一 `com.sakrylle.*` / `sakrylle_cli`。（确认于 2026-06-03）

### Q-12：analytics/Sentry/遥测的 opt-out 机制——配置项还是 build-time feature flag？
- **来源**：cli uncertainties；studio research §gaps
- **问题**：codex-rs 中 analytics/sentry 上报到 OpenAI 内部端点，fork 后需关闭。是否有 `analytics.enabled=false` 配置项？还是需要在 `Cargo.toml` 中用 feature flag 移除？Studio 的 Sentry DSN（`src/main.tsx:9`）是否有 `VITE_SENTRY_DSN=''` 禁用机制？
- **确认方式**：代码核查——`grep -rn "sentry\|analytics\|telemetry" /Volumes/APFS_HD/Documents/Github/codex/codex-rs/ --include="*.rs"`；检查 Studio `src/main.tsx:8-10` 的条件逻辑
- **阻塞任务**：Phase 2 品牌改造（隐私合规，用户数据不能发送到第三方）
- **优先级**：P1
- **状态**：✅ RESOLVED — **遥测 / Sentry 全家桶默认关闭（opt-in）**（用户拍板）。fork 后 analytics/sentry/telemetry 一律不外发：CLI 侧默认禁用上报（配置项关 + 必要时 build-time feature flag 去除依赖），Studio `VITE_SENTRY_DSN` 默认空。具体禁用机制（config 项 vs Cargo feature）留实现期核实，但默认态已定。（确认于 2026-06-03）

---

## 类别 D：Sakrylle Studio fork 实施（影响 Phase 2 Studio）

### Q-13：CodexMonitor 是否引入了 `tauri-plugin-store` 第三持久化路径？
- **来源**：isolation uncertainties；`05` §13
- **问题**：若 `tauri-plugin-store` 存在，除 `settings.json`/`workspaces.json`（`state.rs:50`）和 WebView localStorage 外还有第三个持久化路径，bundle id 变更后需额外迁移。
- **确认方式**：代码核查——`grep -rn "tauri-plugin-store\|Store::load\|@tauri-apps/plugin-store" /Volumes/APFS_HD/Documents/Github/CodexMonitor/`
- **阻塞任务**：`05` Phase 2 Studio 配置隔离完整性
- **优先级**：P1
- **状态**：✅ RESOLVED — 2026-06-06 在 CodexMonitor 工作树核实未使用 `tauri-plugin-store` / `Store::load` / `@tauri-apps/plugin-store`。Studio 本地持久化面为 Tauri appData 下的 settings/workspaces 与 WebView localStorage；bundle id 迁移负责 appData 隔离，localStorage 用 `sakrylle-studio.*` 读旧写新迁移。

### Q-14：Tauri iOS/Windows conf 中 identifier 是否也含 `com.dimillian.codexmonitor`？
- **来源**：studio uncertainties
- **问题**：`tauri.ios.conf.json` 和 `tauri.windows.conf.json` 中 identifier 是否也含 `com.dimillian.codexmonitor`？若是，需同步改为 `com.sakrylle.studio`。
- **确认方式**：代码核查——`cat /Volumes/APFS_HD/Documents/Github/CodexMonitor/src-tauri/tauri.ios.conf.json` 和 `tauri.windows.conf.json`（若存在）
- **阻塞任务**：Phase 2 Studio iOS/Windows 构建正确性
- **优先级**：P1
- **状态**：✅ RESOLVED — 2026-06-06 在 CodexMonitor 工作树核实并改造：主 `tauri.conf.json` 为 `com.sakrylle.studio`，iOS 覆盖为 `com.sakrylle.studio.ios`，Windows/Linux 平台 conf 不含独立 identifier 但窗口 title 已改为 Sakrylle Studio。

### Q-15：Studio 的 `codex app-server` JSON-RPC 子协议与 Sakrylle CLI fork 是否完全兼容？
- **来源**：studio uncertainties
- **问题**：Studio 通过 stdin/stdout JSON-RPC 与 CLI 的 `app-server` 子进程通信（`app_server.rs:749`）。Sakrylle CLI fork 修改品牌字符串、配置路径后，`app-server` 的 JSON-RPC 协议是否保持兼容？特别是 `initialize` 请求中的 `client_name`/`client_version` 字段处理。
- **确认方式**：代码核查——读 `codex-rs/app-server/src/` 中 JSON-RPC initialize 处理逻辑；对照 Studio `app_server.rs:749-780`
- **阻塞任务**：Phase 2 Studio 对接 Sakrylle CLI
- **优先级**：P1
- **状态**：🔧 CODE-CHECK — 留待 Studio Phase 0 开工时实测 app-server JSON-RPC 兼容性（首发最高风险项，`93` 0.C P0-4），需 CLI Phase 1 二进制产出，无需用户决策。

---

## 类别 E：Sakrylle Web fork 实施（影响 Phase 2-3 Web）

### Q-16：open-webui authlib 在 `OPENID_PROVIDER_URL` 返回 RFC 8414 格式时是否有 fallback？
- **来源**：web uncertainties
- **问题**：open-webui `config.py:3897` 直接传 `server_metadata_url`，authlib 内部会按顺序尝试多个 well-known URL。`utils/oauth.py:392-401` 是否顺序尝试 `/.well-known/oauth-authorization-server` 和 `/.well-known/openid-configuration`？若是，sub2api 的 RFC 8414 端点无需额外操作即可被 open-webui 发现，大幅降低 Web OIDC 接入工作量。
- **确认方式**：代码核查——精读 `open-webui/backend/open_webui/utils/oauth.py:380-410`；查阅对应 authlib 版本的 discovery 文档
- **阻塞任务**：Phase 2 Web OIDC 接入路径选择（决定是否必须实现 `openid-configuration`）
- **优先级**：P1（可能大幅影响 Web OIDC 工作量）
- **状态**：🔧 CODE-CHECK — 留待 Web Phase 0 开工时精读 `open-webui/.../utils/oauth.py` discovery fallback + 本地实测，无需用户决策。

### Q-17：open-webui OIDC 回调 URL 格式与 sub2api redirect_uri 白名单对齐
- **来源**：web uncertainties
- **问题**：Sakrylle Web 的 OIDC 回调 URL 应为 `https://<web-host>/oauth/oidc/callback`（open-webui 默认约定）还是其他路径？需与 sub2api `oauth_clients.redirect_uris` 精确白名单对齐。域名需 Q-25 先确认。
- **确认方式**：代码核查——读 open-webui OAuth callback 路由定义（`backend/open_webui/routers/auths.py` 或类似文件中 `/oauth/oidc/callback` 路由）。域名已定（Q-25 = `chat.sakrylle.com`），回调 URL 即 `https://chat.sakrylle.com/oauth/oidc/callback`，待实现期核实路径并对齐白名单。
- **阻塞任务**：Phase 2 Web `oauth_clients` 注册（`03` §9 sakrylle-web client）
- **优先级**：P1
- **状态**：🔧 CODE-CHECK — 域名前置（Q-25）已解决；仅余 callback 路径核实，留待 Web Phase 0 开工，无需用户决策。

### Q-18：`ENABLE_OAUTH_PERSISTENT_CONFIG`（`config.py:143` 默认 False）启用后 DB 与 env 优先级？
- **来源**：isolation uncertainties
- **问题**：若该开关启用，OAuth 配置从 DB 读取并覆盖 env 变量，可能导致 `OPENID_PROVIDER_URL` 等设置被旧 DB 值覆盖，难以通过 env 更新配置。生产部署前需确认此开关的行为和默认值。
- **确认方式**：代码核查——读 `open-webui/backend/open_webui/config.py:143` 及其使用处的优先级逻辑
- **阻塞任务**：Phase 2 Web 部署配置方式选型（env 管理 vs DB 管理 OIDC 配置）
- **优先级**：P2
- **状态**：🔧 CODE-CHECK — 留待 Web Phase 0 开工时读 `config.py:143` 优先级逻辑，无需用户决策。

---

## 类别 F：Sakrylle Chat fork 实施（影响 Phase 2-3 Chat）

### Q-19：OAuth PKCE 回调 scheme `sakrylle-chat://oauth/callback` 是否在 migration 148 中已预置？
- **来源**：chat uncertainties
- **问题**：Sakrylle Chat 需新增 PKCE 回调 scheme `sakrylle-chat://oauth/callback`。此 redirect_uri 是否已在 `backend/migrations/148_oauth_v2_sakrylle_seed.sql` 的 sakrylle-chat client 中预置？还是需要新 migration 添加？
- **确认方式**：代码核查——读 `backend/migrations/148_oauth_v2_sakrylle_seed.sql` 的 redirect_uris 字段内容；确认是否有 `sakrylle-chat` client 条目
- **阻塞任务**：Phase 2 Chat OAuth PKCE 实现（redirect_uri 必须预先在 server 端注册）
- **优先级**：P0（Chat OIDC 接入的前置）
- **状态**：🔧 CODE-CHECK — 回调 scheme 决策已定 `sakrylle-chat://oauth/callback`；仅余"是否已在 148 seed 预置（否则需新 migration）"的核实，留待 Chat Phase 0 开工时读 `148_oauth_v2_sakrylle_seed.sql`，无需用户决策。

### Q-20：kelivo iOS App Group 扩展 bundle id 需要同步修改吗？
- **来源**：chat risks；`kelivo/ios/Runner.xcodeproj/project.pbxproj:554`
- **问题**：iOS `GenerationActivityExtension` bundle id 为 `psyche.kelivo.GenerationActivityExtension`，hardcode 了 parent bundle id。改为 `com.sakrylle.chat` 后，扩展和 App Group 标识符需同步修改，否则 iOS 后台生成功能失效。Sakrylle Chat 是否需要此扩展功能？
- **确认方式**：用户决策（是否需要 iOS Live Activity 后台生成功能）；代码核查（确认扩展的依赖关系）
- **阻塞任务**：Phase 2 Chat iOS 构建范围
- **优先级**：P1
- **状态**：🔧 CODE-CHECK — 用户已拍板**保留 iOS Live Activity（灵动岛）**（见 Q-26）。因此改 bundle id 时必须同步修改扩展 bundle id + App Group 标识符；具体同步点留待 Chat iOS 改造期核实 `project.pbxproj`，无需再向用户确认范围。

### Q-21：kelivo Windows `getApplicationSupportDirectory()` 路径隔离是否有效？
- **来源**：isolation uncertainties；`05` §13
- **问题**：Flutter path_provider 在 Windows 上的路径依赖 pubspec `name` 和组织名，bundle id 对 Windows 路径无直接影响。改 bundle id 后 Windows 数据目录是否真正隔离？需检查 `windows/runner/Runner.rc` 和 `linux/CMakeLists.txt`。
- **确认方式**：代码核查——读 `kelivo/windows/runner/Runner.rc` 和 `kelivo/linux/CMakeLists.txt` 的组织名/appId 配置；可选：构建 Windows 版本实测路径
- **阻塞任务**：`05` Phase 0 验证任务（Windows 隔离完整性）
- **优先级**：P2
- **状态**：🔧 CODE-CHECK — 留待 Chat Phase 0 开工时读 `windows/runner/Runner.rc` + `linux/CMakeLists.txt`，无需用户决策。

---

## 类别 G：Sakrylle Image 升级（影响 Phase 1 生产 RP 验证）

### Q-22：sub2api `/oauth/token` 当前是否已返回 `id_token` 字段？
- **来源**：image uncertainties；`50-sakrylle-image-research.md`
- **问题**：`OAUTH_V2_INTEGRATION.md` §3.3 的响应示例中无 `id_token`，不确定服务端是否已实现。若已实现，Image OIDC 升级只需客户端加解析代码；若未实现，需等待 OIDC 基座 Phase 1 完成。
- **确认方式**：代码核查——读 `backend/internal/service/oauth_provider_service.go` 中 `mintTokensFromCode` 的 token 响应结构体，确认是否有 `IDToken` 字段
- **阻塞任务**：Phase 1 Sakrylle Image OIDC 升级排期（决定是否可以立即客户端改造还是必须等服务端）
- **优先级**：P0
- **状态**：✅ RESOLVED — 2026-06-03 核实：当时服务端 `/oauth/token` 不签发 `id_token`（`mintTokensFromCode` token 响应结构体无 `IDToken` 字段），结论需新增 id_token 签发（Phase 1 G4）。**后续更新（2026-06-04）：G4 已实现——服务端现在在授权 scope 含 `openid` 时返回可验签 id_token（RS256/ES256 按 client `signing_algorithm` 选择）。Image OIDC 升级不再被服务端阻塞，缺口仅在客户端侧。**

### Q-23：生产 Docker Compose 中 `image.sakrylle.com` 服务是否有 `VITE_SAKRYLLE_OAUTH_BASE`/`VITE_SAKRYLLE_OAUTH_CLIENT_ID` env 注入？
- **来源**：image uncertainties
- **问题**：这两个变量目前硬编码在 bundle 内（无 `inject-api-url.sh` 占位符支持），若生产未注入则靠 fallback 硬编码值（`https://sub.sakrylle.com`/`sakrylle-image-playground`）。需确认是否需要补充占位符机制，以便运行时覆盖 client_id 和 OAuth base URL。
- **确认方式**：生产只读查询——`ssh ssh-tokyo 'grep -A 30 "image\|gpt_image\|image-playground" /opt/stack/docker-compose.yml'`；代码核查（`gpt_image_playground/deploy/inject-api-url.sh` 是否已有 OAUTH_BASE 替换逻辑）
- **阻塞任务**：Phase 1 Image OIDC 升级（若需改 client_id，必须先有注入机制）
- **优先级**：P1
- **状态**：🔧 CODE-CHECK — client_id 决策已定（沿用 `sakrylle-image-playground`，不改），故注入机制非阻塞；占位符是否需补留待 Image Phase 0 开工时只读核实生产 compose env + `inject-api-url.sh`，无需用户决策。

### Q-24：`/v1/me` 服务端是否返回 `sub` 字段（OIDC 标准主键）？
- **来源**：image uncertainties
- **问题**：`SakrylleMePayload`（`gpt_image_playground/src/lib/sakrylleAccount.ts:38-53`）只有 `user_id/username`，无 `sub`。`/v1/me` 服务端响应（`backend/internal/handler/oauth_provider_account_handler.go:161-221`）是否有 `sub` 字段？OIDC UserInfo 标准要求 `sub` 是必填字段。
- **确认方式**：代码核查——读 `backend/internal/handler/oauth_provider_account_handler.go:161-221` 的响应 JSON 结构；确认是否有 `sub` key
- **阻塞任务**：Phase 3 UserInfo 合规（G7）；Phase 1 Image 前端 claim 解析
- **优先级**：P1
- **状态**：✅ RESOLVED — 2026-06-03 核实：当时 `/v1/me` 无 `sub` 字段（返回 `user_id`/`credit_remaining`/`group_id`），结论需加 `sub` claim（Phase 3 G7）。**后续更新（2026-06-04）：G7 已实现——`/v1/me` 现在在授权 scope 含 `openid` 时返回顶层 OIDC `sub`（=`user.ID` 字符串）；商业状态留在 scoped account/group 块，绝不作顶层 OIDC claim。**

---

## 类别 H：产品战略决策（需用户确认）

### Q-25：Sakrylle Web 部署域名是什么？
- **来源**：web research；`90` M4
- **问题**：open-webui fork 计划部署在哪个域名（`chat.sakrylle.com` 或其他）？域名决定 OIDC redirect_uri、Nginx conf、Cloudflare DNS 记录、`oauth_clients.redirect_uris` 白名单。
- **确认方式**：用户决策
- **阻塞任务**：Phase 2 Web `oauth_clients` 注册；Nginx 配置；Cloudflare DNS
- **优先级**：P1（Phase 2 Web 开始前需锁定）
- **状态**：✅ RESOLVED — **Sakrylle Web 域名 = `chat.sakrylle.com`**（用户拍板）。redirect_uri = `https://chat.sakrylle.com/oauth/oidc/callback`（路径待 Q-17 实现期核实）；Nginx conf + Cloudflare A 记录 → `64.83.47.108` 按此域名配置。（确认于 2026-06-03）

### Q-26：Sakrylle Chat 是否需要 iOS Live Activity（`GenerationActivityExtension`）功能？
- **来源**：Q-20 前置问题
- **问题**：若需要，改 bundle id 时必须同步修改扩展 bundle id 和 App Group 标识符，工作量增加；若不需要，可删除扩展简化改造。
- **确认方式**：用户决策（产品功能优先级）
- **阻塞任务**：Phase 2 Chat iOS 改造范围（与 Q-20 关联）
- **优先级**：P2
- **状态**：✅ RESOLVED — **保留 iOS Live Activity（灵动岛）功能**（用户拍板）。因此 `GenerationActivityExtension` 扩展不删除，改 bundle id 时须同步修改扩展 bundle id + App Group 标识符（实现细节见 Q-20）。（确认于 2026-06-03）

### Q-27：是否引入 org/team/workspace 多租户？
- **来源**：api-commercial gaps
- **问题**：sub2api 当前仅有 user-group 粒度的扁平结构，无父子账户/组织层级。Sakrylle 生态是否计划为企业用户提供 org/team 多租户（类似 OpenAI Organization header）？若是，需在 OIDC claims 中设计 `org_id`/`team_id` 字段，并在 `api_keys` 表增加 org 维度。
- **确认方式**：用户决策（产品规划）
- **阻塞任务**：OIDC claims 设计最终版（`04` 文档）；`api_keys` 表扩展
- **优先级**：P2（Phase 3 前决策即可，当前 YAGNI）
- **状态**：✅ RESOLVED — **不引入 org/team/workspace 多租户**（用户拍板）。维持现有 user-group 扁平结构；OIDC claims 不设计 `org_id`/`team_id`，`api_keys` 表不加 org 维度。后续若有企业需求再单独立项。（确认于 2026-06-03）

### Q-28：是否引入 ES256（P-256）作为第二签名算法？
- **来源**：`03` §7，§14
- **问题**：RS256（RSA-2048）互操作性最广，ES256（P-256）密钥更短、性能更好。是否需要在 OIDC discovery 同时列出两者？还是仅 RS256 首发，后续按需扩展？
- **确认方式**：用户决策（当前判断 YAGNI，除非有 RP 明确需求）
- **阻塞任务**：Phase 1 密钥基座设计（需在密钥服务接口预留扩展点）
- **优先级**：P2
- **状态**：✅ RESOLVED — **引入 ES256（P-256）作为第二签名算法，RS256 为主**（用户拍板）。OIDC discovery `id_token_signing_alg_values_supported` 同时列出 `RS256`（主）+ `ES256`；密钥服务（G3）从一开始即按多算法 keystore 设计，两套密钥对各带 kid。（确认于 2026-06-03）

### Q-29：`email:read` scope 是否已对第一方 client（Image、CLI 等）默认授予？
- **来源**：image uncertainties；`OAUTH_V2_INTEGRATION.md` §6
- **问题**：文档标注 `email:read` "not granted to first-party clients by default"。若 Image 升级到 `scope=openid email`，服务端是否会返回 email claim？需确认 scope 中间件对第一方 client 的处理逻辑。
- **确认方式**：代码核查——读 `backend/internal/service/oauth_scopes.go` 的 first-party client 逻辑；读 `backend/migrations/144_oauth_seed_sakrylle.sql` 和 `148_oauth_v2_sakrylle_seed.sql` 中 `allowed_scopes` 字段
- **阻塞任务**：Phase 1 id_token email claim 实现；Phase 2 Web/Chat OIDC 接入
- **优先级**：P1
- **状态**：✅ RESOLVED — **`email:read` 对第一方 client（Image/CLI/Studio/Web/Chat）默认授予**（用户拍板）。故第一方 client 请求 `scope=openid email` 时服务端返回 email claim。需在 client seed（144/148）的 `allowed_scopes` 落实并校验中间件放行逻辑（实现期）。（确认于 2026-06-03）

### Q-30：Sakrylle Chat 是否需要 Web 平台（Flutter Web）发布？
- **来源**：调研综合（kelivo 支持 6 端：Android/iOS/macOS/Win/Linux/Web）
- **问题**：Flutter Web 平台的 bundle id 概念不同（无 bundle id），OAuth PKCE 回调方案也不同（需 Web redirect 而非自定义 scheme）。若需要 Web 端，回调 scheme 需要单独设计（类似 `sakrylle-chat://` 仅适用于原生端）。
- **确认方式**：用户决策（产品功能优先级）
- **阻塞任务**：Phase 2 Chat OAuth PKCE 实现（Web 端回调方案）
- **优先级**：P2
- **状态**：✅ RESOLVED — **Sakrylle Chat 不发布 Flutter Web 平台**（用户拍板）。Chat 仅原生端（Android/iOS/macOS/Win/Linux），OAuth PKCE 统一走自定义 scheme `sakrylle-chat://oauth/callback`（桌面 loopback），无需单独设计 Web redirect 回调方案。（确认于 2026-06-03）

---

## 问题数量统计

> 全部 30 问已于 2026-06-03 完成首轮分诊：13 个 ✅ RESOLVED（用户拍板或代码核实），17 个 🔧 CODE-CHECK（留待各产品实现期 Phase 0 自查，无开放决策）。**无 ❓ 待确认项。**

### 按状态分布

| 状态 | 数量 | 编号 |
|---|---|---|
| ✅ RESOLVED（用户拍板） | 9 | Q-11、Q-12、Q-25、Q-26、Q-27、Q-28、Q-29、Q-30、Q-03（issuer 决策） |
| ✅ RESOLVED（代码核实） | 4 | Q-01、Q-02、Q-22、Q-24 |
| 🔧 CODE-CHECK（实现期自查） | 17 | Q-04、Q-05、Q-06、Q-07、Q-08、Q-09、Q-10、Q-13、Q-14、Q-15、Q-16、Q-17、Q-18、Q-19、Q-20、Q-21、Q-23 |
| ❓ 待确认 | 0 | — |
| **合计** | **30** | |

### 按类别分布（原 P0/P1/P2 优先级）

| 类别 | 问题数 | ✅ RESOLVED | 🔧 CODE-CHECK |
|---|---|---|---|
| A：OIDC 基座 / 密钥管理 | 5 | 3（Q-01/02/03）| 2（Q-04/05）|
| B：Sakrylle API 架构 | 3 | 0 | 3（Q-06/07/08）|
| C：Sakrylle CLI fork | 4 | 2（Q-11/12）| 2（Q-09/10）|
| D：Sakrylle Studio fork | 3 | 0 | 3（Q-13/14/15）|
| E：Sakrylle Web fork | 3 | 0 | 3（Q-16/17/18）|
| F：Sakrylle Chat fork | 3 | 0 | 3（Q-19/20/21）|
| G：Sakrylle Image 升级 | 3 | 2（Q-22/24）| 1（Q-23）|
| H：产品战略决策 | 6 | 6（Q-25~30）| 0 |
| **合计** | **30** | **13** | **17** |

### 用户拍板的关键决策（9 项，已冻结）

- **Q-03** issuer = `https://sub.sakrylle.com`
- **Q-11** CLI 二进制 = `sakrylle`（+ 短别名 `skl`）
- **Q-12** 遥测 / Sentry 全家桶默认关闭（opt-in）
- **Q-25** Sakrylle Web 域名 = `chat.sakrylle.com`
- **Q-26** Chat 保留 iOS Live Activity（灵动岛）
- **Q-27** 不引入 org/team/workspace 多租户
- **Q-28** 第二签名算法 = ES256（RS256 为主）
- **Q-29** `email:read` 对第一方 client 默认授予
- **Q-30** Chat 不发布 Flutter Web

### 代码核实结论（4 项，决定 Phase 1 实现路径）

- **Q-01** `security_secrets` 表已存在（migration 053:2）、仅无非对称签名密钥 → RS256+ES256 私钥复用该表、加密 at-rest、无需建表（G3，已确认 2026-06-03）
- **Q-02** `oauth_scope_enforcement_enabled` 默认 false（有意过渡态）→ 开启需生产审批（Phase 4 A11）
- **Q-22** id_token 签发（G4）→ ✅ 已实现（2026-06-04，含 ES256 per-client 签发）
- **Q-24** `/v1/me` 返回 `sub`（G7）→ ✅ 已实现（2026-06-04）

### 原 7 个 P0 问题的当前归属

原"必须在 Phase 1 开始前解决"的 7 个 P0 均已落定：Q-01/Q-02/Q-03/Q-22 已 ✅ RESOLVED（含一次 Phase 1.A 开工前的 issuer 只读核对）；Q-06/Q-09/Q-19 转 🔧 CODE-CHECK，由各产品 Phase 0 实现期自查，不再阻塞决策。
