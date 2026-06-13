package handler

import (
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestConsentHTMLBucketsByImageOnly(t *testing.T) {
	html := oauthConsentHTML("Test Client", &service.AuthorizeRequest{}, "test-nonce")

	// Bucketing must key on image_only, not allow_image_generation.
	if !strings.Contains(html, "g.image_only") {
		t.Fatalf("consent HTML should bucket image groups by g.image_only")
	}
	if strings.Contains(html, "g.allow_image_generation") {
		t.Fatalf("consent HTML must not bucket by g.allow_image_generation (overloaded by Codex gate)")
	}
}
