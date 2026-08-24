package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestDeepSeekChatRejectsNonUserImageWithoutScheduling(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{
		"model":"deepseek-v4-flash-vision-exp",
		"messages":[{"role":"assistant","content":[{"type":"image_url","image_url":{"url":"https://example.com/image.png"}}]}]
	}`))
	setDeepSeekImageRoleTestAuth(c)

	newOpenAIImageChatRejectionHandler(t).ChatCompletions(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "invalid_request_error", gjson.Get(recorder.Body.String(), "error.type").String())
	require.Contains(t, gjson.Get(recorder.Body.String(), "error.message").String(), "only supported in user messages")
	_, selected := c.Get(opsAccountIDKey)
	require.False(t, selected)
}

func TestDeepSeekResponsesRejectsUnsupportedImageRole(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(`{
		"model":"deepseek-v4-flash-vision-exp",
		"input":[{"role":"system","content":[{"type":"input_image","file_id":"file-api-system"}]}]
	}`))
	setDeepSeekImageRoleTestAuth(c)

	newOpenAIHandlerForPreviousResponseIDValidation(t, nil).Responses(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "invalid_request_error", gjson.Get(recorder.Body.String(), "error.type").String())
	require.Contains(t, gjson.Get(recorder.Body.String(), "error.message").String(), "structured tool outputs")
	_, selected := c.Get(opsAccountIDKey)
	require.False(t, selected)
}

func TestDeepSeekMessagesRejectsUnsupportedImageRoleWithAnthropicError(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(`{
		"model":"deepseek-v4-flash-vision-exp",
		"max_tokens":64,
		"messages":[{"role":"assistant","content":[{"type":"image","source":{"type":"file","file_id":"file-api-assistant"}}]}]
	}`))
	setDeepSeekImageRoleTestAuth(c)

	newOpenAIHandlerForPreviousResponseIDValidation(t, nil).Messages(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "error", gjson.Get(recorder.Body.String(), "type").String())
	require.Equal(t, "invalid_request_error", gjson.Get(recorder.Body.String(), "error.type").String())
	require.Contains(t, gjson.Get(recorder.Body.String(), "error.message").String(), "only supported in user messages")
	_, selected := c.Get(opsAccountIDKey)
	require.False(t, selected)
}

func setDeepSeekImageRoleTestAuth(c *gin.Context) {
	groupID := int64(4348)
	user := &service.User{ID: 4348}
	apiKey := &service.APIKey{
		ID:      4348,
		UserID:  user.ID,
		User:    user,
		GroupID: &groupID,
		Group: &service.Group{
			ID:       groupID,
			Platform: service.PlatformDeepseek,
		},
	}
	c.Set(string(middleware.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: user.ID, Concurrency: 1})
}
