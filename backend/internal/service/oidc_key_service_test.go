package service

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
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
func (s *memKeyStore) Delete(_ context.Context, k string) error { delete(s.m, k); return nil }

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
	ctx := context.Background()
	store := newMemKeyStore()
	svc := NewOIDCKeyService(store, b64Encryptor{})
	if err := svc.EnsureKey(ctx); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}
	kid := svc.CurrentKID()
	if kid == "" {
		t.Fatal("CurrentKID must be non-empty after EnsureKey")
	}

	claims := jwt.MapClaims{"iss": "test", "sub": "1", "aud": "c", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()}
	tokenStr, err := svc.Sign(claims, SigningAlgRS256)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	jwks, err := svc.PublicJWKS(ctx)
	if err != nil {
		t.Fatalf("PublicJWKS: %v", err)
	}
	// Should have at least 1 RSA key, possibly also EC key
	rsaKeys := 0
	var rsaJWK *JWK
	for i := range jwks.Keys {
		if jwks.Keys[i].Kty == "RSA" {
			rsaKeys++
			rsaJWK = &jwks.Keys[i]
		}
	}
	if rsaKeys < 1 {
		t.Fatalf("expected at least 1 RSA JWK, got %d", rsaKeys)
	}
	jwk := *rsaJWK
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
	ctx := context.Background()
	store := newMemKeyStore()
	svc := NewOIDCKeyService(store, b64Encryptor{})
	if err := svc.EnsureKey(ctx); err != nil {
		t.Fatal(err)
	}
	jwks, err := svc.PublicJWKS(ctx)
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
	ctx := context.Background()
	store := newMemKeyStore()
	svc := NewOIDCKeyService(store, b64Encryptor{})
	if err := svc.EnsureKey(ctx); err != nil {
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
	ctx := context.Background()
	store := newMemKeyStore()
	svc1 := NewOIDCKeyService(store, b64Encryptor{})
	if err := svc1.EnsureKey(ctx); err != nil {
		t.Fatal(err)
	}
	kid1 := svc1.CurrentKID()

	svc2 := NewOIDCKeyService(store, b64Encryptor{})
	if err := svc2.EnsureKey(ctx); err != nil {
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

// TestOIDCKeyService_RotateKey verifies that key rotation generates a new key,
// moves the old key to previous keys list, and both keys appear in JWKS.
func TestOIDCKeyService_RotateKey(t *testing.T) {
	ctx := context.Background()
	store := newMemKeyStore()
	svc := NewOIDCKeyService(store, b64Encryptor{})

	// Initialize with first key
	if err := svc.EnsureKey(ctx); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}
	oldKID := svc.CurrentKID()
	if oldKID == "" {
		t.Fatal("CurrentKID must be non-empty after EnsureKey")
	}

	// Sign a token with the old key
	claims := jwt.MapClaims{"iss": "test", "sub": "1", "aud": "c", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()}
	oldToken, err := svc.Sign(claims, SigningAlgRS256)
	if err != nil {
		t.Fatalf("Sign with old key: %v", err)
	}

	// Rotate to new key
	if err := svc.RotateKey(ctx); err != nil {
		t.Fatalf("RotateKey: %v", err)
	}

	newKID := svc.CurrentKID()
	if newKID == "" {
		t.Fatal("CurrentKID must be non-empty after rotation")
	}
	if newKID == oldKID {
		t.Error("RotateKey must generate a new KID, but it's the same as old")
	}

	// Sign a token with the new key
	newToken, err := svc.Sign(claims, SigningAlgRS256)
	if err != nil {
		t.Fatalf("Sign with new key: %v", err)
	}

	// JWKS should contain both keys during grace period
	jwks, err := svc.PublicJWKS(ctx)
	if err != nil {
		t.Fatalf("PublicJWKS: %v", err)
	}

	// Should have at least 2 RSA keys (old + new) plus possibly EC key
	rsaKeys := 0
	var oldJWK, newJWK *JWK
	for i := range jwks.Keys {
		if jwks.Keys[i].Kty == "RSA" {
			rsaKeys++
			if jwks.Keys[i].Kid == oldKID {
				oldJWK = &jwks.Keys[i]
			}
			if jwks.Keys[i].Kid == newKID {
				newJWK = &jwks.Keys[i]
			}
		}
	}

	if rsaKeys < 2 {
		t.Errorf("expected at least 2 RSA keys in JWKS during grace period, got %d", rsaKeys)
	}
	if oldJWK == nil {
		t.Error("old key missing from JWKS during grace period")
	}
	if newJWK == nil {
		t.Error("new key missing from JWKS")
	}

	// Verify old token still validates against JWKS
	if oldJWK != nil {
		oldPub := pubKeyFromJWK(t, *oldJWK)
		parsed, err := jwt.Parse(oldToken, func(tok *jwt.Token) (any, error) {
			return oldPub, nil
		}, jwt.WithValidMethods([]string{"RS256"}))
		if err != nil || !parsed.Valid {
			t.Errorf("old token must verify against old key in JWKS: err=%v", err)
		}
	}

	// Verify new token validates against JWKS
	if newJWK != nil {
		newPub := pubKeyFromJWK(t, *newJWK)
		parsed, err := jwt.Parse(newToken, func(tok *jwt.Token) (any, error) {
			return newPub, nil
		}, jwt.WithValidMethods([]string{"RS256"}))
		if err != nil || !parsed.Valid {
			t.Errorf("new token must verify against new key in JWKS: err=%v", err)
		}
	}

	// Verify previous KIDs list
	prevKIDs, err := svc.GetPreviousKIDs(ctx)
	if err != nil {
		t.Fatalf("GetPreviousKIDs: %v", err)
	}
	if len(prevKIDs) != 1 {
		t.Errorf("expected 1 previous KID, got %d", len(prevKIDs))
	}
	if len(prevKIDs) > 0 && prevKIDs[0] != oldKID {
		t.Errorf("previous KID %q != old KID %q", prevKIDs[0], oldKID)
	}
}

// TestOIDCKeyService_CleanupExpiredKeys verifies that keys beyond grace period
// are deleted from store and removed from previous kids list.
func TestOIDCKeyService_CleanupExpiredKeys(t *testing.T) {
	ctx := context.Background()
	store := newMemKeyStore()
	svc := NewOIDCKeyService(store, b64Encryptor{})

	// Set a short grace period for testing (1 second)
	if err := store.Put(ctx, oidcGracePeriodTTLKey, "1"); err != nil {
		t.Fatalf("set grace period: %v", err)
	}

	// Initialize and rotate
	if err := svc.EnsureKey(ctx); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}
	oldKID := svc.CurrentKID()

	if err := svc.RotateKey(ctx); err != nil {
		t.Fatalf("RotateKey: %v", err)
	}
	newKID := svc.CurrentKID()

	// Verify old key exists in store
	oldKeyPath := oidcKeyPrefix + "rsa_" + oldKID
	if _, found, _ := store.Get(ctx, oldKeyPath); !found {
		t.Error("old key should exist in store after rotation")
	}

	// Wait for grace period to expire
	time.Sleep(1100 * time.Millisecond)

	// Cleanup expired keys
	deleted, err := svc.CleanupExpiredKeys(ctx)
	if err != nil {
		t.Fatalf("CleanupExpiredKeys: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 key deleted, got %d", deleted)
	}

	// Verify old key no longer in store
	if _, found, _ := store.Get(ctx, oldKeyPath); found {
		t.Error("old key should be deleted from store after cleanup")
	}

	// Verify new key still exists
	newKeyPath := oidcKeyPrefix + "rsa_" + newKID
	if _, found, _ := store.Get(ctx, newKeyPath); !found {
		t.Error("new key should still exist in store after cleanup")
	}

	// Verify previous KIDs list is empty
	prevKIDs, err := svc.GetPreviousKIDs(ctx)
	if err != nil {
		t.Fatalf("GetPreviousKIDs: %v", err)
	}
	if len(prevKIDs) != 0 {
		t.Errorf("expected 0 previous KIDs after cleanup, got %d", len(prevKIDs))
	}

	// JWKS should only contain current key
	jwks, err := svc.PublicJWKS(ctx)
	if err != nil {
		t.Fatalf("PublicJWKS: %v", err)
	}
	rsaKeys := 0
	for _, jwk := range jwks.Keys {
		if jwk.Kty == "RSA" {
			rsaKeys++
			if jwk.Kid == oldKID {
				t.Error("old key should not be in JWKS after grace period expired")
			}
		}
	}
	if rsaKeys < 1 {
		t.Error("JWKS should still contain current RSA key")
	}
}

// TestOIDCKeyService_MultipleRotations verifies that multiple rotations work
// correctly and expired keys are cleaned up while recent keys are retained.
func TestOIDCKeyService_MultipleRotations(t *testing.T) {
	ctx := context.Background()
	store := newMemKeyStore()
	svc := NewOIDCKeyService(store, b64Encryptor{})

	// Set grace period to 2 seconds
	if err := store.Put(ctx, oidcGracePeriodTTLKey, "2"); err != nil {
		t.Fatalf("set grace period: %v", err)
	}

	// Initialize
	if err := svc.EnsureKey(ctx); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}
	kid1 := svc.CurrentKID()

	// First rotation
	if err := svc.RotateKey(ctx); err != nil {
		t.Fatalf("RotateKey 1: %v", err)
	}
	kid2 := svc.CurrentKID()

	// Wait 1 second
	time.Sleep(1 * time.Second)

	// Second rotation
	if err := svc.RotateKey(ctx); err != nil {
		t.Fatalf("RotateKey 2: %v", err)
	}
	kid3 := svc.CurrentKID()

	// Should have 2 previous keys
	prevKIDs, err := svc.GetPreviousKIDs(ctx)
	if err != nil {
		t.Fatalf("GetPreviousKIDs: %v", err)
	}
	if len(prevKIDs) != 2 {
		t.Errorf("expected 2 previous KIDs, got %d", len(prevKIDs))
	}

	// JWKS should have 3 RSA keys (kid1, kid2, kid3) plus possibly EC
	jwks, err := svc.PublicJWKS(ctx)
	if err != nil {
		t.Fatalf("PublicJWKS: %v", err)
	}
	rsaKeys := 0
	for _, jwk := range jwks.Keys {
		if jwk.Kty == "RSA" {
			rsaKeys++
		}
	}
	if rsaKeys != 3 {
		t.Errorf("expected 3 RSA keys in JWKS, got %d", rsaKeys)
	}

	// Wait for first key to expire (total 2.1 seconds from rotation 1)
	time.Sleep(1100 * time.Millisecond)

	// Cleanup should remove kid1 but keep kid2
	deleted, err := svc.CleanupExpiredKeys(ctx)
	if err != nil {
		t.Fatalf("CleanupExpiredKeys: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 key deleted, got %d", deleted)
	}

	// Verify kid1 removed but kid2 and kid3 remain
	if _, found, _ := store.Get(ctx, oidcKeyPrefix+"rsa_"+kid1); found {
		t.Error("kid1 should be deleted")
	}
	if _, found, _ := store.Get(ctx, oidcKeyPrefix+"rsa_"+kid2); !found {
		t.Error("kid2 should still exist")
	}
	if _, found, _ := store.Get(ctx, oidcKeyPrefix+"rsa_"+kid3); !found {
		t.Error("kid3 should still exist")
	}

	// Previous KIDs should only have kid2
	prevKIDs, err = svc.GetPreviousKIDs(ctx)
	if err != nil {
		t.Fatalf("GetPreviousKIDs after cleanup: %v", err)
	}
	if len(prevKIDs) != 1 {
		t.Errorf("expected 1 previous KID after cleanup, got %d", len(prevKIDs))
	}
	if len(prevKIDs) > 0 && prevKIDs[0] != kid2 {
		t.Errorf("expected previous KID to be kid2, got %q", prevKIDs[0])
	}
}

// TestOIDCKeyService_SignatureVerificationDuringGracePeriod verifies that
// tokens signed before rotation can still be verified during grace period.
func TestOIDCKeyService_SignatureVerificationDuringGracePeriod(t *testing.T) {
	ctx := context.Background()
	store := newMemKeyStore()
	svc := NewOIDCKeyService(store, b64Encryptor{})

	if err := svc.EnsureKey(ctx); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}

	// Sign 5 tokens with the original key
	claims := jwt.MapClaims{"iss": "test", "sub": "user", "aud": "client", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()}
	var tokensBeforeRotation []string
	for i := 0; i < 5; i++ {
		token, err := svc.Sign(claims, SigningAlgRS256)
		if err != nil {
			t.Fatalf("Sign token %d: %v", i, err)
		}
		tokensBeforeRotation = append(tokensBeforeRotation, token)
	}

	// Rotate key
	if err := svc.RotateKey(ctx); err != nil {
		t.Fatalf("RotateKey: %v", err)
	}

	// Get JWKS (should contain both keys)
	jwks, err := svc.PublicJWKS(ctx)
	if err != nil {
		t.Fatalf("PublicJWKS: %v", err)
	}

	// Build key map for verification
	keyMap := make(map[string]*rsa.PublicKey)
	for _, jwk := range jwks.Keys {
		if jwk.Kty == "RSA" {
			keyMap[jwk.Kid] = pubKeyFromJWK(t, jwk)
		}
	}

	// All old tokens should verify
	for i, token := range tokensBeforeRotation {
		parsed, err := jwt.Parse(token, func(tok *jwt.Token) (any, error) {
			kid, ok := tok.Header["kid"].(string)
			if !ok {
				return nil, fmt.Errorf("missing kid header")
			}
			pub, found := keyMap[kid]
			if !found {
				return nil, fmt.Errorf("key %q not in JWKS", kid)
			}
			return pub, nil
		}, jwt.WithValidMethods([]string{"RS256"}))

		if err != nil || !parsed.Valid {
			t.Errorf("token %d signed before rotation failed verification: %v", i, err)
		}
	}

	// New tokens should also verify
	newToken, err := svc.Sign(claims, SigningAlgRS256)
	if err != nil {
		t.Fatalf("Sign new token: %v", err)
	}

	parsed, err := jwt.Parse(newToken, func(tok *jwt.Token) (any, error) {
		kid, ok := tok.Header["kid"].(string)
		if !ok {
			return nil, fmt.Errorf("missing kid header")
		}
		pub, found := keyMap[kid]
		if !found {
			return nil, fmt.Errorf("key %q not in JWKS", kid)
		}
		return pub, nil
	}, jwt.WithValidMethods([]string{"RS256"}))

	if err != nil || !parsed.Valid {
		t.Errorf("new token failed verification: %v", err)
	}
}

// TestOIDCKeyService_PersistenceAcrossRestarts verifies that previous keys
// are correctly reloaded after service restart.
func TestOIDCKeyService_PersistenceAcrossRestarts(t *testing.T) {
	ctx := context.Background()
	store := newMemKeyStore()

	// First service instance
	svc1 := NewOIDCKeyService(store, b64Encryptor{})
	if err := svc1.EnsureKey(ctx); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}
	kid1 := svc1.CurrentKID()

	// Rotate key
	if err := svc1.RotateKey(ctx); err != nil {
		t.Fatalf("RotateKey: %v", err)
	}
	kid2 := svc1.CurrentKID()

	// "Restart" - create new service instance with same store
	svc2 := NewOIDCKeyService(store, b64Encryptor{})
	if err := svc2.EnsureKey(ctx); err != nil {
		t.Fatalf("EnsureKey after restart: %v", err)
	}

	// Current KID should match
	if svc2.CurrentKID() != kid2 {
		t.Errorf("current KID after restart %q != %q", svc2.CurrentKID(), kid2)
	}

	// Previous KIDs should be reloaded
	prevKIDs, err := svc2.GetPreviousKIDs(ctx)
	if err != nil {
		t.Fatalf("GetPreviousKIDs after restart: %v", err)
	}
	if len(prevKIDs) != 1 || prevKIDs[0] != kid1 {
		t.Errorf("previous KIDs not correctly reloaded: got %v, want [%s]", prevKIDs, kid1)
	}

	// JWKS should contain both keys
	jwks, err := svc2.PublicJWKS(ctx)
	if err != nil {
		t.Fatalf("PublicJWKS after restart: %v", err)
	}
	rsaKeys := 0
	hasKid1, hasKid2 := false, false
	for _, jwk := range jwks.Keys {
		if jwk.Kty == "RSA" {
			rsaKeys++
			if jwk.Kid == kid1 {
				hasKid1 = true
			}
			if jwk.Kid == kid2 {
				hasKid2 = true
			}
		}
	}
	if rsaKeys < 2 {
		t.Errorf("expected at least 2 RSA keys after restart, got %d", rsaKeys)
	}
	if !hasKid1 {
		t.Error("kid1 missing from JWKS after restart")
	}
	if !hasKid2 {
		t.Error("kid2 missing from JWKS after restart")
	}
}

// TestOIDCKeyService_RotateWithoutInitialKey verifies that rotation fails
// gracefully when called before EnsureKey.
func TestOIDCKeyService_RotateWithoutInitialKey(t *testing.T) {
	ctx := context.Background()
	store := newMemKeyStore()
	svc := NewOIDCKeyService(store, b64Encryptor{})

	// Attempt rotation without initializing
	err := svc.RotateKey(ctx)
	if err == nil {
		t.Error("RotateKey should fail when no current key exists")
	}
	if !strings.Contains(err.Error(), "signing key not loaded") {
		t.Errorf("expected 'signing key not loaded' error, got: %v", err)
	}
}

// TestOIDCKeyService_CleanupWithNoExpiredKeys verifies cleanup is safe when
// no keys have expired.
func TestOIDCKeyService_CleanupWithNoExpiredKeys(t *testing.T) {
	ctx := context.Background()
	store := newMemKeyStore()
	svc := NewOIDCKeyService(store, b64Encryptor{})

	// Initialize and rotate
	if err := svc.EnsureKey(ctx); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}
	if err := svc.RotateKey(ctx); err != nil {
		t.Fatalf("RotateKey: %v", err)
	}

	// Cleanup immediately (nothing should be expired)
	deleted, err := svc.CleanupExpiredKeys(ctx)
	if err != nil {
		t.Fatalf("CleanupExpiredKeys: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 keys deleted, got %d", deleted)
	}

	// Previous keys should still exist
	prevKIDs, err := svc.GetPreviousKIDs(ctx)
	if err != nil {
		t.Fatalf("GetPreviousKIDs: %v", err)
	}
	if len(prevKIDs) != 1 {
		t.Errorf("expected 1 previous KID, got %d", len(prevKIDs))
	}
}

// TestOIDCKeyService_CleanupWithNoKeys verifies cleanup is safe when service
// has no keys at all.
func TestOIDCKeyService_CleanupWithNoKeys(t *testing.T) {
	ctx := context.Background()
	store := newMemKeyStore()
	svc := NewOIDCKeyService(store, b64Encryptor{})

	// Cleanup without any keys
	deleted, err := svc.CleanupExpiredKeys(ctx)
	if err != nil {
		t.Fatalf("CleanupExpiredKeys should not error with no keys: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 keys deleted, got %d", deleted)
	}
}

// TestOIDCKeyService_GracePeriodCustomization verifies that grace period TTL
// can be configured via store.
func TestOIDCKeyService_GracePeriodCustomization(t *testing.T) {
	tests := []struct {
		name           string
		gracePeriodTTL string
		wantSeconds    int64
	}{
		{"default 24h", "", 86400},
		{"custom 1h", "3600", 3600},
		{"custom 30m", "1800", 1800},
		{"custom 1 week", "604800", 604800},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			store := newMemKeyStore()

			if tt.gracePeriodTTL != "" {
				if err := store.Put(ctx, oidcGracePeriodTTLKey, tt.gracePeriodTTL); err != nil {
					t.Fatalf("set grace period: %v", err)
				}
			}

			svc := NewOIDCKeyService(store, b64Encryptor{})
			if err := svc.EnsureKey(ctx); err != nil {
				t.Fatalf("EnsureKey: %v", err)
			}
			if err := svc.RotateKey(ctx); err != nil {
				t.Fatalf("RotateKey: %v", err)
			}

			// Check the stored timestamp format
			raw, found, err := store.Get(ctx, oidcPreviousKIDsKey)
			if err != nil || !found {
				t.Fatalf("failed to get previous KIDs: err=%v, found=%v", err, found)
			}

			var prevs []previousKIDEntry
			if err := json.Unmarshal([]byte(raw), &prevs); err != nil {
				t.Fatalf("unmarshal previous keys: %v", err)
			}
			if len(prevs) != 1 {
				t.Fatalf("expected 1 previous key record, got %d", len(prevs))
			}

			// Verify timestamp is recent
			retiredAt := prevs[0].RetiredAt
			if time.Since(retiredAt) > 5*time.Second {
				t.Errorf("retirement timestamp too old: %v", retiredAt)
			}
		})
	}
}

// TestOIDCKeyService_ConcurrentRotation verifies that concurrent rotation
// calls are safe (idempotency / race conditions).
func TestOIDCKeyService_ConcurrentRotation(t *testing.T) {
	ctx := context.Background()
	store := newMemKeyStore()
	svc := NewOIDCKeyService(store, b64Encryptor{})

	if err := svc.EnsureKey(ctx); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}

	// Launch 5 concurrent rotations
	errCh := make(chan error, 5)
	for i := 0; i < 5; i++ {
		go func() {
			errCh <- svc.RotateKey(ctx)
		}()
	}

	// Collect results
	successCount := 0
	for i := 0; i < 5; i++ {
		if err := <-errCh; err == nil {
			successCount++
		}
	}

	// At least one should succeed (not enforcing exactly 1 because of timing)
	if successCount == 0 {
		t.Error("expected at least one concurrent rotation to succeed")
	}

	// Should have at least 1 previous key (the original)
	prevKIDs, err := svc.GetPreviousKIDs(ctx)
	if err != nil {
		t.Fatalf("GetPreviousKIDs: %v", err)
	}
	if len(prevKIDs) == 0 {
		t.Error("expected at least 1 previous KID after concurrent rotations")
	}
}

// TestOIDCKeyService_ConcurrentRotateAndCleanup exercises rotation and cleanup
// running concurrently to surface the TOCTOU/data race on the shared
// previous-kids store list (Get->filter->Put in cleanupExpiredKeysFor vs the
// append in RotateKey). With -race this fails if the full store RMW is not
// serialized under s.mu. Regression guard for fix #5.
func TestOIDCKeyService_ConcurrentRotateAndCleanup(t *testing.T) {
	ctx := context.Background()
	store := newMemKeyStore()
	svc := NewOIDCKeyService(store, b64Encryptor{})
	if err := svc.EnsureKey(ctx); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}
	// Grace period 0 makes every cleanup actively rewrite/delete the shared
	// previous-kids store list, maximizing overlap with rotation's append on
	// that same key so the unserialized Get->filter->Put race surfaces.
	if err := store.Put(ctx, oidcGracePeriodTTLKey, "0"); err != nil {
		t.Fatalf("seed grace period ttl: %v", err)
	}

	var wg sync.WaitGroup
	var done int32
	start := make(chan struct{})

	// Rotation writers: each performs several rotations, appending to the
	// shared previous-kids store list under s.mu.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 25; j++ {
				_ = svc.RotateKey(ctx)
			}
			atomic.StoreInt32(&done, 1)
		}()
	}
	// Cleanup readers/writers: hammer the same store list (Get->filter->Put)
	// without s.mu until rotations finish, maximizing overlap so the
	// unserialized RMW surfaces as a data race under -race.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for atomic.LoadInt32(&done) == 0 {
				_, _ = svc.CleanupExpiredKeys(ctx)
			}
		}()
	}
	close(start) // release all goroutines together to maximize overlap
	wg.Wait()

	// JWKS must still load cleanly after the concurrent churn.
	if _, err := svc.PublicJWKS(ctx); err != nil {
		t.Fatalf("JWKS after concurrent rotate/cleanup: %v", err)
	}
}

// TestOIDCKeyService_PreviousKIDsOrder verifies that previous KIDs are returned
// in order (most recent first).
func TestOIDCKeyService_PreviousKIDsOrder(t *testing.T) {
	ctx := context.Background()
	store := newMemKeyStore()
	svc := NewOIDCKeyService(store, b64Encryptor{})

	if err := svc.EnsureKey(ctx); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}
	kid1 := svc.CurrentKID()

	// Wait and rotate to ensure different timestamps
	time.Sleep(10 * time.Millisecond)
	if err := svc.RotateKey(ctx); err != nil {
		t.Fatalf("RotateKey 1: %v", err)
	}
	kid2 := svc.CurrentKID()

	time.Sleep(10 * time.Millisecond)
	if err := svc.RotateKey(ctx); err != nil {
		t.Fatalf("RotateKey 2: %v", err)
	}

	prevKIDs, err := svc.GetPreviousKIDs(ctx)
	if err != nil {
		t.Fatalf("GetPreviousKIDs: %v", err)
	}
	if len(prevKIDs) != 2 {
		t.Fatalf("expected 2 previous KIDs, got %d", len(prevKIDs))
	}

	// Previous KIDs are stored oldest first.
	if prevKIDs[0] != kid1 {
		t.Errorf("expected oldest previous KID to be %q, got %q", kid1, prevKIDs[0])
	}
	if prevKIDs[1] != kid2 {
		t.Errorf("expected most recent previous KID to be %q, got %q", kid2, prevKIDs[1])
	}
}

// TestOIDCKeyService_RotationPreservesECKey verifies that EC key (if present)
// remains in JWKS after RSA rotation.
func TestOIDCKeyService_RotationPreservesECKey(t *testing.T) {
	ctx := context.Background()
	store := newMemKeyStore()
	svc := NewOIDCKeyService(store, b64Encryptor{})

	if err := svc.EnsureKey(ctx); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}

	// Get initial JWKS
	jwksBefore, err := svc.PublicJWKS(ctx)
	if err != nil {
		t.Fatalf("PublicJWKS before: %v", err)
	}

	// Find EC key if present
	var ecKeyBefore *JWK
	for i, jwk := range jwksBefore.Keys {
		if jwk.Kty == "EC" {
			ecKeyBefore = &jwksBefore.Keys[i]
			break
		}
	}

	// Rotate RSA key
	if err := svc.RotateKey(ctx); err != nil {
		t.Fatalf("RotateKey: %v", err)
	}

	// Get JWKS after rotation
	jwksAfter, err := svc.PublicJWKS(ctx)
	if err != nil {
		t.Fatalf("PublicJWKS after: %v", err)
	}

	// If EC key existed before, it should still exist
	if ecKeyBefore != nil {
		found := false
		for _, jwk := range jwksAfter.Keys {
			if jwk.Kty == "EC" && jwk.Kid == ecKeyBefore.Kid {
				found = true
				break
			}
		}
		if !found {
			t.Error("EC key disappeared after RSA rotation")
		}
	}
}

// TestOIDCKeyService_InvalidGracePeriodTTL verifies handling of invalid
// grace period configuration.
func TestOIDCKeyService_InvalidGracePeriodTTL(t *testing.T) {
	tests := []struct {
		name      string
		ttlValue  string
		wantError bool
	}{
		{"negative value", "-100", false}, // treated as 0, immediate expiry
		{"zero value", "0", false},        // valid, immediate expiry
		{"non-numeric", "invalid", false}, // falls back to default
		{"empty string", "", false},       // falls back to default
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			store := newMemKeyStore()

			if err := store.Put(ctx, oidcGracePeriodTTLKey, tt.ttlValue); err != nil {
				t.Fatalf("set grace period: %v", err)
			}

			svc := NewOIDCKeyService(store, b64Encryptor{})
			err := svc.EnsureKey(ctx)
			if tt.wantError && err == nil {
				t.Error("expected error for invalid TTL")
			}
			if !tt.wantError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestOIDCKeyService_StorageFailureDuringRotation verifies error handling
// when storage operations fail.
func TestOIDCKeyService_StorageFailureDuringRotation(t *testing.T) {
	ctx := context.Background()

	// Store backed by an in-memory map; EnsureKey must succeed first so we have
	// a current key to rotate. failOnPut is flipped on only for the rotation.
	store := &failingStore{
		memKeyStore: newMemKeyStore(),
	}

	svc := NewOIDCKeyService(store, b64Encryptor{})
	if err := svc.EnsureKey(ctx); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}

	// Capture pre-rotation state so we can assert rotation left it intact.
	kidBefore := svc.CurrentKID()
	prevBefore, err := svc.GetPreviousKIDs(ctx)
	if err != nil {
		t.Fatalf("GetPreviousKIDs before rotation: %v", err)
	}

	// Rotation should fail cleanly when the store rejects the new key Put.
	store.failOnPut = true
	if err := svc.RotateKey(ctx); err == nil {
		t.Error("expected rotation to fail with storage error")
	}

	// Fail-clean invariant: a failed rotation must not advance the current kid
	// or leak a half-written previous-kids entry into in-memory state.
	if got := svc.CurrentKID(); got != kidBefore {
		t.Errorf("current kid changed after failed rotation: got %q, want %q", got, kidBefore)
	}
	prevAfter, err := svc.GetPreviousKIDs(ctx)
	if err != nil {
		t.Fatalf("GetPreviousKIDs after rotation: %v", err)
	}
	if len(prevAfter) != len(prevBefore) {
		t.Errorf("previous kids changed after failed rotation: got %v, want %v", prevAfter, prevBefore)
	}
}

// TestOIDCKeyService_EncryptionFailure verifies error handling when encryption
// fails during key generation.
func TestOIDCKeyService_EncryptionFailure(t *testing.T) {
	ctx := context.Background()
	store := newMemKeyStore()

	// Encryptor that fails
	failEnc := &failingEncryptor{fail: true}
	svc := NewOIDCKeyService(store, failEnc)

	// Should fail to ensure key
	err := svc.EnsureKey(ctx)
	if err == nil {
		t.Error("expected EnsureKey to fail with encryption error")
	}
}

// TestOIDCKeyService_DecryptionFailure verifies error handling when decryption
// fails during key loading.
func TestOIDCKeyService_DecryptionFailure(t *testing.T) {
	ctx := context.Background()
	store := newMemKeyStore()

	// Create key with working encryptor
	svc1 := NewOIDCKeyService(store, b64Encryptor{})
	if err := svc1.EnsureKey(ctx); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}

	// Try to load with failing decryptor
	failDec := &failingEncryptor{fail: true}
	svc2 := NewOIDCKeyService(store, failDec)

	err := svc2.EnsureKey(ctx)
	if err == nil {
		t.Error("expected EnsureKey to fail with decryption error")
	}
}

// TestOIDCKeyService_KIDUniqueness verifies that generated KIDs are unique
// across multiple rotations.
func TestOIDCKeyService_KIDUniqueness(t *testing.T) {
	ctx := context.Background()
	store := newMemKeyStore()
	svc := NewOIDCKeyService(store, b64Encryptor{})

	if err := svc.EnsureKey(ctx); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}

	kids := map[string]bool{svc.CurrentKID(): true}

	// Generate 10 rotations
	for i := 0; i < 10; i++ {
		if err := svc.RotateKey(ctx); err != nil {
			t.Fatalf("RotateKey %d: %v", i, err)
		}
		kid := svc.CurrentKID()
		if kids[kid] {
			t.Errorf("duplicate KID generated: %q", kid)
		}
		kids[kid] = true
	}

	if len(kids) != 11 { // initial + 10 rotations
		t.Errorf("expected 11 unique KIDs, got %d", len(kids))
	}
}

// failingStore is a test store that can simulate failures.
type failingStore struct {
	*memKeyStore
	failOnPut    bool
	failOnGet    bool
	failOnDelete bool
}

func (s *failingStore) Put(ctx context.Context, k, v string) error {
	if s.failOnPut {
		return fmt.Errorf("simulated Put failure")
	}
	return s.memKeyStore.Put(ctx, k, v)
}

func (s *failingStore) Get(ctx context.Context, k string) (string, bool, error) {
	if s.failOnGet {
		return "", false, fmt.Errorf("simulated Get failure")
	}
	return s.memKeyStore.Get(ctx, k)
}

func (s *failingStore) Delete(ctx context.Context, k string) error {
	if s.failOnDelete {
		return fmt.Errorf("simulated Delete failure")
	}
	return s.memKeyStore.Delete(ctx, k)
}

// failingEncryptor is a test encryptor that can simulate failures.
type failingEncryptor struct {
	fail bool
}

func (e *failingEncryptor) Encrypt(plaintext string) (string, error) {
	if e.fail {
		return "", fmt.Errorf("simulated encryption failure")
	}
	return "enc:" + base64.StdEncoding.EncodeToString([]byte(plaintext)), nil
}

func (e *failingEncryptor) Decrypt(ciphertext string) (string, error) {
	if e.fail {
		return "", fmt.Errorf("simulated decryption failure")
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(ciphertext, "enc:"))
	return string(b), err
}

// TestOIDCKeyService_ES256_EnsureAndSign verifies ES256 key generation,
// signing, and JWKS publication.
func TestOIDCKeyService_ES256_EnsureAndSign(t *testing.T) {
	ctx := context.Background()
	store := newMemKeyStore()
	svc := NewOIDCKeyService(store, b64Encryptor{})

	if err := svc.EnsureKey(ctx); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}

	ecKID := svc.CurrentECKID()
	if ecKID == "" {
		t.Fatal("CurrentECKID must be non-empty after EnsureKey")
	}

	// Sign with ES256
	claims := jwt.MapClaims{
		"iss": "test",
		"sub": "1",
		"aud": "c",
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	tokenStr, err := svc.Sign(claims, SigningAlgES256)
	if err != nil {
		t.Fatalf("Sign ES256: %v", err)
	}

	// Get JWKS and find EC key
	jwks, err := svc.PublicJWKS(ctx)
	if err != nil {
		t.Fatalf("PublicJWKS: %v", err)
	}

	var ecJWK *JWK
	for i := range jwks.Keys {
		if jwks.Keys[i].Kty == "EC" {
			ecJWK = &jwks.Keys[i]
			break
		}
	}
	if ecJWK == nil {
		t.Fatal("expected EC JWK in key set")
	}

	// Verify JWK structure
	if ecJWK.Kty != "EC" {
		t.Errorf("JWK kty = %q, want EC", ecJWK.Kty)
	}
	if ecJWK.Alg != "ES256" {
		t.Errorf("JWK alg = %q, want ES256", ecJWK.Alg)
	}
	if ecJWK.Use != "sig" {
		t.Errorf("JWK use = %q, want sig", ecJWK.Use)
	}
	if ecJWK.Kid != ecKID {
		t.Errorf("JWK kid %q != CurrentECKID %q", ecJWK.Kid, ecKID)
	}
	if ecJWK.Crv != "P-256" {
		t.Errorf("JWK crv = %q, want P-256", ecJWK.Crv)
	}
	if ecJWK.X == "" || ecJWK.Y == "" {
		t.Error("JWK must expose x and y coordinates")
	}

	// Verify token structure
	parsed, _, err := new(jwt.Parser).ParseUnverified(tokenStr, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	if parsed.Header["kid"] != ecKID {
		t.Errorf("token header kid %v != %q", parsed.Header["kid"], ecKID)
	}
	if parsed.Header["alg"] != "ES256" {
		t.Errorf("token alg = %v, want ES256", parsed.Header["alg"])
	}
}

// TestOIDCKeyService_ES256_CoordinatePadding verifies that EC public key
// coordinates are properly padded to 32 bytes (256 bits) as required by P-256.
func TestOIDCKeyService_ES256_CoordinatePadding(t *testing.T) {
	ctx := context.Background()
	store := newMemKeyStore()
	svc := NewOIDCKeyService(store, b64Encryptor{})

	if err := svc.EnsureKey(ctx); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}

	jwks, err := svc.PublicJWKS(ctx)
	if err != nil {
		t.Fatalf("PublicJWKS: %v", err)
	}

	var ecJWK *JWK
	for i := range jwks.Keys {
		if jwks.Keys[i].Kty == "EC" {
			ecJWK = &jwks.Keys[i]
			break
		}
	}
	if ecJWK == nil {
		t.Fatal("expected EC JWK in key set")
	}

	// Decode coordinates
	xBytes, err := base64.RawURLEncoding.DecodeString(ecJWK.X)
	if err != nil {
		t.Fatalf("decode x: %v", err)
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(ecJWK.Y)
	if err != nil {
		t.Fatalf("decode y: %v", err)
	}

	// P-256 coordinates must be exactly 32 bytes
	if len(xBytes) != 32 {
		t.Errorf("x coordinate length = %d, want 32", len(xBytes))
	}
	if len(yBytes) != 32 {
		t.Errorf("y coordinate length = %d, want 32", len(yBytes))
	}
}

// TestOIDCKeyService_ES256_KIDStability verifies RFC 7638 thumbprint
// generation produces stable KIDs across restarts.
func TestOIDCKeyService_ES256_KIDStability(t *testing.T) {
	ctx := context.Background()
	store := newMemKeyStore()

	svc1 := NewOIDCKeyService(store, b64Encryptor{})
	if err := svc1.EnsureKey(ctx); err != nil {
		t.Fatal(err)
	}
	kid1 := svc1.CurrentECKID()

	// Reload with new service instance
	svc2 := NewOIDCKeyService(store, b64Encryptor{})
	if err := svc2.EnsureKey(ctx); err != nil {
		t.Fatal(err)
	}
	kid2 := svc2.CurrentECKID()

	if kid2 != kid1 {
		t.Errorf("reloaded EC KID %q != original %q (KID should be stable)", kid2, kid1)
	}
}

// TestOIDCKeyService_ES256_NoPrivateFields verifies that the published EC
// JWK contains only public key material (no private d component).
func TestOIDCKeyService_ES256_NoPrivateFields(t *testing.T) {
	ctx := context.Background()
	store := newMemKeyStore()
	svc := NewOIDCKeyService(store, b64Encryptor{})

	if err := svc.EnsureKey(ctx); err != nil {
		t.Fatal(err)
	}

	jwks, err := svc.PublicJWKS(ctx)
	if err != nil {
		t.Fatal(err)
	}

	raw, _ := json.Marshal(jwks)
	// EC private key field is "d"
	if strings.Contains(string(raw), "\"d\":") {
		t.Errorf("JWKS leaked private EC field d: %s", raw)
	}
}

// TestOIDCKeyService_DualKeyJWKS verifies that JWKS contains both RSA and EC
// keys for dual-algorithm support.
func TestOIDCKeyService_DualKeyJWKS(t *testing.T) {
	ctx := context.Background()
	store := newMemKeyStore()
	svc := NewOIDCKeyService(store, b64Encryptor{})

	if err := svc.EnsureKey(ctx); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}

	jwks, err := svc.PublicJWKS(ctx)
	if err != nil {
		t.Fatalf("PublicJWKS: %v", err)
	}

	rsaCount, ecCount := 0, 0
	for _, jwk := range jwks.Keys {
		switch jwk.Kty {
		case "RSA":
			rsaCount++
			if jwk.Alg != "RS256" {
				t.Errorf("RSA key has alg %q, want RS256", jwk.Alg)
			}
		case "EC":
			ecCount++
			if jwk.Alg != "ES256" {
				t.Errorf("EC key has alg %q, want ES256", jwk.Alg)
			}
		}
	}

	if rsaCount < 1 {
		t.Errorf("expected at least 1 RSA key, got %d", rsaCount)
	}
	if ecCount != 1 {
		t.Errorf("expected exactly 1 EC key, got %d", ecCount)
	}
}

// TestOIDCKeyService_DedupCurrentFromPrevious verifies the self-heal on load:
// if a prior rotation left the current kid lingering in the previous-kids list
// (pointer-flip failure), reloading the service must not surface that kid twice
// in JWKS.
func TestOIDCKeyService_DedupCurrentFromPrevious(t *testing.T) {
	ctx := context.Background()
	store := newMemKeyStore()

	svc1 := NewOIDCKeyService(store, b64Encryptor{})
	if err := svc1.EnsureKey(ctx); err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}
	curKID := svc1.CurrentKID()
	if curKID == "" {
		t.Fatal("CurrentKID empty after EnsureKey")
	}

	// Manufacture the dirty state: stuff the current kid into the previous-kids
	// list as if a rotation's pointer flip had failed.
	svc1.mu.Lock()
	if err := svc1.appendPreviousKID(ctx, oidcPreviousKIDsKey, curKID); err != nil {
		svc1.mu.Unlock()
		t.Fatalf("appendPreviousKID: %v", err)
	}
	svc1.mu.Unlock()

	// Reload from the same store: EnsureKey must drop the current kid from the
	// in-memory previous slice.
	svc2 := NewOIDCKeyService(store, b64Encryptor{})
	if err := svc2.EnsureKey(ctx); err != nil {
		t.Fatalf("EnsureKey reload: %v", err)
	}
	if svc2.CurrentKID() != curKID {
		t.Fatalf("CurrentKID after reload = %q, want %q", svc2.CurrentKID(), curKID)
	}

	jwks, err := svc2.PublicJWKS(ctx)
	if err != nil {
		t.Fatalf("PublicJWKS: %v", err)
	}
	count := 0
	for i := range jwks.Keys {
		if jwks.Keys[i].Kid == curKID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("current kid %q appears %d times in JWKS, want 1", curKID, count)
	}
}
