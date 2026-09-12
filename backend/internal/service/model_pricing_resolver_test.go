//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newTestBillingServiceForResolver() *BillingService {
	bs := &BillingService{
		fallbackPrices: make(map[string]*ModelPricing),
	}
	bs.fallbackPrices["claude-sonnet-4"] = &ModelPricing{
		InputPricePerToken:         3e-6,
		OutputPricePerToken:        15e-6,
		CacheCreationPricePerToken: 3.75e-6,
		CacheReadPricePerToken:     0.3e-6,
		SupportsCacheBreakdown:     false,
	}
	return bs
}

func TestResolve_NoGroupID(t *testing.T) {
	bs := newTestBillingServiceForResolver()
	r := NewModelPricingResolver(&ChannelService{}, bs)

	resolved := r.Resolve(context.Background(), PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: nil,
	})

	require.NotNil(t, resolved)
	require.Equal(t, BillingModeToken, resolved.Mode)
	require.NotNil(t, resolved.BasePricing)
	require.InDelta(t, 3e-6, resolved.BasePricing.InputPricePerToken, 1e-12)
	// BillingService.GetModelPricing uses fallback internally, but resolveBasePricing
	// reports "litellm" when GetModelPricing succeeds (regardless of internal source)
	require.Equal(t, "litellm", resolved.Source)
}

func TestResolve_UnknownModel(t *testing.T) {
	bs := newTestBillingServiceForResolver()
	r := NewModelPricingResolver(&ChannelService{}, bs)

	resolved := r.Resolve(context.Background(), PricingInput{
		Model:   "unknown-model-xyz",
		GroupID: nil,
	})

	require.NotNil(t, resolved)
	require.Nil(t, resolved.BasePricing)
	// Unknown model: GetModelPricing returns error, source is "fallback"
	require.Equal(t, "fallback", resolved.Source)
}

func TestGetIntervalPricing_NoIntervals(t *testing.T) {
	bs := newTestBillingServiceForResolver()
	r := NewModelPricingResolver(&ChannelService{}, bs)

	basePricing := &ModelPricing{InputPricePerToken: 5e-6}
	resolved := &ResolvedPricing{
		Mode:        BillingModeToken,
		BasePricing: basePricing,
		Intervals:   nil,
	}

	result := r.GetIntervalPricing(resolved, 50000)
	require.Equal(t, basePricing, result)
}

func TestGetIntervalPricing_MatchesInterval(t *testing.T) {
	bs := newTestBillingServiceForResolver()
	r := NewModelPricingResolver(&ChannelService{}, bs)

	resolved := &ResolvedPricing{
		Mode:                   BillingModeToken,
		BasePricing:            &ModelPricing{InputPricePerToken: 5e-6},
		SupportsCacheBreakdown: true,
		Intervals: []PricingInterval{
			{MinTokens: 0, MaxTokens: testPtrInt(128000), InputPrice: testPtrFloat64(1e-6), OutputPrice: testPtrFloat64(2e-6)},
			{MinTokens: 128000, MaxTokens: nil, InputPrice: testPtrFloat64(3e-6), OutputPrice: testPtrFloat64(6e-6)},
		},
	}

	result := r.GetIntervalPricing(resolved, 50000)
	require.NotNil(t, result)
	require.InDelta(t, 1e-6, result.InputPricePerToken, 1e-12)
	require.InDelta(t, 2e-6, result.OutputPricePerToken, 1e-12)
	require.True(t, result.SupportsCacheBreakdown)

	result2 := r.GetIntervalPricing(resolved, 200000)
	require.NotNil(t, result2)
	require.InDelta(t, 3e-6, result2.InputPricePerToken, 1e-12)
}

func TestGetIntervalPricing_NoMatch_FallsBackToBase(t *testing.T) {
	bs := newTestBillingServiceForResolver()
	r := NewModelPricingResolver(&ChannelService{}, bs)

	basePricing := &ModelPricing{InputPricePerToken: 99e-6}
	resolved := &ResolvedPricing{
		Mode:        BillingModeToken,
		BasePricing: basePricing,
		Intervals: []PricingInterval{
			{MinTokens: 10000, MaxTokens: testPtrInt(50000), InputPrice: testPtrFloat64(1e-6)},
		},
	}

	result := r.GetIntervalPricing(resolved, 5000)
	require.Equal(t, basePricing, result)
}

func TestGPT56ExplicitZeroCacheWritePriceIsPreserved(t *testing.T) {
	bs := &BillingService{}
	resolver := NewModelPricingResolver(nil, bs)
	zero := 0.0

	t.Run("flat channel price", func(t *testing.T) {
		resolved := &ResolvedPricing{
			Mode: BillingModeToken,
			BasePricing: &ModelPricing{
				InputPricePerToken:  5e-6,
				OutputPricePerToken: 30e-6,
			},
		}
		resolver.applyTokenOverrides(&ChannelModelPricing{CacheWritePrice: &zero}, resolved)

		require.True(t, resolved.BasePricing.CacheCreationPriceExplicit)
		cost, err := bs.CalculateCostUnified(CostInput{
			Model:          "gpt-5.6-sol",
			Tokens:         UsageTokens{CacheCreationTokens: 100},
			RateMultiplier: 1,
			Resolver:       resolver,
			Resolved:       resolved,
		})
		require.NoError(t, err)
		require.Zero(t, cost.CacheCreationCost)
	})

	t.Run("interval price", func(t *testing.T) {
		pricing := intervalToModelPricing(&PricingInterval{CacheWritePrice: &zero}, &ModelPricing{}, nil)
		require.True(t, pricing.CacheCreationPriceExplicit)

		cost, err := bs.CalculateCostUnified(CostInput{
			Model:          "gpt-5.6-sol",
			Tokens:         UsageTokens{CacheCreationTokens: 100},
			RateMultiplier: 1,
			Resolver:       resolver,
			Resolved: &ResolvedPricing{
				Mode:        BillingModeToken,
				BasePricing: pricing,
			},
		})
		require.NoError(t, err)
		require.Zero(t, cost.CacheCreationCost)
	})
}

func TestGetRequestTierPrice(t *testing.T) {
	bs := newTestBillingServiceForResolver()
	r := NewModelPricingResolver(&ChannelService{}, bs)

	resolved := &ResolvedPricing{
		Mode: BillingModePerRequest,
		RequestTiers: []PricingInterval{
			{TierLabel: "1K", PerRequestPrice: testPtrFloat64(0.04)},
			{TierLabel: "2K", PerRequestPrice: testPtrFloat64(0.08)},
		},
	}

	require.InDelta(t, 0.04, r.GetRequestTierPrice(resolved, "1K"), 1e-12)
	require.InDelta(t, 0.08, r.GetRequestTierPrice(resolved, "2K"), 1e-12)
	require.InDelta(t, 0.0, r.GetRequestTierPrice(resolved, "4K"), 1e-12)
}

func TestGetRequestTierPriceByContext(t *testing.T) {
	bs := newTestBillingServiceForResolver()
	r := NewModelPricingResolver(&ChannelService{}, bs)

	resolved := &ResolvedPricing{
		Mode: BillingModePerRequest,
		RequestTiers: []PricingInterval{
			{MinTokens: 0, MaxTokens: testPtrInt(128000), PerRequestPrice: testPtrFloat64(0.05)},
			{MinTokens: 128000, MaxTokens: nil, PerRequestPrice: testPtrFloat64(0.10)},
		},
	}

	require.InDelta(t, 0.05, r.GetRequestTierPriceByContext(resolved, 50000), 1e-12)
	require.InDelta(t, 0.10, r.GetRequestTierPriceByContext(resolved, 200000), 1e-12)
}

func TestGetRequestTierPrice_NilPerRequestPrice(t *testing.T) {
	bs := newTestBillingServiceForResolver()
	r := NewModelPricingResolver(&ChannelService{}, bs)

	resolved := &ResolvedPricing{
		Mode: BillingModePerRequest,
		RequestTiers: []PricingInterval{
			{TierLabel: "1K", PerRequestPrice: nil},
		},
	}

	require.InDelta(t, 0.0, r.GetRequestTierPrice(resolved, "1K"), 1e-12)
}

// ===========================================================================
// Channel override tests — exercises applyChannelOverrides via Resolve
// ===========================================================================

// helper: creates a resolver wired to a ChannelService that returns the given
// channel (active, groupID=100, platform=anthropic) with the specified pricing.
func newResolverWithChannel(t *testing.T, pricing []ChannelModelPricing) *ModelPricingResolver {
	t.Helper()
	const groupID = 100
	repo := &mockChannelRepository{
		listAllFn: func(_ context.Context) ([]Channel, error) {
			return []Channel{{
				ID:           1,
				Name:         "test-channel",
				Status:       StatusActive,
				GroupIDs:     []int64{groupID},
				ModelPricing: pricing,
			}}, nil
		},
		getGroupPlatformsFn: func(_ context.Context, _ []int64) (map[int64]string, error) {
			return map[int64]string{groupID: "anthropic"}, nil
		},
	}
	cs := NewChannelService(repo, nil, nil, nil, nil)
	bs := newTestBillingServiceForResolver()
	return NewModelPricingResolver(cs, bs)
}

// groupIDPtr returns a pointer to groupID 100 (the test constant).
func groupIDPtr() *int64 { v := int64(100); return &v }

// ---------------------------------------------------------------------------
// 1. Token mode overrides
// ---------------------------------------------------------------------------

func TestResolve_WithChannelOverride_TokenFlat(t *testing.T) {
	r := newResolverWithChannel(t, []ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: BillingModeToken,
		InputPrice:  testPtrFloat64(10e-6),
		OutputPrice: testPtrFloat64(50e-6),
	}})

	resolved := r.Resolve(context.Background(), PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: groupIDPtr(),
	})

	require.NotNil(t, resolved)
	require.Equal(t, BillingModeToken, resolved.Mode)
	require.Equal(t, "channel", resolved.Source)
	require.NotNil(t, resolved.BasePricing)
	require.InDelta(t, 10e-6, resolved.BasePricing.InputPricePerToken, 1e-12)
	require.Zero(t, resolved.BasePricing.InputPricePerTokenPriority)
	require.InDelta(t, 50e-6, resolved.BasePricing.OutputPricePerToken, 1e-12)
	require.Zero(t, resolved.BasePricing.OutputPricePerTokenPriority)
}

func TestResolve_WithChannelTimePricingAndGroupMultiplier(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	r := newResolverWithChannel(t, []ChannelModelPricing{{
		Platform:         "anthropic",
		Models:           []string{"claude-sonnet-4"},
		BillingMode:      BillingModeToken,
		InputPrice:       testPtrFloat64(2e-6),
		CacheReadPrice:   testPtrFloat64(0.4e-6),
		ImageInputPrice:  testPtrFloat64(4e-6),
		ImageOutputPrice: testPtrFloat64(6e-6),
		TimeVersions: []PricingTimeVersion{{
			EffectiveFrom:     time.Date(2026, 8, 17, 0, 0, 0, 0, location),
			Timezone:          "Asia/Shanghai",
			DefaultMultiplier: 0.5,
			InputPrice:        testPtrFloat64(3e-6),
			Windows: []PricingTimeWindow{{
				Label: "peak", Weekdays: 127, StartMinute: 540, EndMinute: 720, Multiplier: 1,
			}},
		}},
	}})
	base := r.billingService.fallbackPrices["claude-sonnet-4"]
	base.InputPricePerTokenPriority = 6e-6
	base.OutputPricePerTokenPriority = 30e-6
	base.CacheCreationPricePerTokenPriority = 7.5e-6
	base.CacheReadPricePerTokenPriority = 0.6e-6
	base.CacheCreation5mPrice = 3.75e-6
	base.CacheCreation1hPrice = 7.5e-6

	resolved := r.Resolve(context.Background(), PricingInput{
		Model:     "claude-sonnet-4",
		GroupID:   groupIDPtr(),
		PricingAt: time.Date(2026, 8, 17, 8, 59, 0, 0, location),
	})
	require.NotNil(t, resolved.TimeResolution)
	require.Equal(t, "off_peak", resolved.TimeResolution.PeriodLabel)
	require.InDelta(t, 1.5e-6, resolved.BasePricing.InputPricePerToken, 1e-12,
		"a version-explicit input price is already multiplied by ResolveAt and must not be multiplied twice")
	require.InDelta(t, 3e-6, resolved.BasePricing.InputPricePerTokenPriority, 1e-12)
	require.InDelta(t, 7.5e-6, resolved.BasePricing.OutputPricePerToken, 1e-12,
		"an output price inherited from the base card must receive the active version multiplier")
	require.InDelta(t, 15e-6, resolved.BasePricing.OutputPricePerTokenPriority, 1e-12)
	require.InDelta(t, 1.875e-6, resolved.BasePricing.CacheCreationPricePerToken, 1e-12,
		"an inherited cache-write price must receive the active version multiplier")
	require.InDelta(t, 3.75e-6, resolved.BasePricing.CacheCreationPricePerTokenPriority, 1e-12)
	require.InDelta(t, 1.875e-6, resolved.BasePricing.CacheCreation5mPrice, 1e-12)
	require.InDelta(t, 3.75e-6, resolved.BasePricing.CacheCreation1hPrice, 1e-12)
	require.InDelta(t, 0.2e-6, resolved.BasePricing.CacheReadPricePerToken, 1e-12,
		"a channel-explicit cache-read price was already multiplied by ResolveAt")
	require.InDelta(t, 0.4e-6, resolved.BasePricing.CacheReadPricePerTokenPriority, 1e-12)
	require.InDelta(t, 2e-6, resolved.BasePricing.ImageInputPricePerToken, 1e-12)
	require.InDelta(t, 3e-6, resolved.BasePricing.ImageOutputPricePerToken, 1e-12)

	cost, err := r.billingService.CalculateCostUnified(CostInput{
		Model:          "claude-sonnet-4",
		Tokens:         UsageTokens{InputTokens: 1_000_000},
		RateMultiplier: 1.2,
		Resolver:       r,
		Resolved:       resolved,
	})
	require.NoError(t, err)
	require.InDelta(t, 1.5, cost.TotalCost, 1e-9)
	require.InDelta(t, 1.8, cost.ActualCost, 1e-9)

	cost, err = r.billingService.CalculateCostUnified(CostInput{
		Model: "claude-sonnet-4",
		Tokens: UsageTokens{
			OutputTokens:        1_000_000,
			CacheCreationTokens: 1_000_000,
			CacheReadTokens:     1_000_000,
		},
		RateMultiplier: 1,
		Resolver:       r,
		Resolved:       resolved,
	})
	require.NoError(t, err)
	require.InDelta(t, 7.5, cost.OutputCost, 1e-9)
	require.InDelta(t, 1.875, cost.CacheCreationCost, 1e-9)
	require.InDelta(t, 0.2, cost.CacheReadCost, 1e-9)
	require.InDelta(t, 9.575, cost.TotalCost, 1e-9,
		"the cost path must not add another TimeVersion multiplier after resolution")
}

func TestResolve_DeepSeekOfficialChannelTimePricing(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	r := newResolverWithChannel(t, []ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"deepseek-v4-flash"},
		BillingMode: BillingModeToken,
		InputPrice:  testPtrFloat64(10),
	}})
	r.billingService.fallbackPrices["deepseek-v4-flash"] = &ModelPricing{
		OutputPricePerToken:    20,
		CacheReadPricePerToken: 2,
	}

	tests := []struct {
		name           string
		at             time.Time
		wantMultiplier float64
		wantInput      float64
		wantResolution bool
	}{
		{name: "before official effective date", at: time.Date(2026, 8, 16, 23, 59, 0, 0, location), wantInput: 10},
		{name: "effective date starts off peak", at: time.Date(2026, 8, 17, 0, 0, 0, 0, location), wantMultiplier: 0.5, wantInput: 5, wantResolution: true},
		{name: "morning peak starts", at: time.Date(2026, 8, 17, 9, 0, 0, 0, location), wantMultiplier: 1, wantInput: 10, wantResolution: true},
		{name: "morning peak right boundary", at: time.Date(2026, 8, 17, 12, 0, 0, 0, location), wantMultiplier: 0.5, wantInput: 5, wantResolution: true},
		{name: "afternoon peak starts", at: time.Date(2026, 8, 17, 14, 0, 0, 0, location), wantMultiplier: 1, wantInput: 10, wantResolution: true},
		{name: "afternoon peak right boundary", at: time.Date(2026, 8, 17, 18, 0, 0, 0, location), wantMultiplier: 0.5, wantInput: 5, wantResolution: true},
		{name: "friday peak", at: time.Date(2026, 8, 21, 17, 59, 0, 0, location), wantMultiplier: 1, wantInput: 10, wantResolution: true},
		{name: "weekend is off peak", at: time.Date(2026, 8, 22, 10, 0, 0, 0, location), wantMultiplier: 0.5, wantInput: 5, wantResolution: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved := r.Resolve(context.Background(), PricingInput{
				Model:     "deepseek-v4-flash",
				GroupID:   groupIDPtr(),
				PricingAt: tt.at,
			})
			require.Equal(t, PricingSourceChannel, resolved.Source)
			require.InDelta(t, tt.wantInput, resolved.BasePricing.InputPricePerToken, 1e-12)
			if !tt.wantResolution {
				require.Nil(t, resolved.TimeResolution)
				require.InDelta(t, 20, resolved.BasePricing.OutputPricePerToken, 1e-12)
				return
			}
			require.NotNil(t, resolved.TimeResolution)
			require.Equal(t, "Asia/Shanghai", resolved.TimeResolution.Timezone)
			require.InDelta(t, tt.wantMultiplier, resolved.TimeResolution.Multiplier, 1e-12)
			require.InDelta(t, 20*tt.wantMultiplier, resolved.BasePricing.OutputPricePerToken, 1e-12,
				"the default schedule must also scale fallback fields not set on the channel card")
			require.InDelta(t, 2*tt.wantMultiplier, resolved.BasePricing.CacheReadPricePerToken, 1e-12)
		})
	}
	require.InDelta(t, 20, r.billingService.fallbackPrices["deepseek-v4-flash"].OutputPricePerToken, 1e-12,
		"time pricing must not mutate the shared official fallback card")
}

func TestResolve_DeepSeekExplicitTimeVersionOverridesOfficialSchedule(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	r := newResolverWithChannel(t, []ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"deepseek-v4-pro"},
		BillingMode: BillingModeToken,
		InputPrice:  testPtrFloat64(10),
		TimeVersions: []PricingTimeVersion{{
			ID:                77,
			EffectiveFrom:     time.Date(2026, 8, 17, 0, 0, 0, 0, location),
			Timezone:          "Asia/Shanghai",
			DefaultMultiplier: 0.25,
			OutputPrice:       testPtrFloat64(30),
			Windows: []PricingTimeWindow{{
				Label: "custom_peak", Weekdays: 31, StartMinute: 9 * 60, EndMinute: 12 * 60, Multiplier: 0.8,
			}},
		}},
	}})

	resolved := r.Resolve(context.Background(), PricingInput{
		Model:     "deepseek-v4-pro",
		GroupID:   groupIDPtr(),
		PricingAt: time.Date(2026, 8, 17, 10, 0, 0, 0, location),
	})
	require.NotNil(t, resolved.TimeResolution)
	require.Equal(t, int64(77), resolved.TimeResolution.VersionID)
	require.Equal(t, "custom_peak", resolved.TimeResolution.PeriodLabel)
	require.InDelta(t, 0.8, resolved.TimeResolution.Multiplier, 1e-12)
	require.InDelta(t, 8, resolved.BasePricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 24, resolved.BasePricing.OutputPricePerToken, 1e-12,
		"the version-explicit output price must be multiplied exactly once")
}

func TestResolve_ExplicitTimeVersionScalesIntervalPricesAndInheritedBuckets(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	r := newResolverWithChannel(t, []ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: BillingModeToken,
		InputPrice:  testPtrFloat64(2e-6),
		Intervals: []PricingInterval{{
			MinTokens:      0,
			MaxTokens:      testPtrInt(128000),
			InputPrice:     testPtrFloat64(4e-6),
			CacheReadPrice: testPtrFloat64(2e-6),
		}},
		TimeVersions: []PricingTimeVersion{{
			ID:                88,
			EffectiveFrom:     time.Date(2026, 8, 17, 0, 0, 0, 0, location),
			Timezone:          "Asia/Shanghai",
			DefaultMultiplier: 0.5,
		}},
	}})

	resolved := r.Resolve(context.Background(), PricingInput{
		Model:     "claude-sonnet-4",
		GroupID:   groupIDPtr(),
		PricingAt: time.Date(2026, 8, 17, 8, 0, 0, 0, location),
	})
	require.NotNil(t, resolved.TimeResolution)
	require.InDelta(t, 0.5, resolved.TimeResolution.Multiplier, 1e-12)
	require.Len(t, resolved.Intervals, 1)
	require.InDelta(t, 2e-6, *resolved.Intervals[0].InputPrice, 1e-12,
		"ResolveAt does not scale interval fields, so the resolver must do it after merging")
	require.InDelta(t, 1e-6, *resolved.Intervals[0].CacheReadPrice, 1e-12)

	intervalPricing := r.GetIntervalPricing(resolved, 1000)
	require.InDelta(t, 2e-6, intervalPricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 7.5e-6, intervalPricing.OutputPricePerToken, 1e-12,
		"an interval without output price must inherit the already time-scaled base output")
	require.InDelta(t, 1.875e-6, intervalPricing.CacheCreationPricePerToken, 1e-12,
		"an interval without cache-write price must inherit the time-scaled base cache-write price")
	require.InDelta(t, 1e-6, intervalPricing.CacheReadPricePerToken, 1e-12)

	basePricing := r.GetIntervalPricing(resolved, 200000)
	require.InDelta(t, 1e-6, basePricing.InputPricePerToken, 1e-12,
		"the channel flat input price must not be multiplied twice")
	require.InDelta(t, 7.5e-6, basePricing.OutputPricePerToken, 1e-12)
}

func TestResolve_DeepSeekOfficialFallbackTimePricingUsesRequestStart(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	bs := newTestBillingServiceForResolver()
	bs.fallbackPrices["deepseek-v4-flash"] = &ModelPricing{InputPricePerToken: 3, OutputPricePerToken: 9}
	bs.fallbackPrices["deepseek-v4-flash-vision-exp"] = bs.fallbackPrices["deepseek-v4-flash"]
	bs.fallbackPrices["deepseek-v4-pro"] = &ModelPricing{InputPricePerToken: 9, OutputPricePerToken: 27}
	r := NewModelPricingResolver(nil, bs)
	offPeak := time.Date(2026, 8, 17, 13, 0, 0, 0, location)

	tests := []struct {
		model     string
		wantInput float64
	}{
		{model: "deepseek-v4-flash", wantInput: 1.5},
		{model: "deepseek-v4-flash-0731", wantInput: 1.5},
		{model: "deepseek-v4-flash-vision-exp", wantInput: 1.5},
		{model: "deepseek-v4-pro", wantInput: 4.5},
		{model: "deepseek-v4-pro-0813", wantInput: 4.5},
		{model: "deepseek-chat", wantInput: 1.5},
		{model: "deepseek-reasoner", wantInput: 1.5},
		{model: "provider/deepseek-v4-flash:free", wantInput: 1.5},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			resolved := r.Resolve(context.Background(), PricingInput{Model: tt.model, PricingAt: offPeak})
			require.NotNil(t, resolved.TimeResolution)
			require.InDelta(t, 0.5, resolved.TimeResolution.Multiplier, 1e-12)
			require.InDelta(t, tt.wantInput, resolved.BasePricing.InputPricePerToken, 1e-12)
		})
	}

	peakRequestStart := time.Date(2026, 8, 17, 10, 0, 0, 0, location)
	ctx := withPricingAt(context.Background(), peakRequestStart)
	resolved := r.Resolve(ctx, PricingInput{Model: "deepseek-v4-flash"})
	require.Equal(t, peakRequestStart, resolved.TimeResolution.PricingAt)
	require.InDelta(t, 1, resolved.TimeResolution.Multiplier, 1e-12)
	require.InDelta(t, 3, resolved.BasePricing.InputPricePerToken, 1e-12)

	resolved = r.Resolve(ctx, PricingInput{Model: "deepseek-v4-flash", PricingAt: offPeak})
	require.Equal(t, offPeak, resolved.TimeResolution.PricingAt, "an explicit request start must win over context fallback")
	require.InDelta(t, 1.5, resolved.BasePricing.InputPricePerToken, 1e-12)

	unknownResolver := newResolverWithChannel(t, []ChannelModelPricing{{
		Platform: "anthropic", Models: []string{"deepseek-v4"}, BillingMode: BillingModeToken,
		InputPrice: testPtrFloat64(7),
	}})
	unknown := unknownResolver.Resolve(context.Background(), PricingInput{Model: "deepseek-v4", GroupID: groupIDPtr(), PricingAt: offPeak})
	require.Nil(t, unknown.TimeResolution)
	require.InDelta(t, 7, unknown.BasePricing.InputPricePerToken, 1e-12,
		"unlisted DeepSeek models must not inherit the V4 schedule")
}

func TestResolve_WithChannelOverride_TokenPartialOverride(t *testing.T) {
	// Channel only sets InputPrice; OutputPrice should remain from the base (LiteLLM/fallback).
	r := newResolverWithChannel(t, []ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: BillingModeToken,
		InputPrice:  testPtrFloat64(20e-6),
		// OutputPrice intentionally nil
	}})

	resolved := r.Resolve(context.Background(), PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: groupIDPtr(),
	})

	require.NotNil(t, resolved)
	require.Equal(t, "channel", resolved.Source)
	require.NotNil(t, resolved.BasePricing)
	// InputPrice overridden by channel
	require.InDelta(t, 20e-6, resolved.BasePricing.InputPricePerToken, 1e-12)
	// OutputPrice kept from base (fallback: 15e-6)
	require.InDelta(t, 15e-6, resolved.BasePricing.OutputPricePerToken, 1e-12)
}

func TestResolve_WithChannelOverride_TokenWithIntervals(t *testing.T) {
	r := newResolverWithChannel(t, []ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: BillingModeToken,
		Intervals: []PricingInterval{
			{MinTokens: 0, MaxTokens: testPtrInt(128000), InputPrice: testPtrFloat64(2e-6), OutputPrice: testPtrFloat64(8e-6)},
			{MinTokens: 128000, MaxTokens: nil, InputPrice: testPtrFloat64(4e-6), OutputPrice: testPtrFloat64(16e-6)},
		},
	}})

	resolved := r.Resolve(context.Background(), PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: groupIDPtr(),
		Group:   &Group{LongContextPricingEnabled: false},
	})

	require.NotNil(t, resolved)
	require.Equal(t, "channel", resolved.Source)
	require.False(t, resolved.longContextPricingEnabled)
	require.Len(t, resolved.Intervals, 2)

	// GetIntervalPricing should use channel intervals
	iv := r.GetIntervalPricing(resolved, 50000)
	require.NotNil(t, iv)
	require.InDelta(t, 2e-6, iv.InputPricePerToken, 1e-12)
	require.InDelta(t, 8e-6, iv.OutputPricePerToken, 1e-12)

	iv2 := r.GetIntervalPricing(resolved, 200000)
	require.NotNil(t, iv2)
	require.InDelta(t, 4e-6, iv2.InputPricePerToken, 1e-12)
	require.InDelta(t, 16e-6, iv2.OutputPricePerToken, 1e-12)
}

func TestResolve_WithChannelOverride_TokenNilBasePricing(t *testing.T) {
	// Base pricing is nil (unknown model), channel has flat prices → creates new BasePricing.
	r := newResolverWithChannel(t, []ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"unknown-model-xyz"},
		BillingMode: BillingModeToken,
		InputPrice:  testPtrFloat64(7e-6),
		OutputPrice: testPtrFloat64(21e-6),
	}})

	resolved := r.Resolve(context.Background(), PricingInput{
		Model:   "unknown-model-xyz",
		GroupID: groupIDPtr(),
	})

	require.NotNil(t, resolved)
	require.Equal(t, "channel", resolved.Source)
	// BasePricing was nil from resolveBasePricing but applyTokenOverrides creates a new one
	require.NotNil(t, resolved.BasePricing)
	require.InDelta(t, 7e-6, resolved.BasePricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 21e-6, resolved.BasePricing.OutputPricePerToken, 1e-12)
}

// ---------------------------------------------------------------------------
// 2. Per-request mode overrides
// ---------------------------------------------------------------------------

func TestResolve_WithChannelOverride_PerRequest(t *testing.T) {
	r := newResolverWithChannel(t, []ChannelModelPricing{{
		Platform:        "anthropic",
		Models:          []string{"claude-sonnet-4"},
		BillingMode:     BillingModePerRequest,
		PerRequestPrice: testPtrFloat64(0.05),
		Intervals: []PricingInterval{
			{MinTokens: 0, MaxTokens: testPtrInt(128000), PerRequestPrice: testPtrFloat64(0.03)},
			{MinTokens: 128000, MaxTokens: nil, PerRequestPrice: testPtrFloat64(0.10)},
		},
	}})

	resolved := r.Resolve(context.Background(), PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: groupIDPtr(),
	})

	require.NotNil(t, resolved)
	require.Equal(t, BillingModePerRequest, resolved.Mode)
	require.Equal(t, "channel", resolved.Source)
	require.InDelta(t, 0.05, resolved.DefaultPerRequestPrice, 1e-12)
	require.Len(t, resolved.RequestTiers, 2)

	// Verify tier lookups
	require.InDelta(t, 0.03, r.GetRequestTierPriceByContext(resolved, 50000), 1e-12)
	require.InDelta(t, 0.10, r.GetRequestTierPriceByContext(resolved, 200000), 1e-12)
}

func TestResolve_WithChannelOverride_PerRequestNilPrice(t *testing.T) {
	// PerRequestPrice nil → DefaultPerRequestPrice stays 0.
	r := newResolverWithChannel(t, []ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: BillingModePerRequest,
		// PerRequestPrice intentionally nil
		Intervals: []PricingInterval{
			{MinTokens: 0, MaxTokens: testPtrInt(128000), PerRequestPrice: testPtrFloat64(0.02)},
		},
	}})

	resolved := r.Resolve(context.Background(), PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: groupIDPtr(),
	})

	require.NotNil(t, resolved)
	require.Equal(t, BillingModePerRequest, resolved.Mode)
	require.InDelta(t, 0.0, resolved.DefaultPerRequestPrice, 1e-12)
	require.Len(t, resolved.RequestTiers, 1)
}

// ---------------------------------------------------------------------------
// 3. Image mode overrides
// ---------------------------------------------------------------------------

func TestResolve_WithChannelOverride_Image(t *testing.T) {
	r := newResolverWithChannel(t, []ChannelModelPricing{{
		Platform:        "anthropic",
		Models:          []string{"claude-sonnet-4"},
		BillingMode:     BillingModeImage,
		PerRequestPrice: testPtrFloat64(0.08),
		Intervals: []PricingInterval{
			{TierLabel: "1K", PerRequestPrice: testPtrFloat64(0.04)},
			{TierLabel: "2K", PerRequestPrice: testPtrFloat64(0.08)},
			{TierLabel: "4K", PerRequestPrice: testPtrFloat64(0.16)},
		},
	}})

	resolved := r.Resolve(context.Background(), PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: groupIDPtr(),
	})

	require.NotNil(t, resolved)
	require.Equal(t, BillingModeImage, resolved.Mode)
	require.Equal(t, "channel", resolved.Source)
	require.InDelta(t, 0.08, resolved.DefaultPerRequestPrice, 1e-12)
	require.Len(t, resolved.RequestTiers, 3)
}

func TestResolve_WithChannelOverride_ImageTierLabels(t *testing.T) {
	r := newResolverWithChannel(t, []ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: BillingModeImage,
		Intervals: []PricingInterval{
			{TierLabel: "1K", PerRequestPrice: testPtrFloat64(0.04)},
			{TierLabel: "2K", PerRequestPrice: testPtrFloat64(0.08)},
			{TierLabel: "4K", PerRequestPrice: testPtrFloat64(0.16)},
		},
	}})

	resolved := r.Resolve(context.Background(), PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: groupIDPtr(),
	})

	require.InDelta(t, 0.04, r.GetRequestTierPrice(resolved, "1K"), 1e-12)
	require.InDelta(t, 0.08, r.GetRequestTierPrice(resolved, "2K"), 1e-12)
	require.InDelta(t, 0.16, r.GetRequestTierPrice(resolved, "4K"), 1e-12)
	require.InDelta(t, 0.0, r.GetRequestTierPrice(resolved, "8K"), 1e-12) // not found
}

// ---------------------------------------------------------------------------
// 4. Source tracking & default mode
// ---------------------------------------------------------------------------

func TestResolve_WithChannelOverride_SourceIsChannel(t *testing.T) {
	r := newResolverWithChannel(t, []ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: BillingModeToken,
		InputPrice:  testPtrFloat64(1e-6),
	}})

	resolved := r.Resolve(context.Background(), PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: groupIDPtr(),
	})

	require.Equal(t, "channel", resolved.Source)
}

func TestResolve_WithChannelOverride_DefaultMode(t *testing.T) {
	// Channel pricing with empty BillingMode → defaults to BillingModeToken.
	r := newResolverWithChannel(t, []ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: "", // intentionally empty
		InputPrice:  testPtrFloat64(5e-6),
	}})

	resolved := r.Resolve(context.Background(), PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: groupIDPtr(),
	})

	require.Equal(t, "channel", resolved.Source)
	require.Equal(t, BillingModeToken, resolved.Mode)
	require.NotNil(t, resolved.BasePricing)
	require.InDelta(t, 5e-6, resolved.BasePricing.InputPricePerToken, 1e-12)
}

// ---------------------------------------------------------------------------
// 5. GetIntervalPricing integration after channel override
// ---------------------------------------------------------------------------

func TestGetIntervalPricing_WithChannelIntervals(t *testing.T) {
	// Channel provides intervals that override the base pricing path.
	r := newResolverWithChannel(t, []ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: BillingModeToken,
		Intervals: []PricingInterval{
			{MinTokens: 0, MaxTokens: testPtrInt(100000), InputPrice: testPtrFloat64(1e-6), OutputPrice: testPtrFloat64(5e-6)},
			{MinTokens: 100000, MaxTokens: nil, InputPrice: testPtrFloat64(2e-6), OutputPrice: testPtrFloat64(10e-6)},
		},
	}})

	resolved := r.Resolve(context.Background(), PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: groupIDPtr(),
	})

	// Token count 50000 matches first interval
	pricing := r.GetIntervalPricing(resolved, 50000)
	require.NotNil(t, pricing)
	require.InDelta(t, 1e-6, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 5e-6, pricing.OutputPricePerToken, 1e-12)

	// Token count 150000 matches second interval
	pricing2 := r.GetIntervalPricing(resolved, 150000)
	require.NotNil(t, pricing2)
	require.InDelta(t, 2e-6, pricing2.InputPricePerToken, 1e-12)
	require.InDelta(t, 10e-6, pricing2.OutputPricePerToken, 1e-12)
}

func TestGetIntervalPricing_ChannelIntervalsNoMatch(t *testing.T) {
	// Channel intervals don't match token count → falls back to BasePricing.
	r := newResolverWithChannel(t, []ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: BillingModeToken,
		InputPrice:  testPtrFloat64(4e-6),
		Intervals: []PricingInterval{
			// Only covers tokens > 50000
			{MinTokens: 50000, MaxTokens: testPtrInt(200000), InputPrice: testPtrFloat64(9e-6)},
		},
	}})

	resolved := r.Resolve(context.Background(), PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: groupIDPtr(),
	})

	// Token count 1000 doesn't match any interval (1000 <= 50000 minTokens)
	pricing := r.GetIntervalPricing(resolved, 1000)
	// Should fall back to BasePricing after applying the channel default.
	require.NotNil(t, pricing)
	require.Equal(t, resolved.BasePricing, pricing)
	require.InDelta(t, 4e-6, pricing.InputPricePerToken, 1e-12)
}

// ===========================================================================
// 6. Error path tests
// ===========================================================================

func TestResolve_WithChannelOverride_CacheError(t *testing.T) {
	// When ListAll returns an error, the ChannelService cache build fails.
	// Resolve should gracefully fall back to base pricing without panicking.
	repo := &mockChannelRepository{
		listAllFn: func(_ context.Context) ([]Channel, error) {
			return nil, errors.New("database unavailable")
		},
	}
	cs := NewChannelService(repo, nil, nil, nil, nil)
	bs := newTestBillingServiceForResolver()
	r := NewModelPricingResolver(cs, bs)

	gid := int64(100)
	resolved := r.Resolve(context.Background(), PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: &gid,
	})

	require.NotNil(t, resolved)
	// Should NOT panic, should NOT have source "channel"
	require.NotEqual(t, "channel", resolved.Source)
	// Base pricing should still be present (from BillingService fallback)
	require.NotNil(t, resolved.BasePricing)
	require.InDelta(t, 3e-6, resolved.BasePricing.InputPricePerToken, 1e-12)
}

// ===========================================================================
// 7. GetRequestTierPriceByContext boundary tests
// ===========================================================================

func TestGetRequestTierPriceByContext_EmptyTiers(t *testing.T) {
	bs := newTestBillingServiceForResolver()
	r := NewModelPricingResolver(&ChannelService{}, bs)

	resolved := &ResolvedPricing{
		Mode:         BillingModePerRequest,
		RequestTiers: nil, // empty
	}

	price := r.GetRequestTierPriceByContext(resolved, 50000)
	require.InDelta(t, 0.0, price, 1e-12)

	// Also test with explicit empty slice
	resolved2 := &ResolvedPricing{
		Mode:         BillingModePerRequest,
		RequestTiers: []PricingInterval{},
	}

	price2 := r.GetRequestTierPriceByContext(resolved2, 50000)
	require.InDelta(t, 0.0, price2, 1e-12)
}

func TestGetRequestTierPriceByContext_ExactBoundary(t *testing.T) {
	bs := newTestBillingServiceForResolver()
	r := NewModelPricingResolver(&ChannelService{}, bs)

	resolved := &ResolvedPricing{
		Mode: BillingModePerRequest,
		RequestTiers: []PricingInterval{
			{MinTokens: 0, MaxTokens: testPtrInt(128000), PerRequestPrice: testPtrFloat64(0.05)},
			{MinTokens: 128000, MaxTokens: nil, PerRequestPrice: testPtrFloat64(0.10)},
		},
	}

	// totalContextTokens = 128000 exactly:
	// FindMatchingInterval checks: totalTokens > MinTokens && totalTokens <= MaxTokens
	// For first interval: 128000 > 0 (true) && 128000 <= 128000 (true) → matches first interval
	price := r.GetRequestTierPriceByContext(resolved, 128000)
	require.InDelta(t, 0.05, price, 1e-12)

	// totalContextTokens = 128001 should match second interval
	// For first interval: 128001 > 0 (true) && 128001 <= 128000 (false) → no match
	// For second interval: 128001 > 128000 (true) && MaxTokens == nil → matches
	price2 := r.GetRequestTierPriceByContext(resolved, 128001)
	require.InDelta(t, 0.10, price2, 1e-12)
}

// ===========================================================================
// 8. filterValidIntervals
// ===========================================================================

func TestFilterValidIntervals(t *testing.T) {
	tests := []struct {
		name      string
		intervals []PricingInterval
		wantLen   int
	}{
		{
			name:      "empty list",
			intervals: nil,
			wantLen:   0,
		},
		{
			name: "all-nil interval filtered out",
			intervals: []PricingInterval{
				{MinTokens: 0, MaxTokens: testPtrInt(128000)},
			},
			wantLen: 0,
		},
		{
			name: "interval with only InputPrice kept",
			intervals: []PricingInterval{
				{MinTokens: 0, MaxTokens: testPtrInt(128000), InputPrice: testPtrFloat64(1e-6)},
			},
			wantLen: 1,
		},
		{
			name: "interval with only OutputPrice kept",
			intervals: []PricingInterval{
				{MinTokens: 0, MaxTokens: testPtrInt(128000), OutputPrice: testPtrFloat64(2e-6)},
			},
			wantLen: 1,
		},
		{
			name: "interval with only CacheWritePrice kept",
			intervals: []PricingInterval{
				{MinTokens: 0, CacheWritePrice: testPtrFloat64(3e-6)},
			},
			wantLen: 1,
		},
		{
			name: "interval with only CacheReadPrice kept",
			intervals: []PricingInterval{
				{MinTokens: 0, CacheReadPrice: testPtrFloat64(0.5e-6)},
			},
			wantLen: 1,
		},
		{
			name: "interval with only PerRequestPrice kept",
			intervals: []PricingInterval{
				{TierLabel: "1K", PerRequestPrice: testPtrFloat64(0.04)},
			},
			wantLen: 1,
		},
		{
			name: "interval with only multiplier kept",
			intervals: []PricingInterval{
				{MinTokens: 272000, InputMultiplier: testPtrFloat64(2)},
			},
			wantLen: 1,
		},
		{
			name: "mixed valid and invalid",
			intervals: []PricingInterval{
				{MinTokens: 0, MaxTokens: testPtrInt(128000), InputPrice: testPtrFloat64(1e-6)},
				{MinTokens: 128000, MaxTokens: nil}, // all-nil → filtered out
				{MinTokens: 256000, OutputPrice: testPtrFloat64(5e-6)},
			},
			wantLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterValidIntervals(tt.intervals)
			require.Len(t, result, tt.wantLen)
		})
	}
}

// ===========================================================================
// 9. ImageOutputPriceExplicit tests
// ===========================================================================

func TestApplyTokenOverrides_FlatSetsImageOutputPriceExplicit(t *testing.T) {
	r := newResolverWithChannel(t, []ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: BillingModeToken,
		InputPrice:  testPtrFloat64(3e-6),
		OutputPrice: testPtrFloat64(15e-6),
		// ImageOutputPrice intentionally nil
	}})
	resolved := r.Resolve(context.Background(), PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: groupIDPtr(),
	})

	require.Equal(t, PricingSourceChannel, resolved.Source)
	require.True(t, resolved.BasePricing.ImageOutputPriceExplicit)
	require.Equal(t, 0.0, resolved.BasePricing.ImageOutputPricePerToken)
}

func TestApplyTokenOverrides_FlatWithImageOutputPriceSetsExplicit(t *testing.T) {
	r := newResolverWithChannel(t, []ChannelModelPricing{{
		Platform:         "anthropic",
		Models:           []string{"claude-sonnet-4"},
		BillingMode:      BillingModeToken,
		InputPrice:       testPtrFloat64(3e-6),
		OutputPrice:      testPtrFloat64(15e-6),
		ImageOutputPrice: testPtrFloat64(50e-6),
	}})
	resolved := r.Resolve(context.Background(), PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: groupIDPtr(),
	})

	require.True(t, resolved.BasePricing.ImageOutputPriceExplicit)
	require.InDelta(t, 50e-6, resolved.BasePricing.ImageOutputPricePerToken, 1e-12)
}

func TestApplyTokenOverrides_IntervalSetsImageOutputPriceExplicit(t *testing.T) {
	r := newResolverWithChannel(t, []ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: BillingModeToken,
		// No ImageOutputPrice
		Intervals: []PricingInterval{
			{MinTokens: 0, MaxTokens: testPtrInt(100000), InputPrice: testPtrFloat64(3e-6), OutputPrice: testPtrFloat64(15e-6)},
		},
	}})
	resolved := r.Resolve(context.Background(), PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: groupIDPtr(),
	})

	// BasePricing should have explicit mark (for interval fallback)
	require.True(t, resolved.BasePricing.ImageOutputPriceExplicit)
	require.Equal(t, 0.0, resolved.BasePricing.ImageOutputPricePerToken)

	// intervalToModelPricing should also have explicit mark
	pricing := r.GetIntervalPricing(resolved, 50000)
	require.True(t, pricing.ImageOutputPriceExplicit)
	require.Equal(t, 0.0, pricing.ImageOutputPricePerToken)
}

// ===========================================================================
// 10. Regression: channel overrides must not pollute fallbackPrices
// ===========================================================================

// TestApplyTokenOverrides_FlatDoesNotPolluteFallbackPrices verifies that the
// flat-override path in applyTokenOverrides clones the BasePricing struct
// before mutation, so the shared fallbackPrices map entry is not written through.
func TestApplyTokenOverrides_FlatDoesNotPolluteFallbackPrices(t *testing.T) {
	r := newResolverWithChannel(t, []ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: BillingModeToken,
		InputPrice:  testPtrFloat64(10e-6), // base is 3e-6
		OutputPrice: testPtrFloat64(50e-6), // base is 15e-6
	}})

	resolved := r.Resolve(context.Background(), PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: groupIDPtr(),
	})

	// Resolved pricing should reflect the channel override
	require.NotNil(t, resolved)
	require.InDelta(t, 10e-6, resolved.BasePricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 50e-6, resolved.BasePricing.OutputPricePerToken, 1e-12)

	// Global fallbackPrices must NOT be polluted
	fp := r.billingService.fallbackPrices["claude-sonnet-4"]
	require.InDelta(t, 3e-6, fp.InputPricePerToken, 1e-12, "fallback InputPricePerToken polluted")
	require.InDelta(t, 15e-6, fp.OutputPricePerToken, 1e-12, "fallback OutputPricePerToken polluted")
	require.False(t, fp.ImageOutputPriceExplicit, "fallback ImageOutputPriceExplicit polluted")
}

// TestApplyTokenOverrides_IntervalDoesNotPolluteFallbackPrices verifies that
// the interval-override path also clones before mutation.
func TestApplyTokenOverrides_IntervalDoesNotPolluteFallbackPrices(t *testing.T) {
	r := newResolverWithChannel(t, []ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: BillingModeToken,
		Intervals: []PricingInterval{
			{MinTokens: 0, MaxTokens: testPtrInt(100000), InputPrice: testPtrFloat64(2e-6), OutputPrice: testPtrFloat64(8e-6)},
		},
	}})

	resolved := r.Resolve(context.Background(), PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: groupIDPtr(),
	})

	require.NotNil(t, resolved)
	require.True(t, resolved.BasePricing.ImageOutputPriceExplicit)

	// Global fallbackPrices must NOT be polluted
	fp := r.billingService.fallbackPrices["claude-sonnet-4"]
	require.InDelta(t, 3e-6, fp.InputPricePerToken, 1e-12, "fallback InputPricePerToken polluted")
	require.InDelta(t, 15e-6, fp.OutputPricePerToken, 1e-12, "fallback OutputPricePerToken polluted")
	require.False(t, fp.ImageOutputPriceExplicit, "fallback ImageOutputPriceExplicit polluted")
}

func TestResolve_GroupPricingOverridesChannel(t *testing.T) {
	r := newResolverWithChannel(t, []ChannelModelPricing{{
		Platform: "anthropic", Models: []string{"claude-sonnet-4"}, BillingMode: BillingModeToken,
		InputPrice: testPtrFloat64(10e-6), OutputPrice: testPtrFloat64(20e-6),
	}})
	group := &Group{ID: 100, ModelPricing: []ChannelModelPricing{{
		Models: []string{"claude-sonnet-*"}, BillingMode: BillingModeToken,
		InputPrice: testPtrFloat64(1e-6), OutputPrice: testPtrFloat64(2e-6),
	}}}
	resolved := r.Resolve(context.Background(), PricingInput{Model: "claude-sonnet-4", GroupID: groupIDPtr(), Group: group})

	require.Equal(t, PricingSourceGroup, resolved.Source)
	require.InDelta(t, 1e-6, resolved.BasePricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 2e-6, resolved.BasePricing.OutputPricePerToken, 1e-12)
}

func TestResolve_GroupLongContextUsesPresetNotCustomIntervals(t *testing.T) {
	bs := newTestBillingServiceForResolver()
	bs.fallbackPrices["claude-sonnet-4"].LongContextInputThreshold = 200000
	bs.fallbackPrices["claude-sonnet-4"].LongContextThresholdInclusive = true
	bs.fallbackPrices["claude-sonnet-4"].LongContextInputMultiplier = 2
	bs.fallbackPrices["claude-sonnet-4"].LongContextOutputMultiplier = 2
	r := NewModelPricingResolver(nil, bs)
	group := &Group{ID: 100, ModelPricing: []ChannelModelPricing{{
		Models: []string{"claude-sonnet-4"}, BillingMode: BillingModeToken,
		InputPrice: testPtrFloat64(1e-6), OutputPrice: testPtrFloat64(2e-6),
		Intervals: []PricingInterval{
			{MinTokens: 0, MaxTokens: testPtrInt(200000), InputPrice: testPtrFloat64(9e-6)},
			{MinTokens: 200000, InputPrice: testPtrFloat64(18e-6)},
		},
	}}}

	resolved := r.Resolve(context.Background(), PricingInput{Model: "claude-sonnet-4", Group: group})
	require.False(t, resolved.longContextPricingEnabled)
	require.Empty(t, resolved.Intervals, "group token intervals are not a user-facing long-context ladder")
	require.InDelta(t, 1e-6, r.GetIntervalPricing(resolved, 300000).InputPricePerToken, 1e-12)
	require.Equal(t, 200000, resolved.BasePricing.LongContextInputThreshold)

	group.LongContextPricingEnabled = true
	resolved = r.Resolve(context.Background(), PricingInput{Model: "claude-sonnet-4", Group: group})
	require.True(t, resolved.longContextPricingEnabled)
	require.Empty(t, resolved.Intervals)
	require.InDelta(t, 1e-6, r.GetIntervalPricing(resolved, 300000).InputPricePerToken, 1e-12)
	require.Equal(t, 200000, resolved.BasePricing.LongContextInputThreshold)
	require.InDelta(t, 2.0, resolved.BasePricing.LongContextInputMultiplier, 1e-12)
}

func TestCalculateCostUnified_UsesContinuousMediaUnits(t *testing.T) {
	bs := newTestBillingServiceForResolver()
	r := NewModelPricingResolver(nil, bs)
	price := 0.08
	group := &Group{ModelPricing: []ChannelModelPricing{{
		Models: []string{"grok-voice-think-fast-2.0"}, BillingMode: BillingModePerRequest,
		PerRequestPrice: &price,
	}}}
	cost, err := bs.CalculateCostUnified(CostInput{
		Ctx: context.Background(), Model: "grok-voice-think-fast-2.0", Group: group,
		UsageUnits: 1.5, RateMultiplier: 1, Resolver: r,
	})
	require.NoError(t, err)
	require.InDelta(t, 0.12, cost.TotalCost, 1e-12)
}
