---
title: 01 · 仓库清单
status: canonical
scope: overview
last_verified: 2026-06-06
---

# 01 · 仓库清单

> 规划文档（planning only）。本文为静态快照（截至 2026-06-03），生产状态以服务器实际运行为准。
> 兄弟文档：见 `00-executive-summary.md`（执行摘要）、`03-sakrylle-api-oidc-architecture.md`（IdP 架构）、`05-configuration-isolation-standard.md`（配置隔离规范）。

---

## 1. 总览

| # | 产品 | 上游 | 本地路径 | Fork 分支 | 上线状态 |
|---|---|---|---|---|---|
| 1 | Sakrylle API | Wei-Shaw/sub2api | `/Volumes/APFS_HD/Documents/Github/sub2api` | `theme/monet-purple` | **已上线** |
| 2 | Sakrylle Image | gpt_image_playground（不确定原始上游名） | `/Volumes/APFS_HD/Documents/Github/gpt_image_playground` | `不确定` | **已上线** |
| 3 | Sakrylle CLI | openai/codex | `/Volumes/APFS_HD/Documents/Github/codex` | 上游主线（未 fork） | 未 fork |
| 4 | Sakrylle Studio | Dimillian/CodexMonitor | `/Volumes/APFS_HD/Documents/Github/CodexMonitor` | 上游主线（未 fork） | 未 fork |
| 5 | Sakrylle Web | open-webui/open-webui | `/Volumes/APFS_HD/Documents/Github/open-webui` | 上游主线（未 fork） | 未 fork |
| 6 | Sakrylle Chat | kelivo（不确定原始上游名） | `/Volumes/APFS_HD/Documents/Github/kelivo` | 上游主线（未 fork） | 未 fork |

---

## 2. 仓库详情

### 2.1 Sakrylle API（sub2api fork）

| 属性 | 值 |
|---|---|
| 产品名 | Sakrylle API |
| 上游基座 | [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api) |
| Fork remote | [Ranshen1209/sub2api](https://github.com/Ranshen1209/sub2api) |
| 本地路径 | `/Volumes/APFS_HD/Documents/Github/sub2api` |
| 工作分支 | `theme/monet-purple` |
| 技术栈 | Go 1.23 / Gin / ent ORM / PostgreSQL 18 / Redis 8 / Vue 3 / Vite |
| 上线状态 | **已上线生产** |
| 生产域名 | `sub.sakrylle.com`（主站）、`api.sakrylle.com`（API 网关）、`doc.sakrylle.com`（文档）、`status.sakrylle.com`（监控） |
| 服务器 | `cliproxyapi-jp`（154.36.159.42，SSH alias `ssh-tokyo`） |
| Sakrylle 定位 | 全生态 IdP + AI API 网关（OAuth 2.0 provider，billing，model routing） |

**关键文件（按功能分类）：**

| 功能 | 文件路径 | 关键行 |
|---|---|---|
| OAuth 路由注册 | `backend/internal/server/routes/oauth.go` | 57–112 |
| Device Flow 路由 | `backend/internal/server/routes/oauth_device.go` | 54–79 |
| OAuth 核心服务（3000+ 行） | `backend/internal/service/oauth_provider_service.go` | — |
| Scope 定义 | `backend/internal/service/oauth_scopes.go` | 25（canonicalScopes map） |
| Handler：discovery/token/revoke | `backend/internal/handler/oauth_provider_handler.go` | 625–647（discovery 文档） |
| UserInfo 端点（/v1/me） | `backend/internal/handler/oauth_provider_account_handler.go` | 76（函数入口），161–221（claim 裁剪） |
| Consent 页 XSS 防护 | `backend/internal/handler/oauth_provider_consent.go` | 326 |
| SPA fallback bypass 列表 | `backend/internal/web/embed_on.go` | 315 |
| JWT 签发（HS256） | `backend/internal/service/auth_service.go` | 1172（`jwt.NewWithClaims(HS256)`） |
| JWT 配置 | `backend/internal/config/config.go` | 1195（`JWTConfig`） |
| API Key 鉴权中间件 | `backend/internal/server/middleware/api_key_auth.go` | 32–233 |
| Auth cache 快照 | `backend/internal/service/api_key_auth_cache.go` | 6（`APIKeyAuthSnapshot`） |
| Channel 服务（定价缓存） | `backend/internal/service/channel_service.go` | 136（TTL 10min），546（checkRestricted） |
| 网关服务（/v1/models） | `backend/internal/service/gateway_service.go` | 9714（`GetAvailableModels`） |
| Migration：OAuth provider | `backend/migrations/143_oauth_provider.sql` | — |
| Migration：OAuth v2 | `backend/migrations/145_oauth_v2.sql` | — |
| Migration：Sakrylle client seed | `backend/migrations/144_oauth_seed_sakrylle.sql` | — |
| Migration：v2 clients seed | `backend/migrations/148_oauth_v2_sakrylle_seed.sql` | — |
| 前端主色 | `frontend/tailwind.config.js` | （`#9181bd` Monet purple） |

---

### 2.2 Sakrylle Image（gpt_image_playground fork）

| 属性 | 值 |
|---|---|
| 产品名 | Sakrylle Image |
| 上游基座 | gpt_image_playground（不确定原始仓库 remote，须确认） |
| Fork remote | 不确定（须确认） |
| 本地路径 | `/Volumes/APFS_HD/Documents/Github/gpt_image_playground` |
| 工作分支 | 不确定（须确认） |
| 技术栈 | React 19 + TypeScript + Vite 6 + Tailwind 3 + Zustand 5 + i18next / 纯 SPA，无后端 |
| 上线状态 | **已上线生产** |
| 生产域名 | `image.sakrylle.com` |
| Sakrylle 定位 | AI 图像生成工坊，作为 sub2api OAuth RP 接入，展示 OAuth PKCE 全流程参考实现 |

**关键文件：**

| 功能 | 文件路径 | 关键行 |
|---|---|---|
| OAuth PKCE 全流程 | `src/lib/sakrylleAuth.ts` | 10–20（常量），229（beginLogin），248（handleCallback），390（refreshIfNeeded），399（forceRefreshToken） |
| 平台 API 封装（/v1/me 等） | `src/lib/sakrylleAccount.ts` | — |
| OAuth Bearer fallback | `src/lib/oauthFallback.ts` | — |
| 多 group token 路由 | `src/lib/groupSelection.ts` | — |
| OAuth callback 入口 | `src/main.tsx` | （pathname===/oauth/callback 检测） |
| 余额轮询 / 登录按钮 | `src/components/Header.tsx` | 15（SAKRYLLE_PURCHASE_URL），163（GitHub 链接） |
| API base URL | `src/lib/apiProfiles.ts` | 12（默认 `https://api.sakrylle.com/v1`） |
| IndexedDB 定义 | `src/lib/db.ts` | 3–4（`sakrylle-image-playground`，`DB_VERSION=3`） |
| vite-env 变量声明 | `src/vite-env.d.ts` | 6–17（4 个已声明，OAUTH_BASE/CLIENT_ID 未声明） |
| Dockerfile（二阶段构建） | `deploy/Dockerfile` | — |
| 占位符运行时注入脚本 | `deploy/inject-api-url.sh` | — |
| nginx 配置（SPA fallback） | `deploy/nginx.conf` | — |
| Sakrylle logo SVG 组件 | `src/components/icons.tsx` | （`SakrylleLogo`，紫渐变+樱花 SVG） |

---

### 2.3 Sakrylle CLI（OpenAI Codex CLI fork，待 fork）

| 属性 | 值 |
|---|---|
| 产品名 | Sakrylle CLI |
| 上游基座 | [openai/codex](https://github.com/openai/codex) |
| Fork remote | 待创建（`Ranshen1209/sakrylle-cli`，建议名） |
| 本地路径 | `/Volumes/APFS_HD/Documents/Github/codex`（当前为上游 clone） |
| 工作分支 | 待创建 |
| 技术栈 | Node.js 薄封装（ESM `codex-cli/bin/codex.js`）+ Rust 核心（`codex-rs/`，edition 2024，ratatui TUI，reqwest，sqlx-sqlite，rmcp） |
| 上线状态 | 未 fork，未上线 |
| Sakrylle 定位 | 命令行 AI 编程助手，接入 `api.sakrylle.com/v1`，CLI 首选 Device Flow OIDC，备选 API Key |

**关键文件（上游只读参考）：**

| 功能 | 文件路径 | 关键行 |
|---|---|---|
| Node.js 入口，平台检测 + spawn | `codex-cli/bin/codex.js` | 1–70 |
| npm 包名 `@openai/codex` | `codex-cli/package.json` | 2（name），7（bin） |
| Rust main，clap CLI，子命令 | `codex-rs/cli/src/main.rs` | 87–130（bin_name="codex"） |
| 配置目录解析（CODEX_HOME 入口） | `codex-rs/utils/home-dir/src/lib.rs` | 13–18（`find_codex_home()`），59（硬编码 `.codex`） |
| config.toml schema | `codex-rs/config/src/config_toml.rs` | 136–190（`ConfigToml`） |
| ModelProviderInfo（base_url/env_key/wire_api） | `codex-rs/model-provider-info/src/lib.rs` | 82–120 |
| wire_api="chat" 已移除错误 | `codex-rs/model-provider-info/src/lib.rs` | 46（`CHAT_WIRE_API_REMOVED_ERROR`） |
| API Key env 常量 | `codex-rs/login/src/auth/manager.rs` | 467–468（OPENAI_API_KEY / CODEX_API_KEY） |
| auth.json 路径 | `codex-rs/login/src/auth/storage.rs` | 85（`codex_home.join("auth.json")`） |
| 系统配置路径 | `codex-rs/config/src/loader/mod.rs` | 52（`/etc/codex/config.toml`） |
| TUI 品牌字符串（"OpenAI Codex"） | `codex-rs/tui/src/history_cell/session.rs` | 343, 410 |
| TUI 欢迎页品牌 | `codex-rs/tui/src/onboarding/welcome.rs` | 97 |
| originator（User-Agent 标识） | `codex-rs/login/src/auth/default_client.rs` | 37（`DEFAULT_ORIGINATOR="codex_cli_rs"`） |
| Responses API proxy（内建适配层） | `codex-rs/responses-api-proxy/src/lib.rs` | 53（默认上游 `https://api.openai.com/v1/responses`） |

**最高优先级验证项：** `wire_api="chat"` 已被硬性移除，Codex 只说 `POST /v1/responses`；sub2api 已实现该端点，CLI 接入 blocker 已解除。后续重点是按 `10` / `11` / `91` 验证真实 Codex 场景兼容性（streaming SSE、tool_call、文件编辑、shell exec）与 `usage_logs` 计费记录。

---

### 2.4 Sakrylle Studio（CodexMonitor fork，待 fork）

| 属性 | 值 |
|---|---|
| 产品名 | Sakrylle Studio |
| 上游基座 | [Dimillian/CodexMonitor](https://github.com/Dimillian/CodexMonitor) |
| Fork remote | 待创建（`Ranshen1209/sakrylle-studio`，建议名） |
| 本地路径 | `/Volumes/APFS_HD/Documents/Github/CodexMonitor`（当前为上游 clone） |
| 工作分支 | 待创建 |
| 技术栈 | Tauri 2.10.3（Rust 后端）+ React 19 + TypeScript 5.8 + Vite 7；portable-pty + xterm.js；git2；whisper-rs；tauri-plugin-updater |
| 上线状态 | 未 fork，未上线 |
| Sakrylle 定位 | Sakrylle CLI 的桌面 GUI，通过 JSON-RPC stdio 驱动 CLI 子进程（`codex app-server`），配置透传至 `~/.sakrylle-cli/` |

**关键文件（上游只读参考）：**

| 功能 | 文件路径 | 关键行 |
|---|---|---|
| Tauri 主配置（productName / identifier / updater） | `src-tauri/tauri.conf.json` | 4–5（productName / identifier），updater.endpoints |
| Cargo.toml（Rust 包名） | `src-tauri/Cargo.toml` | package.name = `codex-monitor` |
| CLI 子进程 spawn | `src-tauri/src/backend/app_server.rs` | 749（`spawn_workspace_session`），646（`build_codex_command_with_bin`，默认 fallback `codex`） |
| CODEX_HOME 解析 | `src-tauri/src/codex/home.rs` | 13（`resolve_default_codex_home`） |
| App 设置文件路径 | `src-tauri/src/state.rs` | 50（`app_data_dir().join(settings.json)`） |
| auth.json 读取 | `src-tauri/src/shared/account.rs` | 63–65 |
| About 窗口标题（硬编码品牌） | `src-tauri/src/menu.rs` | 339 |
| 首页标题（硬编码品牌） | `src/features/home/components/Home.tsx` | 57 |
| About 页（GitHub URL / 标题） | `src/features/about/components/AboutView.tsx` | 5, 49, 75 |
| localStorage key 前缀（`codexmonitor.*`） | `src/features/threads/utils/threadStorage.ts` | 3–7（5 个 key） |
| Sentry DSN（硬编码上游项目） | `src/main.tsx` | 9 |
| updater GitHub URL | `src/features/update/utils/postUpdateRelease.ts` | 4, 6 |
| APP_IDENTIFIER 常量 / 三平台路径 | `src-tauri/src/bin/codex_monitor_daemonctl.rs` | 30（`APP_IDENTIFIER = com.dimillian.codexmonitor`），250–288 |
| 默认 daemon 端口 | `src-tauri/src/bin/codex_monitor_daemonctl.rs` | 28（`DEFAULT_LISTEN_ADDR = 0.0.0.0:4732`） |

---

### 2.5 Sakrylle Web（open-webui fork，待 fork）

| 属性 | 值 |
|---|---|
| 产品名 | Sakrylle Web |
| 上游基座 | [open-webui/open-webui](https://github.com/open-webui/open-webui) |
| Fork remote | 待创建（`Ranshen1209/sakrylle-web`，建议名） |
| 本地路径 | `/Volumes/APFS_HD/Documents/Github/open-webui`（当前为上游 clone） |
| 工作分支 | 待创建 |
| 技术栈 | SvelteKit + TypeScript + Tailwind CSS（前端）+ FastAPI + SQLAlchemy + Alembic（后端）+ authlib（OIDC）；默认 SQLite，可切 PostgreSQL |
| 上线状态 | 未 fork，未上线 |
| Sakrylle 定位 | 全功能 AI 聊天界面，通过 OpenAI-compat API 接入 `api.sakrylle.com/v1`，OIDC SSO 依赖 Sakrylle API IdP 扩展完成后接入 |

**关键文件（上游只读参考）：**

| 功能 | 文件路径 | 关键行 |
|---|---|---|
| OIDC/OAuth provider 注册逻辑 | `backend/open_webui/config.py` | 3579–3906（所有 `OAUTH_*` / `OPENID_*` ConfigVar） |
| OpenAI endpoint 配置 | `backend/open_webui/config.py` | 291–417（`OPENAI_API_BASE_URLS` / `OPENAI_API_KEYS`） |
| WEBUI_NAME / 追加后缀 bug | `backend/open_webui/env.py` | 771–773（改前须删 772–773 追加逻辑） |
| DATA_DIR / DATABASE_URL | `backend/open_webui/env.py` | 216, 261 |
| WEBUI_SECRET_KEY | `backend/open_webui/env.py` | 612–616 |
| OAuth callback（userinfo 提取） | `backend/open_webui/utils/oauth.py` | 1557–1573（userinfo 拉取，仅支持一层 key） |
| 前端品牌常量 | `src/lib/constants.ts` | 4（`APP_NAME = 'Open WebUI'`） |
| Svelte store 初值 | `src/lib/stores/index.ts` | 13（`WEBUI_NAME` writable） |
| 页面 title（硬编码） | `src/app.html` | 118（`<title>Open WebUI</title>`，不读 WEBUI_NAME） |
| 品牌图片（可 volume mount 覆盖） | `static/static/` | favicon.png/svg/ico, splash.png, splash-dark.png, logo.png |
| PWA manifest | `static/static/site.webmanifest` | name/short_name 硬编码 `Open WebUI` |
| Connections 面板 UI | `src/lib/components/admin/Settings/Connections.svelte` | 39–76 |

**接入前提（阻塞项）：** open-webui 的 generic OIDC 分支要求 `server_metadata_url=OPENID_PROVIDER_URL`，即 `/.well-known/openid-configuration`。Sakrylle API 当前仅暴露 RFC 8414 的 `/.well-known/oauth-authorization-server`，**OIDC 基座完成前无法完整接入**。见 `30-sakrylle-web-research.md`。

---

### 2.6 Sakrylle Chat（kelivo fork，待 fork）

| 属性 | 值 |
|---|---|
| 产品名 | Sakrylle Chat |
| 上游基座 | kelivo（不确定原始上游 remote，须确认） |
| Fork remote | 待创建（`Ranshen1209/sakrylle-chat`，建议名） |
| 本地路径 | `/Volumes/APFS_HD/Documents/Github/kelivo` |
| 工作分支 | 待创建 |
| 技术栈 | Flutter 1.1.15 / Dart SDK ^3.8.1 / provider ^6.0.5（状态管理）/ Hive ^2.2.3（对话历史）/ shared_preferences ^2.2.3（设置）/ http + dio（HTTP）/ Material 3 + dynamic_color |
| 上线状态 | 未 fork，未上线 |
| Sakrylle 定位 | 跨平台 AI 聊天客户端（Android / iOS / macOS / Windows / Linux / Web），通过 OAuth PKCE 接入 Sakrylle API，支持 Monet purple 主题 |

**关键文件（上游只读参考）：**

| 功能 | 文件路径 | 关键行 |
|---|---|---|
| 核心 provider 配置，内置 provider 列表 | `lib/core/providers/settings_provider.dart` | 54–68（`_builtInProviderKeysInOrder`），4404–4434（`_defaultBase` URL 映射） |
| Provider 配置 UI（baseUrl/apiKey/chatPath） | `lib/features/provider/pages/provider_detail_page.dart` | 902–950，129–143（`isUserAdded()` 固定集合） |
| API 请求构建（Bearer 注入） | `lib/core/services/api/chat_api_service.dart` | 703–762 |
| Hive 初始化 | `lib/core/services/chat/chat_service.dart` | 51–78（box 名称常量） |
| 跨平台数据目录 | `lib/utils/app_directories.dart` | 17–28（`getAppDataDirectory()`） |
| 主题调色板（9 套，含紫色） | `lib/theme/palettes.dart` | 234（purple 调色板，primary light `#5D5698`，与 Sakrylle `#9181bd` 不同） |
| ThemeData 构建 | `lib/theme/theme_factory.dart` | — |
| Android bundle id | `android/app/build.gradle.kts` | 12（namespace），27（applicationId = `com.psyche.kelivo`） |
| iOS bundle id | `ios/Runner.xcodeproj/project.pbxproj` | 689（`psyche.kelivo`），554（Extension bundle id） |
| macOS bundle id | `macos/Runner/Configs/AppInfo.xcconfig` | 8（PRODUCT_NAME），11（`com.psyche.kelivo`） |
| AndroidManifest（无自定义 scheme） | `android/app/src/main/AndroidManifest.xml` | 12（android:label="Kelivo"） |
| iOS Info.plist（无 CFBundleURLSchemes） | `ios/Runner/Info.plist` | CFBundleDisplayName = Kelivo |
| Flutter 入口 + DynamicColorBuilder | `lib/main.dart` | 104（desktop title "Kelivo"） |
| pubspec.yaml（版本 / 依赖） | `pubspec.yaml` | 1（name: Kelivo），版本 1.1.15+52 |

**注意事项：**
- API Key 以明文 JSON 存储于 SharedPreferences（无加密），fork 时须迁移至 `flutter_secure_storage`。
- 无 OAuth/OIDC 基础设施，需从零添加 `flutter_web_auth_2` + 自定义 URL scheme 注册。
- iOS Extension bundle id（`psyche.kelivo.GenerationActivityExtension`）须随主 bundle id 同步修改，否则 iOS 后台生成功能失效。

---

## 3. 快速对比表

| 产品 | 认证机制（当前） | OIDC 就绪程度 | 品牌化进度 | 配置隔离就绪 |
|---|---|---|---|---|
| Sakrylle API | OAuth 2.0 provider（完整），HS256 会话 JWT | OIDC Core 1.0 完整（id_token/JWKS/discovery/UserInfo/RS256+ES256 per-client，2026-06-04）；剩 scope enforcement 开启需生产审批 | 完成（Monet purple + 樱花） | 生产运行中 |
| Sakrylle Image | OAuth PKCE（完整，生产使用中） | 服务端已就绪；客户端缺 openid scope/id_token 解析，client 侧改动即可消费 | 完成（Sakrylle 品牌化） | localStorage 前缀已用 `sakrylle-image-playground` |
| Sakrylle CLI | API Key only（`OPENAI_API_KEY`/`CODEX_API_KEY`），无 OIDC | 无，需 fork 后对接 Device Flow 或 API Key | 未改（10 处+ 硬编码） | 危险：默认 `~/.codex/` 与上游冲突，必须改 `find_codex_home()` |
| Sakrylle Studio | 无（透传给 CLI auth.json） | 无独立 auth，依赖 CLI | 未改（6 文件 10 处） | 危险：`com.dimillian.codexmonitor` + `codexmonitor.*` localStorage 前缀未改 |
| Sakrylle Web | 密码 + 四路 OAuth/OIDC（authlib） | open-webui 侧完整，阻塞于 sub2api 缺 `/.well-known/openid-configuration` | 未改（需 env.py:772 代码修改） | `DATA_DIR` 独立即隔离，无冲突 |
| Sakrylle Chat | API Key 明文（SharedPreferences） | 无，需从零实现 PKCE + URL scheme | 未改（4 处 bundle id + 多处字符串） | 改 bundle id 后自动隔离，SharedPreferences 需加密 |

---

## 4. 后续问题

1. Sakrylle Image 上游仓库的 remote URL 须确认（调研结果显示本地路径存在但未记录 fork remote）。
2. kelivo 的上游 remote URL 须确认（同上）。
3. Sakrylle CLI 的 `sakrylle-cli` GitHub 仓库是否已创建，或先在本地 `/Volumes/APFS_HD/Documents/Github/codex` 分支开发后再推？
4. Sakrylle Studio daemon 端口应选何值以避免与上游 CodexMonitor（4732）冲突？建议 4733，须确认无其他服务占用。
5. Sakrylle Web 是否计划在 `chat.sakrylle.com` 或其他子域上线？该域名的 Cloudflare DNS A 记录和 nginx 配置须预先规划。
