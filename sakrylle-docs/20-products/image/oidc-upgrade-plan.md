---
title: 51 · Sakrylle Image — OAuth2 → OIDC 升级方案
status: local
scope: product
last_verified: 2026-06-06
---

# 51 · Sakrylle Image — OAuth2 → OIDC 升级方案

> **🎉 更新（2026-06-05）**：Phase 0–3 全部完成（commit ed0c141），生产灰度 `OIDC_ENABLED=true` + `oauth_scope_enforcement_enabled=true`。本文档作为升级记录保留。
>
> 文档编号: 51 | 状态: ~~规划~~ **✅ 全部完成** | 日期: 2026-06-03 | 更新: 2026-06-05
>
> 关联文档:
> - [50-sakrylle-image-research.md](./50-sakrylle-image-research.md)（Sakrylle Image 现状调研，本文所有 client 侧 file:line 引用源）
> - [02-sakrylle-api-oauth-current-state.md](./02-sakrylle-api-oauth-current-state.md)（IdP 侧 OAuth2 现状）✅ **已更新为 OIDC 完成状态**
> - [03-sakrylle-api-oidc-architecture.md](./03-sakrylle-api-oidc-architecture.md)（IdP 侧 OIDC 基座目标）✅ **已全部实现**

---

## 0. 阅读须知 / 强约束

**本文是 client 侧（Sakrylle Image = `gpt_image_playground` fork）的 OAuth2 → OIDC 升级执行方案。** Sakrylle Image（`image.sakrylle.com`）与 Sakrylle API（`sub.sakrylle.com`）**均已上线生产**。

- **不破坏生产**：所有改动先在 dev/preview 环境落地、走 feature flag 灰度、保留可回滚路径。凡触及生产部署（Vercel / Cloudflare / Docker 镜像）或 sub2api 生产侧 OAuth client 配置的任务，均显式标注 **「需审批」**。
- **不要求兼容旧登录态**：升级期间可强制用户重新登录（清空 `localStorage['sakrylle-image-playground.auth']`），但**不得**让生产用户在升级未完成时陷入登录中断。这是本方案采用「server 先行、client feature flag 默认关」灰度顺序的根本原因。
- **关键耦合（来自调研 §8 / 文档 50 §5.4 / 文档 03 §5.4 R3）**：client 单侧加 `openid` scope 而 server 未注册该 scope 时，sub2api 的 `NormalizeScopes` 会**静默丢弃** `openid`（`oauth_scopes.go:25-37` 不含 `openid`）。当前行为是「丢弃后授权仍成功，但不返回 id_token」（静默降级，不报错）—— **但这依赖 server 端 scope 校验对未知 scope 的宽容策略**。一旦 server 端启用严格 scope 校验（`allowed_scopes` 精确比对，拒绝未注册 scope），client 先加 `openid` 会导致**授权请求被拒、登录中断**。因此：**client 端启用 `openid` 必须 gated 在 feature flag 之后，且仅在确认 server 已注册 `openid` 规范 scope 并已加入本 client `allowed_scopes` 后才打开。**
- **品牌一致性**：主色 Monet Purple `#9181bd`；`￥` 仅展示（不转换数值，见 sub2api CLAUDE.md Currency policy）；樱花 logo / Liquid Glass UI 保持。

---

## 1. 现状 → 目标差距速览

引用自文档 50 §5.1（client 侧缺口）：

| # | 缺口 | client 侧位置 | 升级动作 |
|---|---|---|---|
| C1 | 无 `openid` scope | `src/lib/sakrylleAuth.ts:16`（`SCOPE` 常量） | Phase 2 加 `openid profile email`（gated） |
| C2 | 无 nonce | `beginLogin()` `src/lib/sakrylleAuth.ts:229` 不生成 nonce | Phase 2 生成 + 持久化 + 回调校验 |
| C3 | 无 id_token 字段与解析 | `SakrylleAuthToken` 接口 `:25-41` 无 `idToken`；`handleCallback` `:248` 不读 | Phase 2 加字段 + 解析 payload |
| C4 | 身份依赖私有 `/v1/me`（当前 `/v1/me` 无 `sub`，`oauth_provider_account_handler.go:65-71`，须 03 基座补） | `SakrylleMePayload` `src/lib/sakrylleAccount.ts:38-53` 无 `sub` | Phase 2 用 id_token claims 取身份（硬依赖 OIDC 基座 03） |
| C5 | 无 Discovery | OAuth 端点硬编码 `:10-11` | Phase 1 引入 `/.well-known/openid-configuration` |
| P0-1 | `VITE_SAKRYLLE_OAUTH_BASE` 无运行时注入 | `deploy/Dockerfile`、`deploy/inject-api-url.sh`、`src/vite-env.d.ts` | Phase 0 补齐占位符 |
| P0-2 | `VITE_SAKRYLLE_OAUTH_CLIENT_ID` 无运行时注入 | 同上 | Phase 0 补齐占位符 |

**目标**：Sakrylle Image 作为合规 OIDC RP，通过 `scope=openid profile email` 取得 id_token，使用 Discovery（`/.well-known/openid-configuration`）发现端点，从 id_token claims（`sub`/`name`/`email`/`preferred_username`）取用户身份，UserInfo（`/v1/me`）取实时数据（balance/group），**替代/补充**当前完全依赖 `/v1/me` 拿身份的方式。

---

## 2. 跨文档依赖关系（关键，决定灰度顺序）

> **🎉 更新（2026-06-04）**：所有 IdP 侧依赖已满足！

本方案的 client 侧改造**强依赖** sub2api IdP 侧 OIDC 基座先落地。映射到文档 03 的 Gap / Phase：

| client 侧任务 | 依赖的 IdP 侧基座（文档 03） | 依赖性质 | 状态 |
|---|---|---|---|
| Phase 1 用 Discovery | 文档 03 G1（`/.well-known/openid-configuration`） | **硬依赖**：server 无该端点则 client Discovery 失败 | ✅ **已实现** |
| Phase 2 请求 `openid` scope | 文档 03 G5（注册 `openid`/`profile`/`email` 规范 scope）+ client `allowed_scopes` 含 `openid` | **硬依赖**：否则静默丢弃或被拒 | ✅ **已实现** |
| Phase 2 取 id_token | 文档 03 G4（id_token 签发）+ G2/G3（JWKS + RS256/ES256 密钥） | **硬依赖**（原：provider 不签发 id_token） | ✅ **已实现** |
| Phase 2 校验 nonce | 文档 03 G8（authorize 接收并回填 nonce） | **硬依赖**：server 不回填则 nonce 校验恒失败 | ✅ **已实现** |
| Phase 2 用 UserInfo `sub` | 文档 03 G7（UserInfo OIDC 分支，授 `openid` 必返 `sub`） | **硬依赖**（原：`/v1/me` 无 `sub`） | ✅ **已实现** |
| Phase 3 登出 | 文档 03 G10（`/oauth/logout` RP-Initiated Logout） | 软依赖（无则保留现有本地 logout + RFC 7009 revoke） | ✅ **已实现** |

> **✅ 基座依赖已全部满足（2026-06-04）**：文档 03 的 IdP 侧 OIDC 基座（G1-G10）已完整实现——provider 已签发 id_token、`/v1/me` 已返回 `sub`、Discovery/JWKS/Logout 端点全部就位。**Sakrylle Image 的 client 侧升级现在可以立即开始，无需等待 IdP 侧任何工作。**

---

## 3. 客户端 OAuth client 注册现状对齐

来自文档 02 §4.4：当前 sub2api 已 seed 两个 image client：

| client_id | seed migration | redirect_uris | 当前 scope（不含 openid） |
|---|---|---|---|
| `sakrylle-image-playground` | 144 | `image.sakrylle.com/oauth/callback`, `localhost:5173` | `images:create`, `account:balance:read`, `models:read`, `offline_access` |
| `sakrylle-image-playground-v2` | 148 | `https://image.sakrylle.com/oauth/callback` | 同上 |

**client 实际固定使用** `sakrylle-image-playground`（`src/lib/sakrylleAuth.ts:11` fallback 常量），bundle 内硬编码。

> **决策（已确认 2026-06-03）**：本次升级**沿用现有 client_id `sakrylle-image-playground`**，**不新建 `-v2`**。旧登录态不做迁移，靠 feature flag（§4）灰度切换。因此 §3 的工作就是在该现有 client 的 `allowed_scopes` 上**追加 `openid profile email`**（原 `images:create` / `account:balance:read` / `models:read` / `offline_access` 全部保留）。Phase 0 仍核实生产 bundle 实际 client_id 与运行时注入形态，以确认其确为 `sakrylle-image-playground`、避免改错 `allowed_scopes`。

文档 03 §9 建议 image client 目标 scope：`openid profile email image_generation balance:read models:read`。注意 `image_generation`/`balance:read` 是 legacy alias（文档 02 §4.5 → 映射到 `images:create`/`account:balance:read`），client 当前用的是 canonical 名（`images:create`）。**保持 canonical 名，仅追加 `openid profile email`。** 其中 `email`（OIDC `email` scope）对第一方 client **默认授予**（已确认 2026-06-03）——授权后可直接从 id_token claim 或 UserInfo `/v1/me` 取 `email`，无需额外同意步骤。

---

## 4. Feature Flag 设计（贯穿全 Phase）

为满足「不破坏生产 + 可灰度 + 可回滚」，引入单一 client 侧开关控制是否走 OIDC：

- **开关名**：`VITE_SAKRYLLE_OIDC_ENABLED`（运行时可注入，复用 Phase 0 的占位符机制）。
- **语义**：`false`（默认）= 当前 OAuth2 行为完全不变（scope 不含 `openid`，不解析 id_token，身份继续走 `/v1/me`）；`true` = 走 OIDC（加 `openid`、解析 id_token、Discovery）。
- **读取**：通过 `src/lib/runtimeEnv.ts` 的 `readRuntimeEnv` 包装（与现有 `OAUTH_BASE`/`CLIENT_ID` 同款）。
- **默认 false 的意义**：即便 client 新版本先于 server 上线，只要 flag 默认关，生产行为与今天逐字节一致（零回归）。打开 flag 是一次纯配置/灰度操作，无需重新发版。

> 设计原则遵循全局 coding-style：KISS（单一布尔开关，不做复杂分级）、不臆造 server 端 flag（client flag 独立于 sub2api 的 `oauth_provider_enabled` 等 settings）。

---

## 5. 分阶段实施计划

> 标注约定：**[串行]** / **[并行]** 指该 Phase 内任务的执行关系；**[依赖 OIDC 基座]** 指依赖文档 03 IdP 侧改造；**「需审批」** 指触及生产部署或生产 sub2api 配置。

---

### Phase 0 · 调研与保护（前置，[串行]，**不依赖 server**）

**目标**：摸清生产现状、补齐运行时注入机制（OAUTH_BASE / CLIENT_ID / OIDC flag）、搭建 feature flag 脚手架，使后续 OIDC 改动可灰度、可回滚、不重建镜像换配置。

**依赖项**：无（纯 client 侧 + 只读核查生产）。可与 sub2api 文档 03 Phase 0/1 完全并行。

**风险（生产）**：
- 新增 Dockerfile `ENV` 占位符若 sed 替换写错，可能导致容器启动注入失败 → 白屏。缓解：新占位符与现有 fallback 并存，fallback 值不变（见文档 50 §8「OAUTH_BASE 占位符变更」风险，影响低）。
- 误判生产实际 client_id → Phase 1/2 改错 `allowed_scopes`。缓解：client_id 已确认沿用 `sakrylle-image-playground`（不新建 `-v2`，已确认 2026-06-03），本 Phase 仅核对生产 bundle 与该值一致。

**任务清单**：

- [ ] 核实生产实际 client_id 与部署形态
  - 目标：确认生产 Sakrylle Image 实际 client_id（**已确认 2026-06-03 沿用 `sakrylle-image-playground`，不新建 `-v2`**；本任务为核对生产 bundle 确为该值），以及 `docker-compose.yml` 是否通过 `environment:` 注入了 `DEFAULT_API_URL` 等。
  - 涉及文件：`/opt/stack/docker-compose.yml`（服务器，只读）；本地 `src/lib/sakrylleAuth.ts:11`（fallback 常量）。
  - 实施说明：SSH `ssh-tokyo`，只读 `docker compose config` / `cat docker-compose.yml` 对应 image 服务的 `image:` 与 `environment:` 节；解决文档 50 §10 后续问题 1/2。**只读，不改任何生产配置。**
  - 验收标准：核对生产 bundle client_id 为 `sakrylle-image-playground`（与决策一致）并记录现有 env 注入清单；若 client 当前未被运行时覆盖（占位符未注入），结论为「固定 fallback `sakrylle-image-playground`」。

- [ ] `vite-env.d.ts` 补声明三变量
  - 目标：让 `VITE_SAKRYLLE_OAUTH_BASE` / `VITE_SAKRYLLE_OAUTH_CLIENT_ID` / `VITE_SAKRYLLE_OIDC_ENABLED` 有构建期类型（当前为 `any`，文档 50 §5.2）。
  - 涉及文件：`src/vite-env.d.ts:6-17`。
  - 实施说明：在 `ImportMetaEnv` 接口追加三个 `readonly ...?: string`。不改任何运行时逻辑，纯类型声明。
  - 验收标准：`tsc --noEmit` 通过；`import.meta.env.VITE_SAKRYLLE_OAUTH_BASE` 类型为 `string | undefined`。

- [ ] Dockerfile 补 `ENV` 占位符
  - 目标：使 OAUTH_BASE / CLIENT_ID / OIDC flag 可运行时注入，换端点/client_id 不重建镜像（文档 50 §5.2、§7 P0）。
  - 涉及文件：`deploy/Dockerfile:6-10`。
  - 实施说明：在现有 5 个 `ENV` 占位符后追加 `ENV VITE_SAKRYLLE_OAUTH_BASE=__VITE_SAKRYLLE_OAUTH_BASE__`、`VITE_SAKRYLLE_OAUTH_CLIENT_ID=__VITE_SAKRYLLE_OAUTH_CLIENT_ID__`、`VITE_SAKRYLLE_OIDC_ENABLED=__VITE_SAKRYLLE_OIDC_ENABLED__`（占位符命名沿用现有 `__VAR__` 风格——**需先核对现有占位符实际格式**，文档 50 仅说「占位符」未给出确切 token 写法）。
  - 验收标准：构建产物含三个占位符 token，可被 sed 命中。
  - 「不确定」：现有占位符确切格式（`__VAR__` vs `${VAR}` vs 其他）须先 Read `deploy/Dockerfile` 与 `inject-api-url.sh` 确认后再定。

- [ ] `inject-api-url.sh` 补 sed 替换行
  - 目标：容器启动时把占位符替换为容器 env 实值。
  - 涉及文件：`deploy/inject-api-url.sh:21-25`。
  - 实施说明：对每个新占位符追加一行 sed，回退逻辑：env 未设置时替换为现有 bundle fallback（`https://sub.sakrylle.com` / `sakrylle-image-playground` / `false`），保证行为与今天一致。
  - 验收标准：本地 `docker run` 注入测试：不传 env → fallback 值；传 env → 覆盖生效；占位符无残留。

- [ ] feature flag 读取脚手架
  - 目标：建立 `VITE_SAKRYLLE_OIDC_ENABLED` 单一读取点，后续所有 OIDC 分支 gated 于此。
  - 涉及文件：`src/lib/sakrylleAuth.ts`（新增 `const OIDC_ENABLED = readRuntimeEnv(import.meta.env.VITE_SAKRYLLE_OIDC_ENABLED) === 'true'`，置于 `:10-16` 常量区）。
  - 实施说明：仅定义常量 + 导出，本 Phase 不接任何分支逻辑（分支在 Phase 2 接）。默认 `false`。
  - 验收标准：`OIDC_ENABLED` 可被单测覆盖（mock env）；默认 `false`。

- [ ] 占位符注入回归测试
  - 目标：保证新增占位符不影响现有 5 个占位符注入。
  - 涉及文件：新增 `deploy/` 下注入冒烟脚本或 `src/lib/sakrylleAuth.test.ts` 扩展。
  - 实施说明：断言三新占位符 + 五旧占位符全部正确替换。
  - 验收标准：注入测试通过，CI 绿。

**Phase 0 验收标准（整体）**：换 OAuth 端点 / client_id / 开关 OIDC flag 均无需重建镜像；feature flag 默认 false 时生产行为与升级前逐字节一致；生产实际 client_id 已核实记录。

---

### Phase 1 · OIDC 最小接入：Discovery + openid scope（dev/preview，[依赖 OIDC 基座 G1/G5]）

**目标**：在 **dev/preview 环境**（`localhost:5173` / preview 部署）打通 OIDC discovery，使 client 能从 `/.well-known/openid-configuration` 解析端点，并在 flag 打开时请求 `scope=openid`。**不动生产**。

**依赖项**：
- **[依赖 OIDC 基座]** 文档 03 G1（server 已上线 `/.well-known/openid-configuration`）+ G5（server 已注册 `openid`/`profile`/`email` 规范 scope，且 `sakrylle-image-playground` 的 `allowed_scopes` 已含 `openid`）。
- Phase 0 全部完成。

**风险（生产）**：
- 本 Phase 限定 dev/preview，生产 flag 保持 `false`，生产零影响。
- Discovery 失败（server 端点未就绪或网络问题）须 fail-safe 回退到硬编码端点，不能因 Discovery 失败导致登录完全不可用。

**任务清单**：

- [ ] [并行] 新增 OIDC Discovery 拉取模块
  - 目标：从 issuer `https://sub.sakrylle.com` 的 discovery 端点 `${OAUTH_BASE}/.well-known/openid-configuration`（已确认 2026-06-03：issuer = `https://sub.sakrylle.com`，与 `OAUTH_BASE` 同源）获取 `authorization_endpoint`/`token_endpoint`/`userinfo_endpoint`（UserInfo 端点）/`jwks_uri`/`issuer`/`end_session_endpoint`。
  - 涉及文件：新增 `src/lib/sakrylleOidcDiscovery.ts`（遵循 many-small-files 原则，不塞进 `sakrylleAuth.ts`）。
  - 实施说明：带超时 + 内存缓存（discovery 不必每次拉，缓存 TTL 如 1h）；**fail-safe**：拉取失败时回退到现有硬编码 `OAUTH_BASE` 衍生端点（`/oauth/authorize`、`/oauth/token`），保证 flag 关时与今天一致、flag 开时 Discovery 故障也不致命。校验 `issuer === https://sub.sakrylle.com` 且与 `OAUTH_BASE` 同源（防端点劫持）。
  - 验收标准：mock fetch 单测覆盖成功 / 超时 / 字段缺失 / issuer 不匹配四路径；fail-safe 回退验证通过。

- [ ] [串行，依赖上一项] `beginLogin` 端点来源切换为 Discovery（gated）
  - 目标：flag 开时用 Discovery 的 `authorization_endpoint`，flag 关时用现有硬编码。
  - 涉及文件：`src/lib/sakrylleAuth.ts:229`（`beginLogin`）。
  - 实施说明：`if (OIDC_ENABLED) { endpoints = await getDiscovery() } else { 走现有逻辑 }`。**不改 flag 关时的任何代码路径。**
  - 验收标准：flag 关 → 现有 19 个 `sakrylleAuth.test.ts` 用例全过（零回归）；flag 开 → 用 Discovery 端点。

- [ ] [串行] `SCOPE` 常量条件追加 `openid profile email`（gated）
  - 目标：flag 开时请求 `openid profile email` + 现有 scope；flag 关时 scope 与今天一致。
  - 涉及文件：`src/lib/sakrylleAuth.ts:16`。
  - 实施说明：`const SCOPE = OIDC_ENABLED ? 'openid profile email ' + V2_SCOPES : V2_SCOPES`。保持现有 canonical scope 名（`images:create` 等，见 §3），仅前置 `openid profile email`。
  - 验收标准：flag 关 → scope 字符串与现状逐字节一致；flag 开 → 含 `openid profile email`；preview 环境授权请求被 server 接受（不被拒、不静默丢 openid）。

- [ ] [需审批] sub2api 侧把 `openid` 加入 image client `allowed_scopes`
  - 目标：在现有 client `sakrylle-image-playground`（已确认 2026-06-03 沿用，不新建 `-v2`）的 `allowed_scopes` 上追加 `openid profile email`（原 scope 保留），否则 server 拒绝或丢弃。
  - 涉及文件（sub2api 侧，**仅协调，不在本仓库改**）：`backend/migrations/144_oauth_seed_sakrylle.sql` / `148_oauth_v2_sakrylle_seed.sql`（fork 部署前替换 seed，见文档 03 §10 Phase 2）；或生产直接 SQL `UPDATE oauth_clients SET allowed_scopes=...`。
  - 实施说明：**这是触生产 sub2api OAuth client 配置的操作，需审批**。先在 preview/本地 sub2api 改，验证 Phase 1 client 端通过，再走生产。遵循文档 03「server 先行」原则。
  - 验收标准：preview sub2api 上 image client `allowed_scopes` 含 `openid`；client 授权请求不被拒。

**Phase 1 验收标准（整体）**：dev/preview 环境下，flag 开时 client 能 Discovery 端点 + 请求 `openid` scope 且授权成功；flag 关时生产/本地行为零回归；Discovery 故障可 fail-safe 回退。

---

### Phase 2 · id_token / UserInfo 替换身份获取 + 品牌一致性（dev/preview，[依赖 OIDC 基座 G4/G8/G7]）

**目标**：flag 开时，client 解析 id_token、校验 nonce、从 id_token claims（`sub`/`name`/`email`/`preferred_username`）取身份，UserInfo（`/v1/me`）仅取实时数据（balance/group）；身份展示遵循 Monet 品牌。**仍限 dev/preview，生产 flag 保持 false。**

**依赖项**：
- **[依赖 OIDC 基座，硬依赖]** 文档 03 G4（server 在授 `openid` 时签发 id_token，RS256/ES256 按 client `signing_algorithm` 选择）、G8（authorize 接收并回填 `nonce`）、G7（UserInfo 授 `openid` 必返 `sub`）。**✅ 已就绪（2026-06-04）**：provider 已签发 id_token、`/v1/me` 已返回顶层 `sub`，本 Phase 的 id_token / UserInfo 取身份任务**不再被 03 基座阻塞**。
- Phase 1 完成。

**风险（生产）**：
- **id_token 签名无法在纯 SPA 安全验证**（文档 50 §5.3、文档 03 §12 R1）：SPA 无 BFF，引入 `jose` 做 JWKS 验签会增加 bundle 体积且密钥学保证有限。**决策**：本 Phase 仅**解析 payload 取 claims**（不做密码学验签），并在代码注释 + 文档明确「id_token 仅用于读身份展示，授权决策仍以 server 端 opaque access_token 为准」。这是当前用途（显示用户名）的可接受折衷（文档 50 §5.3）。验签留待未来 BFF 方案（YAGNI）。
  - **签名算法（已确认 2026-06-03）**：server 端以 **RS256 为主签名算法、ES256 为第二算法**。未来引入 client 侧 id_token 验签（BFF 阶段）时，验签实现须**同时接受 RS256 与 ES256 两者**（按 JWT header `alg` 与 JWKS `kid` 选取对应公钥），不得硬编码单一算法。当前 Phase 仅解析 payload，不受算法影响，但接口/工具设计应为双算法预留。
- nonce 校验恒失败风险：若 server 未回填 nonce（G8 未实现），flag 开时校验会失败。缓解：校验失败仅 `console.warn` + 降级到 `/v1/me`，不阻断登录（与文档 50 §8「静默降级」缓解一致）。

**任务清单**：

- [ ] [串行] `beginLogin` 生成并持久化 nonce（gated，C2）
  - 目标：flag 开时生成 nonce（crypto.getRandomValues），随 authorize 请求发送，写 sessionStorage。
  - 涉及文件：`src/lib/sakrylleAuth.ts:229`（`beginLogin`）；新 sessionStorage key `sakrylle-image-playground.oidc-nonce`（与现有 `.pkce-verifier`/`.pkce-state` 同 namespace，文档 50 §4.3）。
  - 实施说明：与现有 PKCE verifier/state 生成并列；nonce 拼入 `${authorization_endpoint}?...&nonce=<nonce>`。flag 关时不生成。
  - 验收标准：flag 开 → authorize URL 含 nonce，sessionStorage 写入；flag 关 → 无 nonce（零回归）。

- [ ] [串行] `SakrylleAuthToken` 接口加 `idToken` / `idTokenClaims` 字段（C3）
  - 目标：token 模型容纳 id_token。
  - 涉及文件：`src/lib/sakrylleAuth.ts:25-41`（接口）。
  - 实施说明：加 `idToken?: string`、`idTokenClaims?: { sub: string; name?: string; email?: string; email_verified?: boolean; preferred_username?: string; nonce?: string; iss?: string; aud?: string; exp?: number }`。可选字段，flag 关时恒 undefined，`saveToken` 序列化兼容旧值。
  - 验收标准：旧 localStorage 值（无 idToken）可正常反序列化（向后兼容）；新字段类型正确。

- [ ] [串行] `handleCallback` 解析 id_token + 校验 nonce（gated，C3/C2）
  - 目标：flag 开时从 token 响应读 `id_token`，base64url 解码 payload，校验 `nonce` 与 sessionStorage 一致、`aud === CLIENT_ID`、`iss === issuer`、`exp` 未过期。
  - 涉及文件：`src/lib/sakrylleAuth.ts:248`（`handleCallback`）；新增 `src/lib/idTokenDecode.ts`（纯 payload 解码工具，不验签，独立文件便于测试与未来替换为验签实现）。
  - 实施说明：解析失败 / nonce 不符 → `console.warn` + 降级（不写 idTokenClaims，身份回退 `/v1/me`），**不抛错中断登录**（缓解 server 未就绪风险）。校验通过后从 sessionStorage 清除 nonce（与 verifier/state 同时清，`:259-260`）。
  - 验收标准：单测覆盖 id_token 存在且 nonce 匹配 / nonce 不匹配 / id_token 缺失（flag 开但 server 未返）/ flag 关四路径；flag 关时 `handleCallback` 现有 19 用例零回归。

- [ ] [串行] 身份来源切换：优先 id_token claims，回退 `/v1/me`（C4）
  - 目标：flag 开且有 `idTokenClaims` 时，用户名/显示名/邮箱来自 claims（`name`/`preferred_username`/`email`/`sub`）；balance/group 仍走 `/v1/me`（实时数据绝不进 id_token，文档 03 §8）。
  - 涉及文件：`src/lib/sakrylleAccount.ts:38-53`（`SakrylleMePayload` / `fetchMe`）；展示侧 `src/components/Header.tsx`。
  - 实施说明：在 `fetchMe` / 身份选择处加 `if (OIDC_ENABLED && token.idTokenClaims) { 身份字段取 claims }`，balance/currency/group 字段不变（继续 `/v1/me`）。降低（非消除）对 `/v1/me` 身份字段的依赖。`sub`（字符串）作为稳定用户标识，替代 `user_id`（数字）。
  - 验收标准：flag 开 + claims 存在 → Header 显示来自 id_token 的用户名；balance 仍来自 `/v1/me`；flag 关 → 完全走现有 `/v1/me`（零回归）。

- [ ] [并行] 身份展示品牌一致性复核
  - 目标：新身份展示（若 UI 有变动）符合 Monet 品牌；`￥` 仅展示规则不变。
  - 涉及文件：`src/components/Header.tsx`（余额展示 `:15` 区）、`src/index.css`（Monet 紫 `--primary` `#9181bd`）。
  - 实施说明：本 Phase 身份字段来源变化通常不改 UI 结构（仍显示用户名/余额），仅核对无新增非品牌色、无误把 `￥` 改成 `$`（遵循 sub2api CLAUDE.md Currency policy）。若新增「已用 OIDC 登录」类标识，用 `#9181bd`。
  - 验收标准：视觉无回归；无非莫奈紫硬编码色；货币符号仍 `￥`。

- [ ] [并行] 测试扩展：id_token / nonce 路径
  - 目标：覆盖文档 50 §5.1 C2/C3/C4 新路径（遵循 testing.md 80%+）。
  - 涉及文件：`src/lib/sakrylleAuth.test.ts`、新增 `src/lib/idTokenDecode.test.ts`、`src/lib/sakrylleOidcDiscovery.test.ts`。
  - 实施说明：AAA 结构；mock id_token（含/不含 nonce、过期、aud 错配）；断言降级行为。
  - 验收标准：新增用例全过；现有 19+9 用例零回归；覆盖率达标。

**Phase 2 验收标准（整体）**：dev/preview flag 开时，登录后用户名来自 id_token claims、nonce 校验生效（失败优雅降级）、balance 仍实时；品牌一致；生产 flag 关时零回归。

---

### Phase 3 · 刷新 / 登出 / 会话完善（dev/preview，[部分依赖 OIDC 基座 G10]）

**目标**：让 OIDC 会话生命周期完整——刷新时正确处理 id_token、登出走 RP-Initiated Logout（若 server 支持）、多 tab 会话同步兼容 OIDC。**仍限 dev/preview。**

**依赖项**：
- **[依赖 OIDC 基座]** 文档 03 G10（`/oauth/logout` `end_session_endpoint`），软依赖：未实现则保留现有本地 logout + RFC 7009 revoke。
- Phase 2 完成。

**风险（生产）**：
- 刷新后 server 是否重发 id_token 不确定（OIDC 规范下 refresh 可选返 id_token）。缓解：刷新时若返新 id_token 则更新 claims，否则保留旧 claims（access_token 已刷新即可，身份 claims 不必每次刷）。
- RP-Initiated Logout 的 `post_logout_redirect_uri` 须在 server 白名单（`oauth_clients.logout_redirect_uris`，文档 02 §4.3）。

**任务清单**：

- [ ] [串行] 刷新路径处理 id_token（gated）
  - 目标：`refreshIfNeeded` / `forceRefreshToken` 刷新成功后，若响应含新 `id_token` 则更新 `idTokenClaims`，否则保留。
  - 涉及文件：`src/lib/sakrylleAuth.ts:390`（`refreshIfNeeded`）、`:399`（`forceRefreshToken`）。
  - 实施说明：复用 Phase 2 的 `idTokenDecode` + nonce 跳过（refresh 无 nonce，校验 aud/iss/exp 即可）。flag 关时刷新逻辑不变。
  - 验收标准：flag 开刷新 → 有新 id_token 则更新、无则保留 claims；flag 关 → 现有刷新 dedupe/retry/logout 用例零回归。

- [ ] [串行，依赖 G10] RP-Initiated Logout（gated）
  - 目标：flag 开且 Discovery 含 `end_session_endpoint` 时，登出走 `${end_session_endpoint}?id_token_hint=<idToken>&post_logout_redirect_uri=<image.sakrylle.com>`；否则保留现有 `logoutAndRevoke`（本地清 + RFC 7009 revoke，`:317`/`:338`）。
  - 涉及文件：`src/lib/sakrylleAuth.ts:310`（`logout`）、`:317`（`revokeToken`）、`:338`（`logoutAndRevoke`）。
  - 实施说明：**软依赖**——`end_session_endpoint` 不存在则不改现有行为。`post_logout_redirect_uri` 须与 server `logout_redirect_uris` 白名单一致（需审批时同步配置）。
  - 验收标准：server 支持时登出跳转 IdP 注销页再回 image；不支持时现有 revoke 行为不变。

- [ ] [需审批] sub2api 侧配置 image client `logout_redirect_uris`
  - 目标：使 `https://image.sakrylle.com/` 在 server logout 白名单内。
  - 涉及文件（sub2api 侧，**仅协调**）：`oauth_clients.logout_redirect_uris`（文档 02 §4.3，字段已存在）。
  - 实施说明：**触生产 sub2api 配置，需审批**。依赖文档 03 G10 端点先实现。
  - 验收标准：preview 验证 logout 回跳成功，不在白名单的 URI 被拒（不当 open redirector）。

- [ ] [并行] 多 tab 会话同步兼容性核查
  - 目标：现有 storage 事件多 tab 同步（文档 50 §4.5 Header `:15` 区、`localStorage` auth key）在新增 idTokenClaims 后仍正确。
  - 涉及文件：`src/components/Header.tsx`、`src/lib/sakrylleAuth.ts`（saveToken/storage 事件）。
  - 实施说明：登出/登录在一个 tab 触发后，其他 tab 的 idTokenClaims 随 auth key 同步清/更新。
  - 验收标准：多 tab 登出后所有 tab 身份态清空；无 stale claims。

**Phase 3 验收标准（整体）**：刷新正确维护 id_token；登出在 server 支持时走标准 OIDC logout、不支持时回退现有 revoke；多 tab 同步无回归；生产 flag 关时零回归。

---

### Phase 4 · 灰度发布 / 验证 / 回滚（生产，[全程需审批]）

**目标**：把 OIDC 从 dev/preview 灰度到生产 `image.sakrylle.com`，全程可观测、可回滚。

**依赖项**：
- Phase 1-3 在 dev/preview 全绿。
- **[依赖 OIDC 基座]** sub2api 生产侧文档 03 Phase 1（id_token 签发 + Discovery + JWKS + openid scope）已上线生产并验证。

**风险（生产，最高关注）**：
- **R-A（高）**：server 生产侧 OIDC 基座未完全就绪就打开 client flag → 登录中断。缓解：flag 开启前，先用脚本验证生产 `https://sub.sakrylle.com/.well-known/openid-configuration` 返回合法、`openid` 在 `scopes_supported`、image client `allowed_scopes` 含 `openid`。
- **R-B（中）**：scope enforcement（文档 03 §12 R3，`oauth_scope_enforcement_enabled`）若在 server 端开启且 image client scope 覆盖不全 → 调用被拒。缓解：开 flag 前确认 image client 已含全部所需 scope（含 `openid profile email images:create account:balance:read models:read offline_access`）。
- **R-C（中）**：灰度期间用户被迫重登（清旧 auth）。缓解：flag 切换不强制清 localStorage——旧 OAuth2 token 仍有效，下次自然刷新/登录时进入 OIDC；仅在身份字段缺失时回退 `/v1/me`（Phase 2 降级已覆盖）。
- **R-D（低）**：Cloudflare Workers 备用部署（`wrangler.jsonc`）的 env 注入机制与 nginx sed 不同（文档 50 §10 问题 6）。缓解：本灰度仅针对当前生产 Docker+nginx 方案；Workers 路径若启用需单独设计 env 注入（标注「不确定」，不在本 Phase 范围）。

**任务清单**：

- [ ] [需审批] 生产 server OIDC 就绪性预检
  - 目标：确认生产 sub2api 已具备 OIDC 基座，再考虑开 client flag。
  - 涉及文件：脚本核查（只读）。
  - 实施说明：`curl https://sub.sakrylle.com/.well-known/openid-configuration` 校验 `issuer`/`jwks_uri`/`id_token_signing_alg_values_supported`/`scopes_supported` 含 `openid`；核对生产 image client `allowed_scopes`（SSH 只读查 DB 或 admin UI）。
  - 验收标准：四项全满足才放行后续；任一不满足则**阻塞**，回退依赖文档 03 进度。

- [ ] [需审批] 构建并推送启用 OIDC 的 client 镜像（flag 仍默认 false）
  - 目标：把 Phase 0-3 代码合入生产镜像，但 `VITE_SAKRYLLE_OIDC_ENABLED` 仍 `false`（行为不变）。
  - 涉及文件：`deploy/Dockerfile`、镜像构建流程；**「不确定」是否有 GHA workflow 自动构建推送**（文档 50 §10 问题 1，未查到 `.github/workflows/`）。
  - 实施说明：**触生产部署，需审批**。先发版（flag 关）确认生产零回归，再单独切 flag。
  - 验收标准：新镜像上线后生产行为与升级前一致（flag 关）；占位符注入正确。

- [ ] [需审批] 生产 feature flag 灰度开启
  - 目标：把生产容器 `VITE_SAKRYLLE_OIDC_ENABLED` 设为 `true`（通过 compose `environment:` + 重启容器注入，无需重建镜像——Phase 0 占位符已就位）。
  - 涉及文件：`/opt/stack/docker-compose.yml`（image 服务 `environment:`）。
  - 实施说明：**触生产部署，需审批**。在低峰期切；切后立即验证一条完整登录链路。
  - 验收标准：生产新登录走 OIDC（请求含 `openid`、收到并解析 id_token、用户名来自 claims、balance 实时）；图像生成调用不受影响。

- [ ] [需审批] 灰度后验证 + 监控
  - 目标：确认无回归。
  - 涉及文件：浏览器实测 + sub2api `usage_logs`（只读，确认计费链路 `sk_oauth_` access_token 不变，文档 03 §13.5 零回归）。
  - 实施说明：实测登录/刷新/登出/图像生成；核对 access_token 仍 `sk_oauth_` opaque、计费正常。
  - 验收标准：核心流程全绿；`usage_logs` 计费正常；无 console error 风暴。

- [ ] 回滚预案（演练 + 文档）
  - 目标：任意异常可秒级回滚。
  - 涉及文件：本文档 + runbook。
  - 实施说明：**回滚 = 把生产 `VITE_SAKRYLLE_OIDC_ENABLED` 改回 `false` + 重启容器**（占位符注入，无需重建镜像）。client 立即退回纯 OAuth2 行为；旧 `sk_oauth_` token 仍有效，身份回退 `/v1/me`；access_token / 计费 / 网关路径零影响（OIDC 全为叠加，文档 03 §10 Phase 4 回滚原则一致）。无需触碰 sub2api（server 端 OIDC 端点保留无害，client 不请求 `openid` 即静默不走）。
  - 验收标准：演练验证 flag 翻回 false 后行为与升级前一致，耗时 < 1 次容器重启。

**Phase 4 验收标准（整体）**：生产 OIDC 灰度成功、计费/access_token 零回归、可一键（翻 flag）回滚；全程审批留痕。

---

## 6. 风险清单（生产，汇总）

| ID | 风险 | 概率 | 影响 | 缓解 | 关联 |
|---|---|---|---|---|---|
| R1 | client 先于 server 启用 `openid`，授权被拒/丢弃 → 登录中断 | 中 | 高 | feature flag 默认 false；server 先行；Phase 4 预检 | 文档 50 §8、文档 03 R3 |
| R2 | id_token 无法在 SPA 验签（server 签名 RS256 主 + ES256 第二算法，已确认 2026-06-03） | 确定 | 中 | 仅读 payload 取身份展示，授权仍以 server opaque token 为准；文档明确；未来 BFF 验签须同时接受 RS256/ES256 | 文档 50 §5.3、文档 03 R1 |
| R3 | nonce 未被 server 回填 → 校验恒失败 | 中 | 中 | 校验失败仅 warn + 降级 `/v1/me`，不中断登录 | 依赖文档 03 G8 |
| R4 | scope enforcement 开启后 image client scope 覆盖不全 → 调用被拒 | 中 | 高 | Phase 4 预检确认 client scope 全覆盖 | 文档 02 §4.5、文档 03 R3 |
| R5 | Dockerfile 占位符/ sed 注入错误 → 白屏 | 低 | 高 | 新旧占位符并存、fallback 不变、注入回归测试 | 文档 50 §5.2、§8 |
| R6 | Cloudflare Workers 备用部署 env 注入机制不同 | 低 | 中（仅备用启用时） | 本方案仅覆盖 Docker+nginx 生产；Workers 注入另设计（「不确定」） | 文档 50 §10 问题 6 |
| R7 | 灰度期用户被迫重登 | 中 | 低 | flag 切换不清旧 token，旧 OAuth2 token 仍有效自然过渡 | 本文 §5 Phase 4 R-C |

---

## 7. 回滚方案（汇总）

**核心原则**：OIDC 改造对 client 全为「叠加 + flag gated」，access_token（`sk_oauth_`）形态与计费路径零改动，因此回滚成本极低。

| 回滚层级 | 触发场景 | 动作 | 影响 |
|---|---|---|---|
| L1（首选） | 生产 OIDC 行为异常 | 生产 `VITE_SAKRYLLE_OIDC_ENABLED=false` + 重启容器 | client 退回纯 OAuth2；旧 token 有效；零计费影响。无需重建镜像、无需碰 server |
| L2 | 新镜像本身有问题（与 OIDC 无关） | 回滚到上一个镜像 tag | 标准镜像回滚；「不确定」具体 tag 见 §Phase 4 |
| L3 | server 端 OIDC 基座需下线 | client 已 L1 关 flag 即可；server 侧按文档 03 §10 Phase 4 回滚（保留端点无害） | 两侧独立回滚，互不阻塞 |

**回滚后状态**：与本升级开工前逐字节一致（flag 关 = 现状）。

---

## 8. 迁移步骤（端到端时序）

> 强调「server 先行、client flag 默认关、灰度切 flag」三段式。

1. **[并行，无依赖]** Phase 0 client 准备（占位符 + flag 脚手架 + 类型 + 测试）。可与 sub2api 文档 03 Phase 0/1 同时推进。
2. **[server 先行]** 等 sub2api 文档 03 Phase 1 在 **preview** 落地（id_token + Discovery + JWKS + openid scope + image client `allowed_scopes` 含 openid）。
3. **[client dev/preview]** Phase 1 → Phase 2 → Phase 3，在 dev/preview flag 开，full e2e 验证。
4. **[server 生产]** sub2api 文档 03 Phase 1 上生产并预检通过（本文 Phase 4 第 1 任务）。
5. **[client 生产，flag 关]** 发版含 OIDC 代码、flag 默认 false，确认零回归。
6. **[client 生产，灰度切 flag]** 低峰期把生产 `VITE_SAKRYLLE_OIDC_ENABLED=true`，立即验证 + 监控。
7. **[守护]** 保留 L1 回滚（翻 flag）至少一个观察周期，确认稳定后视为完成。

---

## 9. 不确定项汇总（需 Phase 0 / 跨团队确认）

1. ~~**「不确定」** 生产 Sakrylle Image 实际 client_id（`sakrylle-image-playground` vs `-v2`）~~ **已确认 2026-06-03：沿用现有 `sakrylle-image-playground`，不新建 `-v2`，旧登录态不迁、靠 feature flag 灰度。** Phase 0 仅核对生产 bundle 确为该值（文档 50 §10 问题 1/2）。
2. **「不确定」** `deploy/Dockerfile` / `inject-api-url.sh` 现有占位符确切 token 格式 —— 补占位符前须 Read 确认（本文 Phase 0）。
3. **「不确定」** 是否有 GHA workflow 自动构建推送 image 镜像 —— 影响 Phase 4 发布方式（文档 50 §10 问题 1）。
4. ~~**「不确定」** sub2api 文档 03 IdP 侧 OIDC 基座（G2/G3/G4/G5/G8/G7/G10）当前实现进度~~ **已解决 2026-06-04：IdP 侧 OIDC 基座（G1-G10）已完整实现并测试通过（含 ES256 per-client 签发）。Phase 1+ 不再被 IdP 侧阻塞。**
5. **「不确定」** server 端 scope 校验对未注册 scope 的策略（静默丢弃 vs 严格拒绝）及 `oauth_scope_enforcement_enabled` 生产值 —— 决定 R1/R4 严重度（文档 03 §14、文档 02 §6 问题 1）。
6. **「不确定」** refresh 时 server 是否重发 id_token —— 影响 Phase 3 刷新处理（OIDC 规范下可选）。
7. **「不确定」** Cloudflare Workers 备用部署是否需同步支持 OIDC env 注入 —— 不在本方案范围，启用时另设计（文档 50 §10 问题 6）。
8. ~~**「不确定」** `email:read`（OIDC `email` scope）是否已在 sub2api 端完整实现并授予第一方 client~~ **已确认 2026-06-03：`email:read` 对第一方 client 默认授予**，授权后可直接从 id_token claim 或 UserInfo（`/v1/me`）取 `email`。其完整签发仍随文档 03 OIDC 基座（G4/G7）落地（文档 50 §10 问题 4）。

---

*文档生成时间：2026-06-03 | 状态：规划完成，待 sub2api 文档 03 IdP 侧 OIDC 基座推进后开工*
