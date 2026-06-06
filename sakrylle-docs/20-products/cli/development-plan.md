---
title: 11 · Sakrylle CLI 改造开发计划
status: local
scope: product
last_verified: 2026-06-06
---

# 11 · Sakrylle CLI 改造开发计划

> 规划文档（planning only）。Sakrylle API（sub2api fork）与 Sakrylle Image **已上线生产**；Sakrylle CLI 本身是**全新开发**的客户端 fork，本地改造不触生产。**唯一触及生产侧的环节**是在 sub2api（OIDC provider）注册 / 校验 `sakrylle-cli` 这个 OAuth client —— 凡涉及生产 `oauth_clients` / `settings` / 密钥的步骤一律标注「需审批」。
> 兄弟文档：
> - `10-sakrylle-cli-research.md` —— 上游 Codex CLI 现状（所有 file:line 引自该报告）
> - `03-sakrylle-api-oidc-architecture.md` —— OIDC 基座（loopback redirect / device flow / PKCE / id_token）
> - `05-configuration-isolation-standard.md` —— `~/.sakrylle-cli`、`SAKRYLLE_*` 变量、与上游零冲突
> - `21-sakrylle-studio-development-plan.md` —— Sakrylle Studio（CodexMonitor fork）对接点

---

## 1. 目标与范围

把上游 **OpenAI Codex CLI**（Rust 核心 `codex-rs/` + Node 包装 `codex-cli/`，本地路径 `/Volumes/APFS_HD/Documents/Github/codex/`）fork 成 **Sakrylle CLI**，使其：

1. **默认指向 Sakrylle API**（`api.sakrylle.com` `/v1`，Responses API 协议），而非 OpenAI。
2. **配置与数据完全隔离**到 `~/.sakrylle-cli/`（`SAKRYLLE_CLI_HOME`），与上游 codex（`~/.codex` / `CODEX_*`）**同机零冲突并行**（见 `05`）。
3. **品牌彻底 Sakrylle 化**：二进制名 `sakrylle`，**提供短别名 `skl`**（已确认 2026-06-03），去除全部 "OpenAI Codex" 字样。
4. **核心需求 —— `sakrylle login` 一键 OIDC 登录**：自动拉起系统浏览器 → sub2api 同意页 → 用户点一次授权 → CLI 本地 loopback 回调自动捕获 authorization code → PKCE(S256) 兑换 token → 落盘 `~/.sakrylle-cli/auth.json`，**全程零复制粘贴**。无浏览器环境降级到 Device Flow。

**不做（YAGNI / 超范围）**：不改 sub2api 核心网关计费逻辑；不实现 OIDC 服务端（那是 `03` 的工作，本 CLI 是 RP 消费方）；不实现 Memories / Web Search / Plugin Marketplace 等依赖 `chatgpt.com/backend-api` 的专有功能（降级即可，见 `10` 差距 #8）。

### 关键依赖关系

| 本 CLI 依赖 | 来源 | 状态 |
|---|---|---|
| `POST /v1/responses` 端点 | sub2api（`gateway.go:91-105,187-198`、`gateway_handler_responses.go`） | **已就位**（`10` 结论 3）—— blocker 已解除 |
| `sakrylle-cli` OAuth client seed | sub2api migration 148（`148_oauth_v2_sakrylle_seed.sql:26-57`） | **已种入**（`10` 结论 7），但 redirect_uris / scope 需按本文 §6 核对，**可能需补 loopback redirect_uri → 需审批** |
| OIDC 一键登录所需的 `id_token` / discovery / JWKS | sub2api OIDC 基座（`03` Phase 1：G1-G5） | **✅ 已就绪（2026-06-04）**：provider 在含 `openid` scope 时签发可验签 id_token（RS256/ES256 按 client 选择）、`/v1/me` 返回 `sub`、discovery/JWKS 端点均在。CLI 可直接做严格 OIDC 一键登录（验 id_token）；若选择先行也可走纯 OAuth2 |
| Device Flow 端点 `POST /oauth/device/code`（RFC 8628） | sub2api（`oauth_device.go:54`） | **已就位**，但与上游 Codex 端点路径**不兼容**（`10` 差距 #7），fork 需改路径 |

---

## 2. 与上游 Codex 的对应关系（命名 / 路径速查）

| 维度 | 上游 Codex | Sakrylle CLI | 来源 |
|---|---|---|---|
| 二进制名 | `codex` | `sakrylle`（短别名 `skl`，已确认 2026-06-03） | `cli/src/main.rs:99`、`package.json:7` |
| npm 包 | `@openai/codex` | `@sakrylle/cli`（待定，见 §不确定项） | `codex-cli/package.json:2` |
| 配置/数据根 | `CODEX_HOME` → `~/.codex` | `SAKRYLLE_CLI_HOME` → `~/.sakrylle-cli` | `utils/home-dir/src/lib.rs:13-17,59`；`05` §4 |
| 系统配置 | `/etc/codex/config.toml` | `/etc/sakrylle/config.toml` | `config/src/loader/mod.rs:52`；`05` §7 |
| API key env | `OPENAI_API_KEY` / `CODEX_API_KEY` | `SAKRYLLE_API_KEY`（优先）+ 上游作 fallback | `login/src/auth/manager.rs:467-468`；`05` §5 |
| 上游基址 | `https://api.openai.com/v1` | `https://api.sakrylle.com/v1`（`SAKRYLLE_API_BASE_URL` 覆盖） | `10` §9；`05` §5 |
| OAuth issuer | `https://auth.openai.com`（硬编码） | `https://sub.sakrylle.com`（单一 issuer，已确认 2026-06-03；`SAKRYLLE_OIDC_ISSUER` 仅为非生产环境覆盖） | `login/src/server.rs:54`；`03` §6 |
| OAuth client_id | ChatGPT 内建 | `sakrylle-cli`（public client，已确认 2026-06-03） | `03` §9；`148_oauth_v2_sakrylle_seed.sql` |
| originator / UA | `codex_cli_rs` | `sakrylle_cli_rs`（待定） | `login/src/auth/default_client.rs:36` |
| daemon 名 | `codex-monitor-daemon` 等价 | `sakrylle-cli-daemon` | `05` §8 |

---

## 3. Phase 总览

| Phase | 主题 | 触生产？ | 依赖 | 串/并行 |
|---|---|---|---|---|
| Phase 0 | 调研与保护（fork、护栏、锁定命名） | 否 | — | 串行前置 |
| Phase 1 | 最小可用集成（endpoint + 配置隔离骨架，API key 临时跑通） | 否 | Phase 0 | 串行 |
| Phase 2 | 品牌与命令名 | 否 | Phase 1 | 与 Phase 3 部分并行 |
| Phase 3 | **OIDC 一键登录（loopback PKCE 主流程 + device flow 降级 + 刷新/登出）** | client 注册需审批 | Phase 1；OIDC 基座（`03` Phase 1） | 与 Phase 2 部分并行 |
| Phase 4 | 测试 / 发布 / 回滚 | 发布需审批 | Phase 1-3 | 串行收尾 |

---

## Phase 0 · 调研与保护（串行前置）

**目标**：建立 fork、锁定命名与未决项、确保不污染上游 `~/.codex`，为后续改造拉好护栏。

**涉及文件/模块**：`codex-rs/utils/home-dir/`、`codex-rs/config/src/loader/`、`codex-rs/login/`、`backend/migrations/148_oauth_v2_sakrylle_seed.sql`（**只读复核**）。

**依赖项**：无。

**风险**：在隔离落地前误启动 fork 会写入 `~/.codex`，污染上游登录态（`05` R1，高）。Phase 0 期间**禁止在未设 `SAKRYLLE_CLI_HOME` 时运行 fork**。

- [ ] 建立 Sakrylle CLI fork 仓库与构建基线
    - 目标：从 `openai/codex` 拉出独立 fork，确认本机 Cargo + pnpm 可构建出二进制
    - 涉及文件：整个 `codex-rs/` workspace（edition 2024）、`codex-cli/`
    - 实施说明：fork → 本地 `cargo build` 跑通 `codex-rs/cli`；记录交叉编译目标（`10` §4.2：linux-musl / apple-darwin / windows-msvc 各 x86_64+aarch64）。**不改任何代码**，仅验证基线可编译。
    - 验收标准：能产出可运行的上游二进制（命名仍为 `codex`），`cargo build` 零错误

- [ ] 只读复核 `sakrylle-cli` client seed（**触生产侧只读**）
    - 目标：确认 sub2api 已注册的 `sakrylle-cli` client 的 `redirect_uris` / `allowed_scopes` / `pkce_required` / `device_flow_enabled`，与本文 §6 期望比对
    - 涉及文件：`backend/migrations/148_oauth_v2_sakrylle_seed.sql:26-57`（Read/Grep 只读）
    - 实施说明：核对 `10` 结论 7（`device_flow_enabled=true`、scopes 含 `responses:create`）与 `03` §9（scope `openid profile email models:read`、redirect_uri `http://127.0.0.1:<port>/callback`）。**记录差异**，尤其 loopback redirect_uri 是否已在白名单——若缺，列为 Phase 3 的「需审批」补 seed 项。
    - 验收标准：产出一张「seed 现状 vs §6 期望」差异表；不修改任何生产数据

- [ ] 锁定品牌命名（与 `05` §8 对齐）
    - 目标：冻结 二进制名 `sakrylle` / 短别名 `skl` / 配置根 `~/.sakrylle-cli` / daemon `sakrylle-cli-daemon` / originator `sakrylle_cli_rs` / npm 包名
    - 涉及文件：本文 §2 表
    - 实施说明：npm 包名（`@sakrylle/cli`）与 originator 字符串属「不确定」项（见文末），需与生态主理人确认；其余按 `05` 锁定。
    - 验收标准：命名表冻结写入本文 §2，无歧义

- [ ] 确认 codex 子目录是否全随 `CODEX_HOME`（变量遮蔽核查）
    - 目标：排除 `~/.codex` 残留硬编码漏改点（`05` Phase 0 同款核查）
    - 涉及文件：`codex-rs/utils/home-dir/src/lib.rs:13,59`、`state/src/lib.rs:79-84`、`rollout/src/lib.rs:24-25`、`memories/read/src/lib.rs:14`、`app-server-transport/src/transport/mod.rs:46-54`、`app-server-daemon/src/lib.rs:30-32`
    - 实施说明：grep `\.codex` / `find_codex_home` 全 workspace，确认 SQLite（`CODEX_SQLITE_HOME`，`state/src/lib.rs:79`）是否独立于 `CODEX_HOME`（`05` 后续问题，标「不确定」）
    - 验收标准：列出所有路径起点，确认改 `find_codex_home()` 一处 + `CODEX_SQLITE_HOME` 可整体迁移，或记录额外漏改点

---

## Phase 1 · 最小可用集成（串行，依赖 Phase 0）

**目标**：fork 用 **API key 模式**（最简单，`10` §8.3）跑通到 `api.sakrylle.com` 的真实请求；配置/数据隔离骨架（`SAKRYLLE_CLI_HOME`）就位。此阶段**不碰 OIDC**——先证明数据面通。

**涉及文件/模块**：`codex-rs/utils/home-dir/src/lib.rs`、`codex-rs/config/src/loader/mod.rs`、`codex-rs/login/src/auth/manager.rs`、`codex-rs/model-provider-info/src/lib.rs`、`config.toml` schema（`config/src/config_toml.rs:136-190`）。

**依赖项**：Phase 0；sub2api `POST /v1/responses`（已就位）。

**风险**：
- 未隔离即运行 → 污染上游 `~/.codex`（`05` R1）。本 Phase 第一项任务必须先落地隔离。
- `requires_openai_auth` 若仍为 `true`，TUI 会进 ChatGPT onboarding（`tui/src/lib.rs:1888-1889`）；自定义 provider 必须设 `false`。
- `wire_api` 只能是 `"responses"`（`"chat"` 已硬性移除，`model-provider-info/src/lib.rs:46`）。
- `requireGroupAnthropic` 中间件（`gateway.go:187`）：`/v1/responses` 需 Anthropic 平台 group 的 key（`10` 风险 5）——测试必须用 Claude 系 group 的 API key。

- [ ] 配置/数据根隔离：`find_codex_home()` 改读 `SAKRYLLE_CLI_HOME`
    - 目标：所有路径起点从 `~/.codex` 迁到 `~/.sakrylle-cli`，与上游零冲突（`05` Phase 1）
    - 涉及文件：`codex-rs/utils/home-dir/src/lib.rs:13-17,59`、`codex-rs/config/src/loader/mod.rs:52`、`codex-rs/state/src/lib.rs:79`（`CODEX_SQLITE_HOME`）
    - 实施说明：优先级 `SAKRYLLE_CLI_HOME` > `CODEX_HOME`（保留作 fallback 以平滑迁移，`05` §5 原则）> 默认 `~/.sakrylle-cli`；系统配置 `/etc/codex` → `/etc/sakrylle`；SQLite home 同步迁移。**遵循上游单根混放约定**（不强行 XDG 分离，`05` §4.2 CLI 例外）。
    - 验收标准：设 `SAKRYLLE_CLI_HOME` 后所有读写落在该目录；同机并行上游 codex 时，双方 `auth.json` / `sessions/` / socket / PID 互不可见（`05` 验收 2）

- [ ] API key env 注入：新增 `SAKRYLLE_API_KEY`
    - 目标：`SAKRYLLE_API_KEY` 优先于 `CODEX_API_KEY` / `OPENAI_API_KEY`，上游变量保留 fallback
    - 涉及文件：`codex-rs/login/src/auth/manager.rs:467-483`
    - 实施说明：在 env 优先级链最前插入 `SAKRYLLE_API_KEY`（`05` §5）；不删除上游变量（共存，零冲突）
    - 验收标准：仅设 `SAKRYLLE_API_KEY` 时 auth 链能取到 key；同机上游 codex 读 `OPENAI_API_KEY` 不受影响

- [ ] 默认 model provider 指向 Sakrylle（Responses API）
    - 目标：开箱默认 `base_url=https://api.sakrylle.com/v1`、`wire_api="responses"`、`requires_openai_auth=false`
    - 涉及文件：`codex-rs/model-provider-info/src/lib.rs:319-353`（参照 `create_openai_provider()` 新增 Sakrylle provider）、`config/src/config_toml.rs:143-146`
    - 实施说明：自定义 provider id 用 `sakrylle`（**不可**用保留 ID `openai`/`amazon-bedrock`/`ollama`/`lmstudio`，`10` §6.2）；`SAKRYLLE_API_BASE_URL` 可覆盖 `base_url`（`05` §5）。默认 `config.toml` 模板见 `10` §6.2 / §9.1。
    - 验收标准：全新 `~/.sakrylle-cli` 无 config.toml 时，CLI 默认即向 `api.sakrylle.com/v1/responses` 发请求，不进 ChatGPT onboarding

- [ ] 端到端冒烟（API key 临时跑通）
    - 目标：用 Claude 系 group 的真实 API key 完成一次非交互 `exec` 调用
    - 涉及文件：`codex-rs/cli/src/main.rs`（`exec` 子命令，`10` §5）
    - 实施说明：`SAKRYLLE_API_KEY=<key> sakrylle exec "..."`；key 必须属 Anthropic 平台 group（`gateway.go:187` `requireGroupAnthropic`）。验证 sub2api `usage_logs` 落了真实计费行（与 relay-pulse claude-kiro 同款「真实计费路径」校验，见项目 CLAUDE.md）。
    - 验收标准：请求 200、返回内容合理、sub2api `usage_logs` 有对应 `total_cost>0` 行

---

## Phase 2 · 品牌与命令名（可与 Phase 3 部分并行，依赖 Phase 1）

**目标**：去除全部上游品牌标识，二进制/包名/帮助文本/TUI/originator 全部 Sakrylle 化。

**涉及文件/模块**：`10` §3「品牌标识出现位置」12 处 + npm 包。

**依赖项**：Phase 1（隔离骨架，避免改名后仍写 `~/.codex`）。

**风险**：originator 覆盖（`CODEX_INTERNAL_ORIGINATOR_OVERRIDE`）**仅初始化时一次生效**（`10` 风险 4），改硬编码常量比 env 覆盖更可靠。

- [ ] 二进制名与 CLI 帮助文本
    - 目标：`bin_name`、clap help、子命令说明全改为 `sakrylle`（别名 `skl`）
    - 涉及文件：`codex-rs/cli/src/main.rs:91-130`（`bin_name="codex"` 在 `:99`，`MultitoolCli` 在 `:102`）
    - 实施说明：保留子命令结构（`exec`/`review`/`login`/`mcp`/`plugin`/`responses-api-proxy`/`app`，`10` §5）；新增 `skl` 短别名
    - 验收标准：`sakrylle --help` 与 `skl --help` 输出无 "codex" 字样

- [ ] npm 包封装
    - 目标：`@sakrylle/cli`（待定）包，bin 名 `sakrylle`，平台检测 spawn 改后的 Rust 二进制名
    - 涉及文件：`codex-cli/package.json:2,7`、`codex-cli/bin/codex.js:1-70`
    - 实施说明：包名待确认（见文末不确定项）；`bin/codex.js` 的二进制名映射改为 `sakrylle`
    - 验收标准：`npx @sakrylle/cli` 能 spawn 正确平台二进制

- [ ] TUI / status / onboarding 品牌字符串
    - 目标：替换全部 "OpenAI Codex" / "OpenAI's command-line coding agent" / ChatGPT 文案
    - 涉及文件（`10` §3 表）：`tui/src/onboarding/welcome.rs:97-98`、`tui/src/history_cell/session.rs:343,410`、`tui/src/status/card.rs:713,731`、`tui/src/onboarding/auth.rs:392,557`、`mcp-server/src/message_processor.rs:221`、`tui/src/chatwidget.rs:201`（Memories doc URL）
    - 实施说明：会话标题/状态卡改 "Sakrylle CLI"；登录提示文案对齐 Phase 3 的 Sakrylle 登录流；doc URL 指向 doc.sakrylle.com（或移除）
    - 验收标准：TUI 全程无上游品牌字样；配色可后续接入 Monet purple `#9181bd`（视 TUI 主题能力，非强制）

- [ ] originator / User-Agent
    - 目标：`DEFAULT_ORIGINATOR` 改 `sakrylle_cli_rs`（待定），不泄露上游身份
    - 涉及文件：`codex-rs/login/src/auth/default_client.rs:36-37`
    - 实施说明：直接改硬编码常量（env 覆盖仅初始化一次生效，不可靠）
    - 验收标准：出站请求 UA / originator header 不含 `codex_cli_rs`

- [ ] 货币与遥测
    - 目标：任何金额展示用 `￥`（仅展示，不转换，对齐项目 CLAUDE.md 货币政策）；遥测 / release URL 指向 fork
    - 涉及文件：CLI 内若有余额/用量展示处；analytics 上报端点（`10` 差距 #9）
    - 实施说明：`SAKRYLLE_TELEMETRY_DISABLED` 默认行为对齐 `05` §5；GitHub release 检查指向 fork 仓库
    - 验收标准：无金额展示走 `$`；遥测不上报 OpenAI 内部端点

---

## Phase 3 · OIDC 一键登录（核心，依赖 Phase 1 + OIDC 基座）

**目标**：实现 `sakrylle login` 的**一键 OIDC 登录**主流程（loopback + PKCE，RFC 8252 native app 最佳实践），降级 `--device`（RFC 8628），并完成 token 静默刷新与 `sakrylle logout` 吊销。**全程零复制粘贴**是核心验收。

**涉及文件/模块**：`codex-rs/login/`（`server.rs` loopback、`pkce.rs`、`device_code_auth.rs`、`auth/manager.rs`、`auth/storage.rs`）。**复用上游 loopback server + PKCE 实现，仅替换 issuer / endpoint / client_id / 凭据落盘路径**（`10` §8.1 流程已具备 loopback+PKCE+回调）。

**依赖项（关键）**：
- sub2api OIDC 基座（`03` Phase 1：G5 `openid` scope、G4 id_token、G1 discovery、G2 JWKS）。**若 OIDC 基座未落地**，可先实现纯 OAuth2 Authorization Code + PKCE（拿 `sk_oauth_` access_token，不验 id_token）跑通一键登录；严格 OIDC（验 id_token / 取 `sub`）待 `03` 完成后补。
- `sakrylle-cli` client 的 loopback redirect_uri 在 sub2api 白名单内（**Phase 0 核查；若缺需补 seed → 需审批**）。

**风险**：
- **issuer 硬编码**：上游 `DEFAULT_ISSUER="https://auth.openai.com"`（`server.rs:54`）无法 env 覆盖，必须 fork 改 `ServerOptions.issuer`（`server.rs:64-89`）。
- **Device endpoint 路径不兼容**：上游 `{issuer}/api/accounts/deviceauth/usercode`（`device_code_auth.rs:67`、token 在 `:106`）vs sub2api RFC 8628 `POST /oauth/device/code`（`oauth_device.go:54`）。fork 必须改端点路径（`10` 差距 #7 / 风险 3）。
- **loopback 端口冲突**：上游固定 `DEFAULT_PORT=1455` / `FALLBACK_PORT=1457`（`server.rs:55,57`）；RFC 8252 推荐**随机端口**，但 redirect_uri 必须在 sub2api 白名单——需「随机端口 + 固定兜底端口列表」双策略（见下方任务）。
- redirect_uri 不匹配时 sub2api 渲染内联 HTML 而非 302（不当 open redirector，`03` 安全要点）——CLI 须能识别此 HTML 错误页。

### 3a · loopback PKCE 一键登录主流程（端到端，串行）

- [ ] 改造 OAuth 配置：issuer / client_id / scope 指向 Sakrylle
    - 目标：`ServerOptions` 用 **单一 issuer `https://sub.sakrylle.com`**（已确认 2026-06-03；`SAKRYLLE_OIDC_ISSUER` 仅供非生产环境覆盖）、`client_id=sakrylle-cli`、`scope=openid profile email models:read`
    - 涉及文件：`codex-rs/login/src/server.rs:54,64-89`
    - 实施说明：issuer 默认硬编码 `https://sub.sakrylle.com`、可被 env 覆盖（仅非生产），对齐 `03` §6（issuer 一经发布不可改）；scope 对齐 `03` §9 CLI 行；endpoint 从 issuer 的 `/.well-known/openid-configuration`（`03` G1）发现，避免硬编码路径
    - 验收标准：`ServerOptions` 不含 `auth.openai.com`；issuer 默认即 `https://sub.sakrylle.com`；scope/client_id 与 §6 一致

- [ ] 打开系统默认浏览器（跨平台）
    - 目标：`sakrylle login` 自动拉起浏览器到 authorize URL（拼好 client_id / redirect_uri / scope / state / PKCE challenge / nonce）
    - 涉及文件：`codex-rs/login/src/server.rs`（浏览器拉起逻辑）
    - 实施说明：macOS `open`、Linux `xdg-open`、Windows `start`（或复用上游已有的 opener crate）；URL 含 `code_challenge`（S256）、随机 `state`、`nonce`（`03` §8 id_token 回填）。拉起失败要**打印 URL 兜底**让用户手动打开（不算破坏「一键」，是浏览器缺失时的健壮性）。
    - 验收标准：三平台默认浏览器正确打开 sub2api consent 页；拉起失败有可读兜底提示

- [ ] 本地 loopback 回调服务 + 随机端口 + 超时
    - 目标：在 `http://127.0.0.1:<随机端口>/callback` 起临时 HTTP server 自动捕获 `code`（RFC 8252）
    - 涉及文件：`codex-rs/login/src/server.rs:55,57`（端口常量）
    - 实施说明：**采用随机端口 + 通配 loopback 白名单方案（已确认 2026-06-03）**：绑 `127.0.0.1:0` 让 OS 分配端口；`sakrylle-cli` 的 redirect 白名单含 **`http://127.0.0.1` 任意端口 `/callback` + `http://localhost`**（RFC 8252 允许 loopback 任意端口），故无需「固定兜底端口列表」。设登录超时（如 300s）后关闭 server 并报错。（redirect_uri 的精确匹配/前缀匹配实现细节 → 实现期定，见 §7。）
    - 验收标准：server 在 OS 分配的随机端口监听；用户点授权后回调被自动捕获；超时自动清理不悬挂
    - 标注：注册通配 loopback 白名单 → 写 sub2api `oauth_clients.redirect_uris` → **需审批**

- [ ] state / PKCE 校验 + code 换 token
    - 目标：回调校验 `state`（CSRF），用 `code_verifier`（PKCE S256）换 access_token + refresh_token（+ id_token，若授 `openid`）
    - 涉及文件：`codex-rs/login/src/server.rs`、`codex-rs/login/src/pkce.rs`
    - 实施说明：复用上游 PKCE 实现，仅改 token endpoint 为 sub2api `/oauth/token`（`03` §6）；`state` 不匹配立即拒绝；S256 用常量时间比较（上游已具备）。**OIDC 基座已就绪（2026-06-04）**：用 discovery 的 `jwks_uri`（`03` G2）验 id_token 签名、校验 `iss`/`aud`/`nonce`/`exp`（`03` §8）。**签名算法：RS256 + ES256（per-client 由 `signing_algorithm` 选择）—— CLI 校验 id_token 时 allowed algs 须同时含 RS256 与 ES256**（并据 JWK `kid`/`alg` 选对应公钥；不接受其他 alg，杜绝 `alg=none` / 算法降级）。**注意 `aud` 以单元素 JSON 数组发出，校验时需兼容数组形态。** 若选择先行未验签：仅存 access_token，跳过 id_token 验证（记 TODO）。
    - 验收标准：state/PKCE 校验通过才落盘；id_token（如有）通过 JWKS 验签，且 allowed algs = {RS256, ES256}

- [ ] 成功页 HTML + 错误处理
    - 目标：回调成功后浏览器显示 Sakrylle 品牌成功页（提示「可关闭此页返回终端」）；各类失败有清晰文案
    - 涉及文件：`codex-rs/login/src/server.rs`（回调响应 HTML）
    - 实施说明：成功页用 Monet purple `#9181bd` + 樱花品牌；错误分支覆盖：用户拒绝授权、state 不匹配、token 兑换失败、sub2api 返回 redirect_uri 不匹配的**内联 HTML 错误页**（CLI 须识别并转成可读 CLI 错误，而非把 HTML 当 code）
    - 验收标准：成功/各失败路径都有明确终端反馈，无静默挂起

- [ ] 凭据落盘与权限（0600）
    - 目标：token 写入 `~/.sakrylle-cli/auth.json`，文件权限 `0600`
    - 涉及文件：`codex-rs/login/src/auth/storage.rs:31-48,84-85`（`AuthDotJson`、`get_auth_file()`）
    - 实施说明：落盘路径随 `SAKRYLLE_CLI_HOME`（Phase 1 已迁）；存 access_token / refresh_token /（id_token claims 如 `sub`/`exp`）；Unix 设 `0600`，Windows 设等价 ACL（仅当前用户）。**绝不**写 `~/.codex/auth.json`（`05` R1）。
    - 验收标准：`auth.json` 在 `~/.sakrylle-cli/`、权限 `0600`；上游 codex 的 `~/.codex/auth.json` 不受影响

- [ ] 一键登录端到端验证
    - 目标：`sakrylle login` → 浏览器 → 点一次授权 → 终端显示登录成功，**全程零复制粘贴**
    - 涉及文件：上述全链路
    - 实施说明：登录后立即用所得 token 跑一次 `exec` 验证可用；确认走 Anthropic 平台 group（`gateway.go:187`）
    - 验收标准：从执行命令到可用 token 全程无任何手动复制；登录后 `exec` 调用成功 + `usage_logs` 有计费行

### 3b · Device Flow 降级（无浏览器 / SSH 远程，可与 3a 并行开发）

- [ ] `sakrylle login --device` Device Authorization Flow（RFC 8628）
    - 目标：无浏览器环境显示 `user_code` + `verification_uri`，用户在另一台设备完成授权，CLI 轮询拿 token
    - 涉及文件：`codex-rs/login/src/device_code_auth.rs:67,106,159`
    - 实施说明：**改端点路径**对接 sub2api `POST /oauth/device/code`（`oauth_device.go:54`）+ 设备验证页 `/oauth/device`（`03` §6 蓝图），替换上游 `{issuer}/api/accounts/deviceauth/usercode`（`10` 差距 #7）；轮询 token endpoint 直至授权或超时；token 落盘逻辑复用 3a
    - 验收标准：SSH 远程环境 `sakrylle login --device` 显示 user_code + verification_uri；在另一设备完成后 CLI 自动拿到 token 并落盘

### 3c · 刷新与登出（依赖 3a 落盘）

- [ ] refresh_token 静默续期
    - 目标：access_token 临期/过期时用 refresh_token 自动换新，无需用户重新登录
    - 涉及文件：`codex-rs/login/src/auth/manager.rs`
    - 实施说明：调 sub2api `/oauth/token`（`grant_type=refresh_token`）；sub2api 侧 refresh 是 rotation + 家族撤销（`03` §4），CLI 须落盘**轮换后的新 refresh_token**（旧的失效）；可选 env `CODEX_REFRESH_TOKEN_URL_OVERRIDE` 等价物（`10` §7）但首选从 discovery 取 endpoint
    - 验收标准：access_token 过期后下次调用自动续期成功；replay（重用旧 refresh_token）被 sub2api 拒绝且 CLI 提示重新登录

- [ ] `sakrylle logout` 吊销 + 清理本地凭据
    - 目标：调 sub2api 吊销端点（RFC 7009 `/oauth/revoke`，`03` §6）撤销 token，并删除本地 `auth.json`
    - 涉及文件：`codex-rs/login/src/auth/manager.rs`、`storage.rs`
    - 实施说明：先调 `/oauth/revoke` 吊销 refresh/access token，再删 `~/.sakrylle-cli/auth.json`；吊销网络失败也要清本地（本地登出优先），并提示「服务端可能仍有效，请确认」
    - 验收标准：`sakrylle logout` 后本地无凭据；被吊销 token 再调用返回未授权

---

## Phase 4 · 测试 / 发布 / 回滚（串行收尾）

**目标**：CLI 各路径测试覆盖、跨平台构建发布、回滚预案。

**涉及文件/模块**：`codex-rs/` 各 crate 测试、CI 交叉编译、`codex-cli` npm 发布。

**依赖项**：Phase 1-3。

**风险**：发布到公共 registry / 改生产 client seed 属生产动作（需审批）；跨平台 loopback / 浏览器拉起行为差异需各平台实测。

- [ ] 单元 / 集成测试（隔离、auth、登录流）
    - 目标：覆盖 `SAKRYLLE_CLI_HOME` 隔离、env 优先级、PKCE/state 校验、id_token 验签（如 OIDC 就位）、refresh rotation、logout 吊销
    - 涉及文件：各 crate `tests/`
    - 实施说明：遵循项目测试规范（80%+ 覆盖、AAA、描述性命名）；登录流用 mock IdP 或 sub2api 预览环境
    - 验收标准：核心路径 80%+ 覆盖；同机并行上游 codex 零污染冒烟通过（`05` 验收 1-2）

- [ ] 跨平台构建与一键登录三平台实测
    - 目标：6 个目标三元组（`10` §4.2）构建通过；macOS/Linux/Windows 浏览器拉起 + loopback 回调实测
    - 实施说明：重点验证浏览器拉起（`open`/`xdg-open`/`start`）与 loopback 端口在三平台行为；SSH 远程实测 `--device`
    - 验收标准：三平台一键登录与 device flow 均跑通

- [ ] 发布与 client seed 终态（**需审批**）
    - 目标：npm 包发布；sub2api `sakrylle-cli` client 的 redirect_uris / scope 终态固化
    - 涉及文件：`codex-cli/package.json`；`backend/migrations/`（若需新增/替换 seed）
    - 实施说明：若 Phase 3 采用「通配 loopback 端口」或补 scope，需改生产 `oauth_clients`——**走审批**；fork 部署前替换 seed（`03` Phase 2 同款）
    - 验收标准：用户可 `npx @sakrylle/cli login` 完成一键登录；生产写操作经审批
    - 标注：**需审批**（生产 `oauth_clients` / registry 发布）

- [ ] 回滚预案
    - 目标：CLI 改造可回退不影响 sub2api 现网
    - 实施说明：CLI 是新客户端，回滚 = 不发布 / 用户卸载；OIDC client seed 若已改，回滚 = 还原 redirect_uris/scope（不影响其他 RP，`03` Phase 4 回滚原则——OIDC 全为叠加，access_token 路径零回归）
    - 验收标准：回滚后 sub2api 现有 `sk_oauth_` 调用、计费、其他 RP（Image/Web）零回归

---

## 4. 与 Sakrylle Studio 对接点（见 `21-sakrylle-studio-development-plan.md`）

> 接口形态以下为已知的天然对接面；首发认证方案已确认（见首条）。

- **Studio 首发认证 = 复用 CLI 凭据（已确认 2026-06-03，采用 `21` 分支 A）**：Studio（CodexMonitor fork，`com.sakrylle.studio`）首发**只读 `~/.sakrylle-cli/auth.json`**，认证完全由 CLI 管理；**不**共享 `~/.sakrylle/` 生态通用根、Studio 自身**不独立持有 OIDC token**。Studio 独立 OIDC 浏览器登录为后置增强项（`21` 分支 B，等 `03` 完成）。CLI 凭据落盘仍是 `~/.sakrylle-cli/auth.json`，Studio 跨进程只读该文件（最简、最隔离）。
- **Studio 监控 CLI 会话**：CodexMonitor 上游通过 app-server daemon（socket/PID 在 `CODEX_HOME`）观察 codex 会话。Sakrylle Studio 若要监控 Sakrylle CLI，需对齐 daemon 名（`sakrylle-cli-daemon`，`05` §8）、socket 路径（随 `SAKRYLLE_CLI_HOME`，`05` §11）、端口（Studio daemon 4732→4733，`05` §7）。**对接契约（socket 协议 / 会话 schema）在 `21` 定义**。
- **client_id 区分**：CLI=`sakrylle-cli`、Studio=`sakrylle-studio`（148 seed 现为 `sakrylle-desktop`，统一为 `sakrylle-studio`，见 `21` 附录 B / P0-7）——两者 OAuth client 独立，互不复用 token。注：首发 Studio 复用 CLI 凭据（上条），`sakrylle-studio` client 仅在 Studio 独立 OIDC（`21` 分支 B）启用时才需注册。

---

## 5. 串行 / 并行总结

- **串行主链**：Phase 0 → Phase 1 → （Phase 2 ∥ Phase 3）→ Phase 4。
- **Phase 1 内部串行**：隔离根 → API key env → 默认 provider → 冒烟（后者依赖前者）。
- **Phase 2 ∥ Phase 3**：品牌改名与 OIDC 登录无共享 state，可并行。
- **Phase 3 内部**：3a（loopback 主流程）串行链；3b（device flow）可与 3a 并行开发；3c（刷新/登出）依赖 3a 落盘。
- **跨文档依赖**：Phase 3 严格 OIDC 依赖 `03` Phase 1（G1-G5）落地；Phase 1 隔离与 `05` Phase 1 同源（可对照执行）。

---

## 6. `sakrylle-cli` OAuth client 注册建议（**需审批** —— 触生产 sub2api 侧）

> 现状：migration 148 已种入 `sakrylle-cli`（公共 client、`device_flow_enabled=true`、scopes 含 `responses:create`，`10` 结论 7）。以下为对接一键登录所需的**期望终态**，Phase 0 核查差异后按需补 seed（写生产 `oauth_clients` → 需审批）。

| 字段 | 建议值 | 理由 / 来源 |
|---|---|---|
| `client_id` | `sakrylle-cli` | `03` §9、148 seed |
| 客户端类型 | 公共（native app，无 client_secret） | RFC 8252；`03` §9 |
| `pkce_required` | `true`（S256 强制） | `03` §9 所有 public client |
| grant 类型 | `authorization_code`（loopback 主）+ `device_code`（降级）+ `refresh_token` | 本文 Phase 3 |
| `redirect_uris` | **`http://127.0.0.1` 任意端口 `/callback` + `http://localhost`（已确认 2026-06-03，通配 loopback 端口，RFC 8252 最佳实践）** | Phase 3「随机端口」任务；redirect_uri 精确/前缀匹配的实现细节 → 实现期定 |
| `allowed_scopes` | `openid profile email models:read responses:create` | `03` §9 CLI 行 + 148 已有 `responses:create`（调 `/v1/responses` 需要） |
| `access_token_ttl_seconds` | 默认（如 86400） | `03` §9 |
| `refresh_token_ttl_seconds` | 默认（如 2592000，启用 rotation） | `03` §4 |
| `device_flow_enabled` | `true` | `10` 结论 7 已就位 |

**待审批确认点**：
1. **redirect 策略已确认（2026-06-03）**：`sakrylle-cli` 白名单含 `http://127.0.0.1` 任意端口 `/callback` + `http://localhost`（通配 loopback 端口）。Phase 0 核查现 seed 是否已含；若无 → 补（需审批）。
2. **loopback 端口策略已确认（2026-06-03）**：随机端口 + 通配 loopback 白名单（取代旧的「精确白名单 + 固定兜底端口」纠结）。redirect_uri 在 sub2api 侧用精确匹配还是前缀匹配，属实现细节 → 实现期定（读 `oauth_provider_service.go` 或实测）。
3. `responses:create` scope 与 `requireGroupAnthropic`（`gateway.go:187`）的关系：CLI token 绑定的 group 必须是 Anthropic 平台（Claude 系），否则 `/v1/responses` 被拒（`10` 风险 5）——需在文档/onboarding 明确告知用户选 Claude group 的授权。

---

## 7. 不确定项（需确认，未臆造）

> 以下为剩余实现期/调研待定项。**已确认项**（CLI 短别名 `skl`、单一 issuer `https://sub.sakrylle.com`、`client_id=sakrylle-cli` public client、通配 loopback redirect 白名单、id_token 验签 algs RS256+ES256、Studio 首发复用 CLI 凭据、不引入多租户）已回填至正文相应章节并标注「已确认 2026-06-03」，不再列为不确定项。

- **npm 包名**：`@sakrylle/cli`（暂定）—— 需确认 npm scope `@sakrylle` 归属与是否已注册（纯实现细节，实现期定）。
- **originator 字符串**：`sakrylle_cli_rs`（暂定）—— 命名风格待生态主理人定（纯实现细节，实现期定）。
- **sub2api redirect_uri 匹配的实现形态**（精确 URL 匹配 vs `http://127.0.0.1` 前缀/通配匹配）—— **策略已定**（通配 loopback 端口，见 §6/Phase 3）；具体匹配实现属实现细节，**实现期定**（读 `oauth_provider_service.go` 或实测）。
- **OIDC 基座落地时序**：**✅ 已就绪（2026-06-04）：provider 在含 `openid` 时签发 id_token、`/v1/me` 返回 `sub`、discovery/JWKS 端点全部就位。** CLI Phase 3 可直接做严格 OIDC 一键登录（验 id_token）；若工程上选择先行，仍可走纯 OAuth2（不验 id_token）并留 TODO。
- **`CODEX_SQLITE_HOME` 是否独立于 `CODEX_HOME`**（`05` 后续问题）—— Phase 0 核查，影响隔离完整性。
- **`requires_openai_auth=false` 下 ChatGPT 专有功能（Memories/remote compaction）是否完全静默跳过还是 UI 异常**（`10` 后续问题 2）—— 需实测，影响 Phase 2 文案与功能降级范围。
- **`sakrylle-cli` device flow 是否已有生产真实用户在用**（`03` 后续问题）—— 影响灰度顺序。
