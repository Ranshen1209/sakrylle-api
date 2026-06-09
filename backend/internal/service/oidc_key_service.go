package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	oidcRSAKeyBits      = 2048
	oidcCurrentKIDKey   = "oidc_signing_current_kid"
	oidcKeyPrefix       = "oidc_signing_key_"
	oidcPreviousKIDsKey = "oidc_signing_previous_kids"
	// oidcPreviousKIDsKeyEC stores the retired EC kids list. RSA keeps the
	// historical un-suffixed key (oidcPreviousKIDsKey) for backward compat with
	// data written before EC rotation existed; EC uses its own suffixed key.
	oidcPreviousKIDsKeyEC = "oidc_signing_previous_kids_ec"
	oidcGracePeriodTTLKey = "oidc_grace_period_ttl_seconds"
	defaultGracePeriodSec = 86400 // 24 hours
)

// SigningAlgorithm represents the JWS algorithm used for id_token signing.
type SigningAlgorithm string

const (
	// SigningAlgRS256 is RSA PKCS#1 v1.5 with SHA-256 (RFC 7518 §3.3).
	SigningAlgRS256 SigningAlgorithm = "RS256"
	// SigningAlgES256 is ECDSA P-256 with SHA-256 (RFC 7518 §3.4).
	SigningAlgES256 SigningAlgorithm = "ES256"
)

// SigningKeyType identifies the asymmetric key type (RSA or EC).
type SigningKeyType string

const (
	SigningKeyTypeRSA SigningKeyType = "RSA"
	SigningKeyTypeEC  SigningKeyType = "EC"
)

// OIDCKeyStore is the minimal persistence surface for OIDC signing keys.
// Production wires this to the security_secrets table; tests use an in-memory
// implementation. The stored value for a key entry is the AES-256-GCM
// ciphertext of the PEM private key — never plaintext.
type OIDCKeyStore interface {
	Get(ctx context.Context, key string) (value string, found bool, err error)
	Put(ctx context.Context, key, value string) error
	Delete(ctx context.Context, key string) error
}

// previousKIDEntry tracks a retired signing key with its expiry timestamp.
// Stored as JSON array in oidc_signing_previous_kids.
type previousKIDEntry struct {
	KID       string    `json:"kid"`
	RetiredAt time.Time `json:"retired_at"`
}

// JWK is a public JSON Web Key (RFC 7517). Supports both RSA and EC public
// key material. There are deliberately no fields for private components
// (d, p, q, dp, dq, qi for RSA; d for EC), so a JWK can never serialize a private key.
type JWK struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	// RSA fields
	N string `json:"n,omitempty"`
	E string `json:"e,omitempty"`
	// EC fields
	Crv string `json:"crv,omitempty"` // EC curve name (P-256)
	X   string `json:"x,omitempty"`   // EC public key X coordinate (base64url)
	Y   string `json:"y,omitempty"`   // EC public key Y coordinate (base64url)
}

// JWKS is a JWK Set as published at /.well-known/jwks.json.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// OIDCKeyService owns the id_token signing keys (RS256 and ES256). The private
// keys are held only in process memory (after decryption) and at rest only as
// ciphertext in the key store. The key material is never logged.
type OIDCKeyService struct {
	store OIDCKeyStore
	enc   SecretEncryptor

	mu sync.RWMutex
	// RS256 key pair
	rsaKID      string
	rsaPriv     *rsa.PrivateKey
	rsaPrevKIDs []string // retired RSA kids still in grace period
	// ES256 key pair
	ecKID      string
	ecPriv     *ecdsa.PrivateKey
	ecPrevKIDs []string // retired EC kids still in grace period
}

// NewOIDCKeyService constructs the service. Call EnsureKey before signing.
func NewOIDCKeyService(store OIDCKeyStore, enc SecretEncryptor) *OIDCKeyService {
	return &OIDCKeyService{store: store, enc: enc}
}

// EnsureKey loads the current signing keys (both RS256 and ES256), generating
// and persisting them if absent. Idempotent and safe to call once at startup.
// Fails closed: any error leaves the service unable to sign rather than falling
// back to an insecure state.
func (s *OIDCKeyService) EnsureKey(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("oidc key service is nil")
	}
	if s.store == nil || s.enc == nil {
		return fmt.Errorf("oidc key service requires a store and an encryptor")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Load or generate RS256 key
	if s.rsaPriv == nil {
		rsaKID, found, err := s.store.Get(ctx, oidcCurrentKIDKey+"_rsa")
		if err != nil {
			return fmt.Errorf("load current rsa kid: %w", err)
		}
		if found && rsaKID != "" {
			rsaPriv, err := s.loadRSAKey(ctx, rsaKID)
			if err != nil {
				return err
			}
			s.rsaKID, s.rsaPriv = rsaKID, rsaPriv

			// Load previous RSA KIDs for JWKS multi-key support
			prevKIDs, err := s.loadPreviousKIDs(ctx)
			if err != nil {
				// Log but don't fail - previous keys are best-effort for grace period
				s.rsaPrevKIDs = nil
			} else {
				s.rsaPrevKIDs = prevKIDs
			}
		} else {
			if err := s.generateAndStoreRSA(ctx); err != nil {
				return err
			}
		}
	}

	// Load or generate ES256 key
	if s.ecPriv == nil {
		ecKID, found, err := s.store.Get(ctx, oidcCurrentKIDKey+"_ec")
		if err != nil {
			return fmt.Errorf("load current ec kid: %w", err)
		}
		if found && ecKID != "" {
			ecPriv, err := s.loadECKey(ctx, ecKID)
			if err != nil {
				return err
			}
			s.ecKID, s.ecPriv = ecKID, ecPriv

			// Load previous EC KIDs for JWKS multi-key support during grace period.
			prevKIDs, err := s.loadPreviousKIDsFor(ctx, oidcPreviousKIDsKeyEC)
			if err != nil {
				// Best-effort, mirror the RSA branch: previous keys only matter
				// for the grace period, never for current signing.
				s.ecPrevKIDs = nil
			} else {
				s.ecPrevKIDs = prevKIDs
			}
		} else {
			if err := s.generateAndStoreEC(ctx); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *OIDCKeyService) loadRSAKey(ctx context.Context, kid string) (*rsa.PrivateKey, error) {
	enc, found, err := s.store.Get(ctx, oidcKeyPrefix+"rsa_"+kid)
	if err != nil {
		return nil, fmt.Errorf("load rsa signing key: %w", err)
	}
	if !found || enc == "" {
		return nil, fmt.Errorf("oidc rsa signing key for current kid is missing")
	}
	pemStr, err := s.enc.Decrypt(enc)
	if err != nil {
		return nil, fmt.Errorf("decrypt rsa signing key: %w", err)
	}
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("oidc rsa signing key PEM decode failed")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse rsa signing key: %w", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("oidc signing key is not an RSA key")
	}
	return rsaKey, nil
}

// loadRSAPublicKeyOnly loads only the public key component for a given RSA kid.
// Used by PublicJWKS to include previous keys in the key set.
func (s *OIDCKeyService) loadRSAPublicKeyOnly(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	priv, err := s.loadRSAKey(ctx, kid)
	if err != nil {
		return nil, err
	}
	return &priv.PublicKey, nil
}

// loadPreviousKIDs retrieves the list of retired RSA KIDs still within grace period.
func (s *OIDCKeyService) loadPreviousKIDs(ctx context.Context) ([]string, error) {
	return s.loadPreviousKIDsFor(ctx, oidcPreviousKIDsKey)
}

// loadPreviousKIDsFor retrieves the retired KIDs still within grace period from
// the given store key. RSA and EC each keep their own previous-kids list.
func (s *OIDCKeyService) loadPreviousKIDsFor(ctx context.Context, storeKey string) ([]string, error) {
	jsonStr, found, err := s.store.Get(ctx, storeKey)
	if err != nil {
		return nil, fmt.Errorf("load previous kids: %w", err)
	}
	if !found || jsonStr == "" {
		return nil, nil
	}

	var entries []previousKIDEntry
	if err := json.Unmarshal([]byte(jsonStr), &entries); err != nil {
		return nil, fmt.Errorf("parse previous kids JSON: %w", err)
	}

	kids := make([]string, 0, len(entries))
	for _, entry := range entries {
		kids = append(kids, entry.KID)
	}
	return kids, nil
}

// loadECPublicKeyOnly loads only the public key component for a given EC kid.
// Used by PublicJWKS to include previous EC keys in the key set during the
// rotation grace period.
func (s *OIDCKeyService) loadECPublicKeyOnly(ctx context.Context, kid string) (*ecdsa.PublicKey, error) {
	priv, err := s.loadECKey(ctx, kid)
	if err != nil {
		return nil, err
	}
	return &priv.PublicKey, nil
}

func (s *OIDCKeyService) loadECKey(ctx context.Context, kid string) (*ecdsa.PrivateKey, error) {
	enc, found, err := s.store.Get(ctx, oidcKeyPrefix+"ec_"+kid)
	if err != nil {
		return nil, fmt.Errorf("load ec signing key: %w", err)
	}
	if !found || enc == "" {
		return nil, fmt.Errorf("oidc ec signing key for current kid is missing")
	}
	pemStr, err := s.enc.Decrypt(enc)
	if err != nil {
		return nil, fmt.Errorf("decrypt ec signing key: %w", err)
	}
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("oidc ec signing key PEM decode failed")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse ec signing key: %w", err)
	}
	ecKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("oidc signing key is not an EC key")
	}
	return ecKey, nil
}

func (s *OIDCKeyService) generateAndStoreRSA(ctx context.Context) error {
	priv, err := rsa.GenerateKey(rand.Reader, oidcRSAKeyBits)
	if err != nil {
		return fmt.Errorf("generate rsa key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return fmt.Errorf("marshal rsa key: %w", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	encrypted, err := s.enc.Encrypt(string(pemBytes))
	if err != nil {
		return fmt.Errorf("encrypt rsa key: %w", err)
	}
	kid := rsaKID(&priv.PublicKey)
	if err := s.store.Put(ctx, oidcKeyPrefix+"rsa_"+kid, encrypted); err != nil {
		return fmt.Errorf("store rsa signing key: %w", err)
	}
	if err := s.store.Put(ctx, oidcCurrentKIDKey+"_rsa", kid); err != nil {
		return fmt.Errorf("store current rsa kid: %w", err)
	}
	s.rsaKID, s.rsaPriv = kid, priv
	s.rsaPrevKIDs = nil
	return nil
}

func (s *OIDCKeyService) generateAndStoreEC(ctx context.Context) error {
	// Generate ECDSA P-256 key pair per RFC 7518 §3.4
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate ec key: %w", err)
	}
	// x509.MarshalPKCS8PrivateKey supports *ecdsa.PrivateKey natively
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return fmt.Errorf("marshal ec key: %w", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	encrypted, err := s.enc.Encrypt(string(pemBytes))
	if err != nil {
		return fmt.Errorf("encrypt ec key: %w", err)
	}
	kid := ecKID(&priv.PublicKey)
	if err := s.store.Put(ctx, oidcKeyPrefix+"ec_"+kid, encrypted); err != nil {
		return fmt.Errorf("store ec signing key: %w", err)
	}
	if err := s.store.Put(ctx, oidcCurrentKIDKey+"_ec", kid); err != nil {
		return fmt.Errorf("store current ec kid: %w", err)
	}
	s.ecKID, s.ecPriv = kid, priv
	return nil
}

// CurrentKID returns the active RS256 key id (empty before EnsureKey).
func (s *OIDCKeyService) CurrentKID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rsaKID
}

// CurrentECKID returns the active ES256 key id (empty before EnsureKey).
func (s *OIDCKeyService) CurrentECKID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ecKID
}

// GetVerificationKey returns the public key and key type for the given kid.
// Used by the RP-Initiated Logout handler to verify id_token_hint signatures.
// Returns (nil, "", error) when the kid is unknown.
func (s *OIDCKeyService) GetVerificationKey(ctx context.Context, kid string) (any, SigningKeyType, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Check current and previous RSA kids.
	if kid == s.rsaKID {
		pub, err := s.loadRSAPublicKeyOnly(ctx, kid)
		if err != nil {
			return nil, "", fmt.Errorf("oidc: failed to load RSA public key for kid %q: %w", kid, err)
		}
		return pub, SigningKeyTypeRSA, nil
	}
	for _, prevKID := range s.rsaPrevKIDs {
		if kid == prevKID {
			pub, err := s.loadRSAPublicKeyOnly(ctx, kid)
			if err != nil {
				return nil, "", fmt.Errorf("oidc: failed to load previous RSA public key for kid %q: %w", kid, err)
			}
			return pub, SigningKeyTypeRSA, nil
		}
	}

	// Check current and previous EC kids.
	if kid == s.ecKID {
		pub, err := s.loadECPublicKeyOnly(ctx, kid)
		if err != nil {
			return nil, "", fmt.Errorf("oidc: failed to load EC public key for kid %q: %w", kid, err)
		}
		return pub, SigningKeyTypeEC, nil
	}
	for _, prevKID := range s.ecPrevKIDs {
		if kid == prevKID {
			pub, err := s.loadECPublicKeyOnly(ctx, kid)
			if err != nil {
				return nil, "", fmt.Errorf("oidc: failed to load previous EC public key for kid %q: %w", kid, err)
			}
			return pub, SigningKeyTypeEC, nil
		}
	}

	return nil, "", fmt.Errorf("oidc: unknown kid %q", kid)
}

// Sign produces a compact JWT for the given claims with the kid header set
// so relying parties can select the verifying key from JWKS. The algorithm
// parameter determines whether RS256 or ES256 is used.
func (s *OIDCKeyService) Sign(claims jwt.MapClaims, algorithm SigningAlgorithm) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	switch algorithm {
	case SigningAlgRS256:
		if s.rsaPriv == nil {
			return "", fmt.Errorf("oidc rsa signing key not loaded; call EnsureKey first")
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tok.Header["kid"] = s.rsaKID
		return tok.SignedString(s.rsaPriv)

	case SigningAlgES256:
		if s.ecPriv == nil {
			return "", fmt.Errorf("oidc ec signing key not loaded; call EnsureKey first")
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
		tok.Header["kid"] = s.ecKID
		return tok.SignedString(s.ecPriv)

	default:
		return "", fmt.Errorf("unsupported signing algorithm: %s", algorithm)
	}
}

// PublicJWKS returns the public key set for /.well-known/jwks.json.
// Returns both RS256 and ES256 keys for dual-algorithm support.
// During a key rotation grace period, returns both current and previous keys
// (for both RSA and EC) so RPs can verify tokens signed with either key.
func (s *OIDCKeyService) PublicJWKS(ctx context.Context) (JWKS, error) {
	s.mu.RLock()
	rsaPriv, rsaKID := s.rsaPriv, s.rsaKID
	ecPriv, ecKID := s.ecPriv, s.ecKID
	rsaPrevKIDs := make([]string, len(s.rsaPrevKIDs))
	copy(rsaPrevKIDs, s.rsaPrevKIDs)
	ecPrevKIDs := make([]string, len(s.ecPrevKIDs))
	copy(ecPrevKIDs, s.ecPrevKIDs)
	s.mu.RUnlock()

	keys := []JWK{}

	// Add current RSA key
	if rsaPriv != nil {
		keys = append(keys, publicRSAJWK(&rsaPriv.PublicKey, rsaKID))
	}

	// Add previous RSA keys still in grace period
	for _, prevKID := range rsaPrevKIDs {
		pubKey, err := s.loadRSAPublicKeyOnly(ctx, prevKID)
		if err != nil {
			// Skip keys that can't be loaded (e.g., already cleaned up)
			continue
		}
		keys = append(keys, publicRSAJWK(pubKey, prevKID))
	}

	// Add current EC key
	if ecPriv != nil {
		keys = append(keys, publicECJWK(&ecPriv.PublicKey, ecKID))
	}

	// Add previous EC keys still in grace period
	for _, prevKID := range ecPrevKIDs {
		pubKey, err := s.loadECPublicKeyOnly(ctx, prevKID)
		if err != nil {
			continue
		}
		keys = append(keys, publicECJWK(pubKey, prevKID))
	}

	if len(keys) == 0 {
		return JWKS{}, fmt.Errorf("oidc signing keys not loaded")
	}

	return JWKS{Keys: keys}, nil
}

// RotateKey generates a new RS256 signing key, moves the current key to the previous
// keys list with an expiry timestamp, and updates the in-memory state.
// The old key remains in the store during the grace period so RPs can still
// verify tokens signed before rotation.
func (s *OIDCKeyService) RotateKey(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.rsaPriv == nil {
		return fmt.Errorf("oidc rsa signing key not loaded; call EnsureKey first")
	}

	oldKID := s.rsaKID

	// Generate new RSA key
	newPriv, err := rsa.GenerateKey(rand.Reader, oidcRSAKeyBits)
	if err != nil {
		return fmt.Errorf("generate new rsa key: %w", err)
	}

	newKID := rsaKID(&newPriv.PublicKey)

	// Marshal and encrypt new key
	der, err := x509.MarshalPKCS8PrivateKey(newPriv)
	if err != nil {
		return fmt.Errorf("marshal new rsa key: %w", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	encrypted, err := s.enc.Encrypt(string(pemBytes))
	if err != nil {
		return fmt.Errorf("encrypt new rsa key: %w", err)
	}

	// Store new key
	if err := s.store.Put(ctx, oidcKeyPrefix+"rsa_"+newKID, encrypted); err != nil {
		return fmt.Errorf("store new rsa signing key: %w", err)
	}

	// Retire the old key into the RSA previous-kids list (now() timestamp).
	if err := s.appendPreviousKID(ctx, oidcPreviousKIDsKey, oldKID); err != nil {
		return err
	}

	// Update current kid pointer
	if err := s.store.Put(ctx, oidcCurrentKIDKey+"_rsa", newKID); err != nil {
		return fmt.Errorf("update current rsa kid pointer: %w", err)
	}

	// Update in-memory state
	s.rsaKID = newKID
	s.rsaPriv = newPriv
	s.rsaPrevKIDs = append(s.rsaPrevKIDs, oldKID)

	return nil
}

// GetPreviousKIDs returns the list of retired RSA KIDs still in grace period.
func (s *OIDCKeyService) GetPreviousKIDs(ctx context.Context) ([]string, error) {
	return s.loadPreviousKIDsFor(ctx, oidcPreviousKIDsKey)
}

// GetECPreviousKIDs returns the list of retired EC KIDs still in grace period.
func (s *OIDCKeyService) GetECPreviousKIDs(ctx context.Context) ([]string, error) {
	return s.loadPreviousKIDsFor(ctx, oidcPreviousKIDsKeyEC)
}

// appendPreviousKID loads the retired-kids list at prevKIDsStoreKey, appends
// oldKID with a now() retirement timestamp, and persists it. Used by both the
// RSA and EC rotation paths. Caller must hold s.mu.
func (s *OIDCKeyService) appendPreviousKID(ctx context.Context, prevKIDsStoreKey, oldKID string) error {
	jsonStr, found, err := s.store.Get(ctx, prevKIDsStoreKey)
	if err != nil {
		return fmt.Errorf("load previous kids for rotation: %w", err)
	}
	var entries []previousKIDEntry
	if found && jsonStr != "" {
		if err := json.Unmarshal([]byte(jsonStr), &entries); err != nil {
			return fmt.Errorf("parse previous kids JSON: %w", err)
		}
	}
	entries = append(entries, previousKIDEntry{KID: oldKID, RetiredAt: time.Now().UTC()})
	updatedJSON, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("marshal updated previous kids: %w", err)
	}
	if err := s.store.Put(ctx, prevKIDsStoreKey, string(updatedJSON)); err != nil {
		return fmt.Errorf("store updated previous kids: %w", err)
	}
	return nil
}

// RotateECKey generates a new ES256 signing key, moves the current EC key to the
// previous-keys list with an expiry timestamp, and updates in-memory state. The
// old key remains in the store during the grace period (and in JWKS) so RPs can
// still verify ES256 id_tokens signed before rotation. Mirrors RotateKey (RSA).
func (s *OIDCKeyService) RotateECKey(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ecPriv == nil {
		return fmt.Errorf("oidc ec signing key not loaded; call EnsureKey first")
	}

	oldKID := s.ecKID

	newPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate new ec key: %w", err)
	}
	newKID := ecKID(&newPriv.PublicKey)

	der, err := x509.MarshalPKCS8PrivateKey(newPriv)
	if err != nil {
		return fmt.Errorf("marshal new ec key: %w", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	encrypted, err := s.enc.Encrypt(string(pemBytes))
	if err != nil {
		return fmt.Errorf("encrypt new ec key: %w", err)
	}

	// Store new key first, then retire the old one, then flip the pointer — same
	// ordering as RSA so a mid-rotation store failure can't strand the pointer.
	if err := s.store.Put(ctx, oidcKeyPrefix+"ec_"+newKID, encrypted); err != nil {
		return fmt.Errorf("store new ec signing key: %w", err)
	}
	if err := s.appendPreviousKID(ctx, oidcPreviousKIDsKeyEC, oldKID); err != nil {
		return err
	}
	if err := s.store.Put(ctx, oidcCurrentKIDKey+"_ec", newKID); err != nil {
		return fmt.Errorf("update current ec kid pointer: %w", err)
	}

	s.ecKID = newKID
	s.ecPriv = newPriv
	s.ecPrevKIDs = append(s.ecPrevKIDs, oldKID)

	return nil
}

// CleanupExpiredKeys removes signing keys (both RSA and EC) that have exceeded
// the grace period from the store and their previous-kids lists, and prunes the
// matching in-memory previous-kid slices. Returns the total count of keys deleted.
func (s *OIDCKeyService) CleanupExpiredKeys(ctx context.Context) (int, error) {
	// Load grace period TTL from settings or use default. Read under s.mu so
	// this store access is serialized with the rotation paths that also touch
	// the store; the lock is released before calling cleanupExpiredKeysFor
	// (which acquires s.mu itself) to avoid a self-deadlock.
	gracePeriodSec := defaultGracePeriodSec
	s.mu.Lock()
	ttlStr, found, err := s.store.Get(ctx, oidcGracePeriodTTLKey)
	s.mu.Unlock()
	if err != nil {
		return 0, fmt.Errorf("load grace period ttl: %w", err)
	}
	if found && ttlStr != "" {
		// A successfully parsed value — including 0 or a negative — overrides the
		// default. 0 means "expire retired keys immediately"; negatives are clamped
		// to 0 (same effect). Only a non-numeric value falls through to the default.
		// See TestOIDCKeyCleanup and TestOIDCKeyService_InvalidGracePeriodTTL.
		var ttl int
		if _, err := fmt.Sscanf(ttlStr, "%d", &ttl); err == nil {
			if ttl < 0 {
				ttl = 0
			}
			gracePeriodSec = ttl
		}
	}

	gracePeriod := time.Duration(gracePeriodSec) * time.Second
	now := time.Now().UTC()

	rsaDeleted, err := s.cleanupExpiredKeysFor(ctx, oidcPreviousKIDsKey, "rsa_", gracePeriod, now, &s.rsaPrevKIDs)
	if err != nil {
		return rsaDeleted, err
	}
	ecDeleted, err := s.cleanupExpiredKeysFor(ctx, oidcPreviousKIDsKeyEC, "ec_", gracePeriod, now, &s.ecPrevKIDs)
	if err != nil {
		return rsaDeleted + ecDeleted, err
	}
	return rsaDeleted + ecDeleted, nil
}

// cleanupExpiredKeysFor purges expired retired keys for one key type. keyKind is
// the store-key infix ("rsa_" or "ec_"); prevKIDsStoreKey is that type's
// previous-kids list; memPrevKIDs points at the matching in-memory slice, which
// is pruned to the still-valid kids. Returns the number of keys deleted.
func (s *OIDCKeyService) cleanupExpiredKeysFor(
	ctx context.Context,
	prevKIDsStoreKey, keyKind string,
	gracePeriod time.Duration,
	now time.Time,
	memPrevKIDs *[]string,
) (int, error) {
	// Hold s.mu across the entire Get->filter->Put on the shared previous-kids
	// store list (and the in-memory prune below), not just the prune. This
	// serializes with RotateKey/RotateECKey/appendPreviousKID — which mutate the
	// same list under s.mu — closing the TOCTOU that could drop a freshly
	// rotated kid. Safe from deadlock: the caller CleanupExpiredKeys does not
	// hold s.mu, and rotation never calls back into cleanup.
	s.mu.Lock()
	defer s.mu.Unlock()

	jsonStr, found, err := s.store.Get(ctx, prevKIDsStoreKey)
	if err != nil {
		return 0, fmt.Errorf("load previous kids: %w", err)
	}
	if !found || jsonStr == "" {
		return 0, nil // Nothing to clean up
	}

	var entries []previousKIDEntry
	if err := json.Unmarshal([]byte(jsonStr), &entries); err != nil {
		return 0, fmt.Errorf("parse previous kids JSON: %w", err)
	}

	var validEntries []previousKIDEntry
	var expiredKIDs []string
	for _, entry := range entries {
		if now.After(entry.RetiredAt.Add(gracePeriod)) {
			expiredKIDs = append(expiredKIDs, entry.KID)
		} else {
			validEntries = append(validEntries, entry)
		}
	}

	deletedCount := 0
	for _, kid := range expiredKIDs {
		if err := s.store.Delete(ctx, oidcKeyPrefix+keyKind+kid); err != nil {
			// Best effort: skip keys that fail to delete, continue cleanup.
			continue
		}
		deletedCount++
	}

	if len(validEntries) == 0 {
		if err := s.store.Delete(ctx, prevKIDsStoreKey); err != nil {
			return deletedCount, fmt.Errorf("delete empty previous kids list: %w", err)
		}
	} else {
		updatedJSON, err := json.Marshal(validEntries)
		if err != nil {
			return deletedCount, fmt.Errorf("marshal filtered previous kids: %w", err)
		}
		if err := s.store.Put(ctx, prevKIDsStoreKey, string(updatedJSON)); err != nil {
			return deletedCount, fmt.Errorf("store filtered previous kids: %w", err)
		}
	}

	// Prune the in-memory previous-kid slice to the still-valid set.
	validKIDSet := make(map[string]bool, len(validEntries))
	for _, entry := range validEntries {
		validKIDSet[entry.KID] = true
	}
	newPrevKIDs := make([]string, 0, len(*memPrevKIDs))
	for _, kid := range *memPrevKIDs {
		if validKIDSet[kid] {
			newPrevKIDs = append(newPrevKIDs, kid)
		}
	}
	*memPrevKIDs = newPrevKIDs

	return deletedCount, nil
}

func publicRSAJWK(pub *rsa.PublicKey, kid string) JWK {
	return JWK{
		Kty: "RSA",
		Use: "sig",
		Alg: "RS256",
		Kid: kid,
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

func publicECJWK(pub *ecdsa.PublicKey, kid string) JWK {
	return JWK{
		Kty: "EC",
		Use: "sig",
		Alg: "ES256",
		Kid: kid,
		Crv: "P-256",
		X:   base64.RawURLEncoding.EncodeToString(ecCoordBytes(pub.X)),
		Y:   base64.RawURLEncoding.EncodeToString(ecCoordBytes(pub.Y)),
	}
}

// ecCoordBytes renders a P-256 affine coordinate as exactly 32 big-endian bytes,
// left-padded with zeros. RFC 7518 §6.2.1.2 requires the x/y octet strings to be
// the full field-element size; big.Int.Bytes() strips leading zero bytes, which
// would intermittently (~1/256 per coordinate) yield a short, non-spec encoding
// that strict RP JWK parsers reject. See TestOIDCKeyService_ES256_CoordinatePadding.
func ecCoordBytes(c *big.Int) []byte {
	const p256CoordLen = 32
	b := c.Bytes()
	if len(b) >= p256CoordLen {
		return b
	}
	padded := make([]byte, p256CoordLen)
	copy(padded[p256CoordLen-len(b):], b)
	return padded
}

// rsaKID derives a stable RFC 7638 JWK thumbprint (SHA-256 over the canonical
// {e,kty,n} JSON object), base64url-encoded.
func rsaKID(pub *rsa.PublicKey) string {
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes())
	canonical := fmt.Sprintf(`{"e":"%s","kty":"RSA","n":"%s"}`, e, n)
	sum := sha256.Sum256([]byte(canonical))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// ecKID derives a stable RFC 7638 JWK thumbprint for EC keys (SHA-256 over
// the canonical {crv,kty,x,y} JSON object), base64url-encoded.
func ecKID(pub *ecdsa.PublicKey) string {
	x := base64.RawURLEncoding.EncodeToString(ecCoordBytes(pub.X))
	y := base64.RawURLEncoding.EncodeToString(ecCoordBytes(pub.Y))
	canonical := fmt.Sprintf(`{"crv":"P-256","kty":"EC","x":"%s","y":"%s"}`, x, y)
	sum := sha256.Sum256([]byte(canonical))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
