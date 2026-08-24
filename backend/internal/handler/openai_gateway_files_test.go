package handler

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

func TestParseDeepSeekUploadedFileMetadata(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("purpose", "user_data"))
	filePart, err := writer.CreateFormFile("file", "sample.png")
	require.NoError(t, err)
	_, err = filePart.Write([]byte("png"))
	require.NoError(t, err)
	// SDKs may serialize expiration fields after the file part.
	require.NoError(t, writer.WriteField("expires_after[anchor]", "created_at"))
	require.NoError(t, writer.WriteField("expires_after[seconds]", "3600"))
	require.NoError(t, writer.Close())

	metadata, err := parseDeepSeekUploadedFileMetadata(writer.FormDataContentType(), body.Bytes())
	require.NoError(t, err)
	require.Equal(t, "image/png", metadata.mimeType)
	require.Equal(t, int64(3600), metadata.expiresAfterSeconds)
	require.Equal(t, int64(3), metadata.sizeBytes)
	require.True(t, metadata.hasExpiresAfter)
}

func TestParseDeepSeekUploadedFileMetadataRequiresCompleteValidExpiry(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	filePart, err := writer.CreateFormFile("file", "sample.txt")
	require.NoError(t, err)
	_, err = filePart.Write([]byte("x"))
	require.NoError(t, err)
	require.NoError(t, writer.WriteField("expires_after[anchor]", "created_at"))
	require.NoError(t, writer.WriteField("expires_after[seconds]", "3599"))
	require.NoError(t, writer.Close())

	_, err = parseDeepSeekUploadedFileMetadata(writer.FormDataContentType(), body.Bytes())
	require.ErrorContains(t, err, "expires_after seconds")
}

func TestParseDeepSeekUploadedFileMetadataRejectsPartialExpiry(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	filePart, err := writer.CreateFormFile("file", "sample.txt")
	require.NoError(t, err)
	_, err = filePart.Write([]byte("x"))
	require.NoError(t, err)
	require.NoError(t, writer.WriteField("expires_after[anchor]", "created_at"))
	require.NoError(t, writer.Close())

	_, err = parseDeepSeekUploadedFileMetadata(writer.FormDataContentType(), body.Bytes())
	require.ErrorContains(t, err, "requires one created_at anchor")
}

func TestParseDeepSeekUploadedFileMetadataEnforcesFilePartLimitExactly(t *testing.T) {
	buildUpload := func(size int) (string, []byte) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", "sample.bin")
		require.NoError(t, err)
		_, err = part.Write(bytes.Repeat([]byte("x"), size))
		require.NoError(t, err)
		require.NoError(t, writer.Close())
		return writer.FormDataContentType(), body.Bytes()
	}

	contentType, exact := buildUpload(3)
	metadata, err := parseDeepSeekUploadedFileMetadataWithLimit(contentType, exact, 3)
	require.NoError(t, err)
	require.Equal(t, int64(3), metadata.sizeBytes)

	contentType, oversized := buildUpload(4)
	_, err = parseDeepSeekUploadedFileMetadataWithLimit(contentType, oversized, 3)
	require.ErrorIs(t, err, errDeepSeekFilesFileTooLarge)
}

func TestDeepSeekFilesMayFailoverAcrossAccountsOnlyForSafeKeyWideRead(t *testing.T) {
	require.True(t, deepSeekFilesMayFailoverAcrossAccounts(http.MethodGet, ""))
	require.False(t, deepSeekFilesMayFailoverAcrossAccounts(http.MethodPost, ""))
	require.False(t, deepSeekFilesMayFailoverAcrossAccounts(http.MethodGet, "file-owned"))
	require.False(t, deepSeekFilesMayFailoverAcrossAccounts(http.MethodDelete, "file-owned"))
}

func TestPreserveDeepSeekFileRefreshExpiryWhenAnthropicOmitsIt(t *testing.T) {
	expiresAt := time.Unix(1_800_000_000, 0).UTC()
	existing := &service.DeepSeekFileRecord{ID: "file-a", ExpiresAt: &expiresAt}
	refreshed := &service.DeepSeekFileRecord{ID: "file-a"}

	preserveDeepSeekFileRefreshExpiry(refreshed, existing)
	require.NotNil(t, refreshed.ExpiresAt)
	require.Equal(t, expiresAt, *refreshed.ExpiresAt)

	upstreamExpiry := expiresAt.Add(time.Hour)
	refreshed.ExpiresAt = &upstreamExpiry
	preserveDeepSeekFileRefreshExpiry(refreshed, existing)
	require.Equal(t, upstreamExpiry, *refreshed.ExpiresAt)
}

func TestWriteDeepSeekFilesResponseFiltersCookieAndCORSHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":                []string{"application/json"},
			"X-Request-ID":                []string{"req-files-2"},
			"Set-Cookie":                  []string{"session=upstream"},
			"Access-Control-Allow-Origin": []string{"https://attacker.example"},
		},
	}

	writeDeepSeekFilesResponse(c, &service.OpenAIGatewayService{}, resp, []byte(`{"ok":true}`))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	require.Equal(t, "req-files-2", recorder.Header().Get("X-Request-ID"))
	require.Empty(t, recorder.Header().Get("Set-Cookie"))
	require.Empty(t, recorder.Header().Get("Access-Control-Allow-Origin"))
}

func TestNormalizeDeepSeekFilesRedirectResponsePreventsClientReplay(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTemporaryRedirect,
		Header: http.Header{
			"Location": []string{"https://redirect.example/files"},
		},
	}
	normalizeDeepSeekFilesRedirectResponse(resp)
	require.Equal(t, http.StatusBadGateway, resp.StatusCode)
	require.Empty(t, resp.Header.Get("Location"))
}

func TestDeepSeekFilesHelperFailuresUseNativeAnthropicErrorShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		run  func(*OpenAIGatewayHandler, *gin.Context)
	}{
		{
			name: "concurrency failure",
			run: func(h *OpenAIGatewayHandler, c *gin.Context) {
				h.handleConcurrencyError(c, errors.New("redis unavailable"), "user", false)
			},
		},
		{
			name: "profit veto exhaustion",
			run: func(h *OpenAIGatewayHandler, c *gin.Context) {
				h.handleOpenAIProfitVetoExhausted(c, false, zap.NewNop(), 3)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/files", nil)
			setDeepSeekFilesNativeAnthropicErrors(c, true)
			test.run(&OpenAIGatewayHandler{}, c)

			require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
			require.Equal(t, "error", gjson.Get(recorder.Body.String(), "type").String())
			require.Equal(t, "api_error", gjson.Get(recorder.Body.String(), "error.type").String())
			require.NotEmpty(t, gjson.Get(recorder.Body.String(), "error.message").String())
		})
	}
}
