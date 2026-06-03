package service

import (
	"context"
	"testing"

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
	tok, err := svc.maybeSignIDToken(context.Background(), "cid", 7, []string{"profile:read"}, "")
	if err != nil || tok != "" {
		t.Fatalf("expected no id_token without openid; tok=%q err=%v", tok, err)
	}

	// With openid + profile + email: signed token with correct claims.
	tok, err = svc.maybeSignIDToken(context.Background(), "cid", 7, []string{"openid", "profile", "email"}, "nce")
	if err != nil || tok == "" {
		t.Fatalf("expected id_token; err=%v", err)
	}
	parsed, _, perr := new(jwt.Parser).ParseUnverified(tok, jwt.MapClaims{})
	if perr != nil {
		t.Fatalf("parse: %v", perr)
	}
	cl := parsed.Claims.(jwt.MapClaims)
	if cl["sub"] != "7" || cl["aud"] != "cid" || cl["iss"] != testIssuer {
		t.Errorf("core claims wrong: %v", cl)
	}
	if cl["nonce"] != "nce" {
		t.Errorf("nonce not echoed: %v", cl["nonce"])
	}
	if cl["email"] != "b@x.io" || cl["name"] != "bob" || cl["preferred_username"] != "bob" {
		t.Errorf("profile/email claims missing: %v", cl)
	}
	if parsed.Header["alg"] != "RS256" {
		t.Errorf("alg must be RS256, got %v", parsed.Header["alg"])
	}
}

func TestMaybeSignIDToken_NotWiredIsNoOp(t *testing.T) {
	svc := &OAuthProviderService{} // OIDC not wired
	tok, err := svc.maybeSignIDToken(context.Background(), "c", 1, []string{"openid"}, "")
	if err != nil || tok != "" {
		t.Errorf("unwired OIDC must no-op; tok=%q err=%v", tok, err)
	}
}

func TestMaybeSignIDToken_EmptyIssuerFailsClosed(t *testing.T) {
	ks := newTestSignerSvc(t)
	svc := (&OAuthProviderService{}).WithOIDC(ks.Sign, func(context.Context) string { return "" }, nil)
	if _, err := svc.maybeSignIDToken(context.Background(), "c", 1, []string{"openid"}, ""); err == nil {
		t.Error("empty issuer with openid granted must fail closed")
	}
}
