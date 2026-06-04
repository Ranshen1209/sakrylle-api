package service

import (
	"reflect"
	"testing"
	"time"
)

const testIssuer = "https://sub.sakrylle.com"

func baseUser() OIDCUserClaims {
	return OIDCUserClaims{UserID: 42, Email: "user@example.com", Username: "alice"}
}

func TestBuildIDTokenClaims_RequiredClaims(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	claims, err := BuildIDTokenClaims(testIssuer, "client-x", baseUser(), []string{"openid"}, "", time.Time{}, now, time.Hour, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if claims["iss"] != testIssuer {
		t.Errorf("iss = %v, want %s", claims["iss"], testIssuer)
	}
	if claims["sub"] != "42" {
		t.Errorf("sub = %v, want \"42\" (user.ID string)", claims["sub"])
	}
	// HIGH-2: aud must be a string array, not a bare string, so strict
	// JS/Python OIDC libraries that iterate aud do not throw.
	if aud, ok := claims["aud"].([]string); !ok || !reflect.DeepEqual(aud, []string{"client-x"}) {
		t.Errorf("aud = %#v, want []string{\"client-x\"}", claims["aud"])
	}
	if claims["iat"] != now.Unix() {
		t.Errorf("iat = %v, want %d", claims["iat"], now.Unix())
	}
	if claims["exp"] != now.Add(time.Hour).Unix() {
		t.Errorf("exp = %v, want %d", claims["exp"], now.Add(time.Hour).Unix())
	}
}

func TestBuildIDTokenClaims_ScopeGating(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)

	// openid only: no profile/email claims.
	c, _ := BuildIDTokenClaims(testIssuer, "c", baseUser(), []string{"openid"}, "", time.Time{}, now, time.Hour, "")
	for _, k := range []string{"name", "preferred_username", "email", "email_verified"} {
		if _, ok := c[k]; ok {
			t.Errorf("claim %q must be absent without its scope", k)
		}
	}

	// openid + profile + email: claims present.
	c2, _ := BuildIDTokenClaims(testIssuer, "c", baseUser(), []string{"openid", "profile", "email"}, "", time.Time{}, now, time.Hour, "")
	if c2["name"] != "alice" || c2["preferred_username"] != "alice" {
		t.Errorf("profile scope must yield name/preferred_username; got name=%v pref=%v", c2["name"], c2["preferred_username"])
	}
	if c2["email"] != "user@example.com" {
		t.Errorf("email scope must yield email; got %v", c2["email"])
	}
	// MEDIUM: email present ⇒ email_verified MUST be explicitly false
	// (OIDC Core §5.1: absent email_verified is treated as unverified, but
	// being explicit is honest and avoids RP ambiguity).
	if v, ok := c2["email_verified"].(bool); !ok || v != false {
		t.Errorf("email_verified must be explicit false when email is set; got %#v", c2["email_verified"])
	}
}

func TestBuildIDTokenClaims_AuthTime(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	authTime := time.Unix(1_699_999_000, 0)

	// auth_time present when supplied (RP may request max_age / step-up).
	c, _ := BuildIDTokenClaims(testIssuer, "c", baseUser(), []string{"openid"}, "", authTime, now, time.Hour, "")
	if c["auth_time"] != authTime.Unix() {
		t.Errorf("auth_time = %v, want %d", c["auth_time"], authTime.Unix())
	}
	// auth_time omitted when zero (unknown).
	c2, _ := BuildIDTokenClaims(testIssuer, "c", baseUser(), []string{"openid"}, "", time.Time{}, now, time.Hour, "")
	if _, ok := c2["auth_time"]; ok {
		t.Error("auth_time must be absent when authTime is zero")
	}
}

func TestBuildIDTokenClaims_NonceRoundTrip(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	withNonce, _ := BuildIDTokenClaims(testIssuer, "c", baseUser(), []string{"openid"}, "n-123", time.Time{}, now, time.Hour, "")
	if withNonce["nonce"] != "n-123" {
		t.Errorf("nonce must be echoed; got %v", withNonce["nonce"])
	}
	noNonce, _ := BuildIDTokenClaims(testIssuer, "c", baseUser(), []string{"openid"}, "", time.Time{}, now, time.Hour, "")
	if _, ok := noNonce["nonce"]; ok {
		t.Error("nonce claim must be absent when no nonce was requested")
	}
}

func TestBuildIDTokenClaims_Validation(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	if _, err := BuildIDTokenClaims("", "c", baseUser(), []string{"openid"}, "", time.Time{}, now, time.Hour, ""); err == nil {
		t.Error("empty issuer must error")
	}
	if _, err := BuildIDTokenClaims(testIssuer, "", baseUser(), []string{"openid"}, "", time.Time{}, now, time.Hour, ""); err == nil {
		t.Error("empty client_id must error")
	}
	if _, err := BuildIDTokenClaims(testIssuer, "c", OIDCUserClaims{UserID: 0}, []string{"openid"}, "", time.Time{}, now, time.Hour, ""); err == nil {
		t.Error("zero user id must error")
	}
}

// id_token must never carry mutable business/PII state.
func TestBuildIDTokenClaims_NoBusinessData(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c, err := BuildIDTokenClaims(testIssuer, "c", baseUser(), []string{"openid", "profile", "email"}, "n", time.Time{}, now, time.Hour, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"balance", "group", "group_id", "rate_multiplier", "quota", "quota_used", "model_mapping", "models", "capabilities"} {
		if _, ok := c[forbidden]; ok {
			t.Errorf("id_token must not contain business claim %q", forbidden)
		}
	}
}

func TestAssertNoForbiddenClaims(t *testing.T) {
	if err := assertNoForbiddenClaims(map[string]any{"sub": "1", "email": "a@b.c"}); err != nil {
		t.Errorf("allowlisted claims must pass: %v", err)
	}
	if err := assertNoForbiddenClaims(map[string]any{"sub": "1", "balance": 8.94}); err == nil {
		t.Error("a forbidden business claim must be rejected")
	}
}
