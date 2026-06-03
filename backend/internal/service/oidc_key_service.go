package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"sync"

	"github.com/golang-jwt/jwt/v5"
)

const (
	oidcRSAKeyBits    = 2048
	oidcCurrentKIDKey = "oidc_signing_current_kid"
	oidcKeyPrefix     = "oidc_signing_key_rs256_"
)

// OIDCKeyStore is the minimal persistence surface for OIDC signing keys.
// Production wires this to the security_secrets table; tests use an in-memory
// implementation. The stored value for a key entry is the AES-256-GCM
// ciphertext of the PEM private key — never plaintext.
type OIDCKeyStore interface {
	Get(ctx context.Context, key string) (value string, found bool, err error)
	Put(ctx context.Context, key, value string) error
}

// JWK is a public JSON Web Key (RFC 7517). Only public RSA material is
// represented — there are deliberately no fields for the private components
// (d, p, q, dp, dq, qi), so a JWK can never serialize a private key.
type JWK struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// JWKS is a JWK Set as published at /.well-known/jwks.json.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// OIDCKeyService owns the RS256 id_token signing key. The private key is held
// only in process memory (after decryption) and at rest only as ciphertext in
// the key store. The key material is never logged.
type OIDCKeyService struct {
	store OIDCKeyStore
	enc   SecretEncryptor

	mu   sync.RWMutex
	kid  string
	priv *rsa.PrivateKey
}

// NewOIDCKeyService constructs the service. Call EnsureKey before signing.
func NewOIDCKeyService(store OIDCKeyStore, enc SecretEncryptor) *OIDCKeyService {
	return &OIDCKeyService{store: store, enc: enc}
}

// EnsureKey loads the current signing key, generating and persisting one if
// absent. Idempotent and safe to call once at startup. Fails closed: any error
// leaves the service unable to sign rather than falling back to an insecure
// state.
func (s *OIDCKeyService) EnsureKey(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("oidc key service is nil")
	}
	if s.store == nil || s.enc == nil {
		return fmt.Errorf("oidc key service requires a store and an encryptor")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.priv != nil {
		return nil
	}

	kid, found, err := s.store.Get(ctx, oidcCurrentKIDKey)
	if err != nil {
		return fmt.Errorf("load current kid: %w", err)
	}
	if found && kid != "" {
		priv, err := s.loadKey(ctx, kid)
		if err != nil {
			return err
		}
		s.kid, s.priv = kid, priv
		return nil
	}
	return s.generateAndStore(ctx)
}

func (s *OIDCKeyService) loadKey(ctx context.Context, kid string) (*rsa.PrivateKey, error) {
	enc, found, err := s.store.Get(ctx, oidcKeyPrefix+kid)
	if err != nil {
		return nil, fmt.Errorf("load signing key: %w", err)
	}
	if !found || enc == "" {
		return nil, fmt.Errorf("oidc signing key for current kid is missing")
	}
	pemStr, err := s.enc.Decrypt(enc)
	if err != nil {
		return nil, fmt.Errorf("decrypt signing key: %w", err)
	}
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("oidc signing key PEM decode failed")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse signing key: %w", err)
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("oidc signing key is not an RSA key")
	}
	return rsaKey, nil
}

func (s *OIDCKeyService) generateAndStore(ctx context.Context) error {
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
	if err := s.store.Put(ctx, oidcKeyPrefix+kid, encrypted); err != nil {
		return fmt.Errorf("store signing key: %w", err)
	}
	if err := s.store.Put(ctx, oidcCurrentKIDKey, kid); err != nil {
		return fmt.Errorf("store current kid: %w", err)
	}
	s.kid, s.priv = kid, priv
	return nil
}

// CurrentKID returns the active key id (empty before EnsureKey).
func (s *OIDCKeyService) CurrentKID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.kid
}

// Sign produces a compact RS256 JWT for the given claims with the kid header
// set so relying parties can select the verifying key from JWKS.
func (s *OIDCKeyService) Sign(claims jwt.MapClaims) (string, error) {
	s.mu.RLock()
	priv, kid := s.priv, s.kid
	s.mu.RUnlock()
	if priv == nil {
		return "", fmt.Errorf("oidc signing key not loaded; call EnsureKey first")
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	return tok.SignedString(priv)
}

// PublicJWKS returns the public key set for /.well-known/jwks.json.
func (s *OIDCKeyService) PublicJWKS() (JWKS, error) {
	s.mu.RLock()
	priv, kid := s.priv, s.kid
	s.mu.RUnlock()
	if priv == nil {
		return JWKS{}, fmt.Errorf("oidc signing key not loaded")
	}
	return JWKS{Keys: []JWK{publicJWK(&priv.PublicKey, kid)}}, nil
}

func publicJWK(pub *rsa.PublicKey, kid string) JWK {
	return JWK{
		Kty: "RSA",
		Use: "sig",
		Alg: "RS256",
		Kid: kid,
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
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
