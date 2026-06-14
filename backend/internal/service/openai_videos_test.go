package service

import "testing"

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
