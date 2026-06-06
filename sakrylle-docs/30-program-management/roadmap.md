---
title: 90 · Sakrylle 生态总路线图
status: canonical
scope: program
last_verified: 2026-06-06
---

# 90 · Sakrylle 生态总路线图

> 规划文档（planning only）。Sakrylle API（sub.sakrylle.com）与 Sakrylle Image（image.sakrylle.com）**已上线生产**，本文不含任何直接改生产配置、破坏性迁移、删用户数据的指令。所有触及生产的步骤标注「需额外审批」。
> 兄弟文档：`03-sakrylle-api-oidc-architecture.md`（OIDC 基座详设）、`05-configuration-isolation-standard.md`（隔离规范）、`91-risk-register.md`（风险登记）、`92-open-questions.md`（待确认问题）、`93-implementation-checklist.md`（总实施 checklist）。

---

## 1. 生态概览与依赖拓扑

### 1.1 六个产品成员

| 产品 | 上游基座 | 技术栈 | 当前状态 | 角色 |
|---|---|---|---|---|
| **Sakrylle API** | Wei-Shaw/sub2api | Go 1.23 + Vue 3/Vite | **已上线**（sub.sakrylle.com） | 中心 IdP + AI API 网关 |
| **Sakrylle Image** | gpt_image_playground | React 19 + Vite/TS SPA | **已上线**（image.sakrylle.com） | 最早验证 OIDC 的 RP |
| **Sakrylle CLI** | openai/codex | Rust (codex-rs) + Node 封装 | 未 fork | 开发者命令行工具 |
| **Sakrylle Studio** | Dimillian/CodexMonitor | Tauri 2 + React/TS | 未 fork | CLI 的桌面 GUI 前端 |
| **Sakrylle Web** | open-webui | SvelteKit + FastAPI/Python | 未 fork | Web 端 AI 聊天界面 |
| **Sakrylle Chat** | kelivo | Flutter（Android/iOS/macOS/Win/Linux/Web） | 未 fork | 跨端移动/桌面客户端 |

### 1.2 核心依赖拓扑

```
Sakrylle API（IdP + 网关）
  ├── OIDC 基座（Phase 1 核心任务）
  │     ├── [依赖] Sakrylle Image OIDC 升级（最早 RP 验证）
  │     ├── [依赖] Sakrylle CLI fork（Device Flow OIDC）
  │     ├── [依赖] Sakrylle Studio fork（Tauri PKCE loopback）
  │     ├── [依赖] Sakrylle Web fork（OIDC SSO，需 openid-configuration）
  │     └── [依赖] Sakrylle Chat fork（OAuth PKCE 移动 scheme）
  └── Responses API 端点（/v1/responses）
        └── [依赖] Sakrylle CLI（codex 仅说 /v1/responses）
              └── [依赖] Sakrylle Studio（透传 CLI 子进程）
```

**关键约束**：
- OIDC 基座（`03` 文档 Phase 1）是 CLI、Studio、Web、Chat 的 SSO 硬性前置依赖。
- Sakrylle Image 已有 OAuth PKCE，OIDC 升级成本最低（加 `openid` scope + 客户端解析 id_token），**是验证 OIDC 链路的最佳首发 RP**。
- Responses API（`/v1/responses`）：`backend/internal/handler/gateway_handler_responses.go` + `backend/internal/server/routes/gateway.go:91-105` 已实现，CLI 对接 blocker 已解除。
- CLI 配置隔离（`SAKRYLLE_CLI_HOME`）**不依赖 OIDC**，可最早独立启动，与 OIDC 基座并行推进。

---

## 2. 阶段定义与全局原则

### Phase 0 · 调研与保护（已基本完成）
锁定技术决策，确认未决问题，建立生产护栏。文档 `02`–`05`、`10`、`20`、`30`、`40`、`50` 已产出。`92` 的 30 问已于 2026-06-03 全部分诊：13 个 ✅ RESOLVED（用户拍板 + 代码核实），17 个 🔧 CODE-CHECK（实现期 Phase 0 自查）。**关键决策已冻结**：issuer = `https://sub.sakrylle.com`（Q-03）、CLI 二进制 = `sakrylle`/`skl`（Q-11）、不引入多租户（Q-27）、第二签名算法 = ES256（Q-28）、Web 域名 = `chat.sakrylle.com`（Q-25）、Chat 不发 Flutter Web（Q-30）、保留 iOS Live Activity（Q-26）、遥测默认关闭（Q-12）、`email:read` 第一方默认授予（Q-29）。代码核实结论：`security_secrets` 表已存在（migration 053，G3 复用该表、无需建表，Q-01，已确认 2026-06-03）。Q-22（`/oauth/token` 签 id_token，G4）、Q-24（`/v1/me` 返 `sub`，G7）原记「当前未实现」，**现已于 2026-06-04 实现**（含 ES256 per-client 签发）。Phase 0 剩余的 Q-06/Q-09/Q-19 等转为各产品实现期自查。

### Phase 1 · 最小可用集成
最小改动实现可验证的 OIDC 端到端链路：OIDC 基座（sub2api 侧）+ Sakrylle Image 升级验证（生产 RP 冒烟）。**不新增额外基础设施，全部是叠加改造，不破坏现有 OAuth RP。**

### Phase 2 · 品牌与配置隔离
各待 fork 产品完成配置隔离（目录 / bundle id / 端口 / 环境变量前缀），品牌替换（Monet Purple + 樱花 logo），建立 OIDC 接入基础。**各 fork 天然并行，不相互依赖。CLI 配置隔离可在 Phase 0 即启动。**

### Phase 3 · 完整 OIDC / 权限 / 审计
全生态 SSO 打通，UserInfo 合规，RP-Initiated Logout，scope enforcement 开启，安全审查。

### Phase 4 · 测试 / 发布 / 回滚
OIDC 一致性测试，并行冒烟测试，各产品生产上线，回滚预案。

---

## 3. 推荐上线顺序与理由

### 3.1 整体顺序图

```
[可最早独立启动，不依赖 OIDC]
  CLI 配置隔离（SAKRYLLE_CLI_HOME + 品牌字符串替换）
  Studio 品牌隔离（bundle id + 端口，不涉及 OIDC）

[串行核心链路：Phase 0 → Phase 1]
  Phase 0 P0 问题确认（Q-01/03/06/09/22）
    ↓
  OIDC 基座：RS256 密钥（G3） [串行首项]
    ↓（并行启动）
  JWKS 端点（G2）        scope 注册 openid（G5，可与 G3 并行）
    ↓
  id_token 签发（G4）
    ↓（并行启动）
  openid-configuration（G1）    Sakrylle Image OIDC 升级（生产 RP 验证）

[OIDC 基座完成后，并行启动 Phase 2 各 fork]
  ↓              ↓              ↓              ↓
Sakrylle CLI   Sakrylle      Sakrylle Web   Sakrylle Chat
Device Flow    Studio        OIDC SSO       PKCE OAuth
OIDC 接入      OIDC 接入      接入           移动 scheme
（依赖 OIDC）   （依赖 CLI）   （依赖 OIDC）  （依赖 OIDC）
  ↓              ↓              ↓              ↓
[Phase 3：全生态 UserInfo / Logout / scope enforcement]
  ↓
[Phase 4：一致性测试 + 灰度发布 + 回滚]
```

### 3.2 推荐排序详述

**第一优先（串行，Phase 0 剩余 + Phase 1）**：

1. **P0 问题确认**（已于 2026-06-03 落定，见 `92` §A）
   - Q-01：**已核实 `security_secrets` 表已存在（migration 053）** → G3 RS256+ES256 keystore 复用该表（新增 key 行、加密 at-rest，无需建表，已确认 2026-06-03）
   - Q-03：issuer **已冻结 `https://sub.sakrylle.com`**；Phase 1.A 开工前仅做一次只读核对生产值一致性
   - Q-06：`mintRepo` 生产路径 replay 防护 → 转实现期自查（Phase 1.A 开工时确认非 legacy fake 路径）
   - Q-22：原核实「`/oauth/token` 当时不签发 `id_token`」→ id_token 签发（G4）**已于 2026-06-04 实现**，Image OIDC 升级不再被基座阻塞

2. **Sakrylle API OIDC 基座**（Phase 1）
   - 理由：所有客户端 SSO 的硬性前置。RS256 密钥 → JWKS → id_token → discovery。
   - 依赖：无（纯叠加，不破坏现有 OAuth RP）。
   - 工程难点：RS256 私钥管理基础设施（G3），需额外审批。

3. **Sakrylle Image OIDC 升级**（紧跟 Phase 1 基座完成后）
   - 理由：Image 已在生产用 OAuth PKCE，是最低成本验证 OIDC 链路的切入点。
   - 具体改动：`src/lib/sakrylleAuth.ts:16` scope 加 `openid`，解析 id_token claims，补 `vite-env.d.ts` 声明，在 Dockerfile 补 `VITE_SAKRYLLE_OAUTH_BASE`/`VITE_SAKRYLLE_OAUTH_CLIENT_ID` 占位符注入。
   - 需额外审批：生产镜像更新（触生产）。

**第二批（可并行，Phase 2）**：

4. **Sakrylle CLI fork**（配置隔离部分不依赖 OIDC，可最早启动）
   - 配置隔离（`find_codex_home()` 改 `SAKRYLLE_CLI_HOME`，默认 `~/.sakrylle-cli`）：**不依赖 OIDC**，可在 Phase 0 即启动。
   - 品牌替换（TUI 10 处字符串，`DEFAULT_ORIGINATOR=sakrylle_cli`，bin name `sakrylle` + 短别名 `skl`，遥测/Sentry 默认关闭）：并行进行。
   - Responses API 对接（config.toml 注册 sakrylle provider，`wire_api=responses`）：blocker 已解除。
   - Device Flow OIDC 接入（`login/src/server.rs:54` 替换 issuer 和 client_id）：**依赖 OIDC 基座**。

5. **Sakrylle Studio fork**（依赖 CLI 完成 app-server 协议兼容性验证）
   - Tauri bundle id 替换（`com.sakrylle.studio`）、daemon 端口 4732→4733、localStorage 前缀 `codexmonitor.*`→`sakrylle-monitor.*`。
   - Sentry DSN 清空（遥测默认关闭，Q-12；`src/main.tsx:9` 置空 `VITE_SENTRY_DSN`），updater endpoint 改指 fork 仓库。
   - Settings 配置 Sakrylle CLI 路径（`codexBin` 字段，运行时可配，无需重新编译）。
   - OIDC login 流程：依赖 CLI app-server JSON-RPC 协议兼容性。

6. **Sakrylle Web fork**（依赖 OIDC 基座，需 `/.well-known/openid-configuration`）
   - `env.py:772-773` 删 `(Open WebUI)` 后缀逻辑、`constants.ts:4` 改 `APP_NAME`、`app.html:118` 改 title。
   - `OPENID_PROVIDER_URL=https://sub.sakrylle.com/.well-known/openid-configuration` 接入（需 Q-16 确认 authlib fallback 行为）。
   - 独立 `DATA_DIR`（`/data/sakrylle-web`）隔离，docker compose volume 改名。
   - 域名 = `chat.sakrylle.com`（Q-25 已定），注册 `sakrylle-web` client（`03` §9），redirect_uri = `https://chat.sakrylle.com/oauth/oidc/callback`。

7. **Sakrylle Chat fork**（依赖 OIDC 基座，需 id_token 支持用户身份验证）
   - bundle id 四处替换（`com.sakrylle.chat`：Android/iOS/macOS）；**保留 iOS Live Activity（Q-26）**，故 `GenerationActivityExtension` 扩展 bundle id + App Group 同步改 `com.sakrylle.chat.*`；不发 Flutter Web（Q-30）。
   - `flutter_web_auth_2` + iOS `CFBundleURLSchemes` + Android `intent-filter` 实现 PKCE 回调 scheme `sakrylle-chat://oauth/callback`。
   - OAuth access_token 写 `FlutterSecureStorage`（替换 SharedPreferences 明文存储）。
   - Monet Purple `#9181bd` Material3 调色板新增并设为默认。
   - 预置 Sakrylle API provider 条目（`settings_provider.dart` `_builtInProviderKeysInOrder`）。

**第三批（Phase 3，全生态 OIDC 验证）**：

8. **UserInfo 合规 + RP-Initiated Logout**（依赖 Phase 1）
9. **scope enforcement 开启**（`oauth_scope_enforcement_enabled=true`，需对所有 RP 验 scope 完整性，需额外审批）
10. **安全审查**（OIDC 签名密钥管理、token 存储、XSS 防护）

---

## 4. 并行机会分析

| 任务组 | 可并行性 | 说明 |
|---|---|---|
| CLI 配置隔离 vs. OIDC 基座 | **完全并行** | 配置隔离不依赖 IdP 端点，可在 Phase 0 即启动 |
| OIDC scope 注册（G5）vs. RS256 密钥（G3） | **并行** | G5 仅改 oauth_scopes.go，不依赖密钥 |
| JWKS 端点（G2）vs. openid-configuration（G1） | **并行** | 两者均依赖 G3，但可同步开发 |
| Studio / Web / Chat fork 品牌与隔离 | **三者并行** | 各 fork 仓库独立，无共享 state |
| CLI Device Flow OIDC vs. Web OIDC 接入 | **并行** | 均依赖 OIDC 基座，不相互依赖 |
| Phase 2 品牌改造 vs. Phase 3 UserInfo/Logout | **部分并行** | UserInfo 合规可在品牌改造完成前并行开始 |
| Sakrylle Image 生产发布 vs. 其他 fork 开发 | **并行** | Image 是独立生产服务，其他 fork 在开发中 |
| P0 问题核查（Q-01/Q-03/Q-06/Q-09/Q-22）| **全部并行** | 均为只读代码核查或只读 DB 查询，无相互依赖 |

---

## 5. 里程碑

### M1 · OIDC 链路可验证（Phase 1 完成）

**目标**：至少一个 RP 可完成完整 OIDC Authorization Code 流程，id_token 可用 JWKS 验签。

| 子里程碑 | 涉及产品 | 关键验收 |
|---|---|---|
| M1-a：RS256+ES256 密钥基座落地 | Sakrylle API | ✅ **达成（2026-06-04）**：进程内加载私钥，`/.well-known/jwks.json` 返回合法 JWKS（RS256 + ES256 各含 kid；Q-28）|
| M1-b：id_token 签发 | Sakrylle API | ✅ **达成（2026-06-04）**：`scope=openid` 时 `/oauth/token` 响应含可用 JWKS 验签的 `id_token`（RS256/ES256 按 client `signing_algorithm` 选择）|
| M1-c：openid-configuration | Sakrylle API | ✅ **达成（2026-06-04）**：discovery 含 `issuer=https://sub.sakrylle.com`、`jwks_uri`、`id_token_signing_alg_values_supported=[RS256,ES256]` |
| M1-d：Image OIDC 升级冒烟 | Sakrylle Image | ✅ **达成（2026-06-05）**：Phase 0–3 全部完成（commit ed0c141），生产 `OIDC_ENABLED=true`，`oauth_scope_enforcement_enabled=true`，Image 是第一个完成 OIDC 全链路接入的 RP |

**M1 全部达成（2026-06-05）。** Sakrylle Image 已在生产完成 OIDC 全链路：Discovery + openid scope + id_token 解析 + nonce 校验 + RP-Initiated Logout + scope enforcement。所有客户端产品（CLI/Studio/Web/Chat）的 OIDC 接入现在可以启动。

### M2 · CLI 可用（Phase 2 CLI 完成）

**目标**：Sakrylle CLI 在开发者机器上可与上游 codex 并行安装，可接入 Sakrylle API，`~/.sakrylle-cli/` 与 `~/.codex/` 完全隔离。

| 子里程碑 | 关键验收 |
|---|---|
| M2-a：配置隔离 | `~/.sakrylle-cli/` 与 `~/.codex/` 完全隔离，同机并行无污染（auth.json/sessions/socket 不共享）|
| M2-b：API 接入 | config.toml 注册 sakrylle provider，`SAKRYLLE_API_KEY` 鉴权，`/v1/responses` 调用成功 |
| M2-c：品牌替换 | TUI 显示 "Sakrylle CLI"，`DEFAULT_ORIGINATOR=sakrylle_cli`，无 OpenAI 字样泄露 |
| M2-d：Device Flow OIDC（依赖 M1） | `sakrylle login` 触发 Device Flow 对接 sub2api OIDC，返回并存储 id_token |

### M3 · Studio 可用（Phase 2 Studio 完成，依赖 M2-b）

**目标**：Sakrylle Studio 桌面 GUI 可启动 Sakrylle CLI 子进程，与上游 CodexMonitor 同机无冲突。

| 子里程碑 | 关键验收 |
|---|---|
| M3-a：bundle id + 端口 | `com.sakrylle.studio`，daemon 端口 4733，同机不与 CodexMonitor 冲突 |
| M3-b：CLI 路径绑定 | Settings 配置 sakrylle CLI 路径，`codex app-server` 子进程 JSON-RPC 成功通信 |
| M3-c：updater + Sentry | release URL 指 Ranshen1209/sakrylle-studio，Sentry DSN 替换或清空 |

### M4 · Web 可用（Phase 2-3 Web 完成，依赖 M1）

**目标**：Sakrylle Web 可通过 OIDC SSO 登录，连接 Sakrylle API 发对话请求，品牌完全 Sakrylle 化（无 "Open WebUI" 字样）。

| 子里程碑 | 关键验收 |
|---|---|
| M4-a：品牌 + DATA_DIR 隔离 | `WEBUI_NAME` 正确显示（无后缀），独立数据目录，favicon/logo 已替换 |
| M4-b：OIDC SSO 接入 | `OPENID_PROVIDER_URL` 指向 sub.sakrylle.com，登录后 id_token 验通，用户自动建账 |
| M4-c：API 接入 | `OPENAI_API_BASE_URLS=https://api.sakrylle.com/v1`，模型列表正常，对话请求计费生效 |

### M5 · Chat 可用（Phase 2-3 Chat 完成，依赖 M1）

**目标**：Sakrylle Chat 移动端可完成 PKCE OAuth 登录，API key 安全存储，Monet Purple 主题默认生效。

| 子里程碑 | 关键验收 |
|---|---|
| M5-a：bundle id 四处替换 | Android/iOS/macOS 各平台数据目录与 kelivo 完全隔离 |
| M5-b：OAuth PKCE 实现 | `flutter_web_auth_2` + 自定义 scheme `sakrylle-chat://` 回调成功，access_token 写 FlutterSecureStorage |
| M5-c：Monet 主题 | `#9181bd` 调色板为默认，Material3 全组件色系生效 |
| M5-d：Sakrylle API 预置 | 首次启动显示 Sakrylle API provider，`https://api.sakrylle.com/v1` 为默认 base URL |

### M6 · 全生态 OIDC 完整合规（Phase 3-4 完成）

**目标**：6 个产品 SSO 打通，scope enforcement 开启，OIDC 一致性测试通过，可对外发布。

| 子里程碑 | 关键验收 |
|---|---|
| M6-a：UserInfo 合规 | `/v1/me` 授 `openid` 必返 `sub`；余额绝不进 id_token（遵循 `04` claims 原则） |
| M6-b：RP-Initiated Logout | `/oauth/logout` 端点，`post_logout_redirect_uri` 白名单校验，不当 open redirector |
| M6-c：scope enforcement | `oauth_scope_enforcement_enabled=true`（需额外审批），所有 RP scope 验通无 403 |
| M6-d：OIDC 一致性测试 | discovery/JWKS/id_token/UserInfo/nonce/PKCE 全覆盖，80%+ 测试通过 |
| M6-e：安全审查通过 | 密钥管理、token 存储、XSS 防护审查无高危（P0）和高危（P1）未缓解项 |

---

## 6. 排期估算（相对工作量，非绝对天数）

> 以下为单人全职相对估算。并行任务标注 `[P]`。

| 阶段 | 任务 | 估算工作量 | 串并行 |
|---|---|---|---|
| **Phase 0 剩余** | P0 问题已落定（Q-01/02/03/22 RESOLVED；Q-06/09/19 转实现期自查）| 已完成 | — |
| **Phase 1** | RS256+ES256 密钥基座（G3，复用现有 `security_secrets` 表，无需建表，工作量相应下调）| 中（2-3 天）| 串行首项 |
| Phase 1 | JWKS 端点（G2）| 中（1-2 天）| 依赖 G3 |
| Phase 1 | scope 注册（G5）| 小（半天）| `[P]` 与 G3 并行 |
| Phase 1 | id_token 签发（G4）| 大（2-3 天）| 依赖 G3 |
| Phase 1 | openid-configuration（G1）| 中（1 天）| `[P]` 与 JWKS 并行 |
| Phase 1 | Sakrylle Image OIDC 升级 | 小（1-2 天）| 依赖 OIDC 基座完成 |
| **Phase 2** | CLI 配置隔离（可最早启动）| 中（1-2 天）| `[P]` 与 OIDC 并行 |
| Phase 2 | CLI 品牌替换（~10 处）| 小（半天-1 天）| `[P]` |
| Phase 2 | CLI Responses API 对接 | 小（半天）| `[P]`（blocker 已解除）|
| Phase 2 | CLI Device Flow OIDC 接入 | 中（1-2 天）| 依赖 OIDC 基座 |
| Phase 2 | Studio fork 改造（bundle+端口+localStorage+Sentry）| 中（2-3 天）| `[P]` 依赖 CLI |
| Phase 2 | Web fork 品牌+隔离+OIDC | 大（3-4 天）| `[P]` 依赖 OIDC 基座 |
| Phase 2 | Chat fork（bundle+OAuth+主题+预置 provider）| 大（4-6 天）| `[P]` 依赖 OIDC 基座 |
| **Phase 3** | UserInfo OIDC 分支（G7）| 中（1-2 天）| 串行，依赖 Phase 1 |
| Phase 3 | RP-Initiated Logout（G10）| 中（1-2 天）| `[P]` 与 UserInfo |
| Phase 3 | scope enforcement 开启 | 小（配置+测试，1 天）| 依赖所有 RP 完成，需额外审批 |
| **Phase 4** | OIDC 一致性测试 | 中（2-3 天）| 串行最后 |
| Phase 4 | 灰度发布 + 回滚预案 | 小（1 天）| 需额外审批 |

---

## 7. 里程碑快速参考

| 里程碑 | 前置条件 | 解锁内容 |
|---|---|---|
| **M1** OIDC 链路可验证 | P0 问题确认；无其他外部依赖（叠加改造）| Image OIDC 冒烟；CLI/Web/Chat OIDC 接入可启动 |
| **M2** CLI 可用 | M1（OIDC 部分）；CLI 配置隔离独立于 M1 | Studio 可启动（依赖 CLI）|
| **M3** Studio 可用 | M2（app-server JSON-RPC 兼容）| 桌面 GUI 覆盖 CLI 用户 |
| **M4** Web 可用 | M1 | Web 端聊天 + SSO 上线 |
| **M5** Chat 可用 | M1 | 移动端覆盖 |
| **M6** 全生态合规 | M1–M5 全部 | scope enforcement；可对外发布 |

---

## 8. 与调研文档对照索引

| 本文引用 | 详设文档 |
|---|---|
| OIDC 基座 Phase 1 任务 | `03-sakrylle-api-oidc-architecture.md` §10 |
| 配置隔离规范（`SAKRYLLE_CLI_HOME` 等）| `05-configuration-isolation-standard.md` |
| CLI fork 改造计划 | `10-sakrylle-cli-research.md` §recommendations |
| Studio fork 改造计划 | `20-sakrylle-studio-research.md` §recommendations |
| Web fork 改造计划 | `31-sakrylle-web-development-plan.md` |
| Chat fork 改造计划 | `41-sakrylle-chat-development-plan.md` |
| Image 升级（OIDC）| `50-sakrylle-image-research.md` §recommendations |
| 风险详表 | `91-risk-register.md` |
| 待确认问题 | `92-open-questions.md` |
| 实施 checklist | `93-implementation-checklist.md` |
