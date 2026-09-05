package service

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// PricingSource 定价来源标识
const (
	PricingSourceGroup    = "group"
	PricingSourceChannel  = "channel"
	PricingSourceLiteLLM  = "litellm"
	PricingSourceFallback = "fallback"
)

// ResolvedPricing 统一定价解析结果
type ResolvedPricing struct {
	// Mode 计费模式
	Mode BillingMode

	// Token 模式：基础定价（来自 LiteLLM 或 fallback）
	BasePricing *ModelPricing

	// Token 模式：区间定价列表（如有，覆盖 BasePricing 中的对应字段）
	Intervals []PricingInterval

	// 按次/图片模式：分层定价
	RequestTiers []PricingInterval

	// 按次/图片模式：默认价格（未命中层级时使用）
	DefaultPerRequestPrice float64

	// 来源标识
	Source string // "channel", "litellm", "fallback"

	// 是否支持缓存细分
	SupportsCacheBreakdown bool

	// 渠道峰谷定价命中信息；nil 表示使用静态价格。
	TimeResolution *PricingTimeResolution

	// 渠道定价原始配置（用于区间模式下获取 ImageOutputPrice）
	channelPricing *ChannelModelPricing

	longContextPricingEnabled bool
}

// ModelPricingResolver 统一模型定价解析器。
// 解析链：Group → Channel → LiteLLM → Fallback。
type ModelPricingResolver struct {
	channelService *ChannelService
	billingService *BillingService
}

// NewModelPricingResolver 创建定价解析器实例
func NewModelPricingResolver(channelService *ChannelService, billingService *BillingService) *ModelPricingResolver {
	return &ModelPricingResolver{
		channelService: channelService,
		billingService: billingService,
	}
}

// PricingInput 定价解析输入
type PricingInput struct {
	Model     string
	GroupID   *int64 // nil 表示不检查渠道
	Group     *Group
	PricingAt time.Time // 零值表示以解析时刻计价
}

var deepSeekOfficialTimePricingEffectiveFrom = time.Date(2026, 8, 16, 16, 0, 0, 0, time.UTC) // 2026-08-17 00:00 Asia/Shanghai

// Resolve 解析模型定价。
// 1. 获取基础定价（LiteLLM → Fallback）
// 2. 如果指定了 GroupID，查找渠道定价并覆盖
func (r *ModelPricingResolver) Resolve(ctx context.Context, input PricingInput) *ResolvedPricing {
	longContextPricingEnabled := input.Group == nil || input.Group.LongContextPricingEnabled
	if groupPricing := matchGroupModelPricing(input.Group, input.Model); groupPricing != nil {
		// Group token cards only override the first-tier / flat rates.
		// Long-context ladders come from official presets, gated by the checkbox.
		if groupPricing.BillingMode == "" || groupPricing.BillingMode == BillingModeToken {
			stripped := groupPricing.Clone()
			stripped.Intervals = nil
			groupPricing = &stripped
		}
		resolved := r.resolveConfiguredPricing(groupPricing, input.Model, PricingSourceGroup)
		resolved.longContextPricingEnabled = longContextPricingEnabled
		return resolved
	}
	pricingAt := resolvePricingAt(ctx, input.PricingAt)

	var chPricing *ChannelModelPricing
	var timeResolution *PricingTimeResolution
	var applyDeepSeekOfficialMultiplier bool
	if input.GroupID != nil && r.channelService != nil {
		chPricing = r.lookupChannelPricingNormalized(ctx, *input.GroupID, input.Model)
		if chPricing != nil {
			chPricing, timeResolution, applyDeepSeekOfficialMultiplier = resolveChannelPricingAt(chPricing, input.Model, pricingAt)

			mode := chPricing.BillingMode
			if mode == "" {
				mode = BillingModeToken
			}
			if mode == BillingModePerRequest || mode == BillingModeImage || mode == BillingModeVideo {
				resolved := &ResolvedPricing{
					Mode:           mode,
					Source:         PricingSourceChannel,
					TimeResolution: timeResolution,
					channelPricing: chPricing,
				}
				resolved.longContextPricingEnabled = longContextPricingEnabled
				r.applyRequestTierOverrides(chPricing, resolved)
				return resolved
			}
		}
	}

	// 1. 获取基础定价
	basePricing, source := r.resolveBasePricing(input.Model)

	resolved := &ResolvedPricing{
		Mode:                   BillingModeToken,
		BasePricing:            basePricing,
		Source:                 source,
		SupportsCacheBreakdown: basePricing != nil && basePricing.SupportsCacheBreakdown,
	}
	resolved.longContextPricingEnabled = longContextPricingEnabled

	// 2. 如果有 GroupID，尝试渠道覆盖
	if chPricing != nil {
		resolved.Source = PricingSourceChannel
		resolved.TimeResolution = timeResolution
		resolved.channelPricing = chPricing
		r.applyTokenOverrides(chPricing, resolved)
		if applyDeepSeekOfficialMultiplier && timeResolution != nil {
			applyResolvedTokenPricingMultiplier(resolved, timeResolution.Multiplier)
		} else if timeResolution != nil {
			applyInheritedTokenPricingMultiplier(resolved, chPricing, timeResolution.Multiplier)
		}
	} else if input.GroupID != nil && r.channelService != nil {
		r.applyChannelOverrides(ctx, *input.GroupID, input.Model, pricingAt, resolved)
	}
	if resolved.Source != PricingSourceChannel {
		applyDeepSeekOfficialFallbackTimePricing(input.Model, pricingAt, resolved)
	}

	return resolved
}

func resolvePricingAt(ctx context.Context, explicit time.Time) time.Time {
	if !explicit.IsZero() {
		return explicit
	}
	if frozen := pricingAtFromContext(ctx); !frozen.IsZero() {
		return frozen
	}
	return time.Now()
}

// resolveChannelPricingAt preserves configured time versions. When no explicit
// time schedule exists, current DeepSeek V4 token cards use the official
// Beijing peak/off-peak schedule without requiring a database migration.
func resolveChannelPricingAt(pricing *ChannelModelPricing, model string, at time.Time) (*ChannelModelPricing, *PricingTimeResolution, bool) {
	if pricing == nil {
		return nil, nil, false
	}
	if len(pricing.TimeVersions) > 0 || (pricing.TimePricing != nil && len(pricing.TimePricing.Periods) > 0) ||
		(pricing.BillingMode != "" && pricing.BillingMode != BillingModeToken) || deepSeekOfficialPricingKey(model) == "" {
		resolved, resolution := pricing.ResolveAt(at)
		return &resolved, resolution, false
	}

	resolved := pricing.Clone()
	resolution := resolveDeepSeekOfficialTimePricing(at)
	return &resolved, resolution, resolution != nil
}

// ResolveChannelPricingForDisplay applies the same explicit-or-official
// schedule used by request billing and retains the schedule on the returned
// card. User-facing DTOs use this instead of reimplementing a clock decision
// in the frontend.
func ResolveChannelPricingForDisplay(pricing *ChannelModelPricing, model string, at time.Time) (*ChannelModelPricing, *PricingTimeResolution) {
	if pricing == nil {
		return nil, nil
	}
	display := pricing.Clone()
	if len(display.TimeVersions) == 0 &&
		(display.TimePricing == nil || len(display.TimePricing.Periods) == 0) &&
		(display.BillingMode == "" || display.BillingMode == BillingModeToken) &&
		deepSeekOfficialPricingKey(model) != "" {
		display.TimeVersions = []PricingTimeVersion{deepSeekOfficialTimePricingVersion()}
	}
	resolved, resolution := display.ResolveAt(at)
	return &resolved, resolution
}

func deepSeekOfficialTimePricingVersion() PricingTimeVersion {
	return PricingTimeVersion{
		EffectiveFrom:     deepSeekOfficialTimePricingEffectiveFrom,
		Timezone:          "Asia/Shanghai",
		DefaultMultiplier: 0.5,
		Windows: []PricingTimeWindow{
			{Label: "peak", Weekdays: 31, StartMinute: 9 * 60, EndMinute: 12 * 60, Multiplier: 1},
			{Label: "peak", Weekdays: 31, StartMinute: 14 * 60, EndMinute: 18 * 60, Multiplier: 1},
		},
	}
}

func resolveDeepSeekOfficialTimePricing(at time.Time) *PricingTimeResolution {
	pricing := ChannelModelPricing{TimeVersions: []PricingTimeVersion{deepSeekOfficialTimePricingVersion()}}
	_, resolution := pricing.ResolveAt(at)
	return resolution
}

func applyDeepSeekOfficialFallbackTimePricing(model string, at time.Time, resolved *ResolvedPricing) {
	if resolved == nil || resolved.BasePricing == nil || deepSeekOfficialPricingKey(model) == "" {
		return
	}
	resolution := resolveDeepSeekOfficialTimePricing(at)
	if resolution == nil {
		return
	}
	resolved.TimeResolution = resolution
	applyResolvedTokenPricingMultiplier(resolved, resolution.Multiplier)
}

func applyResolvedTokenPricingMultiplier(resolved *ResolvedPricing, multiplier float64) {
	if resolved == nil || multiplier == 1 {
		return
	}
	if resolved.BasePricing != nil {
		pricing := *resolved.BasePricing
		pricing.InputPricePerToken *= multiplier
		pricing.InputPricePerTokenPriority *= multiplier
		pricing.ImageInputPricePerToken *= multiplier
		pricing.OutputPricePerToken *= multiplier
		pricing.OutputPricePerTokenPriority *= multiplier
		pricing.CacheCreationPricePerToken *= multiplier
		pricing.CacheCreationPricePerTokenPriority *= multiplier
		pricing.CacheReadPricePerToken *= multiplier
		pricing.CacheReadPricePerTokenPriority *= multiplier
		pricing.CacheCreation5mPrice *= multiplier
		pricing.CacheCreation1hPrice *= multiplier
		pricing.ImageOutputPricePerToken *= multiplier
		resolved.BasePricing = &pricing
	}
	applyResolvedIntervalPricingMultiplier(resolved, multiplier)
	if resolved.channelPricing != nil {
		pricing := resolved.channelPricing.Clone()
		applyPricingMultiplier(&pricing, multiplier)
		pricing.Intervals = append([]PricingInterval(nil), resolved.Intervals...)
		resolved.channelPricing = &pricing
	}
}

// applyInheritedTokenPricingMultiplier completes an explicit TimeVersion after
// the resolved channel card has been merged over the base model card. ResolveAt
// already multiplied every non-nil channel/version field, so only buckets that
// still come from the base card are multiplied here. Interval prices are not
// handled by ChannelModelPricing.ResolveAt and therefore always need scaling.
func applyInheritedTokenPricingMultiplier(resolved *ResolvedPricing, channelPricing *ChannelModelPricing, multiplier float64) {
	if resolved == nil || channelPricing == nil || multiplier == 1 {
		return
	}
	if resolved.BasePricing != nil {
		pricing := *resolved.BasePricing
		if channelPricing.InputPrice == nil {
			pricing.InputPricePerToken *= multiplier
			pricing.InputPricePerTokenPriority *= multiplier
		}
		if channelPricing.OutputPrice == nil {
			pricing.OutputPricePerToken *= multiplier
			pricing.OutputPricePerTokenPriority *= multiplier
		}
		if channelPricing.CacheWritePrice == nil {
			pricing.CacheCreationPricePerToken *= multiplier
			pricing.CacheCreationPricePerTokenPriority *= multiplier
			pricing.CacheCreation5mPrice *= multiplier
			pricing.CacheCreation1hPrice *= multiplier
		}
		if channelPricing.CacheReadPrice == nil {
			pricing.CacheReadPricePerToken *= multiplier
			pricing.CacheReadPricePerTokenPriority *= multiplier
		}
		resolved.BasePricing = &pricing
	}
	applyResolvedIntervalPricingMultiplier(resolved, multiplier)
}

func applyResolvedIntervalPricingMultiplier(resolved *ResolvedPricing, multiplier float64) {
	if resolved == nil || len(resolved.Intervals) == 0 || multiplier == 1 {
		return
	}
	intervals := make([]PricingInterval, len(resolved.Intervals))
	copy(intervals, resolved.Intervals)
	for i := range intervals {
		intervals[i].InputPrice = multipliedPrice(intervals[i].InputPrice, multiplier)
		intervals[i].OutputPrice = multipliedPrice(intervals[i].OutputPrice, multiplier)
		intervals[i].CacheWritePrice = multipliedPrice(intervals[i].CacheWritePrice, multiplier)
		intervals[i].CacheReadPrice = multipliedPrice(intervals[i].CacheReadPrice, multiplier)
	}
	resolved.Intervals = intervals
	if resolved.channelPricing != nil {
		pricing := resolved.channelPricing.Clone()
		pricing.Intervals = append([]PricingInterval(nil), resolved.Intervals...)
		resolved.channelPricing = &pricing
	}
}

func (r *ModelPricingResolver) resolveConfiguredPricing(config *ChannelModelPricing, model, source string) *ResolvedPricing {
	mode := config.BillingMode
	if mode == "" {
		mode = BillingModeToken
	}
	resolved := &ResolvedPricing{Mode: mode, Source: source, channelPricing: config}
	if mode == BillingModePerRequest || mode == BillingModeImage || mode == BillingModeVideo {
		r.applyRequestTierOverrides(config, resolved)
		return resolved
	}
	resolved.BasePricing, _ = r.resolveBasePricing(model)
	resolved.SupportsCacheBreakdown = resolved.BasePricing != nil && resolved.BasePricing.SupportsCacheBreakdown
	r.applyTokenOverrides(config, resolved)
	return resolved
}

func matchGroupModelPricing(group *Group, model string) *ChannelModelPricing {
	if group == nil {
		return nil
	}
	model = normalizeChannelPricingModelName(model)
	var wildcard *ChannelModelPricing
	for i := range group.ModelPricing {
		entry := &group.ModelPricing[i]
		for _, pattern := range entry.Models {
			normalized := normalizeChannelPricingModelName(pattern)
			if normalized == model {
				cp := entry.Clone()
				return &cp
			}
			if strings.HasSuffix(normalized, "*") && strings.HasPrefix(model, strings.TrimSuffix(normalized, "*")) && wildcard == nil {
				cp := entry.Clone()
				wildcard = &cp
			}
		}
	}
	return wildcard
}

// resolveBasePricing 从 LiteLLM 或 Fallback 获取基础定价
func (r *ModelPricingResolver) resolveBasePricing(model string) (*ModelPricing, string) {
	pricing, err := r.billingService.GetModelPricing(model)
	if err != nil {
		slog.Debug("failed to get model pricing from LiteLLM, using fallback",
			"model", model, "error", err)
		return nil, PricingSourceFallback
	}
	return pricing, PricingSourceLiteLLM
}

// lookupChannelPricingNormalized 查找渠道定价：先用字面模型名做精确/通配匹配，
// 未命中时用与官方兜底价一致的归一化模型名再查一次。
//
// 官方兜底价对 OpenAI/Codex 族会把 gpt-5.6-luna-high 这类变体名归一化到基名
// （billing_service.go 的 normalizeKnownOpenAICodexModel 分支），而渠道定价此前
// 只认字面名。两者不对称导致：管理员只配基名、请求模型带 effort 后缀时，渠道定价
// 未命中而官方兜底命中，计费候选循环首个成功即返回，渠道定价永远轮不到（issue #5256）。
//
// 字面名优先，保证管理员对具体变体的显式配价不被基名覆盖；非 OpenAI 模型
// normalizeKnownOpenAICodexModel 返回空串，此处天然 no-op。
func (r *ModelPricingResolver) lookupChannelPricingNormalized(ctx context.Context, groupID int64, model string) *ChannelModelPricing {
	if r.channelService == nil {
		return nil
	}
	if pricing := r.channelService.GetChannelModelPricing(ctx, groupID, model); pricing != nil {
		return pricing
	}
	normalized := normalizeKnownOpenAICodexModel(model)
	if normalized == "" || strings.EqualFold(normalized, strings.TrimSpace(model)) {
		return nil
	}
	return r.channelService.GetChannelModelPricing(ctx, groupID, normalized)
}

// applyChannelOverrides 应用渠道定价覆盖
func (r *ModelPricingResolver) applyChannelOverrides(ctx context.Context, groupID int64, model string, pricingAt time.Time, resolved *ResolvedPricing) {
	chPricing := r.lookupChannelPricingNormalized(ctx, groupID, model)
	if chPricing == nil {
		return
	}
	var resolution *PricingTimeResolution
	var applyDeepSeekMultiplier bool
	chPricing, resolution, applyDeepSeekMultiplier = resolveChannelPricingAt(chPricing, model, pricingAt)

	resolved.Source = PricingSourceChannel
	resolved.TimeResolution = resolution
	resolved.channelPricing = chPricing
	resolved.Mode = chPricing.BillingMode
	if resolved.Mode == "" {
		resolved.Mode = BillingModeToken
	}

	switch resolved.Mode {
	case BillingModeToken:
		r.applyTokenOverrides(chPricing, resolved)
		if applyDeepSeekMultiplier && resolution != nil {
			applyResolvedTokenPricingMultiplier(resolved, resolution.Multiplier)
		} else if resolution != nil {
			applyInheritedTokenPricingMultiplier(resolved, chPricing, resolution.Multiplier)
		}
	case BillingModePerRequest, BillingModeImage, BillingModeVideo:
		r.applyRequestTierOverrides(chPricing, resolved)
	}
}

// applyTokenOverrides 应用 token 模式的渠道覆盖
func (r *ModelPricingResolver) applyTokenOverrides(chPricing *ChannelModelPricing, resolved *ResolvedPricing) {
	if resolved.BasePricing == nil {
		resolved.BasePricing = &ModelPricing{}
	} else {
		// 防止修改 fallbackPrices 中的共享指针
		cloned := *resolved.BasePricing
		resolved.BasePricing = &cloned
	}

	applyChannelTokenPriceOverrides(resolved.BasePricing, chPricing)
	if chPricing.CacheWrite1hPrice != nil {
		resolved.SupportsCacheBreakdown = true
		resolved.BasePricing.SupportsCacheBreakdown = true
	}
	for i := range chPricing.Intervals {
		if chPricing.Intervals[i].CacheWrite1hPrice != nil {
			resolved.SupportsCacheBreakdown = true
			resolved.BasePricing.SupportsCacheBreakdown = true
			break
		}
	}
	resolved.BasePricing.FastMultiplier = chPricing.FastMultiplier
	resolved.BasePricing.FlexMultiplier = chPricing.FlexMultiplier
	if chPricing.MaxReasoningEffortMultiplier != nil {
		resolved.BasePricing.MaxReasoningEffortMultiplier = chPricing.MaxReasoningEffortMultiplier
	}
	// 渠道定价覆盖一切：显式配置则用配置值，未配置则归零（不回退到 LiteLLM）
	if chPricing.ImageOutputPrice != nil {
		resolved.BasePricing.ImageOutputPricePerToken = *chPricing.ImageOutputPrice
	} else {
		resolved.BasePricing.ImageOutputPricePerToken = 0
	}
	resolved.BasePricing.ImageOutputPriceExplicit = true
	applyChannelImageInputPrice(chPricing, resolved.BasePricing)

	// 区间未命中时回退到上面已经应用渠道覆盖的基础价。
	resolved.Intervals = filterValidIntervals(chPricing.Intervals)
}

// applyChannelImageInputPrice 应用渠道图片输入价：显式配置则用配置值；
// 未配置时归零，使 computeTokenBreakdown 回退到文本输入价（向后兼容，
// 避免 commit 引入的 LiteLLM 图片输入价泄漏进渠道自定义定价）。
// 与 image_output 不同，此处不设 Explicit 标志——图片输入未配置应回退文本价，
// 而非硬置 0。
func applyChannelImageInputPrice(chPricing *ChannelModelPricing, pricing *ModelPricing) {
	if chPricing != nil && chPricing.ImageInputPrice != nil {
		pricing.ImageInputPricePerToken = *chPricing.ImageInputPrice
	} else {
		pricing.ImageInputPricePerToken = 0
	}
}

// applyRequestTierOverrides 应用按次/图片模式的渠道覆盖
func (r *ModelPricingResolver) applyRequestTierOverrides(chPricing *ChannelModelPricing, resolved *ResolvedPricing) {
	resolved.RequestTiers = filterValidIntervals(chPricing.Intervals)
	if chPricing.PerRequestPrice != nil {
		resolved.DefaultPerRequestPrice = *chPricing.PerRequestPrice
	}
}

// filterValidIntervals 过滤掉所有价格字段都为空的无效 interval。
// 前端可能创建了只有 min/max 但无价格的空 interval。
func filterValidIntervals(intervals []PricingInterval) []PricingInterval {
	var valid []PricingInterval
	for _, iv := range intervals {
		if iv.InputPrice != nil || iv.OutputPrice != nil ||
			iv.CacheWritePrice != nil || iv.CacheWrite1hPrice != nil || iv.CacheReadPrice != nil ||
			iv.PerRequestPrice != nil || iv.InputMultiplier != nil ||
			iv.OutputMultiplier != nil || iv.CacheWriteMultiplier != nil ||
			iv.CacheReadMultiplier != nil {
			valid = append(valid, iv)
		}
	}
	return valid
}

// GetIntervalPricing 根据 context token 数获取区间定价。
// 如果有区间列表，找到匹配区间并构造 ModelPricing；否则直接返回 BasePricing。
func (r *ModelPricingResolver) GetIntervalPricing(resolved *ResolvedPricing, totalContextTokens int) *ModelPricing {
	if len(resolved.Intervals) == 0 {
		return resolved.BasePricing
	}

	iv := FindMatchingInterval(resolved.Intervals, totalContextTokens)
	if iv == nil {
		return resolved.BasePricing
	}

	pricing := intervalToModelPricing(iv, resolved.BasePricing, resolved.channelPricing)
	// BasePricing 为 nil（仅配置区间）时拷贝不到该标志，从 resolved 回填，
	// 保证 computeCacheCreationCost 的 5m/1h 分档判断不被区间路径吞掉。
	pricing.SupportsCacheBreakdown = resolved.SupportsCacheBreakdown
	return pricing
}

// intervalToModelPricing 将区间定价转换为 ModelPricing
func intervalToModelPricing(iv *PricingInterval, base *ModelPricing, chPricing *ChannelModelPricing) *ModelPricing {
	pricing := &ModelPricing{}
	if base != nil {
		*pricing = *base
	}
	applyMultiplier := func(value float64, multiplier *float64) float64 {
		if multiplier == nil {
			return value
		}
		return value * *multiplier
	}
	if iv.InputPrice != nil {
		pricing.InputPricePerTokenPriority = channelTierOverridePrice(pricing.InputPricePerToken, pricing.InputPricePerTokenPriority, *iv.InputPrice)
		pricing.InputPricePerToken = *iv.InputPrice
	} else if iv.InputMultiplier != nil {
		pricing.InputPricePerToken = applyMultiplier(pricing.InputPricePerToken, iv.InputMultiplier)
		pricing.InputPricePerTokenPriority = applyMultiplier(pricing.InputPricePerTokenPriority, iv.InputMultiplier)
	}
	if iv.OutputPrice != nil {
		pricing.OutputPricePerTokenPriority = channelTierOverridePrice(pricing.OutputPricePerToken, pricing.OutputPricePerTokenPriority, *iv.OutputPrice)
		pricing.OutputPricePerToken = *iv.OutputPrice
	} else if iv.OutputMultiplier != nil {
		pricing.OutputPricePerToken = applyMultiplier(pricing.OutputPricePerToken, iv.OutputMultiplier)
		pricing.OutputPricePerTokenPriority = applyMultiplier(pricing.OutputPricePerTokenPriority, iv.OutputMultiplier)
	}
	if iv.CacheWritePrice != nil {
		pricing.CacheCreationPricePerTokenPriority = channelTierOverridePrice(pricing.CacheCreationPricePerToken, pricing.CacheCreationPricePerTokenPriority, *iv.CacheWritePrice)
		pricing.CacheCreationPricePerToken = *iv.CacheWritePrice
		pricing.CacheCreationPriceExplicit = true
		pricing.CacheCreation5mPrice = *iv.CacheWritePrice
		if iv.CacheWrite1hPrice == nil {
			pricing.CacheCreation1hPrice = *iv.CacheWritePrice
		}
	} else if iv.CacheWriteMultiplier != nil {
		pricing.CacheCreationPricePerToken = applyMultiplier(pricing.CacheCreationPricePerToken, iv.CacheWriteMultiplier)
		pricing.CacheCreationPricePerTokenPriority = applyMultiplier(pricing.CacheCreationPricePerTokenPriority, iv.CacheWriteMultiplier)
		pricing.CacheCreation5mPrice = applyMultiplier(pricing.CacheCreation5mPrice, iv.CacheWriteMultiplier)
		pricing.CacheCreation1hPrice = applyMultiplier(pricing.CacheCreation1hPrice, iv.CacheWriteMultiplier)
	}
	if iv.CacheWrite1hPrice != nil {
		pricing.CacheCreation1hPrice = *iv.CacheWrite1hPrice
		pricing.SupportsCacheBreakdown = true
	}
	if iv.CacheReadPrice != nil {
		pricing.CacheReadPricePerTokenPriority = channelTierOverridePrice(pricing.CacheReadPricePerToken, pricing.CacheReadPricePerTokenPriority, *iv.CacheReadPrice)
		pricing.CacheReadPricePerToken = *iv.CacheReadPrice
	} else if iv.CacheReadMultiplier != nil {
		pricing.CacheReadPricePerToken = applyMultiplier(pricing.CacheReadPricePerToken, iv.CacheReadMultiplier)
		pricing.CacheReadPricePerTokenPriority = applyMultiplier(pricing.CacheReadPricePerTokenPriority, iv.CacheReadMultiplier)
	}
	// 渠道定价存在时，ImageOutputPrice 显式覆盖；图片输入价用渠道级配置
	// （区间不携带图片输入价，与 image_output 一致）。
	if chPricing != nil {
		pricing.ImageOutputPriceExplicit = true
		if chPricing.ImageOutputPrice != nil {
			pricing.ImageOutputPricePerToken = *chPricing.ImageOutputPrice
		}
		applyChannelImageInputPrice(chPricing, pricing)
	}
	return pricing
}

// GetRequestTierPrice 根据层级标签获取按次价格
func (r *ModelPricingResolver) GetRequestTierPrice(resolved *ResolvedPricing, tierLabel string) float64 {
	for _, tier := range resolved.RequestTiers {
		if tier.TierLabel == tierLabel && tier.PerRequestPrice != nil {
			return *tier.PerRequestPrice
		}
	}
	return 0
}

// GetRequestTierPriceByContext 根据 context token 数获取按次价格
func (r *ModelPricingResolver) GetRequestTierPriceByContext(resolved *ResolvedPricing, totalContextTokens int) float64 {
	iv := FindMatchingInterval(resolved.RequestTiers, totalContextTokens)
	if iv != nil && iv.PerRequestPrice != nil {
		return *iv.PerRequestPrice
	}
	return 0
}
