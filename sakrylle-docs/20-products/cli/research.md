---
title: 10 · Sakrylle CLI 上游（OpenAI Codex CLI）现状研究报告
status: local
scope: product
last_verified: 2026-06-06
---

# 10 · Sakrylle CLI 上游（OpenAI Codex CLI）现状研究报告

> 规划文档（planning only，只读调研）。**不含**任何改动生产配置或业务代码的指令。所有 path:line 均经直接读取源码确认。
> 兄弟文档：见 `03-sakrylle-api-oidc-architecture.md`（IdP 端点）、`05-configuration-isolation-standard.md`（配置隔离规范）、`11-sakrylle-cli-development-plan.md`（改造计划）。

---

## 1. 调研范围

- 上游仓库：`openai/codex`，本地路径 `/Volumes/APFS_HD/Documents/Github/codex/`。
- 覆盖：技术栈与构建体系、入口文件、配置目录（`~/.codex`）与 `config.toml` 结构、环境变量全集、三种认证机制（Browser OAuth / Device Code / API Key）、自定义 endpoint 配置方法、缓存与日志路径、品牌标识出现位置（全部 file:line）。

---

## 2. 关键结论

1. **Node.js 薄封装 + Rust 核心**：`codex-cli/bin/codex.js` 仅做平台检测后 spawn 对应平台 Rust 二进制；所有逻辑（TUI、认证、配置、沙盒、MCP、Memories）全在 `codex-rs/`。
2. **单一环境变量可完整隔离**：所有路径集中在 `CODEX_HOME`（默认 `~/.codex`，`utils/home-dir/src/lib.rs:14`）—— 设 `CODEX_HOME=$HOME/.sakrylle-cli` 即可与上游零冲突。
3. **wire_api="chat" 已被硬性移除**：`model-provider-info/src/lib.rs:46` 中 `CHAT_WIRE_API_REMOVED_ERROR`，反序列化时直接报错。Codex 仅支持 **Responses API**（`POST /v1/responses`）。sub2api 已实现 `POST /v1/responses`（`backend/internal/handler/gateway_handler_responses.go`，路由注册 `backend/internal/server/routes/gateway.go:91-105,187-198`），包含 Anthropic 平台支持及 `/backend-api/codex/responses` 别名 —— **此 blocker 已解除，Codex 可直接对接**。
4. **requires_openai_auth=false 完全跳过登录 UI**：`tui/src/lib.rs:1888-1889` 中若 `!config.model_provider.requires_openai_auth` 则直接返回 `LoginStatus::NotAuthenticated`，不进入 ChatGPT onboarding 流程。API key 模式零改动即可用。
5. **Device Code Flow issuer 硬编码 `https://auth.openai.com`**：`login/src/server.rs:54` 中 `DEFAULT_ISSUER`，如需对接 Sakrylle OIDC 需要 fork 修改 `ServerOptions.issuer`。Codex 构造的端点路径（`{issuer}/api/accounts/deviceauth/usercode`，`device_code_auth.rs:67`）与 sub2api RFC 8628 端点（`POST /oauth/device/code`，`oauth_device.go:54`）**路径格式不兼容**，fork 时需同步修改。
6. **品牌标识分布广泛**：至少 12 处硬编码 "OpenAI Codex" / "OpenAI's command-line coding agent" / `codex_cli_rs` originator 等，需逐一替换。
7. **`sakrylle-cli` client 已在 sub2api migration 148 种入**：`client_id='sakrylle-cli'`，公共 client，`device_flow_enabled=true`，scopes 含 `responses:create`（`backend/migrations/148_oauth_v2_sakrylle_seed.sql:45`）。

---

## 3. 相关文件路径（path:line）

| 关注点 | 路径:行 |
|---|---|
| Node.js 入口，平台检测 + spawn | `codex-cli/bin/codex.js:1-70` |
| npm 包名 `@openai/codex`，bin `codex` | `codex-cli/package.json:2,7` |
| Rust main，`bin_name="codex"`，子命令列表 | `codex-rs/cli/src/main.rs:91-130` |
| `MultitoolCli` clap struct | `codex-rs/cli/src/main.rs:102` |
| `find_codex_home()`，`CODEX_HOME` 解析，默认 `~/.codex` | `codex-rs/utils/home-dir/src/lib.rs:13-17` |
| `.codex` 硬编码目录名 | `codex-rs/utils/home-dir/src/lib.rs:59` |
| `ConfigToml` 结构体（config.toml schema） | `codex-rs/config/src/config_toml.rs:136-190` |
| `model_provider`、`model_providers` 字段 | `codex-rs/config/src/config_toml.rs:143-146` |
| 系统级配置路径 `/etc/codex/config.toml` | `codex-rs/config/src/loader/mod.rs:52` |
| `ModelProviderInfo` struct（base_url/env_key/requires_openai_auth/wire_api） | `codex-rs/model-provider-info/src/lib.rs:82-120` |
| `wire_api="chat"` 已移除错误常量 | `codex-rs/model-provider-info/src/lib.rs:46` |
| `WireApi` enum（仅 `Responses`） | `codex-rs/model-provider-info/src/lib.rs:51-80` |
| `create_openai_provider()`，`requires_openai_auth=true` | `codex-rs/model-provider-info/src/lib.rs:319-353` |
| `OPENAI_PROVIDER_NAME="OpenAI"` | `codex-rs/model-provider-info/src/lib.rs:35` |
| `OPENAI_PROVIDER_ID="openai"`（保留 ID，不可复用） | `codex-rs/model-provider-info/src/lib.rs:36` |
| `OPENAI_API_KEY_ENV_VAR` / `CODEX_API_KEY_ENV_VAR` / `CODEX_ACCESS_TOKEN_ENV_VAR` | `codex-rs/login/src/auth/manager.rs:467-469` |
| `AuthDotJson` struct，`OPENAI_API_KEY` 存储 | `codex-rs/login/src/auth/storage.rs:31-48` |
| `get_auth_file()`，auth.json 路径 | `codex-rs/login/src/auth/storage.rs:84-85` |
| `DEFAULT_ISSUER="https://auth.openai.com"` | `codex-rs/login/src/server.rs:54` |
| `DEFAULT_PORT=1455`，`FALLBACK_PORT=1457` | `codex-rs/login/src/server.rs:55,57` |
| `ServerOptions` 结构（issuer、client_id 字段可覆盖） | `codex-rs/login/src/server.rs:64-89` |
| Device Code：`request_user_code()` 端点 `{issuer}/api/accounts/deviceauth/usercode` | `codex-rs/login/src/device_code_auth.rs:67` |
| Device Code：`request_device_code()` 入口 | `codex-rs/login/src/device_code_auth.rs:159` |
| `DEFAULT_ORIGINATOR="codex_cli_rs"` | `codex-rs/login/src/auth/default_client.rs:36` |
| `CODEX_INTERNAL_ORIGINATOR_OVERRIDE_ENV_VAR` | `codex-rs/login/src/auth/default_client.rs:37` |
| `requires_openai_auth=false` 跳过登录 UI | `codex-rs/tui/src/lib.rs:1888-1889` |
| `LoginStatus::NotAuthenticated` 分支 | `codex-rs/tui/src/lib.rs:1409` |
| `SQLite home env CODEX_SQLITE_HOME` | `codex-rs/state/src/lib.rs:79` |
| SQLite 文件名常量（state_5 / logs_2 / goals_1 / memories_1） | `codex-rs/state/src/lib.rs:81-84` |
| `HISTORY_FILENAME="history.jsonl"` | `codex-rs/message-history/src/lib.rs:45` |
| app-server socket 路径常量 | `codex-rs/app-server-transport/src/transport/mod.rs:46-54` |
| PID 文件、lock 文件常量 | `codex-rs/app-server-daemon/src/lib.rs:30-32` |
| `responses-api-proxy` 默认上游 `https://api.openai.com/v1/responses` | `codex-rs/responses-api-proxy/src/lib.rs:53` |
| sub2api `POST /v1/responses` 路由注册 | `backend/internal/server/routes/gateway.go:91-105,187-198` |
| sub2api Responses handler | `backend/internal/handler/gateway_handler_responses.go` |
| sub2api Device Code 路由 | `backend/internal/server/routes/oauth_device.go:54` |
| sub2api `sakrylle-cli` client seed | `backend/migrations/148_oauth_v2_sakrylle_seed.sql:26-57` |

### 品牌标识出现位置（全部 file:line）

| 品牌字符串 | 路径:行 |
|---|---|
| `"Welcome to Codex"` + `", OpenAI's command-line coding agent"` | `codex-rs/tui/src/onboarding/welcome.rs:97-98` |
| `Span::from("OpenAI Codex").bold()` — 会话标题 | `codex-rs/tui/src/history_cell/session.rs:343` |
| `format!("OpenAI Codex (v{})", self.version)` | `codex-rs/tui/src/history_cell/session.rs:410` |
| `Span::from("OpenAI Codex").bold()` — status card | `codex-rs/tui/src/status/card.rs:713` |
| `"Sign in with ChatGPT to use Codex as part of your paid plan"` | `codex-rs/tui/src/onboarding/auth.rs:392` |
| `"API key configured (run codex login to use ChatGPT)"` | `codex-rs/tui/src/status/card.rs:731` |
| `DEFAULT_ORIGINATOR="codex_cli_rs"` （User-Agent / originator header） | `codex-rs/login/src/auth/default_client.rs:36` |
| `Implementation::new("codex-mcp-server", ...).with_title("Codex")` | `codex-rs/mcp-server/src/message_processor.rs:221` |
| `MEMORIES_DOC_URL="https://developers.openai.com/codex/memories"` | `codex-rs/tui/src/chatwidget.rs:201` |
| `"https://developers.openai.com/codex/security"` | `codex-rs/tui/src/onboarding/auth.rs:557` |
| `bin_name="codex"` （clap help 输出） | `codex-rs/cli/src/main.rs:99` |
| npm 包 `@openai/codex`，bin `codex` | `codex-cli/package.json:2,7` |

---

## 4. 技术栈与构建体系

### 4.1 整体架构

```
codex-cli/           <- Node.js ESM 薄封装（无自己逻辑）
  bin/codex.js       <- 入口：平台检测 → spawn Rust 二进制
  package.json       <- name="@openai/codex"，bin.codex=bin/codex.js

codex-rs/            <- Rust Cargo workspace（edition 2024）
  cli/               <- 可执行 crate，bin name="codex"，src/main.rs
  tui/               <- ratatui TUI 主循环
  login/             <- 认证（Browser OAuth / Device Code / API Key）
  config/            <- 配置加载（TOML），codex-config crate
  utils/home-dir/    <- find_codex_home()
  model-provider-info/ <- ModelProviderInfo，WireApi
  state/             <- SQLite（sqlx + sqlite-bundled）
  app-server/        <- 后台 app-server daemon
  app-server-transport/ <- UNIX socket 传输层
  mcp-server/        <- MCP server（rmcp 1.7）
  responses-api-proxy/ <- Responses API 适配器工具
  sandboxing/        <- landlock/bwrap（Linux）、seatbelt（macOS）、Windows Sandbox
  ...（40+ crates）
```

### 4.2 构建方式

- 主构建：Cargo（pnpm workspace 管理 Node 侧）
- 可选：Bazel（`BUILD.bazel`、`MODULE.bazel` 就位）
- 交叉编译目标：
  - `x86_64-unknown-linux-musl`、`aarch64-unknown-linux-musl`
  - `x86_64-apple-darwin`、`aarch64-apple-darwin`
  - `x86_64-pc-windows-msvc`、`aarch64-pc-windows-msvc`

### 4.3 主要依赖

| 用途 | 库 |
|---|---|
| TUI | ratatui |
| HTTP | reqwest |
| 配置 | serde / toml |
| 数据库 | sqlx（sqlite-bundled feature） |
| CLI 解析 | clap |
| MCP | rmcp 1.7 |
| OAuth PKCE | 自建（`login/src/server.rs`、`login/src/pkce.rs`） |
| 沙盒 | landlock、bwrap、seatbelt |

---

## 5. 入口文件

| 层次 | 文件 | 作用 |
|---|---|---|
| Node.js | `codex-cli/bin/codex.js:1` | 检测平台三元组，spawn 对应平台 Rust 二进制 |
| Rust main | `codex-rs/cli/src/main.rs:91` | `MultitoolCli`（clap）解析，dispatch 到 TUI / exec / login / mcp-server / app-server 等子命令 |
| TUI | `codex-rs/tui/src/lib.rs:run_tui()` | TUI 主循环 |
| app-server | `codex-rs/app-server/src/lib.rs` | 后台 daemon 模式入口 |

**主要子命令**（`codex-rs/cli/src/main.rs:119-130`）：

- `exec`（alias `e`）—— 非交互式执行
- `review` —— 代码审查
- `login` —— 管理登录（子命令：chatgpt / device-code / api-key / access-token / status / logout）
- `mcp` —— MCP server/client 管理
- `plugin` —— 插件管理
- `responses-api-proxy` —— Responses API 适配代理
- `app` —— macOS/Windows 桌面 app 集成

---

## 6. 配置目录 `~/.codex` 与 `config.toml` 结构

### 6.1 目录布局

```
~/.codex/                               <- CODEX_HOME（utils/home-dir/src/lib.rs:14）
  config.toml                           <- 用户全局配置（ConfigToml）
  auth.json                             <- ChatGPT OAuth token / API key（AuthDotJson）
  .credentials.json                     <- MCP OAuth fallback（rmcp-client/src/oauth.rs:371）
  history.jsonl                         <- 会话历史（message-history/src/lib.rs:45）
  state_5.sqlite                        <- 主状态 DB（state/src/lib.rs:84）
  logs_2.sqlite                         <- 使用日志 DB（state/src/lib.rs:81）
  goals_1.sqlite                        <- goals DB（state/src/lib.rs:82）
  memories_1.sqlite                     <- memories DB（state/src/lib.rs:83）
  log/                                  <- 日志目录（可 config.toml log_dir 覆盖）
    codex-tui.log
  sessions/                             <- 会话 rollout 束（rollout/src/lib.rs:24）
  archived_sessions/                    <- 归档会话（rollout/src/lib.rs:25）
  memories/                             <- 持久记忆（memories/read/src/lib.rs:14）
  app-server-control/
    app-server-control.sock             <- Unix domain socket（transport/mod.rs:46-47）
    app-server-startup.lock
  app-server-daemon/
    app-server.pid                      <- PID 文件（app-server-daemon/src/lib.rs:30）
    app-server-updater.pid              <- 更新 PID（src/lib.rs:31）
    daemon.lock                         <- 操作锁（src/lib.rs:32）
  plugins/
    cache/
    data/
  rules/                                <- 权限规则
  shell_snapshots/
  proxy/                                <- 网络代理 MITM CA
  .tmp/                                 <- 临时文件
  installation_id
  cloud-requirements-cache.json
```

**系统级配置**（只读，优先级低于用户配置）：
- Unix：`/etc/codex/config.toml`（`config/src/loader/mod.rs:52`）
- Windows：`%ProgramData%\OpenAI\Codex\`（`loader/mod.rs:643`）

### 6.2 `config.toml` 关键字段

来源：`codex-rs/config/src/config_toml.rs:136`

```toml
# 模型选择
model = "claude-sonnet-4-6"
model_provider = "sakrylle"          # 自定义 provider ID（不能用保留 ID "openai"）
model_context_window = 200000

# 沙盒
approval_policy = "suggest"
sandbox_mode = "workspace-write"

# 自定义 provider（推荐方式）
[model_providers.sakrylle]
name = "Sakrylle API"
base_url = "https://api.sakrylle.com/v1"
env_key = "SAKRYLLE_API_KEY"
env_key_instructions = "在 Sakrylle API 控制台创建 API Key 后设置此变量"
requires_openai_auth = false
wire_api = "responses"              # 唯一合法值；"chat" 已在 model-provider-info/src/lib.rs:46 硬性移除

# 日志目录（可独立覆盖）
log_dir = "~/.sakrylle-cli/log"

# SQLite 目录（可独立覆盖，对应 CODEX_SQLITE_HOME）
sqlite_home = "~/.sakrylle-cli"
```

**保留 provider ID**（`model-provider-info/src/lib.rs:36-48`，不可用于自定义 provider）：
`openai`、`amazon-bedrock`、`ollama`、`lmstudio`

---

## 7. 环境变量全集

| 变量 | 说明 | 来源 path:line |
|---|---|---|
| `CODEX_HOME` | 配置/数据根目录，**单一隔离抓手**，默认 `~/.codex` | `utils/home-dir/src/lib.rs:14` |
| `CODEX_SQLITE_HOME` | SQLite 数据库目录（默认 `$CODEX_HOME`） | `state/src/lib.rs:79` |
| `OPENAI_API_KEY` | API key 主 env（优先级第一） | `login/src/auth/manager.rs:467` |
| `CODEX_API_KEY` | API key 备用 env（优先级第二） | `login/src/auth/manager.rs:468` |
| `CODEX_ACCESS_TOKEN` | ChatGPT OAuth access token（agent/headless 用） | `login/src/auth/manager.rs:469` |
| `OPENAI_ORGANIZATION` | 注入 `OpenAI-Organization` header | `model-provider-info/src/lib.rs:338` |
| `OPENAI_PROJECT` | 注入 `OpenAI-Project` header | `model-provider-info/src/lib.rs:341` |
| `CODEX_REFRESH_TOKEN_URL_OVERRIDE` | 覆盖 OAuth token refresh endpoint | `login/` |
| `CODEX_REVOKE_TOKEN_URL_OVERRIDE` | 覆盖 OAuth token revoke endpoint | `login/` |
| `CODEX_INTERNAL_ORIGINATOR_OVERRIDE` | 覆盖 originator / User-Agent 前缀（初始化时一次生效） | `login/src/auth/default_client.rs:37` |
| `CODEX_CA_CERTIFICATE` | 自定义 CA 证书 PEM 文件路径 | `login/` |
| `SSL_CERT_FILE` | 同上（兼容 curl 惯例） | `login/` |
| `CODEX_SANDBOX` | sandbox 模式标志 | `sandboxing/` |
| `CODEX_SANDBOX_NETWORK_DISABLED` | 禁用 sandbox 内网络 | `sandboxing/` |
| `CODEX_THREAD_ID` | 线程 ID 注入子进程 | `protocol/` |
| `CODEX_ROLLOUT_TRACE_ROOT` | trace 根目录覆盖 | `rollout-trace/` |
| `CODEX_NETWORK_PROXY_ACTIVE` | 网络代理激活标志 | `network-proxy/` |
| `CODEX_NETWORK_ALLOW_LOCAL_BINDING` | 允许本地绑定 | `network-proxy/` |
| `CODEX_OSS_PORT` | OSS provider 端口（实验性） | `model-provider-info/` |
| `CODEX_OSS_BASE_URL` | OSS provider base URL（实验性） | `model-provider-info/` |
| `CODEX_MANAGED_CONFIG_SYSTEM_PATH` | 系统托管配置路径覆盖 | `config/loader/` |

**fork 新增变量**（Sakrylle CLI 改造后生效，见 `11-sakrylle-cli-development-plan.md`）：

| 变量 | 说明 | 优先级 |
|---|---|---|
| `SAKRYLLE_CLI_HOME` | CLI 根目录，覆盖 `CODEX_HOME`，默认 `~/.sakrylle-cli` | 高于 `CODEX_HOME` |
| `SAKRYLLE_API_KEY` | Sakrylle API key，优先于 `CODEX_API_KEY` / `OPENAI_API_KEY` | 高于两者 |

---

## 8. 三种认证机制

### 8.1 ChatGPT Browser OAuth（默认，requires_openai_auth=true）

流程（`login/src/server.rs`）：

1. 本地启动 HTTP 服务器监听 `localhost:1455`（备用 1457，`server.rs:55,57`）
2. 生成 PKCE code_challenge/verifier
3. 浏览器重定向到 `DEFAULT_ISSUER/oauth/authorize`
4. 回调 `localhost:1455/callback`，携带 `code`
5. 用 code + verifier 换取 access_token + refresh_token
6. 写入 `$CODEX_HOME/auth.json`（`AuthDotJson`，`storage.rs:84-85`）

**issuer 硬编码**：`"https://auth.openai.com"`（`server.rs:54`），无法通过环境变量覆盖，需 fork 修改 `ServerOptions.issuer`（`server.rs:64-89`）。

### 8.2 Device Code Flow（无头环境）

流程（`login/src/device_code_auth.rs:159`）：

1. `GET {issuer}/api/accounts/deviceauth/usercode`（`device_code_auth.rs:67`）—— 获取用户码
2. 显示 `{issuer}/codex/device` URL + 用户码，提示用户在浏览器输入
3. 轮询 `POST {issuer}/api/accounts/deviceauth/token`（`device_code_auth.rs:106`）直至授权完成
4. 换取 token，写 auth.json

**端点路径与 sub2api 不兼容**：上游构造的路径是 `{issuer}/api/accounts/deviceauth/usercode`，而 sub2api RFC 8628 路由是 `POST /oauth/device/code`（`oauth_device.go:54`）。**fork Codex 时必须修改 `device_code_auth.rs:67,106` 的端点路径**，对应 sub2api 实现。

### 8.3 API Key 模式（最简单，推荐 Sakrylle CLI 初期使用）

优先级（`login/src/auth/manager.rs:467-483`）：

1. `OPENAI_API_KEY` 环境变量
2. `CODEX_API_KEY` 环境变量
3. `CODEX_ACCESS_TOKEN` 环境变量
4. `$CODEX_HOME/auth.json` 中的 `OPENAI_API_KEY` 字段

设置 `requires_openai_auth = false` 后（`tui/src/lib.rs:1888`），TUI 完全跳过 onboarding 登录页，直接以 API key 模式工作。

---

## 9. 自定义 Endpoint 配置

### 9.1 推荐方式：自定义 model_providers

在 `~/.sakrylle-cli/config.toml` 中：

```toml
model_provider = "sakrylle"

[model_providers.sakrylle]
name = "Sakrylle API"
base_url = "https://api.sakrylle.com/v1"
env_key = "SAKRYLLE_API_KEY"
env_key_instructions = "在 Sakrylle API 控制台创建 API Key 后设置此变量"
requires_openai_auth = false
wire_api = "responses"
```

注意：`base_url` 指向后，Codex 会向 `https://api.sakrylle.com/v1/responses` 发送 `POST` 请求（**Responses API 协议**）。sub2api 已在 `gateway.go:91` 注册该路由，**直接可用**。

### 9.2 wire_api 限制（关键 blocker — 已解除）

`model-provider-info/src/lib.rs:46` 中：

```rust
const CHAT_WIRE_API_REMOVED_ERROR: &str =
    "`wire_api = \"chat\"` is no longer supported...";
```

`WireApi` enum（`src/lib.rs:51-57`）只有一个成员 `Responses`，对应 `POST /v1/responses`。**Chat Completions（`/v1/chat/completions`）不再被 Codex 直接支持**。

sub2api 已实现 `POST /v1/responses`（`gateway_handler_responses.go`，Anthropic 平台支持），此 blocker **已解除**。config.toml 配置 `wire_api = "responses"` + `base_url = "https://api.sakrylle.com/v1"` 即可直接对接。

### 9.3 简单覆盖内建 openai provider（不推荐）

```toml
openai_base_url = "https://api.sakrylle.com/v1"
```

问题：内建 openai provider 的 `requires_openai_auth=true`（`model-provider-info/src/lib.rs:351`），会触发 ChatGPT OAuth 登录流程，无法用于 Sakrylle。

---

## 10. 缓存、日志、数据路径汇总

| 路径 | 说明 | 可否覆盖 |
|---|---|---|
| `$CODEX_HOME/config.toml` | 用户配置 | 通过 `CODEX_HOME`（fork 改为 `SAKRYLLE_CLI_HOME`） |
| `$CODEX_HOME/auth.json` | OAuth token / API key | 同上 |
| `$CODEX_HOME/.credentials.json` | MCP OAuth fallback | 同上 |
| `$CODEX_HOME/history.jsonl` | 会话历史 | 同上 |
| `$CODEX_HOME/state_5.sqlite` | 主状态 DB | 通过 `CODEX_SQLITE_HOME` 或 `CODEX_HOME` |
| `$CODEX_HOME/logs_2.sqlite` | 使用日志 DB | 同上 |
| `$CODEX_HOME/goals_1.sqlite` | Goals DB | 同上 |
| `$CODEX_HOME/memories_1.sqlite` | Memories DB | 同上 |
| `$CODEX_HOME/log/codex-tui.log` | TUI 文本日志 | `config.toml log_dir` |
| `$CODEX_HOME/sessions/` | 会话 rollout 束 | 通过 `CODEX_HOME` |
| `$CODEX_HOME/archived_sessions/` | 归档会话 | 同上 |
| `$CODEX_HOME/memories/` | 持久记忆 | 同上 |
| `$CODEX_HOME/app-server-control/app-server-control.sock` | Unix socket | 同上 |
| `$CODEX_HOME/app-server-daemon/app-server.pid` | PID | 同上 |
| `$CODEX_HOME/plugins/` | 插件 | 同上 |
| `/etc/codex/config.toml` | 系统配置（Unix） | `CODEX_MANAGED_CONFIG_SYSTEM_PATH` |

---

## 11. 差距分析

| # | 差距 | 严重程度 |
|---|---|---|
| 1 | ~~Codex 仅支持 Responses API，sub2api 无此端点~~ → **已解除**：sub2api 已实现 `POST /v1/responses`（`gateway.go:91-105,187-198`；`gateway_handler_responses.go`），Anthropic 平台支持 + `/backend-api/codex/responses` 别名均就位 | ~~CRITICAL~~ → **已解决** |
| 2 | Browser OAuth / Device Code issuer 硬编码 `auth.openai.com`，无法直接对接 Sakrylle OIDC（fork 需修改 `server.rs:54`、`device_code_auth.rs:67,106`） | HIGH（Phase 3） |
| 3 | `CODEX_HOME` 默认 `~/.codex`，不隔离会与上游竞争 auth.json / socket | HIGH（Phase 0） |
| 4 | 品牌字符串 "OpenAI Codex" 等 12 处硬编码 | MEDIUM（Phase 1） |
| 5 | `DEFAULT_ORIGINATOR="codex_cli_rs"` 泄露上游身份 | MEDIUM（Phase 1） |
| 6 | npm 包名 `@openai/codex`，bin `codex` 与上游冲突 | MEDIUM（Phase 1） |
| 7 | Device Code 端点路径与 sub2api RFC 8628 路由不兼容（`/api/accounts/deviceauth/usercode` vs `/oauth/device/code`） | MEDIUM（Phase 3，如需 Device Flow） |
| 8 | Memories / Web Search / Plugin Marketplace 依赖 `chatgpt.com/backend-api/codex` 专有端点 | LOW（功能降级，非阻断） |
| 9 | analytics/telemetry 上报 OpenAI 内部端点 | LOW（Phase 2） |

---

## 12. 风险

1. **`~/.codex` 路径竞争**：用户若同时装有上游 codex，双方争用同一 auth.json / socket，可能互相覆盖登录状态。**必须在 fork 第一步设置 `SAKRYLLE_CLI_HOME`**。
2. **SQLite 文件名版本迭代**：`state_5`、`logs_2` 已有版本尾缀，upstream 升级时可能变，需要迁移注意。
3. **Device Code Flow 的端点路径格式差异**：sub2api 实现为 `POST /oauth/device/code`（RFC 8628 标准格式），而 Codex 上游 `device_code_auth.rs:67` 构造的路径是 `{issuer}/api/accounts/deviceauth/usercode`（ChatGPT 专有格式）。两者**路径不兼容**，fork Codex 后需修改对应端点路径。
4. **`CODEX_INTERNAL_ORIGINATOR_OVERRIDE` 仅初始化时一次生效**：不能在运行中动态修改。
5. **sub2api `requireGroupAnthropic` 中间件**（`gateway.go:187`）：`POST /v1/responses` 需要 Anthropic 平台的 group，纯 OpenAI 平台的 API key 会被拒绝。CLI 接入文档须说明这一限制，用户需使用 Claude 系 group 的 key。

---

## 13. 后续问题

1. sub2api Device Grant 实现（`oauth_device_service.go`）的端点路径格式（`/oauth/device/code`）是否可以在 fork Codex 的 `device_code_auth.rs:67,106` 中兼容对接？（见 `11` Phase 3 详细计划）
2. Codex chatgpt 相关功能（Memories、remote compaction、agent identity JWT）在 `requires_openai_auth=false` 时是否完全静默跳过，还是会有 UI 异常？（「不确定」，需实测）
3. analytics/sentry 的 opt-out 机制是否仅靠 `analytics.enabled=false` 即可关闭，还是需要构建时 feature-flag？（「不确定」，需查 Cargo.toml feature 配置）
4. fork 后 Codex 版本升级策略：SQLite 文件名（`state_5`、`logs_2`）有版本尾缀迭代历史，rebase 时需注意迁移。
