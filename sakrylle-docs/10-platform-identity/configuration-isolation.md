---
title: 05 · Sakrylle 生态配置隔离规范
status: canonical
scope: platform
last_verified: 2026-06-06
---

# 05 · Sakrylle 生态配置隔离规范

> 规划文档（planning only）。Sakrylle API / Sakrylle Image **已上线生产**。本文约束 6 个客户端 fork 的本地配置/数据落盘，**目标：与上游软件在同一台机器上零冲突并行运行**。任何触及生产服务端的改动标注「需额外审批」。
> 兄弟文档：见 `03-sakrylle-api-oidc-architecture.md`（IdP 端点）、`04-oauth-oidc-commercial-capabilities.md`（认证选型）。

---

## 1. 调研范围

对 5 个上游仓库（codex / CodexMonitor / open-webui / kelivo / gpt_image_playground）+ Sakrylle API（服务端）完成只读路径扫描，定义：(a) `~/.sakrylle/`（通用）与 `~/.sakrylle-cli/`（CLI 专用）目录约定 + 各平台映射；(b) `SAKRYLLE_*` 环境变量全集；(c) 13 项隔离面；(d) 每个生态软件"与上游零冲突"对照表；(e) `com.sakrylle.*` bundle/app 标识命名。

## 2. 关键结论

1. **codex 是单变量隔离**：所有路径集中在 `CODEX_HOME`（默认 `~/.codex`，`utils/home-dir/src/lib.rs:13`）—— fork 改 `find_codex_home()` 一处即可整体迁移。
2. **CodexMonitor 由 `APP_IDENTIFIER` 驱动三平台路径**（`com.dimillian.codexmonitor`，`daemonctl.rs:30`）—— 改 bundle id 即路径隔离，但 localStorage key 前缀混乱需统一。
3. **open-webui 由 `DATA_DIR` 驱动全部落盘**（`env.py:216`）—— 挂独立 DATA_DIR 即隔离，但 `WEBUI_NAME` 硬编码 + 自动追加 `(Open WebUI)` 后缀（`env.py:772-773`）需处理。
4. **kelivo 由 bundle id / `applicationId` 驱动**（`com.psyche.kelivo`）—— 改四处标识即路径隔离；API key 明文存 SharedPreferences 需安全加固。
5. **gpt_image_playground 已基本品牌化**（localStorage/IndexedDB 全用 `sakrylle-image-playground` 前缀）—— 仅补 vite-env 声明。
6. **最高风险是 CLI 与上游 codex 争 `~/.codex`**（auth.json/sessions/socket）—— **必须**用 `SAKRYLLE_CLI_HOME` 严格隔离，绝不复用 `CODEX_*`。

## 3. 相关文件路径（path:line，引用自调研）

| 软件 | 隔离入口 | 路径:行 |
|---|---|---|
| codex | `find_codex_home()`（所有路径起点） | `codex-rs/utils/home-dir/src/lib.rs:13`（硬编码 `.codex` `:59`） |
| codex | API key 环境变量常量 | `codex-rs/login/src/auth/manager.rs:467-468`（`OPENAI_API_KEY` / `CODEX_API_KEY`） |
| codex | auth.json 路径 | `codex-rs/login/src/auth/storage.rs:85` |
| codex | MCP OAuth fallback | `codex-rs/rmcp-client/src/oauth.rs:371`（`.credentials.json`） |
| codex | socket 路径 | `codex-rs/app-server-transport/src/transport/mod.rs:46-54` |
| codex | PID/lock | `codex-rs/app-server-daemon/src/lib.rs:30-32` |
| codex | sessions 子目录 | `codex-rs/rollout/src/lib.rs:24-25` |
| codex | memories | `codex-rs/memories/read/src/lib.rs:14` |
| codex | 系统配置 | `codex-rs/config/src/loader/mod.rs:52`（`/etc/codex/config.toml`） |
| CodexMonitor | `APP_IDENTIFIER` / 三平台目录 | `src-tauri/src/bin/codex_monitor_daemonctl.rs:30` / `:250-288` |
| CodexMonitor | 默认监听端口 4732 | `daemonctl.rs:28` |
| CodexMonitor | tauri identifier/productName | `src-tauri/tauri.conf.json:4-5` |
| CodexMonitor | localStorage `codexmonitor.*` 前缀 | `src/features/threads/utils/threadStorage.ts:3-7` |
| CodexMonitor | settings.json 路径 | `src-tauri/src/state.rs:50-53` |
| open-webui | `DATA_DIR` | `backend/open_webui/env.py:216` |
| open-webui | `DATABASE_URL` | `env.py:261` |
| open-webui | `WEBUI_SECRET_KEY` | `env.py:612-616` |
| open-webui | `WEBUI_NAME` / 追加后缀 / favicon | `env.py:771` / `:772-773` / `:775` |
| open-webui | OIDC 配置 | `config.py:3591-3636` |
| kelivo | Android namespace/appId | `android/app/build.gradle.kts:12,27`（`com.psyche.kelivo`） |
| kelivo | macOS bundle id | `macos/Runner/Configs/AppInfo.xcconfig:11` |
| kelivo | iOS bundle id | `ios/Runner.xcodeproj/project.pbxproj:689`（`psyche.kelivo`） |
| kelivo | 数据目录入口 | `lib/utils/app_directories.dart:17-28` |
| kelivo | SharedPreferences key（无前缀） | `lib/core/providers/settings_provider.dart:44-101` |
| image | OAuth/PKCE/存储 key | `src/lib/sakrylleAuth.ts:10-20` |
| image | IndexedDB 名 | `src/lib/db.ts:3-4`（`sakrylle-image-playground`） |
| image | VITE_ 声明 | `src/vite-env.d.ts:7-12` |

## 4. 目录约定

### 4.1 两个根
- **`~/.sakrylle/`** —— 生态**通用**根（跨工具共享：OIDC 凭据缓存、通用配置、遥测开关）。
- **`~/.sakrylle-cli/`** —— **CLI 专用**根（codex fork，等价上游 `~/.codex`，**绝不**与 `~/.codex` 重叠）。

### 4.2 各平台映射（遵循平台规范，非一味用 `~/.`）

| 隔离面 | macOS | Linux（XDG） | Windows |
|---|---|---|---|
| 配置 | `~/Library/Application Support/com.sakrylle.<app>/` | `${XDG_CONFIG_HOME:-~/.config}/sakrylle/<app>/` | `%APPDATA%\Sakrylle\<app>\` |
| 缓存 | `~/Library/Caches/com.sakrylle.<app>/` | `${XDG_CACHE_HOME:-~/.cache}/sakrylle/<app>/` | `%LOCALAPPDATA%\Sakrylle\<app>\Cache\` |
| 数据 | `~/Library/Application Support/com.sakrylle.<app>/` | `${XDG_DATA_HOME:-~/.local/share}/sakrylle/<app>/` | `%LOCALAPPDATA%\Sakrylle\<app>\` |
| 日志 | `~/Library/Logs/com.sakrylle.<app>/` | `${XDG_STATE_HOME:-~/.local/state}/sakrylle/<app>/log/` | `%LOCALAPPDATA%\Sakrylle\<app>\Logs\` |

> **CLI 例外**：codex 上游把 config/data/cache/log 全混在单一 `CODEX_HOME`，**不遵循 XDG 分离**（`lib.rs:13` 调研确认）。fork 最小改动 = `~/.sakrylle-cli/` 单根承载全部子目录（与上游对称、迁移成本最低）；**可选增强**（按 XDG 分离各子目录）列为后续任务，非首发必需。

## 5. SAKRYLLE_* 环境变量全集

> 设计原则：**新增 `SAKRYLLE_*` 变量，同时保留上游原变量作 fallback**（确保零冲突并行 + 平滑迁移）。优先级：`SAKRYLLE_*` > 上游变量 > 平台默认。

| 变量 | 含义 | 对应上游 | 默认 |
|---|---|---|---|
| `SAKRYLLE_API_BASE_URL` | 网关 `/v1/*` 基址 | `OPENAI_API_BASE_URL` / `VITE_DEFAULT_API_URL` | `https://api.sakrylle.com/v1` |
| `SAKRYLLE_AUTH_BASE_URL` | OAuth/OIDC 授权页基址 | `VITE_SAKRYLLE_OAUTH_BASE` | `https://sub.sakrylle.com` |
| `SAKRYLLE_OIDC_ISSUER` | OIDC issuer（见 `03` §6） | — | `https://sub.sakrylle.com` |
| `SAKRYLLE_CLIENT_ID` | OAuth client_id | `VITE_SAKRYLLE_OAUTH_CLIENT_ID` | 按客户端（见 `03` §9） |
| `SAKRYLLE_CONFIG_HOME` | 配置根 | `CODEX_HOME`(部分) | 平台默认（§4.2） |
| `SAKRYLLE_CACHE_HOME` | 缓存根 | — | 平台默认 |
| `SAKRYLLE_DATA_HOME` | 数据根 | `DATA_DIR` | 平台默认 |
| `SAKRYLLE_LOG_HOME` | 日志根 | codex `log_dir` | 平台默认 |
| `SAKRYLLE_CLI_HOME` | **CLI 专用根（最高优先于 CODEX_HOME）** | `CODEX_HOME` | `~/.sakrylle-cli` |
| `SAKRYLLE_API_KEY` | API key 注入 | `CODEX_API_KEY` / `OPENAI_API_KEY` | 无 |
| `SAKRYLLE_WEBUI_SECRET_KEY` | Web JWT 密钥 | `WEBUI_SECRET_KEY` | 无（必须设） |
| `SAKRYLLE_TELEMETRY_DISABLED` | 遥测开关（遥测默认关闭，已确认 2026-06-03） | — | `true` |

> **image（SPA）补声明**：`VITE_SAKRYLLE_OAUTH_BASE`、`VITE_SAKRYLLE_OAUTH_CLIENT_ID` 在 `sakrylleAuth.ts:10-11` 已用但**未在 `vite-env.d.ts` 声明**（调研 gap）—— 必须补声明，否则 `.env` 缺失时静默 fallback 到硬编码值，配置错误难发现。

## 6. 13 项隔离面规范

| # | 隔离面 | 规范 |
|---|---|---|
| 1 | 配置 | 各 app 独立 §4.2 配置目录；CLI 用 `SAKRYLLE_CLI_HOME` |
| 2 | 缓存 | 独立缓存目录；CodexMonitor 上游无独立缓存（appData 兼做），fork 可分离 |
| 3 | 日志 | 平台日志目录（§4.2） |
| 4 | 数据 | open-webui 独立 `SAKRYLLE_DATA_HOME`；kelivo/CodexMonitor 由 bundle id 驱动 |
| 5 | 凭据 | OS keyring 优先；kelivo 当前明文存 SharedPreferences → **迁 FlutterSecureStorage** |
| 6 | token | OAuth token 存各 app 隔离区；image 已用 `sakrylle-image-playground.auth` localStorage |
| 7 | API Key | `SAKRYLLE_API_KEY`（与 `CODEX_API_KEY` 共存，不复用） |
| 8 | env 变量 | `SAKRYLLE_*` 前缀（§5），上游变量保留为 fallback |
| 9 | endpoint | `SAKRYLLE_API_BASE_URL` / `SAKRYLLE_AUTH_BASE_URL`，默认 sakrylle 域 |
| 10 | telemetry | `SAKRYLLE_TELEMETRY_DISABLED`（**默认关闭**，已确认 2026-06-03）；GitHub release URL 指向 fork 仓库 |
| 11 | lock/pid/socket | codex `app-server-control.sock`/`*.pid`/`daemon.lock` 随 `SAKRYLLE_CLI_HOME` 迁移；CodexMonitor daemon 端口 **4732 → 4733**（避端口冲突） |
| 12 | CLI history | 随 `SAKRYLLE_CLI_HOME`（sessions/archived_sessions/memories/shell_snapshots） |
| 13 | 桌面本地存储 | CodexMonitor localStorage `codexmonitor.*` → `sakrylle-studio.*`（兼容读 `sakrylle-monitor.*` 历史冲突前缀，需一次性迁移）；image 已 `sakrylle-image-playground.*` 无需改 |

> 2026-06-06 更新：CodexMonitor 工作树已实现 Studio 侧 `com.sakrylle.studio`、`SAKRYLLE_CLI_HOME` / `~/.sakrylle-cli`、`sakrylle-studio.*` localStorage、`sakrylle-cli-daemon` / 4733、Sentry 默认关闭和上游 updater trust chain 移除；仍需真实 CLI 冒烟与 Sakrylle updater 签名 key。

## 7. 各软件"与上游零冲突"对照表

| 软件（fork） | 上游标识 | 必须改 | 绝不碰 | 风险 |
|---|---|---|---|---|
| **Sakrylle CLI**（codex） | `CODEX_HOME=~/.codex`、`CODEX_*` env | `find_codex_home()` 先读 `SAKRYLLE_CLI_HOME` 默认 `~/.sakrylle-cli`；系统配置 `/etc/codex` → `/etc/sakrylle`；新增 `SAKRYLLE_API_KEY` | **绝不复用 `~/.codex` 与 `CODEX_*`** | 同机跑上游 codex 会争 auth.json/sessions/socket → 必须严格隔离 |
| **Sakrylle Studio**（CodexMonitor） | `com.dimillian.codexmonitor`、端口 4732 | `tauri.conf.json` identifier→`com.sakrylle.studio`、productName→`Sakrylle Studio`；`APP_IDENTIFIER` 常量；daemon 端口→4733；GitHub release URL→fork；localStorage `codexmonitor.*`→`sakrylle-studio.*`（兼容读 `sakrylle-monitor.*`） | — | settings.json 路径变 → 旧配置（含 remoteBackendToken）不自动迁移；localStorage 只做读旧写新迁移 |
| **Sakrylle Web**（open-webui） | `WEBUI_NAME=Open WebUI`、favicon | `WEBUI_NAME` 默认已改为 `Sakrylle Web`；上游 `(Open WebUI)` 后缀追加逻辑已移除；`WEBUI_FAVICON_URL`；独立 `DATA_DIR`（=`SAKRYLLE_DATA_HOME`）；静态品牌资源/manifest 由 Web 本地仓库维护 | 保留 `WEBUI_*` 作 fallback ≥1 版本 | 若直接重命名 `WEBUI_SECRET_KEY` 而不留 fallback → JWT 密钥缺失服务起不来 |
| **Sakrylle Chat**（kelivo） | `com.psyche.kelivo` / `psyche.kelivo` | Android `namespace`+`applicationId`、macOS+iOS bundle id（四处）；pubspec `name`；建议 SharedPreferences key 加前缀（breaking，需迁移）；**API key → FlutterSecureStorage** | — | 改 SharedPreferences key 前缀 = 旧数据丢失，需迁移；Windows 无 bundle id，靠 exe/`name` 隔离需验证 |
| **Sakrylle Image**（已上线） | 已基本品牌化 | 仅补 `VITE_SAKRYLLE_OAUTH_BASE`/`VITE_SAKRYLLE_OAUTH_CLIENT_ID` 到 `vite-env.d.ts` | **localStorage/IndexedDB 前缀已是 `sakrylle-image-playground.*`，不改**（改了丢用户本地状态） | 未声明 VITE_ 变量 → `.env` 缺失静默 fallback 硬编码值 |

## 8. bundle / app 标识命名（`com.sakrylle.*`，已确认 2026-06-03）

| 产品 | bundle id / app id |
|---|---|
| Sakrylle Studio（桌面） | `com.sakrylle.studio` |
| Sakrylle Chat（移动/桌面） | `com.sakrylle.chat`（iOS 简写 `sakrylle.chat`，对齐 kelivo `psyche.kelivo` 简写惯例） |
| Sakrylle CLI | 无 bundle（native bin），二进制 `sakrylle`（短别名 `skl`，已确认 2026-06-03）；daemon 名 `sakrylle-cli-daemon`（替 `codex-monitor-daemon`/codex 等价） |
| Sakrylle Web | 无 bundle（容器服务） |
| Sakrylle Image | 无 bundle（SPA） |
| Chat 移动 deep link scheme | `sakrylle-chat://oauth/callback`（见 `03` §9；scheme 实现期核实） |

> 「不确定」：kelivo Windows `getApplicationSupportDirectory()` 实际返回 `%LOCALAPPDATA%/<organization>/<appName>`（依 pubspec `name`），bundle id 变更对 Windows 隔离效果**需验证**（调研标注）。

## 9. 分阶段实施计划

> 串行/并行已标注。各 fork 仓库独立，**多软件 fork 改造天然可并行**（无共享 state）。

### Phase 0 · 调研与保护（串行前置）
- **目标**：锁定命名、确认未决项、建迁移护栏。
- [ ] 确认 codex 各子目录是否真随 `CODEX_HOME`（核对 `lib.rs:13` 变量遮蔽疑问）
  - 涉及文件：`codex-rs/utils/home-dir/src/lib.rs`、thread-store/connectors 模块
  - 验收标准：确认无 `~/.codex` 残留硬编码漏改点
- [ ] 确认 CodexMonitor 是否引入 `tauri-plugin-store`（第三持久化路径）
  - 涉及文件：`CodexMonitor/package.json`、`src-tauri/Cargo.toml`
  - 验收标准：明确持久化路径数量
- [ ] 验证 kelivo Windows 路径隔离机制
  - 验收标准：Windows 数据目录隔离结论 + 文档补充

### Phase 1 · CLI 隔离（最高优先，独立可并行）
- [ ] codex `find_codex_home()` 改 `SAKRYLLE_CLI_HOME`
  - 目标：与 `~/.codex` 严格隔离
  - 涉及文件：`utils/home-dir/src/lib.rs:13`、`config/src/loader/mod.rs:52`、`login/src/auth/manager.rs:467-468`
  - 实施说明：优先读 `SAKRYLLE_CLI_HOME`→默认 `~/.sakrylle-cli`；`/etc/codex`→`/etc/sakrylle`（保留旧路径兼容层）；新增 `SAKRYLLE_API_KEY` 与 `CODEX_API_KEY` 共存
  - 验收标准：同机并行运行上游 codex + Sakrylle CLI 互不污染 auth.json/sessions/socket

### Phase 2 · 桌面/移动/Web 品牌与隔离（并行）
- [x] Sakrylle Studio bundle/端口/localStorage 迁移 — **[✓ 2026-06-06 CodexMonitor 工作树已实现]**
  - 涉及文件：`tauri.conf.json:4-5`、`daemonctl.rs:28/30`、`threadStorage.ts:3-7`
  - 实施说明：端口 4732→4733；localStorage 新前缀为 `sakrylle-studio.*`，兼容读 `codexmonitor.*` / `sakrylle-monitor.*` 后写新 key；settings.json/workspaces.json 由 bundle id 隔离，首发不迁移上游数据
  - 验收标准：与上游 CodexMonitor 同机并行无端口/配置冲突；仍需真实 Sakrylle CLI 冒烟
- [ ] Sakrylle Web `DATA_DIR` + 品牌
  - 涉及文件：`env.py:216/771/772-773/775`
  - 实施说明：删追加后缀逻辑；新增 `SAKRYLLE_*` 别名保留 `WEBUI_*` fallback
  - 验收标准：独立 DATA_DIR 隔离；品牌完全 Sakrylle 化
- [ ] Sakrylle Chat bundle id（四处）+ 凭据加固
  - 涉及文件：`build.gradle.kts:12,27`、`AppInfo.xcconfig:11`、`project.pbxproj:689`、`settings_provider.dart`
  - 实施说明：API key SharedPreferences→FlutterSecureStorage；SharedPreferences key 加前缀（带迁移）
  - 验收标准：bundle 隔离 + 凭据加密
- [ ] Sakrylle Image `vite-env.d.ts` 补声明
  - 涉及文件：`src/vite-env.d.ts:7-12`、`sakrylleAuth.ts:10-11`
  - 验收标准：两 VITE_ 变量有声明，无静默 fallback；localStorage 前缀**不动**

### Phase 3 · 凭据/审计统一（依赖 Phase 1-2）
- [ ] 统一凭据存储策略（OS keyring 优先）
- [ ] 遥测/release URL 指向 fork（隔离面 #10）

### Phase 4 · 测试 / 发布 / 回滚
- [ ] 各 fork 并行/同机共存冒烟测试
  - 验收标准：6 软件与各自上游同机运行零冲突
- [ ] 迁移回滚预案（localStorage/SharedPreferences/settings.json 迁移失败兜底）

## 10. 优先级

P0：Phase 1（CLI 隔离，唯一会争上游目录的高风险项）。
P1：Phase 2 各 fork 品牌/隔离。
P2：Phase 3 凭据加固 + 审计。

## 11. 风险

- **R1（高）**：CLI 不隔离 → 与上游 codex 争 `~/.codex`（auth.json/sessions/memories/socket），数据交叉污染。**`SAKRYLLE_CLI_HOME` 是硬性前提。**
- **R2（中）**：CodexMonitor 端口 4732 同机冲突 → 改 4733。
- **R3（中）**：localStorage/SharedPreferences/settings.json key 迁移 → 旧用户本地状态/token 丢失，需一次性迁移逻辑。
- **R4（中）**：kelivo API key 明文存储 → root/开发模式可读，需 FlutterSecureStorage。
- **R5（低）**：open-webui 重命名 `WEBUI_SECRET_KEY` 不留 fallback → 服务起不来。
- **R6（低）**：image 未声明 VITE_ 变量 → 配置错误静默 fallback 难发现。

## 12. 验收标准（整体）

1. 6 个软件均能与各自上游在同一台机器**并行运行零冲突**（目录/端口/socket/localStorage 不重叠）。
2. CLI **绝不**读写 `~/.codex` 或 `CODEX_*`；`SAKRYLLE_CLI_HOME` 生效。
3. `SAKRYLLE_*` 变量全集（§5）实现，上游变量保留 fallback。
4. bundle id 统一 `com.sakrylle.*`（§8）。
5. 凭据加密存储（kelivo 迁 FlutterSecureStorage）。
6. 所有 key 前缀迁移有兜底逻辑，不静默丢用户数据。

## 13. 后续问题

- codex 子目录是否全随 `CODEX_HOME`（变量遮蔽疑问）？（**不确定**，Phase 0）
- CodexMonitor 是否有 `tauri-plugin-store` 第三持久化路径？（**已核实 2026-06-06：CodexMonitor 工作树未使用 `tauri-plugin-store`；持久化路径为 appData settings/workspaces + WebView localStorage**）
- kelivo Windows 路径隔离实际机制？（**不确定**）
- open-webui `ENABLE_OAUTH_PERSISTENT_CONFIG`（`config.py:143` 默认 False）启用后 DB OAuth 配置与 env 优先级？（**不确定**）
- codex app-server `sqlite_home` 是否独立于 `CODEX_HOME`？（**不确定**，`config_manager.rs` 未深入）
