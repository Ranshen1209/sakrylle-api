package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// signTestRequestObject builds an HS256-signed request object JWT mirroring what
// an RP would publish at its request_uri. clientSecret is the HMAC key.
func signTestRequestObject(t *testing.T, issuer, clientID, clientSecret string, aud jwt.ClaimStrings) string {
	t.Helper()
	claims := &RequestObjectClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    clientID,
			Audience:  aud,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
		ClientID:            clientID,
		RedirectURI:         "https://rp.example.com/cb",
		ResponseType:        "code",
		Scope:               "openid profile",
		State:               "s-123",
		CodeChallenge:       "challenge-abc",
		CodeChallengeMethod: "S256",
		Nonce:               "nonce-xyz",
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(clientSecret))
	if err != nil {
		t.Fatalf("sign request object: %v", err)
	}
	return signed
}

// TestFetchAndParseRequestURI_Success exercises the real request_uri path:
// FetchRequestURI pulls the object over the SSRF-safe client, then
// ParseRequestObjectJWT verifies and unpacks it. The handler wires these two
// together at /oauth/authorize. We override DefaultHTTPClient with a
// loopback-permitting client so the httptest server (127.0.0.1) is reachable
// while still going through the real fetch code path.
func TestFetchAndParseRequestURI_Success(t *testing.T) {
	issuer := "https://sub.sakrylle.com"
	clientID := "client-a"
	clientSecret := "test-client-secret"

	jwtStr := signTestRequestObject(t, issuer, clientID, clientSecret, jwt.ClaimStrings{issuer})

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/jwt")
		_, _ = w.Write([]byte(jwtStr))
	}))
	defer srv.Close()

	old := DefaultHTTPClient
	// srv.Client() trusts the test server's TLS cert and dials loopback, so the
	// https-only FetchRequestURI path is exercised end to end against httptest.
	DefaultHTTPClient = srv.Client()
	defer func() { DefaultHTTPClient = old }()

	// The host whitelist comes from the client's registered request_uris.
	allowed := []string{srv.URL + "/request.jwt"}

	raw, err := FetchRequestURI(srv.URL+"/request.jwt", allowed)
	if err != nil {
		t.Fatalf("FetchRequestURI: %v", err)
	}
	if strings.TrimSpace(raw) != jwtStr {
		t.Fatalf("fetched body does not match published request object")
	}

	// Verify with the client_secret (HS path), as the inline `request` path does.
	reqObj, err := ParseRequestObjectJWT(raw, issuer, clientID, clientSecret)
	if err != nil {
		t.Fatalf("ParseRequestObjectJWT: %v", err)
	}
	if reqObj.RedirectURI != "https://rp.example.com/cb" {
		t.Fatalf("redirect_uri = %q", reqObj.RedirectURI)
	}
	if reqObj.ResponseType != "code" {
		t.Fatalf("response_type = %q", reqObj.ResponseType)
	}
	if reqObj.CodeChallenge != "challenge-abc" || reqObj.CodeChallengeMethod != "S256" {
		t.Fatalf("pkce = %q/%q", reqObj.CodeChallenge, reqObj.CodeChallengeMethod)
	}
	if len(reqObj.Scopes) != 2 {
		t.Fatalf("scopes = %v", reqObj.Scopes)
	}
}

// TestFetchRequestURI_HostNotRegistered verifies an unregistered host is
// rejected before any network dial, so the authorize handler errors and never
// merges an attacker-controlled object.
func TestFetchRequestURI_HostNotRegistered(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("should-not-be-fetched"))
	}))
	defer srv.Close()

	old := DefaultHTTPClient
	DefaultHTTPClient = srv.Client()
	defer func() { DefaultHTTPClient = old }()

	// request_uri points at srv, but the client only registered a different host.
	allowed := []string{"https://registered.example.com/request.jwt"}

	_, err := FetchRequestURI(srv.URL+"/request.jwt", allowed)
	if err == nil {
		t.Fatal("expected unregistered request_uri host to be rejected")
	}
	if !strings.Contains(err.Error(), "not in the client's registered request_uris") {
		t.Fatalf("expected host-whitelist rejection, got: %v", err)
	}
}

// TestParseRequestObjectJWT_RejectsAudOmittingIssuer guards the tightened aud
// check: a non-empty aud that does not contain the issuer must be rejected
// (previously only an empty aud was rejected, letting a wrong-aud object pass).
func TestParseRequestObjectJWT_RejectsAudOmittingIssuer(t *testing.T) {
	issuer := "https://sub.sakrylle.com"
	clientID := "client-a"
	secret := "test-client-secret"

	jwtStr := signTestRequestObject(t, issuer, clientID, secret,
		jwt.ClaimStrings{"https://other.example.com"})

	_, err := ParseRequestObjectJWT(jwtStr, issuer, clientID, secret)
	if err == nil {
		t.Fatal("expected aud omitting issuer to be rejected")
	}
	if !strings.Contains(err.Error(), "aud does not contain issuer") {
		t.Fatalf("expected aud rejection, got: %v", err)
	}
}
