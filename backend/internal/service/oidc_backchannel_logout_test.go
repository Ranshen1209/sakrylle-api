package service

import (
	"testing"
	"time"
)

func TestBuildLogoutToken_IncludesUniqueJTI(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)

	c1 := BuildLogoutToken("https://sub.sakrylle.com", "42", "client-a", "sid-1", now, 0)
	jti1, ok := c1["jti"].(string)
	if !ok || jti1 == "" {
		t.Fatalf("logout_token must carry a non-empty jti, got %v", c1["jti"])
	}

	c2 := BuildLogoutToken("https://sub.sakrylle.com", "42", "client-a", "sid-1", now, 0)
	jti2, ok := c2["jti"].(string)
	if !ok || jti2 == "" {
		t.Fatalf("logout_token must carry a non-empty jti, got %v", c2["jti"])
	}

	if jti1 == jti2 {
		t.Fatalf("jti must be unique per logout_token, got duplicate %q", jti1)
	}
}

func TestBuildLogoutToken_CarriesSidWhenPresent(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := BuildLogoutToken("https://sub.sakrylle.com", "42", "client-a", "sid-xyz", now, 0)
	if c["sid"] != "sid-xyz" {
		t.Fatalf("sid = %v, want sid-xyz", c["sid"])
	}
}
