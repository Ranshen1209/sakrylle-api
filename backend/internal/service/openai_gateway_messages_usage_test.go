//go:build unit

package service

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
)

func TestCopyOpenAIUsageFromResponsesUsageTrustsCanonicalCacheCreationValue(t *testing.T) {
	usage := &apicompat.ResponsesUsage{
		InputTokens:              20,
		OutputTokens:             2,
		CacheCreationInputTokens: 0,
		InputTokensDetails: &apicompat.ResponsesInputTokensDetails{
			CachedTokens:     3,
			CacheWriteTokens: 19,
		},
	}

	got := copyOpenAIUsageFromResponsesUsage(usage)

	require.Equal(t, 20, got.InputTokens)
	require.Equal(t, 3, got.CacheReadInputTokens)
	require.Zero(t, got.CacheCreationInputTokens)
}

func TestCopyOpenAIUsageFromResponsesUsageTopLevelPromptCacheHitPrecedence(t *testing.T) {
	var usage apicompat.ResponsesUsage
	require.NoError(t, json.Unmarshal([]byte(`{
		"input_tokens":100,
		"prompt_cache_hit_tokens":0,
		"prompt_cache_miss_tokens":100,
		"input_tokens_details":{"cached_tokens":99}
	}`), &usage))

	got := copyOpenAIUsageFromResponsesUsage(&usage)
	require.Zero(t, got.CacheReadInputTokens, "explicit DeepSeek hit=0 must override nested cached_tokens")
}

func TestClaudeUsageToOpenAIUsageAddsNativeAnthropicCacheBuckets(t *testing.T) {
	got := claudeUsageToOpenAIUsage(&ClaudeUsage{
		InputTokens:              60,
		OutputTokens:             7,
		CacheReadInputTokens:     40,
		CacheCreationInputTokens: 10,
	})

	require.Equal(t, 110, got.InputTokens)
	require.Equal(t, 60, got.InputTokens-got.CacheReadInputTokens-got.CacheCreationInputTokens)
	require.Equal(t, 7, got.OutputTokens)
}

func TestClaudeUsageToOpenAIUsageKeepsDeepSeekInclusivePromptTotal(t *testing.T) {
	got := claudeUsageToOpenAIUsage(&ClaudeUsage{
		InputTokens:              100,
		CacheReadInputTokens:     40,
		CacheCreationInputTokens: 10,
		inputTokensIncludeCache:  true,
	})

	require.Equal(t, 100, got.InputTokens)
	require.Equal(t, 50, got.InputTokens-got.CacheReadInputTokens-got.CacheCreationInputTokens)
}
