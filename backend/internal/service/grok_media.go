package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type GrokMediaEndpoint string

const (
	GrokMediaEndpointImagesGenerations GrokMediaEndpoint = "images_generations"
	GrokMediaEndpointImagesEdits       GrokMediaEndpoint = "images_edits"
	GrokMediaEndpointVideosGenerations GrokMediaEndpoint = "videos_generations"
	GrokMediaEndpointVideoStatus       GrokMediaEndpoint = "video_status"
)

func (e GrokMediaEndpoint) RequiresRequestBody() bool {
	return e != GrokMediaEndpointVideoStatus
}

func (e GrokMediaEndpoint) IsGenerationRequest() bool {
	switch e {
	case GrokMediaEndpointImagesGenerations, GrokMediaEndpointImagesEdits, GrokMediaEndpointVideosGenerations:
		return true
	default:
		return false
	}
}

type GrokMediaRequestInfo struct {
	Model          string
	Prompt         string
	N              int
	Size           string
	SizeTier       string
	ResponseFormat string
	InputImageURLs []string
	MaskImageURL   string
	Uploads        []OpenAIImagesUpload
	MaskUpload     *OpenAIImagesUpload
}

func (r GrokMediaRequestInfo) ModerationBody() []byte {
	payload := map[string]any{}
	if prompt := strings.TrimSpace(r.Prompt); prompt != "" {
		payload["prompt"] = prompt
	}

	images := make([]map[string]string, 0, len(r.InputImageURLs)+len(r.Uploads)+1)
	for _, imageURL := range r.InputImageURLs {
		if imageURL = strings.TrimSpace(imageURL); imageURL != "" {
			images = append(images, map[string]string{"image_url": imageURL})
		}
	}
	for _, upload := range r.Uploads {
		if dataURL := upload.ModerationDataURL(); dataURL != "" {
			images = append(images, map[string]string{"image_url": dataURL})
		}
	}
	if maskURL := strings.TrimSpace(r.MaskImageURL); maskURL != "" {
		images = append(images, map[string]string{"image_url": maskURL})
	}
	if r.MaskUpload != nil {
		if dataURL := r.MaskUpload.ModerationDataURL(); dataURL != "" {
			images = append(images, map[string]string{"image_url": dataURL})
		}
	}
	if len(images) > 0 {
		payload["images"] = images
	}
	if len(payload) == 0 {
		return nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return body
}

func (e GrokMediaEndpoint) httpMethod() string {
	if e == GrokMediaEndpointVideoStatus {
		return http.MethodGet
	}
	return http.MethodPost
}

func ExtractGrokMediaModel(contentType string, body []byte) string {
	return ParseGrokMediaRequest(contentType, body).Model
}

func ParseGrokMediaRequest(contentType string, body []byte) GrokMediaRequestInfo {
	info := GrokMediaRequestInfo{N: 1}
	if gjson.ValidBytes(body) {
		parseGrokMediaJSONRequest(body, &info)
	} else {
		parseGrokMediaMultipartRequest(contentType, body, &info)
	}
	info.Model = strings.TrimSpace(info.Model)
	info.Prompt = strings.TrimSpace(info.Prompt)
	info.Size = strings.TrimSpace(info.Size)
	info.ResponseFormat = strings.ToLower(strings.TrimSpace(info.ResponseFormat))
	info.SizeTier = NormalizeImageBillingTierOrDefault(info.Size)
	if info.N <= 0 {
		info.N = 1
	}
	return info
}

func parseGrokMediaJSONRequest(body []byte, info *GrokMediaRequestInfo) {
	if info == nil {
		return
	}
	info.Model = strings.TrimSpace(gjson.GetBytes(body, "model").String())
	info.Prompt = strings.TrimSpace(gjson.GetBytes(body, "prompt").String())
	info.Size = strings.TrimSpace(gjson.GetBytes(body, "size").String())
	info.ResponseFormat = strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "response_format").String()))
	if n := gjson.GetBytes(body, "n"); n.Exists() && n.Type == gjson.Number {
		info.N = int(n.Int())
	}
	appendJSONImageURLs := func(value gjson.Result) {
		if !value.Exists() {
			return
		}
		switch {
		case value.IsArray():
			for _, item := range value.Array() {
				if imageURL := strings.TrimSpace(item.Get("image_url").String()); imageURL != "" {
					info.InputImageURLs = append(info.InputImageURLs, imageURL)
					continue
				}
				if item.Type == gjson.String {
					imageURL := strings.TrimSpace(item.String())
					if imageURL == "" {
						continue
					}
					info.InputImageURLs = append(info.InputImageURLs, imageURL)
				}
			}
		default:
			if imageURL := strings.TrimSpace(value.Get("image_url").String()); imageURL != "" {
				info.InputImageURLs = append(info.InputImageURLs, imageURL)
				return
			}
			if value.Type == gjson.String {
				imageURL := strings.TrimSpace(value.String())
				if imageURL == "" {
					return
				}
				info.InputImageURLs = append(info.InputImageURLs, imageURL)
			}
		}
	}
	appendJSONImageURLs(gjson.GetBytes(body, "image"))
	appendJSONImageURLs(gjson.GetBytes(body, "images"))
	info.MaskImageURL = strings.TrimSpace(gjson.GetBytes(body, "mask.image_url").String())
}

func parseGrokMediaMultipartRequest(contentType string, body []byte, info *GrokMediaRequestInfo) {
	if info == nil {
		return
	}
	mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return
	}
	boundary := strings.TrimSpace(params["boundary"])
	if boundary == "" {
		return
	}
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return
		}
		if err != nil {
			return
		}
		name := strings.TrimSpace(part.FormName())
		if name == "" {
			_ = part.Close()
			continue
		}
		data, err := io.ReadAll(io.LimitReader(part, openAIImageMaxUploadPartSize))
		_ = part.Close()
		if err != nil {
			return
		}
		fileName := strings.TrimSpace(part.FileName())
		partContentType := strings.TrimSpace(part.Header.Get("Content-Type"))
		if fileName != "" {
			upload := OpenAIImagesUpload{
				FieldName:   name,
				FileName:    fileName,
				ContentType: partContentType,
				Data:        data,
			}
			if name == "mask" {
				info.MaskUpload = &upload
				continue
			}
			if name == "image" || strings.HasPrefix(name, "image[") {
				info.Uploads = append(info.Uploads, upload)
			}
			continue
		}

		value := strings.TrimSpace(string(data))
		switch name {
		case "model":
			info.Model = value
		case "prompt":
			info.Prompt = value
		case "size":
			info.Size = value
		case "response_format":
			info.ResponseFormat = strings.ToLower(value)
		case "n":
			if n, err := strconv.Atoi(value); err == nil {
				info.N = n
			}
		case "image", "image_url":
			if value != "" {
				info.InputImageURLs = append(info.InputImageURLs, value)
			}
		case "mask", "mask_image_url":
			info.MaskImageURL = value
		}
	}
}

func GrokMediaVideoRequestSessionHash(requestID string) string {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return ""
	}
	return "grok-video:" + DeriveSessionHashFromSeed(requestID)
}

func (s *OpenAIGatewayService) BindGrokMediaVideoRequestAccount(ctx context.Context, groupID *int64, requestID string, accountID int64) error {
	return s.BindStickySession(ctx, groupID, GrokMediaVideoRequestSessionHash(requestID), accountID)
}

func (e GrokMediaEndpoint) upstreamURL(baseURL, requestID string) (string, error) {
	switch e {
	case GrokMediaEndpointImagesGenerations:
		return xai.BuildImagesGenerationsURL(baseURL)
	case GrokMediaEndpointImagesEdits:
		return xai.BuildImagesEditsURL(baseURL)
	case GrokMediaEndpointVideosGenerations:
		return xai.BuildVideosGenerationsURL(baseURL)
	case GrokMediaEndpointVideoStatus:
		return xai.BuildVideoURL(baseURL, requestID)
	default:
		return "", fmt.Errorf("unsupported grok media endpoint: %s", e)
	}
}

func (s *OpenAIGatewayService) ForwardGrokMedia(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	endpoint GrokMediaEndpoint,
	requestID string,
	body []byte,
	contentType string,
) (*OpenAIForwardResult, error) {
	startTime := time.Now()
	if account == nil {
		return nil, fmt.Errorf("grok account is required")
	}
	if account.Platform != PlatformGrok {
		return nil, fmt.Errorf("account platform %s is not supported for grok media", account.Platform)
	}

	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	targetURL, err := endpoint.upstreamURL(account.GetGrokBaseURL(), requestID)
	if err != nil {
		return nil, err
	}

	body, contentType, err = prepareGrokMediaForwardBody(endpoint, body, contentType)
	if err != nil {
		return nil, err
	}

	var bodyReader io.Reader
	if endpoint.RequiresRequestBody() {
		bodyReader = bytes.NewReader(body)
	}
	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	defer releaseUpstreamCtx()
	upstreamReq, err := http.NewRequestWithContext(upstreamCtx, endpoint.httpMethod(), targetURL, bodyReader)
	if err != nil {
		return nil, err
	}
	upstreamReq.Header.Set("Authorization", "Bearer "+token)
	upstreamReq.Header.Set("Accept", "application/json")
	upstreamReq.Header.Set("User-Agent", "sub2api-grok/1.0")
	if endpoint.RequiresRequestBody() {
		contentType = strings.TrimSpace(contentType)
		if contentType == "" {
			contentType = "application/json"
		}
		upstreamReq.Header.Set("Content-Type", contentType)
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	upstreamStart := time.Now()
	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	SetOpsLatencyMs(c, OpsUpstreamLatencyMsKey, time.Since(upstreamStart).Milliseconds())
	if err != nil {
		return nil, s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
	}
	defer func() { _ = resp.Body.Close() }()

	requestIDHeader := firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id"))
	requestInfo := ParseGrokMediaRequest(contentType, body)
	requestModel := requestInfo.Model
	if resp.StatusCode >= 400 {
		s.updateGrokUsageSnapshot(ctx, account.ID, xai.ParseQuotaHeaders(resp.Header, resp.StatusCode))
		return s.handleGrokMediaErrorResponse(ctx, resp, c, account, requestIDHeader, requestModel)
	}

	s.updateGrokUsageSnapshot(ctx, account.ID, xai.ParseQuotaHeaders(resp.Header, resp.StatusCode))
	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, openAITooLargeError)
	if err != nil {
		return nil, err
	}
	responseBody, err := s.normalizeGrokImageResponse(ctx, endpoint, account, respBody, requestInfo.ResponseFormat)
	if err != nil {
		return nil, err
	}
	writeGrokMediaResponse(c, resp, responseBody, s.responseHeaderFilter)
	usage := grokMediaUsageFromResponse(endpoint, requestInfo, responseBody)
	return &OpenAIForwardResult{
		RequestID:        requestIDHeader,
		ResponseID:       usage.ResponseID,
		Usage:            usage.Usage,
		Model:            requestModel,
		BillingModel:     requestModel,
		UpstreamModel:    requestModel,
		ResponseHeaders:  resp.Header.Clone(),
		Duration:         time.Since(startTime),
		ImageCount:       usage.ImageCount,
		ImageSize:        usage.ImageSize,
		ImageInputSize:   usage.ImageInputSize,
		ImageOutputSizes: usage.ImageOutputSizes,
	}, nil
}

func prepareGrokMediaForwardBody(endpoint GrokMediaEndpoint, body []byte, contentType string) ([]byte, string, error) {
	if endpoint != GrokMediaEndpointImagesEdits || gjson.ValidBytes(body) {
		return body, contentType, nil
	}
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return body, contentType, nil
	}

	info := ParseGrokMediaRequest(contentType, body)
	payload := make(map[string]any)
	if info.Model != "" {
		payload["model"] = info.Model
	}
	if info.Prompt != "" {
		payload["prompt"] = info.Prompt
	}
	if info.N > 1 {
		payload["n"] = info.N
	}
	if info.Size != "" {
		payload["size"] = info.Size
	}
	if info.ResponseFormat != "" {
		payload["response_format"] = info.ResponseFormat
	}

	images := make([]map[string]string, 0, len(info.InputImageURLs)+len(info.Uploads))
	for _, imageURL := range info.InputImageURLs {
		if imageURL = strings.TrimSpace(imageURL); imageURL != "" {
			images = append(images, map[string]string{"image_url": imageURL})
		}
	}
	for _, upload := range info.Uploads {
		dataURL, err := openAIImageUploadToDataURL(upload)
		if err != nil {
			return nil, "", err
		}
		images = append(images, map[string]string{"image_url": dataURL})
	}
	if len(images) > 0 {
		payload["image"] = images[0]
		if len(images) > 1 {
			payload["images"] = images
		}
	}

	maskImageURL := strings.TrimSpace(info.MaskImageURL)
	if info.MaskUpload != nil {
		dataURL, err := openAIImageUploadToDataURL(*info.MaskUpload)
		if err != nil {
			return nil, "", err
		}
		maskImageURL = dataURL
	}
	if maskImageURL != "" {
		payload["mask"] = map[string]string{"image_url": maskImageURL}
	}

	out, err := marshalOpenAIUpstreamJSON(payload)
	if err != nil {
		return nil, "", err
	}
	return out, "application/json", nil
}

func (s *OpenAIGatewayService) normalizeGrokImageResponse(
	ctx context.Context,
	endpoint GrokMediaEndpoint,
	account *Account,
	body []byte,
	responseFormat string,
) ([]byte, error) {
	if endpoint != GrokMediaEndpointImagesGenerations && endpoint != GrokMediaEndpointImagesEdits {
		return body, nil
	}
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body, nil
	}
	data := gjson.GetBytes(body, "data")
	if !data.IsArray() {
		return body, nil
	}

	format := strings.ToLower(strings.TrimSpace(responseFormat))
	if format == "" {
		format = "b64_json"
	}
	out := append([]byte(nil), body...)
	changed := false
	for idx, item := range data.Array() {
		imageB64 := normalizeOpenAIImageBase64(item.Get("b64_json").String())
		mimeType := grokImageMIMETypeFromDataURL(item.Get("url").String())
		if imageB64 == "" {
			imageURL := strings.TrimSpace(item.Get("url").String())
			if imageURL == "" {
				continue
			}
			if normalized := normalizeOpenAIImageBase64(imageURL); normalized != "" {
				imageB64 = normalized
			} else {
				downloaded, contentType, err := s.downloadGrokImageURL(ctx, account, imageURL)
				if err != nil {
					return nil, err
				}
				imageB64 = base64.StdEncoding.EncodeToString(downloaded)
				mimeType = normalizeGrokImageMIMEType(contentType, downloaded)
			}
		}
		if imageB64 == "" {
			continue
		}
		if mimeType == "" {
			mimeType = "image/png"
		}
		path := fmt.Sprintf("data.%d", idx)
		if format == "url" {
			out, _ = sjson.SetBytes(out, path+".url", "data:"+mimeType+";base64,"+imageB64)
			out, _ = sjson.DeleteBytes(out, path+".b64_json")
		} else {
			out, _ = sjson.SetBytes(out, path+".b64_json", imageB64)
			out, _ = sjson.DeleteBytes(out, path+".url")
		}
		changed = true
	}
	if !changed {
		return body, nil
	}
	return out, nil
}

func (s *OpenAIGatewayService) downloadGrokImageURL(ctx context.Context, account *Account, rawURL string) ([]byte, string, error) {
	if s == nil || s.httpUpstream == nil {
		return nil, "", fmt.Errorf("grok image downloader is not configured")
	}
	downloadURL := normalizeGrokImageDownloadURL(rawURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "image/*,*/*;q=0.8")
	req.Header.Set("User-Agent", "sub2api-grok/1.0")

	accountID := int64(0)
	concurrency := 1
	proxyURL := ""
	if account != nil {
		accountID = account.ID
		if account.Concurrency > 0 {
			concurrency = account.Concurrency
		}
		if account.ProxyID != nil && account.Proxy != nil {
			proxyURL = account.Proxy.URL()
		}
	}
	resp, err := s.httpUpstream.Do(req, proxyURL, accountID, concurrency)
	if err != nil {
		return nil, "", err
	}
	if resp == nil {
		return nil, "", fmt.Errorf("download image bytes failed: empty response")
	}
	defer func() {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("download image bytes failed: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, openAIImageMaxDownloadBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > openAIImageMaxDownloadBytes {
		return nil, "", fmt.Errorf("download image bytes failed: image exceeds %d bytes", openAIImageMaxDownloadBytes)
	}
	if !isGrokDownloadedImage(resp.Header.Get("Content-Type"), data) {
		return nil, "", fmt.Errorf("download image bytes failed: non-image response content_type=%q", resp.Header.Get("Content-Type"))
	}
	return data, resp.Header.Get("Content-Type"), nil
}

func isGrokDownloadedImage(contentType string, data []byte) bool {
	if mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType)); err == nil {
		if strings.HasPrefix(strings.ToLower(mediaType), "image/") {
			return true
		}
	}
	if len(data) == 0 {
		return false
	}
	return strings.HasPrefix(strings.ToLower(http.DetectContentType(data)), "image/")
}

func normalizeGrokImageDownloadURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		return trimmed
	case strings.HasPrefix(trimmed, "//"):
		return "https:" + trimmed
	default:
		return "https://" + trimmed
	}
}

func grokImageMIMETypeFromDataURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(raw), "data:") {
		return ""
	}
	semi := strings.Index(raw, ";")
	if semi <= len("data:") {
		return ""
	}
	return strings.TrimSpace(raw[len("data:"):semi])
}

func normalizeGrokImageMIMEType(contentType string, data []byte) string {
	if mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType)); err == nil && strings.HasPrefix(strings.ToLower(mediaType), "image/") {
		return mediaType
	}
	if len(data) > 0 {
		if detected := http.DetectContentType(data); strings.HasPrefix(strings.ToLower(detected), "image/") {
			return detected
		}
	}
	return "image/png"
}

type grokMediaUsageMetadata struct {
	ResponseID       string
	Usage            OpenAIUsage
	ImageCount       int
	ImageSize        string
	ImageInputSize   string
	ImageOutputSizes []string
}

func grokMediaUsageFromResponse(endpoint GrokMediaEndpoint, requestInfo GrokMediaRequestInfo, responseBody []byte) grokMediaUsageMetadata {
	usage, _ := extractOpenAIUsageFromJSONBytes(responseBody)
	meta := grokMediaUsageMetadata{Usage: usage}
	switch endpoint {
	case GrokMediaEndpointImagesGenerations, GrokMediaEndpointImagesEdits:
		imageCount := countOpenAIResponseImageOutputsFromJSONBytes(responseBody)
		if imageCount <= 0 {
			imageCount = requestInfo.N
		}
		if imageCount <= 0 {
			imageCount = 1
		}
		meta.ImageCount = imageCount
		meta.ImageSize = requestInfo.SizeTier
		meta.ImageInputSize = requestInfo.Size
		meta.ImageOutputSizes = collectOpenAIResponseImageOutputSizesFromJSONBytes(responseBody)
	case GrokMediaEndpointVideosGenerations:
		meta.ResponseID = extractGrokMediaVideoRequestID(responseBody)
		meta.ImageCount = 1
		meta.ImageSize = requestInfo.SizeTier
		meta.ImageInputSize = requestInfo.Size
	}
	return meta
}

func extractGrokMediaVideoRequestID(body []byte) string {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ""
	}
	for _, path := range []string{"request_id", "id", "data.request_id", "data.id", "video.request_id", "video.id"} {
		if id := strings.TrimSpace(gjson.GetBytes(body, path).String()); id != "" {
			return id
		}
	}
	return ""
}

func (s *OpenAIGatewayService) handleGrokMediaErrorResponse(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *Account,
	requestIDHeader string,
	requestedModel string,
) (*OpenAIForwardResult, error) {
	body := s.readUpstreamErrorBody(resp)
	upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	if upstreamMsg == "" {
		upstreamMsg = fmt.Sprintf("xAI upstream returned status %d", resp.StatusCode)
	}

	upstreamDetail := ""
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = truncateString(string(body), maxBytes)
	}
	setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)

	if status, errType, errMsg, matched := applyErrorPassthroughRule(
		c,
		account.Platform,
		resp.StatusCode,
		body,
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	); matched {
		MarkResponseCommitted(c)
		writeGrokMediaErrorResponse(c, status, errType, errMsg)
		return nil, fmt.Errorf("upstream error: %d (passthrough rule matched) message=%s", resp.StatusCode, upstreamMsg)
	}

	if !account.ShouldHandleErrorCode(resp.StatusCode) {
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  requestIDHeader,
			Kind:               "http_error",
			Message:            upstreamMsg,
			Detail:             upstreamDetail,
		})
		MarkResponseCommitted(c)
		writeGrokMediaErrorResponse(c, http.StatusInternalServerError, "upstream_error", "Upstream gateway error")
		return nil, fmt.Errorf("upstream error: %d (not in custom error codes) message=%s", resp.StatusCode, upstreamMsg)
	}

	s.handleGrokAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, body)
	kind := "http_error"
	if s.shouldFailoverUpstreamError(resp.StatusCode) {
		kind = "failover"
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  requestIDHeader,
		Kind:               kind,
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	})
	if kind == "failover" {
		return nil, &UpstreamFailoverError{
			StatusCode:             resp.StatusCode,
			ResponseBody:           body,
			RetryableOnSameAccount: account.IsPoolMode() && account.IsPoolModeRetryableStatus(resp.StatusCode),
		}
	}

	MarkResponseCommitted(c)
	writeGrokMediaErrorResponse(c, resp.StatusCode, grokMediaErrorType(resp.StatusCode), upstreamMsg)
	return nil, fmt.Errorf("upstream error: %d %s", resp.StatusCode, upstreamMsg)
}

func grokMediaErrorType(statusCode int) string {
	switch statusCode {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	default:
		return "upstream_error"
	}
}

func writeGrokMediaErrorResponse(c *gin.Context, statusCode int, errType, message string) {
	if c == nil || c.Writer == nil || c.Writer.Written() {
		return
	}
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"type":    strings.TrimSpace(errType),
			"message": strings.TrimSpace(message),
		},
	})
}

func writeGrokMediaResponse(c *gin.Context, resp *http.Response, body []byte, filter *responseheaders.CompiledHeaderFilter) {
	if c == nil || resp == nil {
		return
	}
	writeOpenAIPassthroughResponseHeaders(c.Writer.Header(), resp.Header, filter)
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(resp.StatusCode, contentType, body)
}
