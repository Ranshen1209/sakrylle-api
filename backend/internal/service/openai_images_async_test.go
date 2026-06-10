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
	in := m["input"].(map[string]any)
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
	in := m["input"].(map[string]any)
	imgs := in["images"].([]any)
	if len(imgs) != 1 || !strings.HasPrefix(imgs[0].(string), "data:image/png;base64,") {
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
	data := m["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("data len = %d", len(data))
	}
	if data[0].(map[string]any)["b64_json"] != "AAA" {
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
