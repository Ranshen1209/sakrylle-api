package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func newTestSignerSvc(t *testing.T) *OIDCKeyService {
	t.Helper()
	s := NewOIDCKeyService(newMemKeyStore(), b64Encryptor{})
	if err := s.EnsureKey(context.Background()); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}
	return s
}

func TestMaybeSignIDToken_GatingAndSigning(t *testing.T) {
	ks := newTestSignerSvc(t)
	svc := (&OAuthProviderService{}).WithOIDC(
		ks.Sign,
		func(context.Context) string { return testIssuer },
		func(_ context.Context, id int64) (OIDCUserClaims, error) {
			return OIDCUserClaims{UserID: id, Username: "bob", Email: "b@x.io"}, nil
		},
	)

	// Without openid: no id_token.
	tok, err := svc.maybeSignIDToken(context.Background(), "cid", 7, []string{"profile:read"}, "", time.Time{})
	if err != nil || tok != "" {
		t.Fatalf("expected no id_token without openid; tok=%q err=%v", tok, err)
	}

	// With openid + profile + email: signed token with correct claims.
	authTime := time.Unix(1_699_990_000, 0)
	tok, err = svc.maybeSignIDToken(context.Background(), "cid", 7, []string{"openid", "profile", "email"}, "nce", authTime)
	if err != nil || tok == "" {
		t.Fatalf("expected id_token; err=%v", err)
	}
	parsed, _, perr := new(jwt.Parser).ParseUnverified(tok, jwt.MapClaims{})
	if perr != nil {
		t.Fatalf("parse: %v", perr)
	}
	assertSignedClaims(t, parsed, authTime)
}

func assertSignedClaims(t *testing.T, parsed *jwt.Token, authTime time.Time) {
	t.Helper()
	cl := parsed.Claims.(jwt.MapClaims)
	if cl["sub"] != "7" || cl["iss"] != testIssuer {
		t.Errorf("core claims wrong: %v", cl)
	}
	// HIGH-2: aud must decode as a JSON array, not a bare string.
	aud, ok := cl["aud"].([]any)
	if !ok || len(aud) != 1 || aud[0] != "cid" {
		t.Errorf("aud must be a single-element array [\"cid\"]; got %#v", cl["aud"])
	}
	if cl["nonce"] != "nce" {
		t.Errorf("nonce not echoed: %v", cl["nonce"])
	}
	if cl["email"] != "b@x.io" || cl["name"] != "bob" || cl["preferred_username"] != "bob" {
		t.Errorf("profile/email claims missing: %v", cl)
	}
	// MEDIUM: email present ⇒ email_verified explicitly false.
	if v, ok := cl["email_verified"].(bool); !ok || v != false {
		t.Errorf("email_verified must be explicit false; got %#v", cl["email_verified"])
	}
	// MEDIUM: auth_time present (JSON decodes numbers as float64).
	if v, ok := cl["auth_time"].(float64); !ok || int64(v) != authTime.Unix() {
		t.Errorf("auth_time = %#v, want %d", cl["auth_time"], authTime.Unix())
	}
	if parsed.Header["alg"] != "RS256" {
		t.Errorf("alg must be RS256, got %v", parsed.Header["alg"])
	}
}

func TestMaybeSignIDToken_NotWiredIsNoOp(t *testing.T) {
	svc := &OAuthProviderService{} // OIDC not wired
	tok, err := svc.maybeSignIDToken(context.Background(), "c", 1, []string{"openid"}, "", time.Time{})
	if err != nil || tok != "" {
		t.Errorf("unwired OIDC must no-op; tok=%q err=%v", tok, err)
	}
}

func TestMaybeSignIDToken_EmptyIssuerFailsClosed(t *testing.T) {
	ks := newTestSignerSvc(t)
	svc := (&OAuthProviderService{}).WithOIDC(ks.Sign, func(context.Context) string { return "" }, nil)
	if _, err := svc.maybeSignIDToken(context.Background(), "c", 1, []string{"openid"}, "", time.Time{}); err == nil {
		t.Error("empty issuer with openid granted must fail closed")
	}
}

// MEDIUM-1: a failed user-claim lookup must NOT produce a token that advertises
// profile/email scope but carries no name/email claim. The signer logs and
// strips those scopes so the id_token honestly reflects its contents; the
// token still mints (sub is enough for an OIDC identity assertion).
func TestMaybeSignIDToken_UserLookupErrorStripsProfileEmail(t *testing.T) {
	ks := newTestSignerSvc(t)
	svc := (&OAuthProviderService{}).WithOIDC(
		ks.Sign,
		func(context.Context) string { return testIssuer },
		func(_ context.Context, _ int64) (OIDCUserClaims, error) {
			return OIDCUserClaims{}, errors.New("db down")
		},
	)
	tok, err := svc.maybeSignIDToken(context.Background(), "cid", 7, []string{"openid", "profile", "email"}, "n", time.Time{})
	if err != nil || tok == "" {
		t.Fatalf("token must still mint on lookup failure (sub suffices); tok=%q err=%v", tok, err)
	}
	parsed, _, perr := new(jwt.Parser).ParseUnverified(tok, jwt.MapClaims{})
	if perr != nil {
		t.Fatalf("parse: %v", perr)
	}
	cl := parsed.Claims.(jwt.MapClaims)
	if cl["sub"] != "7" {
		t.Errorf("sub must still be present; got %v", cl["sub"])
	}
	for _, k := range []string{"name", "preferred_username", "email", "email_verified"} {
		if _, ok := cl[k]; ok {
			t.Errorf("claim %q must be stripped when user lookup fails; got %v", k, cl[k])
		}
	}
}
