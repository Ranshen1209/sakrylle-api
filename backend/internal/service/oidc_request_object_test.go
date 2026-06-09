package service

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TestParseRequestObjectJWTWithJWKS_RS256_Valid verifies that a well-formed
// RS256 request object (iss == client_id, aud == issuer) is accepted by the
// asymmetric parse path. This guards against the contradictory
// WithIssuer(issuer) constraint that previously rejected every compliant token.
func TestParseRequestObjectJWTWithJWKS_RS256_Valid(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	issuer := "https://sub.sakrylle.com"
	clientID := "client-a"
	claims := &RequestObjectClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    clientID,
			Audience:  jwt.ClaimStrings{issuer},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
		ClientID:            clientID,
		RedirectURI:         "https://rp/cb",
		ResponseType:        "code",
		Scope:               "openid",
		State:               "s",
		CodeChallenge:       "abc",
		CodeChallengeMethod: "S256",
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "k1"
	signed, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("sign request object: %v", err)
	}

	keyFunc := func(alg, kid string) (any, error) { return &priv.PublicKey, nil }
	req, err := ParseRequestObjectJWTWithJWKS(signed, issuer, clientID, "", keyFunc)
	if err != nil {
		t.Fatalf("expected valid RS256 request object, got: %v", err)
	}
	if req.RedirectURI != "https://rp/cb" {
		t.Fatalf("redirect_uri = %q, want https://rp/cb", req.RedirectURI)
	}
	if req.CodeChallenge != "abc" || req.CodeChallengeMethod != "S256" {
		t.Fatalf("code_challenge = %q/%q", req.CodeChallenge, req.CodeChallengeMethod)
	}
}
