package service

import (
	"context"
	"fmt"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/securitysecret"
)

const (
	// SecretTypeOIDCSigningKey is the secret type for OIDC signing keys stored
	// in the security_secrets table.
	SecretTypeOIDCSigningKey = "oidc_signing_key"
)

var (
	// ErrKeyNotFound is returned when a requested key does not exist in the store.
	ErrKeyNotFound = fmt.Errorf("key not found")
)

// SecuritySecretsOIDCKeyStore implements OIDCKeyStore over the security_secrets
// table using ent. Private key material is encrypted at rest using AES-256-GCM
// via the injected SecretEncryptor. The key field in security_secrets is the
// logical key name (e.g., "oidc_signing_current_kid", "oidc_signing_key_rs256_<kid>");
// the value field stores the encrypted PEM-encoded private key as a base64 string.
type SecuritySecretsOIDCKeyStore struct {
	client *dbent.Client
	enc    SecretEncryptor
}

// NewSecuritySecretsOIDCKeyStore creates a new key store backed by the
// security_secrets table.
func NewSecuritySecretsOIDCKeyStore(client *dbent.Client, enc SecretEncryptor) *SecuritySecretsOIDCKeyStore {
	return &SecuritySecretsOIDCKeyStore{
		client: client,
		enc:    enc,
	}
}

// Get retrieves the encrypted value for the given key. Returns (value, true, nil)
// if the key exists, ("", false, nil) if not found, or ("", false, err) on error.
func (s *SecuritySecretsOIDCKeyStore) Get(ctx context.Context, key string) (string, bool, error) {
	secret, err := s.client.SecuritySecret.Query().
		Where(securitysecret.KeyEQ(key)).
		Only(ctx)

	if err != nil {
		if dbent.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("query security_secrets for key %q: %w", key, err)
	}

	return secret.Value, true, nil
}

// Put stores or updates the value for the given key. The value is expected to
// be already encrypted by the caller (typically OIDCKeyService encrypts the PEM
// before calling Put).
func (s *SecuritySecretsOIDCKeyStore) Put(ctx context.Context, key, value string) error {
	// Upsert: update if exists, create if not
	// OnConflictColumns specifies the conflict target (the "key" unique constraint)
	err := s.client.SecuritySecret.
		Create().
		SetKey(key).
		SetValue(value).
		OnConflictColumns(securitysecret.FieldKey).
		UpdateValue().
		Exec(ctx)

	if err != nil {
		return fmt.Errorf("upsert security_secrets key %q: %w", key, err)
	}

	return nil
}

// Delete removes the key from the store. Returns nil if the key doesn't exist.
// Used during key rotation grace period cleanup.
func (s *SecuritySecretsOIDCKeyStore) Delete(ctx context.Context, key string) error {
	_, err := s.client.SecuritySecret.
		Delete().
		Where(securitysecret.KeyEQ(key)).
		Exec(ctx)

	if err != nil {
		return fmt.Errorf("delete security_secrets key %q: %w", key, err)
	}

	return nil
}
