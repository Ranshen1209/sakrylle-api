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

func TestConsentHTMLLoginRedirectPreservesAuthorizeURL(t *testing.T) {
	html := oauthConsentHTML("Test Client", &service.AuthorizeRequest{}, "test-nonce")

	if !strings.Contains(html, `window.location.pathname + window.location.search + window.location.hash`) {
		t.Fatalf("consent HTML should preserve the full authorize URL when redirecting to login")
	}
	if !strings.Contains(html, `window.location.href = "/login?redirect=" + next`) {
		t.Fatalf("consent HTML should send login a redirect query so OIDC resumes after relogin")
	}
}
