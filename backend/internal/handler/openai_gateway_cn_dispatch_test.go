package handler

// CN 分组 /v1/messages 调度闸门回归（修复:正常途径创建的 CN 分组曾恒 403）。
// Sakrylle 的兼容协议策略也保留 OpenAI/Grok 分组的历史豁免行为。

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAllowsOpenAICompatibleMessagesDispatch_CNProvidersExempt(t *testing.T) {
	for _, platform := range []string{
		service.PlatformOpenAI,
		service.PlatformGrok,
		service.PlatformKimi,
		service.PlatformZhipu,
		service.PlatformDeepseek,
	} {
		group := &service.Group{Platform: platform, AllowMessagesDispatch: false}
		require.True(t, allowsOpenAICompatibleMessagesDispatch(group),
			"%s 分组必须保留兼容协议豁免", platform)
	}

	require.False(t, allowsOpenAICompatibleMessagesDispatch(nil))
	require.False(t, allowsOpenAICompatibleMessagesDispatch(&service.Group{
		Platform: service.PlatformAnthropic, AllowMessagesDispatch: false,
	}))
	require.True(t, allowsOpenAICompatibleMessagesDispatch(&service.Group{
		Platform: service.PlatformAnthropic, AllowMessagesDispatch: true,
	}))
}
