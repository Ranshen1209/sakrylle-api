package service

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
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
