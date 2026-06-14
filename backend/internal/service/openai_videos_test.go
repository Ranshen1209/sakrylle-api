package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseVideosRequestAndUsageSynth(t *testing.T) {
	body := []byte(`{"model":"agnes-video-v2.0","prompt":"a cat","size":"1280x768"}`)
	req, err := ParseOpenAIVideosRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "agnes-video-v2.0" || req.Prompt != "a cat" {
		t.Fatalf("parsed wrong: %#v", req)
	}
	if _, err := ParseOpenAIVideosRequest([]byte(`{"prompt":"x"}`)); err == nil {
		t.Fatal("empty model must be rejected")
	}
	if _, err := ParseOpenAIVideosRequest([]byte(`not json`)); err == nil {
		t.Fatal("invalid json must be rejected")
	}

	if got := synthVideoOutputTokens(10.0, 18.375); got != 10 {
		t.Fatalf("10.0s -> 10 tokens, got %d", got)
	}
	if got := synthVideoOutputTokens(10.4, 18.375); got != 10 {
		t.Fatalf("round down 10.4 -> 10, got %d", got)
	}
	if got := synthVideoOutputTokens(10.5, 18.375); got != 11 {
		t.Fatalf("round half up 10.5 -> 11, got %d", got)
	}
	if got := synthVideoOutputTokens(0, 18.375); got != 18 {
		t.Fatalf("0 -> fallback round(18.375)=18, got %d", got)
	}
}

func TestVideoClientSubmit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/videos" {
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Authorization") != "Bearer KEY" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"video_id":"video_abc","status":"queued","seconds":"10.0","size":"1280x768"}`))
	}))
	defer srv.Close()

	cl := &VideoClient{httpClient: &http.Client{Timeout: 5 * time.Second}, baseURL: srv.URL, apiKey: "KEY", submitPath: "/v1/videos"}
	sub, err := cl.Submit(context.Background(), []byte(`{"model":"agnes-video-v2.0","prompt":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if sub.VideoID != "video_abc" {
		t.Fatalf("video_id = %q", sub.VideoID)
	}

	bad := &VideoClient{httpClient: srv.Client(), baseURL: srv.URL, apiKey: "WRONG", submitPath: "/v1/videos"}
	if _, err := bad.Submit(context.Background(), []byte(`{}`)); err == nil {
		t.Fatal("401 must error")
	}
}

func TestAccountVideoConfig(t *testing.T) {
	a := &Account{Credentials: map[string]any{
		"video_enabled":          "true",
		"video_models":           "agnes-video-v2.0",
		"video_submit_path":      "/v1/videos",
		"video_poll_path":        "/agnesapi",
		"video_poll_interval_ms": "5000",
		"video_max_wait_ms":      "600000",
		"video_default_seconds":  "18.375",
		"video_host_suffix":      "agnes-ai.com, googleapis.com",
	}}
	if !a.IsVideoEnabled() {
		t.Fatal("video should be enabled")
	}
	if !a.IsDeclaredVideoModel("agnes-video-v2.0") {
		t.Fatal("declared video model should match")
	}
	if a.VideoSubmitPath() != "/v1/videos" || a.VideoPollPath() != "/agnesapi" {
		t.Fatalf("paths wrong: %s %s", a.VideoSubmitPath(), a.VideoPollPath())
	}
	if a.VideoPollIntervalMs() != 5000 || a.VideoMaxWaitMs() != 600000 {
		t.Fatalf("intervals wrong: %d %d", a.VideoPollIntervalMs(), a.VideoMaxWaitMs())
	}
	if a.VideoDefaultSeconds() != 18.375 {
		t.Fatalf("default seconds wrong: %v", a.VideoDefaultSeconds())
	}
	if hs := a.VideoHostSuffixes(); len(hs) != 2 || hs[0] != "agnes-ai.com" || hs[1] != "googleapis.com" {
		t.Fatalf("host suffixes wrong: %#v", hs)
	}
	b := &Account{Credentials: map[string]any{}}
	if b.IsVideoEnabled() {
		t.Fatal("absent -> disabled")
	}
	if b.VideoSubmitPath() != "/v1/videos" || b.VideoPollPath() != "/agnesapi" {
		t.Fatal("absent -> default paths")
	}
	if b.VideoPollIntervalMs() != 5000 || b.VideoMaxWaitMs() != 600000 {
		t.Fatal("absent -> default intervals")
	}
	if b.VideoDefaultSeconds() != 18.375 {
		t.Fatal("absent -> default seconds")
	}
}
