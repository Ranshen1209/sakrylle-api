package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// newDiscoveryTestHandler returns an OAuthProviderHandler wired with a test
// OIDC key service so both discovery endpoints return 200.
func newDiscoveryTestHandler(t *testing.T) *OAuthProviderHandler {
	t.Helper()
	h := NewOAuthProviderHandler(nil, nil)
	h.SetOIDCKeyService(newTestOIDCKeys(t))
	return h
}

// getDiscoveryDoc calls the given handler and returns the parsed JSON body.
func getDiscoveryDoc(t *testing.T, h *OAuthProviderHandler, path string, handler func(*gin.Context)) map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET(path, handler)

	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "sub.sakrylle.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d body %s", path, w.Code, w.Body.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("GET %s: unmarshal: %v", path, err)
	}
	return doc
}

// stringSlice extracts a JSON array into []string for easier assertion.
func stringSlice(t *testing.T, v any, label string) []string {
	t.Helper()
	arr, ok := v.([]any)
	if !ok {
		t.Fatalf("%s: expected []any, got %T", label, v)
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		s, ok := e.(string)
		if !ok {
			t.Fatalf("%s: element %v is not string", label, e)
		}
		out = append(out, s)
	}
	return out
}

// TestDiscoverySharedFields verifies that RFC 8414 Metadata and OIDC Discovery
// return identical values for every field that originates from
// commonDiscoveryMetadata (the shared helper).
func TestDiscoverySharedFields(t *testing.T) {
	h := newDiscoveryTestHandler(t)
	rfc := getDiscoveryDoc(t, h, "/.well-known/oauth-authorization-server", h.Metadata)
	oidc := getDiscoveryDoc(t, h, "/.well-known/openid-configuration", h.OpenIDConfiguration)

	// Fields that both endpoints must share (all come from commonDiscoveryMetadata).
	sharedFields := []string{
		"issuer",
		"authorization_endpoint",
		"token_endpoint",
		"userinfo_endpoint",
		"jwks_uri",
		"end_session_endpoint",
		"response_types_supported",
		"response_modes_supported",
		"grant_types_supported",
		"code_challenge_methods_supported",
		"scopes_supported",
		"token_endpoint_auth_methods_supported",
		"claims_supported",
		"prompt_values_supported",
		"service_documentation",
	}

	for _, field := range sharedFields {
		rfcVal, ok := rfc[field]
		if !ok {
			t.Errorf("RFC 8414 missing shared field %q", field)
			continue
		}
		oidcVal, ok := oidc[field]
		if !ok {
			t.Errorf("OIDC missing shared field %q", field)
			continue
		}

		// Compare JSON representations so []string vs []any differences don't
		// cause false mismatches.
		rfcJSON, _ := json.Marshal(rfcVal)
		oidcJSON, _ := json.Marshal(oidcVal)
		if string(rfcJSON) != string(oidcJSON) {
			t.Errorf("field %q differs: RFC=%s  OIDC=%s", field, rfcJSON, oidcJSON)
		}
	}
}

// TestRFC8414MetadataNewFields verifies the fields that were added to the
// RFC 8414 Metadata endpoint as part of the alignment (jwks_uri,
// end_session_endpoint, claims_supported, prompt_values_supported).
func TestRFC8414MetadataNewFields(t *testing.T) {
	h := newDiscoveryTestHandler(t)
	doc := getDiscoveryDoc(t, h, "/.well-known/oauth-authorization-server", h.Metadata)

	// jwks_uri must be the standard JWKS endpoint.
	jwksURI, ok := doc["jwks_uri"].(string)
	if !ok {
		t.Fatal("jwks_uri missing or not string")
	}
	if want := "https://sub.sakrylle.com/.well-known/jwks.json"; jwksURI != want {
		t.Errorf("jwks_uri = %q, want %q", jwksURI, want)
	}

	// end_session_endpoint must point to the logout route.
	logoutURI, ok := doc["end_session_endpoint"].(string)
	if !ok {
		t.Fatal("end_session_endpoint missing or not string")
	}
	if want := "https://sub.sakrylle.com/oauth/logout"; logoutURI != want {
		t.Errorf("end_session_endpoint = %q, want %q", logoutURI, want)
	}

	// claims_supported must include at least the core OIDC claims.
	claims := stringSlice(t, doc["claims_supported"], "claims_supported")
	for _, want := range []string{"iss", "sub", "aud", "exp", "iat"} {
		if !oidcContains(claims, want) {
			t.Errorf("claims_supported missing %q", want)
		}
	}

	// prompt_values_supported must be present with the expected values.
	prompts := stringSlice(t, doc["prompt_values_supported"], "prompt_values_supported")
	for _, want := range []string{"none", "login", "consent", "select_account"} {
		if !oidcContains(prompts, want) {
			t.Errorf("prompt_values_supported missing %q", want)
		}
	}

	// Also verify RFC 8414-specific fields that predate this change are still present.
	for _, field := range []string{
		"revocation_endpoint",
		"device_authorization_endpoint",
		"ui_locales_supported",
		"revocation_endpoint_auth_methods_supported",
	} {
		if _, ok := doc[field]; !ok {
			t.Errorf("RFC 8414 missing its own field %q", field)
		}
	}
}

// TestOIDCDiscoverySpecificFields verifies that the OIDC Discovery endpoint
// includes OIDC-specific fields that the RFC 8414 endpoint does not have.
func TestOIDCDiscoverySpecificFields(t *testing.T) {
	h := newDiscoveryTestHandler(t)
	doc := getDiscoveryDoc(t, h, "/.well-known/openid-configuration", h.OpenIDConfiguration)

	// subject_types_supported
	subjectTypes := stringSlice(t, doc["subject_types_supported"], "subject_types_supported")
	if !oidcContains(subjectTypes, "public") {
		t.Error("subject_types_supported missing 'public'")
	}

	// id_token_signing_alg_values_supported
	algs := stringSlice(t, doc["id_token_signing_alg_values_supported"], "id_token_signing_alg_values_supported")
	for _, want := range []string{"RS256", "ES256"} {
		if !oidcContains(algs, want) {
			t.Errorf("id_token_signing_alg_values_supported missing %q", want)
		}
	}

	// userinfo_signing_alg_values_supported
	uiAlgs := stringSlice(t, doc["userinfo_signing_alg_values_supported"], "userinfo_signing_alg_values_supported")
	for _, want := range []string{"RS256", "ES256"} {
		if !oidcContains(uiAlgs, want) {
			t.Errorf("userinfo_signing_alg_values_supported missing %q", want)
		}
	}

	// Boolean OIDC-specific fields advertised true.
	for _, field := range []string{
		"claims_parameter_supported",
		"backchannel_logout_supported",
		"backchannel_logout_session_supported",
	} {
		val, ok := doc[field]
		if !ok {
			t.Errorf("OIDC discovery missing field %q", field)
			continue
		}
		b, ok := val.(bool)
		if !ok || !b {
			t.Errorf("field %q = %v, want true", field, val)
		}
	}

	// request/request_uri are advertised false: fetch+merge is implemented but
	// request-object signature verification is not yet wired for real clients
	// (ParseRequestObjectJWT is called with an empty client_secret).
	for _, field := range []string{
		"request_parameter_supported",
		"request_uri_parameter_supported",
	} {
		val, ok := doc[field]
		if !ok {
			t.Errorf("OIDC discovery missing field %q", field)
			continue
		}
		b, ok := val.(bool)
		if !ok || b {
			t.Errorf("field %q = %v, want false", field, val)
		}
	}
}

// TestOIDCDiscoveryRequiresKeys verifies that the OIDC Discovery endpoint
// returns 404 when no OIDC key service is wired.
func TestOIDCDiscoveryRequiresKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewOAuthProviderHandler(nil, nil)
	// oidcKeys is nil by default

	r := gin.New()
	r.GET("/.well-known/openid-configuration", h.OpenIDConfiguration)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/openid-configuration", nil)
	req.Host = "sub.sakrylle.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("want 404 when OIDC keys absent, got %d", w.Code)
	}
}

// TestRFC8414MetadataAlwaysAvailable verifies that the RFC 8414 endpoint
// always returns 200 regardless of whether OIDC keys are wired.
func TestRFC8414MetadataAlwaysAvailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewOAuthProviderHandler(nil, nil)
	// oidcKeys is nil -- RFC 8414 is independent of OIDC signing

	r := gin.New()
	r.GET("/.well-known/oauth-authorization-server", h.Metadata)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	req.Host = "sub.sakrylle.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("RFC 8414 should always be 200, got %d", w.Code)
	}
}
