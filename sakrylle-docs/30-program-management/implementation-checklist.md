---
title: 93 · Sakrylle 生态总实施 Checklist（跨产品视图）
status: canonical
scope: program
last_verified: 2026-06-06
---

# 93 · Sakrylle 生态总实施 Checklist（跨产品视图）

> 规划文档（planning only）。Sakrylle API（`sub.sakrylle.com`）与 Sakrylle Image（`image.sakrylle.com`）**已上线生产**；其余 4 个客户端（CLI / Studio / Web / Chat）为待 fork 的新产品，本地改造不触生产。本文不含任何"直接改生产配置 / 破坏性迁移 / 删用户数据"的指令；凡触及生产（sub2api `oauth_clients` / `settings` / 签名密钥、Image 生产镜像/部署）的动作一律标注 **【需审批】**。
>
> 本文是**跨产品聚合视图**，按 Phase 0–4 横向汇总六个成员的关键任务，便于统一勾选与进度对账。每条任务的**完整实施细节、file:line、验收标准**请回到对应详设文档；本文只做聚合、依赖标注与关键路径串联。
>
> **兄弟文档（交叉引用，相对文件名）**：
> - 基座：`03-sakrylle-api-oidc-architecture.md`（OIDC 基座，所有 RP 的硬前置）、`04-oauth-oidc-commercial-capabilities.md`（claims 边界）、`05-configuration-isolation-standard.md`（配置隔离）
> - 各产品：`11-sakrylle-cli-development-plan.md`、`21-sakrylle-studio-development-plan.md`、`31-sakrylle-web-development-plan.md`、`41-sakrylle-chat-development-plan.md`、`51-sakrylle-image-oidc-upgrade-plan.md`
> - 总览：`90-roadmap.md`（上线顺序，本文与其一致）、`91-risk-register.md`（风险）、`92-open-questions.md`（30 问已全部分诊：13 ✅ RESOLVED + 17 🔧 CODE-CHECK，2026-06-03）

---

## 1. 关键路径（本文据 `90-roadmap.md` §3 串联）

```
OIDC 基座（03）先行
  └─ RS256 密钥(G3) → JWKS(G2) → id_token(G4) → discovery(G1)；scope 注册(G5) 与 G3 并行
      ↓
  用 Image 升级（51）验证 OIDC（最低成本、已是生产 PKCE RP，最佳首发验证 RP）
      ↓ 基座完成后，并行启动 4 个 fork
  ┌──────────────┬──────────────┬──────────────┬──────────────┐
  CLI 一键登录    Studio          Web             Chat
  （11，依赖基座   （21，复用 CLI  （31，原生      （41，PKCE
   loopback/      凭据；OIDC 为   OIDC SSO）       移动 scheme）
   public client） 增强项）
```

**两条可"最早独立启动、不依赖 OIDC 基座"的支线**（与基座并行，见 `90` §1.2 / `05` Phase 1）：
- CLI **配置隔离**（`SAKRYLLE_CLI_HOME` + 品牌字符串，`11` Phase 1/2）—— 不依赖 IdP。
- Studio **品牌/隔离**（bundle id + 端口 + Sentry/updater，`21` Phase 2）—— 纯字符串/配置。
- Image **Phase 0 client 侧准备**（占位符注入 + feature flag 脚手架，`51` Phase 0）—— 不依赖 server。

**全局判据**：所有"是否依赖 OIDC 基座(03)"以该任务能否在 `03` Phase 1（G1–G5）未落地时独立完成为准。所有"触生产"以是否写 sub2api 生产 `oauth_clients`/`settings`/密钥、或更新 Image 生产镜像/部署为准（API 与 Image 为生产）。

---

## Phase 0 · 调研与保护（串行前置；以只读核查与 fork 护栏为主）

> 目标：确认 `92` 的 7 个 P0 问题、锁定命名/issuer 决策、建立隔离与回滚护栏。本阶段几乎全部为只读核查或 fork 仓库内操作，**不触生产**（只读查询除外）。

### 0.A OIDC 基座 / API 侧（`03` Phase 0、`92` 类别 A/B/G）

- [x] **Q-01** ✅ RESOLVED（2026-06-03）：**`security_secrets` 表已存在（migration 053）、仅无非对称签名密钥** → G3 RS256+ES256 私钥复用该表（新增 key 行、加密 at-rest，无需建表）— 产品：API（`92` Q-01 / `03` §7）
- [x] **Q-02** ✅ RESOLVED（2026-06-03）：`oauth_scope_enforcement_enabled` 默认 false 为**有意过渡态** → 分阶段开启，Phase 4 A11 切 `true`（需额外审批）— 产品：API（`91` R-PROD-02 / `92` Q-02）
- [x] **Q-03** ✅ RESOLVED（2026-06-03）：issuer **冻结 `https://sub.sakrylle.com`**（用户拍板）；Phase 1.A 开工前仅做一次生产值只读核对（**只读不改**）— 产品：API（`03` §6 / `91` R-KEY-01）
- [ ] **Q-06** 🔧 CODE-CHECK：留待 Phase 1.A 开工时确认 replay 防护生产路径走 `mintRepo`（非 legacy 非原子 fake 路径）— 产品：API｜代码核查（`03` §12 R4 / `92` Q-06）
- [x] **Q-22** ✅ RESOLVED（2026-06-03 核实；2026-06-04 已实现）：原核实「`/oauth/token` 当时不签发 `id_token`」→ id_token 签发（G4）**已实现**，Image OIDC 升级不再被基座阻塞 — 产品：API/Image（`92` Q-22）
- [x] issuer 决策已冻结 `https://sub.sakrylle.com`（Q-03）；Phase 1.A 前核对 148 seed `oauth_issuer` 一致 — 产品：API（`03` §6 / `90` §3.2）
- [ ] 核对 client seed 命名 `sakrylle-desktop`（148）vs `sakrylle-studio`（建议统一，bundle 统一 `com.sakrylle.*`）— 产品：API/Studio｜🔧 CODE-CHECK（`03` §9 / `21` P0-7）

### 0.B CLI（`11` Phase 0、`92` 类别 C）

- [ ] **Q-09** 全 workspace grep `\.codex`/`find_codex_home`，确认无残留硬编码漏改点 — 产品：CLI｜不依赖 OIDC 基座｜并行｜代码核查（`05` §13 / `91` R-CONF-01 / `92` Q-09）
- [ ] **Q-10** 确认 `CODEX_SQLITE_HOME` 是否独立于 `CODEX_HOME`（SQLite 隔离完整性）— 产品：CLI｜并行｜代码核查（`92` Q-10）
- [ ] 建立 CLI fork 仓库 + 构建基线（`cargo build` 跑通，**不改代码**）— 产品：CLI｜不依赖 OIDC 基座（`11` Phase 0）
- [ ] 【需审批·只读】只读复核 `sakrylle-cli` client seed（redirect_uris/scopes/pkce/device_flow），产出"seed 现状 vs 期望"差异表 — 产品：CLI｜触生产侧只读（`11` Phase 0 / §6）
- [x] CLI 命名已锁定 — **Q-11** ✅ RESOLVED（2026-06-03）：bin `sakrylle` + 短别名 `skl`、`SAKRYLLE_CLI_HOME`、daemon `sakrylle-cli-daemon`、`DEFAULT_ORIGINATOR=sakrylle_cli`、bundle `com.sakrylle.*`；遥测/Sentry 默认关闭（Q-12）；Studio 首发复用 CLI 凭据 — 产品：CLI（`05` §8 / `92` Q-11/Q-12）

### 0.C Studio（`21` Phase 0、`92` 类别 D）

- [ ] **Q-13** 确认是否引入 `tauri-plugin-store`（第三持久化路径）— 产品：Studio｜不依赖 OIDC 基座｜代码核查（`92` Q-13）
- [ ] **Q-14** 确认 `tauri.ios/windows.conf.json` 是否含独立 identifier — 产品：Studio｜代码核查（`92` Q-14）
- [ ] **Q-15** 实测 `app-server` JSON-RPC 协议兼容性（**首发最高风险，P0-4**）— 产品：Studio｜依赖 CLI Phase 1 产出二进制｜代码核查 + 实测（`21` P0-4 / `92` Q-15）
- [ ] Fork Studio 仓库 + 基线 `pnpm tauri dev` 启动上游原版；确认 CSS 方案 / Sentry 空值禁用 / CI 现状 — 产品：Studio（`21` P0-1/2/5/6）

### 0.D Web（`31` Phase 0、`92` 类别 E/H）

- [ ] **Q-16** 实测 open-webui authlib discovery fallback（是否兼容 RFC 8414，可能大幅省 Web OIDC 工作量）— 产品：Web｜代码核查 + 本地实测（`31` P0-4 / `92` Q-16）
- [x] **Q-25** ✅ RESOLVED（2026-06-03）：Sakrylle Web 域名 = **`chat.sakrylle.com`**（用户拍板）；redirect_uri = `https://chat.sakrylle.com/oauth/oidc/callback`（路径待 Q-17 实现期核实）；Nginx + CF DNS 按此配置 — 产品：Web（`92` Q-25）
- [ ] **Q-18** 确认 `ENABLE_OAUTH_PERSISTENT_CONFIG` DB vs env 优先级 — 产品：Web｜代码核查（`92` Q-18）
- [ ] Fork Web 仓库 + 基线；【需审批·只读】只读查 `sakrylle-web` client 是否已注册；确认 Tailwind/CSS 主题变量路径 — 产品：Web（`31` P0-1/3/5）

### 0.E Chat（`41` Phase 0、`92` 类别 F/H）

- [ ] **Q-19** 🔧 CODE-CHECK：回调 scheme 已定 `sakrylle-chat://oauth/callback`；仅余"是否已在 148 seed 预置（否则新 migration）"核实，留待 Chat Phase 0 读 `148_oauth_v2_sakrylle_seed.sql` — 产品：Chat｜代码核查（`92` Q-19）
- [x] **Q-20 / Q-26** ✅ RESOLVED（2026-06-03）：**保留 iOS Live Activity（灵动岛）**（用户拍板）→ `GenerationActivityExtension` 不删除，改 bundle id 时同步改扩展 bundle id + App Group（`com.sakrylle.chat.*`）— 产品：Chat（`92` Q-20/Q-26）
- [ ] **Q-21** 🔧 CODE-CHECK：留待 Chat Phase 0 读 `windows/runner/Runner.rc` + `linux/CMakeLists.txt` 验证路径隔离 — 产品：Chat｜代码核查（`92` Q-21）
- [x] **Q-30** ✅ RESOLVED（2026-06-03）：**不发布 Flutter Web 平台**（用户拍板）→ 仅原生端，OAuth 统一走自定义 scheme，无需 Web redirect 方案 — 产品：Chat（`92` Q-30）
- [ ] Fork Chat 仓库 + 基线 `flutter build apk --debug` 通过 — 产品：Chat（`41` 0-1）

### 0.F Image（`51` Phase 0、`92` 类别 G）

- [ ] **Q-23** 🔧 CODE-CHECK：client_id 已定沿用 `sakrylle-image-playground`（不改）；仅余生产 compose env + `inject-api-url.sh` 占位符只读核实，留待 Image Phase 0 — 产品：Image｜生产只读查询（`51` Phase 0 / `92` Q-23）
- [x] **Q-24** ✅ RESOLVED（2026-06-03 核实；2026-06-04 已实现）：原核实「`/v1/me` 当时无 `sub` 字段」→ `sub` claim（G7）**已实现**，含 `openid` scope 时返回顶层 OIDC `sub` — 产品：Image/API（`92` Q-24）
- [x] **Q-29** ✅ RESOLVED（2026-06-03）：**`email:read` 对第一方 client 默认授予**（用户拍板）→ 第一方 client `scope=openid email` 时返回 email claim；落实于 144/148 seed `allowed_scopes` — 产品：API/Image/Web/Chat（`92` Q-29）
- [ ] `vite-env.d.ts` 补三变量声明（OAUTH_BASE / CLIENT_ID / OIDC_ENABLED）— 产品：Image｜不依赖 OIDC 基座（`51` Phase 0 / `05` §5）
- [ ] Dockerfile 补 `ENV` 占位符 + `inject-api-url.sh` 补 sed 替换 + feature flag 读取脚手架（默认 false）+ 注入回归测试 — 产品：Image｜不依赖 OIDC 基座（`51` Phase 0）

### ✅ Phase 0 验收标准

1. `92` 的 **30 问全部分诊完毕**（13 ✅ RESOLVED + 17 🔧 CODE-CHECK，0 ❓ 待确认，2026-06-03）。原 7 个 P0 中 Q-01/02/03/22 已 RESOLVED，Q-06/09/19 转实现期自查。
2. issuer（`https://sub.sakrylle.com`）、CLI 命名（`sakrylle`/`skl`）、Web 域名（`chat.sakrylle.com`）、签名算法（RS256+ES256）等关键决策冻结；CLI/Studio/Web/Chat 四仓 fork 完成且基线可构建。
3. CLI 隔离残留点核查清零（Q-09）；Studio `app-server` 协议兼容性结论产出（Q-15，首发阻断项）。
4. Image client 侧占位符 + feature flag 脚手架就位，flag 默认 false 时生产行为逐字节一致。
5. 所有触生产动作仅做**只读核查**，零写操作。

---

## Phase 1 · 最小可用集成

> 目标：(a) sub2api 落地可验签 id_token + discovery（OIDC 基座核心）；(b) 用 Image 升级在生产冒烟验证 OIDC；(c) CLI/Web 各自先以"不依赖 OIDC"的数据面跑通。**基座是后续所有 SSO 的硬前置。**

### 1.A OIDC 基座（`03` Phase 1 — 关键路径首项，依赖 Phase 0）✅ **[已完成 2026-06-04]**

- [x] RS256 密钥对生成 + AES-256-GCM 加密存储 + kid（G3）— **[✓ 2026-06-04]** 产品：API｜复用 `security_secrets` 表
- [x] `/.well-known/jwks.json` 端点（G2）— **[✓ 2026-06-04]** 产品：API｜RS256 + ES256 密钥发布
- [x] 注册 `openid`/`profile`/`email` 规范 scope（G5）— **[✓ 2026-06-04]** 产品：API｜scope 定义已完成
- [x] id_token 签发（scope 含 openid 时签 RS256 JWT，回填 nonce）（G4/G6/G8）— **[✓ 2026-06-04]** 产品：API｜含 nonce + auth_time
- [x] `/.well-known/openid-configuration` 端点（G1）— **[✓ 2026-06-04]** 产品：API｜Discovery 端点已实现
- [x] `/v1/me` 返回 OIDC UserInfo（含 `sub` 字段）— **[✓ 2026-06-04]** 产品：API｜scope-gated claims
- [x] RP-Initiated Logout (`/oauth/logout`)— **[✓ 2026-06-04]** 产品：API｜含 server-side session 清理
- [x] `prompt=none` 静默认证 — **[✓ 2026-06-04]** 产品：API｜含 consent check
- [x] **ES256 运行时按 client 签发** — **[✓ 2026-06-04]** 产品：API｜migration 151 `signing_algorithm` 已映射 ent schema + codegen，repo 映射进服务模型，`maybeSignIDToken` 解析 client 算法（空值/未知值安全回退 RS256）经 `OIDCKeyService.Sign` 签发；Discovery `id_token_signing_alg_values_supported` 如实列 `["RS256","ES256"]`
- [x] **EC（ES256）对称轮换 + grace-period 清理** — **[✓ 2026-06-04]** 产品：API｜新增 `OIDCKeyService.RotateECKey`（生成新 EC key、旧 key 移入 EC previous-kids、更新指针，写入顺序与 RSA 一致防悬空指针）；`CleanupExpiredKeys` 同时清理 RSA + EC 过期 key 返回合并计数；JWKS 宽限期内同时发布 RSA + EC 的 current + previous（dual-kid）；删除零引用死代码 `SignRS256`（改用算法感知 `Sign`）；新增 `TestOIDCECKeyRotation`、`TestOIDCECKeyCleanup`、`TestOIDCCleanupBothKeyTypes`（含 `-race`，5×）全部通过。**自动调度器**（2026-06-05）：`OIDCKeyRotationScheduler` 双 goroutine（rotation + cleanup），配置项 `oidc_auto_rotation_enabled`/`oidc_key_rotation_interval_hours`/`oidc_key_cleanup_interval_hours`；手动触发接口仍保留
- [x] **测试修复** — **[✓ 2026-06-04]** 产品：API｜`TestOIDCKeyCleanup`、`TestOIDCKeyService_StorageFailureDuringRotation`、`TestSecuritySecretsOIDCKeyStore_Integration_WithOIDCKeyService`、`TestSecuritySecretsOIDCKeyStore_DecryptFailure_Handling` 全部通过；修复 P-256 JWK 坐标零填充 bug（RFC 7518 §6.2.1.2）；新增 per-client ES256 签发测试

**注**：OIDC 基座核心 + ES256 per-client 签发已全部完成并测试通过，客户端（Image/Web/CLI/Chat）现可开始 OIDC 接入。**剩余项**：(a) `oauth_scope_enforcement_enabled` 开启（生产决策，需审批，见 A11）；(b) 客户端产品 OIDC 接入；(c) migration 151 生产上线 + per-client `signing_algorithm` 配置（运营步骤，直接 SQL）。

### 1.B Image OIDC 升级（`51` Phase 0–3）✅ **[已完成 2026-06-05，commit ed0c141]**

- [x] Phase 0：占位符注入 + `OIDC_ENABLED` feature flag scaffold — **[✓ 2026-06-05]** 产品：Image｜14 文件改动，+539 行
- [x] Phase 1：OIDC Discovery + `openid profile email` scope（gated）— **[✓ 2026-06-05]** 产品：Image
- [x] Phase 2：id_token 解析 + nonce + identity merge — **[✓ 2026-06-05]** 产品：Image｜测试 268→281（+13 OIDC 用例）
- [x] Phase 3：refresh 保留 id_token + RP-Initiated Logout — **[✓ 2026-06-05]** 产品：Image
- [x] **【已审批】** sub2api 侧 `openid profile email` 已加入 image client `allowed_scopes`（migration 150，生产已确认）— **[✓ 2026-06-05]** 产品：API/Image
- [x] **【已审批】** image client `logout_redirect_uris` 配置为 `image.sakrylle.com` — **[✓ 2026-06-05]** 产品：API/Image
- [x] **【已审批】** `OIDC_ENABLED=true` 生产灰度（compose env 已配置） — **[✓ 2026-06-05]** 产品：Image
- [x] **【已审批】** `oauth_scope_enforcement_enabled=true` 开启 — **[✓ 2026-06-05]** 产品：API｜sub2api 已重启

### 1.C CLI 数据面（`11` Phase 1，不依赖 OIDC，可与基座并行）

- [ ] `find_codex_home()` 改读 `SAKRYLLE_CLI_HOME`（默认 `~/.sakrylle-cli`，`CODEX_HOME` 保留 fallback）— 产品：CLI｜**不依赖 OIDC 基座**｜并行｜**第一项必须先落地（防污染 `~/.codex`）**（`11` Phase 1 / `05` Phase 1 / `91` R-CONF-01）
- [ ] 新增 `SAKRYLLE_API_KEY` env（优先于 `CODEX_API_KEY`/`OPENAI_API_KEY`）— 产品：CLI｜不依赖 OIDC 基座（`11` Phase 1）
- [ ] 默认 model provider 指向 Sakrylle（`base_url=api.sakrylle.com/v1`、`wire_api=responses`、`requires_openai_auth=false`）— 产品：CLI｜不依赖 OIDC 基座（`11` Phase 1）
- [ ] 端到端冒烟（API key 模式，Claude 系 group key，验 `usage_logs` 有计费行）— 产品：CLI｜不依赖 OIDC 基座（`11` Phase 1 / `91` R-RESP-01）

### 1.D Studio 最小集成（`21` Phase 1，依赖 CLI Phase 1）

- [ ] codexBin 默认 fallback 探测 `sakrylle`→`skl`（非上游 `codex`）— 产品：Studio｜**依赖 CLI Phase 1（硬阻断）**｜串行（`21` P1-1）
- [ ] spawn 时注入 `SAKRYLLE_CLI_HOME` 给 CLI 子进程 + sessions 用量扫描路径对齐 — 产品：Studio｜依赖 CLI Phase 1（`21` P1-2/P1-3）
- [ ] 端到端冒烟（握手→线程→消息→响应→用量），CLI 数据落 `~/.sakrylle-cli` — 产品：Studio｜依赖 CLI Phase 1（`21` P1-4）

### 1.E Web 数据面（`31` Phase 1，不依赖 OIDC，密码登录模式先上线）

- [ ] 修复 `WEBUI_NAME` 后缀 bug（删 `env.py:772-773`）+ 改 `APP_NAME`(`constants.ts:4`) + 页面 title(`app.html:118`)（3 处必须改码）— 产品：Web｜**不依赖 OIDC 基座**｜串行（`31` P1-1/2/3 / `91` R-CONF-03）
- [ ] 准备品牌图片资源（favicon/splash/logo/manifest，volume mount）— 产品：Web｜并行（`31` P1-4）
- [ ] **【需审批】** Docker Compose 配置（隔离 DATA_DIR + 接入 Sakrylle API）+ Nginx conf + DNS — 产品：Web｜触生产部署（`31` P1-5/6/7）
- [ ] GHA 构建 `ghcr.io/ranshen1209/sakrylle-web` — 产品：Web｜不依赖 OIDC 基座（`31` P1-8）

### ✅ Phase 1 验收标准（对应 `90` M1）

1. **M1-a/b/c** ✅ **[已满足 2026-06-04]**：`/.well-known/openid-configuration` + `/.well-known/jwks.json` 可被标准 OIDC RP 库自动发现并验签；`scope=openid` 时 `/oauth/token` 返回可验签 id_token。
2. **M1-d** ✅ **[已满足 2026-06-05]**：Image 客户端 Phase 0–3 全部完成（commit ed0c141），生产 `OIDC_ENABLED=true`，`oauth_scope_enforcement_enabled=true`，Image 是第一个完成 OIDC 全链路接入的 RP。
3. CLI（API key 模式）端到端跑通 `api.sakrylle.com/v1/responses`，`usage_logs` 有真实计费行；与上游 codex 同机零冲突（`~/.sakrylle-cli`）。⏳ 待 fork
4. Studio 默认启动 Sakrylle CLI 二进制、完整监控链路可用。⏳ 待 fork
5. Web 以密码登录模式上线，接入 Sakrylle API，品牌无 `(Open WebUI)` 后缀。⏳ 待 fork
6. access_token（`sk_oauth_`）形态、网关计费、auth 缓存路径**零回归**（`04` §12 / `91` R-TOKEN-03）。✅ 已验证

---

## Phase 2 · 品牌与配置隔离（各 fork 天然并行，多数不依赖 OIDC 基座）

> 目标：4 个 fork 完成品牌替换（Monet 紫 `#9181bd` + 樱花 logo）+ 配置隔离（目录/bundle id/端口/前缀），以及 sub2api consent 页品牌化与各 RP client 注册。

### 2.A OIDC 基座品牌/client 注册（`03` Phase 2）

- [ ] consent 页品牌化（Monet 紫 + 樱花 logo + ￥ 规则，保留 XSS 防护）— 产品：API｜不依赖 OIDC 基座（与 Phase 1 部分并行）（`03` Phase 2）
- [ ] **【需审批】** client 注册补全（5 RP：CLI/Studio/Web/Chat/Image 的 redirect_uri 白名单 + scope + pkce；替换 144/148 seed）— 产品：API｜触生产 `oauth_clients`（`03` Phase 2 §9 / `91` R-PROD-03）

### 2.B CLI 品牌（`11` Phase 2，与 OIDC 登录可并行，不依赖基座）

- [ ] 二进制名 + CLI 帮助文本（`sakrylle`/`skl`）+ npm 包封装 — 产品：CLI｜不依赖 OIDC 基座｜并行（`11` Phase 2）
- [ ] TUI/status/onboarding 品牌字符串 + originator/UA + 货币(￥)/遥测指向 fork — 产品：CLI｜不依赖 OIDC 基座（`11` Phase 2 / **Q-12** 遥测 opt-out 机制）

### 2.C Studio 品牌/隔离（`21` Phase 2，无外部依赖，可立即启动）

- [ ] bundle id `com.sakrylle.studio`（驱动三平台目录隔离）+ productName/窗口标题/Rust 元数据 — 产品：Studio｜不依赖 OIDC 基座｜串行最优先（`21` P2-1/2 / `05` §8）
- [ ] 前端品牌字符串（~20 处）+ Monet 主题 + 樱花图标 + localStorage 前缀迁移（含兜底）+ daemon 端口 4733 — 产品：Studio｜不依赖 OIDC 基座｜并行（`21` P2-3/4/5/6/7）
- [ ] **【发布阻断·安全】** Sentry DSN 禁用（删硬编码）+ updater endpoint→fork + 重新生成 minisign pubkey — 产品：Studio｜不依赖 OIDC 基座（`21` P2-8/9 / `91` R-CRED-03）

### 2.D Web 品牌/隔离已在 Phase 1 核心完成；主题色 + 收紧归入 Phase 3。

### 2.E Chat 品牌/隔离 + 存储安全（`41` Phase 0–2）

- [ ] bundle id 四处替换 `com.sakrylle.chat`（Android/iOS/macOS/Win/Linux）+ pubspec 包名 + 应用名字符串 — 产品：Chat｜不依赖 OIDC 基座｜各平台并行（`41` 0-2~0-7 / 1-3 / `05` §8）
- [ ] 预置 Sakrylle API 内置 provider（首位、`isUserAdded` 固定集合、默认配置）— 产品：Chat｜不依赖 OIDC 基座（`41` 1-1/1-2；**「不确定」balance 字段路径，需实测**）
- [ ] Monet Purple Material3 调色板设为默认 + 樱花应用图标 — 产品：Chat｜不依赖 OIDC 基座（`41` 1-4/2-1）
- [ ] **【安全】** API key 迁移至 `flutter_secure_storage`（含一次性迁移）+ SiliconFlow fallback key 审查 — 产品：Chat｜不依赖 OIDC 基座（`41` 2-2/2-3 / `91` R-CRED-01）

### ✅ Phase 2 验收标准

1. 4 个 fork 均与各自上游 + codex 在同机零冲突（目录/bundle id/端口/socket/localStorage 不重叠，`05` §12）。
2. 全部用户可见品牌为 Sakrylle 系（Monet 紫 + 樱花 + fork 链接），无上游残留。
3. **安全阻断项全部完成**：Studio Sentry 不外发 + updater 自有 pubkey；Chat API key 加密存储。
4. 5 RP client 在 sub2api 注册项与 `03` §9 一致（**【需审批】** 已留痕）。
5. consent 页品牌化、无 XSS 回归。

---

## Phase 3 · 完整 OIDC / 各 RP SSO 接入 / 权限 / 审计（依赖 Phase 1 基座）

> 目标：基座补齐 UserInfo/Logout/silent auth；各 RP 接入 OIDC 登录；scope enforcement 开启前置核查。

### 3.A OIDC 基座补齐（`03` Phase 3）✅ **[核心已完成 2026-06-04，高级特性 2026-06-05]**

- [x] UserInfo OIDC 分支（授 `openid` 必返顶层 `sub`；商业状态留 scoped account/group 块，**绝不进 id_token**）(G7) — **[✓ 2026-06-04]** 产品：API（`03` G7 / `04` §6 / `91` R-TOKEN-03）
- [x] RP-Initiated Logout `/oauth/logout`（白名单校验，不当 open redirector）(G10) — **[✓ 2026-06-04]** 产品：API
- [x] `prompt=none` silent auth (G9) — **[✓ 2026-06-04]** 产品：API
- [x] **Pairwise Subject Identifier** (G12) — **[✓ 2026-06-05]** 产品：API（`oidc_pairwise.go` + migration 153，OIDC Core §8）
- [x] **sector_identifier_uri 获取** (G13) — **[✓ 2026-06-05]** 产品：API（`oidc_pairwise.go:150-218`，OIDC Core §8.1）
- [x] **Request Object / request_uri** (G14/G15) — **[✓ 2026-06-05]** 产品：API（`oidc_request_object.go` + migration 155，OIDC Core §6）
- [x] **Claims Parameter** (G16) — **[✓ 2026-06-05]** 产品：API（`oidc_claims_enforcement.go` + migration 154，OIDC Core §5.5）
- [x] **Back-Channel Logout** (G17) — **[✓ 2026-06-05]** 产品：API（`oidc_backchannel_logout.go` + migration 156，OIDC Back-Channel Logout 1.0）
- [x] **Front-Channel Logout** (G18) — **[✓ 2026-06-05]** 产品：API（`/oauth/frontchannel-logout` + migration 161，OIDC Front-Channel Logout 1.0）
- [x] **Token Introspection** (G19) — **[✓ 2026-06-05]** 产品：API（`POST /oauth/introspect` + migration 160，RFC 7662）
- [x] **Session ID (sid)** (G20) — **[✓ 2026-06-05]** 产品：API（migration 158 + `oidc_id_token.go`）
- [x] **at_hash / c_hash** (G21) — **[✓ 2026-06-05]** 产品：API（`oidc_id_token.go:48-82`，OIDC Core §3.1.3.8 / §3.3.2.11）
- [x] **per-user email_verified** (G22) — **[✓ 2026-06-05]** 产品：API（migration 157，OIDC Core §5.1）
- [x] **Signed UserInfo JWT** (G23) — **[✓ 2026-06-05]** 产品：API（`oidc_userinfo_jwt.go`，OIDC Core §5.3.2）
- [x] **Consent Grant 跟踪** (G24) — **[✓ 2026-06-05]** 产品：API（migration 159，第三方客户端授权记录）
- [x] **自动密钥轮换调度器** (G25) — **[✓ 2026-06-05]** 产品：API（`oidc_key_rotation.go`，双 goroutine rotation + cleanup）
- [ ] （可选）`client_credentials` grant（服务账号）(G11) —— 非 OIDC 核心，暂不实现 — 产品：API（`03` G11 / `04` §9）

### 3.B CLI OIDC 一键登录（`11` Phase 3 — 核心，依赖基座；可与品牌并行）

- [ ] loopback PKCE 一键登录主流程（issuer/client_id/scope 指向 Sakrylle → 拉起浏览器 → 随机/兜底端口 loopback 回调 → state/PKCE 校验 → code 换 token → 成功页 → 凭据 0600 落盘 → 端到端零复制粘贴）— 产品：CLI｜**依赖 OIDC 基座（G1–G5；未就位则先走纯 OAuth2 不验 id_token，留 TODO）**｜串行链（`11` Phase 3a / `03` §9）
- [ ] Device Flow 降级 `--device`（改端点对接 sub2api `/oauth/device/code`）— 产品：CLI｜依赖基座｜可与 3a 并行（`11` Phase 3b）
- [ ] refresh_token 静默续期（落盘轮换后新 token）+ `sakrylle logout` 吊销（RFC 7009）— 产品：CLI｜依赖 3a 落盘（`11` Phase 3c）
- [ ] **【需审批】** `sakrylle-cli` client loopback redirect_uri 白名单 / scope 补 seed（含"通配 loopback 端口 vs 精确白名单"决策）— 产品：CLI/API｜触生产 `oauth_clients`（`11` §6 待审批点 / **Q-05** 是否已有生产用户）

### 3.C Studio 认证（`21` Phase 3）

- [ ] 分支 A（**首发**）：读 `~/.sakrylle-cli/auth.json` 反映认证状态 + 隐藏 ChatGPT 登录误导 UI — 产品：Studio｜**不依赖 OIDC 基座**｜串行（`21` P3-A1/A2）
- [ ] 分支 B（增强）：`sakrylle-studio` client 注册【需审批】+ 改 `codex_login_core` 走 Sakrylle OIDC loopback 登录 + token 安全存储 — 产品：Studio｜**依赖 OIDC 基座 Phase 1**（`21` P3-B1/B2）

### 3.D Web OIDC SSO（`31` Phase 2–3，依赖基座）

- [ ] **【需审批】** 注册 `sakrylle-web` 机密 client（client_secret bcrypt）+ 确认平铺 userinfo 字段映射（`OAUTH_EMAIL_CLAIM`/`OAUTH_USERNAME_CLAIM`）— 产品：API/Web｜**依赖 OIDC 基座**｜触生产（`31` P2-1/P2-2 / **Q-17/Q-29**）
- [ ] `.env` 填 OIDC SSO 配置（`OPENID_PROVIDER_URL` 指 sub.sakrylle.com）+ SSO 全流程验证 — 产品：Web｜依赖基座（`31` P2-3/P2-4）
- [ ] Monet 主题色 + per-user API key（可选）+ backchannel logout（可选）+ 关闭社区分享等 — 产品：Web｜部分依赖基座（`31` P3-1~P3-5）

### 3.E Chat OIDC（`41` Phase 3，依赖基座 + client 注册）

- [ ] OAuth 依赖 + 各平台自定义 URL scheme 注册（`com.sakrylle.chat://oauth/callback`；桌面 loopback）— 产品：Chat｜**依赖 OIDC 基座**（`41` 3-1/3-2 / **Q-19**）
- [ ] PKCE 授权流程核心（`SakrylleOAuthService`）+ access_token 注入 provider + 登录 UI 入口 — 产品：Chat｜依赖基座（`41` 3-3/3-4/3-5）
- [ ] **【需审批】** sub2api 注册 `sakrylle-chat` 公共 client（PKCE 强制、自定义 scheme + loopback 白名单）— 产品：API/Chat｜触生产 `oauth_clients`（`41` 3-6 / `92` Q-19）

### 3.F Image OIDC 身份/会话（`51` Phase 2–3，dev/preview，依赖基座 G4/G8/G7/G10）

- [ ] id_token 解析（仅读 payload，不验签）+ nonce 校验 + 身份来源切换（优先 claims，balance 仍 `/v1/me`）— 产品：Image｜依赖基座 G4/G8/G7｜dev/preview（`51` Phase 2 / `91` R-CRED-02 已知）
- [ ] 刷新处理 id_token + RP-Initiated Logout（软依赖 G10）+ 多 tab 同步 — 产品：Image｜部分依赖基座 G10｜dev/preview（`51` Phase 3）
- [ ] **【需审批】** image client `logout_redirect_uris` 配置 — 产品：API/Image｜触生产（`51` Phase 3）

### ✅ Phase 3 验收标准（对应 `90` M2–M5 的 OIDC 部分 + M6 雏形）

1. UserInfo 授 `openid` 必返 `sub`；余额绝不进 id_token（`04` claims 原则）。
2. CLI 一键登录全程零复制粘贴（基座就位则验 id_token；未就位走纯 OAuth2 + TODO）。
3. Studio 首发走复用 CLI 凭据；Web/Chat 通过 OIDC SSO 登录并计费正常。
4. 各 RP client 注册项与 `03` §9 一致，所有生产写操作经审批。
5. id_token claims 严守 `04` §6 清单，无 token-only 放行路径（`91` R-TOKEN-03 / R-PROD-02）。

---

## Phase 4 · 测试 / 发布 / 回滚（串行收尾，生产动作需审批）

> 目标：OIDC 一致性测试、各 fork 同机共存冒烟、生产灰度上线、回滚预案。

- [ ] OIDC 一致性测试（discovery/JWKS/id_token/UserInfo/nonce/PKCE 全覆盖，80%+）— 产品：API｜串行（`03` Phase 4 / `90` M6-d）
- [ ] CLI 单元/集成测试（隔离/auth/登录流/refresh rotation/logout）+ 三平台一键登录与 device flow 实测 — 产品：CLI（`11` Phase 4）
- [ ] Studio 同机共存冒烟 + 多平台打包 + CI（minisign 签名）发布 — 产品：Studio（`21` Phase 4）
- [ ] Web 集成测试清单（SSO/模型/计费/品牌/WebSocket/数据隔离）— 产品：Web（`31` P4-1）
- [ ] Chat 多平台 release 构建 + 功能冒烟（含 OAuth 登录续期登出）— 产品：Chat（`41` 4-1/4-2）
- [ ] **【需审批】** Image 灰度：生产 server OIDC 就绪性预检 → 推送 OIDC 镜像（flag 关）→ 切 flag `true` → 灰度验证监控 — 产品：Image/API｜触生产部署（`51` Phase 4 / `91` R-PROD-04）
- [ ] **【需审批】** scope enforcement 开启（`oauth_scope_enforcement_enabled=true`，先全量验证各 RP scope 覆盖）— 产品：API｜触生产 `settings`（`90` M6-c / `91` R-PROD-02 / **Q-02**）
- [ ] 安全审查（密钥管理 / token 存储 / XSS）无 P0/P1 未缓解 — 产品：全生态（`90` M6-e）
- [ ] 各产品回滚预案（OIDC 全为叠加：基座回滚 = RP 暂不请求 openid + access_token 路径零回归；Image = 翻 flag false 重启；CLI/Studio/Chat = 不发布/卸载/client `disabled=true`；Web = 删 OIDC env 退回密码登录）— 产品：全生态（`03`/`51`/`11`/`21`/`31`/`41` 各 Phase 4）

### ✅ Phase 4 验收标准（对应 `90` M6）

1. OIDC 一致性测试通过，6 个产品 SSO 链路可端到端验证（CLI 走 device flow / loopback）。
2. 各 fork 与上游同机零冲突；多平台构建/发布/CI 到位。
3. 生产灰度（Image OIDC、scope enforcement）全程**【需审批】** 留痕，计费/`sk_oauth_` 零回归。
4. 任意异常可回滚，回滚后状态与升级前一致；安全审查无高危未缓解项。

---

## 2. sub2api 生产侧需审批动作（单列汇总）

> 以下均为触及 sub2api 生产（`oauth_clients` / `settings` / 签名密钥）或 Image 生产部署的动作，**执行前必须审批**，遵循 `CLAUDE.md` 审批流（`ssh-tokyo` + 直接 SQL / `docker compose`）。密钥/SQL 写操作先在 preview 演练，用 `INSERT ... ON CONFLICT DO UPDATE` 原子更新避免残留旧值（`91` R-PROD-03）。

| # | 动作 | 对象 | 触发 Phase | 状态 |
|---|---|---|---|---|
| A1 | RS256 私钥生成 + KEK(`OIDC_KEY_ENCRYPTION_KEY`) 注入 | 签名密钥 / `.env` | 1.A G3 | ✅ 已完成 2026-06-04 |
| A2 | issuer 只读核对（**只读，不改**） | `settings.oauth_issuer` | 0.A Q-03 | ✅ 已完成 2026-06-03 |
| A3 | image client `allowed_scopes` 加 `openid profile email` | `oauth_clients` | 1.B | ✅ 已完成 2026-06-05（migration 150，生产已确认） |
| A4 | consent 品牌化随发布上线 + 5 RP client 注册（替换 144/148 seed） | `oauth_clients` | 2.A | ⏳ 待执行 |
| A5 | `sakrylle-cli` loopback redirect_uri / scope 补 seed | `oauth_clients` | 3.B | ⏳ 待 CLI fork |
| A6 | `sakrylle-studio` client 注册（增强分支 B） | `oauth_clients` | 3.C | ⏳ 待 Studio fork |
| A7 | `sakrylle-web` 机密 client 注册 + Web 容器/Nginx/DNS | `oauth_clients` / 部署 | 1.E / 3.D | ⏳ 待 Web fork |
| A8 | `sakrylle-chat` 公共 client 注册 | `oauth_clients` | 3.E | ⏳ 待 Chat fork |
| A9 | image client `logout_redirect_uris` 配置 | `oauth_clients` | 3.F | ✅ 已完成 2026-06-05 |
| A10 | Image feature flag 灰度切 `true` | 容器 env | 4 | ✅ 已完成（compose env `OIDC_ENABLED=true`） |
| A11 | scope enforcement 开启 | `settings` | 4 | ✅ 已完成 2026-06-05（sub2api 已重启） |
| A12 | client 回滚 = `disabled=true` | `oauth_clients` | 4 回滚 | 备用 |

> **共性约束**：所有 `oauth_clients` 写操作改完后须按 `CLAUDE.md` 重启 sub2api（channel/缓存）或发 Redis Pub/Sub invalidate（auth 缓存）；redirect_uri 走精确白名单（loopback/localhost 端口需显式列出，除非确认支持通配 loopback —— **不确定**，`11` §7 待核 `oauth_provider_service.go` 匹配策略）。

---

## 3. 未决问题对实施的阻断映射（速查）

> 完整 30 问见 `92-open-questions.md`。30 问已于 2026-06-03 全部分诊（13 ✅ RESOLVED + 17 🔧 CODE-CHECK，0 ❓）。下表列原"卡住某 Phase 起步"的关键项的最新状态——**已无开放决策阻断项**，余项均为实现期自查。

| 问题 | 阻断 | 状态（2026-06-03）|
|---|---|---|
| Q-01 security_secrets 表 | G3 密钥存储路径 | ✅ RESOLVED：表已存在（migration 053），复用、加密 at-rest、无需建表（已确认 2026-06-03）|
| Q-02 scope enforcement 意图 | Phase 4 A11 开启决策 | ✅ RESOLVED：有意过渡态，分阶段开启 |
| Q-03 生产 issuer 值 | discovery `issuer` 一致性 | ✅ RESOLVED：冻结 `https://sub.sakrylle.com`；Phase 1.A 前只读核对 |
| Q-06 mintRepo replay wiring | 安全前置（不能带漏洞签 id_token） | 🔧 CODE-CHECK：Phase 1.A 开工自查 |
| Q-09 codex `.codex` 残留 | CLI 隔离完整性 | 🔧 CODE-CHECK：CLI Phase 0/1 grep 自查 |
| Q-15 app-server 协议兼容 | Studio 能否对接 CLI（首发阻断） | 🔧 CODE-CHECK：Studio Phase 0 实测（需 CLI 二进制）|
| Q-19 sakrylle-chat redirect_uri | Chat OAuth 实现前置 | 🔧 CODE-CHECK：scheme 已定，余 seed 核实 |
| Q-22 /oauth/token 是否已返 id_token | Image 排期 | ✅ RESOLVED：id_token 签发（G4）已实现（2026-06-04），不再阻塞 |
| Q-25 Web 域名 | Web client/Nginx/DNS | ✅ RESOLVED：`chat.sakrylle.com` |
| Q-11 CLI 二进制名 | Studio codexBin 默认值 / 安装脚本 | ✅ RESOLVED：`sakrylle`（+`skl`）|

---

## 4. 备注与不确定项

- **本文不重复 file:line 级实施细节**：聚合视图刻意只到任务粒度，避免与详设文档漂移。勾选某条前请读对应文档的"实施说明 + 验收标准"。
- **「不确定」集中转交 `92`**：本文未新增臆造结论；所有「不确定」均指回 `92` 对应 Q 编号或对应详设文档原文。
- **关键路径以 `90-roadmap.md` 为准**：若本文与 `90` 顺序出现分歧，以 `90` 为准并回填本文。
- **OIDC 全程"叠加"原则**：基座与各 RP 改造均不改 `sk_oauth_` access_token 形态、不动网关计费/缓存/限流路径（`04` §7 / `03` §10 Phase 4）；这是"可低风险回滚"的根本保证。
