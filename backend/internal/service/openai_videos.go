package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/sjson"
)

func (a *Account) IsVideoEnabled() bool {
	if a == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(a.GetCredential("video_enabled")), "true")
}

func (a *Account) videoModels() []string {
	if a == nil {
		return nil
	}
	raw := strings.TrimSpace(a.GetCredential("video_models"))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (a *Account) IsDeclaredVideoModel(model string) bool {
	model = strings.TrimSpace(strings.ToLower(model))
	for _, m := range a.videoModels() {
		if strings.ToLower(m) == model {
			return true
		}
	}
	return false
}

func (a *Account) videoStrCredential(key, def string) string {
	if a == nil {
		return def
	}
	if v := strings.TrimSpace(a.GetCredential(key)); v != "" {
		return v
	}
	return def
}

func (a *Account) VideoSubmitPath() string {
	return a.videoStrCredential("video_submit_path", "/v1/videos")
}
func (a *Account) VideoPollPath() string { return a.videoStrCredential("video_poll_path", "/agnesapi") }

func (a *Account) videoIntCredential(key string, def int) int {
	if a == nil {
		return def
	}
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

func (a *Account) VideoPollIntervalMs() int {
	return a.videoIntCredential("video_poll_interval_ms", 5000)
}
func (a *Account) VideoMaxWaitMs() int { return a.videoIntCredential("video_max_wait_ms", 600000) }

func (a *Account) VideoDefaultSeconds() float64 {
	if a == nil {
		return 18.375
	}
	v := strings.TrimSpace(a.GetCredential("video_default_seconds"))
	if v == "" {
		return 18.375
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 {
		return 18.375
	}
	return f
}

func (a *Account) VideoHostSuffixes() []string {
	if a == nil {
		return nil
	}
	raw := strings.TrimSpace(a.GetCredential("video_host_suffix"))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// validateVideoURL allows only http/https and, when allowSuffixes is non-empty,
// requires the host to equal or be a subdomain of one suffix. Mirrors
// validateAsyncImageURL but supports multiple suffixes.
func validateVideoURL(raw string, allowSuffixes []string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid video url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported video url scheme")
	}
	if len(allowSuffixes) == 0 {
		return nil
	}
	host := u.Hostname()
	for _, suf := range allowSuffixes {
		if host == suf || strings.HasSuffix(host, "."+suf) {
			return nil
		}
	}
	return fmt.Errorf("video url host not allowed")
}

// buildVideosResponse builds the client-facing JSON for a completed video task.
func buildVideosResponse(model, videoURL string, seconds float64, size string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"created": time.Now().Unix(),
		"model":   model,
		"object":  "video",
		"status":  "completed",
		"url":     videoURL,
		"seconds": seconds,
		"size":    size,
	})
}

// OpenAIVideosRequest is the client-facing /v1/videos request (OpenAI-ish).
// Raw preserves the original body for verbatim forwarding to Agnes (after model rewrite).
type OpenAIVideosRequest struct {
	Model  string
	Prompt string
	Raw    []byte
}

// ParseOpenAIVideosRequest extracts model+prompt and keeps the raw body. Rejects
// invalid JSON and an empty model.
func ParseOpenAIVideosRequest(body []byte) (*OpenAIVideosRequest, error) {
	var probe struct {
		Model  string `json:"model"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, fmt.Errorf("failed to parse request body")
	}
	if strings.TrimSpace(probe.Model) == "" {
		return nil, fmt.Errorf("videos endpoint requires a model")
	}
	return &OpenAIVideosRequest{Model: probe.Model, Prompt: probe.Prompt, Raw: body}, nil
}

// synthVideoOutputTokens maps generated video seconds to synthetic output tokens
// (1 token = 1 second) for token-mode billing. Falls back to fallbackSeconds when
// seconds is non-positive/unknown. Returns 0 only when both are non-positive.
func synthVideoOutputTokens(seconds, fallbackSeconds float64) int {
	if seconds <= 0 {
		seconds = fallbackSeconds
	}
	if seconds <= 0 {
		return 0
	}
	return int(math.Round(seconds))
}

// VideoClient talks to the Agnes async video API. baseURL includes the /v1 segment
// (e.g. https://apihub.agnes-ai.com/v1); pollBaseURL is the host without /v1 because
// Agnes's poll endpoint (/agnesapi) is NOT under /v1.
type VideoClient struct {
	httpClient   *http.Client
	baseURL      string
	pollBaseURL  string
	apiKey       string
	submitPath   string
	pollPath     string
	hostSuffixes []string
}

type videoSubmitResp struct {
	VideoID string `json:"video_id"`
	TaskID  string `json:"task_id"`
	Status  string `json:"status"`
	Seconds string `json:"seconds"`
	Size    string `json:"size"`
}

func (cl *VideoClient) submitURL() string {
	return strings.TrimRight(cl.baseURL, "/") + "/" + strings.TrimLeft(cl.submitPath, "/")
}

// Submit POSTs the (already model-rewritten) body to the Agnes video submit endpoint
// and returns the task identifiers.
func (cl *VideoClient) Submit(ctx context.Context, body []byte) (*videoSubmitResp, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cl.submitURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cl.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := cl.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("video submit status %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	var sr videoSubmitResp
	if err := json.Unmarshal(rb, &sr); err != nil {
		return nil, fmt.Errorf("video submit parse: %w", err)
	}
	if strings.TrimSpace(sr.VideoID) == "" && strings.TrimSpace(sr.TaskID) == "" {
		return nil, fmt.Errorf("video submit returned no id")
	}
	return &sr, nil
}

var errVideoTimeout = errors.New("video task timed out")

// VideoPollResult is the terminal outcome of polling a video task.
type VideoPollResult struct {
	URL     string
	Seconds float64
	Size    string
	Failed  bool
	ErrMsg  string
}

type videoTaskResp struct {
	Status             string `json:"status"`
	Seconds            string `json:"seconds"`
	Size               string `json:"size"`
	RemixedFromVideoID string `json:"remixed_from_video_id"`
	Error              string `json:"error"`
}

func (cl *VideoClient) pollURL(videoID string) string {
	base := strings.TrimRight(cl.pollBaseURL, "/") + "/" + strings.TrimLeft(cl.pollPath, "/")
	return base + "?video_id=" + url.QueryEscape(videoID)
}

func parseVideoSeconds(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return f
}

func (cl *VideoClient) getTask(ctx context.Context, videoID string) (*videoTaskResp, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cl.pollURL(videoID), nil)
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
		return nil, fmt.Errorf("video poll status %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	var tr videoTaskResp
	if err := json.Unmarshal(rb, &tr); err != nil {
		return nil, fmt.Errorf("video poll parse: %w", err)
	}
	return &tr, nil
}

// Poll polls the Agnes video task until completed/failed, ctx cancel, or maxWait.
// Tolerates up to 3 consecutive transient poll errors before aborting.
func (cl *VideoClient) Poll(ctx context.Context, videoID string, interval, maxWait time.Duration) (*VideoPollResult, error) {
	deadline := time.Now().Add(maxWait)
	consecFail := 0
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			return nil, errVideoTimeout
		}
		tr, err := cl.getTask(ctx, videoID)
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
			switch strings.ToLower(strings.TrimSpace(tr.Status)) {
			case "completed":
				return &VideoPollResult{URL: strings.TrimSpace(tr.RemixedFromVideoID), Seconds: parseVideoSeconds(tr.Seconds), Size: tr.Size}, nil
			case "failed":
				return &VideoPollResult{Failed: true, ErrMsg: tr.Error}, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
	}
}

func (s *OpenAIGatewayService) newVideoClient(account *Account) *VideoClient {
	base := strings.TrimRight(strings.TrimSpace(account.GetCredential("base_url")), "/")
	pollBase := strings.TrimSuffix(base, "/v1")
	return &VideoClient{
		httpClient:   &http.Client{Timeout: 60 * time.Second},
		baseURL:      base,
		pollBaseURL:  pollBase,
		apiKey:       account.GetOpenAIApiKey(),
		submitPath:   account.VideoSubmitPath(),
		pollPath:     account.VideoPollPath(),
		hostSuffixes: account.VideoHostSuffixes(),
	}
}

// ForwardVideo submits a video job to the Agnes async API, polls to completion,
// writes a JSON response with the upstream video URL, and returns a result whose
// synthesized Usage drives token-mode billing (OutputTokens = round(seconds)).
// ctx must be cancelable so a client disconnect stops polling (no response, no billing).
func (s *OpenAIGatewayService) ForwardVideo(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	parsed *OpenAIVideosRequest,
	requestModel, upstreamModel string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	cl := s.newVideoClient(account)

	body := parsed.Raw
	if upstreamModel != "" && upstreamModel != parsed.Model {
		if rewritten, err := sjson.SetBytes(body, "model", upstreamModel); err == nil {
			body = rewritten
		}
	}

	submitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	sub, err := cl.Submit(submitCtx, body)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, asyncFail(c, http.StatusBadGateway, "video submit failed: "+err.Error())
	}
	videoID := sub.VideoID
	if videoID == "" {
		videoID = sub.TaskID
	}

	interval := time.Duration(account.VideoPollIntervalMs()) * time.Millisecond
	maxWait := time.Duration(account.VideoMaxWaitMs()) * time.Millisecond
	pr, err := cl.Poll(ctx, videoID, interval, maxWait)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case errors.Is(err, errVideoTimeout):
			return nil, asyncFail(c, http.StatusGatewayTimeout, "video task timed out")
		default:
			return nil, asyncFail(c, http.StatusBadGateway, err.Error())
		}
	}
	if pr.Failed || strings.TrimSpace(pr.URL) == "" {
		msg := "video task failed"
		if pr.ErrMsg != "" {
			msg += ": " + pr.ErrMsg
		}
		return nil, asyncFail(c, http.StatusBadGateway, msg)
	}
	if err := validateVideoURL(pr.URL, account.VideoHostSuffixes()); err != nil {
		return nil, asyncFail(c, http.StatusBadGateway, "video url rejected: "+err.Error())
	}

	outputTokens := synthVideoOutputTokens(pr.Seconds, account.VideoDefaultSeconds())
	if outputTokens == 0 {
		return nil, asyncFail(c, http.StatusInternalServerError, "video billing not configured (no seconds)")
	}

	respJSON, err := buildVideosResponse(requestModel, pr.URL, pr.Seconds, pr.Size)
	if err != nil {
		return nil, asyncFail(c, http.StatusInternalServerError, err.Error())
	}
	c.Data(http.StatusOK, "application/json", respJSON)

	return &OpenAIForwardResult{
		Usage:         OpenAIUsage{OutputTokens: outputTokens},
		Model:         requestModel,
		UpstreamModel: upstreamModel,
		Duration:      time.Since(startTime),
		ImageCount:    1,
	}, nil
}
