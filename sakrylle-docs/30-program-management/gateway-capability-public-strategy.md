---
title: 94 · 网关能力公开策略
status: canonical
scope: program
last_verified: 2026-06-20
---

# 94 · 网关能力公开策略

> 策略文档（policy only）。本文只定义 Gemini native、Antigravity、Videos 三条网关能力的公开口径、上线门槛和默认可见性，不包含任何生产配置、数据库写入或代码改动指令。
> 兄弟文档：[`roadmap.md`](./roadmap.md)、[`risk-register.md`](./risk-register.md)、[`../10-platform-identity/rp-integration-guide.md`](../10-platform-identity/rp-integration-guide.md)。

---

## 1. 结论

三条能力都不按“已有模型直接全量公开”处理。默认策略如下：

| 能力 | 当前公开状态 | 默认用户面 | 公开原则 |
|---|---|---|---|
| Gemini native | 邀请制 beta | 手动 API key + Gemini SDK/CLI 直连 | 作为高级协议入口公开，不进入普通 OpenAI-compatible 主路径 |
| Antigravity | 封闭 beta | 指定分组 + 专用 `/antigravity/*` 路由 | 作为 coding/agent 实验能力逐客户开通，不做首页级营销 |
| Videos | 内测 / 邀请制 beta | 手动 API key + OpenAI-ish `/v1/videos` | 先验证计费、失败退款和任务查询，再进公开文档 |

“公开”分四层，必须逐层放开：

1. **路由可用**：代码中存在 endpoint，不代表可对外宣传。
2. **分组可用**：指定 group/channel/account 可以调用，不代表全用户可见。
3. **模型广场可见**：用户能在 UI 看到入口，必须已完成定价、限制与文案。
4. **官网/公开文档可见**：面向非定向用户承诺可用，必须有 SLA 口径、退款口径和排障文档。

---

## 2. Gemini Native

### 2.1 当前事实

- 路由：`/v1beta/*`，兼容 Gemini native REST 形态。
- 鉴权：使用 Google-style API key 中间件；`sk_oauth_` token 对 `/v1beta/*` 默认拒绝，手动 API key 可用。
- 分组：要求 `group.platform = 'gemini'`，或在 `/antigravity/v1beta/*` 下由 Antigravity 强制平台接管。
- 模型列表：代码有 fallback model list，但公开模型必须以 channel/group 的 pricing + restrict 配置为准，不允许把 fallback 当成公开售卖清单。

### 2.2 公开口径

Gemini native 是“协议兼容入口”，不是普通聊天模型的新包装。公开时统一写成：

> Gemini native beta：面向需要 Gemini SDK、Gemini CLI 或 Google `v1beta` REST 协议的高级用户。该入口使用 Gemini 原生请求/响应格式，不保证与 OpenAI-compatible API 互换。

不使用以下口径：

- “所有 Gemini 模型均可用”
- “可替代 OpenAI/Claude 主接口”
- “OAuth 登录后的 RP 默认可调用”

### 2.3 公开门槛

| 门槛 | 要求 |
|---|---|
| 模型控制 | 公开 group 必须 `restrict_models=true`，并且每个可见模型都有明确 pricing row |
| 计费验证 | 每个公开模型至少跑通 `generateContent` + `streamGenerateContent`，并确认 `usage_logs.total_cost > 0` |
| OAuth 边界 | 保持 `/v1beta/*` 不进 OAuth scope matrix；如未来要给 RP 用，先新增 scope 与 consent 文案 |
| 文档边界 | docs 必须强调 Gemini native 是原生协议入口，不能复用 OpenAI-compatible 示例 |
| 入口控制 | 不上首页，不在普通“新手接入”路径推荐；只在 API reference / 高级接入页出现 |

### 2.4 放开顺序

1. 管理员手动创建/验证 Gemini group。
2. 指定测试用户发放 API key。
3. 补充隐藏文档页或客户私发说明。
4. 稳定后再允许模型广场显示 “Gemini native beta”。

---

## 3. Antigravity

### 3.1 当前事实

- 路由：`/antigravity/v1/messages`、`/antigravity/v1/messages/count_tokens`、`/antigravity/v1/models`、`/antigravity/v1/usage`。
- Gemini native 变体：`/antigravity/v1beta/*`，强制 `PlatformAntigravity`，且拒绝 OAuth unlisted resources。
- 模型：Antigravity 默认模型映射包含 Claude + Gemini；未配置账号映射时使用 `DefaultAntigravityModelMapping`。
- 运维：存在 Antigravity OAuth、privacy mode 设置、User-Agent 版本覆盖、模型级限流与智能重试。

### 3.2 公开口径

Antigravity 是 coding/agent 实验能力，不是普通 Claude/Gemini 的稳定别名。公开时统一写成：

> Antigravity beta：面向 agentic coding 场景的实验性通道，使用专用 `/antigravity/*` endpoint。模型、额度与可用性按分组独立控制。

对普通用户页面，优先使用“Agentic Coding Beta”或“Coding Beta”作为产品文案；“Antigravity”保留在 API 文档、模型名、管理员配置和面向高级用户的说明中。

### 3.3 不公开的内容

| 内容 | 原因 |
|---|---|
| 上游账号来源、OAuth client secret、内部 UA 版本 | 凭据与运维细节，不应成为用户承诺 |
| “Claude + Gemini 全部可用” | 实际受 mapping、pricing、账号额度和模型级限流约束 |
| “稳定生产 SLA” | 当前更适合 beta，需明确容量和上游波动风险 |
| `/antigravity/v1beta/*` 的 OAuth RP 能力 | 当前明确不在 OAuth scope matrix 内 |

### 3.4 公开门槛

| 门槛 | 要求 |
|---|---|
| 隐私 | OAuth 账号必须确认 `privacy_mode = privacy_set`，批量导入后也要异步校验完成 |
| 模型映射 | 后端 `DefaultAntigravityModelMapping`、迁移、前端默认映射保持一致；新增公开别名前必须补测试 |
| 定价 | 每个公开模型都必须有 pricing row，且 group multiplier 明确；禁止依赖默认模型泄露 |
| 容量 | 至少一组 Claude 路径和一组 Gemini 路径跑通流式、非流式、工具调用与计费 |
| 限流 | 模型级 429/503 降级、账号切换、single-account 503 canary 均需有监控口径 |
| 文档 | 文档必须标注 beta、专用 endpoint、模型可能按容量动态调整 |

### 3.5 放开顺序

1. 内部 key 验证 `/antigravity/v1/messages` 与 `/antigravity/v1beta/*`。
2. 单客户 / 单分组 beta，人工观察 429、503、账单和模型映射。
3. 模型广场仅对该分组展示，不做全站推荐。
4. 稳定后再把“Agentic Coding Beta”放进公开 API reference。

---

## 4. Videos

### 4.1 当前事实

- 旧 Sora 表和字段已由 migration `090_drop_sora` 移除；不要按 Sora 客户端能力对外宣传。
- 当前 Videos 是 Agnes async video bridge：`POST /v1/videos` 提交任务，`GET /v1/videos/{id}` 查询任务。
- 账号级开关：`credentials.video_enabled = "true"`，模型白名单来自 `credentials.video_models`。
- 计费：提交成功后合成 `OutputTokens = round(seconds)`，走 token billing；GET 查询不计费。
- 安全：完成态 URL 经过 `video_host_suffix` allowlist 校验。
- OAuth：当前 scope matrix 没有 `videos:create`，因此公开 RP/OAuth 使用前必须先补 scope 与 consent 文案。

### 4.2 公开口径

Videos 先按“异步任务 API beta”公开，不按完整视频产品公开。公开时统一写成：

> Videos beta：异步视频生成 API。提交任务返回 task id，客户端轮询查询结果。当前仅面向白名单分组开放，计费、失败处理和结果链接有效期以 beta 文档为准。

不使用以下口径：

- “Sora”
- “视频工作室 / 作品库”
- “生成一定成功”
- “失败自动不收费”（除非后续实现完成态计费或自动退款）

### 4.3 公开门槛

| 门槛 | 要求 |
|---|---|
| 计费口径 | 明确 submit-time billing：提交成功即扣费；若任务后续失败，必须有自动退款或人工调整 SOP |
| 失败处理 | 记录上游失败率、超时率、完成率；beta 文档写清楚失败反馈渠道 |
| OAuth scope | 若允许 RP 调用，新增 `videos:create` scope、scope matrix、consent 文案与测试；否则只允许手动 API key |
| 任务查询 | 公开多账号组前，task id 必须能绑定提交账号；当前单账号组假设不能扩大使用 |
| 定价 | 每个 video model 必须有 token pricing 或明确合成价格，`video_default_seconds` 不可缺失 |
| 链接安全 | `video_host_suffix` 必填；禁止空 allowlist 用于公开账号 |
| 产品边界 | 不承诺图库、存储配额、历史记录；旧 Sora storage 字段已移除 |

### 4.4 放开顺序

1. 内部手动 API key 测试：提交、轮询、失败、URL allowlist、扣费。
2. 白名单分组 beta：只开放一个 video account，避免 task 查询错账号。
3. 增加失败退款 SOP 或实现完成态计费。
4. 补 `videos:create` 后才允许 OIDC/RP 使用。
5. 稳定后再进入公开 API reference；不进入首页或普通模型推荐。

---

## 5. 统一可见性规则

| 层级 | Gemini native | Antigravity | Videos |
|---|---|---|---|
| 首页 / marketing | 不展示 | 不展示 | 不展示 |
| 普通新手文档 | 不默认推荐 | 不默认推荐 | 不默认推荐 |
| API reference | beta 后可展示 | beta 后可展示 | scope/退款补齐后可展示 |
| 模型广场 | 仅分组可见 | 仅分组可见 | beta 期不展示或仅白名单展示 |
| OIDC/RP | 默认不可用 | `/antigravity/v1` 可按 scope；`v1beta` 不可用 | 默认不可用，等 `videos:create` |
| 手动 API key | 邀请制可用 | 邀请制可用 | 邀请制可用 |

所有公开文案必须满足：

- 不承诺未配置模型。
- 不承诺上游无限容量。
- 不把 fallback model list 当成售卖清单。
- 不把 beta 能力纳入 SLA。
- 不在未补 scope 的情况下让 OAuth RP 访问新能力。

---

## 6. 决策记录

| 日期 | 决策 |
|---|---|
| 2026-06-20 | Gemini native、Antigravity、Videos 均不全量公开；统一按 beta/邀请制逐层放开 |
| 2026-06-20 | Gemini native 保持手动 API key 优先，`/v1beta/*` 不进入 OAuth scope matrix |
| 2026-06-20 | Antigravity 使用专用 `/antigravity/*` endpoint，普通用户文案优先叫 “Agentic Coding Beta” |
| 2026-06-20 | Videos 不使用 Sora 口径；补 `videos:create` 与失败退款/调整机制前不进入公开 API reference |
