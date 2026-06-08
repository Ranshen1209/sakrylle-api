---
title: 21 · Sakrylle Studio 改造开发计划（CodexMonitor fork）
status: local
scope: product
last_verified: 2026-06-06
---

# 21 · Sakrylle Studio 改造开发计划（CodexMonitor fork）

**文档日期**：2026-06-03
**版本状态**：规划文档（planning only，**未上线产品**）。Sakrylle API / Sakrylle Image 已上线生产；Sakrylle Studio 尚未 fork，本计划**不触及任何生产系统**。任何触及生产服务端（`oauth_clients` seed、`settings` 表、签名密钥注入）的步骤均标注「需额外审批」。
**核心原则**：**尽量用配置/字符串替换而非改业务逻辑**。CodexMonitor 与上游零冲突的主抓手是 **bundle identifier**（驱动三平台数据目录）+ **codexBin 运行时配置**（指向 Sakrylle CLI 二进制），二者均无逻辑耦合，绝大多数改造是纯字符串替换与配置项调整。**不重写任何核心进程管理 / JSON-RPC 通信逻辑。**

**上游仓库**：[Dimillian/CodexMonitor](https://github.com/Dimillian/CodexMonitor)（Tauri 2.10.3 + React 19 + Vite 7 + Rust/Tokio）
**Fork 目标**：`Ranshen1209/sakrylle-studio`，分支 `theme/sakrylle`（命名与 relay-pulse / sakrylle-web fork 保持一致）。「不确定」：fork 仓库尚未创建，需在 Phase 0 操作。

**兄弟文档**：
- `20-sakrylle-studio-research.md` —— CodexMonitor 现状调研（本计划所有 file:line 引用源）
- `10-sakrylle-cli-research.md` / `11-sakrylle-cli-development-plan.md` —— Sakrylle CLI（codex fork）调研与改造计划。**Studio 依赖 CLI 二进制与 JSON-RPC 协议兼容**，是首发硬前置
- `05-configuration-isolation-standard.md` —— 配置隔离与 bundle id 规范（§4.2 路径映射、§8 命名、§6 隔离面）
- `03-sakrylle-api-oidc-architecture.md` —— OIDC 基座（Phase 3 认证打通的服务端依赖）

---

## 关键依赖关系总览

```
Phase 0（调研与保护）
    ↓ 串行
Phase 1（最小可用集成：指向 Sakrylle CLI 二进制、能启动监控）  ← 依赖 11-CLI Phase 1（二进制可运行 + app-server 协议兼容）
    ↓ 串行
Phase 2（品牌与配置隔离：改名 + bundle id + 独立数据目录）     ← 可与 Phase 1 部分并行（纯字符串/配置，不依赖 CLI 运行时）
    ↓ 串行
Phase 3（认证打通：复用 CLI 凭据，必要时独立 OIDC）           ← OIDC 分支依赖 03-OIDC Phase 1 完成；复用 CLI 凭据分支不依赖
    ↓ 串行
Phase 4（测试 / 打包 / 发布 / 回滚）
```

**外部依赖明确标注**：

| 本文 Phase | 依赖的兄弟计划 | 阻断性 |
|---|---|---|
| Phase 1 | `11-sakrylle-cli-development-plan.md` Phase 1（Sakrylle CLI 二进制 `sakrylle`/`skl` 可运行 + `app-server` 子命令兼容 codex JSON-RPC 子协议） | **硬阻断**（Studio 无 CLI 无法启动监控） |
| Phase 3（OIDC 分支） | `03-sakrylle-api-oidc-architecture.md` Phase 1（id_token / JWKS / discovery 就位）+ `sakrylle-studio` client seed | 阻断 OIDC 分支；不阻断「复用 CLI 凭据」分支 |
| Phase 2 | 无外部依赖（纯品牌/配置） | 可立即启动 |

**关键判断（已确认 2026-06-03）**：**Studio 首发认证 = 复用 CLI 凭据（分支 A，读 `~/.sakrylle-cli/auth.json`）**；Phase 1 + Phase 2 可先行完成并发布该模式的 Studio。**独立 OIDC 浏览器登录（分支 B）后置为增强项**，等 sub2api OIDC 改造（`03`）完成再做（理由见 Phase 3 §取舍）。

**实现状态（2026-06-06）**：CodexMonitor 工作树已完成 Sakrylle Studio 代码级品牌/配置隔离、Sakrylle CLI 默认发现与 `SAKRYLLE_CLI_HOME` 注入、localStorage `sakrylle-studio.*` 迁移、daemon `sakrylle-cli-daemon`/4733 隔离、Sentry 默认关闭、移除上游 updater 信任链、公开文档与设置页 i18n rebrand。仍需真实 Sakrylle CLI app-server 冒烟、生成 Sakrylle 自有 Tauri updater 签名公钥并恢复自动更新、在带 `cmake` 的环境中重跑 Rust checks、Monet 主题/图标/多平台打包签名发布。

---

## Phase 0 · 调研与保护

**目标**：在任何代码改动之前，fork 上游、固定基础版本、确认 §6 全部「不确定项」、建立迁移护栏。**全部为只读或 fork 仓库内操作，不触及生产。**

### 任务列表（串行）

- [ ] **P0-1：Fork 上游 + 固定基础分支**
  - 目标：创建 Sakrylle Studio fork，固定改造基础版本
  - 涉及操作：
    - 在 GitHub 创建 `Ranshen1209/sakrylle-studio` fork（基于 `Dimillian/CodexMonitor` 最新稳定 tag；若无 tag 则取主分支某一固定 commit）
    - 创建 `theme/sakrylle` 分支
    - 记录基础 commit hash，后续写入主仓库 `CLAUDE.md` 的 Companion services 章节（fork 部署后）
  - 实施说明：fork 后 `git clone` 到本地，路径建议 `/Volumes/APFS_HD/Documents/Github/sakrylle-studio/`（与 sakrylle-docs / sakrylle-cli 平级，便于交叉引用）
  - 验收标准：fork 存在，`theme/sakrylle` 分支创建，本地可 `pnpm install` + `pnpm tauri dev` 启动上游原版（基线可运行）

- [ ] **P0-2：确认前端 CSS 方案**（`20` 文档 §6 U2）
  - 目标：确定 Monet Purple 主题应改的位置（Tailwind config / CSS Modules / 其他）
  - 涉及文件：`package.json` devDependencies、`src/` 全局样式入口
  - 实施说明：fork 后执行 `grep -rl "tailwind" .` + 检查 `package.json`；若是 Tailwind 则定位 `tailwind.config.{js,ts}`，否则定位 CSS 变量定义文件
  - 验收标准：给出 CSS 方案结论 + Monet Purple 替换点清单，写入本文 §附录 A

- [ ] **P0-3：确认 `tauri.ios.conf.json` / `tauri.windows.conf.json` 是否含独立 identifier**（`20` 文档 §6 U4）
  - 目标：避免改了 `tauri.conf.json` 主 identifier 却漏掉平台 conf 中的覆盖值
  - 涉及文件：`src-tauri/tauri.conf.json`、`src-tauri/tauri.ios.conf.json`（若存在）、`src-tauri/tauri.windows.conf.json`（若存在）
  - 实施说明：`ls src-tauri/tauri.*.conf.json` + 对每个文件 `grep identifier`
  - 验收标准：列出所有含 `identifier` 字段的文件清单，作为 Phase 2 替换 checklist

- [ ] **P0-4：实测 `app-server` JSON-RPC 协议兼容性**（`20` 文档 §6 U1 —— **首发最高风险项**）
  - 目标：确认 Sakrylle CLI（codex fork）是否实现兼容的 `codex app-server` JSON-RPC 子协议（`initialize` / `thread/open` / `thread/sendUserMessage` / `workspace/getInfo` / `usage/getSummary`）
  - 涉及文件（CLI 侧）：Sakrylle CLI fork 的 `codex-rs/app-server/`；（Studio 侧）`src-tauri/src/backend/app_server.rs:749`（spawn）、`:646`（codexBin 解析）
  - 实施说明：**依赖 `11-CLI` Phase 1 产出二进制**。手动测试：`sakrylle app-server`，向其 stdin 发 `initialize` JSON-RPC，确认 stdout 返回兼容握手响应。若 Sakrylle CLI 仅 rebrand 未改 app-server 协议，则**天然兼容**（codex 上游协议未动）
  - 依赖：`11-sakrylle-cli-development-plan.md` Phase 1
  - 验收标准：给出「协议兼容 / 需 Rust 侧适配」结论；若需适配，量化 `app_server.rs` 改动范围并升级为 Phase 1 阻断任务

- [ ] **P0-5：确认 `VITE_SENTRY_DSN` 空值是否 disable Sentry**（`20` 文档 §6 U8）
  - 目标：决定 Sentry 处理策略（替换为 Sakrylle DSN / 删 DSN + 禁用）
  - 涉及文件：`src/main.tsx:8-9`
  - 实施说明：读完整 `src/main.tsx`，确认是否有 `enabled: !!dsn` 类似逻辑；若无，需补禁用逻辑
  - 验收标准：给出「空 DSN 已禁用 / 需补禁用代码」结论；记录首发策略（**首发建议直接禁用，删硬编码 DSN**，避免崩溃数据外泄至 Dimillian Sentry）

- [ ] **P0-6：确认持久化路径总数**（`20` 文档 §6 U9/U10）
  - 目标：确认是否引入 `tauri-plugin-store`（第三持久化路径）；确认 `.github/workflows/` 是否存在自动构建
  - 涉及文件：`package.json`、`src-tauri/Cargo.toml`、`.github/workflows/`
  - 实施说明：`grep -i "tauri-plugin-store" package.json src-tauri/Cargo.toml`；`ls .github/workflows/`
  - 验收标准：明确持久化路径数量（appData settings.json + workspaces.json + localStorage [+ store?]）；明确是否有现成 CI 可复用

- [ ] **P0-7：核对 `sakrylle-studio` vs `sakrylle-desktop` client 命名**（`03` 文档 §9 不确定项）
  - 目标：统一 OAuth client_id 命名（migration 148 seed 用 `sakrylle-desktop`，`03`/`05` 文档用 `sakrylle-studio`）
  - 涉及文件：`backend/migrations/148_oauth_v2_sakrylle_seed.sql`
  - 实施说明：**只读 SQL** 查当前生产 `oauth_clients` 是否有 `sakrylle-desktop` 行及其 `redirect_uris`；不在此 Phase 写库
  - 验收标准：给出命名对齐结论（建议统一为 `sakrylle-studio`，在 seed 替换时对齐）；记录现有 redirect_uris
  - 标注：仅只读查询；后续写库 → **需额外审批**

- [ ] **P0-8：建立迁移护栏决策**
  - 目标：明确 bundle id 迁移后旧数据处理策略
  - 涉及说明：`com.dimillian.codexmonitor` → `com.sakrylle.studio` 后，旧 `settings.json` / `workspaces.json` 不自动迁移（首次启动空白配置）。这是**全新 fork 产品**，可接受全新启动，但需启动迁移检测逻辑（见 Phase 2 P2-6）
  - 验收标准：决策写入文档 —— **首发采用「全新启动，不迁移上游 CodexMonitor 数据」**；localStorage 仅做 key 前缀一次性迁移（同实例内）

---

## Phase 1 · 最小可用集成（指向 Sakrylle CLI 二进制、能启动监控）

**目标**：Studio fork 能发现并启动 **Sakrylle CLI 二进制**（`sakrylle` / `skl`），通过 `app-server` JSON-RPC 完成握手、打开线程、收发消息，UI 能展示监控数据。**此 Phase 不改品牌、不改 bundle id**（仅验证集成可行），仅做让 Studio 默认指向 Sakrylle CLI 的最小改动。
**前置依赖**：`11-sakrylle-cli-development-plan.md` Phase 1（CLI 二进制可运行 + `app-server` 协议兼容，P0-4 已验证）。**硬阻断**。

### 任务列表（串行）

- [ ] **P1-1：codexBin 默认 fallback 指向 Sakrylle CLI**（`20` 差距 G5）
  - 目标：用户首次使用无需手动配置即可指向 Sakrylle CLI，不再 fallback 到上游 `codex`
  - 涉及文件：`src-tauri/src/backend/app_server.rs:646`（`build_codex_command_with_bin()`，当前空值 fallback 为 `"codex"`）
  - 实施说明：将空值 fallback 改为按序探测 `sakrylle` → `skl`（PATH 查找）；保留用户在 Settings → Codex 页手动覆盖 `codexBin` 的能力（运行时配置，无需重编译）。**不改 spawn / JSON-RPC 逻辑**（`app_server.rs:749` 不动）
  - 依赖：`11-CLI` Phase 1（二进制名 `sakrylle`/`skl` 确定）
  - 验收标准：未配置 `codexBin` 时，Studio 启动的是 `sakrylle app-server`（非 `codex`）；手动覆盖仍生效

- [ ] **P1-2：注入 `SAKRYLLE_CLI_HOME` 环境变量给子进程**（`05` 隔离面 #1/#11/#12）
  - 目标：Studio 启动的 CLI 子进程使用 `~/.sakrylle-cli`（而非 `~/.codex`），与上游 codex 零冲突
  - 涉及文件：`src-tauri/src/backend/app_server.rs:749`（spawn 处注入 env）、`src-tauri/src/codex/home.rs:13`（`CODEX_HOME` 解析）
  - 实施说明：spawn 时注入 `SAKRYLLE_CLI_HOME`（来自 AppSettings 或默认 `~/.sakrylle-cli`）。**与 `11-CLI` 的隔离设计对齐**：CLI fork 的 `find_codex_home()` 优先读 `SAKRYLLE_CLI_HOME`。若 CLI 尚未支持该变量（仅支持 `CODEX_HOME`），过渡期注入 `CODEX_HOME=~/.sakrylle-cli`（仍隔离，但语义不纯）；目标态用 `SAKRYLLE_CLI_HOME`
  - 依赖：`11-CLI` Phase 1（`SAKRYLLE_CLI_HOME` 支持）
  - 验收标准：Studio 启动的 CLI 子进程读写 `~/.sakrylle-cli`；同机若装上游 codex，`~/.codex` 不被 Studio 触碰
  - 标注：「不确定」—— CLI fork 是否已支持 `SAKRYLLE_CLI_HOME`，依赖 `11-CLI` 进度；过渡方案见实施说明

- [ ] **P1-3：sessions JSONL 用量扫描路径对齐**（`20` 差距 G13）
  - 目标：用量统计 UI 扫描 `~/.sakrylle-cli/sessions/` 而非 `~/.codex/sessions/`
  - 涉及文件：`src-tauri/src/shared/local_usage_core.rs:522`（`resolve_codex_sessions_root`）
  - 实施说明：该函数应基于 `SAKRYLLE_CLI_HOME`（同 P1-2）推导 sessions root。**前提**：Sakrylle CLI 产生的 JSONL schema 与上游一致（`20` U5）；若 CLI 改了 schema，用量 UI 会显示空数据 —— 需在 P0-4 / `11-CLI` 调研中确认
  - 依赖：`11-CLI` Phase 1（sessions JSONL schema 确认）
  - 验收标准：用量统计 UI 读取 Sakrylle CLI 产生的 sessions 数据并正确展示；schema 不一致时记录为已知差距（G13）

- [ ] **P1-4：端到端冒烟（最小可用验证）**
  - 目标：验证 Studio fork ↔ Sakrylle CLI 完整链路打通
  - 涉及说明：依赖 P1-1/P1-2/P1-3
  - 实施说明：`pnpm tauri dev` 启动 Studio fork → Settings 确认 codexBin 指向 sakrylle → 新建/打开工作区 → 发送一条用户消息 → 观察 `app-server-event` 推送到前端 → UI 展示响应；确认 CLI 子进程写入 `~/.sakrylle-cli`
  - 验收标准：完成「握手 → 打开线程 → 发消息 → 收响应 → 用量统计可见」全链路；CLI 数据落 `~/.sakrylle-cli`

### Phase 1 风险

| 风险 | 等级 | 缓解 |
|---|---|---|
| `app-server` JSON-RPC 协议不兼容（U1） | 高 | P0-4 提前验证；若不兼容升级为阻断任务，量化 `app_server.rs` 适配范围 |
| CLI 尚未支持 `SAKRYLLE_CLI_HOME` | 中 | 过渡注入 `CODEX_HOME=~/.sakrylle-cli`（仍隔离） |
| sessions JSONL schema 漂移 | 中 | P1-3 接受用量 UI 降级为已知差距，不阻断首发 |

### Phase 1 验收标准

1. Studio fork 默认启动 Sakrylle CLI 二进制（`sakrylle`/`skl`），非上游 `codex`。
2. CLI 子进程严格使用 `~/.sakrylle-cli`，与上游 `~/.codex` 零冲突。
3. 完整监控链路（握手/线程/消息/用量）端到端可用。

---

## Phase 2 · 品牌与配置隔离（改名 + bundle id + 独立数据目录）

**目标**：完成 Sakrylle Studio 品牌化（名称/图标/链接/主题）+ bundle id 迁移（驱动三平台数据目录隔离）+ localStorage 前缀迁移 + daemon 端口隔离 + Sentry/updater 安全处理。**与 Phase 1 部分并行**（纯字符串/配置改动，不依赖 CLI 运行时；但发布前需与 Phase 1 合并验证）。
**外部依赖**：无（纯品牌/配置层）。

### 任务列表

> 标注 **[并行]** 的任务彼此独立可并行；标注 **[串行]** 的有前后依赖。

- [ ] **P2-1：[串行，最优先] bundle identifier 迁移**（`20` 差距 G1 / `05` §8）
  - 目标：三平台数据目录从 `com.dimillian.codexmonitor` 隔离到 `com.sakrylle.studio`（bundle identifier 已确认 2026-06-03）
  - 涉及文件：`src-tauri/tauri.conf.json:identifier`；以及 P0-3 确认的 `tauri.ios.conf.json` / `tauri.windows.conf.json`（若含独立 identifier）；`src-tauri/src/bin/codex_monitor_daemonctl.rs:30`（`APP_IDENTIFIER` 常量）
  - 实施说明：`identifier` → `com.sakrylle.studio`；同步 `APP_IDENTIFIER` 常量。**改 bundle id 即完成 Tauri 层路径隔离，无需改 Rust 路径推导代码**（`app_data_dir()` 自动跟随）。目标路径见 `05` §4.2：macOS `~/Library/Application Support/com.sakrylle.studio/`
  - 验收标准：Studio fork 数据落 `com.sakrylle.studio/`，与上游 CodexMonitor 的 `com.dimillian.codexmonitor/` 完全隔离

- [ ] **P2-2：[并行] productName / 窗口标题 / Rust package 元数据**（`20` §4.7）
  - 目标：应用显示名全部改为 Sakrylle Studio
  - 涉及文件：`src-tauri/tauri.conf.json`（`productName`、`windows[0].title`）；`src-tauri/Cargo.toml`（package `name` → `sakrylle-studio`、`description` → `Sakrylle Studio — GUI for Sakrylle CLI`）
  - 实施说明：`menu.rs:67` 的 `app_name` 从 `package_info().name` 动态读取，改 Cargo.toml 即可联动；`menu.rs:339` About 窗口标题硬编码 `"About Codex Monitor"` 需单独改（见 P2-3）
  - 验收标准：窗口标题、Dock/任务栏名、菜单均显示 Sakrylle Studio

- [ ] **P2-3：[并行] 前端品牌字符串替换（约 20 处）**（`20` 差距 G7/G8，§3.2 清单）
  - 目标：消除所有用户可见的 `Codex Monitor` / `Dimillian` / `@dimillian` 引用
  - 涉及文件（逐一，引自 `20` §3.2）：
    - `src/features/home/components/Home.tsx:57`（首页标题）
    - `src/features/about/components/AboutView.tsx:5`（GitHub URL → fork）、`:49`（标题）、`:75`（署名）、`:6`（社交链接，U3 待确认内容）
    - `src/features/settings/components/sections/SettingsAboutSection.tsx:115`
    - `src/features/settings/components/sections/SettingsServerSection.tsx:186,339,393,549`
    - `src/features/settings/components/sections/SettingsCodexSection.tsx:233`（`used by CodexMonitor`）
    - `src/features/app/hooks/useWorkspaceDialogs.ts:282,299`
    - `src/features/notifications/hooks/useAgentResponseRequiredNotifications.ts:362`
    - `src-tauri/src/menu.rs:339`（About 窗口标题）
  - 实施说明：纯字符串替换 `Codex Monitor` → `Sakrylle Studio`；GitHub URL → `Ranshen1209/sakrylle-studio`；署名改为 Sakrylle 品牌（移除 `@dimillian`，AboutView.tsx:6 社交链接按 P0-3 确认内容处理）。**逐文件 diff 复核，避免漏改**
  - 验收标准：全仓 `grep -ri "codex monitor\|dimillian"` 仅剩注释/上游 license 必要引用；UI 无上游品牌可见

- [ ] **P2-4：[并行] Monet Purple 主题 + 樱花强调色**（`20` 差距 G9，§4.6）
  - 目标：引入 Sakrylle 视觉系统（紫色主导 + 樱花粉点缀，￥仅展示）
  - 涉及文件：P0-2 确认的 CSS 方案位置（Tailwind config 或 CSS 变量文件）
  - 实施说明：主色 500 `#9181bd`，梯度 50 `#f8f6fc` → 950 `#2d2640`（`05`/`20` §4.6）；主渐变 `linear-gradient(135deg, #9181bd 0%, #7b6aab 100%)`；强调色樱花粉 accent 500 `#ec6a9c` 用于 logo/高亮/CTA。复用 Sakrylle API frontend `tailwind.config.js` 的配置方式保持生态一致。`tauri-plugin-liquid-glass` 毛玻璃效果保留（U7：最低 macOS 版本待确认，若过高需降级路径）
  - 实施说明（￥规则）：Studio 若展示价格/余额，遵循主仓 Currency policy —— `￥` 仅展示、不转换底层数值
  - 验收标准：主题配色符合 Monet Purple；与 Sakrylle API frontend / Image / Status 视觉统一；暗色模式正常

- [ ] **P2-5：[并行] 应用图标替换为 cherry-blossom**（`20` 差距 G10）
  - 目标：应用图标从上游换为 Sakrylle 樱花图标
  - 涉及文件：`src-tauri/icons/`（多尺寸 `.png`/`.ico`/`.icns`）、`public/app-icon.png`（如存在）
  - 实施说明：用 Sakrylle cherry-blossom 图标重新生成全套 Tauri 图标尺寸（`pnpm tauri icon <source.png>` 可一键生成）
  - 验收标准：所有平台应用图标为 Sakrylle 樱花，无上游残留

- [x] **P2-6：[串行，依赖 P2-1] localStorage 前缀迁移 + 启动迁移逻辑**（`20` 差距 G6 / `05` 隔离面 #13）— **[✓ 2026-06-06 CodexMonitor 工作树已实现]**
  - 目标：localStorage 前缀 `codexmonitor.*` → `sakrylle-studio.*`；历史冲突前缀 `sakrylle-monitor.*` 仅作为读旧 fallback
  - 涉及文件：`src/features/threads/utils/threadStorage.ts:3-7`
  - 实施说明：改前缀常量；读新 key 优先，新 key 不存在时从旧前缀复制到新 key。settings.json/workspaces.json 因 bundle id 变更不自动迁移（P0-8 决策为全新启动，**不迁移上游数据**）；仅 localStorage 做同实例内前缀迁移
  - 验收标准：前缀迁移后旧 thread 历史可见；与上游 CodexMonitor 实例 localStorage 不混用；迁移失败有兜底（保留旧 key 不删，仅复制）

- [x] **P2-7：[并行] daemon 端口 4732 → 4733**（`20` 差距 G11 / `05` 隔离面 #11）— **[✓ 2026-06-06 CodexMonitor 工作树已实现]**
  - 目标：远程 backend daemon 端口与上游隔离
  - 涉及文件：`src-tauri/src/bin/codex_monitor_daemonctl.rs:28`（默认端口）；daemon 二进制 `src-tauri/src/bin/codex_monitor_daemon.rs`
  - 实施说明：默认端口 `4732` → `4733`；`AppSettings.remoteBackendHost` 仍可用户覆盖。**远程 backend 是否纳入首发范围属产品决策（U6）**——即便首发不主推，端口仍应改以避免同机冲突。建议 daemon 二进制/进程名一并对齐 `05` §8（`sakrylle-cli-daemon` 等价命名，「不确定」是否需重命名 bin crate，可后续）
  - 验收标准：Studio fork daemon 监听 4733，与上游 4732 不冲突

- [x] **P2-8：[并行] Sentry DSN 处理**（`20` 差距 G2，§7 高风险）— **[✓ 2026-06-06 CodexMonitor 工作树已实现：默认不初始化，需显式 DSN 才启用]**
  - 目标：消除崩溃数据外泄至 Dimillian Sentry project
  - 涉及文件：`src/main.tsx:8-9`
  - 实施说明：按 P0-5 结论 + **遥测默认关闭（已确认 2026-06-03）** —— **首发直接禁用**（删硬编码 DSN，确认空值时 Sentry 不初始化；若上游无空值禁用逻辑则补 `enabled: !!dsn`）。如未来需崩溃监控，再接 Sakrylle 自有 Sentry project（产品决策，须保持默认关闭、显式 opt-in）
  - 验收标准：无 DSN 时 Sentry 不初始化，无崩溃数据外发；构建无 Sentry 报错
  - 标注：安全相关，发布前必须完成（高风险差距 G2）

- [x] **P2-9：[并行] updater endpoint + pubkey 处理**（`20` 差距 G3，§7 高风险）— **[部分完成 2026-06-06：上游 trust chain 已移除；Sakrylle 自有 pubkey 生成仍是发布阻断]**
  - 目标：自动更新指向 fork 仓库；重新生成签名密钥对
  - 涉及文件：`src-tauri/tauri.conf.json:plugins.updater.endpoints`（→ `Ranshen1209/sakrylle-studio` releases）、`plugins.updater.pubkey`（重新生成 minisign 密钥对）；`src/features/update/utils/postUpdateRelease.ts:4,6`（GitHub releases URL）
  - 实施说明：用 `tauri signer generate` 生成新 minisign 密钥对；**私钥安全保管（绝不入库）**，公钥写 `tauri.conf.json`；CI 发布时用私钥签名。若首发不做自动更新，可暂时禁用 updater 插件（仍须移除上游 pubkey/endpoint 避免信任他人签名）
  - 验收标准：updater endpoint 指向 fork；pubkey 为 Sakrylle 自有；无上游 Dimillian 签名信任残留
  - 当前状态（2026-06-06）：CodexMonitor 工作树已移除上游 updater 配置和 pubkey，release notes 指向 Sakrylle fork；在生成 Sakrylle 自有 minisign key 并恢复配置前，自动更新保持禁用。
  - 标注：安全相关，发布前必须完成（高风险差距 G3）；私钥管理 → 谨慎处理

### Phase 2 风险

| 风险 | 等级 | 缓解 |
|---|---|---|
| localStorage 迁移逻辑有 bug 致 thread 历史丢失 | 中 | 迁移只复制不删旧 key；提供回滚（P2-6 兜底） |
| 漏改品牌字符串（约 20 处分散） | 中 | P2-3 逐文件 diff + 全仓 grep 复核 |
| Sentry/updater 未处理即发布（安全） | 高 | P2-8/P2-9 列为发布阻断项 |
| `liquid-glass` 最低 macOS 版本过高（U7） | 低 | 确认后若过高提供降级路径 |
| 前缀命名历史不一致（`sakrylle-monitor` vs `sakrylle-studio`） | 低 | 已统一为 `sakrylle-studio.*`；`sakrylle-monitor.*` 仅做 legacy fallback |

### Phase 2 验收标准

1. bundle id `com.sakrylle.studio`，三平台数据目录与上游零冲突（`05` §4.2）。
2. 全部用户可见品牌为 Sakrylle Studio，无 `Codex Monitor`/`Dimillian` 残留。
3. Monet Purple 主题 + 樱花图标到位，生态视觉统一。
4. localStorage 前缀迁移有兜底，不丢用户数据。
5. daemon 端口 4733；Sentry 不外发；updater 不再信任上游 pubkey，生成 Sakrylle 自有 pubkey 后恢复自动更新。

---

## Phase 3 · 认证打通（复用 CLI 凭据，必要时独立 OIDC）

**目标**：让 Studio 正确反映/获取用户认证状态。**首发 = 「复用 Sakrylle CLI 凭据」（分支 A，已确认 2026-06-03）**，独立 OIDC 浏览器登录（分支 B）后置为增强项。
**外部依赖**：OIDC 分支依赖 `03-sakrylle-api-oidc-architecture.md` Phase 1（id_token/JWKS/discovery）+ `sakrylle-studio` client seed（P0-7）；复用 CLI 凭据分支**无外部阻断**。

### 认证方案取舍（首发决策）

| 方案 | 机制 | 优点 | 缺点 | 首发推荐 |
|---|---|---|---|---|
| **A. 复用 CLI 凭据**（已确认首发用此 2026-06-03） | Studio 只读 `~/.sakrylle-cli/auth.json`，解析其中 API key / token 做用户信息展示；认证完全由 CLI 管理 | 零认证逻辑、零密钥持有、与 CLI 单一真相源；符合上游 Studio 现有「认证透传」架构（`20` §4.5） | Studio 无法独立触发登录（用户须先在 CLI 配置凭据） | ✅ **首发用此（已确认）** |
| **B. 独立 OIDC 浏览器登录** | Studio 走 Authorization Code + PKCE（loopback `http://127.0.0.1:<port>/callback`），指向 `https://sub.sakrylle.com/oauth/authorize`，自行持有 token | Studio 可独立登录，体验完整 | 需 sub2api OIDC 就位、需改 `codex_login_core`、Studio 须安全持有 token（密钥隔离面新增） | ⏳ 后置增强项，等 `03` OIDC 完成（已确认后置 2026-06-03） |

**推荐理由**：上游 Studio 本就**无独立认证层**（`20` 结论 4 / §4.5），auth 完全由 CLI 管理；方案 A 与该架构天然契合、改动最小、不引入 Studio 侧密钥持有风险。Sakrylle CLI 初期采用 API Key 模式（`requires_openai_auth=false`，`10-CLI` 结论 4），方案 A 直接可用。方案 B 待 `03` OIDC 基座 + CLI Device Flow（`11-CLI` Phase 3）成熟后作为体验升级。

### 任务列表

#### 分支 A：复用 CLI 凭据（首发，串行）

- [ ] **P3-A1：auth.json 读取路径对齐 + API Key 模式展示**（`20` 差距 G12，§4.5）
  - 目标：Studio 读取 `~/.sakrylle-cli/auth.json` 展示认证状态（不再读 `~/.codex/auth.json`）
  - 涉及文件：`src-tauri/src/shared/account.rs:63`（`read_auth_account`）
  - 实施说明：路径基于 `SAKRYLLE_CLI_HOME`（同 P1-2）。Sakrylle CLI 若为 API Key 模式（`requires_openai_auth=false`），auth.json 含 API key 字段而非 ChatGPT JWT —— Studio 展示「已配置 API Key」状态而非套餐信息；若 auth.json 缺失则显示「未认证」并提示用户在 CLI 配置 `SAKRYLLE_API_KEY`
  - 依赖：`11-CLI`（auth.json 字段格式确认）
  - 验收标准：Studio 正确反映 Sakrylle CLI 的认证状态（API Key 已配置/未认证）；不读 `~/.codex`

- [ ] **P3-A2：现有 OpenAI/ChatGPT 登录 UI 处理**（`20` 差距 G12）
  - 目标：避免上游绑定 OpenAI OAuth 的登录 UI（`account/login/start` → `auth.openai.com`、`plan_type`）误导用户
  - 涉及文件：`src-tauri/src/shared/codex_core.rs:653`（`codex_login_core`，JSON-RPC `account/login/start`）；前端登录入口组件
  - 实施说明：API Key 模式下 `account/login/start` 不应被触发。**首发最小改动**：隐藏/禁用「用 ChatGPT 登录」入口，引导用户走 CLI API Key 配置；**不删除** `codex_login_core` 逻辑（保留给分支 B 复用）
  - 验收标准：UI 无「Sign in with ChatGPT」误导入口；用户路径清晰指向 CLI 凭据

#### 分支 B：独立 OIDC 浏览器登录（增强，依赖 `03` OIDC）

- [ ] **P3-B1：[依赖 03-OIDC Phase 1] `sakrylle-studio` client 确认 + redirect_uri 白名单**
  - 目标：sub2api `oauth_clients` 有 `sakrylle-studio`（公共 native client），loopback redirect_uri 白名单到位
  - 涉及文件：`backend/migrations/148_oauth_v2_sakrylle_seed.sql`（P0-7 命名对齐）
  - 实施说明：按 `03` §9 —— 公共 client、PKCE S256 强制、grant `authorization_code+PKCE`（loopback）、redirect `http://127.0.0.1:<port>/callback`、scope `openid profile email models:read balance:read`
  - 依赖：`03-OIDC` Phase 1
  - 验收标准：client 注册项与 `03` §9 一致
  - 标注：写生产 `oauth_clients` → **需额外审批**

- [ ] **P3-B2：[依赖 P3-B1] 改 codex_login_core 指向 Sakrylle OIDC**（`20` 差距 G12）
  - 目标：登录流程 `authUrl` 指向 `https://sub.sakrylle.com/oauth/authorize`（替换 `auth.openai.com`）
  - 涉及文件：`src-tauri/src/shared/codex_core.rs:653`
  - 实施说明：Studio 走 loopback Authorization Code + PKCE；启动本地回调服务器接 `http://127.0.0.1:<port>/callback`，用 code+verifier 换 token。**与 CLI 不同**：CLI 首选 Device Flow（`03` §9），Studio 是桌面 GUI 用 loopback 浏览器登录更自然。token 安全持有（OS keyring 优先，`05` 隔离面 #5/#6）
  - 依赖：`03-OIDC` Phase 1（authorize/token/PKCE 就位，已有）；JWKS/id_token（`03` Phase 1）
  - 验收标准：Studio 可独立完成 Sakrylle OIDC 登录，token 安全存储，用户信息（email/group）从 UserInfo/id_token 展示

### Phase 3 风险

| 风险 | 等级 | 缓解 |
|---|---|---|
| CLI auth.json 字段格式与上游不同致解析失败 | 中 | P3-A1 依赖 `11-CLI` 确认字段；解析失败降级为「未认证」展示 |
| 分支 B 提前于 `03` OIDC 启动 | 中 | 明确分支 B 阻断在 `03` Phase 1；首发只做分支 A |
| Studio 持有 OIDC token（分支 B）新增密钥隔离面 | 中 | OS keyring 存储（`05` #5）；首发不引入（用分支 A） |

### Phase 3 验收标准

1. **首发**：Studio 正确读取 `~/.sakrylle-cli/auth.json` 反映认证状态，不读 `~/.codex`，无 OpenAI 登录误导 UI。
2. **增强（B）**：`sakrylle-studio` client 注册、Studio 可独立完成 Sakrylle OIDC loopback 登录、token 安全存储（依赖 `03` OIDC 完成）。

---

## Phase 4 · 测试 / 打包 / 发布 / 回滚

**目标**：多平台构建、与上游同机共存冒烟、CI 自动发布、回滚预案。

### 任务列表

- [ ] **P4-1：与上游 CodexMonitor 同机共存冒烟**（`05` §12 整体验收）
  - 目标：验证零冲突（数据目录 / daemon 端口 / localStorage / CLI home）
  - 实施说明：同机安装上游 CodexMonitor（`com.dimillian.codexmonitor` + 端口 4732 + `~/.codex`）与 Sakrylle Studio（`com.sakrylle.studio` + 端口 4733 + `~/.sakrylle-cli`），并行运行
  - 验收标准：两者数据目录/端口/socket/localStorage 完全不重叠；互不干扰

- [ ] **P4-2：单元 / 集成测试**（遵循项目测试规范，80% 覆盖目标）
  - 目标：覆盖改动点（codexBin 解析、CLI_HOME 注入、auth.json 路径、localStorage 迁移、品牌字符串）
  - 实施说明：Rust 侧 `cargo test`（home 解析、env 注入、sessions root 推导）；前端 Vitest（localStorage 迁移逻辑、品牌渲染）
  - 验收标准：核心改动有测试覆盖；迁移逻辑有 RED→GREEN 用例（含失败兜底）

- [ ] **P4-3：多平台打包**
  - 目标：产出 macOS（含 Apple Silicon + Intel）/ Windows / Linux 安装包
  - 涉及文件：`src-tauri/tauri.conf.json` bundle 配置；P0-3 确认的平台 conf
  - 实施说明：`pnpm tauri build` 各目标平台；macOS 需代码签名/公证（「不确定」是否有 Sakrylle Apple Developer 证书 —— 待确认，无则首发可出未签名包 + 用户手动放行）
  - 验收标准：各平台安装包可安装启动；bundle id 正确
  - 「不确定」：Apple 代码签名/公证证书是否就绪

- [ ] **P4-4：CI 自动发布**（复用 P0-6 结论）
  - 目标：push `theme/sakrylle` → GHA 构建 + 签名（minisign）+ 发布 GitHub Release
  - 涉及文件：`.github/workflows/`（复用上游或新建，按 P0-6）
  - 实施说明：CI 注入 updater 私钥（GitHub Secret）签名；release 上传 `latest.json`（updater manifest）。**与主仓 GHA→GHCR 模式不同**（Studio 是桌面 app，走 GitHub Release 而非容器镜像）
  - 验收标准：push 触发自动构建发布；updater 可从 fork release 拉到签名更新

- [ ] **P4-5：回滚预案**
  - 目标：迁移失败兜底 + 发布回退
  - 实施说明：
    - localStorage 迁移失败 → 旧 key 保留（P2-6 只复制不删），用户回退上游版本数据完整
    - bundle id 迁移 = 全新启动（P0-8），无破坏性数据操作可回滚
    - updater 私钥泄漏 → 轮换 minisign 密钥对 + 发新版（参考 `tauri signer` 流程）
    - 发布回退 → 删除/标记 pre-release 对应 GitHub Release，updater manifest 回指上一版
  - 验收标准：每条迁移/发布操作有明确回滚步骤；无不可逆数据丢失路径

### Phase 4 验收标准

1. Sakrylle Studio 与上游 CodexMonitor 同机并行零冲突（目录/端口/socket/localStorage）。
2. 核心改动有测试覆盖（迁移逻辑含失败兜底用例）。
3. 多平台安装包可发布；CI 自动构建签名发布到 fork release。
4. 所有迁移/发布有回滚预案。

---

## 优先级

- **P0**：Phase 0（调研保护，含 U1 协议兼容性 —— 首发最高风险）+ Phase 1（最小可用集成，硬依赖 `11-CLI`）。
- **P1**：Phase 2（品牌/隔离，含 Sentry/updater 安全阻断项）+ Phase 3 分支 A（复用 CLI 凭据）。
- **P2**：Phase 3 分支 B（独立 OIDC，依赖 `03`）+ Phase 4 完整 CI/多平台/回滚。

**首发最小集**：Phase 0 → Phase 1 → Phase 2 → Phase 3-A → Phase 4 基础打包。独立 OIDC（3-B）、远程 backend daemon 首发可缓。

---

## 风险汇总（跨 Phase）

| 风险 | 等级 | 来源 | 缓解 |
|---|---|---|---|
| `app-server` JSON-RPC 协议不兼容 | 高 | `20` U1 | P0-4 提前验证；不兼容则量化 Rust 适配 |
| Sentry DSN 未替换即发布 | 高 | `20` G2/§7 | P2-8 发布阻断项，首发直接禁用 |
| updater pubkey 未替换 | 高 | `20` G3/§7 | P2-9 发布阻断项，重新生成密钥对 |
| `~/.codex` 路径污染（与上游 codex 争 auth.json） | 高 | `05` R1 | P1-2 注入 `SAKRYLLE_CLI_HOME`；绝不复用 `~/.codex` |
| CLI fork 进度阻断 Phase 1 | 高 | `11-CLI` 依赖 | Phase 0 与 CLI 计划对齐时序；Phase 2 可先并行 |
| localStorage 迁移丢用户数据 | 中 | `05` R3 / `20` §7 | P2-6 只复制不删 + 兜底 |
| bundle id 迁移历史数据丢失 | 中 | `20` §7 | P0-8 决策全新启动（可接受，新产品） |
| sessions JSONL schema 漂移致用量空数据 | 中 | `20` G13/U5 | P1-3 降级为已知差距，不阻断首发 |
| daemon 端口 4732 同机冲突 | 中 | `05` R2 | P2-7 改 4733 |
| 分支 B 提前于 `03` OIDC | 中 | `03` 依赖 | 首发只做分支 A |
| Apple 代码签名证书未就绪 | 低 | P4-3 | 首发可出未签名包 + 手动放行 |
| `liquid-glass` 最低 macOS 版本过高 | 低 | `20` U7 | 确认后提供降级路径 |

---

## 整体验收标准

1. Sakrylle Studio fork 默认启动 **Sakrylle CLI 二进制**（`sakrylle`/`skl`），完整监控链路（握手/线程/消息/用量）端到端可用。
2. bundle id `com.sakrylle.studio`，CLI home `~/.sakrylle-cli`，daemon 端口 4733 —— 与上游 CodexMonitor + codex **同机零冲突**（`05` §12）。
3. 全部用户可见品牌为 Sakrylle Studio（Monet Purple + 樱花图标 + fork 链接），无上游残留。
4. Sentry 不外发崩溃数据；updater 指向 fork + 自有 minisign pubkey。
5. 首发认证走「复用 CLI 凭据」（读 `~/.sakrylle-cli/auth.json`）；独立 OIDC 为增强项（依赖 `03`）。
6. localStorage 前缀迁移有兜底，不丢用户数据；多平台打包 + CI 发布 + 回滚预案到位。

---

## 附录 A · Phase 0 待回填项

> 以下由 Phase 0 调研填充，当前为「不确定」占位。

| 编号 | 待确认项 | 对应任务 | 状态 |
|---|---|---|---|
| A1 | 前端 CSS 方案（Tailwind / CSS Modules）+ Monet 替换点 | P0-2 | 待回填 |
| A2 | 平台 conf 独立 identifier 字段清单 | P0-3 | 已核实 2026-06-06：主 `tauri.conf.json` 为 `com.sakrylle.studio`；`tauri.ios.conf.json` 为 `com.sakrylle.studio.ios`；Windows/Linux conf 无独立 identifier，title 已改为 Sakrylle Studio |
| A3 | `app-server` JSON-RPC 协议兼容结论 | P0-4 | 待回填（**首发阻断**） |
| A4 | `VITE_SENTRY_DSN` 空值禁用结论 | P0-5 | 已核实 2026-06-06：Studio 默认不初始化 Sentry，只有显式 DSN 才启用 |
| A5 | 持久化路径总数（是否有 `tauri-plugin-store`）+ CI 现状 | P0-6 | 已核实 2026-06-06：无 `tauri-plugin-store`；持久化路径为 appData settings/workspaces + WebView localStorage；CI/发布签名仍待最终配置 |
| A6 | `sakrylle-studio` vs `sakrylle-desktop` client 命名对齐 | P0-7 | 待回填 |
| A7 | localStorage 前缀最终命名（`sakrylle-studio.*`；兼容读 `sakrylle-monitor.*`） | P2-6 | 已对齐 2026-06-06：新写入统一 `sakrylle-studio.*`，`codexmonitor.*` / `sakrylle-monitor.*` 只作为 legacy fallback |
| A8 | Apple 代码签名/公证证书是否就绪 | P4-3 | 待回填 |
| A9 | `tauri-plugin-liquid-glass` 最低 macOS 版本 | P2-4 | 待回填 |

## 附录 B · 命名速查（与生态对齐）

| 项 | 值 | 来源 |
|---|---|---|
| 产品名 | Sakrylle Studio | `05` §8 |
| bundle identifier | `com.sakrylle.studio`（已确认 2026-06-03） | `05` §8 |
| fork 仓库 | `Ranshen1209/sakrylle-studio` | 本文 P0-1 |
| fork 分支 | `theme/sakrylle` | 与 relay-pulse/web fork 一致 |
| Rust package name | `sakrylle-studio` | `20` §4.7 |
| OAuth client_id | `sakrylle-studio`（统一自 148 seed `sakrylle-desktop`；仅分支 B 独立 OIDC 时需注册，首发分支 A 复用 CLI 凭据不用） | `03` §9 / P0-7 |
| CLI 二进制 | `sakrylle` / `skl`（短别名 `skl` 已确认 2026-06-03） | `10-CLI` / `11-CLI` |
| CLI home | `~/.sakrylle-cli`（env `SAKRYLLE_CLI_HOME`） | `05` §4.1 |
| daemon 端口 | 4733（上游 4732） | `05` §6 #11 |
| daemon 名（可选） | `sakrylle-cli-daemon` | `05` §8 |
| 主色 | Monet Purple `#9181bd` | `20` §4.6 |
| 强调色 | 樱花粉 `#ec6a9c` | `20` §4.6 |
| 货币展示 | `￥` 仅展示、不转换 | 主仓 Currency policy |
