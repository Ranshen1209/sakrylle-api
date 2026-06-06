---
title: 20 · Sakrylle Studio 调研：CodexMonitor 现状分析
status: local
scope: product
last_verified: 2026-06-06
---

# 20 · Sakrylle Studio 调研：CodexMonitor 现状分析

> 规划文档（planning only）。Sakrylle API / Sakrylle Image **已上线生产**，Sakrylle Studio 尚未 fork，**不涉及任何生产系统**。本文为只读调研，不含改动指令。
> 兄弟文档：见 `21-sakrylle-studio-development-plan.md`（改造计划）、`05-configuration-isolation-standard.md`（配置隔离规范）、`03-sakrylle-api-oidc-architecture.md`（IdP 基座）、`10-sakrylle-cli-research.md`（CLI 上游调研）。

---

## 1. 调研范围

对上游 [Dimillian/CodexMonitor](https://github.com/Dimillian/CodexMonitor) 做全面只读扫描，覆盖：

- 技术栈与构建系统（Tauri 2、React 19、Rust crate 依赖）
- 进程启动与通信机制（如何发现、启动、监控 codex 子进程）
- 配置与数据目录（Tauri 应用数据 + CLI 共享路径）
- 认证机制（auth 如何传递）
- UI 框架与主题体系
- Bundle identifier 与发布体系（自动更新、pubkey、多平台配置）
- 品牌硬编码表面（待替换点，含 file:line）
- 差距分析（相对 Sakrylle 生态目标）

---

## 2. 关键结论

1. **Tauri 2 + React 19**：Rust 后端负责进程管理与系统集成，React 前端负责 UI；通过 Tauri IPC（`invoke` + event）桥接。前端无法直接操作系统资源，所有进程/文件操作均走 Rust 侧 invoke handler。
2. **codex 子进程为核心**：Studio 本身不执行任何 AI 逻辑；它通过 `tokio::process::Command::spawn` 启动 `codex app-server`，经 stdin/stdout JSON-RPC（LSP 风格）完成双向通信。Tauri 事件 `app-server-event` 将推送推向前端。
3. **CLI 二进制路径可运行时配置**：`AppSettings.codexBin` 字段在 Settings → Codex 页面可由用户填写，或通过 `CODEX_HOME` 注入；无需重新编译即可指向 Sakrylle CLI 二进制（`sakrylle`）。
4. **认证完全透传**：Studio 不持有任何 API 凭据；auth 完全由 CLI 管理（`~/.codex/auth.json`），Studio 只读取 JWT payload 做用户信息展示（`plan_type` / `email`）。
5. **配置隔离由 bundle id 驱动**：Tauri `app_data_dir()` 推导的三平台路径完全取决于 `tauri.conf.json:identifier`（`com.dimillian.codexmonitor`）。改 bundle id 即完成 Tauri 层路径隔离，无需改 Rust 代码。
6. **CLI 路径共用是主要风险**：Studio 通过 `CODEX_HOME`（默认 `~/.codex/`）与 CLI 共享配置；若不设独立 `CODEX_HOME`，多版本并存会导致 auth.json / sessions JSONL 污染。
7. **品牌硬编码分散于 8 类文件，约 20 处**：改造成本低，无逻辑耦合，绝大多数为纯字符串替换。
8. **Sentry DSN 硬编码上游 project**：崩溃报告会送至 Dimillian 的 Sentry，fork 前必须替换或禁用（优先替换为 Sakrylle 自有 DSN）。环境变量 `VITE_SENTRY_DSN` 可覆盖，但不确定是否已有空值处理逻辑（`src/main.tsx:8`）。
9. **updater pubkey 硬编码**：自动更新依赖 `tauri.conf.json` 中的 minisign 公钥；fork 须重新生成密钥对，否则自动更新无法使用或存在安全风险。
10. **localStorage 前缀有 `codexmonitor.*`**：已通过 `05` 文档确认，`src/features/threads/utils/threadStorage.ts:3-7` 存在该前缀，fork 需改为 `sakrylle-monitor.*`。
11. **daemon 端口 4732 需改 4733**：与规范 `05-configuration-isolation-standard.md` §4.1 中的 Studio 隔离要求对齐。
12. **tauri-plugin-liquid-glass 已引入**：表明支持 macOS Liquid Glass 毛玻璃效果（Tauri 2.10.3 + macOS 15+），最低 macOS 版本需确认（不确定，见 §6 U7）。

---

## 3. 相关文件路径（path:line，引用自调研）

> 以下路径以 CodexMonitor 仓库根目录为基准（上游未 fork 至本仓库），均来自调研摘要中的 keyFiles / brandingSurfaces / configAndDataPaths 字段。

### 3.1 核心架构文件

| 关注点 | 路径:行 |
|---|---|
| Tauri 主配置（productName / identifier / updater / pubkey） | `src-tauri/tauri.conf.json` |
| Rust package name / description | `src-tauri/Cargo.toml` |
| app-server 子进程 spawn（`spawn_workspace_session`） | `src-tauri/src/backend/app_server.rs:749` |
| codexBin 路径解析，默认 fallback `codex` | `src-tauri/src/backend/app_server.rs:646` |
| codex_home 解析（CODEX_HOME env 或 `~/.codex`） | `src-tauri/src/codex/home.rs:13` |
| auth.json 读取（`read_auth_account`） | `src-tauri/src/shared/account.rs:63` |
| AppState::load（app_data_dir + settings.json + workspaces.json） | `src-tauri/src/state.rs:50` |
| sessions JSONL 扫描用量统计（`resolve_codex_sessions_root`） | `src-tauri/src/shared/local_usage_core.rs:522` |
| About 窗口标题（硬编码 `"About Codex Monitor"`） | `src-tauri/src/menu.rs:339` |
| app_name 动态读取（从 `package_info().name`，改 Cargo.toml 可解决） | `src-tauri/src/menu.rs:67` |
| Rust 程序入口 | `src-tauri/src/main.rs` |
| Tauri `run()` + invoke_handler 注册 | `src-tauri/src/lib.rs:70` |
| TCP daemon 二进制（远程 backend 模式） | `src-tauri/src/bin/codex_monitor_daemon.rs` |
| daemon 默认端口（`4732`，需改 `4733`） | `src-tauri/src/bin/codex_monitor_daemonctl.rs:28` |
| APP_IDENTIFIER 常量（三平台路径驱动） | `src-tauri/src/bin/codex_monitor_daemonctl.rs:30` |
| localStorage `codexmonitor.*` 前缀 | `src/features/threads/utils/threadStorage.ts:3-7` |
| config_toml_core（读写 CLI config.toml） | `src-tauri/src/shared/codex_core.rs`（不确定具体行，需 fork 后确认） |
| codex_login_core（JSON-RPC `account/login/start`） | `src-tauri/src/shared/codex_core.rs:653` |

### 3.2 前端品牌硬编码文件

| 关注点 | 路径:行 |
|---|---|
| 首页标题 `Codex Monitor` | `src/features/home/components/Home.tsx:57` |
| About 页 GitHub URL（`Dimillian/CodexMonitor`） | `src/features/about/components/AboutView.tsx:5` |
| About 页标题 `Codex Monitor` | `src/features/about/components/AboutView.tsx:49` |
| About 页署名（`Made with ♥ by Codex & Dimillian`） | `src/features/about/components/AboutView.tsx:75` |
| About 页 Twitter/社交链接（`@dimillian`，不确定，见 §6 U3） | `src/features/about/components/AboutView.tsx:6`（不确定） |
| Settings 自动更新描述（`CodexMonitor checks for new app versions`） | `src/features/settings/components/sections/SettingsAboutSection.tsx:115` |
| Settings Server 页多处字符串 | `src/features/settings/components/sections/SettingsServerSection.tsx:186,339,393,549` |
| Settings Codex 页描述（`used by CodexMonitor`） | `src/features/settings/components/sections/SettingsCodexSection.tsx:233` |
| 工作区对话框文案（`from CodexMonitor`） | `src/features/app/hooks/useWorkspaceDialogs.ts:282,299` |
| 通知文案（`Open CodexMonitor to respond`） | `src/features/notifications/hooks/useAgentResponseRequiredNotifications.ts:362` |
| React 入口（Sentry DSN 硬编码上游 project） | `src/main.tsx:9` |
| VITE_SENTRY_DSN 环境变量入口（覆盖 DSN） | `src/main.tsx:8` |
| 更新后 GitHub releases URL（Dimillian/CodexMonitor） | `src/features/update/utils/postUpdateRelease.ts:4,6` |
| React 根组件 | `src/App.tsx` |

---

## 4. 当前实现摘要

### 4.1 技术栈

| 层 | 技术 | 版本 |
|---|---|---|
| 桌面框架 | Tauri | 2.10.3 |
| Rust 运行时 | Tokio（异步） | — |
| 前端框架 | React + TypeScript | 19 + 5.8 |
| 前端构建 | Vite | 7 |
| 终端组件 | xterm.js + portable-pty（PTY） | — |
| 虚拟列表 | @tanstack/react-virtual | — |
| 崩溃监控 | Sentry React SDK（DSN 硬编码上游 project，**fork 前必须替换**） | — |
| 自动更新 | tauri-plugin-updater（endpoint 硬编码 Dimillian/CodexMonitor，**fork 前必须替换**） | — |
| Git 集成 | git2（libgit2 Rust 绑定） | — |
| 语音听写 | whisper-rs | — |
| HTTP 客户端 | reqwest | — |
| 配置文件解析 | toml_edit（读写 CLI `config.toml`） | — |
| Tauri 插件 | dialog / notification / opener / process / liquid-glass | — |

### 4.2 进程启动：如何发现和启动 codex

Studio 发现并启动 codex 子进程的完整链路：

```
用户在 Settings → Codex 页面配置 codexBin（留空则 fallback 为 "codex"）
  │
  ├─ app_server.rs:646  build_codex_command_with_bin()
  │    ├─ 读 AppSettings.codexBin
  │    └─ 若为空：从 PATH 查找 "codex" 二进制
  │
  └─ app_server.rs:749  spawn_workspace_session()
       ├─ tokio::process::Command::spawn("{codexBin} app-server")
       ├─ cwd = 当前工作区目录（workspaces.json 中的路径）
       ├─ 注入 CODEX_HOME 环境变量（来自 AppSettings 或系统默认）
       ├─ stdin/stdout → JSON-RPC 双向管道（LSP 风格）
       └─ 推送 Tauri 事件 "app-server-event" 给 React 前端
```

**主要 JSON-RPC 消息类型**（Studio → codex 子进程）：

| 消息 | 作用 |
|---|---|
| `initialize` | 握手，协商协议版本 |
| `thread/open` | 打开会话线程 |
| `thread/sendUserMessage` | 发送用户提示 |
| `account/login/start` | 触发 CLI OAuth 登录流程，CLI 返回 `authUrl` |
| `workspace/getInfo` | 获取工作区元信息 |
| `usage/getSummary` | 汇总用量数据 |

**监控机制**：Studio 持续监听 codex 子进程的 stdout，解析 JSON-RPC 推送事件（`app-server-event`）；若子进程退出（非零状态码），Studio 捕获退出码并在 UI 展示错误状态。

### 4.3 远程 Backend 模式（可选功能）

除默认本地子进程模式外，Studio 支持远程 backend：

- `codex_monitor_daemon.rs`：独立 TCP daemon 二进制，监听 `localhost:4732`（AppSettings.remoteBackendHost 可配）。
- Studio 通过 WebSocket 连接，使用 `AppSettings.remoteBackendToken` 认证。
- Sakrylle Studio fork 需将端口默认值改为 `4733`（规范 `05` §4.1 要求，避免与上游 daemon 端口冲突）。
- 此功能是否纳入 Sakrylle Studio 首发范围，属产品决策（见 §8 后续问题）。

### 4.4 配置与数据目录

| 目录/文件 | 归属 | 路径（默认，macOS）|
|---|---|---|
| Tauri 应用数据（`app_data_dir()`） | Studio 自有 | `~/Library/Application Support/com.dimillian.codexmonitor/` |
| `settings.json` | Studio 自有 | `<app_data_dir>/settings.json`（AppSettings：codexBin、remoteBackendHost 等）|
| `workspaces.json` | Studio 自有 | `<app_data_dir>/workspaces.json` |
| `config.toml` | CLI 共享（读写） | `~/.codex/config.toml`（通过 `config_toml_core` 读写，**隔离风险点**）|
| `auth.json` | CLI 共享（只读） | `~/.codex/auth.json`（JWT payload 展示用）|
| `sessions/*.jsonl` | CLI 共享（只读） | `~/.codex/sessions/YYYY/MM/*.jsonl`（用量统计扫描）|
| `CODEX_HOME` | 环境变量覆盖 | 全局覆盖 `~/.codex` 路径（`src-tauri/src/codex/home.rs:13`）|
| localStorage | 浏览器存储 | 前缀 `codexmonitor.*`（`threadStorage.ts:3-7`，**需改为 `sakrylle-monitor.*`**）|

**Sakrylle Studio 目标路径**（改 bundle id 后自动推导）：

| 平台 | 目标路径 |
|---|---|
| macOS | `~/Library/Application Support/com.sakrylle.studio/` |
| Linux | `${XDG_CONFIG_HOME:-~/.config}/sakrylle/studio/`（Tauri 推导）|
| Windows | `%APPDATA%\Sakrylle\studio\`（Tauri 推导）|

### 4.5 认证机制

Studio **无独立认证层**，完整流程：

```
1. 用户在 Studio 触发"登录"
   │
   └─ 发送 JSON-RPC "account/login/start" 给 codex 子进程（codex_core.rs:653）
      │
      └─ codex 子进程执行 PKCE Authorization Code Flow
           ├─ 向 https://auth.openai.com 发起授权
           ├─ 返回 authUrl 给 Studio
           ├─ Studio 调用 tauri-plugin-opener 打开浏览器
           └─ 用户完成 OpenAI 登录 → CLI 写 JWT 至 ~/.codex/auth.json
              │
              └─ Studio 读取 auth.json（account.rs:63），解析 JWT payload 中：
                   - https://api.openai.com/auth:plan_type → 显示用户套餐
                   - email → 显示用户邮箱
```

**Sakrylle 场景下的认证模式**：
- 若 Sakrylle CLI 采用 `requires_openai_auth=false`（API Key 模式），Studio 现有登录 UI 不会被触发，用户只需在 CLI 配置 `SAKRYLLE_API_KEY`，Studio 直接显示"未认证"状态或读取 auth.json 中的 API key 字段做展示。
- 若后续接入 Sakrylle OIDC，需修改 `codex_login_core`（`codex_core.rs:653`）处的 login 流程，替换 `authUrl` 解析逻辑指向 `https://sub.sakrylle.com/oauth/authorize`。
- 见 `03-sakrylle-api-oidc-architecture.md` §9 了解 `sakrylle-desktop` client 注册详情（migration 148 已有 seed）。

### 4.6 UI 框架与主题

- **前端**：React 19 + TypeScript，CSS 方案待 fork 后确认（「不确定」，可能为 CSS Modules 或 Tailwind）。
- **当前主题**：暗色系，无公开设计系统文档。
- **`tauri-plugin-liquid-glass`**：已引入，表明支持 macOS Liquid Glass 毛玻璃效果（Tauri 2.10.3 + macOS 15+）。
- **目标主题**：引入 Monet Purple（`#9181bd`）主色，参考 Sakrylle API frontend 的 `tailwind.config.js` 配置方式，以及 `05` 规范中的梯度定义（50 `#f8f6fc` → 950 `#2d2640`；主渐变 `linear-gradient(135deg, #9181bd 0%, #7b6aab 100%)`）。强调色樱花粉（accent 500 `#ec6a9c`）用于 logo/高亮/CTA，紫色为主导。

### 4.7 Bundle Identifier 与发布体系

| 字段 | 当前值 | 目标值 | 文件 |
|---|---|---|---|
| `identifier`（核心，三平台路径由此推导） | `com.dimillian.codexmonitor` | `com.sakrylle.studio` | `src-tauri/tauri.conf.json` |
| `productName` | `Codex Monitor` | `Sakrylle Studio` | `src-tauri/tauri.conf.json` |
| `windows[0].title` | `Codex Monitor` | `Sakrylle Studio` | `src-tauri/tauri.conf.json` |
| Rust package name | `codex-monitor` | `sakrylle-studio` | `src-tauri/Cargo.toml` |
| Rust package description | `A Tauri App`（建议更新） | `Sakrylle Studio — GUI for Sakrylle CLI` | `src-tauri/Cargo.toml` |
| updater endpoint | Dimillian/CodexMonitor releases | Ranshen1209/sakrylle-studio releases | `src-tauri/tauri.conf.json:plugins.updater.endpoints` |
| updater pubkey | Dimillian minisign 公钥 | 重新生成 minisign 密钥对 | `src-tauri/tauri.conf.json:plugins.updater.pubkey` |
| npm 包名（如有） | 「不确定」 | `@sakrylle/studio` | `package.json`（fork 后确认是否存在）|

> 注意：iOS conf（`tauri.ios.conf.json`）和 Windows conf（`tauri.windows.conf.json`）可能存在独立的 identifier 字段，需 fork 后检查（见 §6 U4）。

---

## 5. 差距分析

### 5.1 高优先级差距（必须修复，fork 方可发布）

| # | 差距 | 影响 | 涉及路径 |
|---|---|---|---|
| G1 | Bundle id 为 `com.dimillian.codexmonitor`，三平台数据目录错误 | Tauri 应用数据与上游共用 | `src-tauri/tauri.conf.json:identifier` |
| G2 | Sentry DSN 硬编码上游 project | 崩溃报告泄露用户设备信息至 Dimillian 账户 | `src/main.tsx:9` |
| G3 | updater pubkey 为 Dimillian minisign 私钥签名 | 自动更新不可用或存在安全风险 | `src-tauri/tauri.conf.json:plugins.updater` |
| G4 | `CODEX_HOME` 默认 `~/.codex/`，与原版 codex 争抢 auth.json / sessions | 多版本并存时 token 污染 | `src-tauri/src/codex/home.rs:13` |
| G5 | codexBin 默认 fallback 为 `codex` | 指向原版 CLI；用户首次使用需手动配置 | `src-tauri/src/backend/app_server.rs:646` |
| G6 | localStorage 前缀 `codexmonitor.*` | 与上游 Studio 实例数据混用 | `src/features/threads/utils/threadStorage.ts:3-7` |

### 5.2 中优先级差距（品牌与体验）

| # | 差距 | 影响 | 涉及路径 |
|---|---|---|---|
| G7 | 约 20 处品牌字符串引用 `Codex Monitor` / `Dimillian` | UI 用户可见，品牌错误 | 见第 3.2 节 |
| G8 | About 页 Twitter 链接硬编码 `@dimillian`（「不确定」，见 §6 U3） | 点击跳转到他人主页 | `src/features/about/components/AboutView.tsx:6` |
| G9 | 无 Monet Purple 主题 | 与 Sakrylle 生态视觉不统一 | 需全新主题实现 |
| G10 | 无 Sakrylle cherry-blossom 图标 | 应用图标为上游 | `src-tauri/icons/` + `public/app-icon.png` |
| G11 | daemon 端口 `4732` 未隔离 | 与原版 daemon 端口冲突 | `src-tauri/src/bin/codex_monitor_daemonctl.rs:28` |

### 5.3 低优先级差距（功能层）

| # | 差距 | 影响 |
|---|---|---|
| G12 | 认证 UI 绑定 OpenAI/ChatGPT OAuth（`auth.openai.com`，`plan_type`） | 若 Sakrylle CLI 使用不同 OAuth provider，需改 `codex_login_core`（`src-tauri/src/shared/codex_core.rs:653`）；API Key 模式不受影响 |
| G13 | sessions JSONL 扫描格式依赖 CLI 产生结构 | 若 Sakrylle CLI 改变 JSONL schema，用量统计 UI 会显示空数据 |
| G14 | Studio 连接 CLI 依赖 `codex app-server` JSON-RPC 子协议兼容性 | 若 Sakrylle CLI 未实现兼容协议则需大量 Rust 侧适配；**Responses API blocker 已在 CLI 侧解除**（`10-sakrylle-cli-research.md` §2 结论 3）|

---

## 6. 不确定项（调研中标注）

| 编号 | 不确定事项 | 确认方式 |
|---|---|---|
| U1 | Sakrylle CLI 是否实现兼容的 `codex app-server` JSON-RPC 子协议（initialize/thread/send_user_message 等） | 调研 Sakrylle CLI fork 的 `codex-rs/app-server/` 实现 |
| U2 | 前端具体 CSS 方案（Tailwind / CSS Modules / 其他） | fork 后 `grep -r "tailwind" .` + 检查 `package.json devDependencies` |
| U3 | `AboutView.tsx:6` Twitter 链接确切内容（是否有 `@dimillian` 或其他社交链接）| fork 后直接读文件 |
| U4 | `tauri.ios.conf.json` 和 `tauri.windows.conf.json` 是否存在独立 identifier 字段 | fork 后检查文件存在性，搜索 `identifier` 字段 |
| U5 | sessions JSONL 格式是否与 Sakrylle CLI 产生的格式一致 | Sakrylle CLI 调研（`11-sakrylle-cli-development-plan.md`）完成后对比 schema |
| U6 | remote backend（TCP daemon）功能是否在 Sakrylle Studio 首发范围 | 产品决策，可暂时保留 UI 仅修改端口 |
| U7 | `tauri-plugin-liquid-glass` 最低 macOS 版本要求 | 查 tauri-plugin-liquid-glass changelog；若最低版本过高需提供降级路径 |
| U8 | `VITE_SENTRY_DSN` 是否已支持完整覆盖（空值时是否 disable Sentry） | 读 `src/main.tsx` 完整实现，确认是否有 `enabled: !!dsn` 类似逻辑 |
| U9 | `tauri-plugin-store` 是否引入（第三持久化路径） | fork 后检查 `package.json` + `src-tauri/Cargo.toml` |
| U10 | GitHub Actions workflows 是否存在（自动构建/发布） | fork 后检查 `.github/workflows/` 目录 |

---

## 7. 风险汇总

| 风险 | 等级 | 说明 |
|---|---|---|
| Sentry DSN 未替换即发布 | 高 | 崩溃数据送至 Dimillian Sentry project，泄露用户设备信息 |
| updater pubkey 未替换 | 高 | 自动更新签名验证失败（或更严重：信任他人签名的更新包）|
| `~/.codex` 路径污染 | 高 | 与原版 codex 共用 auth.json 时，登出/刷新 token 会影响原版 CLI |
| bundle id 迁移后历史数据丢失 | 中 | `com.dimillian.codexmonitor` → `com.sakrylle.studio` 后已有 settings.json/workspaces.json 不自动迁移（首次启动空白配置）；这是 fork 全新产品，可接受，但需要启动迁移检测逻辑 |
| `codex app-server` 协议兼容性 | 中 | Sakrylle CLI 是否实现相同 JSON-RPC 协议尚不确定（U1）；协议不兼容则需大量 Rust 侧适配 |
| localStorage 迁移失败 | 中 | `codexmonitor.*` → `sakrylle-monitor.*` 需一次性迁移逻辑；若迁移逻辑有 bug，用户 thread 历史会丢失 |
| 认证 UI 绑定 OpenAI | 低 | 若 Sakrylle CLI 保持 API key only（`requires_openai_auth=false`），现有登录 UI 不会触发，无问题；仅当需要 OIDC browser login 时才需改 `codex_login_core` |

---

## 8. 后续问题

1. **Sakrylle CLI fork 进度**：是否已实现 `codex app-server` 兼容的 JSON-RPC 子协议？这是 Studio 集成的前置条件（U1）。
2. **remote backend daemon**（`codex_monitor_daemon.rs`）是否纳入 Sakrylle Studio 首发范围（U6）？
3. **iOS / Android 移动端**是否有 Studio 计划（Tauri 2 支持 iOS，依赖 `tauri.ios.conf.json`）？
4. **崩溃监控**：是否计划接入 Sakrylle 自有 Sentry project，还是直接禁用（仅删除 DSN，`enabled=false`）？
5. **用量统计 UI**（sessions JSONL 扫描）依赖 CLI 产生的文件格式，Sakrylle CLI 是否保持同样的 JSONL schema（U5）？
6. **Monet Purple 主题**优先级：首发是否要求完整品牌主题，还是允许先发默认暗色主题（降低改造工作量）？
7. **OIDC 登录时序**：Studio 的 OIDC 接入应在 Sakrylle API OIDC 层（`03` 文档）完成后进行，还是可以先以 API Key 模式首发？
