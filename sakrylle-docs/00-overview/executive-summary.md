---
title: 00 · 执行摘要
status: canonical
scope: overview
last_verified: 2026-06-06
---

# 00 · 执行摘要

> 目标读者：技术负责人、产品决策者。本文不涉及实现细节，细节见各专项文档。
> 安全约束：Sakrylle API 与 Sakrylle Image 已上线生产，本文不含任何直接改生产配置的指令。

---

## 1. 目标

将 6 个开源上游 fork 整合为统一品牌的 **Sakrylle 生态**，以 **Sakrylle API** 作为中心身份提供方（IdP）和 AI API 网关，实现：

- 统一 OAuth 2.0 / OIDC 单点登录（SSO）：用户在任意 Sakrylle 产品登录后，其余产品自动免登。
- 统一品牌视觉：Monet Purple（#9181bd）主色 + 樱花 logo，覆盖全部产品端。
- 配置隔离：与各上游软件在同一台机器上零冲突并行运行。
- 商业能力：余额计费、模型限额、分组定价全部由 Sakrylle API 承载，客户端不感知商业逻辑。

---

## 2. 六个产品与上游基座

| 产品 | 上游基座 | 技术栈 | 上线状态 | 主要 fork 目标 |
|---|---|---|---|---|
| **Sakrylle API** | Wei-Shaw/sub2api | Go 1.23 + Vue 3 / Vite | **已上线**（`sub.sakrylle.com`） | OIDC 扩展，作为全生态 IdP |
| **Sakrylle Image** | gpt_image_playground | React 19 + Vite/TS SPA | **已上线**（`image.sakrylle.com`） | 补全 OIDC id_token 支持 |
| **Sakrylle CLI** | openai/codex | Rust (codex-rs) + Node 封装 | 未 fork | 配置隔离 + Responses API 兼容性验证；二进制 `sakrylle`（+`skl`），遥测默认关闭 |
| **Sakrylle Studio** | Dimillian/CodexMonitor | Tauri 2 + React/TS | 未 fork | bundle id `com.sakrylle.studio` + CLI 路径绑定；首发复用 CLI 凭据 |
| **Sakrylle Web** | open-webui | SvelteKit + FastAPI/Python | 未 fork | OIDC SSO 接入 + 品牌替换；部署域名 `chat.sakrylle.com` |
| **Sakrylle Chat** | kelivo | Flutter（Android/iOS/macOS/Win/Linux）| 未 fork | bundle id `com.sakrylle.chat` + OAuth PKCE（`sakrylle-chat://`）+ Monet 主题；保留 iOS 灵动岛，不发 Flutter Web |

Sakrylle API 是**中心 IdP**：其余 5 个产品均作为 OAuth 2.0 / OIDC RP（Relying Party）接入，不自建账户体系。

---

## 3. OIDC 基座现状（截至 2026-06-05）

**OIDC Core 1.0 + 大量 OIDC 可选能力已完整实现**（2026-06-05）。Sakrylle API 已具备完整的 **OAuth 2.0 Authorization Server** + **高级 OIDC Provider** 能力：

### 已实现功能 ✓（OIDC Core 1.0，2026-06-04）
- ✅ `/.well-known/openid-configuration` 端点
- ✅ `/.well-known/jwks.json` 端点（RS256 + ES256 密钥发布）
- ✅ RS256 + ES256 非对称签名基础设施（复用 `security_secrets` 表）
- ✅ `/oauth/token` 签发 `id_token`（含 `openid` scope 时，RS256/ES256 按 client 选择）
- ✅ `scope=openid/profile/email` 定义和处理
- ✅ `/v1/me` + `/userinfo` 返回标准 OIDC UserInfo claims（`sub/name/preferred_username/email`）
- ✅ OIDC nonce 支持
- ✅ RP-Initiated Logout (`/oauth/logout`) 端点
- ✅ `prompt=none` 静默认证支持（仅 `trusted_first_party` 客户端可免交互；第三方客户端返回 `consent_required`）
- ✅ per-user `email_verified` 标志（migration 157）

### 已实现功能 ✓（OIDC 可选能力，2026-06-05）
- ✅ **Pairwise Subject Identifier**（OIDC Core §8）：每 client 不同 sub，防跨 RP 用户关联
- ✅ **Request Object / request_uri**（OIDC Core §6）：非对称签名验证 + HTTPS 远程获取
- ✅ **Claims Parameter**（OIDC Core §5.5）：essential/value/values 约束过滤
- ✅ **Back-Channel Logout**（OIDC Back-Channel Logout 1.0）：签名 logout_token + sid 跟踪
- ✅ **Front-Channel Logout**（OIDC Front-Channel Logout 1.0）：iframe 通知
- ✅ **Token Introspection**（RFC 7662）：仅 confidential clients 可调用
- ✅ **Session ID (sid)**：用于 logout token 关联
- ✅ **at_hash / c_hash**：access_token 和 authorization_code 的 SHA-256 哈希 claims
- ✅ **Signed UserInfo JWT**：`Accept: application/jwt` 返回签名响应
- ✅ **Consent Grant 跟踪**：第三方客户端授权记录（migration 159）
- ✅ **自动密钥轮换调度器**：双 goroutine（rotation + cleanup），可配置间隔

### 历史背景（2026-06-03 之前）
在 2026-06-03 之前，OIDC 层完全缺失。以下是当时的状态，现已全部解决：
- ~~无 `/.well-known/openid-configuration`~~（已实现）
- ~~无 `/.well-known/jwks.json`、无 RS256/ES256 签名基础设施~~（已实现）
- ~~`/oauth/token` 不签发 `id_token`~~（已实现）
- ~~无 `scope=openid` 定义~~（已实现）
- ~~`/v1/me` 无 `sub` 字段~~（已实现）

### 当前待完成项（IdP 基座已就绪，余项为运营与客户端接入）
- ⏳ **scope enforcement 开启需生产审批**：`oauth_scope_enforcement_enabled` 默认 `false`（过渡态）。开启是一项**生产决策，需审批**——开启后会开始拒绝 scope 不足的 `sk_oauth_` token。
- ⏳ **客户端产品 OIDC 接入**：Image / Web / CLI / Chat 客户端仍需实际采用 OIDC 登录流程（IdP 侧已就绪，不再阻塞）。
- ⏳ **migration 151 生产上线 + per-client `signing_algorithm` 配置**：属运营步骤，按项目惯例以 SQL 管理。

**所有客户端产品的 SSO 接入现在可以开始**——Sakrylle Web（open-webui）、Sakrylle Chat、Sakrylle Image 升级均不再阻塞。

OIDC 架构详见 [03-sakrylle-api-oidc-architecture.md](./03-sakrylle-api-oidc-architecture.md)。

---

## 4. 总体路线图

按从易到难、最小风险原则分四个阶段推进。**Phase 0** 只读调研与保护已基本完成（02-05、10、20、40、50 文档）。**Phase 1** 以 Sakrylle API OIDC 扩展和 Sakrylle Image 升级为最小可用集成，不新增基础设施，复用现有 api_keys 表。**Phase 2** fork 非在线产品（CLI、Studio、Web、Chat），完成品牌与配置隔离，各产品通过 OIDC 标准流程接入已有 IdP。**Phase 3** 扩展至完整 OIDC 合规（scope enforcement、审计日志、RP-Initiated Logout、管理后台多 client 管理），并完成安全审查。**Phase 4** 全面测试、文档发布、各产品生产上线与回滚预案。每个 Phase 的验收标准均在对应专项文档中定义。

---

## 5. 最高优先级项目

以下三项是其余一切工作的解锁前置条件：

**P0-A：Sakrylle API OIDC 基座** ✅ **[已完成 2026-06-04]**
- ✅ RS256 + ES256 密钥对（复用 `security_secrets` 表）
- ✅ `/.well-known/openid-configuration` 端点（issuer = `https://sub.sakrylle.com`）
- ✅ `/.well-known/jwks.json` 端点
- ✅ `openid/profile/email` scope 注册
- ✅ `/oauth/token` 在含 `openid` scope 时签发 `id_token`
- ✅ `/v1/me` 返回 OIDC-兼容 UserInfo（包含 `sub` 字段）
- ✅ **ES256 运行时按 client 签发**（migration 151 `signing_algorithm`，已映射 ent schema + codegen，`maybeSignIDToken` 安全回退 RS256）

**P0-A2：Sakrylle Image OIDC 升级** ✅ **[已完成 2026-06-05]**
- ✅ Phase 0–3 全部完成（commit ed0c141，14 文件，+539 行，+13 测试）
- ✅ 生产 `OIDC_ENABLED=true`，`oauth_scope_enforcement_enabled=true`
- ✅ Image 是第一个完成 OIDC 全链路接入的 RP

**P0-A3：其余客户端产品 OIDC 集成**（IdP 侧 + scope enforcement 已就绪）
- Sakrylle CLI / Studio / Web / Chat OIDC 接入（可立即启动）

**P0-B：Sakrylle CLI 配置隔离与 Responses API 兼容性验证**（`11` 文档 Phase 1 任务）
- codex-rs 已强制移除 `wire_api="chat"`（`model-provider-info/src/lib.rs:46`），CLI 只说 Responses API（`POST /v1/responses`）
- sub2api 已实现 `POST /v1/responses`（见 `10` / `11` / `90` 的更新口径），CLI 接入 blocker 已解除；Phase 1 重点转为默认 provider 配置、Claude 系 group 鉴权、streaming SSE / tool_call / 文件编辑 / shell exec 等真实 Codex 场景兼容性验证，并确认 `usage_logs` 正常计费

**P0-C：配置隔离执行**（`05` 文档各产品 Phase 0 任务）
- CLI fork 必须先改 `find_codex_home()`，使默认目录为 `~/.sakrylle-cli/`（而非 `~/.codex/`），避免与用户系统中的 openai/codex 争抢 auth.json / socket / SQLite
- 其余 4 个待 fork 产品的 bundle id / applicationId / DATA_DIR 必须在第一个 commit 中更新

---

## 6. 最大风险

| 风险 | 严重程度 | 影响范围 | 缓解措施 |
|---|---|---|---|
| **Responses API 兼容性未充分验证**：sub2api 已有 `/v1/responses`，但需实测 Codex streaming SSE、tool_call、文件编辑、shell exec 等完整场景 | 高 | Sakrylle CLI / API | Phase 1 用 Claude 系 group API key 端到端验证，并核对 `usage_logs.total_cost>0`；失败时在 sub2api Responses handler 或 CLI provider 配置中修正 |
| **OIDC 基座（已于 2026-06-04 完成）** | 低 | Web、Chat、Studio | 基座已就绪（id_token/JWKS/discovery/UserInfo/ES256），剩余为客户端接入工作，不再阻塞 |
| **CLI 与上游 codex 争 `~/.codex`** | 高 | CLI / 用户本地环境 | fork 第一个 commit 必须改 `find_codex_home()` 默认路径 |
| ~~`oauth_scope_enforcement_enabled` 默认 false~~ | ~~中~~ | ~~Sakrylle API 生产~~ | ✅ **已于 2026-06-05 开启**：`oauth_scope_enforcement_enabled=true`，sub2api 已重启。新 RP 需确保 `allowed_scopes` 覆盖完整 |
| **Sakrylle Web WEBUI_NAME 后缀 bug**：`env.py:772` 强制追加 `(Open WebUI)` | 中 | Sakrylle Web | 需代码修改，Volume mount 绕不过去，不可忘记 |
| **kelivo API Key 明文存储**：SharedPreferences 无加密 | 中 | Sakrylle Chat | fork 时迁移至 flutter_secure_storage |
| **RS256 私钥管理**：密钥泄露即 id_token 全线失陷 | 高 | Sakrylle API | AES-256-GCM 加密存 DB，KEK 走 env；先发布再签发；kid 轮换窗口 |
| **channel cache 无主动失效**：TTL=10min | 低 | Sakrylle API 定价 | admin 修改 pricing 后手动重启 sub2api（已知问题，CLAUDE.md 记录） |

---

## 7. 给决策者的核心判断

1. **现有 OAuth 2.0 基础扎实**，OIDC 扩展是加法，不需要推倒重来。`access_token` 走 `api_keys` 表的现有计费/缓存路径已验证，改造不碰网关核心。

2. **Responses API 端点 blocker 已解除，但 CLI 真实兼容性仍是上线前必验项**：当前优先级从“实现 `/v1/responses` 或恢复 Chat API”转为“验证 Codex 对 sub2api Responses handler 的 streaming、tool_call、文件编辑、shell exec、计费记录等完整行为”。

3. **Sakrylle Image 可以最快上线 OIDC**：OAuth PKCE 全链路已跑通生产；服务端现已在含 `openid` scope 时返回可验签 `id_token`（2026-06-04），客户端一行 `scope` 改动 + id_token 解析即可消费。

4. **配置隔离是基础卫生，不是可选项**：CLI 若不在第一个 commit 改默认目录，会静默破坏用户系统上已有的 openai codex 配置，产生不可逆的 UX 损害。

5. **本文档集仅规划，不操作**：一切生产变更须通过 CLAUDE.md 规定的 ssh-tokyo + docker compose 审批流执行。
