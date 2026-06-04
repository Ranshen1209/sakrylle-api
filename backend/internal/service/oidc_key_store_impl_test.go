package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/ent/securitysecret"

	_ "github.com/mattn/go-sqlite3"
)

// mockEncryptor is a test double for SecretEncryptor that adds a reversible
// prefix to simulate encryption without real AES overhead.
type mockEncryptor struct {
	encryptErr error
	decryptErr error
}

func (m *mockEncryptor) Encrypt(plaintext string) (string, error) {
	if m.encryptErr != nil {
		return "", m.encryptErr
	}
	return "encrypted:" + plaintext, nil
}

func (m *mockEncryptor) Decrypt(ciphertext string) (string, error) {
	if m.decryptErr != nil {
		return "", m.decryptErr
	}
	if len(ciphertext) < 10 || ciphertext[:10] != "encrypted:" {
		return "", errors.New("invalid ciphertext format")
	}
	return ciphertext[10:], nil
}

func TestSecuritySecretsOIDCKeyStore_Put_Success(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	enc := &mockEncryptor{}
	store := NewSecuritySecretsOIDCKeyStore(client, enc)

	key := "oidc_signing_key_rs256_test123"
	value := "encrypted:-----BEGIN PRIVATE KEY-----\nMIIE..."

	err := store.Put(ctx, key, value)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Verify stored in database
	secret, err := client.SecuritySecret.Query().
		Where(securitysecret.KeyEQ(key)).
		Only(ctx)
	if err != nil {
		t.Fatalf("query after Put failed: %v", err)
	}
	if secret.Value != value {
		t.Errorf("stored value = %q, want %q", secret.Value, value)
	}
}

func TestSecuritySecretsOIDCKeyStore_Put_Upsert(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	enc := &mockEncryptor{}
	store := NewSecuritySecretsOIDCKeyStore(client, enc)

	key := "oidc_signing_current_kid"
	value1 := "kid123"
	value2 := "kid456"

	// First put
	if err := store.Put(ctx, key, value1); err != nil {
		t.Fatalf("first Put failed: %v", err)
	}

	// Second put (upsert)
	if err := store.Put(ctx, key, value2); err != nil {
		t.Fatalf("second Put failed: %v", err)
	}

	// Verify updated value
	secret, err := client.SecuritySecret.Query().
		Where(securitysecret.KeyEQ(key)).
		Only(ctx)
	if err != nil {
		t.Fatalf("query after upsert failed: %v", err)
	}
	if secret.Value != value2 {
		t.Errorf("upserted value = %q, want %q", secret.Value, value2)
	}

	// Verify only one row exists
	count, err := client.SecuritySecret.Query().
		Where(securitysecret.KeyEQ(key)).
		Count(ctx)
	if err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row after upsert, got %d", count)
	}
}

func TestSecuritySecretsOIDCKeyStore_Get_Success(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	enc := &mockEncryptor{}
	store := NewSecuritySecretsOIDCKeyStore(client, enc)

	key := "oidc_signing_key_rs256_abc"
	value := "encrypted:private_key_material"

	// Pre-populate
	err := client.SecuritySecret.Create().
		SetKey(key).
		SetValue(value).
		Exec(ctx)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Test Get
	got, found, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if !found {
		t.Fatal("Get returned found=false, want true")
	}
	if got != value {
		t.Errorf("Get returned %q, want %q", got, value)
	}
}

func TestSecuritySecretsOIDCKeyStore_Get_NotFound(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	enc := &mockEncryptor{}
	store := NewSecuritySecretsOIDCKeyStore(client, enc)

	key := "nonexistent_key"

	got, found, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get returned error: %v, want nil", err)
	}
	if found {
		t.Error("Get returned found=true, want false")
	}
	if got != "" {
		t.Errorf("Get returned value %q, want empty string", got)
	}
}

func TestSecuritySecretsOIDCKeyStore_Get_QueryError(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	client.Close() // Close client to trigger query error

	enc := &mockEncryptor{}
	store := NewSecuritySecretsOIDCKeyStore(client, enc)

	_, _, err := store.Get(ctx, "test_key")
	if err == nil {
		t.Error("Get should return error when database is closed")
	}
}

func TestSecuritySecretsOIDCKeyStore_Integration_WithOIDCKeyService(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	// Use b64Encryptor from oidc_key_service_test.go (reversible test encryptor)
	enc := b64Encryptor{}
	store := NewSecuritySecretsOIDCKeyStore(client, enc)
	svc := NewOIDCKeyService(store, enc)

	// EnsureKey should generate and store keys
	if err := svc.EnsureKey(ctx); err != nil {
		t.Fatalf("EnsureKey failed: %v", err)
	}

	kid := svc.CurrentKID()
	if kid == "" {
		t.Fatal("CurrentKID is empty after EnsureKey")
	}

	// Verify keys persisted to security_secrets table
	count, err := client.SecuritySecret.Query().Count(ctx)
	if err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count < 2 {
		t.Errorf("expected at least 2 security_secrets rows (kid + key), got %d", count)
	}

	// Verify current_kid is stored
	kidSecret, err := client.SecuritySecret.Query().
		Where(securitysecret.KeyEQ("oidc_signing_current_kid")).
		Only(ctx)
	if err != nil {
		t.Fatalf("query current_kid failed: %v", err)
	}
	if kidSecret.Value != kid {
		t.Errorf("stored kid = %q, want %q", kidSecret.Value, kid)
	}

	// Verify signing key is stored encrypted
	keySecret, err := client.SecuritySecret.Query().
		Where(securitysecret.KeyEQ("oidc_signing_key_rs256_" + kid)).
		Only(ctx)
	if err != nil {
		t.Fatalf("query signing key failed: %v", err)
	}
	if keySecret.Value == "" {
		t.Error("stored signing key value is empty")
	}
	if len(keySecret.Value) < 10 || keySecret.Value[:4] != "enc:" {
		t.Errorf("signing key should be encrypted (b64Encryptor prefix 'enc:'), got: %s", keySecret.Value[:min(20, len(keySecret.Value))])
	}

	// Create a second service instance over the same store
	svc2 := NewOIDCKeyService(store, enc)
	if err := svc2.EnsureKey(ctx); err != nil {
		t.Fatalf("second EnsureKey failed: %v", err)
	}

	// Should load the same key (kid stability)
	if svc2.CurrentKID() != kid {
		t.Errorf("second service kid = %q, want %q (key should persist across instances)", svc2.CurrentKID(), kid)
	}
}

func TestSecuritySecretsOIDCKeyStore_DecryptFailure_Handling(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	// Store a key with valid encryption
	goodEnc := &mockEncryptor{}
	goodStore := NewSecuritySecretsOIDCKeyStore(client, goodEnc)

	kid := "test_kid_decrypt_fail"
	err := goodStore.Put(ctx, "oidc_signing_current_kid", kid)
	if err != nil {
		t.Fatalf("setup Put current_kid failed: %v", err)
	}
	err = goodStore.Put(ctx, "oidc_signing_key_rs256_"+kid, "encrypted:dummy_key")
	if err != nil {
		t.Fatalf("setup Put key failed: %v", err)
	}

	// Create a service with an encryptor that fails on decrypt
	badEnc := &mockEncryptor{decryptErr: errors.New("decryption failed")}
	badStore := NewSecuritySecretsOIDCKeyStore(client, badEnc)
	svc := NewOIDCKeyService(badStore, badEnc)

	// EnsureKey should fail when it cannot decrypt the stored key
	err = svc.EnsureKey(ctx)
	if err == nil {
		t.Fatal("EnsureKey should fail when decryption fails")
	}
	if svc.CurrentKID() != "" {
		t.Errorf("CurrentKID should be empty after failed EnsureKey, got %q", svc.CurrentKID())
	}
}

func TestSecuritySecretsOIDCKeyStore_EmptyValue_Rejected(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	enc := &mockEncryptor{}
	store := NewSecuritySecretsOIDCKeyStore(client, enc)

	// Empty values should be rejected by ent validator
	err := store.Put(ctx, "oidc_signing_current_kid", "")
	if err == nil {
		t.Fatal("Put should reject empty value due to ent validator")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
