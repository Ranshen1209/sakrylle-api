package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── OIDC Integration Tests ──────────────────────────────────────────────────
//
// These tests verify end-to-end OIDC flows with real key generation, signing,
// and multi-algorithm support (RS256 + ES256). They use in-memory stores to
// avoid external dependencies.

// TestOIDCKeyRotation verifies key rotation with grace period support.
// During rotation, JWKS must include both current and previous keys so RPs
// can verify tokens signed with either key.
func TestOIDCKeyRotation(t *testing.T) {
	// Arrange
	store := newMemoryOIDCStore()
	enc := &passEncryptor{}
	svc := NewOIDCKeyService(store, enc)

	ctx := context.Background()

	// Act 1: Initialize with first key
	err := svc.EnsureKey(ctx)
	require.NoError(t, err, "initial key generation must succeed")

	firstKID := svc.CurrentKID()
	require.NotEmpty(t, firstKID, "first KID must be set")

	// Act 2: Sign a token with first key
	claims1 := jwt.MapClaims{
		"iss": "https://sub.sakrylle.com",
		"sub": "123",
		"aud": "test-client",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
		"iat": time.Now().Unix(),
	}
	token1, err := svc.Sign(claims1, SigningAlgRS256)
	require.NoError(t, err)
	require.NotEmpty(t, token1)

	// Verify token1 has first KID
	kid1 := extractKIDFromToken(t, token1)
	assert.Equal(t, firstKID, kid1, "token1 must be signed with first key")

	// Act 3: Rotate to second key
	err = svc.RotateKey(ctx)
	require.NoError(t, err, "key rotation must succeed")

	secondKID := svc.CurrentKID()
	require.NotEmpty(t, secondKID, "second KID must be set")
	assert.NotEqual(t, firstKID, secondKID, "second KID must differ from first")

	// Act 4: Sign a new token with second key
	claims2 := jwt.MapClaims{
		"iss": "https://sub.sakrylle.com",
		"sub": "456",
		"aud": "test-client",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
		"iat": time.Now().Unix(),
	}
	token2, err := svc.Sign(claims2, SigningAlgRS256)
	require.NoError(t, err)

	kid2 := extractKIDFromToken(t, token2)
	assert.Equal(t, secondKID, kid2, "token2 must be signed with second key")

	// Assert: JWKS must include both keys during grace period
	jwks, err := svc.PublicJWKS(ctx)
	require.NoError(t, err)

	// Should have at least 2 RSA keys (first + second) + 1 EC key = 3 total
	assert.GreaterOrEqual(t, len(jwks.Keys), 2, "JWKS must include previous key during grace period")

	// Verify both KIDs are present
	kids := make([]string, len(jwks.Keys))
	for i, key := range jwks.Keys {
		kids[i] = key.Kid
	}
	assert.Contains(t, kids, firstKID, "JWKS must include first KID")
	assert.Contains(t, kids, secondKID, "JWKS must include second KID")

	// Act 5: Load previous KIDs from store
	prevKIDs, err := svc.GetPreviousKIDs(ctx)
	require.NoError(t, err)
	assert.Contains(t, prevKIDs, firstKID, "previous KIDs list must include first KID")
}

// TestOIDCKeyCleanup verifies expired keys are removed after grace period.
func TestOIDCKeyCleanup(t *testing.T) {
	// Arrange
	store := newMemoryOIDCStore()
	enc := &passEncryptor{}
	svc := NewOIDCKeyService(store, enc)

	ctx := context.Background()

	// Generate initial key
	err := svc.EnsureKey(ctx)
	require.NoError(t, err)
	firstKID := svc.CurrentKID()

	// Rotate to create a previous key
	err = svc.RotateKey(ctx)
	require.NoError(t, err)
	secondKID := svc.CurrentKID()

	// Manually set grace period to 0 for immediate expiry (test-only)
	err = store.Put(ctx, oidcGracePeriodTTLKey, "0")
	require.NoError(t, err)

	// Act: Cleanup expired keys
	deletedCount, err := svc.CleanupExpiredKeys(ctx)
	require.NoError(t, err)

	// Assert: First key should be deleted
	assert.Equal(t, 1, deletedCount, "should delete 1 expired key")

	// Verify first key is removed from store
	_, found, err := store.Get(ctx, oidcKeyPrefix+"rsa_"+firstKID)
	require.NoError(t, err)
	assert.False(t, found, "expired key must be removed from store")

	// Verify second key is still present
	_, found, err = store.Get(ctx, oidcKeyPrefix+"rsa_"+secondKID)
	require.NoError(t, err)
	assert.True(t, found, "current key must remain in store")

	// JWKS should now only include second key (+ EC key)
	jwks, err := svc.PublicJWKS(ctx)
	require.NoError(t, err)

	rsaKeys := filterRSAKeys(jwks.Keys)
	assert.Len(t, rsaKeys, 1, "JWKS should only have current RSA key after cleanup")
	assert.Equal(t, secondKID, rsaKeys[0].Kid)
}

// TestOIDCECKeyRotation verifies ES256 key rotation with grace-period support,
// mirroring TestOIDCKeyRotation for RSA. During rotation JWKS must include both
// the current and previous EC keys so RPs can verify either ES256 id_token.
func TestOIDCECKeyRotation(t *testing.T) {
	store := newMemoryOIDCStore()
	enc := &passEncryptor{}
	svc := NewOIDCKeyService(store, enc)
	ctx := context.Background()

	require.NoError(t, svc.EnsureKey(ctx))
	firstKID := svc.CurrentECKID()
	require.NotEmpty(t, firstKID, "first EC KID must be set")

	// Sign with the first EC key.
	claims1 := jwt.MapClaims{"iss": "https://sub.sakrylle.com", "sub": "1", "aud": "c", "exp": time.Now().Add(5 * time.Minute).Unix(), "iat": time.Now().Unix()}
	token1, err := svc.Sign(claims1, SigningAlgES256)
	require.NoError(t, err)
	assert.Equal(t, firstKID, extractKIDFromToken(t, token1))

	// Rotate EC key.
	require.NoError(t, svc.RotateECKey(ctx))
	secondKID := svc.CurrentECKID()
	require.NotEmpty(t, secondKID)
	assert.NotEqual(t, firstKID, secondKID, "rotated EC KID must differ")

	token2, err := svc.Sign(claims1, SigningAlgES256)
	require.NoError(t, err)
	assert.Equal(t, secondKID, extractKIDFromToken(t, token2), "new tokens use the new EC key")

	// JWKS must include both EC keys during the grace period (+ the RSA key).
	jwks, err := svc.PublicJWKS(ctx)
	require.NoError(t, err)
	ecKIDs := make([]string, 0)
	for _, k := range filterECKeys(jwks.Keys) {
		ecKIDs = append(ecKIDs, k.Kid)
	}
	assert.Contains(t, ecKIDs, firstKID, "JWKS must include previous EC key during grace period")
	assert.Contains(t, ecKIDs, secondKID, "JWKS must include current EC key")

	prevKIDs, err := svc.GetECPreviousKIDs(ctx)
	require.NoError(t, err)
	assert.Contains(t, prevKIDs, firstKID, "previous EC KIDs list must include the retired key")
}

// TestOIDCECKeyCleanup verifies expired EC keys are removed after the grace
// period, mirroring TestOIDCKeyCleanup for RSA.
func TestOIDCECKeyCleanup(t *testing.T) {
	store := newMemoryOIDCStore()
	enc := &passEncryptor{}
	svc := NewOIDCKeyService(store, enc)
	ctx := context.Background()

	require.NoError(t, svc.EnsureKey(ctx))
	firstKID := svc.CurrentECKID()

	require.NoError(t, svc.RotateECKey(ctx))
	secondKID := svc.CurrentECKID()

	// Immediate expiry.
	require.NoError(t, store.Put(ctx, oidcGracePeriodTTLKey, "0"))

	deleted, err := svc.CleanupExpiredKeys(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted, "should delete exactly the 1 expired EC key")

	_, found, err := store.Get(ctx, oidcKeyPrefix+"ec_"+firstKID)
	require.NoError(t, err)
	assert.False(t, found, "expired EC key must be removed from store")

	_, found, err = store.Get(ctx, oidcKeyPrefix+"ec_"+secondKID)
	require.NoError(t, err)
	assert.True(t, found, "current EC key must remain")

	jwks, err := svc.PublicJWKS(ctx)
	require.NoError(t, err)
	ecKeys := filterECKeys(jwks.Keys)
	assert.Len(t, ecKeys, 1, "JWKS should only have the current EC key after cleanup")
	assert.Equal(t, secondKID, ecKeys[0].Kid)
}

// TestOIDCCleanupBothKeyTypes verifies a single CleanupExpiredKeys call purges
// expired RSA and EC keys together and reports the combined count.
func TestOIDCCleanupBothKeyTypes(t *testing.T) {
	store := newMemoryOIDCStore()
	enc := &passEncryptor{}
	svc := NewOIDCKeyService(store, enc)
	ctx := context.Background()

	require.NoError(t, svc.EnsureKey(ctx))
	require.NoError(t, svc.RotateKey(ctx))   // retire 1 RSA key
	require.NoError(t, svc.RotateECKey(ctx)) // retire 1 EC key

	require.NoError(t, store.Put(ctx, oidcGracePeriodTTLKey, "0"))

	deleted, err := svc.CleanupExpiredKeys(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, deleted, "should delete 1 expired RSA + 1 expired EC key")

	// Both previous-kids lists must now be empty.
	rsaPrev, err := svc.GetPreviousKIDs(ctx)
	require.NoError(t, err)
	assert.Empty(t, rsaPrev)
	ecPrev, err := svc.GetECPreviousKIDs(ctx)
	require.NoError(t, err)
	assert.Empty(t, ecPrev)

	// JWKS retains exactly the two current keys.
	jwks, err := svc.PublicJWKS(ctx)
	require.NoError(t, err)
	assert.Len(t, filterRSAKeys(jwks.Keys), 1)
	assert.Len(t, filterECKeys(jwks.Keys), 1)
}

// TestDualAlgorithmSigning verifies RS256 and ES256 signing work in parallel.
func TestDualAlgorithmSigning(t *testing.T) {
	// Arrange
	store := newMemoryOIDCStore()
	enc := &passEncryptor{}
	svc := NewOIDCKeyService(store, enc)

	ctx := context.Background()
	err := svc.EnsureKey(ctx)
	require.NoError(t, err)

	claims := jwt.MapClaims{
		"iss": "https://sub.sakrylle.com",
		"sub": "123",
		"aud": "test-client",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
		"iat": time.Now().Unix(),
	}

	// Act: Sign with both algorithms
	tokenRS256, err := svc.Sign(claims, SigningAlgRS256)
	require.NoError(t, err)
	require.NotEmpty(t, tokenRS256)

	tokenES256, err := svc.Sign(claims, SigningAlgES256)
	require.NoError(t, err)
	require.NotEmpty(t, tokenES256)

	// Assert: Both tokens are valid JWTs with correct algorithms
	algRS256 := extractAlgFromToken(t, tokenRS256)
	assert.Equal(t, "RS256", algRS256)

	algES256 := extractAlgFromToken(t, tokenES256)
	assert.Equal(t, "ES256", algES256)

	// JWKS must include both RSA and EC keys
	jwks, err := svc.PublicJWKS(ctx)
	require.NoError(t, err)

	rsaKeys := filterRSAKeys(jwks.Keys)
	ecKeys := filterECKeys(jwks.Keys)

	assert.GreaterOrEqual(t, len(rsaKeys), 1, "JWKS must include at least one RSA key")
	assert.Equal(t, 1, len(ecKeys), "JWKS must include exactly one EC key")

	// Verify EC key structure
	ecKey := ecKeys[0]
	assert.Equal(t, "EC", ecKey.Kty)
	assert.Equal(t, "ES256", ecKey.Alg)
	assert.Equal(t, "P-256", ecKey.Crv)
	assert.NotEmpty(t, ecKey.X, "EC key must have X coordinate")
	assert.NotEmpty(t, ecKey.Y, "EC key must have Y coordinate")
}

// TestIDTokenClaimsBuilder verifies BuildIDTokenClaims produces spec-compliant claims.
func TestIDTokenClaimsBuilder(t *testing.T) {
	tests := []struct {
		name         string
		scopes       []string
		nonce        string
		userClaims   OIDCUserClaims
		wantClaims   []string
		wantNoClaims []string
	}{
		{
			name:   "openid only → minimal claims",
			scopes: []string{"openid"},
			nonce:  "test-nonce",
			userClaims: OIDCUserClaims{
				UserID:   123,
				Username: "testuser",
				Email:    "test@example.com",
			},
			wantClaims:   []string{"iss", "sub", "aud", "exp", "iat", "nonce", "auth_time"},
			wantNoClaims: []string{"name", "email"},
		},
		{
			name:   "openid + profile → name claims",
			scopes: []string{"openid", "profile"},
			userClaims: OIDCUserClaims{
				UserID:   123,
				Username: "testuser",
			},
			wantClaims:   []string{"iss", "sub", "aud", "exp", "iat", "name", "preferred_username"},
			wantNoClaims: []string{"email"},
		},
		{
			name:   "openid + email → email claim",
			scopes: []string{"openid", "email"},
			userClaims: OIDCUserClaims{
				UserID: 123,
				Email:  "test@example.com",
			},
			wantClaims:   []string{"iss", "sub", "aud", "exp", "iat", "email", "email_verified"},
			wantNoClaims: []string{"name"},
		},
		{
			name:   "all scopes → all claims",
			scopes: []string{"openid", "profile", "email"},
			nonce:  "nonce-123",
			userClaims: OIDCUserClaims{
				UserID:   456,
				Username: "fulluser",
				Email:    "full@example.com",
			},
			wantClaims: []string{"iss", "sub", "aud", "exp", "iat", "nonce", "auth_time", "name", "preferred_username", "email", "email_verified"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			issuer := "https://sub.sakrylle.com"
			clientID := "test-client"
			authTime := time.Now().Add(-2 * time.Minute)
			now := time.Now()
			ttl := 5 * time.Minute

			// Act
			claims, err := BuildIDTokenClaims(
				issuer,
				clientID,
				tt.userClaims,
				tt.scopes,
				tt.nonce,
				authTime,
				now,
				ttl,
				"",
			)

			// Assert
			require.NoError(t, err)

			// Verify mandatory claims
			for _, claim := range tt.wantClaims {
				assert.Contains(t, claims, claim, "missing claim %s", claim)
			}

			// Verify forbidden claims are absent
			for _, claim := range tt.wantNoClaims {
				assert.NotContains(t, claims, claim, "unexpected claim %s", claim)
			}

			// Verify claim values
			assert.Equal(t, issuer, claims["iss"])
			// aud is emitted as a single-element array per OIDC Core §2 (BuildIDTokenClaims
			// deliberately wraps client_id so strict RP libraries that iterate aud don't throw).
			assert.Equal(t, []string{clientID}, claims["aud"])

			// sub must be string representation of user_id
			subStr, ok := claims["sub"].(string)
			require.True(t, ok, "sub must be string")
			assert.NotEmpty(t, subStr)

			// exp must be in the future
			exp, ok := claims["exp"].(int64)
			require.True(t, ok, "exp must be int64")
			assert.Greater(t, exp, now.Unix(), "exp must be in future")

			// iat must be around now
			iat, ok := claims["iat"].(int64)
			require.True(t, ok, "iat must be int64")
			assert.InDelta(t, now.Unix(), iat, 5, "iat must be around now")

			// nonce echo when provided
			if tt.nonce != "" {
				assert.Equal(t, tt.nonce, claims["nonce"])
			}

			// auth_time must be present
			authTimeVal, ok := claims["auth_time"].(int64)
			require.True(t, ok, "auth_time must be int64")
			assert.Equal(t, authTime.Unix(), authTimeVal)
		})
	}
}

// TestIDTokenWithoutRequiredUserClaims verifies graceful degradation when
// user claim lookup fails (MEDIUM-1 mitigation).
func TestIDTokenWithoutRequiredUserClaims(t *testing.T) {
	// When profile/email scopes are granted but user claims can't be loaded,
	// the service should strip those scopes from the id_token rather than
	// issuing a token that advertises profile/email but carries no name/email.

	// This test validates the service layer behavior; full implementation
	// requires wiring OAuthProviderService.maybeSignIDToken with a failing
	// user lookup function.

	t.Skip("Requires OAuthProviderService integration with failing user lookup")
}

// TestOIDCEndToEndFlow simulates a complete OIDC flow: authorize → code → token → userinfo.
func TestOIDCEndToEndFlow(t *testing.T) {
	// This is a placeholder for a full integration test that would:
	// 1. Create OAuth client with openid scope
	// 2. BeginAuthorize with nonce
	// 3. ApproveAuthorization → code
	// 4. ExchangeAuthorizationCode → access_token + id_token
	// 5. Verify id_token structure and claims
	// 6. Call /v1/me with access_token → verify sub claim

	// Full implementation requires:
	// - Real DB (testcontainers postgres) or in-memory ent client
	// - Complete OAuthProviderService with all repos wired
	// - OIDC key service with real crypto
	// - Handler-level integration

	t.Skip("End-to-end test requires full service wiring + DB")
}

// ── Test Helpers ─────────────────────────────────────────────────────────────

// memoryOIDCStore is an in-memory implementation of OIDCKeyStore for tests.
type memoryOIDCStore struct {
	data map[string]string
}

func newMemoryOIDCStore() *memoryOIDCStore {
	return &memoryOIDCStore{data: make(map[string]string)}
}

func (s *memoryOIDCStore) Get(ctx context.Context, key string) (string, bool, error) {
	v, ok := s.data[key]
	return v, ok, nil
}

func (s *memoryOIDCStore) Put(ctx context.Context, key, value string) error {
	s.data[key] = value
	return nil
}

func (s *memoryOIDCStore) Delete(ctx context.Context, key string) error {
	delete(s.data, key)
	return nil
}

// passEncryptor is a test-only encryptor that base64-encodes without real encryption.
type passEncryptor struct{}

func (passEncryptor) Encrypt(plaintext string) (string, error) {
	return "enc:" + base64.StdEncoding.EncodeToString([]byte(plaintext)), nil
}

func (passEncryptor) Decrypt(ciphertext string) (string, error) {
	if !strings.HasPrefix(ciphertext, "enc:") {
		return "", nil
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(ciphertext, "enc:"))
	return string(b), err
}

// extractKIDFromToken parses JWT header and returns kid claim.
func extractKIDFromToken(t *testing.T, token string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3, "JWT must have 3 parts")

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	require.NoError(t, err, "header must be valid base64url")

	var header map[string]any
	err = json.Unmarshal(headerJSON, &header)
	require.NoError(t, err, "header must be valid JSON")

	kid, ok := header["kid"].(string)
	require.True(t, ok, "kid must be present in header")
	return kid
}

// extractAlgFromToken parses JWT header and returns alg claim.
func extractAlgFromToken(t *testing.T, token string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	require.Len(t, parts, 3, "JWT must have 3 parts")

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	require.NoError(t, err)

	var header map[string]any
	err = json.Unmarshal(headerJSON, &header)
	require.NoError(t, err)

	alg, ok := header["alg"].(string)
	require.True(t, ok, "alg must be present in header")
	return alg
}

// filterRSAKeys returns only RSA keys from a JWKS.
func filterRSAKeys(keys []JWK) []JWK {
	var out []JWK
	for _, k := range keys {
		if k.Kty == "RSA" {
			out = append(out, k)
		}
	}
	return out
}

// filterECKeys returns only EC keys from a JWKS.
func filterECKeys(keys []JWK) []JWK {
	var out []JWK
	for _, k := range keys {
		if k.Kty == "EC" {
			out = append(out, k)
		}
	}
	return out
}
