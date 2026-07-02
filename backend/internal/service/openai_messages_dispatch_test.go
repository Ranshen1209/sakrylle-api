package service

import "testing"

import "github.com/stretchr/testify/require"

func TestNormalizeOpenAIMessagesDispatchModelConfig(t *testing.T) {
	t.Parallel()

	cfg := normalizeOpenAIMessagesDispatchModelConfig(OpenAIMessagesDispatchModelConfig{
		OpusMappedModel:   " gpt-5.4-high ",
		SonnetMappedModel: "gpt-5.4",
		HaikuMappedModel:  " gpt-5.4-mini-medium ",
		ExactModelMappings: map[string]string{
			" claude-sonnet-4-5-20250929 ": " gpt-5.2-high ",
			"":                             "gpt-5.4",
			"claude-opus-4-6":              " ",
		},
	})

	require.Equal(t, "gpt-5.4", cfg.OpusMappedModel)
	require.Equal(t, "gpt-5.4", cfg.SonnetMappedModel)
	require.Equal(t, "gpt-5.4-mini", cfg.HaikuMappedModel)
	require.Equal(t, map[string]string{
		"claude-sonnet-4-5-20250929": "gpt-5.2",
	}, cfg.ExactModelMappings)
}

func TestResolveMessagesDispatchModelDefaults(t *testing.T) {
	t.Parallel()

	group := &Group{}

	require.Equal(t, "gpt-5.5", group.ResolveMessagesDispatchModel("claude-opus-4-6"))
	require.Equal(t, "gpt-5.4", group.ResolveMessagesDispatchModel("claude-sonnet-4-5-20250929"))
	require.Equal(t, "gpt-5.4-mini", group.ResolveMessagesDispatchModel("claude-haiku-4-5-20251001"))
}

func TestResolveMessagesDispatchModelExactMappingsOverrideClaudeDefaults(t *testing.T) {
	t.Parallel()

	group := &Group{
		MessagesDispatchModelConfig: OpenAIMessagesDispatchModelConfig{
			ExactModelMappings: map[string]string{
				"claude-sonnet-4-5-20250929": "gpt-5.5",
			},
		},
	}

	require.Equal(t, "gpt-5.5", group.ResolveMessagesDispatchModel("claude-sonnet-4-5-20250929"))
}

func TestResolveMessagesDispatchModelLeavesGPTModelsUnmapped(t *testing.T) {
	t.Parallel()

	group := &Group{
		DefaultMappedModel: "gpt-5.5",
		MessagesDispatchModelConfig: OpenAIMessagesDispatchModelConfig{
			ExactModelMappings: map[string]string{
				"gpt-5.4": "gpt-5.5",
			},
		},
	}

	require.Empty(t, group.ResolveMessagesDispatchModel("gpt-5.4"))
	require.Empty(t, group.ResolveMessagesDispatchModel("gpt-5.5"))
	require.Empty(t, group.ResolveMessagesDispatchModel("codex-auto-review"))
}

func TestGroupResolveMessagesDispatchModel_GrokMapsClaudeFamilyToGrok(t *testing.T) {
	t.Parallel()

	group := &Group{Platform: PlatformGrok}

	require.Equal(t, "grok-4.3", group.ResolveMessagesDispatchModel("claude-sonnet-4-5"))
	require.Equal(t, "grok-4.3", group.ResolveMessagesDispatchModel("claude-opus-4-6"))
	require.Equal(t, "grok-4.3", group.ResolveMessagesDispatchModel("claude-haiku-4-5"))
	require.Empty(t, group.ResolveMessagesDispatchModel("grok"))
	require.Empty(t, group.ResolveMessagesDispatchModel("gpt-5.4"))
}
