package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
)

var (
	ErrDeepSeekFilesInvalidUpload               = errors.New("invalid DeepSeek Files upload")
	ErrDeepSeekFilesUpstreamProtocolUnsupported = errors.New("DeepSeek Files upload is unsupported by the selected upstream protocol")
)

const (
	deepSeekFileInventoryStoreAttempts = 3
	deepSeekFileInventoryStoreBackoff  = 25 * time.Millisecond
	deepSeekFileInventoryStoreTimeout  = 5 * time.Second
	deepSeekFileCompensationTimeout    = 10 * time.Second
)

type deepSeekFilesPreserveUpstreamContextKey struct{}

// ForwardDeepSeekFiles forwards the OpenAI-compatible DeepSeek Files API.
// DeepSeek exposes this API at /files (without the /v1 prefix on the default
// base URL), while callers use the gateway's /v1/files alias. Upload multipart
// bodies are rebuilt for the selected protocol while preserving each file
// part's bytes, filename, and content type.
//
// fileID is empty for POST /files and GET /files; GET/DELETE /files/:file_id
// pass the ID separately so it can be path-escaped safely.
func (s *OpenAIGatewayService) ForwardDeepSeekFiles(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	method string,
	fileID string,
	body []byte,
) (*http.Response, error) {
	if s == nil || s.httpUpstream == nil {
		return nil, fmt.Errorf("openai gateway upstream is unavailable")
	}
	if account == nil || account.Platform != PlatformDeepseek {
		return nil, fmt.Errorf("DeepSeek Files API requires a DeepSeek account")
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	if method != http.MethodGet && method != http.MethodPost && method != http.MethodDelete {
		return nil, fmt.Errorf("unsupported DeepSeek Files API method: %s", method)
	}
	fileID = strings.TrimSpace(fileID)
	if fileID != "" {
		validatedFileID, validateErr := validateDeepSeekFileID(fileID)
		if validateErr != nil {
			return nil, validateErr
		}
		fileID = validatedFileID
	}
	if method == http.MethodPost && fileID != "" {
		return nil, fmt.Errorf("POST DeepSeek Files API does not accept a file id")
	}
	if method == http.MethodDelete && fileID == "" {
		return nil, fmt.Errorf("DELETE DeepSeek Files API requires a file id")
	}

	// Fixed accounts keep their configured upstream protocol and base URL; this
	// is important for relay credentials that must never be sent to DeepSeek's
	// official host. Adaptive accounts select a Files surface from the caller.
	// The handler normalizes upstream metadata back to the inbound SDK family.
	nativeAnthropic := DeepSeekFilesUseNativeAnthropicUpstream(c, account)
	outboundBody := body
	outboundContentType := ""
	if method == http.MethodPost {
		contentType := ""
		if c != nil && c.Request != nil {
			contentType = c.Request.Header.Get("Content-Type")
		}
		normalizedBody, normalizedContentType, normalizeErr := normalizeDeepSeekFilesUpload(body, contentType, nativeAnthropic)
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		outboundBody = normalizedBody
		outboundContentType = normalizedContentType
	}
	baseURL := deepSeekFilesBaseURL(account, nativeAnthropic)
	endpoint := "/files"
	if nativeAnthropic {
		endpoint = "/v1/files"
	}
	if strings.TrimSpace(baseURL) == "" {
		if nativeAnthropic {
			baseURL = DefaultDeepseekAnthropicBaseURL
		} else {
			baseURL = DefaultDeepseekBaseURL
		}
	}
	validatedURL, err := s.validateUpstreamBaseURL(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid DeepSeek Files base_url: %w", err)
	}
	targetURL := buildOpenAIEndpointURL(validatedURL, endpoint)
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return nil, fmt.Errorf("build DeepSeek Files URL: %w", err)
	}
	if fileID != "" {
		parsedURL.Path = strings.TrimRight(parsedURL.Path, "/") + "/" + url.PathEscape(fileID)
	}
	targetURL = parsedURL.String()

	upstreamCtx, releaseUpstreamCtx := ctx, func() {}
	preserveUpstreamContext := false
	if ctx != nil {
		preserveUpstreamContext, _ = ctx.Value(deepSeekFilesPreserveUpstreamContextKey{}).(bool)
	}
	if !preserveUpstreamContext {
		upstreamCtx, releaseUpstreamCtx = detachUpstreamContext(ctx)
	}
	upstreamReq, err := http.NewRequestWithContext(upstreamCtx, method, targetURL, bytes.NewReader(outboundBody))
	releaseUpstreamCtx()
	if err != nil {
		return nil, fmt.Errorf("build DeepSeek Files request: %w", err)
	}
	requestContext := WithHTTPUpstreamRedirectsDisabled(upstreamReq.Context())
	if !nativeAnthropic {
		requestContext = WithHTTPUpstreamProfile(requestContext, HTTPUpstreamProfileOpenAI)
	}
	upstreamReq = upstreamReq.WithContext(requestContext)
	copyDeepSeekFilesHeaders(upstreamReq.Header, c)
	if method == http.MethodPost {
		upstreamReq.Header.Set("Content-Type", outboundContentType)
	} else {
		deleteHeaderAllForms(upstreamReq.Header, "content-type")
	}
	deleteHeaderAllForms(upstreamReq.Header, "authorization")
	deleteHeaderAllForms(upstreamReq.Header, "x-api-key")
	deleteHeaderAllForms(upstreamReq.Header, "x-goog-api-key")
	deleteHeaderAllForms(upstreamReq.Header, "cookie")
	token, _, err := s.GetAccessToken(upstreamCtx, account)
	if err != nil {
		return nil, fmt.Errorf("get DeepSeek Files credential: %w", err)
	}
	// The OpenAI-compatible endpoint uses Bearer authentication. The native
	// Anthropic endpoint defaults to x-api-key (with the account-level scheme
	// override still honored), just like /v1/messages.
	if nativeAnthropic {
		setAnthropicAPIKeyAuthHeader(upstreamReq.Header, account, strings.TrimSpace(token))
	} else {
		upstreamReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	if upstreamReq.Header.Get("Accept") == "" {
		upstreamReq.Header.Set("Accept", "application/json")
	}
	account.ApplyHeaderOverrides(upstreamReq.Header)
	if nativeAnthropic {
		// DeepSeek requires this beta token on every Anthropic-compatible Files
		// request, including list/retrieve/delete operations.
		ensureAnthropicBetaToken(upstreamReq.Header, deepSeekFilesAPIBetaToken)
	} else {
		deleteHeaderAllForms(upstreamReq.Header, "anthropic-version")
		deleteHeaderAllForms(upstreamReq.Header, "anthropic-beta")
	}

	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, true)
	}
	return resp, nil
}

// HandleDeepSeekFilesUpstreamResult applies account health side effects for
// statuses that identify an unusable credential, provider capacity problem,
// or unsafe redirect. Client/file-specific 4xx responses keep the account
// healthy. The return value is suitable for scheduler success reporting.
func (s *OpenAIGatewayService) HandleDeepSeekFilesUpstreamResult(
	ctx context.Context,
	account *Account,
	statusCode int,
	headers http.Header,
	body []byte,
) bool {
	accountFailure := statusCode >= http.StatusInternalServerError ||
		(statusCode >= http.StatusMultipleChoices && statusCode < http.StatusBadRequest)
	switch statusCode {
	case http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden,
		http.StatusProxyAuthRequired, http.StatusRequestTimeout, http.StatusTooManyRequests:
		accountFailure = true
	}
	if accountFailure {
		s.handleOpenAIAccountUpstreamError(ctx, account, statusCode, headers, body)
	}
	return !accountFailure
}

// RenderDeepSeekFilesError normalizes an upstream error into the caller's Files
// API family so a fixed cross-protocol account never leaks the wrong envelope.
func RenderDeepSeekFilesError(statusCode int, upstreamBody []byte, nativeAnthropic bool) ([]byte, error) {
	message := strings.TrimSpace(ExtractUpstreamErrorMessage(upstreamBody))
	if message == "" {
		message = "Upstream Files API request failed"
	}
	message = truncateString(message, 2048)
	errType := deepSeekFilesErrorType(statusCode)
	if nativeAnthropic {
		return json.Marshal(map[string]any{
			"type": "error",
			"error": map[string]any{
				"type": errType, "message": message,
			},
		})
	}
	return json.Marshal(map[string]any{
		"error": map[string]any{
			"type": errType, "message": message,
		},
	})
}

func deepSeekFilesErrorType(statusCode int) string {
	switch statusCode {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusPaymentRequired, http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	default:
		return "api_error"
	}
}

// DeepSeekFileIDFromUploadResponse extracts only a validated top-level file ID.
// It is used to compensate a 2xx upload whose remaining metadata is malformed.
func DeepSeekFileIDFromUploadResponse(body []byte) (string, bool) {
	var wire struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return "", false
	}
	id, err := validateDeepSeekFileID(wire.ID)
	return id, err == nil
}

// CompensateDeepSeekFileUpload deletes a known file ID on the exact account
// that returned it, independently of client cancellation.
func (s *OpenAIGatewayService) CompensateDeepSeekFileUpload(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	fileID string,
) error {
	compensationCtx, cancel := deepSeekFileOperationContext(ctx, deepSeekFileCompensationTimeout)
	defer cancel()
	return s.deleteDeepSeekFileOnAccount(compensationCtx, c, account, fileID)
}

// CommitDeepSeekFileUpload makes the tenant inventory binding durable before
// the new file ID is exposed to the caller. A transport error from POST is
// ambiguous, so the upload itself is never replayed. Once an HTTP success and
// file ID are available, bounded inventory retries are safe; if they all fail,
// the exact account that created the file receives a compensating DELETE.
func (s *OpenAIGatewayService) CommitDeepSeekFileUpload(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	groupID *int64,
	userID int64,
	record DeepSeekFileRecord,
) error {
	persistCtx, cancelPersist := deepSeekFileOperationContext(ctx, deepSeekFileInventoryStoreTimeout)
	defer cancelPersist()

	var storeErr error
	attempts := 0
	for attempt := 0; attempt < deepSeekFileInventoryStoreAttempts; attempt++ {
		attempts++
		storeErr = s.StoreDeepSeekFileRecord(persistCtx, groupID, userID, record)
		if storeErr == nil {
			return nil
		}
		if attempt+1 >= deepSeekFileInventoryStoreAttempts || persistCtx.Err() != nil {
			break
		}
		if err := waitDeepSeekFileRetry(persistCtx, time.Duration(attempt+1)*deepSeekFileInventoryStoreBackoff); err != nil {
			break
		}
	}

	compensationCtx, cancelCompensation := deepSeekFileOperationContext(ctx, deepSeekFileCompensationTimeout)
	defer cancelCompensation()
	compensationErr := s.deleteDeepSeekFileOnAccount(compensationCtx, c, account, record.ID)
	var cleanupErr error
	if compensationErr == nil {
		cleanupErr = errors.Join(
			s.DeleteDeepSeekFileAccount(compensationCtx, groupID, userID, record.ID),
			s.DeleteDeepSeekFileRecord(compensationCtx, groupID, userID, record.ID),
		)
	}
	return fmt.Errorf("persist DeepSeek file inventory after %d attempts: %w; compensation: %v",
		attempts, storeErr, errors.Join(compensationErr, cleanupErr))
}

func deepSeekFileOperationContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	return context.WithTimeout(base, timeout)
}

func waitDeepSeekFileRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *OpenAIGatewayService) deleteDeepSeekFileOnAccount(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	fileID string,
) error {
	ctx = context.WithValue(ctx, deepSeekFilesPreserveUpstreamContextKey{}, true)
	deleteContext := c
	if c != nil {
		deleteContext = c.Copy()
		if c.Request != nil {
			deleteContext.Request = c.Request.Clone(ctx)
			deleteContext.Request.Method = http.MethodDelete
			deleteContext.Request.Body = http.NoBody
			if deleteContext.Request.URL != nil {
				urlCopy := *deleteContext.Request.URL
				urlCopy.RawQuery = ""
				deleteContext.Request.URL = &urlCopy
			}
		}
	}
	resp, err := s.ForwardDeepSeekFiles(ctx, deleteContext, account, http.MethodDelete, fileID, nil)
	if err != nil {
		return fmt.Errorf("delete created DeepSeek file: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("delete created DeepSeek file: empty upstream response")
	}
	if resp.Body != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
	}
	if (resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices) || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("delete created DeepSeek file: upstream returned status %d", resp.StatusCode)
}

// WriteDeepSeekFilesResponseHeaders applies the same compiled allowlist used by
// the other gateway paths. In particular, the default policy excludes cookies
// and CORS headers supplied by an upstream relay.
func (s *OpenAIGatewayService) WriteDeepSeekFilesResponseHeaders(dst, src http.Header) {
	var filter *responseheaders.CompiledHeaderFilter
	if s != nil {
		filter = s.responseHeaderFilter
	}
	responseheaders.WriteFilteredHeaders(dst, src, filter)
}

func normalizeDeepSeekFilesUpload(body []byte, contentType string, nativeAnthropic bool) ([]byte, string, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return nil, "", fmt.Errorf("%w: content type must be multipart/form-data", ErrDeepSeekFilesInvalidUpload)
	}
	boundary := strings.TrimSpace(params["boundary"])
	if boundary == "" {
		return nil, "", fmt.Errorf("%w: multipart boundary is required", ErrDeepSeekFilesInvalidUpload)
	}

	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	var output bytes.Buffer
	writer := multipart.NewWriter(&output)
	fileParts := 0
	for {
		part, partErr := reader.NextRawPart()
		if errors.Is(partErr, io.EOF) {
			break
		}
		if partErr != nil {
			return nil, "", fmt.Errorf("%w: read multipart body: %v", ErrDeepSeekFilesInvalidUpload, partErr)
		}
		name := part.FormName()
		if nativeAnthropic && (name == "expires_after[anchor]" || name == "expires_after[seconds]") {
			_ = part.Close()
			return nil, "", fmt.Errorf("%w: expires_after is not supported by the Anthropic Files endpoint", ErrDeepSeekFilesUpstreamProtocolUnsupported)
		}
		keep := name == "file"
		if !nativeAnthropic && (name == "expires_after[anchor]" || name == "expires_after[seconds]") {
			keep = true
		}
		if keep {
			if name == "file" {
				fileParts++
			}
			outPart, createErr := writer.CreatePart(cloneDeepSeekFilePartHeader(part.Header))
			if createErr != nil {
				_ = part.Close()
				return nil, "", fmt.Errorf("%w: create multipart part: %v", ErrDeepSeekFilesInvalidUpload, createErr)
			}
			if _, copyErr := io.Copy(outPart, part); copyErr != nil {
				_ = part.Close()
				return nil, "", fmt.Errorf("%w: copy multipart part: %v", ErrDeepSeekFilesInvalidUpload, copyErr)
			}
		}
		if closeErr := part.Close(); closeErr != nil {
			return nil, "", fmt.Errorf("%w: close multipart part: %v", ErrDeepSeekFilesInvalidUpload, closeErr)
		}
	}
	if fileParts != 1 {
		return nil, "", fmt.Errorf("%w: exactly one file field is required", ErrDeepSeekFilesInvalidUpload)
	}
	if !nativeAnthropic {
		if err := writer.WriteField("purpose", "user_data"); err != nil {
			return nil, "", fmt.Errorf("%w: write purpose field: %v", ErrDeepSeekFilesInvalidUpload, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("%w: finalize multipart body: %v", ErrDeepSeekFilesInvalidUpload, err)
	}
	return output.Bytes(), writer.FormDataContentType(), nil
}

func cloneDeepSeekFilePartHeader(src textproto.MIMEHeader) textproto.MIMEHeader {
	dst := make(textproto.MIMEHeader, len(src))
	for key, values := range src {
		dst[key] = append([]string(nil), values...)
	}
	return dst
}

// DeepSeekFilesUseNativeAnthropicIngress identifies the SDK-facing Files
// family from explicit Anthropic protocol headers. Authentication header style
// is not a protocol signal because OpenAI-compatible clients may use x-api-key.
func DeepSeekFilesUseNativeAnthropicIngress(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	header := c.Request.Header
	connectionScoped := deepSeekFilesConnectionScopedHeaders(header)
	_, versionIsConnectionScoped := connectionScoped["anthropic-version"]
	_, betaIsConnectionScoped := connectionScoped["anthropic-beta"]
	return (!versionIsConnectionScoped && strings.TrimSpace(header.Get("anthropic-version")) != "") ||
		(!betaIsConnectionScoped && strings.TrimSpace(header.Get("anthropic-beta")) != "")
}

// DeepSeekFilesUseNativeAnthropicUpstream resolves the account-facing Files
// family independently from the ingress response family. Fixed accounts keep
// their configured protocol; adaptive accounts follow the caller.
func DeepSeekFilesUseNativeAnthropicUpstream(c *gin.Context, account *Account) bool {
	if account == nil {
		return false
	}
	if account.IsAdaptiveAPIProtocol() {
		return DeepSeekFilesUseNativeAnthropicIngress(c)
	}
	return account.IsAnthropicProtocol()
}

func deepSeekFilesBaseURL(account *Account, nativeAnthropic bool) string {
	if account == nil {
		return ""
	}
	if account.IsAdaptiveAPIProtocol() {
		if nativeAnthropic {
			return account.GetCNProtocolBaseURL(APIProtocolAnthropic)
		}
		return account.GetCNProtocolBaseURL(APIProtocolChatCompletions)
	}
	if nativeAnthropic {
		return account.GetAnthropicProtocolBaseURL()
	}
	return account.GetOpenAIFormatBaseURL()
}

// copyDeepSeekFilesHeaders forwards only the gateway's established client
// metadata allowlist. Connection-scoped headers are hop-by-hop even when their
// names would otherwise be allowed.
func copyDeepSeekFilesHeaders(dst http.Header, c *gin.Context) {
	if c == nil || c.Request == nil {
		return
	}
	connectionScoped := deepSeekFilesConnectionScopedHeaders(c.Request.Header)
	for key, values := range c.Request.Header {
		lower := strings.ToLower(strings.TrimSpace(key))
		if !allowedHeaders[lower] {
			continue
		}
		if _, isConnectionScoped := connectionScoped[lower]; isConnectionScoped {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func deepSeekFilesConnectionScopedHeaders(header http.Header) map[string]struct{} {
	scoped := make(map[string]struct{})
	for key, values := range header {
		if !strings.EqualFold(strings.TrimSpace(key), "connection") {
			continue
		}
		for _, value := range values {
			for _, token := range strings.Split(value, ",") {
				if name := strings.ToLower(strings.TrimSpace(token)); name != "" {
					scoped[name] = struct{}{}
				}
			}
		}
	}
	return scoped
}
