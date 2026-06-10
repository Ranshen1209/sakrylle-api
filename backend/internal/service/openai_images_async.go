package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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

// AsyncImageClient is an HTTP client for the 12ai async task bridge.
type AsyncImageClient struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
}

type asyncSubmitResp struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// Submit POSTs body to /v1/task/submit and returns the task ID.
func (cl *AsyncImageClient) Submit(ctx context.Context, body []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cl.baseURL+"/v1/task/submit", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+cl.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := cl.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("submit status %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	var sr asyncSubmitResp
	if err := json.Unmarshal(rb, &sr); err != nil {
		return "", fmt.Errorf("submit parse: %w", err)
	}
	if strings.TrimSpace(sr.ID) == "" {
		return "", fmt.Errorf("submit returned no task id")
	}
	return sr.ID, nil
}

var errAsyncTimeout = errors.New("async image task timed out")

// AsyncPollResult holds the outcome of a completed async task poll.
type AsyncPollResult struct {
	Outputs []string
	Failed  bool
	ErrMsg  string
}

type asyncTaskResp struct {
	ID      string   `json:"id"`
	Status  string   `json:"status"`
	Outputs []string `json:"outputs"`
	Error   string   `json:"error"`
}

func (cl *AsyncImageClient) getTask(ctx context.Context, taskID string) (*asyncTaskResp, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cl.baseURL+"/v1/task/"+taskID, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cl.apiKey)
	resp, err := cl.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("poll status %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	var tr asyncTaskResp
	if err := json.Unmarshal(rb, &tr); err != nil {
		return nil, fmt.Errorf("poll parse: %w", err)
	}
	return &tr, nil
}

// Poll polls the task until terminal status, ctx cancel, or maxWait. Up to 3
// consecutive transient poll errors are tolerated before aborting.
func (cl *AsyncImageClient) Poll(ctx context.Context, taskID string, interval, maxWait time.Duration) (*AsyncPollResult, error) {
	deadline := time.Now().Add(maxWait)
	consecFail := 0
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			return nil, errAsyncTimeout
		}
		tr, err := cl.getTask(ctx, taskID)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			consecFail++
			if consecFail >= 3 {
				return nil, err
			}
		} else {
			consecFail = 0
			switch strings.ToLower(tr.Status) {
			case "completed":
				return &AsyncPollResult{Outputs: tr.Outputs}, nil
			case "partial_completed":
				return &AsyncPollResult{Outputs: tr.Outputs, ErrMsg: tr.Error}, nil
			case "failed":
				return &AsyncPollResult{Failed: true, ErrMsg: tr.Error}, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// FetchAsB64 downloads each URL and returns the base64-encoded contents.
func (cl *AsyncImageClient) FetchAsB64(ctx context.Context, urls []string) ([]string, error) {
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		resp, err := cl.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("download %s status %d", u, resp.StatusCode)
		}
		out = append(out, base64.StdEncoding.EncodeToString(b))
	}
	return out, nil
}
