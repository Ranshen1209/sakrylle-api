package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestAccountAsyncConfig(t *testing.T) {
	a := &Account{Credentials: map[string]any{
		"async_enabled":    "true",
		"async_base_url":   "https://cdn.12ai.org/",
		"api_key":          "sk-x",
		"poll_interval_ms": float64(2000),
	}, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	if !a.IsAsyncImage() {
		t.Fatal("expected IsAsyncImage true")
	}
	if got := a.AsyncImageBaseURL(); got != "https://cdn.12ai.org" {
		t.Fatalf("base url = %q", got)
	}
	if got := a.AsyncPollIntervalMs(); got != 2000 {
		t.Fatalf("poll interval = %d", got)
	}
	if got := a.AsyncMaxWaitMs(); got != 240000 {
		t.Fatalf("default max wait = %d", got)
	}
	off := &Account{Credentials: map[string]any{}}
	if off.IsAsyncImage() {
		t.Fatal("expected IsAsyncImage false when unset")
	}
}

func TestBuildAsyncSubmitBody_Generation(t *testing.T) {
	parsed := &OpenAIImagesRequest{Prompt: "a cat", Size: "1024x1024", Quality: "auto", N: 1}
	b, err := buildAsyncSubmitBody("gpt-image-2", parsed)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["model"] != "gpt-image-2" {
		t.Fatalf("model = %v", m["model"])
	}
	in, _ := m["input"].(map[string]any)
	if in["quality"] != "medium" { // auto → medium
		t.Fatalf("quality = %v, want medium", in["quality"])
	}
	if _, hasImages := in["images"]; hasImages {
		t.Fatal("generation should not include images")
	}
}

func TestBuildAsyncSubmitBody_EditWithUpload(t *testing.T) {
	parsed := &OpenAIImagesRequest{
		Prompt: "make it red", Size: "1024x1024", Quality: "high", N: 1,
		Uploads:    []OpenAIImagesUpload{{ContentType: "image/png", Data: []byte{1, 2, 3}}},
		MaskUpload: &OpenAIImagesUpload{ContentType: "image/png", Data: []byte{4, 5}},
	}
	b, _ := buildAsyncSubmitBody("gpt-image-2", parsed)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	in, _ := m["input"].(map[string]any)
	imgs, _ := in["images"].([]any)
	if len(imgs) != 1 {
		t.Fatalf("images = %v", imgs)
	}
	first, _ := imgs[0].(string)
	if !strings.HasPrefix(first, "data:image/png;base64,") {
		t.Fatalf("images[0] = %v", imgs[0])
	}
	if mask, _ := in["mask"].(string); !strings.HasPrefix(mask, "data:image/png;base64,") {
		t.Fatalf("mask = %v", in["mask"])
	}
}

func TestBuildOpenAIImagesResponse(t *testing.T) {
	b, err := buildOpenAIImagesResponse([]string{"AAA", "BBB"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	data, _ := m["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("data len = %d", len(data))
	}
	d0, _ := data[0].(map[string]any)
	if d0["b64_json"] != "AAA" {
		t.Fatalf("b64_json[0] = %v", data[0])
	}
	if _, ok := m["created"]; !ok {
		t.Fatal("missing created")
	}
}

func TestAsyncClientSubmit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/task/submit" || r.Header.Get("Authorization") != "Bearer sk-x" {
			w.WriteHeader(400)
			return
		}
		_, _ = w.Write([]byte(`{"id":"task_123","status":"queued"}`))
	}))
	defer srv.Close()
	cl := &AsyncImageClient{httpClient: srv.Client(), baseURL: srv.URL, apiKey: "sk-x"}
	id, err := cl.Submit(context.Background(), []byte(`{}`))
	if err != nil || id != "task_123" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

func TestAsyncClientSubmitNoID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"queued"}`))
	}))
	defer srv.Close()
	cl := &AsyncImageClient{httpClient: srv.Client(), baseURL: srv.URL, apiKey: "sk-x"}
	if _, err := cl.Submit(context.Background(), []byte(`{}`)); err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestAsyncClientPollCompletes(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 2 {
			_, _ = w.Write([]byte(`{"status":"in_progress"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"completed","outputs":["https://img/x.png"]}`))
	}))
	defer srv.Close()
	cl := &AsyncImageClient{httpClient: srv.Client(), baseURL: srv.URL, apiKey: "sk-x"}
	pr, err := cl.Poll(context.Background(), "task_1", 5*time.Millisecond, 2*time.Second)
	if err != nil || len(pr.Outputs) != 1 {
		t.Fatalf("pr=%+v err=%v", pr, err)
	}
}

func TestAsyncClientPollFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"failed","error":"nsfw"}`))
	}))
	defer srv.Close()
	cl := &AsyncImageClient{httpClient: srv.Client(), baseURL: srv.URL, apiKey: "sk-x"}
	pr, err := cl.Poll(context.Background(), "t", 5*time.Millisecond, time.Second)
	if err != nil || !pr.Failed || pr.ErrMsg != "nsfw" {
		t.Fatalf("pr=%+v err=%v", pr, err)
	}
}

func TestAsyncClientPollTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"in_progress"}`))
	}))
	defer srv.Close()
	cl := &AsyncImageClient{httpClient: srv.Client(), baseURL: srv.URL, apiKey: "sk-x"}
	_, err := cl.Poll(context.Background(), "t", 5*time.Millisecond, 30*time.Millisecond)
	if !errors.Is(err, errAsyncTimeout) {
		t.Fatalf("want timeout, got %v", err)
	}
}

func TestAsyncClientPollCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"in_progress"}`))
	}))
	defer srv.Close()
	cl := &AsyncImageClient{httpClient: srv.Client(), baseURL: srv.URL, apiKey: "sk-x"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := cl.Poll(ctx, "t", 5*time.Millisecond, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want canceled, got %v", err)
	}
}

func TestAsyncClientFetchAsB64(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte{0xDE, 0xAD})
	}))
	defer srv.Close()
	cl := &AsyncImageClient{httpClient: srv.Client(), baseURL: srv.URL, apiKey: "sk-x"}
	out, err := cl.FetchAsB64(context.Background(), []string{srv.URL + "/a.png"})
	if err != nil || len(out) != 1 || out[0] != "3q0=" { // base64(0xDEAD)
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestIsAsyncImageDisabledByDefault(t *testing.T) {
	a := &Account{Credentials: map[string]any{"api_key": "sk-x", "base_url": "https://new.12ai.org/v1"}}
	if a.IsAsyncImage() {
		t.Fatal("account without async_enabled must not be async (sync path unchanged)")
	}
}

func TestFetchAsB64RejectsBadScheme(t *testing.T) {
	cl := &AsyncImageClient{httpClient: http.DefaultClient, baseURL: "x", apiKey: "k"}
	if _, err := cl.FetchAsB64(context.Background(), []string{"file:///etc/passwd"}); err == nil {
		t.Fatal("expected scheme rejection")
	}
}

func TestFetchAsB64HostSuffixAllowlist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte{1}) }))
	defer srv.Close()
	cl := &AsyncImageClient{httpClient: srv.Client(), baseURL: srv.URL, apiKey: "k", allowedHostSuffix: "12ai.org"}
	if _, err := cl.FetchAsB64(context.Background(), []string{srv.URL + "/x.png"}); err == nil {
		t.Fatal("expected host-not-allowed rejection (127.0.0.1 not under 12ai.org)")
	}
}

func TestFetchAsB64ErrorOmitsURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer srv.Close()
	cl := &AsyncImageClient{httpClient: srv.Client(), baseURL: srv.URL, apiKey: "k"}
	_, err := cl.FetchAsB64(context.Background(), []string{srv.URL + "/secret.png?sig=TOPSECRET"})
	if err == nil || strings.Contains(err.Error(), "TOPSECRET") || strings.Contains(err.Error(), srv.URL) {
		t.Fatalf("error must not leak url/token: %v", err)
	}
}

func TestForwardImagesAsync_FailsClosedWhenUnbilled(t *testing.T) {
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/task/submit":
			_, _ = w.Write([]byte(`{"id":"task_1","status":"queued"}`))
		case strings.HasPrefix(r.URL.Path, "/v1/task/"):
			_, _ = w.Write([]byte(`{"status":"completed","outputs":["` + base + `/img.png"]}`))
		default:
			_, _ = w.Write([]byte{0x01})
		}
	}))
	defer srv.Close()
	base = srv.URL
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"async_enabled": "true", "async_base_url": srv.URL, "api_key": "sk-x",
		// NOTE: no async_image_synth → synth yields 0 output tokens
	}}
	parsed := &OpenAIImagesRequest{Model: "gpt-image-2", Prompt: "x", Size: "1024x1024", SizeTier: "1K", Quality: "high", N: 1}
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/images/edits", nil)
	s := &OpenAIGatewayService{}
	result, err := s.ForwardImagesAsync(context.Background(), c, account, parsed, "gpt-image-2", "gpt-image-2", time.Now())
	if err == nil || result != nil {
		t.Fatalf("expected fail-closed error, got result=%v err=%v", result, err)
	}
	if strings.Contains(w.Body.String(), `"b64_json"`) {
		t.Fatal("must NOT deliver an unbilled image")
	}
}

func TestForwardImagesAsync_EndToEnd(t *testing.T) {
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/task/submit":
			_, _ = w.Write([]byte(`{"id":"task_1","status":"queued"}`))
		case strings.HasPrefix(r.URL.Path, "/v1/task/"):
			_, _ = w.Write([]byte(`{"status":"completed","outputs":["` + base + `/img.png"]}`))
		default: // /img.png download
			_, _ = w.Write([]byte{0x01, 0x02})
		}
	}))
	defer srv.Close()
	base = srv.URL

	account := &Account{
		ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"async_enabled":  "true",
			"async_base_url": srv.URL,
			"api_key":        "sk-x",
			"async_image_synth": map[string]any{
				"output_token_table": map[string]any{"1K": map[string]any{"high": float64(7023)}},
			},
		},
	}
	parsed := &OpenAIImagesRequest{Model: "gpt-image-2", Prompt: "x", Size: "1024x1024", SizeTier: "1K", Quality: "high", N: 1}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/images/edits", nil)

	s := &OpenAIGatewayService{}
	result, err := s.ForwardImagesAsync(context.Background(), c, account, parsed, "gpt-image-2", "gpt-image-2", time.Now())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if result.Usage.OutputTokens != 7023 {
		t.Fatalf("synth output tokens = %d, want 7023", result.Usage.OutputTokens)
	}
	if result.ImageCount != 1 {
		t.Fatalf("image count = %d", result.ImageCount)
	}
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"b64_json"`) {
		t.Fatalf("client response not written: %d %s", w.Code, w.Body.String())
	}
}
