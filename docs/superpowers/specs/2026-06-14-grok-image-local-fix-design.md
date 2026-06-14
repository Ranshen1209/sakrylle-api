# grok-imagine-image-lite 图片端点 400 修复 — 设计

- **日期**: 2026-06-14
- **分支**: `fix/grok-image-local-400`(worktree,基线 `theme/monet-purple`)
- **范围**: 仅本地 sub2api。本轮只修图片 400,不含非流式容错(根在上游 Grok2API,另路并行处理)。

## 背景与问题

channel 14 "Grok Reverse" / group 22(`platform=openai`)服务的图片模型 `grok-imagine-image-lite`,
经 `/v1/images/generations` 调用时返回:

```
400 {"error":"images endpoint requires an image model, got \"grok-imagine-image-lite\""}
```

该模型在上游 Grok2API 是合法图片模型(`registry.py:43`,`Capability.IMAGE`,走 `images.py:_generate_lite`);
400 是 **sub2api 本地**抛出的,与上游无关。我们已为它建好定价行
(`channel_model_pricing` row 326:channel=14、`billing_mode=image`、`per_request_price=0.02`、`platform=openai`),
但请求在本地模型校验阶段就被拦下,根本到不了路由/转发/计费。

## 根因

`backend/internal/service/openai_images.go:457-459` 的 `isOpenAIImageGenerationModel` **硬编码只认 `gpt-image-` 前缀**:

```go
func isOpenAIImageGenerationModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "gpt-image-")
}
```

`validateOpenAIImagesModel`(`:461-470`)据此对任何非 `gpt-image-` 前缀的模型返回上述 400。该校验在解析(`:218`)
和转发(`:572`/`:576`)多处被调用,但都走同一个函数。

经端到端排查,**这是唯一的硬 gate**;放行之后整条链路都是模型无关的:
- 路由只看 `group.platform=openai`(`routes/gateway.go:136-148`)— channel 14 满足。
- 账号选择(`openai_account_scheduler.go:SelectAccountWithSchedulerForImages`)、上游转发
  (`forwardOpenAIImagesAPIKey`,只改写 `model` 字段、其余字段透传)、计费
  (`calculateOpenAIImageCost`,按 `channel_model_pricing` 的 `billing_mode=image`,不看模型名前缀)均不依赖模型前缀。
- channel 14 为 apikey、非 async,不会误入异步图片桥(`IsAsyncImage()` 分支)。

## 设计

### 改动点(唯一代码改动)

`backend/internal/service/openai_images.go` 的 `isOpenAIImageGenerationModel`:把单一前缀改为前缀集合,
新增 `grok-imagine-image`:

```go
func isOpenAIImageGenerationModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	for _, p := range []string{"gpt-image-", "grok-imagine-image"} {
		if strings.HasPrefix(m, p) {
			return true
		}
	}
	return false
}
```

- `grok-imagine-image` 覆盖 `grok-imagine-image-lite` / `-image` / `-pro` / `-edit`,**天然排除 `grok-imagine-video`**(视频不走图片端点)。
- `validateOpenAIImagesModel` 逻辑不动——空模型名 / 非图片模型仍报原错误,只是图片判定放宽。

### 数据流(不变)

放行后请求走现有模型无关链路:路由 → 账号选择 → 上游转发(改写 `model` 字段、其余透传)→ 计费(row 326)。无新增分支。

### 错误处理(不变 + 兜底)

- `channel 14 restrict_models=true` 是第二道闸:前缀放行了但没配定价行的模型仍会 503,**不会误开**。
- 空模型名仍由 `applyOpenAIImagesDefaults` 默认为 `gpt-image-2`,不受影响。

### 测试(TDD,先写后改)

`backend/internal/service/openai_images_test.go` 针对 `validateOpenAIImagesModel` / `isOpenAIImageGenerationModel`:

| 用例 | 期望 | 类型 |
|---|---|---|
| `grok-imagine-image-lite` | 通过校验 | 新增(核心) |
| `grok-imagine-image` / `-pro` / `-edit` | 通过校验 | 新增 |
| `grok-imagine-video` | 被拒(前缀不误纳视频) | 新增(边界) |
| `gpt-image-2` 等 | 仍通过 | 回归 |
| `gpt-5.4`(现有 `:334` 断言) | 仍被拒 | 回归 |
| 空模型名 | 仍返回 `"requires an image model"` | 回归 |

## 实现期需验证的风险项(非设计决策)

1. **capability 分类**:`classifyOpenAIImagesCapability`(`:484-494`)对非 `gpt-image-` 前缀返回 `Native`,
   影响账号选择优先级。需确认 channel 14 的 apikey 账号能被 `account.SupportsOpenAIImageCapability` 选中
   (openai + apikey,理论满足;选不中会回退 basic capability)。
2. **端到端**:核心改动确定能过校验;但"放行后整条链路真能出图 + 正确计费"需在东京用有效 key 实打
   `/v1/images/generations` 验证(上游返回格式、`ImageCount` 解析、row 326 计费触发)。这是本设计无法靠单测覆盖的部分。

## 非目标(out of scope)

- 非流式超时 / 502 容错(根在上游 Grok2API,另路并行)。
- 数据驱动的图片模型识别(`billing_mode=image` 自动判定)— 留作未来真有多家逆向图片渠道时再重构。
- 让 `grok-imagine-image-lite` 走 `/v1/chat/completions` 出图(上游支持,但当前客户端走 images 端点,YAGNI)。

## 部署

合并回 `theme/monet-purple` → GHA → `ghcr.io/ranshen1209/sakrylle-api:purple` → 东京拉取重启。
DB 侧无改动(row 326 已就位);本修复纯代码,无需额外 DB 操作。
