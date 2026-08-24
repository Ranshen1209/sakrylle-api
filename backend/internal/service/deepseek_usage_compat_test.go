//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeepSeekClaudeUsageAliasesSplitPromptCacheTokens(t *testing.T) {
	usage := parseClaudeUsageFromResponseBody([]byte(`{"usage":{"prompt_tokens":120,"prompt_cache_hit_tokens":80,"prompt_cache_miss_tokens":40,"completion_tokens":7}}`))
	require.Equal(t, 120, usage.InputTokens)
	require.Equal(t, 80, usage.CacheReadInputTokens)
	// Anthropic-shaped parser has no completion_tokens field; this assertion
	// documents that only the cache/input aliases are normalized here.
	require.Zero(t, usage.OutputTokens)
}

func TestDeepSeekClaudeUsageAliasesInferTotalWhenPromptMissing(t *testing.T) {
	usage := &ClaudeUsage{CacheReadInputTokens: 999}
	parseSSEUsagePassthrough(`{"type":"message_start","message":{"usage":{"prompt_cache_hit_tokens":12,"prompt_cache_miss_tokens":8}}}`, usage)
	require.Equal(t, 20, usage.InputTokens)
	require.Equal(t, 12, usage.CacheReadInputTokens)
}

func TestDeepSeekClaudeUsageAliasesReplaceZeroInputPlaceholder(t *testing.T) {
	usage := parseClaudeUsageFromResponseBody([]byte(`{"usage":{"input_tokens":0,"prompt_cache_hit_tokens":4,"prompt_cache_miss_tokens":6}}`))
	require.Equal(t, 10, usage.InputTokens)
	require.Equal(t, 4, usage.CacheReadInputTokens)
	require.True(t, usage.inputTokensIncludeCache)
}

func TestDeepSeekClaudeUsageExplicitZeroHitWins(t *testing.T) {
	usage := &ClaudeUsage{CacheReadInputTokens: 55}
	parseSSEUsagePassthrough(`{"type":"message_start","message":{"usage":{"prompt_tokens":40,"prompt_cache_hit_tokens":0,"cached_tokens":55}}}`, usage)
	require.Equal(t, 40, usage.InputTokens)
	require.Zero(t, usage.CacheReadInputTokens)
}

func TestDeepSeekClaudeUsageMissOnlyClearsCanonicalCacheHit(t *testing.T) {
	usage := parseClaudeUsageFromResponseBody([]byte(`{"usage":{"prompt_cache_miss_tokens":100,"cached_tokens":55}}`))
	require.Equal(t, 100, usage.InputTokens)
	require.Zero(t, usage.CacheReadInputTokens)
	require.True(t, usage.inputTokensIncludeCache)

	normalizeDeepSeekClaudeUsageForBilling(usage)
	require.Equal(t, 100, usage.InputTokens, "miss-only total has no cache-hit bucket to subtract")
	require.Zero(t, usage.CacheReadInputTokens)
	require.False(t, usage.inputTokensIncludeCache)
}

func TestGatewaySSEUsageAppliesDeepSeekAliases(t *testing.T) {
	usage := &ClaudeUsage{}
	(&GatewayService{}).parseSSEUsage(`{"type":"message_delta","usage":{"prompt_cache_hit_tokens":4,"prompt_cache_miss_tokens":6,"output_tokens":3}}`, usage)
	require.Equal(t, 10, usage.InputTokens)
	require.Equal(t, 4, usage.CacheReadInputTokens)
	require.Equal(t, 3, usage.OutputTokens)
}

func TestNormalizeDeepSeekClaudeUsageForAccountOnlyDeepSeek(t *testing.T) {
	newUsage := func() *ClaudeUsage {
		return &ClaudeUsage{
			InputTokens:              100,
			CacheReadInputTokens:     40,
			CacheCreationInputTokens: 10,
			inputTokensIncludeCache:  true,
		}
	}

	anthropic := newUsage()
	normalizeDeepSeekClaudeUsageForAccount(&Account{Platform: PlatformAnthropic}, anthropic)
	require.Equal(t, 100, anthropic.InputTokens)
	require.Equal(t, 40, anthropic.CacheReadInputTokens)
	require.Equal(t, 10, anthropic.CacheCreationInputTokens)
	require.True(t, anthropic.inputTokensIncludeCache)

	deepseek := newUsage()
	normalizeDeepSeekClaudeUsageForAccount(&Account{Platform: PlatformDeepseek}, deepseek)
	require.Equal(t, 50, deepseek.InputTokens)
	require.Equal(t, 40, deepseek.CacheReadInputTokens)
	require.Equal(t, 10, deepseek.CacheCreationInputTokens)
	require.False(t, deepseek.inputTokensIncludeCache)
}

func TestDeepSeekClaudeUsageAliasesClampNegativeValues(t *testing.T) {
	usage := parseClaudeUsageFromResponseBody([]byte(`{"usage":{"prompt_cache_hit_tokens":-8,"prompt_cache_miss_tokens":-3}}`))

	require.Zero(t, usage.CacheReadInputTokens)
	require.Zero(t, usage.InputTokens)
	require.True(t, usage.inputTokensIncludeCache)

	normalizeDeepSeekClaudeUsageForBilling(usage)
	require.Zero(t, usage.InputTokens)
	require.Zero(t, usage.CacheReadInputTokens)
	require.False(t, usage.inputTokensIncludeCache)
}

func TestDeepSeekClaudeUsageAliasesNormalizeBeforeGenericBilling(t *testing.T) {
	usage := parseClaudeUsageFromResponseBody([]byte(`{"usage":{"prompt_tokens":100,"prompt_cache_hit_tokens":40,"prompt_cache_miss_tokens":60}}`))
	require.Equal(t, 100, usage.InputTokens, "parser keeps DeepSeek total for native OpenAI conversion")
	require.Equal(t, 40, usage.CacheReadInputTokens)
	require.True(t, usage.inputTokensIncludeCache)

	normalizeDeepSeekClaudeUsageForBilling(usage)
	require.Equal(t, 60, usage.InputTokens, "generic Anthropic billing must charge only cache-miss input")
	require.Equal(t, 40, usage.CacheReadInputTokens)
	require.False(t, usage.inputTokensIncludeCache)

	pricing := newTestBillingService()
	cost, err := pricing.CalculateCost("deepseek-v4-flash", UsageTokens{
		InputTokens:     usage.InputTokens,
		CacheReadTokens: usage.CacheReadInputTokens,
		OutputTokens:    7,
	}, 1)
	require.NoError(t, err)
	want := float64(60)*deepSeekV4FlashInputPricePerToken + float64(40)*deepSeekV4FlashCacheReadPerToken + float64(7)*deepSeekV4FlashOutputPricePerToken
	require.InDelta(t, want, cost.TotalCost, 1e-15)
}
