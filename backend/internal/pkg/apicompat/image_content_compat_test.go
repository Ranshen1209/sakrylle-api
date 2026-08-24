package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChatCompletionsToResponsesPreservesDeepSeekImageForms(t *testing.T) {
	req := &ChatCompletionsRequest{
		Model: "deepseek-v4-flash-vision-exp",
		Messages: []ChatMessage{{
			Role: "user",
			Content: json.RawMessage(`[
				{"type":"image_url","image_url":{"url":"https://example.com/a.jpg","detail":"low"}},
				{"type":"file","file_id":"file-api-a"},
				{"type":"file","file_data":"data:image/png;base64,abc","filename":"b.png"}
			]`),
		}},
	}
	out, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)
	var items []ResponsesInputItem
	require.NoError(t, json.Unmarshal(out.Input, &items))
	require.Len(t, items, 1)
	var parts []ResponsesContentPart
	require.NoError(t, json.Unmarshal(items[0].Content, &parts))
	require.Len(t, parts, 3)
	require.Equal(t, "https://example.com/a.jpg", parts[0].ImageURL)
	require.Equal(t, "low", parts[0].Detail)
	require.Equal(t, "file-api-a", parts[1].FileID)
	require.Equal(t, "data:image/png;base64,abc", parts[2].ImageURL)
}

func TestResponsesToChatCompletionsPreservesFileIDAndDetail(t *testing.T) {
	req := &ResponsesRequest{
		Model: "deepseek-v4-flash-vision-exp",
		Input: json.RawMessage(`[{"role":"user","content":[
			{"type":"input_text","text":"describe"},
			{"type":"input_image","image_url":"https://example.com/a.webp","detail":"high"},
			{"type":"input_image","file_id":"file-api-a"}
		]}]`),
	}
	out, err := ResponsesToChatCompletionsRequest(req)
	require.NoError(t, err)
	var parts []ChatContentPart
	require.NoError(t, json.Unmarshal(out.Messages[0].Content, &parts))
	require.Len(t, parts, 3)
	require.Equal(t, "https://example.com/a.webp", parts[1].ImageURL.URL)
	require.Equal(t, "high", parts[1].ImageURL.Detail)
	require.Equal(t, "file-api-a", parts[2].FileID)
	require.Equal(t, "file", parts[2].Type)
}

func TestAnthropicImageSourcesRoundTripAcrossResponsesAndChat(t *testing.T) {
	req := &AnthropicRequest{
		Model: "deepseek-v4-flash-vision-exp",
		Messages: []AnthropicMessage{{
			Role: "user",
			Content: json.RawMessage(`[
				{"type":"image","source":{"type":"url","url":"https://example.com/a.jpg"}},
				{"type":"image","source":{"type":"file","file_id":"file-api-a"}},
				{"type":"image","source":{"type":"base64","media_type":"image/png","data":"abc"}}
			]`),
		}},
	}
	responsesReq, err := AnthropicToResponses(req)
	require.NoError(t, err)
	var responseItems []ResponsesInputItem
	require.NoError(t, json.Unmarshal(responsesReq.Input, &responseItems))
	var responseParts []ResponsesContentPart
	require.NoError(t, json.Unmarshal(responseItems[0].Content, &responseParts))
	require.Len(t, responseParts, 3)
	require.Equal(t, "https://example.com/a.jpg", responseParts[0].ImageURL)
	require.Equal(t, "file-api-a", responseParts[1].FileID)
	require.Equal(t, "data:image/png;base64,abc", responseParts[2].ImageURL)

	chatReq, err := AnthropicToChatCompletionsRequest(req)
	require.NoError(t, err)
	var chatParts []ChatContentPart
	require.NoError(t, json.Unmarshal(chatReq.Messages[0].Content, &chatParts))
	require.Len(t, chatParts, 3)
	require.Equal(t, "file-api-a", chatParts[1].FileID)
	require.Equal(t, "file", chatParts[1].Type)
	require.Equal(t, "data:image/png;base64,abc", chatParts[2].ImageURL.URL)
}

func TestResponsesToAnthropicPreservesDeepSeekFileSource(t *testing.T) {
	req := &ResponsesRequest{
		Model: "deepseek-v4-flash-vision-exp",
		Input: json.RawMessage(`[{"role":"user","content":[
			{"type":"input_text","text":"describe"},
			{"type":"input_image","file_id":"file-api-a"},
			{"type":"input_image","image_url":"data:image/jpeg;base64,abc"}
		]}]`),
	}
	out, err := ResponsesToAnthropicRequest(req)
	require.NoError(t, err)
	var blocks []AnthropicContentBlock
	require.NoError(t, json.Unmarshal(out.Messages[0].Content, &blocks))
	require.Len(t, blocks, 3)
	require.Equal(t, "file", blocks[1].Source.Type)
	require.Equal(t, "file-api-a", blocks[1].Source.FileID)
	require.Equal(t, "base64", blocks[2].Source.Type)
	require.Equal(t, "abc", blocks[2].Source.Data)
}

func TestResponsesDeveloperImagesRemainVisibleAcrossFixedProtocols(t *testing.T) {
	req := &ResponsesRequest{
		Model: "deepseek-v4-flash-vision-exp",
		Input: json.RawMessage(`[{"role":"developer","content":[
			{"type":"input_text","text":"inspect precisely"},
			{"type":"input_image","file_id":"file-developer"}
		]}]`),
	}

	chat, err := ResponsesToChatCompletionsRequest(req)
	require.NoError(t, err)
	require.Len(t, chat.Messages, 2)
	require.Equal(t, "system", chat.Messages[0].Role)
	require.JSONEq(t, `"inspect precisely"`, string(chat.Messages[0].Content))
	require.Equal(t, "user", chat.Messages[1].Role)
	var chatParts []ChatContentPart
	require.NoError(t, json.Unmarshal(chat.Messages[1].Content, &chatParts))
	require.Len(t, chatParts, 1)
	require.Equal(t, "file-developer", chatParts[0].FileID)

	anthropic, err := ResponsesToAnthropicRequest(req)
	require.NoError(t, err)
	require.JSONEq(t, `"inspect precisely"`, string(anthropic.System))
	require.Len(t, anthropic.Messages, 1)
	var blocks []AnthropicContentBlock
	require.NoError(t, json.Unmarshal(anthropic.Messages[0].Content, &blocks))
	require.Len(t, blocks, 1)
	require.Equal(t, "file", blocks[0].Source.Type)
	require.Equal(t, "file-developer", blocks[0].Source.FileID)
}

func TestResponsesCustomToolOutputImageSurvivesAnthropicBridge(t *testing.T) {
	req := &ResponsesRequest{
		Model: "deepseek-v4-flash-vision-exp",
		Input: json.RawMessage(`[
			{"type":"custom_tool_call","call_id":"call_image","name":"inspect_image","input":"frame 7"},
			{"type":"custom_tool_call_output","call_id":"call_image","output":[
				{"type":"input_text","text":"captured"},
				{"type":"input_image","file_id":"file-tool-image"}
			]}
		]`),
	}

	out, err := ResponsesToAnthropicRequest(req)
	require.NoError(t, err)
	require.Len(t, out.Messages, 2)
	uses := parseContentBlocks(out.Messages[0].Content)
	require.Len(t, uses, 1)
	require.Equal(t, "tool_use", uses[0].Type)
	require.JSONEq(t, `{"input":"frame 7"}`, string(uses[0].Input))

	results := parseContentBlocks(out.Messages[1].Content)
	require.Len(t, results, 1)
	require.Equal(t, "tool_result", results[0].Type)
	var resultBlocks []AnthropicContentBlock
	require.NoError(t, json.Unmarshal(results[0].Content, &resultBlocks))
	require.Len(t, resultBlocks, 2)
	require.Equal(t, "captured", resultBlocks[0].Text)
	require.Equal(t, "file", resultBlocks[1].Source.Type)
	require.Equal(t, "file-tool-image", resultBlocks[1].Source.FileID)
}

func TestAnthropicParallelToolResultImagesKeepResponsesCallAssociation(t *testing.T) {
	req := &AnthropicRequest{
		Model: "deepseek-v4-flash-vision-exp",
		Messages: []AnthropicMessage{
			{Role: "user", Content: json.RawMessage(`"inspect both images"`)},
			{Role: "assistant", Content: json.RawMessage(`[
				{"type":"tool_use","id":"toolu_file","name":"load_file","input":{"name":"a"}},
				{"type":"tool_use","id":"toolu_url","name":"load_url","input":{"name":"b"}}
			]`)},
			{Role: "user", Content: json.RawMessage(`[
				{"type":"tool_result","tool_use_id":"toolu_file","content":[
					{"type":"image","source":{"type":"file","file_id":"file-api-a"}}
				]},
				{"type":"tool_result","tool_use_id":"toolu_url","content":[
					{"type":"text","text":"remote frame"},
					{"type":"image","source":{"type":"url","url":"https://example.com/b.webp"}}
				]}
			]`)},
		},
	}

	out, err := AnthropicToResponses(req)
	require.NoError(t, err)

	var items []ResponsesInputItem
	require.NoError(t, json.Unmarshal(out.Input, &items))
	require.Len(t, items, 5)
	require.Equal(t, "function_call_output", items[3].Type)
	require.Equal(t, "toolu_file", items[3].CallID)
	require.Equal(t, "function_call_output", items[4].Type)
	require.Equal(t, "toolu_url", items[4].CallID)

	var fileParts []ResponsesContentPart
	require.NoError(t, json.Unmarshal(items[3].outputRaw, &fileParts))
	require.Len(t, fileParts, 1)
	require.Equal(t, "file-api-a", fileParts[0].FileID)

	var urlParts []ResponsesContentPart
	require.NoError(t, json.Unmarshal(items[4].outputRaw, &urlParts))
	require.Len(t, urlParts, 2)
	require.Equal(t, "remote frame", urlParts[0].Text)
	require.Equal(t, "https://example.com/b.webp", urlParts[1].ImageURL)

	// Verify the wire shape remains an array instead of a quoted JSON string.
	var wireItems []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(out.Input, &wireItems))
	require.JSONEq(t, `[{"type":"input_image","file_id":"file-api-a"}]`, string(wireItems[3]["output"]))
	require.JSONEq(t, `[
		{"type":"input_text","text":"remote frame"},
		{"type":"input_image","image_url":"https://example.com/b.webp"}
	]`, string(wireItems[4]["output"]))
}

func TestImageContentMarshalShapes(t *testing.T) {
	chat, err := json.Marshal(ChatContentPart{Type: "file", FileID: "file-api-a"})
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"file","file_id":"file-api-a"}`, string(chat))

	responses, err := json.Marshal(ResponsesContentPart{Type: "input_image", FileID: "file-api-a", FileData: "data:image/png;base64,abc"})
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"input_image","file_id":"file-api-a"}`, string(responses))
}

func TestResponsesUsageParsesDeepSeekPromptCacheSplit(t *testing.T) {
	var usage ResponsesUsage
	require.NoError(t, json.Unmarshal([]byte(`{"prompt_cache_hit_tokens":7,"prompt_cache_miss_tokens":5,"output_tokens":2}`), &usage))
	require.Equal(t, 12, usage.InputTokens)
	require.Equal(t, 7, usage.InputTokensDetails.CachedTokens)
}

func TestChatCompletionsToResponsesPreservesSystemAndDropsAssistantImages(t *testing.T) {
	req := &ChatCompletionsRequest{
		Model: "deepseek-v4-flash-vision-exp",
		Messages: []ChatMessage{
			{
				Role: "system",
				Content: json.RawMessage(`[
					{"type":"text","text":"system policy"},
					{"type":"image_url","image_url":{"url":"https://example.com/system.png"}},
					{"type":"file","file_id":"file-system"}
				]`),
			},
			{
				Role: "assistant",
				Content: json.RawMessage(`[
					{"type":"text","text":"assistant answer"},
					{"type":"input_image","file_id":"file-assistant"}
				]`),
			},
		},
	}

	out, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)
	var items []ResponsesInputItem
	require.NoError(t, json.Unmarshal(out.Input, &items))
	require.Len(t, items, 2)

	var systemParts []ResponsesContentPart
	require.NoError(t, json.Unmarshal(items[0].Content, &systemParts))
	require.Len(t, systemParts, 3)
	require.Equal(t, "input_text", systemParts[0].Type)
	require.Equal(t, "system policy", systemParts[0].Text)
	require.Equal(t, "input_image", systemParts[1].Type)
	require.Equal(t, "https://example.com/system.png", systemParts[1].ImageURL)
	require.Equal(t, "file-system", systemParts[2].FileID)

	var assistantParts []ResponsesContentPart
	require.NoError(t, json.Unmarshal(items[1].Content, &assistantParts))
	require.Len(t, assistantParts, 1)
	require.Equal(t, "output_text", assistantParts[0].Type)
	require.Equal(t, "assistant answer", assistantParts[0].Text)
}

func TestChatCompletionsToResponsesPreservesDeveloperImages(t *testing.T) {
	req := &ChatCompletionsRequest{
		Model: "deepseek-v4-flash-vision-exp",
		Messages: []ChatMessage{{
			Role:    "developer",
			Content: json.RawMessage(`[{"type":"text","text":"review"},{"type":"input_image","file_id":"developer-file"}]`),
		}},
	}

	out, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)
	var items []ResponsesInputItem
	require.NoError(t, json.Unmarshal(out.Input, &items))
	require.Len(t, items, 1)
	require.Equal(t, "developer", items[0].Role)
	var parts []ResponsesContentPart
	require.NoError(t, json.Unmarshal(items[0].Content, &parts))
	require.Len(t, parts, 2)
	require.Equal(t, "developer-file", parts[1].FileID)
}

func TestAnthropicFileDataNormalizesRawBase64AcrossOpenAIShapes(t *testing.T) {
	src := &AnthropicImageSource{Type: "base64", MediaType: "image/jpeg", FileData: "abc"}
	responses, ok := responsesContentPartFromAnthropicImageSource(src)
	require.True(t, ok)
	require.Equal(t, "data:image/jpeg;base64,abc", responses.ImageURL)

	chat, ok := chatContentPartFromAnthropicImageSource(src)
	require.True(t, ok)
	require.Equal(t, "data:image/jpeg;base64,abc", chat.FileData)
}

func TestResponsesToChatCompletionsDropsImagesFromNonUserRoles(t *testing.T) {
	req := &ResponsesRequest{
		Model: "deepseek-v4-flash-vision-exp",
		Input: json.RawMessage(`[
			{"role":"system","content":[{"type":"input_text","text":"system policy"},{"type":"input_image","file_id":"file-system"}]},
			{"role":"assistant","content":[{"type":"output_text","text":"assistant answer"},{"type":"input_image","file_id":"file-assistant"}]},
			{"role":"user","content":[{"type":"input_text","text":"inspect"},{"type":"input_image","file_id":"file-user"}]}
		]`),
	}

	out, err := ResponsesToChatCompletionsRequest(req)
	require.NoError(t, err)
	require.Len(t, out.Messages, 3)
	require.JSONEq(t, `"system policy"`, string(out.Messages[0].Content))
	require.JSONEq(t, `"assistant answer"`, string(out.Messages[1].Content))
	var userParts []ChatContentPart
	require.NoError(t, json.Unmarshal(out.Messages[2].Content, &userParts))
	require.Len(t, userParts, 2)
	require.Equal(t, "file", userParts[1].Type)
	require.Equal(t, "file-user", userParts[1].FileID)
}
