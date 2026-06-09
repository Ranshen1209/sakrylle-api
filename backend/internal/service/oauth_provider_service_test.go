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

func (s *stubClientRepo) ListEnabledRedirectURIs(_ context.Context) ([]string, error) {
	out := make([]string, 0)
	for _, c := range s.clients {
		if c.Disabled {
			continue
		}
		out = append(out, c.RedirectURIs...)
	}
	return out, nil
}

func (s *stubClientRepo) ListClientsWithFrontchannelLogout(_ context.Context) ([]*OAuthClient, error) {
	var out []*OAuthClient
	for _, c := range s.clients {
		if !c.Disabled && c.FrontchannelLogoutURI != nil && *c.FrontchannelLogoutURI != "" {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *stubClientRepo) ListClientsWithBackchannelLogout(_ context.Context) ([]*OAuthClient, error) {
	var out []*OAuthClient
	for _, c := range s.clients {
		if !c.Disabled && c.BackchannelLogoutURI != nil && *c.BackchannelLogoutURI != "" {
			out = append(out, c)
		}
	}
	return out, nil
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

func (s *stubRefreshRepo) ListActiveByUser(_ context.Context, userID int64, now time.Time) ([]*OAuthRefreshToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []*OAuthRefreshToken{}
	for _, row := range s.tokens {
		if row.UserID != userID {
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

// ── v2 fake — grant / family / hash-lookup operations ─────────────────────
//
// In-memory fakes for the v2 OAuthRefreshTokenRepository surface. They
// share the same map as the legacy stubs above so tests can mix flows.

func (s *stubRefreshRepo) GetRefreshTokenByHashForUpdate(_ context.Context, tokenHash string, _ time.Time) (*OAuthRefreshToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.tokens[tokenHash]
	if !ok {
		return nil, ErrOAuthRefreshTokenNotFound
	}
	cp := *row
	return &cp, nil
}

func (s *stubRefreshRepo) RevokeRefreshTokensByGrantID(_ context.Context, grantID string, now time.Time) ([]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []int64
	for _, row := range s.tokens {
		if row.GrantID == nil || *row.GrantID != grantID {
			continue
		}
		if row.RevokedAt != nil {
			continue
		}
		t := now
		row.RevokedAt = &t
		ids = append(ids, row.APIKeyID)
	}
	return ids, nil
}

func (s *stubRefreshRepo) RevokeRefreshTokensByTokenFamilyID(_ context.Context, familyID string, now time.Time) ([]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []int64
	for _, row := range s.tokens {
		if row.TokenFamilyID == nil || *row.TokenFamilyID != familyID {
			continue
		}
		if row.RevokedAt != nil {
			continue
		}
		t := now
		row.RevokedAt = &t
		ids = append(ids, row.APIKeyID)
	}
	return ids, nil
}

func (s *stubRefreshRepo) RevokeRefreshTokensByUserAndClient(_ context.Context, userID int64, clientID string, now time.Time) ([]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []int64
	for _, row := range s.tokens {
		if row.UserID != userID || row.ClientID != clientID {
			continue
		}
		if row.RevokedAt != nil {
			continue
		}
		t := now
		row.RevokedAt = &t
		ids = append(ids, row.APIKeyID)
	}
	return ids, nil
}

func (s *stubRefreshRepo) MarkReuseDetected(_ context.Context, tokenHash string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if row, ok := s.tokens[tokenHash]; ok {
		t := now
		row.ReuseDetectedAt = &t
	}
	return nil
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
func (s *stubAPIKeyRepo) Delete(_ context.Context, _ int64) error          { return nil }
func (s *stubAPIKeyRepo) DeleteWithAudit(_ context.Context, _ int64) error { return nil }
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
			// §7.4 / §10.1 / §1993: legacy compat — image-playground frontend
			// has not yet migrated to request offline_access, so allow refresh
			// issuance without it. Mirrors the prod row state set by the
			// Sakrylle seed migration; a non-Sakrylle fork would set this to
			// false and require offline_access.
			AllowRefreshWithoutOfflineAccess: true,
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
	svc := newStubOAuthProviderService(clientRepo, newStubCodeRepo(), refreshRepo, apiKeyRepo, nil, settingRepo, nil)
	return svc, apiKeyRepo, refreshRepo
}

// newStubOAuthProviderService is the test-only constructor that wraps the v2
// signature. The legacy tests don't need access/device/authzTx repositories
// or a GroupAccessPolicy, so we pass nils — the v2 surface methods that
// require them have their own fixtures.
func newStubOAuthProviderService(
	clientRepo OAuthClientRepository,
	codeRepo OAuthCodeRepository,
	refreshRepo OAuthRefreshTokenRepository,
	apiKeyRepo APIKeyRepository,
	groupRepo GroupRepository,
	settingRepo SettingRepository,
	authCache APIKeyAuthCacheInvalidator,
) *OAuthProviderService {
	var oauthAPIKey OAuthAPIKeyRepository
	if v, ok := apiKeyRepo.(OAuthAPIKeyRepository); ok {
		oauthAPIKey = v
	}
	return NewOAuthProviderService(
		clientRepo,
		codeRepo,
		refreshRepo,
		nil, // accessRepo
		nil, // deviceRepo
		nil, // authzTxRepo
		oauthAPIKeyOrAdapter(apiKeyRepo, oauthAPIKey),
		groupRepo,
		nil, // groupAccess
		settingRepo,
		authCache,
	)
}

// oauthAPIKeyOrAdapter wraps a plain APIKeyRepository so the v2 constructor
// signature (OAuthAPIKeyRepository) is satisfied in tests that don't need
// batch-disable semantics. The fallback per-key disable path inside the
// service uses the embedded APIKeyRepository.
func oauthAPIKeyOrAdapter(base APIKeyRepository, oa OAuthAPIKeyRepository) OAuthAPIKeyRepository {
	if oa != nil {
		return oa
	}
	if base == nil {
		return nil
	}
	return &testAPIKeyRepoAdapter{APIKeyRepository: base}
}

type testAPIKeyRepoAdapter struct {
	APIKeyRepository
}

func (a *testAPIKeyRepoAdapter) DisableAPIKeysByIDsReturningKeys(ctx context.Context, ids []int64, now time.Time) ([]string, error) {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		key, err := a.GetByID(ctx, id)
		if err != nil || key == nil {
			continue
		}
		out = append(out, key.Key)
		if key.Status == StatusAPIKeyDisabled {
			continue
		}
		key.Status = StatusAPIKeyDisabled
		_ = a.Update(ctx, key)
	}
	return out, nil
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
		"http://127.0.0.1/oauth/callback",
		"http://[::1]/oauth/callback",
	}
	cases := map[string]bool{
		"https://image.sakrylle.com/oauth/callback":  true,
		"http://localhost:5173/oauth/callback":       true,
		"http://127.0.0.1:49152/oauth/callback":      true,
		"http://[::1]:49152/oauth/callback":          true,
		"http://127.0.0.1/oauth/callback":            false,
		"http://127.0.0.1:49152/oauth/callback/":     false,
		"http://127.0.0.1:49152/oauth/callback?x=1":  false,
		"http://localhost.evil.test/oauth/callback":  false,
		"http://127.0.0.1.evil.test/oauth/callback":  false,
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
	tok2, err := svc.RefreshAccessToken(ctx, client.ClientID, "", tok.RefreshToken, nil)
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
	if _, err := svc.RefreshAccessToken(ctx, client.ClientID, "", tok.RefreshToken, nil); !errors.Is(err, ErrOAuthRefreshTokenRevoked) {
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
		t.Fatal("expected enabled when oauth_provider_enabled=true")
	}
	repo := &stubSettingRepo{values: map[string]string{"oauth_provider_enabled": "false"}}
	svc.settingRepo = repo
	if svc.IsEnabled(context.Background()) {
		t.Fatal("expected disabled when oauth_provider_enabled=false")
	}

	// FIX M1: fail closed on missing setting (no row at all).
	svc.settingRepo = &stubSettingRepo{values: map[string]string{}}
	if svc.IsEnabled(context.Background()) {
		t.Fatal("expected disabled when oauth_provider_enabled is missing (fail closed)")
	}

	// FIX M1: fail closed on unparseable / unrecognized value.
	svc.settingRepo = &stubSettingRepo{values: map[string]string{"oauth_provider_enabled": "garbage"}}
	if svc.IsEnabled(context.Background()) {
		t.Fatal("expected disabled when oauth_provider_enabled is unparseable (fail closed)")
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
			// §7.4 / §10.1: this fixture predates the offline_access scope
			// gate; flip the legacy compat flag so the bcrypt-auth branch
			// keeps emitting a refresh_token without forcing every test to
			// thread offline_access through the AllowedScopes/Scopes lists.
			AllowRefreshWithoutOfflineAccess: true,
		},
	}}
	apiKeyRepo := newStubAPIKeyRepo()
	refreshRepo := newStubRefreshRepo()
	settingRepo := &stubSettingRepo{values: map[string]string{
		"oauth_provider_enabled": "true",
		"oauth_default_group_id": "5",
	}}
	return newStubOAuthProviderService(clientRepo, newStubCodeRepo(), refreshRepo, apiKeyRepo, nil, settingRepo, nil)
}

// TestExchangeAuthorizationCodeConfidentialClient covers the bcrypt-secret
// branch of authenticateClient, which the PKCE-only flow does not exercise.
func TestExchangeAuthorizationCodeConfidentialClient(t *testing.T) {
	const secret = "super-secret-confidential-client-token"
	ctx := context.Background()

	// PKCE S256 is mandatory for ALL clients (incl. confidential), so the
	// authorize request must carry a valid code_challenge and the token
	// exchange a matching code_verifier.
	verifier, challenge := pkceVerifierAndChallenge("verifier-confidential-pkce-aaaaaaaaaaaaaaaaaaaa")

	// Sanity: ValidateAuthorizeRequest now REJECTS a confidential client that
	// omits code_challenge (PKCERequired=false no longer exempts it).
	t.Run("validate_pkce_required_even_for_confidential", func(t *testing.T) {
		svc := newConfidentialServiceUnderTest(t, secret)
		req := &AuthorizeRequest{
			ClientID:     "confidential-client",
			RedirectURI:  "https://confidential.example.com/cb",
			ResponseType: "code",
			Scopes:       []string{"image_generation"},
			State:        "abc",
		}
		if _, err := svc.ValidateAuthorizeRequest(ctx, req); !errors.Is(err, ErrOAuthMissingPKCE) {
			t.Fatalf("confidential client without PKCE must be rejected; got err=%v, want ErrOAuthMissingPKCE", err)
		}
	})

	t.Run("happy_path_correct_secret", func(t *testing.T) {
		svc := newConfidentialServiceUnderTest(t, secret)
		req := &AuthorizeRequest{
			ClientID:            "confidential-client",
			RedirectURI:         "https://confidential.example.com/cb",
			ResponseType:        "code",
			Scopes:              []string{"image_generation"},
			State:               "abc",
			CodeChallenge:       challenge,
			CodeChallengeMethod: "S256",
		}
		client, err := svc.ValidateAuthorizeRequest(ctx, req)
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		issued, err := svc.IssueAuthorizationCode(ctx, client, 42, req)
		if err != nil {
			t.Fatalf("issue: %v", err)
		}
		// Confidential client must present both a valid secret and a matching
		// PKCE verifier.
		tok, err := svc.ExchangeAuthorizationCode(ctx, client.ClientID, secret, issued.Code, req.RedirectURI, verifier)
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
			ClientID:            "confidential-client",
			RedirectURI:         "https://confidential.example.com/cb",
			ResponseType:        "code",
			Scopes:              []string{"image_generation"},
			State:               "abc",
			CodeChallenge:       challenge,
			CodeChallengeMethod: "S256",
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
		if _, err := svc.ExchangeAuthorizationCode(ctx, client.ClientID, "not-the-secret", issued.Code, req.RedirectURI, verifier); !errors.Is(err, ErrOAuthClientAuthFailed) {
			t.Fatalf("wrong secret: got err=%v, want ErrOAuthClientAuthFailed", err)
		}
		// Critical invariant: client auth happens BEFORE code consumption,
		// so a follow-up call with the correct secret must still succeed.
		tok, err := svc.ExchangeAuthorizationCode(ctx, client.ClientID, secret, issued.Code, req.RedirectURI, verifier)
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
			ClientID:            "confidential-client",
			RedirectURI:         "https://confidential.example.com/cb",
			ResponseType:        "code",
			Scopes:              []string{"image_generation"},
			State:               "abc",
			CodeChallenge:       challenge,
			CodeChallengeMethod: "S256",
		}
		client, err := svc.ValidateAuthorizeRequest(ctx, req)
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		issued, err := svc.IssueAuthorizationCode(ctx, client, 42, req)
		if err != nil {
			t.Fatalf("issue: %v", err)
		}
		if _, err := svc.ExchangeAuthorizationCode(ctx, client.ClientID, "", issued.Code, req.RedirectURI, verifier); !errors.Is(err, ErrOAuthClientAuthFailed) {
			t.Fatalf("empty secret: got err=%v, want ErrOAuthClientAuthFailed", err)
		}
	})
}

// ── User-facing grants (ListUserGrants / RevokeUserGrant) ────────────────────

func TestListUserGrants_GroupsByClientAndUnionsScopes(t *testing.T) {
	svc, apiKeyRepo, refreshRepo := newServiceUnderTest(t)
	ctx := context.Background()
	const userID int64 = 7
	now := time.Now()

	// Two active tokens for the same client (different "devices"), distinct scopes.
	apiKey1 := &APIKey{UserID: userID, Key: "sk_oauth_dev1", Status: StatusAPIKeyActive, CreatedAt: now.Add(-3 * time.Hour)}
	if err := apiKeyRepo.Create(ctx, apiKey1); err != nil {
		t.Fatalf("create api_key 1: %v", err)
	}
	apiKey2 := &APIKey{UserID: userID, Key: "sk_oauth_dev2", Status: StatusAPIKeyActive, CreatedAt: now.Add(-1 * time.Hour)}
	if err := apiKeyRepo.Create(ctx, apiKey2); err != nil {
		t.Fatalf("create api_key 2: %v", err)
	}
	lastUsed := now.Add(-15 * time.Minute)
	apiKey2.LastUsedAt = &lastUsed
	if err := apiKeyRepo.Update(ctx, apiKey2); err != nil {
		t.Fatalf("update api_key 2: %v", err)
	}

	if err := refreshRepo.CreateRefreshToken(ctx, &OAuthRefreshToken{
		TokenHash: "h1", ClientID: "sakrylle-image-playground", UserID: userID, APIKeyID: apiKey1.ID,
		Scopes: []string{"image_generation"}, ExpiresAt: now.Add(720 * time.Hour),
	}); err != nil {
		t.Fatalf("create refresh 1: %v", err)
	}
	if err := refreshRepo.CreateRefreshToken(ctx, &OAuthRefreshToken{
		TokenHash: "h2", ClientID: "sakrylle-image-playground", UserID: userID, APIKeyID: apiKey2.ID,
		Scopes: []string{"image_generation", "balance:read"}, ExpiresAt: now.Add(720 * time.Hour),
	}); err != nil {
		t.Fatalf("create refresh 2: %v", err)
	}

	grants, err := svc.ListUserGrants(ctx, userID)
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	if len(grants) != 1 {
		t.Fatalf("want 1 grant (grouped by client_id), got %d", len(grants))
	}
	g := grants[0]
	if g.ClientID != "sakrylle-image-playground" {
		t.Errorf("client_id = %q", g.ClientID)
	}
	if g.ClientName != "Sakrylle Image Playground" {
		t.Errorf("client_name = %q", g.ClientName)
	}
	if g.ActiveTokenCount != 2 {
		t.Errorf("active_token_count = %d, want 2", g.ActiveTokenCount)
	}
	if !containsAll(g.Scopes, "image_generation", "balance:read") || len(g.Scopes) != 2 {
		t.Errorf("scopes union = %v, want [image_generation balance:read]", g.Scopes)
	}
	if g.LastUsedAt == nil || !g.LastUsedAt.Equal(lastUsed) {
		t.Errorf("last_used_at = %v, want %v", g.LastUsedAt, lastUsed)
	}
	// FirstAuthorizedAt should equal earliest api_keys.CreatedAt (apiKey1).
	if !g.FirstAuthorizedAt.Equal(apiKey1.CreatedAt) {
		t.Errorf("first_authorized_at = %v, want %v", g.FirstAuthorizedAt, apiKey1.CreatedAt)
	}
}

func TestListUserGrants_FiltersExpiredAndRevoked(t *testing.T) {
	svc, apiKeyRepo, refreshRepo := newServiceUnderTest(t)
	ctx := context.Background()
	const userID int64 = 8
	now := time.Now()

	active := &APIKey{UserID: userID, Key: "sk_oauth_active", Status: StatusAPIKeyActive}
	revokedKey := &APIKey{UserID: userID, Key: "sk_oauth_revoked", Status: StatusAPIKeyDisabled}
	expiredKey := &APIKey{UserID: userID, Key: "sk_oauth_expired", Status: StatusAPIKeyActive}
	for _, k := range []*APIKey{active, revokedKey, expiredKey} {
		if err := apiKeyRepo.Create(ctx, k); err != nil {
			t.Fatalf("create api_key: %v", err)
		}
	}

	revokedAt := now.Add(-1 * time.Hour)
	tokens := []*OAuthRefreshToken{
		{TokenHash: "ok", ClientID: "sakrylle-image-playground", UserID: userID, APIKeyID: active.ID, ExpiresAt: now.Add(720 * time.Hour)},
		{TokenHash: "rv", ClientID: "sakrylle-image-playground", UserID: userID, APIKeyID: revokedKey.ID, ExpiresAt: now.Add(720 * time.Hour), RevokedAt: &revokedAt},
		{TokenHash: "ex", ClientID: "sakrylle-image-playground", UserID: userID, APIKeyID: expiredKey.ID, ExpiresAt: now.Add(-10 * time.Minute)},
	}
	for _, tok := range tokens {
		if err := refreshRepo.CreateRefreshToken(ctx, tok); err != nil {
			t.Fatalf("create refresh: %v", err)
		}
	}
	// Manually set RevokedAt on the in-memory stub since CreateRefreshToken doesn't persist it.
	refreshRepo.tokens["rv"].RevokedAt = &revokedAt

	grants, err := svc.ListUserGrants(ctx, userID)
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	if len(grants) != 1 {
		t.Fatalf("want 1 grant (only the active token), got %d", len(grants))
	}
	if grants[0].ActiveTokenCount != 1 {
		t.Errorf("active_token_count = %d, want 1", grants[0].ActiveTokenCount)
	}
}

func TestListUserGrants_OrphanClientStillSurfaces(t *testing.T) {
	svc, apiKeyRepo, refreshRepo := newServiceUnderTest(t)
	ctx := context.Background()
	const userID int64 = 9
	now := time.Now()

	apiKey := &APIKey{UserID: userID, Key: "sk_oauth_orphan", Status: StatusAPIKeyActive}
	if err := apiKeyRepo.Create(ctx, apiKey); err != nil {
		t.Fatalf("create api_key: %v", err)
	}
	if err := refreshRepo.CreateRefreshToken(ctx, &OAuthRefreshToken{
		TokenHash: "orphan", ClientID: "deleted-client", UserID: userID, APIKeyID: apiKey.ID,
		Scopes: []string{"image_generation"}, ExpiresAt: now.Add(720 * time.Hour),
	}); err != nil {
		t.Fatalf("create refresh: %v", err)
	}

	grants, err := svc.ListUserGrants(ctx, userID)
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	if len(grants) != 1 {
		t.Fatalf("want 1 grant, got %d", len(grants))
	}
	if grants[0].ClientID != "deleted-client" {
		t.Errorf("client_id = %q", grants[0].ClientID)
	}
	if !grants[0].ClientDisabled {
		t.Error("orphan client should surface as ClientDisabled=true")
	}
}

func TestListUserGrants_NoGrants(t *testing.T) {
	svc, _, _ := newServiceUnderTest(t)
	grants, err := svc.ListUserGrants(context.Background(), 999)
	if err != nil {
		t.Fatalf("list grants: %v", err)
	}
	if len(grants) != 0 {
		t.Errorf("want 0 grants, got %d", len(grants))
	}
}

func TestRevokeUserGrant_RevokesTokensAndDisablesAPIKeys(t *testing.T) {
	svc, apiKeyRepo, refreshRepo := newServiceUnderTest(t)
	ctx := context.Background()
	const userID int64 = 11
	now := time.Now()

	apiKey1 := &APIKey{UserID: userID, Key: "sk_oauth_a", Status: StatusAPIKeyActive}
	apiKey2 := &APIKey{UserID: userID, Key: "sk_oauth_b", Status: StatusAPIKeyActive}
	for _, k := range []*APIKey{apiKey1, apiKey2} {
		if err := apiKeyRepo.Create(ctx, k); err != nil {
			t.Fatalf("create api_key: %v", err)
		}
	}
	for i, apiKey := range []*APIKey{apiKey1, apiKey2} {
		if err := refreshRepo.CreateRefreshToken(ctx, &OAuthRefreshToken{
			TokenHash: "tok" + string(rune('0'+i)),
			ClientID:  "sakrylle-image-playground",
			UserID:    userID, APIKeyID: apiKey.ID,
			Scopes:    []string{"image_generation"},
			ExpiresAt: now.Add(720 * time.Hour),
		}); err != nil {
			t.Fatalf("create refresh: %v", err)
		}
	}

	revoked, err := svc.RevokeUserGrant(ctx, userID, "sakrylle-image-playground", now)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if revoked != 2 {
		t.Errorf("revoked count = %d, want 2", revoked)
	}

	// Both api_keys disabled.
	for _, id := range []int64{apiKey1.ID, apiKey2.ID} {
		got, gerr := apiKeyRepo.GetByID(ctx, id)
		if gerr != nil {
			t.Fatalf("get api_key %d: %v", id, gerr)
		}
		if got.Status != StatusAPIKeyDisabled {
			t.Errorf("api_key %d status = %q, want disabled", id, got.Status)
		}
	}
	// All refresh tokens revoked.
	for hash, tok := range refreshRepo.tokens {
		if tok.RevokedAt == nil {
			t.Errorf("token %q should be revoked", hash)
		}
	}

	// Idempotent: second call on the same grant returns 0.
	again, err := svc.RevokeUserGrant(ctx, userID, "sakrylle-image-playground", now)
	if err != nil {
		t.Fatalf("second revoke: %v", err)
	}
	if again != 0 {
		t.Errorf("idempotent revoke = %d, want 0", again)
	}
}

func TestRevokeUserGrant_DoesNotTouchOtherUsers(t *testing.T) {
	svc, apiKeyRepo, refreshRepo := newServiceUnderTest(t)
	ctx := context.Background()
	now := time.Now()

	mineKey := &APIKey{UserID: 1, Key: "sk_oauth_mine", Status: StatusAPIKeyActive}
	otherKey := &APIKey{UserID: 2, Key: "sk_oauth_other", Status: StatusAPIKeyActive}
	for _, k := range []*APIKey{mineKey, otherKey} {
		if err := apiKeyRepo.Create(ctx, k); err != nil {
			t.Fatalf("create api_key: %v", err)
		}
	}
	for _, tok := range []*OAuthRefreshToken{
		{TokenHash: "mine", ClientID: "sakrylle-image-playground", UserID: 1, APIKeyID: mineKey.ID, ExpiresAt: now.Add(720 * time.Hour)},
		{TokenHash: "other", ClientID: "sakrylle-image-playground", UserID: 2, APIKeyID: otherKey.ID, ExpiresAt: now.Add(720 * time.Hour)},
	} {
		if err := refreshRepo.CreateRefreshToken(ctx, tok); err != nil {
			t.Fatalf("create refresh: %v", err)
		}
	}

	if _, err := svc.RevokeUserGrant(ctx, 1, "sakrylle-image-playground", now); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	other, _ := apiKeyRepo.GetByID(ctx, otherKey.ID)
	if other.Status != StatusAPIKeyActive {
		t.Errorf("other user's api_key was disabled — cross-user leak")
	}
	if refreshRepo.tokens["other"].RevokedAt != nil {
		t.Errorf("other user's refresh token was revoked — cross-user leak")
	}
}

// recordingAuthCacheInvalidator captures every InvalidateAuthCacheByKey call so
// tests can verify the Redis Pub/Sub failsafe is exercised for every revoked
// access_token. Without this assertion, a regression could leave revoked
// tokens valid for ~60s in the auth cache (CLAUDE.md hazard #2 under
// "Deepseek 双协议入口与缓存陷阱").
type recordingAuthCacheInvalidator struct {
	mu       sync.Mutex
	keyCalls []string
}

func (r *recordingAuthCacheInvalidator) InvalidateAuthCacheByKey(_ context.Context, key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keyCalls = append(r.keyCalls, key)
}

func (r *recordingAuthCacheInvalidator) InvalidateAuthCacheByUserID(_ context.Context, _ int64) {}
func (r *recordingAuthCacheInvalidator) InvalidateAuthCacheByGroupID(_ context.Context, _ int64) {
}

func (r *recordingAuthCacheInvalidator) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.keyCalls))
	copy(out, r.keyCalls)
	return out
}

func TestRevokeUserGrant_PublishesCacheInvalidationForEveryToken(t *testing.T) {
	groupID := int64(5)
	clientRepo := &stubClientRepo{clients: map[string]*OAuthClient{
		"sakrylle-image-playground": {
			ClientID:               "sakrylle-image-playground",
			Name:                   "Sakrylle Image Playground",
			RedirectURIs:           []string{"https://image.sakrylle.com/oauth/callback"},
			AllowedScopes:          []string{"image_generation"},
			PKCERequired:           true,
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
	cache := &recordingAuthCacheInvalidator{}
	svc := newStubOAuthProviderService(clientRepo, newStubCodeRepo(), refreshRepo, apiKeyRepo, nil, settingRepo, cache)

	ctx := context.Background()
	const userID int64 = 42
	now := time.Now()

	keys := []*APIKey{
		{UserID: userID, Key: "sk_oauth_dev1", Status: StatusAPIKeyActive},
		{UserID: userID, Key: "sk_oauth_dev2", Status: StatusAPIKeyActive},
		{UserID: userID, Key: "sk_oauth_dev3", Status: StatusAPIKeyActive},
	}
	for _, k := range keys {
		if err := apiKeyRepo.Create(ctx, k); err != nil {
			t.Fatalf("create api_key: %v", err)
		}
	}
	for i, k := range keys {
		if err := refreshRepo.CreateRefreshToken(ctx, &OAuthRefreshToken{
			TokenHash: "tok-" + string(rune('a'+i)),
			ClientID:  "sakrylle-image-playground",
			UserID:    userID, APIKeyID: k.ID,
			ExpiresAt: now.Add(720 * time.Hour),
		}); err != nil {
			t.Fatalf("create refresh: %v", err)
		}
	}

	revoked, err := svc.RevokeUserGrant(ctx, userID, "sakrylle-image-playground", now)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if revoked != 3 {
		t.Fatalf("revoked = %d, want 3", revoked)
	}

	calls := cache.snapshot()
	if len(calls) != 3 {
		t.Fatalf("cache invalidator called %d times, want 3 (one per token)", len(calls))
	}
	wantKeys := map[string]bool{"sk_oauth_dev1": true, "sk_oauth_dev2": true, "sk_oauth_dev3": true}
	for _, k := range calls {
		if !wantKeys[k] {
			t.Errorf("unexpected key in cache invalidator calls: %q", k)
		}
		delete(wantKeys, k)
	}
	if len(wantKeys) > 0 {
		t.Errorf("cache invalidator never called for: %v", wantKeys)
	}
}

// updateFailingAPIKeyRepo simulates an api_keys UPDATE failure so we can verify
// the cache invalidation fail-safe still runs (otherwise revoked tokens stay
// hot in cache for ~60s).
type updateFailingAPIKeyRepo struct {
	*stubAPIKeyRepo
}

func (r *updateFailingAPIKeyRepo) Update(_ context.Context, _ *APIKey) error {
	return errors.New("simulated update failure")
}

func TestRevokeUserGrant_InvalidatesCacheEvenWhenAPIKeyUpdateFails(t *testing.T) {
	groupID := int64(5)
	clientRepo := &stubClientRepo{clients: map[string]*OAuthClient{
		"sakrylle-image-playground": {
			ClientID:               "sakrylle-image-playground",
			RedirectURIs:           []string{"https://image.sakrylle.com/oauth/callback"},
			PKCERequired:           true,
			DefaultGroupID:         &groupID,
			AccessTokenTTLSeconds:  86400,
			RefreshTokenTTLSeconds: 2592000,
		},
	}}
	baseAPIKeyRepo := newStubAPIKeyRepo()
	apiKeyRepo := &updateFailingAPIKeyRepo{stubAPIKeyRepo: baseAPIKeyRepo}
	refreshRepo := newStubRefreshRepo()
	cache := &recordingAuthCacheInvalidator{}
	svc := newStubOAuthProviderService(clientRepo, newStubCodeRepo(), refreshRepo, apiKeyRepo, nil, &stubSettingRepo{values: map[string]string{}}, cache)

	ctx := context.Background()
	const userID int64 = 99
	now := time.Now()

	apiKey := &APIKey{UserID: userID, Key: "sk_oauth_zombie", Status: StatusAPIKeyActive}
	if err := baseAPIKeyRepo.Create(ctx, apiKey); err != nil {
		t.Fatalf("create api_key: %v", err)
	}
	if err := refreshRepo.CreateRefreshToken(ctx, &OAuthRefreshToken{
		TokenHash: "z", ClientID: "sakrylle-image-playground", UserID: userID, APIKeyID: apiKey.ID,
		ExpiresAt: now.Add(720 * time.Hour),
	}); err != nil {
		t.Fatalf("create refresh: %v", err)
	}

	revoked, err := svc.RevokeUserGrant(ctx, userID, "sakrylle-image-playground", now)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if revoked != 1 {
		t.Errorf("revoked = %d, want 1 (refresh token DB revoke succeeded)", revoked)
	}
	calls := cache.snapshot()
	if len(calls) != 1 || calls[0] != "sk_oauth_zombie" {
		t.Errorf("cache invalidator must fire even when api_keys.UPDATE fails; got calls=%v", calls)
	}
	if refreshRepo.tokens["z"].RevokedAt == nil {
		t.Errorf("refresh token must be revoked at the DB level even when api_keys update fails")
	}
}

func containsAll(haystack []string, needles ...string) bool {
	set := make(map[string]struct{}, len(haystack))
	for _, h := range haystack {
		set[h] = struct{}{}
	}
	for _, n := range needles {
		if _, ok := set[n]; !ok {
			return false
		}
	}
	return true
}

// ── AllowedClientOrigins ────────────────────────────────────────────────────

// stubOriginRepo is a minimal OAuthClientRepository that only services the
// dynamic-CORS-allowlist code path. Hand-rolled (instead of reusing
// stubClientRepo) so the test inputs are obvious from the call site.
type stubOriginRepo struct {
	uris []string
	err  error
}

func (s *stubOriginRepo) GetClientByID(_ context.Context, _ string) (*OAuthClient, error) {
	return nil, ErrOAuthClientNotFound
}

func (s *stubOriginRepo) ListEnabledRedirectURIs(_ context.Context) ([]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.uris, nil
}

func (s *stubOriginRepo) ListClientsWithFrontchannelLogout(_ context.Context) ([]*OAuthClient, error) {
	return nil, nil
}

func newOriginTestService(repo OAuthClientRepository) *OAuthProviderService {
	// Only clientRepo is exercised by AllowedClientOrigins; the rest can be
	// nil without panicking because the method never reaches them.
	return &OAuthProviderService{clientRepo: repo}
}

func TestAllowedClientOrigins_DedupesAndSorts(t *testing.T) {
	// Two clients pointing at the same SPA origin via different paths must
	// collapse to one entry. Default port (https→443) and no port collapse
	// to the same authority.
	repo := &stubOriginRepo{uris: []string{
		"https://image.sakrylle.com/oauth/callback",
		"https://image.sakrylle.com/legacy/callback",
		"http://localhost:5173/oauth/callback",
	}}
	got, err := newOriginTestService(repo).AllowedClientOrigins(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		"http://localhost:5173",
		"https://image.sakrylle.com",
	}
	if len(got) != len(want) {
		t.Fatalf("origin count: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("origins not deduped/sorted: got %v, want %v", got, want)
			break
		}
	}
}

func TestAllowedClientOrigins_SkipsNonHTTPSchemes(t *testing.T) {
	// Native-app callbacks (myapp://) and malformed URIs cannot represent a
	// browser Origin header, so they must NOT surface in the CORS allowlist.
	repo := &stubOriginRepo{uris: []string{
		"https://image.sakrylle.com/oauth/callback",
		"myapp://oauth/callback",    // native client
		"com.example.app:/callback", // private-use scheme, RFC 8252 §7.1
		"   ",                       // whitespace
		"",                          // empty
		"not a url",                 // unparseable host-less
		"https:///path-only",        // missing host
	}}
	got, err := newOriginTestService(repo).AllowedClientOrigins(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "https://image.sakrylle.com" {
		t.Errorf("expected only the http(s) origin, got %v", got)
	}
}

func TestAllowedClientOrigins_PropagatesRepoError(t *testing.T) {
	// Repo failure must surface — the router uses the error to keep the
	// previous cached snapshot rather than silently emptying the allowlist.
	repo := &stubOriginRepo{err: errors.New("db unavailable")}
	got, err := newOriginTestService(repo).AllowedClientOrigins(context.Background())
	if err == nil {
		t.Fatalf("expected error, got origins=%v", got)
	}
}

func TestAllowedClientOrigins_EmptyRepoReturnsEmpty(t *testing.T) {
	repo := &stubOriginRepo{uris: nil}
	got, err := newOriginTestService(repo).AllowedClientOrigins(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

// keyLookupAPIKeyRepo wraps stubAPIKeyRepo so GetByKey resolves the seeded
// row by its plaintext Key (the base stub always returns NotFound). Used by
// the introspection audience-scoping test.
type keyLookupAPIKeyRepo struct {
	*stubAPIKeyRepo
}

func (s *keyLookupAPIKeyRepo) GetByKey(_ context.Context, key string) (*APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rows {
		if r.Key == key {
			cp := *r
			return &cp, nil
		}
	}
	return nil, ErrAPIKeyNotFound
}

// TestIntrospectToken_RejectsCrossClient verifies RFC 7662 audience scoping:
// a confidential client may only introspect tokens it owns. A caller passing
// a different client_id than the token's owning client must get active:false.
func TestIntrospectToken_RejectsCrossClient(t *testing.T) {
	baseAPIKeyRepo := newStubAPIKeyRepo()
	apiKeyRepo := &keyLookupAPIKeyRepo{stubAPIKeyRepo: baseAPIKeyRepo}
	accessRepo := newStubAccessRepo()
	settingRepo := &stubSettingRepo{values: map[string]string{
		"oauth_provider_enabled":          "true",
		"oauth_scope_enforcement_enabled": "true",
		"oauth_issuer":                    "https://sub.sakrylle.example",
	}}
	svc := NewOAuthProviderService(
		&stubClientRepo{clients: map[string]*OAuthClient{}},
		newStubCodeRepo(),
		newStubRefreshRepo(),
		accessRepo,
		nil, // deviceRepo
		nil, // authzTxRepo
		oauthAPIKeyOrAdapter(apiKeyRepo, nil),
		nil, // groupRepo
		nil, // groupAccess
		settingRepo,
		nil,
	)

	const tokenA = "sk-oauth-token-a"
	apiKey := &APIKey{Key: tokenA, Status: StatusAPIKeyActive, UserID: 42}
	if err := apiKeyRepo.Create(context.Background(), apiKey); err != nil {
		t.Fatalf("seed api key: %v", err)
	}
	now := time.Now()
	if err := accessRepo.CreateAccessToken(context.Background(), &OAuthAccessToken{
		APIKeyID:  apiKey.ID,
		ClientID:  "client-A",
		UserID:    42,
		Scopes:    []string{"profile:read"},
		IssuedAt:  now,
		ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed access metadata: %v", err)
	}

	// Owning client sees the token as active.
	respOwner, err := svc.IntrospectToken(context.Background(), "client-A", tokenA)
	if err != nil {
		t.Fatalf("introspect (owner): %v", err)
	}
	if !respOwner.Active {
		t.Fatalf("owning client expected active:true, got active:false")
	}

	// A different client must NOT be able to introspect the token.
	respOther, err := svc.IntrospectToken(context.Background(), "client-B", tokenA)
	if err != nil {
		t.Fatalf("introspect (cross-client): %v", err)
	}
	if respOther.Active {
		t.Fatalf("cross-client introspection leaked: expected active:false, got active:true")
	}
}
