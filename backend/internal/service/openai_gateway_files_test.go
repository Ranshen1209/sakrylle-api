package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestForwardDeepSeekFiles_PreservesMultipartRequestAndResponseMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body, contentType := buildDeepSeekFilesTestUpload(t, []byte("image-bytes"), map[string]string{
		"purpose": "vision",
	})
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusCreated,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"X-Request-ID": []string{"file-req-1"},
		},
		Body: io.NopCloser(bytes.NewReader([]byte(`{"id":"file-1"}`))),
	}}
	svc := &OpenAIGatewayService{
		cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{
			Enabled: false, AllowInsecureHTTP: true,
		}}},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:          7,
		Platform:    PlatformDeepseek,
		Type:        AccountTypeAPIKey,
		Concurrency: 2,
		Credentials: map[string]any{
			"api_key":  "sk-deepseek",
			"base_url": "https://api.deepseek.com",
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/files?purpose=vision", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", contentType)
	c.Request.Header.Set("X-Upload-Client", "test")
	c.Request.Header.Set("X-Stainless-Lang", "go")
	c.Request.Header.Set("Authorization", "Bearer gateway-key")

	resp, err := svc.ForwardDeepSeekFiles(context.Background(), c, account, http.MethodPost, "", body)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.Equal(t, "https://api.deepseek.com/files", upstream.lastReq.URL.String())
	require.Equal(t, http.MethodPost, upstream.lastReq.Method)
	fields, fileBytes, fileContentType := readDeepSeekFilesTestUpload(t, upstream.lastReq.Header.Get("Content-Type"), upstream.lastBody)
	require.Equal(t, []string{"user_data"}, fields["purpose"])
	require.Equal(t, []byte("image-bytes"), fileBytes)
	require.Equal(t, "image/png", fileContentType)
	require.Empty(t, upstream.lastReq.Header.Get("X-Upload-Client"))
	require.Equal(t, "go", upstream.lastReq.Header.Get("X-Stainless-Lang"))
	require.Equal(t, "Bearer sk-deepseek", upstream.lastReq.Header.Get("Authorization"))
	require.NotEqual(t, "Bearer gateway-key", upstream.lastReq.Header.Get("Authorization"))
	require.True(t, HTTPUpstreamRedirectsDisabled(upstream.lastReq.Context()))
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	_ = resp.Body.Close()
}

func TestDeepSeekFilesUseNativeAnthropicIngressRequiresExplicitProtocolHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	testCases := []struct {
		name    string
		headers http.Header
		want    bool
	}{
		{name: "x-api-key only is OpenAI", headers: http.Header{"X-Api-Key": []string{"gateway-key"}}},
		{name: "authorization only is OpenAI", headers: http.Header{"Authorization": []string{"Bearer gateway-key"}}},
		{name: "blank Anthropic headers are OpenAI", headers: http.Header{"Anthropic-Version": []string{"  "}, "X-Api-Key": []string{"gateway-key"}}},
		{name: "Anthropic version", headers: http.Header{"Anthropic-Version": []string{"2023-06-01"}}, want: true},
		{name: "Anthropic beta", headers: http.Header{"Anthropic-Beta": []string{deepSeekFilesAPIBetaToken}}, want: true},
		{
			name: "connection scoped Anthropic version is ignored",
			headers: http.Header{
				"Anthropic-Version": []string{"2023-06-01"},
				"Connection":        []string{"keep-alive, ANTHROPIC-VERSION"},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/files", nil)
			c.Request.Header = testCase.headers.Clone()
			require.Equal(t, testCase.want, DeepSeekFilesUseNativeAnthropicIngress(c))
		})
	}
}

func TestCopyDeepSeekFilesHeadersUsesAllowlistAndConnectionTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/files", nil)
	c.Request.Header = http.Header{
		"Accept":            []string{"application/json"},
		"Content-Type":      []string{"multipart/form-data; boundary=test-boundary"},
		"Anthropic-Version": []string{"2023-06-01"},
		"Anthropic-Beta":    []string{deepSeekFilesAPIBetaToken},
		"X-Stainless-Lang":  []string{"go"},
		"User-Agent":        []string{"deepseek-sdk/1.0"},
		"X-Upload-Client":   []string{"untrusted"},
		"X-Forwarded-For":   []string{"203.0.113.7"},
		"Authorization":     []string{"Bearer gateway-key"},
		"X-Api-Key":         []string{"gateway-key"},
		"Cookie":            []string{"session=secret"},
		"connection":        []string{"X-Stainless-Lang", "user-agent, X-Upload-Client"},
	}

	dst := make(http.Header)
	copyDeepSeekFilesHeaders(dst, c)

	require.Equal(t, "application/json", dst.Get("Accept"))
	require.Equal(t, "multipart/form-data; boundary=test-boundary", dst.Get("Content-Type"))
	require.Equal(t, "2023-06-01", dst.Get("Anthropic-Version"))
	require.Equal(t, deepSeekFilesAPIBetaToken, dst.Get("Anthropic-Beta"))
	require.Empty(t, dst.Get("X-Stainless-Lang"))
	require.Empty(t, dst.Get("User-Agent"))
	require.Empty(t, dst.Get("X-Upload-Client"))
	require.Empty(t, dst.Get("X-Forwarded-For"))
	require.Empty(t, dst.Get("Authorization"))
	require.Empty(t, dst.Get("X-Api-Key"))
	require.Empty(t, dst.Get("Cookie"))
	require.Empty(t, dst.Get("Connection"))
}

func TestForwardDeepSeekFiles_AnthropicIngressToFixedOpenAIAddsCanonicalPurpose(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body, contentType := buildDeepSeekFilesTestUpload(t, []byte("anthropic-image"), nil)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusCreated,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"file-openai"}`)),
	}}
	svc := newDeepSeekFilesForwardTestService(upstream)
	account := &Account{
		ID: 15, Platform: PlatformDeepseek, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-fixed-openai", "base_url": "https://openai-files.example/v1"},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/files", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", contentType)
	c.Request.Header.Set("x-api-key", "gateway-key")
	c.Request.Header.Set("anthropic-version", "2023-06-01")

	resp, err := svc.ForwardDeepSeekFiles(context.Background(), c, account, http.MethodPost, "", body)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, "https://openai-files.example/v1/files", upstream.lastReq.URL.String())
	require.Empty(t, upstream.lastReq.Header.Get("anthropic-version"))
	require.Empty(t, upstream.lastReq.Header.Get("anthropic-beta"))
	fields, fileBytes, fileContentType := readDeepSeekFilesTestUpload(t, upstream.lastReq.Header.Get("Content-Type"), upstream.lastBody)
	require.Equal(t, []string{"user_data"}, fields["purpose"])
	require.Equal(t, []byte("anthropic-image"), fileBytes)
	require.Equal(t, "image/png", fileContentType)
}

func TestForwardDeepSeekFiles_OpenAIIngressToFixedAnthropicStripsOpenAIOnlyFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body, contentType := buildDeepSeekFilesTestUpload(t, []byte("openai-image"), map[string]string{
		"purpose":     "user_data",
		"unsupported": "drop-me",
	})
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusCreated,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"file-anthropic"}`)),
	}}
	svc := newDeepSeekFilesForwardTestService(upstream)
	account := &Account{
		ID: 16, Platform: PlatformDeepseek, Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "sk-fixed-anthropic", "api_protocol": APIProtocolAnthropic,
			"base_url": "https://anthropic-files.example",
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/files", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", contentType)
	c.Request.Header.Set("Authorization", "Bearer gateway-key")

	resp, err := svc.ForwardDeepSeekFiles(context.Background(), c, account, http.MethodPost, "", body)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, "https://anthropic-files.example/v1/files", upstream.lastReq.URL.String())
	require.Contains(t, upstream.lastReq.Header.Get("anthropic-beta"), deepSeekFilesAPIBetaToken)
	fields, fileBytes, fileContentType := readDeepSeekFilesTestUpload(t, upstream.lastReq.Header.Get("Content-Type"), upstream.lastBody)
	require.Empty(t, fields)
	require.Equal(t, []byte("openai-image"), fileBytes)
	require.Equal(t, "image/png", fileContentType)
}

func TestForwardDeepSeekFiles_FixedAnthropicRejectsOpenAIExpiryBeforeUpload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body, contentType := buildDeepSeekFilesTestUpload(t, []byte("expiring-image"), map[string]string{
		"purpose":                "user_data",
		"expires_after[anchor]":  "created_at",
		"expires_after[seconds]": "3600",
	})
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusCreated,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"must-not-exist"}`)),
	}}
	svc := newDeepSeekFilesForwardTestService(upstream)
	account := &Account{
		ID: 18, Platform: PlatformDeepseek, Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "sk-fixed-anthropic", "api_protocol": APIProtocolAnthropic,
			"base_url": "https://anthropic-files.example",
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/files", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", contentType)

	resp, err := svc.ForwardDeepSeekFiles(context.Background(), c, account, http.MethodPost, "", body)
	require.Nil(t, resp)
	require.ErrorIs(t, err, ErrDeepSeekFilesUpstreamProtocolUnsupported)
	require.NotErrorIs(t, err, ErrDeepSeekFilesInvalidUpload)
	require.Contains(t, err.Error(), "expires_after")
	require.Empty(t, upstream.requests)
}

func TestForwardDeepSeekFiles_UsesProviderFilesPathForVersionedBase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"data":[]}`))),
	}}
	svc := &OpenAIGatewayService{
		cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{
			Enabled: false, AllowInsecureHTTP: true,
		}}},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:       8,
		Platform: PlatformDeepseek,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "sk-deepseek",
			"base_url": "https://proxy.example/v1?api-version=2026-08-17",
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/files/file-a?limit=client-injection", nil)

	resp, err := svc.ForwardDeepSeekFiles(context.Background(), c, account, http.MethodGet, "file-a", nil)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "https://proxy.example/v1/files/file-a?api-version=2026-08-17", upstream.lastReq.URL.String())
	require.True(t, HTTPUpstreamRedirectsDisabled(upstream.lastReq.Context()))
	_ = resp.Body.Close()
}

func TestDeepSeekFileSessionHashNormalizesMultipleIDs(t *testing.T) {
	svc := &OpenAIGatewayService{}
	first := svc.DeepSeekFileSessionHash(7, "file-b", "file-a", "file-b")
	second := svc.DeepSeekFileSessionHash(7, "file-a", "file-b")
	require.NotEmpty(t, first)
	require.Equal(t, first, second)
	require.NotEqual(t, first, svc.DeepSeekFileSessionHash(7, "file-a"))
	require.NotEqual(t, first, svc.DeepSeekFileSessionHash(8, "file-a", "file-b"))
}

func TestForwardDeepSeekFiles_NativeAnthropicUsesFilesEndpointAndBeta(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"data":[]}`))),
	}}
	svc := &OpenAIGatewayService{
		cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{
			Enabled: false, AllowInsecureHTTP: true,
		}}},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:       9,
		Platform: PlatformDeepseek,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":      "sk-deepseek-anthropic",
			"base_url":     "https://api.deepseek.com/anthropic?relay=tenant-a",
			"api_protocol": APIProtocolAnthropic,
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/files?limit=20", nil)
	c.Request.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")
	c.Request.Header.Set("Authorization", "Bearer gateway-key")

	resp, err := svc.ForwardDeepSeekFiles(context.Background(), c, account, http.MethodGet, "", nil)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "https://api.deepseek.com/anthropic/v1/files?relay=tenant-a", upstream.lastReq.URL.String())
	require.Equal(t, "sk-deepseek-anthropic", upstream.lastReq.Header.Get("x-api-key"))
	require.Empty(t, upstream.lastReq.Header.Get("Authorization"))
	require.ElementsMatch(t, []string{"prompt-caching-2024-07-31", deepSeekFilesAPIBetaToken},
		strings.Split(upstream.lastReq.Header.Get("anthropic-beta"), ","))
	require.True(t, HTTPUpstreamRedirectsDisabled(upstream.lastReq.Context()))
	_ = resp.Body.Close()
}

func TestForwardDeepSeekFiles_FixedAnthropicAccountKeepsConfiguredProtocolForOpenAIIngress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"data":[]}`))),
	}}
	svc := &OpenAIGatewayService{
		cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{
			Enabled: false, AllowInsecureHTTP: true,
		}}},
		httpUpstream: upstream,
	}
	account := &Account{
		ID: 13, Platform: PlatformDeepseek, Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "sk-fixed-anthropic", "api_protocol": APIProtocolAnthropic,
			"base_url": "https://relay.example/anthropic",
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/files/file-a", nil)
	c.Request.Header.Set("Authorization", "Bearer gateway-key")

	resp, err := svc.ForwardDeepSeekFiles(context.Background(), c, account, http.MethodGet, "file-a", nil)
	require.NoError(t, err)
	require.Equal(t, "https://relay.example/anthropic/v1/files/file-a", upstream.lastReq.URL.String())
	require.Equal(t, "sk-fixed-anthropic", upstream.lastReq.Header.Get("x-api-key"))
	require.Empty(t, upstream.lastReq.Header.Get("Authorization"))
	require.Contains(t, upstream.lastReq.Header.Get("anthropic-beta"), deepSeekFilesAPIBetaToken)
	_ = resp.Body.Close()
}

func TestForwardDeepSeekFiles_FixedOpenAIAccountKeepsConfiguredProtocolForAnthropicIngress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"data":[]}`))),
	}}
	svc := &OpenAIGatewayService{
		cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{
			Enabled: false, AllowInsecureHTTP: true,
		}}},
		httpUpstream: upstream,
	}
	account := &Account{
		ID: 14, Platform: PlatformDeepseek, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-fixed-openai", "base_url": "https://relay.example/v1"},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/files/file-a", nil)
	c.Request.Header.Set("x-api-key", "gateway-key")
	c.Request.Header.Set("anthropic-version", "2023-06-01")

	resp, err := svc.ForwardDeepSeekFiles(context.Background(), c, account, http.MethodGet, "file-a", nil)
	require.NoError(t, err)
	require.Equal(t, "https://relay.example/v1/files/file-a", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer sk-fixed-openai", upstream.lastReq.Header.Get("Authorization"))
	require.Empty(t, upstream.lastReq.Header.Get("x-api-key"))
	_ = resp.Body.Close()
}

func TestForwardDeepSeekFiles_AdaptiveUsesOpenAIPathForOpenAIIngress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"data":[]}`))),
	}}
	svc := &OpenAIGatewayService{
		cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{
			Enabled: false, AllowInsecureHTTP: true,
		}}},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:       11,
		Platform: PlatformDeepseek,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":      "sk-adaptive",
			"api_protocol": APIProtocolAdaptive,
			"api_base_urls": map[string]any{
				APIProtocolChatCompletions: "https://chat.example",
				APIProtocolAnthropic:       "https://anthropic.example",
				APIProtocolResponses:       "https://responses.example",
			},
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/files", nil)
	c.Request.Header.Set("x-api-key", "gateway-key")

	resp, err := svc.ForwardDeepSeekFiles(context.Background(), c, account, http.MethodGet, "", nil)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "https://chat.example/files", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer sk-adaptive", upstream.lastReq.Header.Get("Authorization"))
	require.Empty(t, upstream.lastReq.Header.Get("x-api-key"))
	_ = resp.Body.Close()
}

func TestForwardDeepSeekFiles_AdaptiveUsesAnthropicPathForAnthropicIngress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"data":[]}`))),
	}}
	svc := &OpenAIGatewayService{
		cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{
			Enabled: false, AllowInsecureHTTP: true,
		}}},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:       12,
		Platform: PlatformDeepseek,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":      "sk-adaptive-anthropic",
			"api_protocol": APIProtocolAdaptive,
			"api_base_urls": map[string]any{
				APIProtocolChatCompletions: "https://chat.example",
				APIProtocolAnthropic:       "https://anthropic.example",
				APIProtocolResponses:       "https://responses.example",
			},
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/files", nil)
	c.Request.Header.Set("x-api-key", "gateway-key")
	c.Request.Header.Set("anthropic-version", "2023-06-01")

	resp, err := svc.ForwardDeepSeekFiles(context.Background(), c, account, http.MethodGet, "", nil)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "https://anthropic.example/v1/files", upstream.lastReq.URL.String())
	require.Equal(t, "sk-adaptive-anthropic", upstream.lastReq.Header.Get("x-api-key"))
	require.Empty(t, upstream.lastReq.Header.Get("Authorization"))
	require.Contains(t, upstream.lastReq.Header.Get("anthropic-beta"), deepSeekFilesAPIBetaToken)
	_ = resp.Body.Close()
}

func TestForwardDeepSeekFiles_RejectsUnsafeFileID(t *testing.T) {
	svc := &OpenAIGatewayService{httpUpstream: &httpUpstreamRecorder{}}
	account := &Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "sk"}}
	_, err := svc.ForwardDeepSeekFiles(context.Background(), nil, account, http.MethodGet, "../secret", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid character")
}

func TestNormalizeDeepSeekFilesUpload_OpenAIPreservesSupportedExpiry(t *testing.T) {
	body, contentType := buildDeepSeekFilesTestUpload(t, []byte("expiring-image"), map[string]string{
		"purpose":                "wrong-purpose",
		"expires_after[anchor]":  "created_at",
		"expires_after[seconds]": "7200",
	})

	normalized, normalizedContentType, err := normalizeDeepSeekFilesUpload(body, contentType, false)
	require.NoError(t, err)
	fields, fileBytes, fileContentType := readDeepSeekFilesTestUpload(t, normalizedContentType, normalized)
	require.Equal(t, []string{"user_data"}, fields["purpose"])
	require.Equal(t, []string{"created_at"}, fields["expires_after[anchor]"])
	require.Equal(t, []string{"7200"}, fields["expires_after[seconds]"])
	require.Equal(t, []byte("expiring-image"), fileBytes)
	require.Equal(t, "image/png", fileContentType)
}

func TestNormalizeDeepSeekFilesUpload_RejectsMultipleFileParts(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, filename := range []string{"first.png", "second.png"} {
		part, err := writer.CreateFormFile("file", filename)
		require.NoError(t, err)
		_, err = part.Write([]byte(filename))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())

	normalized, normalizedContentType, err := normalizeDeepSeekFilesUpload(body.Bytes(), writer.FormDataContentType(), false)
	require.Nil(t, normalized)
	require.Empty(t, normalizedContentType)
	require.ErrorIs(t, err, ErrDeepSeekFilesInvalidUpload)
	require.Contains(t, err.Error(), "exactly one file")
}

func TestCommitDeepSeekFileUpload_RetriesInventoryBeforeReturningSuccess(t *testing.T) {
	cache := &flakyDeepSeekFileInventoryCache{
		schedulerTestGatewayCache: &schedulerTestGatewayCache{},
		failures:                  2,
	}
	svc := &OpenAIGatewayService{cache: cache}
	groupID := int64(42)
	record := deepSeekInventoryTestRecord("file-retried", time.Now().Unix(), 17)

	err := svc.CommitDeepSeekFileUpload(context.Background(), nil, nil, &groupID, 7, record)
	require.NoError(t, err)
	require.Equal(t, deepSeekFileInventoryStoreAttempts, cache.calls)
	stored, err := svc.GetDeepSeekFileRecord(context.Background(), &groupID, 7, record.ID)
	require.NoError(t, err)
	require.Equal(t, record.ID, stored.ID)
}

func TestCommitDeepSeekFileUpload_FinalInventoryFailureDeletesExactUpstreamFile(t *testing.T) {
	cache := &flakyDeepSeekFileInventoryCache{
		schedulerTestGatewayCache: &schedulerTestGatewayCache{},
		failures:                  deepSeekFileInventoryStoreAttempts,
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusNoContent,
		Header:     make(http.Header),
		Body:       http.NoBody,
	}}
	svc := newDeepSeekFilesForwardTestService(upstream)
	svc.cache = cache
	account := &Account{
		ID: 23, Platform: PlatformDeepseek, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-owner-23", "base_url": "https://owner-23.example/v1"},
	}
	groupID := int64(42)
	record := deepSeekInventoryTestRecord("file-compensate", time.Now().Unix(), account.ID)
	body, contentType := buildDeepSeekFilesTestUpload(t, []byte("created-image"), nil)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/files?purpose=should-not-leak", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", contentType)

	err := svc.CommitDeepSeekFileUpload(context.Background(), c, account, &groupID, 7, record)
	require.Error(t, err)
	require.Contains(t, err.Error(), "persist DeepSeek file inventory")
	require.Equal(t, deepSeekFileInventoryStoreAttempts, cache.calls)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, http.MethodDelete, upstream.lastReq.Method)
	require.Equal(t, "https://owner-23.example/v1/files/file-compensate", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer sk-owner-23", upstream.lastReq.Header.Get("Authorization"))
	_, hasDeadline := upstream.lastReq.Context().Deadline()
	require.True(t, hasDeadline)
}

func TestWriteDeepSeekFilesResponseHeadersUsesCompiledFilter(t *testing.T) {
	svc := &OpenAIGatewayService{}
	dst := make(http.Header)
	svc.WriteDeepSeekFilesResponseHeaders(dst, http.Header{
		"Content-Type":                []string{"application/json"},
		"X-Request-ID":                []string{"req-files-1"},
		"Set-Cookie":                  []string{"session=upstream"},
		"Access-Control-Allow-Origin": []string{"https://attacker.example"},
		"X-Unapproved":                []string{"blocked"},
	})

	require.Equal(t, "application/json", dst.Get("Content-Type"))
	require.Equal(t, "req-files-1", dst.Get("X-Request-ID"))
	require.Empty(t, dst.Get("Set-Cookie"))
	require.Empty(t, dst.Get("Access-Control-Allow-Origin"))
	require.Empty(t, dst.Get("X-Unapproved"))
}

func TestHandleDeepSeekFilesUpstreamResultClassifiesAccountFailures(t *testing.T) {
	svc := &OpenAIGatewayService{}
	tests := []struct {
		status int
		want   bool
	}{
		{status: http.StatusOK, want: true},
		{status: http.StatusBadRequest, want: true},
		{status: http.StatusNotFound, want: true},
		{status: http.StatusUnauthorized, want: false},
		{status: http.StatusForbidden, want: false},
		{status: http.StatusTooManyRequests, want: false},
		{status: http.StatusTemporaryRedirect, want: false},
		{status: http.StatusBadGateway, want: false},
	}
	for _, test := range tests {
		t.Run(fmt.Sprintf("status_%d", test.status), func(t *testing.T) {
			require.Equal(t, test.want, svc.HandleDeepSeekFilesUpstreamResult(
				context.Background(), nil, test.status, nil, nil,
			))
		})
	}
}

func TestRenderDeepSeekFilesErrorUsesCallerFamily(t *testing.T) {
	upstream := []byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`)
	openAIBody, err := RenderDeepSeekFilesError(http.StatusTooManyRequests, upstream, false)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(openAIBody, "type").Exists())
	require.Equal(t, "rate_limit_error", gjson.GetBytes(openAIBody, "error.type").String())
	require.Equal(t, "slow down", gjson.GetBytes(openAIBody, "error.message").String())

	anthropicBody, err := RenderDeepSeekFilesError(http.StatusTooManyRequests, []byte(`{"error":{"message":"slow down"}}`), true)
	require.NoError(t, err)
	require.Equal(t, "error", gjson.GetBytes(anthropicBody, "type").String())
	require.Equal(t, "rate_limit_error", gjson.GetBytes(anthropicBody, "error.type").String())
	require.Equal(t, "slow down", gjson.GetBytes(anthropicBody, "error.message").String())
}

func TestDeepSeekFileIDFromUploadResponseRequiresSafeTopLevelID(t *testing.T) {
	id, ok := DeepSeekFileIDFromUploadResponse([]byte(`{"id":"file-known","created_at":"invalid"}`))
	require.True(t, ok)
	require.Equal(t, "file-known", id)

	_, ok = DeepSeekFileIDFromUploadResponse([]byte(`{"id":"../unsafe"}`))
	require.False(t, ok)
	_, ok = DeepSeekFileIDFromUploadResponse([]byte(`{"data":{"id":"file-nested"}}`))
	require.False(t, ok)
}

func TestEnsureAnthropicBetaTokenDeduplicatesValues(t *testing.T) {
	header := http.Header{
		"Anthropic-Beta": []string{"prompt-caching-2024-07-31, " + deepSeekFilesAPIBetaToken},
		"anthropic-beta": []string{"prompt-caching-2024-07-31"},
	}
	ensureAnthropicBetaToken(header, deepSeekFilesAPIBetaToken)
	require.Equal(t, "prompt-caching-2024-07-31,"+deepSeekFilesAPIBetaToken, header.Get("anthropic-beta"))
}

func TestNativeAnthropicVisionFileReferenceAddsBetaToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{}
	account := &Account{
		ID:       10,
		Platform: PlatformDeepseek,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":      "sk-deepseek",
			"api_protocol": APIProtocolAnthropic,
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")
	body := []byte(`{"model":"deepseek-v4-flash-vision-exp","messages":[{"role":"user","content":[{"type":"image","source":{"type":"file","file_id":"file-api-a"}}]}]}`)
	req, _, err := svc.buildNativeAnthropicUpstreamRequest(context.Background(), c, account, body, "sk-deepseek", "https://api.deepseek.com/anthropic/v1/messages")
	require.NoError(t, err)
	require.Equal(t, "sk-deepseek", req.Header.Get("x-api-key"))
	require.ElementsMatch(t, []string{"prompt-caching-2024-07-31", deepSeekFilesAPIBetaToken},
		strings.Split(req.Header.Get("anthropic-beta"), ","))
}

func TestDeepSeekAnthropicRequestUsesFilesAPINarrowly(t *testing.T) {
	require.True(t, deepSeekAnthropicRequestUsesFilesAPI([]byte(`{"messages":[{"content":[{"type":"image","source":{"type":"file","file_id":"file-api-a"}}]}]}`)))
	require.False(t, deepSeekAnthropicRequestUsesFilesAPI([]byte(`{"metadata":{"file_id":"file-api-a"}}`)))
}

func TestValidateDeepSeekAnthropicImageRoles(t *testing.T) {
	require.Error(t, ValidateDeepSeekAnthropicImageRoles([]byte(`{
		"system":[{"type":"text","text":"policy"},{"type":"image","source":{"type":"file","file_id":"system-file"}}],
		"messages":[{"role":"user","content":"inspect"}]
	}`)))
	require.Error(t, ValidateDeepSeekAnthropicImageRoles([]byte(`{
		"messages":[{"role":"assistant","content":[{"type":"image","source":{"type":"file","file_id":"assistant-file"}}]}]
	}`)))
	require.Error(t, ValidateDeepSeekAnthropicImageRoles([]byte(`{
		"messages":[{"role":"assistant","content":[{"type":"tool_result","tool_use_id":"call-1","content":[{"type":"image","source":{"type":"file","file_id":"assistant-file"}}]}]}]
	}`)))
	require.NoError(t, ValidateDeepSeekAnthropicImageRoles([]byte(`{
		"system":"policy",
		"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-1","content":[{"type":"image","source":{"type":"file","file_id":"user-file"}}]}]}]
	}`)))
}

func TestValidateDeepSeekChatImageRoles(t *testing.T) {
	require.Error(t, ValidateDeepSeekChatImageRoles([]byte(`{
		"messages":[{"role":"system","content":[{"type":"image_url","image_url":{"url":"https://example.com/system.png"}}]}]
	}`)))
	require.Error(t, ValidateDeepSeekChatImageRoles([]byte(`{
		"messages":[{"role":"assistant","content":[{"type":"file","file_id":"assistant-file"}],"tool_calls":[{"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{}"}}]}]
	}`)))
	require.NoError(t, ValidateDeepSeekChatImageRoles([]byte(`{
		"messages":[{"role":"system","content":"policy"},{"role":"user","content":[{"type":"file","file_id":"user-file"}]}]
	}`)))
}

func TestValidateDeepSeekResponsesImageRoles(t *testing.T) {
	require.Error(t, ValidateDeepSeekResponsesImageRoles([]byte(`{
		"input":[{"role":"system","content":[{"type":"input_image","file_id":"system-file"}]}]
	}`)))
	require.Error(t, ValidateDeepSeekResponsesImageRoles([]byte(`{
		"input":[{"role":"assistant","content":[{"type":"image_url","image_url":"https://example.com/prior.png"}]}]
	}`)))
	require.NoError(t, ValidateDeepSeekResponsesImageRoles([]byte(`{"input":[
		{"role":"developer","content":[{"type":"input_image","file_id":"developer-file"}]},
		{"type":"function_call_output","call_id":"call-1","output":[{"type":"input_image","file_id":"tool-file"}]},
		{"role":"user","content":[{"type":"input_image","file_id":"user-file"}]}
	]}`)))
}

type flakyDeepSeekFileInventoryCache struct {
	*schedulerTestGatewayCache
	failures int
	calls    int
}

func (c *flakyDeepSeekFileInventoryCache) StoreDeepSeekFileRecord(
	ctx context.Context,
	groupID, userID int64,
	fileID string,
	record []byte,
	createdAt int64,
) error {
	c.calls++
	if c.calls <= c.failures {
		return errors.New("inventory unavailable")
	}
	return c.schedulerTestGatewayCache.StoreDeepSeekFileRecord(ctx, groupID, userID, fileID, record, createdAt)
}

func newDeepSeekFilesForwardTestService(upstream *httpUpstreamRecorder) *OpenAIGatewayService {
	return &OpenAIGatewayService{
		cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{
			Enabled: false, AllowInsecureHTTP: true,
		}}},
		httpUpstream: upstream,
	}
}

func buildDeepSeekFilesTestUpload(t *testing.T, fileBytes []byte, fields map[string]string) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fileHeader := make(textproto.MIMEHeader)
	fileHeader.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{
		"name": "file", "filename": "vision.png",
	}))
	fileHeader.Set("Content-Type", "image/png")
	filePart, err := writer.CreatePart(fileHeader)
	require.NoError(t, err)
	_, err = filePart.Write(fileBytes)
	require.NoError(t, err)
	for name, value := range fields {
		require.NoError(t, writer.WriteField(name, value))
	}
	require.NoError(t, writer.Close())
	return body.Bytes(), writer.FormDataContentType()
}

func readDeepSeekFilesTestUpload(t *testing.T, contentType string, body []byte) (map[string][]string, []byte, string) {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(contentType)
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	fields := make(map[string][]string)
	var fileBytes []byte
	fileContentType := ""
	for {
		part, partErr := reader.NextRawPart()
		if errors.Is(partErr, io.EOF) {
			break
		}
		require.NoError(t, partErr)
		value, readErr := io.ReadAll(part)
		require.NoError(t, readErr)
		if part.FormName() == "file" {
			fileBytes = value
			fileContentType = part.Header.Get("Content-Type")
		} else {
			fields[part.FormName()] = append(fields[part.FormName()], string(value))
		}
		require.NoError(t, part.Close())
	}
	return fields, fileBytes, fileContentType
}
