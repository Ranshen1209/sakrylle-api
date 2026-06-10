package service

import "testing"

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
