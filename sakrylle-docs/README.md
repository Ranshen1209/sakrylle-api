# Sakrylle 生态文档集

> **Canonical source of truth.** 本目录是 Sakrylle API / OIDC / RP 接入 / 产品规划 / 品牌系统的中心文档源。产品仓的 `oidc-docs/` 只保留本仓 local implementation notes，并链接回本目录。
>
> 安全约束：Sakrylle API（`sub.sakrylle.com`）与 Sakrylle Image（`image.sakrylle.com`）为线上生产服务。本文档集不包含直接改生产配置、破坏性迁移或删除用户数据的操作指令；任何生产数据库、nginx、Docker、Redis、TLS 证书变更仍须按 `CLAUDE.md` 的 ops 流程额外审批。

---

## 文档权威性约定

| status | 含义 | 维护位置 |
|---|---|---|
| `canonical` | 当前事实、平台规范或全生态权威说明 | 本目录 |
| `local` | 某产品仓的本地实现说明、状态、排障 | 对应产品仓 `oidc-docs/` 或 `20-products/*` |
| `historical` | 历史规划、旧路径兼容 stub、旧决策背景 | 不作为当前事实来源 |

如果历史 research / plan 与平台现状冲突，以 [`10-platform-identity/current-state.md`](./10-platform-identity/current-state.md) 为准。

---

## 推荐阅读入口

### 平台 / OIDC / RP 接入

| 文件 | 用途 |
|---|---|
| [`10-platform-identity/current-state.md`](./10-platform-identity/current-state.md) | Sakrylle API OAuth/OIDC 当前实现状态 |
| [`10-platform-identity/oidc-architecture.md`](./10-platform-identity/oidc-architecture.md) | OIDC 架构、设计原则、密钥与 claims 设计 |
| [`10-platform-identity/rp-integration-guide.md`](./10-platform-identity/rp-integration-guide.md) | 所有 RP 的统一接入协议与示例 |
| [`10-platform-identity/commercial-boundaries.md`](./10-platform-identity/commercial-boundaries.md) | token claims 与商业能力边界 |
| [`10-platform-identity/configuration-isolation.md`](./10-platform-identity/configuration-isolation.md) | 全生态配置隔离、环境变量、bundle id 标准 |

### 产品实现文档

| 产品 | Research | Plan / Status |
|---|---|---|
| Sakrylle CLI | [`20-products/cli/research.md`](./20-products/cli/research.md) | [`20-products/cli/development-plan.md`](./20-products/cli/development-plan.md) |
| Sakrylle Studio | [`20-products/studio/research.md`](./20-products/studio/research.md) | [`20-products/studio/development-plan.md`](./20-products/studio/development-plan.md) — 2026-06-06 CodexMonitor working tree has code-level branding/isolation + CLI credential reuse route; Sakrylle CLI smoke test and updater signing key remain release blockers |
| Sakrylle Web | [`20-products/web/research.md`](./20-products/web/research.md) | [`20-products/web/development-plan.md`](./20-products/web/development-plan.md) |
| Sakrylle Chat | [`20-products/chat/research.md`](./20-products/chat/research.md) | [`20-products/chat/development-plan.md`](./20-products/chat/development-plan.md) |
| Sakrylle Image | [`20-products/image/research.md`](./20-products/image/research.md) | [`20-products/image/oidc-upgrade-plan.md`](./20-products/image/oidc-upgrade-plan.md) |

### 项目管理与品牌

| 文件 | 用途 |
|---|---|
| [`00-overview/executive-summary.md`](./00-overview/executive-summary.md) | 决策者执行摘要 |
| [`00-overview/repositories-inventory.md`](./00-overview/repositories-inventory.md) | 相关仓库与关键文件清单 |
| [`30-program-management/roadmap.md`](./30-program-management/roadmap.md) | 全生态路线图 |
| [`30-program-management/risk-register.md`](./30-program-management/risk-register.md) | 风险登记册 |
| [`30-program-management/decision-log.md`](./30-program-management/decision-log.md) | 决策记录与实现期核查项 |
| [`30-program-management/implementation-checklist.md`](./30-program-management/implementation-checklist.md) | 实施 checklist |
| [`30-program-management/gateway-capability-public-strategy.md`](./30-program-management/gateway-capability-public-strategy.md) | Gemini native / Antigravity / Videos 网关能力公开策略 |
| [`40-brand-system/design.md`](./40-brand-system/design.md) | Monet Purple / 樱花品牌与设计系统 |

---

## 产品仓文档边界

产品仓 `oidc-docs/` 应只回答三类问题：

1. 本仓如何消费中心 OIDC / API 规范；
2. 本仓当前实现到哪里、有哪些本地配置；
3. 本仓如何测试、排障、回滚。

不要在产品仓复制 OIDC Provider 端点、claims 边界、配置隔离总规范、全生态 roadmap/risk/DESIGN。此类内容统一维护在本目录。

---

## 旧路径兼容

根目录下保留的旧编号文件（如 `02-sakrylle-api-oauth-current-state.md`）已降级为 `historical` redirect stub，仅用于兼容旧链接。请更新新引用到上表中的新路径。
