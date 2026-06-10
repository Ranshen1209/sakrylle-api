package service

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// IsAsyncImage reports whether this account routes image requests through the
// async task bridge (12ai cdn task API) instead of the synchronous endpoint.
func (a *Account) IsAsyncImage() bool {
	return strings.EqualFold(strings.TrimSpace(a.GetCredential("async_enabled")), "true")
}

// AsyncImageBaseURL is the async task host, e.g. https://cdn.12ai.org (no trailing slash).
func (a *Account) AsyncImageBaseURL() string {
	return strings.TrimRight(strings.TrimSpace(a.GetCredential("async_base_url")), "/")
}

func (a *Account) asyncIntCredential(key string, def int) int {
	v := strings.TrimSpace(a.GetCredential(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// AsyncPollIntervalMs is the poll interval in ms (default 3000).
func (a *Account) AsyncPollIntervalMs() int { return a.asyncIntCredential("poll_interval_ms", 3000) }

// AsyncMaxWaitMs is the max total poll wait in ms (default 240000).
func (a *Account) AsyncMaxWaitMs() int { return a.asyncIntCredential("max_wait_ms", 240000) }

// asyncDataURI returns a data URI for the given content type and raw bytes.
func asyncDataURI(contentType string, data []byte) string {
	ct := strings.TrimSpace(contentType)
	if ct == "" {
		ct = "image/png"
	}
	return "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// buildAsyncSubmitBody translates a parsed OpenAI images request into a 12ai
// task-submit payload: {"model":..., "input":{prompt,size,quality,n,images,mask}}.
func buildAsyncSubmitBody(model string, parsed *OpenAIImagesRequest) ([]byte, error) {
	n := parsed.N
	if n <= 0 {
		n = 1
	}
	input := map[string]any{
		"prompt":  parsed.Prompt,
		"size":    parsed.Size,
		"quality": normalizeAsyncQuality(parsed.Quality),
		"n":       n,
	}
	images := make([]string, 0, len(parsed.Uploads)+len(parsed.InputImageURLs))
	for _, up := range parsed.Uploads {
		images = append(images, asyncDataURI(up.ContentType, up.Data))
	}
	images = append(images, parsed.InputImageURLs...)
	if len(images) > 0 {
		input["images"] = images
	}
	if parsed.MaskUpload != nil {
		input["mask"] = asyncDataURI(parsed.MaskUpload.ContentType, parsed.MaskUpload.Data)
	} else if strings.TrimSpace(parsed.MaskImageURL) != "" {
		input["mask"] = parsed.MaskImageURL
	}
	return json.Marshal(map[string]any{"model": model, "input": input})
}

// buildOpenAIImagesResponse builds an OpenAI images response body from base64 images.
func buildOpenAIImagesResponse(b64s []string) ([]byte, error) {
	data := make([]map[string]any, 0, len(b64s))
	for _, b := range b64s {
		data = append(data, map[string]any{"b64_json": b})
	}
	return json.Marshal(map[string]any{
		"created": time.Now().Unix(),
		"data":    data,
	})
}
