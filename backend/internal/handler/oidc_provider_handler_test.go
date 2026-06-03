package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

type memOIDCStore struct{ m map[string]string }

func (s *memOIDCStore) Get(_ context.Context, k string) (string, bool, error) {
	v, ok := s.m[k]
	return v, ok, nil
}
func (s *memOIDCStore) Put(_ context.Context, k, v string) error { s.m[k] = v; return nil }

type passEnc struct{}

func (passEnc) Encrypt(p string) (string, error) {
	return "e:" + base64.StdEncoding.EncodeToString([]byte(p)), nil
}
func (passEnc) Decrypt(c string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(c, "e:"))
	return string(b), err
}

func newTestOIDCKeys(t *testing.T) *service.OIDCKeyService {
	t.Helper()
	svc := service.NewOIDCKeyService(&memOIDCStore{m: map[string]string{}}, passEnc{})
	if err := svc.EnsureKey(context.Background()); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}
	return svc
}

func TestOpenIDConfiguration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewOAuthProviderHandler(nil, nil)
	r := gin.New()
	r.GET("/.well-known/openid-configuration", h.OpenIDConfiguration)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/openid-configuration", nil)
	req.Host = "sub.sakrylle.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["issuer"] != "https://sub.sakrylle.com" {
		t.Errorf("issuer=%v want https://sub.sakrylle.com", doc["issuer"])
	}
	if doc["jwks_uri"] != "https://sub.sakrylle.com/.well-known/jwks.json" {
		t.Errorf("jwks_uri=%v", doc["jwks_uri"])
	}
	if doc["token_endpoint"] != "https://sub.sakrylle.com/oauth/token" {
		t.Errorf("token_endpoint=%v", doc["token_endpoint"])
	}
	if doc["userinfo_endpoint"] != "https://sub.sakrylle.com/v1/me" {
		t.Errorf("userinfo_endpoint=%v", doc["userinfo_endpoint"])
	}
	if !oidcContains(oidcStrings(doc["id_token_signing_alg_values_supported"]), "RS256") {
		t.Errorf("algs=%v", doc["id_token_signing_alg_values_supported"])
	}
	if !oidcContains(oidcStrings(doc["subject_types_supported"]), "public") {
		t.Errorf("subject_types=%v", doc["subject_types_supported"])
	}
	scopes := oidcStrings(doc["scopes_supported"])
	for _, s := range []string{"openid", "profile", "email"} {
		if !oidcContains(scopes, s) {
			t.Errorf("scopes_supported missing %q: %v", s, scopes)
		}
	}
}

func TestJWKSEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewOAuthProviderHandler(nil, nil)
	h.SetOIDCKeyService(newTestOIDCKeys(t))
	r := gin.New()
	r.GET("/.well-known/jwks.json", h.JWKS)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, priv := range []string{`"d"`, `"p"`, `"q"`, `"dp"`, `"dq"`, `"qi"`} {
		if strings.Contains(body, priv) {
			t.Errorf("JWKS leaked private field %s", priv)
		}
	}
	var set service.JWKS
	if err := json.Unmarshal(w.Body.Bytes(), &set); err != nil {
		t.Fatal(err)
	}
	if len(set.Keys) != 1 || set.Keys[0].Kid == "" || set.Keys[0].Kty != "RSA" || set.Keys[0].Alg != "RS256" {
		t.Errorf("unexpected jwks: %+v", set)
	}
}

func TestJWKSUnavailableWithoutKeyService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewOAuthProviderHandler(nil, nil)
	r := gin.New()
	r.GET("/.well-known/jwks.json", h.JWKS)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("want 503 when key service absent, got %d", w.Code)
	}
}

func oidcStrings(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func oidcContains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
