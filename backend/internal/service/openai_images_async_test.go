package service

import (
	"encoding/json"
	"strings"
	"testing"
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
