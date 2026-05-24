package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"

	"golang.org/x/crypto/bcrypt"
)

// ── stubs ───────────────────────────────────────────────────────────────────

type stubClientRepo struct {
	clients map[string]*OAuthClient
}

func (s *stubClientRepo) GetClientByID(_ context.Context, id string) (*OAuthClient, error) {
	if c, ok := s.clients[id]; ok {
		return c, nil
	}
	return nil, ErrOAuthClientNotFound
}

type stubCodeRepo struct {
	mu    sync.Mutex
	codes map[string]*OAuthCode
}

func newStubCodeRepo() *stubCodeRepo { return &stubCodeRepo{codes: map[string]*OAuthCode{}} }

func (s *stubCodeRepo) CreateCode(_ context.Context, c *OAuthCode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codes[c.CodeHash] = c
	return nil
}

func (s *stubCodeRepo) ConsumeCode(_ context.Context, h string, now time.Time) (*OAuthCode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.codes[h]
	if !ok {
		return nil, ErrOAuthCodeNotFound
	}
	if c.UsedAt != nil {
		return nil, ErrOAuthCodeAlreadyUsed
	}
	if !c.ExpiresAt.After(now) {
		return nil, ErrOAuthCodeExpired
	}
	c.UsedAt = &now
	return c, nil
}

func (s *stubCodeRepo) DeleteExpiredCodes(_ context.Context, _ time.Time) (int, error) {
	return 0, nil
}

type stubRefreshRepo struct {
	mu     sync.Mutex
	tokens map[string]*OAuthRefreshToken
}

func newStubRefreshRepo() *stubRefreshRepo {
	return &stubRefreshRepo{tokens: map[string]*OAuthRefreshToken{}}
}

func (s *stubRefreshRepo) CreateRefreshToken(_ context.Context, t *OAuthRefreshToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tokens[t.TokenHash]; exists {
		return errors.New("duplicate token_hash")
	}
	cp := *t
	s.tokens[t.TokenHash] = &cp
	return nil
}

func (s *stubRefreshRepo) ConsumeForRotation(_ context.Context, oldHash, newHash string, now time.Time) (*OAuthRefreshToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.tokens[oldHash]
	if !ok {
		return nil, ErrOAuthRefreshTokenNotFound
	}
	if row.RevokedAt != nil {
		return nil, ErrOAuthRefreshTokenRevoked
	}
	if !row.ExpiresAt.After(now) {
		return nil, ErrOAuthRefreshTokenExpired
	}
	row.RevokedAt = &now
	row.RotatedToHash = &newHash
	cp := *row
	return &cp, nil
}

func (s *stubRefreshRepo) RevokeRefreshTokensByAPIKeyID(_ context.Context, apiKeyID int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range s.tokens {
		if row.APIKeyID == apiKeyID && row.RevokedAt == nil {
			t := now
			row.RevokedAt = &t
		}
	}
	return nil
}

func (s *stubRefreshRepo) ListActiveByUserAndClient(_ context.Context, userID int64, clientID string, now time.Time) ([]*OAuthRefreshToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []*OAuthRefreshToken{}
	for _, row := range s.tokens {
		if row.UserID != userID || row.ClientID != clientID {
			continue
		}
		if row.RevokedAt != nil {
			continue
		}
		if !row.ExpiresAt.After(now) {
			continue
		}
		cp := *row
		out = append(out, &cp)
	}
	return out, nil
}

// stubAPIKeyRepo is a minimal in-memory APIKeyRepository for OAuth tests.
type stubAPIKeyRepo struct {
	mu     sync.Mutex
	nextID int64
	rows   map[int64]*APIKey
}

func newStubAPIKeyRepo() *stubAPIKeyRepo {
	return &stubAPIKeyRepo{rows: map[int64]*APIKey{}}
}

func (s *stubAPIKeyRepo) Create(_ context.Context, k *APIKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	k.ID = s.nextID
	cp := *k
	s.rows[k.ID] = &cp
	return nil
}

func (s *stubAPIKeyRepo) GetByID(_ context.Context, id int64) (*APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[id]
	if !ok {
		return nil, ErrAPIKeyNotFound
	}
	cp := *r
	return &cp, nil
}

func (s *stubAPIKeyRepo) Update(_ context.Context, k *APIKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rows[k.ID]; !ok {
		return ErrAPIKeyNotFound
	}
	cp := *k
	s.rows[k.ID] = &cp
	return nil
}

// Remaining methods are unused by the OAuth service; provide trivial stubs.
func (s *stubAPIKeyRepo) GetKeyAndOwnerID(_ context.Context, _ int64) (string, int64, error) {
	return "", 0, nil
}
func (s *stubAPIKeyRepo) GetByKey(_ context.Context, _ string) (*APIKey, error) {
	return nil, ErrAPIKeyNotFound
}
func (s *stubAPIKeyRepo) GetByKeyForAuth(_ context.Context, _ string) (*APIKey, error) {
	return nil, ErrAPIKeyNotFound
}
func (s *stubAPIKeyRepo) Delete(_ context.Context, _ int64) error { return nil }
func (s *stubAPIKeyRepo) ListByUserID(_ context.Context, _ int64, _ pagination.PaginationParams, _ APIKeyListFilters) ([]APIKey, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (s *stubAPIKeyRepo) VerifyOwnership(_ context.Context, _ int64, ids []int64) ([]int64, error) {
	return ids, nil
}
func (s *stubAPIKeyRepo) CountByUserID(_ context.Context, _ int64) (int64, error) { return 0, nil }
func (s *stubAPIKeyRepo) ExistsByKey(_ context.Context, _ string) (bool, error)   { return false, nil }
func (s *stubAPIKeyRepo) ListByGroupID(_ context.Context, _ int64, _ pagination.PaginationParams) ([]APIKey, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (s *stubAPIKeyRepo) SearchAPIKeys(_ context.Context, _ int64, _ string, _ int) ([]APIKey, error) {
	return nil, nil
}
func (s *stubAPIKeyRepo) ClearGroupIDByGroupID(_ context.Context, _ int64) (int64, error) {
	return 0, nil
}
func (s *stubAPIKeyRepo) UpdateGroupIDByUserAndGroup(_ context.Context, _, _, _ int64) (int64, error) {
	return 0, nil
}
func (s *stubAPIKeyRepo) CountByGroupID(_ context.Context, _ int64) (int64, error) { return 0, nil }
func (s *stubAPIKeyRepo) ListKeysByUserID(_ context.Context, _ int64) ([]string, error) {
	return nil, nil
}
func (s *stubAPIKeyRepo) ListKeysByGroupID(_ context.Context, _ int64) ([]string, error) {
	return nil, nil
}
func (s *stubAPIKeyRepo) IncrementQuotaUsed(_ context.Context, _ int64, _ float64) (float64, error) {
	return 0, nil
}
func (s *stubAPIKeyRepo) UpdateLastUsed(_ context.Context, _ int64, _ time.Time) error { return nil }
func (s *stubAPIKeyRepo) IncrementRateLimitUsage(_ context.Context, _ int64, _ float64) error {
	return nil
}
func (s *stubAPIKeyRepo) ResetRateLimitWindows(_ context.Context, _ int64) error { return nil }
func (s *stubAPIKeyRepo) GetRateLimitData(_ context.Context, _ int64) (*APIKeyRateLimitData, error) {
	return &APIKeyRateLimitData{}, nil
}

type stubSettingRepo struct {
	values map[string]string
}

func (s *stubSettingRepo) Get(_ context.Context, key string) (*Setting, error) {
	if v, ok := s.values[key]; ok {
		return &Setting{Key: key, Value: v}, nil
	}
	return nil, errors.New("not found")
}

func (s *stubSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	if v, ok := s.values[key]; ok {
		return v, nil
	}
	return "", errors.New("not found")
}

func (s *stubSettingRepo) Set(_ context.Context, key, value string) error {
	s.values[key] = value
	return nil
}

func (s *stubSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, k := range keys {
		if v, ok := s.values[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func (s *stubSettingRepo) SetMultiple(_ context.Context, settings map[string]string) error {
	for k, v := range settings {
		s.values[k] = v
	}
	return nil
}

func (s *stubSettingRepo) GetAll(_ context.Context) (map[string]string, error) {
	out := make(map[string]string, len(s.values))
	for k, v := range s.values {
		out[k] = v
	}
	return out, nil
}

func (s *stubSettingRepo) Delete(_ context.Context, key string) error {
	delete(s.values, key)
	return nil
}

// ── helpers ─────────────────────────────────────────────────────────────────

// pkceVerifierAndChallenge returns (verifier, BASE64URL(SHA256(verifier))).
func pkceVerifierAndChallenge(verifier string) (string, string) {
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func newServiceUnderTest(t *testing.T) (*OAuthProviderService, *stubAPIKeyRepo, *stubRefreshRepo) {
	t.Helper()
	groupID := int64(5)
	clientRepo := &stubClientRepo{clients: map[string]*OAuthClient{
		"sakrylle-image-playground": {
			ClientID: "sakrylle-image-playground",
			Name:     "Sakrylle Image Playground",
			RedirectURIs: []string{
				"https://image.sakrylle.com/oauth/callback",
				"http://localhost:5173/oauth/callback",
			},
			AllowedScopes:          []string{"image_generation", "balance:read", "models:read"},
			PKCERequired:           true,
			DefaultGroupID:         &groupID,
			AccessTokenTTLSeconds:  86400,
			RefreshTokenTTLSeconds: 2592000,
		},
		"disabled-client": {
			ClientID:     "disabled-client",
			Name:         "Disabled Client",
			RedirectURIs: []string{"https://example.com/cb"},
			Disabled:     true,
		},
	}}
	apiKeyRepo := newStubAPIKeyRepo()
	refreshRepo := newStubRefreshRepo()
	settingRepo := &stubSettingRepo{values: map[string]string{
		"oauth_provider_enabled": "true",
		"oauth_default_group_id": "5",
	}}
	svc := NewOAuthProviderService(clientRepo, newStubCodeRepo(), refreshRepo, apiKeyRepo, nil, settingRepo, nil)
	return svc, apiKeyRepo, refreshRepo
}

// ── tests ───────────────────────────────────────────────────────────────────

func TestPKCES256VerifierMatchesChallenge(t *testing.T) {
	verifier, challenge := pkceVerifierAndChallenge("abcdefghijklmnopqrstuvwxyz0123456789-._~")
	if !verifyPKCES256(challenge, verifier) {
		t.Fatalf("verifyPKCES256 should accept matching verifier/challenge")
	}
	if verifyPKCES256(challenge, verifier+"x") {
		t.Fatalf("verifyPKCES256 should reject tampered verifier")
	}
	if verifyPKCES256("", verifier) {
		t.Fatalf("verifyPKCES256 should reject empty challenge")
	}
}

func TestRedirectURIWhitelist(t *testing.T) {
	allowed := []string{
		"https://image.sakrylle.com/oauth/callback",
		"http://localhost:5173/oauth/callback",
	}
	cases := map[string]bool{
		"https://image.sakrylle.com/oauth/callback":  true,
		"http://localhost:5173/oauth/callback":       true,
		"https://image.sakrylle.com/oauth/callback?": false,
		"https://evil.example.com/oauth/callback":    false,
		"":          false,
		"not a url": false,
	}
	for input, want := range cases {
		if got := redirectURIAllowed(allowed, input); got != want {
			t.Errorf("redirectURIAllowed(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestScopesAllowed(t *testing.T) {
	allowed := []string{"image_generation", "balance:read", "models:read"}
	cases := map[string]struct {
		req  []string
		want bool
	}{
		"empty":    {nil, true},
		"subset":   {[]string{"image_generation"}, true},
		"all":      {[]string{"image_generation", "balance:read", "models:read"}, true},
		"unknown":  {[]string{"image_generation", "admin"}, false},
		"only-bad": {[]string{"admin"}, false},
	}
	for name, c := range cases {
		if got := scopesAllowed(allowed, c.req); got != c.want {
			t.Errorf("%s: scopesAllowed = %v, want %v", name, got, c.want)
		}
	}
}

func TestParseScopesDeduplicates(t *testing.T) {
	got := ParseScopes("image_generation balance:read image_generation models:read")
	want := []string{"image_generation", "balance:read", "models:read"}
	if len(got) != len(want) {
		t.Fatalf("ParseScopes returned %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("ParseScopes[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAuthorizeRequestValidation(t *testing.T) {
	svc, _, _ := newServiceUnderTest(t)
	ctx := context.Background()
	_, challenge := pkceVerifierAndChallenge("the-quick-brown-fox-jumps-over-the-lazy-dog-12345")

	cases := []struct {
		name    string
		req     *AuthorizeRequest
		wantErr error
	}{
		{
			"happy_path",
			&AuthorizeRequest{
				ClientID:            "sakrylle-image-playground",
				RedirectURI:         "https://image.sakrylle.com/oauth/callback",
				ResponseType:        "code",
				Scopes:              []string{"image_generation"},
				State:               "abc",
				CodeChallenge:       challenge,
				CodeChallengeMethod: "S256",
			},
			nil,
		},
		{
			"wrong_response_type",
			&AuthorizeRequest{
				ClientID:     "sakrylle-image-playground",
				RedirectURI:  "https://image.sakrylle.com/oauth/callback",
				ResponseType: "token",
				State:        "abc",
			},
			ErrOAuthInvalidResponseType,
		},
		{
			"missing_state",
			&AuthorizeRequest{
				ClientID:     "sakrylle-image-playground",
				RedirectURI:  "https://image.sakrylle.com/oauth/callback",
				ResponseType: "code",
			},
			ErrOAuthMissingState,
		},
		{
			"unknown_client",
			&AuthorizeRequest{
				ClientID:     "ghost",
				RedirectURI:  "https://image.sakrylle.com/oauth/callback",
				ResponseType: "code",
				State:        "abc",
			},
			ErrOAuthClientNotFound,
		},
		{
			"disabled_client",
			&AuthorizeRequest{
				ClientID:     "disabled-client",
				RedirectURI:  "https://example.com/cb",
				ResponseType: "code",
				State:        "abc",
			},
			ErrOAuthClientDisabled,
		},
		{
			"redirect_uri_mismatch",
			&AuthorizeRequest{
				ClientID:     "sakrylle-image-playground",
				RedirectURI:  "https://evil.example.com/cb",
				ResponseType: "code",
				State:        "abc",
			},
			ErrOAuthInvalidRedirectURI,
		},
		{
			"scope_not_allowed",
			&AuthorizeRequest{
				ClientID:     "sakrylle-image-playground",
				RedirectURI:  "https://image.sakrylle.com/oauth/callback",
				ResponseType: "code",
				Scopes:       []string{"admin"},
				State:        "abc",
			},
			ErrOAuthInvalidScope,
		},
		{
			"missing_pkce",
			&AuthorizeRequest{
				ClientID:     "sakrylle-image-playground",
				RedirectURI:  "https://image.sakrylle.com/oauth/callback",
				ResponseType: "code",
				State:        "abc",
			},
			ErrOAuthMissingPKCE,
		},
		{
			"wrong_challenge_method",
			&AuthorizeRequest{
				ClientID:            "sakrylle-image-playground",
				RedirectURI:         "https://image.sakrylle.com/oauth/callback",
				ResponseType:        "code",
				State:               "abc",
				CodeChallenge:       challenge,
				CodeChallengeMethod: "plain",
			},
			ErrOAuthUnsupportedChallenge,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.ValidateAuthorizeRequest(ctx, tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got err=%v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestPKCEFullFlow(t *testing.T) {
	svc, apiKeyRepo, refreshRepo := newServiceUnderTest(t)
	ctx := context.Background()
	verifier, challenge := pkceVerifierAndChallenge("u-uuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuuu")

	req := &AuthorizeRequest{
		ClientID:            "sakrylle-image-playground",
		RedirectURI:         "https://image.sakrylle.com/oauth/callback",
		ResponseType:        "code",
		Scopes:              []string{"image_generation", "balance:read"},
		State:               "rnd-state",
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
	}
	client, err := svc.ValidateAuthorizeRequest(ctx, req)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	const userID int64 = 42

	// First issuance + wrong PKCE verifier — code is consumed (single-use) on PKCE failure.
	issuedCode, err := svc.IssueAuthorizationCode(ctx, client, userID, req)
	if err != nil {
		t.Fatalf("issue code: %v", err)
	}
	if _, err := svc.ExchangeAuthorizationCode(ctx, client.ClientID, "", issuedCode.Code, req.RedirectURI, "wrong-verifier"); !errors.Is(err, ErrOAuthPKCEFailed) {
		t.Fatalf("wrong verifier: got err=%v, want ErrOAuthPKCEFailed", err)
	}
	// Second attempt against the same code → already used.
	if _, err := svc.ExchangeAuthorizationCode(ctx, client.ClientID, "", issuedCode.Code, req.RedirectURI, verifier); !errors.Is(err, ErrOAuthCodeAlreadyUsed) {
		t.Fatalf("code replay: got err=%v, want ErrOAuthCodeAlreadyUsed", err)
	}

	// Fresh issuance for the happy path.
	issuedCode, err = svc.IssueAuthorizationCode(ctx, client, userID, req)
	if err != nil {
		t.Fatalf("re-issue: %v", err)
	}
	if issuedCode.State != "rnd-state" {
		t.Fatalf("state echo broken: got %q", issuedCode.State)
	}

	tok, err := svc.ExchangeAuthorizationCode(ctx, client.ClientID, "", issuedCode.Code, req.RedirectURI, verifier)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if !strings.HasPrefix(tok.AccessToken, "sk_oauth_") {
		t.Fatalf("access_token must have sk_oauth_ prefix, got %q", tok.AccessToken)
	}
	if !strings.HasPrefix(tok.RefreshToken, "rt_") {
		t.Fatalf("refresh_token must have rt_ prefix, got %q", tok.RefreshToken)
	}
	if tok.ExpiresIn != 86400 {
		t.Fatalf("expires_in: got %d, want 86400", tok.ExpiresIn)
	}
	if !IsOAuthAccessToken(tok.AccessToken) {
		t.Fatalf("IsOAuthAccessToken should accept issued token")
	}

	// api_keys row was created with the right group binding.
	if got := len(apiKeyRepo.rows); got != 1 {
		t.Fatalf("expected 1 api_keys row, got %d", got)
	}
	for _, row := range apiKeyRepo.rows {
		if row.UserID != userID {
			t.Errorf("api_key.user_id = %d, want %d", row.UserID, userID)
		}
		if row.GroupID == nil || *row.GroupID != 5 {
			t.Errorf("api_key.group_id = %v, want 5", row.GroupID)
		}
		if row.Status != StatusAPIKeyActive {
			t.Errorf("api_key.status = %q, want active", row.Status)
		}
		if row.ExpiresAt == nil {
			t.Error("api_key.expires_at must be set")
		}
	}

	// Refresh: new access + new refresh, old refresh rotated.
	oldRefreshHash := hashOAuthToken(tok.RefreshToken)
	tok2, err := svc.RefreshAccessToken(ctx, client.ClientID, "", tok.RefreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if tok2.AccessToken == tok.AccessToken {
		t.Error("refresh must produce a new access_token")
	}
	if tok2.RefreshToken == tok.RefreshToken {
		t.Error("refresh must rotate the refresh_token")
	}
	old, ok := refreshRepo.tokens[oldRefreshHash]
	if !ok {
		t.Fatalf("old refresh row missing")
	}
	if old.RevokedAt == nil {
		t.Error("old refresh row should be revoked")
	}
	if old.RotatedToHash == nil {
		t.Error("old refresh row should record rotation target")
	}
	// Replaying the old refresh fails.
	if _, err := svc.RefreshAccessToken(ctx, client.ClientID, "", tok.RefreshToken); !errors.Is(err, ErrOAuthRefreshTokenRevoked) {
		t.Fatalf("refresh replay: got err=%v, want ErrOAuthRefreshTokenRevoked", err)
	}
}

func TestExchangeRedirectURIMismatch(t *testing.T) {
	svc, _, _ := newServiceUnderTest(t)
	ctx := context.Background()
	verifier, challenge := pkceVerifierAndChallenge("verifier-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")
	req := &AuthorizeRequest{
		ClientID:            "sakrylle-image-playground",
		RedirectURI:         "https://image.sakrylle.com/oauth/callback",
		ResponseType:        "code",
		Scopes:              []string{"image_generation"},
		State:               "abc",
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
	}
	client, err := svc.ValidateAuthorizeRequest(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := svc.IssueAuthorizationCode(ctx, client, 42, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ExchangeAuthorizationCode(ctx, client.ClientID, "", issued.Code, "http://localhost:5173/oauth/callback", verifier); !errors.Is(err, ErrOAuthRedirectMismatch) {
		t.Fatalf("got err=%v, want ErrOAuthRedirectMismatch", err)
	}
}

func TestProviderDisabled(t *testing.T) {
	svc, _, _ := newServiceUnderTest(t)
	if !svc.IsEnabled(context.Background()) {
		t.Fatal("expected enabled by default")
	}
	repo := &stubSettingRepo{values: map[string]string{"oauth_provider_enabled": "false"}}
	svc.settingRepo = repo
	if svc.IsEnabled(context.Background()) {
		t.Fatal("expected disabled")
	}
}

// newConfidentialServiceUnderTest builds an isolated service whose only
// registered client is a confidential one (PKCERequired=false +
// client_secret_hash). It does not touch the existing fixture used by other
// tests so they remain unaffected.
func newConfidentialServiceUnderTest(t *testing.T, secret string) *OAuthProviderService {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt hash: %v", err)
	}
	groupID := int64(5)
	clientRepo := &stubClientRepo{clients: map[string]*OAuthClient{
		"confidential-client": {
			ClientID:               "confidential-client",
			Name:                   "Confidential Test Client",
			ClientSecretHash:       string(hash),
			RedirectURIs:           []string{"https://confidential.example.com/cb"},
			AllowedScopes:          []string{"image_generation"},
			PKCERequired:           false,
			DefaultGroupID:         &groupID,
			AccessTokenTTLSeconds:  86400,
			RefreshTokenTTLSeconds: 2592000,
		},
	}}
	apiKeyRepo := newStubAPIKeyRepo()
	refreshRepo := newStubRefreshRepo()
	settingRepo := &stubSettingRepo{values: map[string]string{
		"oauth_provider_enabled": "true",
		"oauth_default_group_id": "5",
	}}
	return NewOAuthProviderService(clientRepo, newStubCodeRepo(), refreshRepo, apiKeyRepo, nil, settingRepo, nil)
}

// TestExchangeAuthorizationCodeConfidentialClient covers the bcrypt-secret
// branch of authenticateClient, which the PKCE-only flow does not exercise.
func TestExchangeAuthorizationCodeConfidentialClient(t *testing.T) {
	const secret = "super-secret-confidential-client-token"
	ctx := context.Background()

	// Sanity: ValidateAuthorizeRequest accepts a request with an empty
	// CodeChallenge for confidential clients (PKCERequired=false).
	t.Run("validate_no_pkce_required", func(t *testing.T) {
		svc := newConfidentialServiceUnderTest(t, secret)
		req := &AuthorizeRequest{
			ClientID:     "confidential-client",
			RedirectURI:  "https://confidential.example.com/cb",
			ResponseType: "code",
			Scopes:       []string{"image_generation"},
			State:        "abc",
		}
		client, err := svc.ValidateAuthorizeRequest(ctx, req)
		if err != nil {
			t.Fatalf("validate confidential client: %v", err)
		}
		if client.ClientID != "confidential-client" {
			t.Fatalf("got client_id=%q", client.ClientID)
		}
	})

	t.Run("happy_path_correct_secret", func(t *testing.T) {
		svc := newConfidentialServiceUnderTest(t, secret)
		req := &AuthorizeRequest{
			ClientID:     "confidential-client",
			RedirectURI:  "https://confidential.example.com/cb",
			ResponseType: "code",
			Scopes:       []string{"image_generation"},
			State:        "abc",
		}
		client, err := svc.ValidateAuthorizeRequest(ctx, req)
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		issued, err := svc.IssueAuthorizationCode(ctx, client, 42, req)
		if err != nil {
			t.Fatalf("issue: %v", err)
		}
		// PKCERequired=false AND no challenge stored on the code → exchange
		// must NOT require a verifier.
		tok, err := svc.ExchangeAuthorizationCode(ctx, client.ClientID, secret, issued.Code, req.RedirectURI, "")
		if err != nil {
			t.Fatalf("exchange: %v", err)
		}
		if !strings.HasPrefix(tok.AccessToken, "sk_oauth_") {
			t.Fatalf("access_token must have sk_oauth_ prefix, got %q", tok.AccessToken)
		}
		if !strings.HasPrefix(tok.RefreshToken, "rt_") {
			t.Fatalf("refresh_token must have rt_ prefix, got %q", tok.RefreshToken)
		}
	})

	t.Run("wrong_secret_does_not_consume_code", func(t *testing.T) {
		svc := newConfidentialServiceUnderTest(t, secret)
		req := &AuthorizeRequest{
			ClientID:     "confidential-client",
			RedirectURI:  "https://confidential.example.com/cb",
			ResponseType: "code",
			Scopes:       []string{"image_generation"},
			State:        "abc",
		}
		client, err := svc.ValidateAuthorizeRequest(ctx, req)
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		issued, err := svc.IssueAuthorizationCode(ctx, client, 42, req)
		if err != nil {
			t.Fatalf("issue: %v", err)
		}
		// Wrong secret → ErrOAuthClientAuthFailed.
		if _, err := svc.ExchangeAuthorizationCode(ctx, client.ClientID, "not-the-secret", issued.Code, req.RedirectURI, ""); !errors.Is(err, ErrOAuthClientAuthFailed) {
			t.Fatalf("wrong secret: got err=%v, want ErrOAuthClientAuthFailed", err)
		}
		// Critical invariant: client auth happens BEFORE code consumption,
		// so a follow-up call with the correct secret must still succeed.
		tok, err := svc.ExchangeAuthorizationCode(ctx, client.ClientID, secret, issued.Code, req.RedirectURI, "")
		if err != nil {
			t.Fatalf("retry with correct secret: %v (code was wrongly consumed by the failed auth attempt)", err)
		}
		if !strings.HasPrefix(tok.AccessToken, "sk_oauth_") {
			t.Fatalf("access_token must have sk_oauth_ prefix, got %q", tok.AccessToken)
		}
	})

	t.Run("empty_secret", func(t *testing.T) {
		svc := newConfidentialServiceUnderTest(t, secret)
		req := &AuthorizeRequest{
			ClientID:     "confidential-client",
			RedirectURI:  "https://confidential.example.com/cb",
			ResponseType: "code",
			Scopes:       []string{"image_generation"},
			State:        "abc",
		}
		client, err := svc.ValidateAuthorizeRequest(ctx, req)
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		issued, err := svc.IssueAuthorizationCode(ctx, client, 42, req)
		if err != nil {
			t.Fatalf("issue: %v", err)
		}
		if _, err := svc.ExchangeAuthorizationCode(ctx, client.ClientID, "", issued.Code, req.RedirectURI, ""); !errors.Is(err, ErrOAuthClientAuthFailed) {
			t.Fatalf("empty secret: got err=%v, want ErrOAuthClientAuthFailed", err)
		}
	})
}
