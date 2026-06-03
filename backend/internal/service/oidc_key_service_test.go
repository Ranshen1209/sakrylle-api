package service

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// memKeyStore is an in-memory OIDCKeyStore for tests.
type memKeyStore struct{ m map[string]string }

func newMemKeyStore() *memKeyStore { return &memKeyStore{m: map[string]string{}} }

func (s *memKeyStore) Get(_ context.Context, k string) (string, bool, error) {
	v, ok := s.m[k]
	return v, ok, nil
}
func (s *memKeyStore) Put(_ context.Context, k, v string) error { s.m[k] = v; return nil }

// b64Encryptor is a reversible stand-in for the production AES-256-GCM
// encryptor. It transforms the plaintext (so a stored value is never raw PEM),
// which is all the key-service unit needs; real AES is covered by
// aes_encryptor_test.go.
type b64Encryptor struct{}

func (b64Encryptor) Encrypt(p string) (string, error) {
	return "enc:" + base64.StdEncoding.EncodeToString([]byte(p)), nil
}
func (b64Encryptor) Decrypt(c string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(c, "enc:"))
	return string(b), err
}

func TestOIDCKeyService_EnsureAndSign(t *testing.T) {
	store := newMemKeyStore()
	svc := NewOIDCKeyService(store, b64Encryptor{})
	if err := svc.EnsureKey(context.Background()); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}
	kid := svc.CurrentKID()
	if kid == "" {
		t.Fatal("CurrentKID must be non-empty after EnsureKey")
	}

	claims := jwt.MapClaims{"iss": testIssuer, "sub": "1", "aud": "c", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()}
	tokenStr, err := svc.Sign(claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	jwks, err := svc.PublicJWKS()
	if err != nil {
		t.Fatalf("PublicJWKS: %v", err)
	}
	if len(jwks.Keys) != 1 {
		t.Fatalf("expected 1 JWK, got %d", len(jwks.Keys))
	}
	jwk := jwks.Keys[0]
	if jwk.Kty != "RSA" || jwk.Alg != "RS256" || jwk.Use != "sig" {
		t.Errorf("JWK header wrong: kty=%s alg=%s use=%s", jwk.Kty, jwk.Alg, jwk.Use)
	}
	if jwk.Kid != kid {
		t.Errorf("JWK kid %q != CurrentKID %q", jwk.Kid, kid)
	}
	if jwk.N == "" || jwk.E == "" {
		t.Error("JWK must expose modulus n and exponent e")
	}

	pub := pubKeyFromJWK(t, jwk)
	parsed, err := jwt.Parse(tokenStr, func(tok *jwt.Token) (any, error) {
		return pub, nil
	}, jwt.WithValidMethods([]string{"RS256"}))
	if err != nil || !parsed.Valid {
		t.Fatalf("token must verify against published JWK: err=%v", err)
	}
	if parsed.Header["kid"] != kid {
		t.Errorf("token header kid %v != %q", parsed.Header["kid"], kid)
	}
	if parsed.Header["alg"] != "RS256" {
		t.Errorf("token alg must be RS256, got %v", parsed.Header["alg"])
	}
}

// The published JWKS must contain only PUBLIC key material — no d/p/q/dp/dq/qi.
func TestOIDCKeyService_JWKSHasNoPrivateFields(t *testing.T) {
	store := newMemKeyStore()
	svc := NewOIDCKeyService(store, b64Encryptor{})
	if err := svc.EnsureKey(context.Background()); err != nil {
		t.Fatal(err)
	}
	jwks, err := svc.PublicJWKS()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(jwks)
	for _, priv := range []string{"\"d\"", "\"p\"", "\"q\"", "\"dp\"", "\"dq\"", "\"qi\""} {
		if strings.Contains(string(raw), priv) {
			t.Errorf("JWKS leaked private RSA field %s: %s", priv, raw)
		}
	}
}

// The stored secret value must be the encrypted form, never raw PEM.
func TestOIDCKeyService_PrivateKeyStoredEncrypted(t *testing.T) {
	store := newMemKeyStore()
	svc := NewOIDCKeyService(store, b64Encryptor{})
	if err := svc.EnsureKey(context.Background()); err != nil {
		t.Fatal(err)
	}
	for k, v := range store.m {
		if strings.Contains(v, "PRIVATE KEY") || strings.Contains(v, "BEGIN ") {
			t.Errorf("stored secret %q appears to hold raw PEM (not encrypted): %q", k, v)
		}
	}
}

// A second service over the same store must load the existing key, not mint a
// new one (kid stability across restarts).
func TestOIDCKeyService_PersistsAndReloads(t *testing.T) {
	store := newMemKeyStore()
	svc1 := NewOIDCKeyService(store, b64Encryptor{})
	if err := svc1.EnsureKey(context.Background()); err != nil {
		t.Fatal(err)
	}
	kid1 := svc1.CurrentKID()

	svc2 := NewOIDCKeyService(store, b64Encryptor{})
	if err := svc2.EnsureKey(context.Background()); err != nil {
		t.Fatal(err)
	}
	if svc2.CurrentKID() != kid1 {
		t.Errorf("reloaded kid %q != original %q (key should not regenerate)", svc2.CurrentKID(), kid1)
	}
}

func pubKeyFromJWK(t *testing.T, jwk JWK) *rsa.PublicKey {
	t.Helper()
	nBytes, err := base64.RawURLEncoding.DecodeString(jwk.N)
	if err != nil {
		t.Fatalf("decode n: %v", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(jwk.E)
	if err != nil {
		t.Fatalf("decode e: %v", err)
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(new(big.Int).SetBytes(eBytes).Int64())}
}
