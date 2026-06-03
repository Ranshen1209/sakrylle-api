package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// ── stubs (handler-package copies of the service-package stubs) ─────────────
//
// These mirror service.oauth_provider_service_test.go's stubs. We can't reuse
// them across packages — _test.go files are not exported. Keep them minimal:
// just enough to drive OAuthProviderService through the HTTP layer.

type oauthHandlerClientRepoStub struct {
	clients map[string]*service.OAuthClient
}

func (s *oauthHandlerClientRepoStub) GetClientByID(_ context.Context, id string) (*service.OAuthClient, error) {
	if c, ok := s.clients[id]; ok {
		return c, nil
	}
	return nil, service.ErrOAuthClientNotFound
}

func (s *oauthHandlerClientRepoStub) ListEnabledRedirectURIs(_ context.Context) ([]string, error) {
	out := make([]string, 0)
	for _, c := range s.clients {
		if c.Disabled {
			continue
		}
		out = append(out, c.RedirectURIs...)
	}
	return out, nil
}

type oauthHandlerCodeRepoStub struct {
	mu    sync.Mutex
	codes map[string]*service.OAuthCode
}

func newOAuthHandlerCodeRepoStub() *oauthHandlerCodeRepoStub {
	return &oauthHandlerCodeRepoStub{codes: map[string]*service.OAuthCode{}}
}

func (s *oauthHandlerCodeRepoStub) CreateCode(_ context.Context, c *service.OAuthCode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *c
	s.codes[c.CodeHash] = &cp
	return nil
}

func (s *oauthHandlerCodeRepoStub) ConsumeCode(_ context.Context, h string, now time.Time) (*service.OAuthCode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.codes[h]
	if !ok {
		return nil, service.ErrOAuthCodeNotFound
	}
	if c.UsedAt != nil {
		return nil, service.ErrOAuthCodeAlreadyUsed
	}
	if !c.ExpiresAt.After(now) {
		return nil, service.ErrOAuthCodeExpired
	}
	c.UsedAt = &now
	return c, nil
}

func (s *oauthHandlerCodeRepoStub) DeleteExpiredCodes(_ context.Context, _ time.Time) (int, error) {
	return 0, nil
}

type oauthHandlerRefreshRepoStub struct {
	mu     sync.Mutex
	tokens map[string]*service.OAuthRefreshToken
}

func newOAuthHandlerRefreshRepoStub() *oauthHandlerRefreshRepoStub {
	return &oauthHandlerRefreshRepoStub{tokens: map[string]*service.OAuthRefreshToken{}}
}

func (s *oauthHandlerRefreshRepoStub) CreateRefreshToken(_ context.Context, t *service.OAuthRefreshToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tokens[t.TokenHash]; exists {
		return errors.New("duplicate token_hash")
	}
	cp := *t
	s.tokens[t.TokenHash] = &cp
	return nil
}

func (s *oauthHandlerRefreshRepoStub) ConsumeForRotation(_ context.Context, oldHash, newHash string, now time.Time) (*service.OAuthRefreshToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.tokens[oldHash]
	if !ok {
		return nil, service.ErrOAuthRefreshTokenNotFound
	}
	if row.RevokedAt != nil {
		return nil, service.ErrOAuthRefreshTokenRevoked
	}
	if !row.ExpiresAt.After(now) {
		return nil, service.ErrOAuthRefreshTokenExpired
	}
	row.RevokedAt = &now
	row.RotatedToHash = &newHash
	cp := *row
	return &cp, nil
}

func (s *oauthHandlerRefreshRepoStub) RevokeRefreshTokensByAPIKeyID(_ context.Context, apiKeyID int64, now time.Time) error {
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

func (s *oauthHandlerRefreshRepoStub) ListActiveByUserAndClient(_ context.Context, userID int64, clientID string, now time.Time) ([]*service.OAuthRefreshToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []*service.OAuthRefreshToken{}
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

func (s *oauthHandlerRefreshRepoStub) ListActiveByUser(_ context.Context, userID int64, now time.Time) ([]*service.OAuthRefreshToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []*service.OAuthRefreshToken{}
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
// In-memory fakes for the v2 OAuthRefreshTokenRepository surface so this
// handler test exercises real rotation/revocation semantics end-to-end.

func (s *oauthHandlerRefreshRepoStub) GetRefreshTokenByHashForUpdate(_ context.Context, tokenHash string, _ time.Time) (*service.OAuthRefreshToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.tokens[tokenHash]
	if !ok {
		return nil, service.ErrOAuthRefreshTokenNotFound
	}
	cp := *row
	return &cp, nil
}

func (s *oauthHandlerRefreshRepoStub) RevokeRefreshTokensByGrantID(_ context.Context, grantID string, now time.Time) ([]int64, error) {
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

func (s *oauthHandlerRefreshRepoStub) RevokeRefreshTokensByTokenFamilyID(_ context.Context, familyID string, now time.Time) ([]int64, error) {
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

func (s *oauthHandlerRefreshRepoStub) RevokeRefreshTokensByUserAndClient(_ context.Context, userID int64, clientID string, now time.Time) ([]int64, error) {
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

func (s *oauthHandlerRefreshRepoStub) MarkReuseDetected(_ context.Context, tokenHash string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if row, ok := s.tokens[tokenHash]; ok {
		t := now
		row.ReuseDetectedAt = &t
	}
	return nil
}

// oauthHandlerAPIKeyOAuthAdapter adapts the handler stub APIKeyRepository to
// the v2 OAuthAPIKeyRepository surface for tests that don't need real
// batch-disable semantics.
type oauthHandlerAPIKeyOAuthAdapter struct {
	*oauthHandlerAPIKeyRepoStub
}

func (a *oauthHandlerAPIKeyOAuthAdapter) DisableAPIKeysByIDsReturningKeys(ctx context.Context, ids []int64, now time.Time) ([]string, error) {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		key, err := a.GetByID(ctx, id)
		if err != nil || key == nil {
			continue
		}
		out = append(out, key.Key)
		if key.Status == service.StatusAPIKeyDisabled {
			continue
		}
		key.Status = service.StatusAPIKeyDisabled
		_ = a.Update(ctx, key)
	}
	return out, nil
}

type oauthHandlerAPIKeyRepoStub struct {
	mu     sync.Mutex
	nextID int64
	rows   map[int64]*service.APIKey
}

func newOAuthHandlerAPIKeyRepoStub() *oauthHandlerAPIKeyRepoStub {
	return &oauthHandlerAPIKeyRepoStub{rows: map[int64]*service.APIKey{}}
}

func (s *oauthHandlerAPIKeyRepoStub) Create(_ context.Context, k *service.APIKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	k.ID = s.nextID
	cp := *k
	s.rows[k.ID] = &cp
	return nil
}

func (s *oauthHandlerAPIKeyRepoStub) GetByID(_ context.Context, id int64) (*service.APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[id]
	if !ok {
		return nil, service.ErrAPIKeyNotFound
	}
	cp := *r
	return &cp, nil
}

func (s *oauthHandlerAPIKeyRepoStub) Update(_ context.Context, k *service.APIKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rows[k.ID]; !ok {
		return service.ErrAPIKeyNotFound
	}
	cp := *k
	s.rows[k.ID] = &cp
	return nil
}

func (s *oauthHandlerAPIKeyRepoStub) GetKeyAndOwnerID(_ context.Context, _ int64) (string, int64, error) {
	return "", 0, nil
}
func (s *oauthHandlerAPIKeyRepoStub) GetByKey(_ context.Context, _ string) (*service.APIKey, error) {
	return nil, service.ErrAPIKeyNotFound
}
func (s *oauthHandlerAPIKeyRepoStub) GetByKeyForAuth(_ context.Context, _ string) (*service.APIKey, error) {
	return nil, service.ErrAPIKeyNotFound
}
func (s *oauthHandlerAPIKeyRepoStub) Delete(_ context.Context, _ int64) error { return nil }
func (s *oauthHandlerAPIKeyRepoStub) ListByUserID(_ context.Context, _ int64, _ pagination.PaginationParams, _ service.APIKeyListFilters) ([]service.APIKey, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (s *oauthHandlerAPIKeyRepoStub) VerifyOwnership(_ context.Context, _ int64, ids []int64) ([]int64, error) {
	return ids, nil
}
func (s *oauthHandlerAPIKeyRepoStub) CountByUserID(_ context.Context, _ int64) (int64, error) {
	return 0, nil
}
func (s *oauthHandlerAPIKeyRepoStub) ExistsByKey(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (s *oauthHandlerAPIKeyRepoStub) ListByGroupID(_ context.Context, _ int64, _ pagination.PaginationParams) ([]service.APIKey, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (s *oauthHandlerAPIKeyRepoStub) SearchAPIKeys(_ context.Context, _ int64, _ string, _ int) ([]service.APIKey, error) {
	return nil, nil
}
func (s *oauthHandlerAPIKeyRepoStub) ClearGroupIDByGroupID(_ context.Context, _ int64) (int64, error) {
	return 0, nil
}
func (s *oauthHandlerAPIKeyRepoStub) UpdateGroupIDByUserAndGroup(_ context.Context, _, _, _ int64) (int64, error) {
	return 0, nil
}
func (s *oauthHandlerAPIKeyRepoStub) CountByGroupID(_ context.Context, _ int64) (int64, error) {
	return 0, nil
}
func (s *oauthHandlerAPIKeyRepoStub) ListKeysByUserID(_ context.Context, _ int64) ([]string, error) {
	return nil, nil
}
func (s *oauthHandlerAPIKeyRepoStub) ListKeysByGroupID(_ context.Context, _ int64) ([]string, error) {
	return nil, nil
}
func (s *oauthHandlerAPIKeyRepoStub) IncrementQuotaUsed(_ context.Context, _ int64, _ float64) (float64, error) {
	return 0, nil
}
func (s *oauthHandlerAPIKeyRepoStub) UpdateLastUsed(_ context.Context, _ int64, _ time.Time) error {
	return nil
}
func (s *oauthHandlerAPIKeyRepoStub) IncrementRateLimitUsage(_ context.Context, _ int64, _ float64) error {
	return nil
}
func (s *oauthHandlerAPIKeyRepoStub) ResetRateLimitWindows(_ context.Context, _ int64) error {
	return nil
}
func (s *oauthHandlerAPIKeyRepoStub) GetRateLimitData(_ context.Context, _ int64) (*service.APIKeyRateLimitData, error) {
	return &service.APIKeyRateLimitData{}, nil
}

type oauthHandlerSettingRepoStub struct {
	values map[string]string
}

func (s *oauthHandlerSettingRepoStub) Get(_ context.Context, key string) (*service.Setting, error) {
	if v, ok := s.values[key]; ok {
		return &service.Setting{Key: key, Value: v}, nil
	}
	return nil, errors.New("not found")
}
func (s *oauthHandlerSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if v, ok := s.values[key]; ok {
		return v, nil
	}
	return "", errors.New("not found")
}
func (s *oauthHandlerSettingRepoStub) Set(_ context.Context, key, value string) error {
	s.values[key] = value
	return nil
}
func (s *oauthHandlerSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, k := range keys {
		if v, ok := s.values[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}
func (s *oauthHandlerSettingRepoStub) SetMultiple(_ context.Context, settings map[string]string) error {
	for k, v := range settings {
		s.values[k] = v
	}
	return nil
}
func (s *oauthHandlerSettingRepoStub) GetAll(_ context.Context) (map[string]string, error) {
	out := make(map[string]string, len(s.values))
	for k, v := range s.values {
		out[k] = v
	}
	return out, nil
}
func (s *oauthHandlerSettingRepoStub) Delete(_ context.Context, key string) error {
	delete(s.values, key)
	return nil
}

// ── helpers ─────────────────────────────────────────────────────────────────

const (
	testOAuthClientID    = "sakrylle-image-playground"
	testOAuthClientName  = "Sakrylle Image Playground"
	testOAuthRedirectURI = "https://image.sakrylle.com/oauth/callback"
)

// pkceVerifierAndChallengeForHandler returns (verifier, BASE64URL(SHA256(verifier))).
func pkceVerifierAndChallengeForHandler(verifier string) (string, string) {
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

// oauthHandlerAuthorizeTxRepoStub is the in-memory v2 authorize-transaction
// repo used by handler tests. It implements both the base repo interface and
// OAuthAuthorizeAtomicRepository (the FIX C2 / A4 atomic consume+issue
// surface) so the same code path runs in tests as in production.
//
// The atomic consume gate enforces FIX A4's row-locked subject re-check, so
// stub fakes that bypass the service layer cannot accidentally let a foreign
// JWT subject win.
type oauthHandlerAuthorizeTxRepoStub struct {
	mu       sync.Mutex
	rows     map[string]*service.OAuthAuthorizeTransaction
	codeRepo *oauthHandlerCodeRepoStub
}

func newOAuthHandlerAuthorizeTxRepoStub(codeRepo *oauthHandlerCodeRepoStub) *oauthHandlerAuthorizeTxRepoStub {
	return &oauthHandlerAuthorizeTxRepoStub{
		rows:     map[string]*service.OAuthAuthorizeTransaction{},
		codeRepo: codeRepo,
	}
}

func (s *oauthHandlerAuthorizeTxRepoStub) CreateAuthorizeTransaction(_ context.Context, tx *service.OAuthAuthorizeTransaction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *tx
	s.rows[tx.TransactionID] = &cp
	return nil
}

func (s *oauthHandlerAuthorizeTxRepoStub) GetAuthorizeTransactionForApproval(_ context.Context, transactionID string, now time.Time) (*service.OAuthAuthorizeTransaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[transactionID]
	if !ok {
		return nil, service.ErrOAuthAuthorizeTransactionNotFound
	}
	if row.ConsumedAt != nil {
		return nil, service.ErrOAuthAuthorizeTransactionConsumed
	}
	if !row.ExpiresAt.After(now) {
		return nil, service.ErrOAuthAuthorizeTransactionNotFound
	}
	cp := *row
	return &cp, nil
}

func (s *oauthHandlerAuthorizeTxRepoStub) MarkAuthorizeTransactionConsumed(_ context.Context, transactionID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[transactionID]
	if !ok {
		return service.ErrOAuthAuthorizeTransactionNotFound
	}
	if row.ConsumedAt == nil {
		t := now
		row.ConsumedAt = &t
	}
	return nil
}

func (s *oauthHandlerAuthorizeTxRepoStub) DeleteExpiredAuthorizeTransactions(_ context.Context, _ time.Time) (int, error) {
	return 0, nil
}

// ConsumeAuthorizeTransactionAndIssueCode mirrors the production atomic
// consume: row-locked existence + subject re-check, mark consumed, insert
// code — all under one logical "transaction". The mutex stands in for FOR
// UPDATE here.
func (s *oauthHandlerAuthorizeTxRepoStub) ConsumeAuthorizeTransactionAndIssueCode(
	ctx context.Context,
	transactionID string,
	expectedUserID int64,
	now time.Time,
	code *service.OAuthCode,
) error {
	if code == nil {
		return errors.New("consume+issue: code argument is nil")
	}
	if expectedUserID <= 0 {
		return service.ErrOAuthSubjectMismatch
	}
	s.mu.Lock()
	row, ok := s.rows[transactionID]
	if !ok {
		s.mu.Unlock()
		return service.ErrOAuthAuthorizeTransactionNotFound
	}
	if row.ConsumedAt != nil {
		s.mu.Unlock()
		return service.ErrOAuthAuthorizeTransactionConsumed
	}
	if !row.ExpiresAt.After(now) {
		s.mu.Unlock()
		return service.ErrOAuthAuthorizeTransactionNotFound
	}
	if row.UserID != expectedUserID {
		s.mu.Unlock()
		return service.ErrOAuthSubjectMismatch
	}
	t := now
	row.ConsumedAt = &t
	s.mu.Unlock()
	// Persist the code outside the row lock; matches production "single tx"
	// from the caller's perspective (any failure here would not leave a
	// half-consumed row behind because we already stamped consumed_at).
	return s.codeRepo.CreateCode(ctx, code)
}

// beginAndApproveForHandler drives the v2 BeginAuthorize → Approve flow
// through the gin handlers and returns the issued authorization code.
// Helper used by tests that need a valid code without relying on the now-
// removed legacy approve-with-form-fields shape.
func beginAndApproveForHandler(t *testing.T, h *OAuthProviderHandler, userID int64, scope, state, codeChallenge string) (code string) {
	t.Helper()
	beginBody, _ := json.Marshal(map[string]string{
		"client_id":             testOAuthClientID,
		"redirect_uri":          testOAuthRedirectURI,
		"response_type":         "code",
		"scope":                 scope,
		"state":                 state,
		"code_challenge":        codeChallenge,
		"code_challenge_method": "S256",
	})
	c, rec := newGinTestContext(http.MethodPost, "/api/v1/oauth/authorize/begin", beginBody, "application/json")
	c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: userID, Concurrency: 1})
	h.BeginAuthorize(c)
	require.Equal(t, http.StatusOK, rec.Code, "begin: %s", rec.Body.String())

	var beginResp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &beginResp))
	txID, _ := beginResp["transaction_id"].(string)
	csrf, _ := beginResp["csrf_token"].(string)
	require.NotEmpty(t, txID)
	require.NotEmpty(t, csrf)

	approveBody, _ := json.Marshal(map[string]any{
		"transaction_id": txID,
		"csrf_token":     csrf,
		"decision":       "approve",
	})
	c2, rec2 := newGinTestContext(http.MethodPost, "/api/v1/oauth/authorize/approve", approveBody, "application/json")
	c2.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: userID, Concurrency: 1})
	h.Approve(c2)
	require.Equal(t, http.StatusOK, rec2.Code, "approve: %s", rec2.Body.String())

	var approveResp map[string]string
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &approveResp))
	parsed, perr := url.Parse(approveResp["redirect_to"])
	require.NoError(t, perr)
	code = parsed.Query().Get("code")
	require.NotEmpty(t, code)
	return code
}

func newOAuthProviderHandlerHarness(t *testing.T) (*OAuthProviderHandler, *service.OAuthProviderService) {
	t.Helper()
	groupID := int64(5)
	clientRepo := &oauthHandlerClientRepoStub{clients: map[string]*service.OAuthClient{
		testOAuthClientID: {
			ClientID: testOAuthClientID,
			Name:     testOAuthClientName,
			RedirectURIs: []string{
				testOAuthRedirectURI,
			},
			AllowedScopes:          []string{"image_generation", "balance:read", "models:read"},
			DefaultScopes:          []string{"image_generation"},
			PKCERequired:           true,
			DefaultGroupID:         &groupID,
			AccessTokenTTLSeconds:  86400,
			RefreshTokenTTLSeconds: 2592000,
			// §7.4 / §10.1: legacy compat — image-playground frontend has not
			// migrated to request offline_access, so allow refresh issuance
			// without it. Mirrors the prod row state set by the Sakrylle
			// seed migration.
			AllowRefreshWithoutOfflineAccess: true,
		},
	}}
	settingRepo := &oauthHandlerSettingRepoStub{values: map[string]string{
		"oauth_provider_enabled": "true",
		"oauth_default_group_id": "5",
	}}
	codeRepo := newOAuthHandlerCodeRepoStub()
	// FIX A1: wire an in-memory authorize-tx repo (with atomic-consume
	// surface) so handler tests exercise the v2 BeginAuthorize → Approve
	// flow end to end.
	svc := service.NewOAuthProviderService(
		clientRepo,
		codeRepo,
		newOAuthHandlerRefreshRepoStub(),
		nil, // accessRepo
		nil, // deviceRepo
		newOAuthHandlerAuthorizeTxRepoStub(codeRepo),
		&oauthHandlerAPIKeyOAuthAdapter{oauthHandlerAPIKeyRepoStub: newOAuthHandlerAPIKeyRepoStub()},
		nil, // GroupRepository — unused on this code path
		nil, // groupAccess
		settingRepo,
		nil,
	)
	h := NewOAuthProviderHandler(svc, nil)
	return h, svc
}

func newGinTestContext(method, target string, body []byte, contentType string) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, reader)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.Request = req
	return c, rec
}

// ── tests ───────────────────────────────────────────────────────────────────

func TestAuthorizeRendersConsentHTML(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)
	_, challenge := pkceVerifierAndChallengeForHandler("the-quick-brown-fox-jumps-over-the-lazy-dog-12345")

	q := url.Values{}
	q.Set("client_id", testOAuthClientID)
	q.Set("redirect_uri", testOAuthRedirectURI)
	q.Set("response_type", "code")
	q.Set("scope", "image_generation balance:read")
	q.Set("state", "rnd-state")
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")

	c, rec := newGinTestContext(http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, "")
	h.Authorize(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	body := rec.Body.String()
	require.Contains(t, body, testOAuthClientName, "consent page must show client name")
	require.Contains(t, body, "image_generation", "consent page must list scope")
	// Friendly description from scopeBulletsHTML.
	require.Contains(t, body, "调用图像生成 API")
}

func TestAuthorizeMissingClientReturnsInlineError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)

	q := url.Values{}
	q.Set("client_id", "ghost-client")
	// Use an attacker-controlled redirect to assert we DON'T 302 there.
	q.Set("redirect_uri", "https://attacker.example.com/cb")
	q.Set("response_type", "code")
	q.Set("state", "abc")

	c, rec := newGinTestContext(http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, "")
	h.Authorize(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	require.Empty(t, rec.Header().Get("Location"), "must NOT redirect to attacker URL on bad client_id")
	body := rec.Body.String()
	require.NotContains(t, body, "attacker.example.com", "inline error must not echo attacker host")
}

func TestAuthorizeBadResponseTypeRedirects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)

	q := url.Values{}
	q.Set("client_id", testOAuthClientID)
	q.Set("redirect_uri", testOAuthRedirectURI)
	q.Set("response_type", "token") // implicit grant — unsupported
	q.Set("state", "state-echo-me")

	c, rec := newGinTestContext(http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, "")
	h.Authorize(c)

	require.Equal(t, http.StatusFound, rec.Code, "spec: unsupported response_type with valid redirect must 302")
	loc := rec.Header().Get("Location")
	require.NotEmpty(t, loc)
	parsed, err := url.Parse(loc)
	require.NoError(t, err)
	require.Equal(t, "image.sakrylle.com", parsed.Host)
	require.Equal(t, "unsupported_response_type", parsed.Query().Get("error"))
	require.Equal(t, "state-echo-me", parsed.Query().Get("state"))
}

func TestTokenRequiresGrantType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)

	form := url.Values{}
	c, rec := newGinTestContext(http.MethodPost, "/oauth/token", []byte(form.Encode()), "application/x-www-form-urlencoded")
	h.Token(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "unsupported_grant_type", resp["error"])
}

func TestTokenInvalidGrant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", testOAuthClientID)
	form.Set("redirect_uri", testOAuthRedirectURI)
	form.Set("code", "fabricated-code-not-in-repo")
	form.Set("code_verifier", "anything")

	c, rec := newGinTestContext(http.MethodPost, "/oauth/token", []byte(form.Encode()), "application/x-www-form-urlencoded")
	h.Token(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "invalid_grant", resp["error"])
}

func TestTokenAuthorizationCodeFlow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, svc := newOAuthProviderHandlerHarness(t)
	verifier, challenge := pkceVerifierAndChallengeForHandler("the-quick-brown-fox-jumps-over-the-lazy-dog-12345")

	authReq := &service.AuthorizeRequest{
		ClientID:            testOAuthClientID,
		RedirectURI:         testOAuthRedirectURI,
		ResponseType:        "code",
		Scopes:              []string{"image_generation", "balance:read"},
		State:               "rnd-state",
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
	}
	client, err := svc.ValidateAuthorizeRequest(context.Background(), authReq)
	require.NoError(t, err)
	issued, err := svc.IssueAuthorizationCode(context.Background(), client, 42, authReq)
	require.NoError(t, err)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", testOAuthClientID)
	form.Set("redirect_uri", testOAuthRedirectURI)
	form.Set("code", issued.Code)
	form.Set("code_verifier", verifier)

	c, rec := newGinTestContext(http.MethodPost, "/oauth/token", []byte(form.Encode()), "application/x-www-form-urlencoded")
	h.Token(c)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

	access, _ := resp["access_token"].(string)
	require.True(t, strings.HasPrefix(access, "sk_oauth_"), "access_token must have sk_oauth_ prefix, got %q", access)
	require.Equal(t, "Bearer", resp["token_type"])
	require.Equal(t, float64(86400), resp["expires_in"])
	refresh, _ := resp["refresh_token"].(string)
	require.True(t, strings.HasPrefix(refresh, "rt_"), "refresh_token must have rt_ prefix, got %q", refresh)
	require.Equal(t, "images:create account:balance:read", resp["scope"])
}

func TestTokenRefreshFlow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, svc := newOAuthProviderHandlerHarness(t)
	verifier, challenge := pkceVerifierAndChallengeForHandler("the-quick-brown-fox-jumps-over-the-lazy-dog-12345")

	// 1. Mint initial code+token via the handler (mirrors TestTokenAuthorizationCodeFlow).
	authReq := &service.AuthorizeRequest{
		ClientID:            testOAuthClientID,
		RedirectURI:         testOAuthRedirectURI,
		ResponseType:        "code",
		Scopes:              []string{"image_generation"},
		State:               "rnd-state",
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
	}
	client, err := svc.ValidateAuthorizeRequest(context.Background(), authReq)
	require.NoError(t, err)
	issued, err := svc.IssueAuthorizationCode(context.Background(), client, 42, authReq)
	require.NoError(t, err)

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", testOAuthClientID)
	form.Set("redirect_uri", testOAuthRedirectURI)
	form.Set("code", issued.Code)
	form.Set("code_verifier", verifier)
	c, rec := newGinTestContext(http.MethodPost, "/oauth/token", []byte(form.Encode()), "application/x-www-form-urlencoded")
	h.Token(c)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var first map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &first))
	originalRefresh, _ := first["refresh_token"].(string)
	originalAccess, _ := first["access_token"].(string)
	require.NotEmpty(t, originalRefresh)
	require.NotEmpty(t, originalAccess)

	// 2. Use refresh_token grant via handler.
	refreshForm := url.Values{}
	refreshForm.Set("grant_type", "refresh_token")
	refreshForm.Set("client_id", testOAuthClientID)
	refreshForm.Set("refresh_token", originalRefresh)
	c2, rec2 := newGinTestContext(http.MethodPost, "/oauth/token", []byte(refreshForm.Encode()), "application/x-www-form-urlencoded")
	h.Token(c2)
	require.Equal(t, http.StatusOK, rec2.Code, "refresh body=%s", rec2.Body.String())
	var second map[string]any
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &second))
	newAccess, _ := second["access_token"].(string)
	newRefresh, _ := second["refresh_token"].(string)
	require.True(t, strings.HasPrefix(newAccess, "sk_oauth_"))
	require.True(t, strings.HasPrefix(newRefresh, "rt_"))
	require.NotEqual(t, originalAccess, newAccess, "refresh must mint a new access_token")
	require.NotEqual(t, originalRefresh, newRefresh, "refresh must rotate the refresh_token")

	// 3. Replay old refresh_token → invalid_grant.
	c3, rec3 := newGinTestContext(http.MethodPost, "/oauth/token", []byte(refreshForm.Encode()), "application/x-www-form-urlencoded")
	h.Token(c3)
	require.Equal(t, http.StatusBadRequest, rec3.Code)
	var replay map[string]any
	require.NoError(t, json.Unmarshal(rec3.Body.Bytes(), &replay))
	require.Equal(t, "invalid_grant", replay["error"])
}

func TestApproveRequiresAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)

	body := map[string]string{
		"transaction_id": "any-id",
		"csrf_token":     "any-csrf",
		"decision":       "approve",
	}
	raw, _ := json.Marshal(body)
	c, rec := newGinTestContext(http.MethodPost, "/api/v1/oauth/authorize/approve", raw, "application/json")
	// Deliberately do NOT set ContextKeyUser → handler must 401.
	h.Approve(c)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestApproveDeniedReturnsAccessDenied(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)
	_, challenge := pkceVerifierAndChallengeForHandler("the-quick-brown-fox-jumps-over-the-lazy-dog-12345")

	// FIX A1: deny path goes through Begin (to seed a transaction) then
	// Approve with decision=deny. Server echoes the registered
	// redirect_uri + state from the transaction row, never trusting the
	// approve POST body.
	beginBody, _ := json.Marshal(map[string]string{
		"client_id":             testOAuthClientID,
		"redirect_uri":          testOAuthRedirectURI,
		"response_type":         "code",
		"scope":                 "image_generation",
		"state":                 "echo-this-state",
		"code_challenge":        challenge,
		"code_challenge_method": "S256",
	})
	bc, brec := newGinTestContext(http.MethodPost, "/api/v1/oauth/authorize/begin", beginBody, "application/json")
	bc.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 42, Concurrency: 1})
	h.BeginAuthorize(bc)
	require.Equal(t, http.StatusOK, brec.Code, "begin: %s", brec.Body.String())
	var beginResp map[string]any
	require.NoError(t, json.Unmarshal(brec.Body.Bytes(), &beginResp))

	denyBody, _ := json.Marshal(map[string]any{
		"transaction_id": beginResp["transaction_id"],
		"csrf_token":     beginResp["csrf_token"],
		"decision":       "deny",
	})
	c, rec := newGinTestContext(http.MethodPost, "/api/v1/oauth/authorize/approve", denyBody, "application/json")
	c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 42, Concurrency: 1})

	h.Approve(c)

	require.Equal(t, http.StatusOK, rec.Code, "deny still returns 200 with redirect_to in body; body=%s", rec.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	redirectTo, _ := resp["redirect_to"].(string)
	require.NotEmpty(t, redirectTo)

	parsed, err := url.Parse(redirectTo)
	require.NoError(t, err)
	require.Equal(t, "image.sakrylle.com", parsed.Host)
	require.Equal(t, "access_denied", parsed.Query().Get("error"))
	require.Equal(t, "echo-this-state", parsed.Query().Get("state"))
}

func TestConsentHTMLEscapesUntrustedQuery(t *testing.T) {
	// XSS guard: untrusted state must NOT be able to break out of the inline
	// <script> block on the consent page.
	hostile := `</script><script>alert(1)</script>`
	req := &service.AuthorizeRequest{
		ClientID:            testOAuthClientID,
		RedirectURI:         testOAuthRedirectURI,
		ResponseType:        "code",
		Scopes:              []string{"image_generation"},
		State:               hostile,
		CodeChallenge:       "challenge",
		CodeChallengeMethod: "S256",
	}
	got := oauthConsentHTML(testOAuthClientName, req, "")

	// The smoking gun is the literal closing-script-tag-then-opening-script
	// sequence. If this substring appears in the rendered HTML the browser
	// will execute the injected payload.
	require.NotContains(t, got, "</script><script>",
		"consent HTML allows attacker-controlled state to break out of inline <script>; "+
			"untrusted query params must be encoded so '</script>' inside a JS string literal "+
			"is rendered as e.g. '<\\/script>'")

	// Empty-nonce path (CSP disabled or middleware fell back to
	// 'unsafe-inline'): the script tag still gets rendered with a nonce
	// attribute so the same template handles both modes uniformly.
	require.Contains(t, got, `<script nonce="">`,
		"empty nonce should still produce a nonce attribute (browser ignores empty value under 'unsafe-inline')")
}

// scriptTagPattern matches every `<script>` or `<script ...>` opening tag in
// the rendered consent HTML. Used by the CSP-nonce regression test to assert
// that no inline script slips through without a nonce attribute, regardless
// of attribute order or future template tweaks (whitespace, line breaks).
var scriptTagPattern = regexp.MustCompile(`<script(\s[^>]*)?>`)

// TestConsentHTMLCarriesCSPNonce locks in that EVERY inline <script> block
// carries the per-request CSP nonce. Production CSP is
// `script-src 'self' 'nonce-...'` with no 'unsafe-inline'; a script tag
// without nonce is silently blocked and both consent buttons stop working.
// See commit history for the live-incident regression this test pins.
func TestConsentHTMLCarriesCSPNonce(t *testing.T) {
	req := &service.AuthorizeRequest{
		ClientID:            testOAuthClientID,
		RedirectURI:         testOAuthRedirectURI,
		ResponseType:        "code",
		Scopes:              []string{"image_generation"},
		State:               "abc",
		CodeChallenge:       "challenge",
		CodeChallengeMethod: "S256",
	}
	// Real nonces are base64-encoded 16-byte buffers — include `+/=` so we
	// also catch a future regression where someone interpolates the nonce
	// without HTML-escaping the attribute value.
	nonce := "Kq+Iw/Hv41mCIaqWf8Ty/CJw=="
	got := oauthConsentHTML(testOAuthClientName, req, nonce)

	require.Contains(t, got, `<script nonce="Kq+Iw/Hv41mCIaqWf8Ty/CJw==">`,
		"inline <script> must carry CSP nonce attribute or production CSP blocks it")

	// Walk every <script ...> opening tag and require nonce= on each.
	// Tolerant to attribute reordering and whitespace changes; will also
	// catch any newly-added inline script that forgets the nonce.
	matches := scriptTagPattern.FindAllString(got, -1)
	require.NotEmpty(t, matches, "consent HTML should contain at least one <script> tag")
	for _, tag := range matches {
		require.Contains(t, tag, "nonce=",
			"found unnonced <script> tag %q; every inline script must include the CSP nonce", tag)
	}
}

// ── confidential client (Basic auth) coverage ──────────────────────────────

const (
	testConfidentialClientID     = "confidential-test-client"
	testConfidentialClientSecret = "super-secret-confidential-token-aaaa"
	testConfidentialRedirectURI  = "https://confidential.example.com/cb"
)

// newConfidentialHandlerHarness builds a handler whose service has a single
// confidential (PKCE-not-required + bcrypt secret) client registered. Used
// by TestTokenBasicAuthCredentials below to exercise the Basic-Auth branch
// of extractClientCredentials and the bcrypt branch of authenticateClient.
func newConfidentialHandlerHarness(t *testing.T) (*OAuthProviderHandler, *service.OAuthProviderService) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(testConfidentialClientSecret), bcrypt.MinCost)
	require.NoError(t, err)

	groupID := int64(5)
	clientRepo := &oauthHandlerClientRepoStub{clients: map[string]*service.OAuthClient{
		testConfidentialClientID: {
			ClientID:               testConfidentialClientID,
			Name:                   "Confidential Test Client",
			ClientSecretHash:       string(hash),
			RedirectURIs:           []string{testConfidentialRedirectURI},
			AllowedScopes:          []string{"image_generation"},
			PKCERequired:           false,
			DefaultGroupID:         &groupID,
			AccessTokenTTLSeconds:  86400,
			RefreshTokenTTLSeconds: 2592000,
		},
	}}
	settingRepo := &oauthHandlerSettingRepoStub{values: map[string]string{
		"oauth_provider_enabled": "true",
		"oauth_default_group_id": "5",
	}}
	svc := service.NewOAuthProviderService(
		clientRepo,
		newOAuthHandlerCodeRepoStub(),
		newOAuthHandlerRefreshRepoStub(),
		nil, // accessRepo
		nil, // deviceRepo
		nil, // authzTxRepo
		&oauthHandlerAPIKeyOAuthAdapter{oauthHandlerAPIKeyRepoStub: newOAuthHandlerAPIKeyRepoStub()},
		nil,
		nil,
		settingRepo,
		nil,
	)
	h := NewOAuthProviderHandler(svc, nil)
	return h, svc
}

// newGinTestContextWithBasicAuth constructs a POST /oauth/token gin context
// with HTTP Basic credentials set in the Authorization header. extractClient
// Credentials checks BasicAuth first, so this is the only way to exercise
// that branch from the handler layer.
func newGinTestContextWithBasicAuth(form url.Values, basicUser, basicPass string) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(basicUser, basicPass)
	c.Request = req
	return c, rec
}

// TestTokenBasicAuthCredentials covers the Basic-Auth code path of
// extractClientCredentials and the bcrypt branch of authenticateClient.
func TestTokenBasicAuthCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mintCode := func(t *testing.T, svc *service.OAuthProviderService) string {
		t.Helper()
		authReq := &service.AuthorizeRequest{
			ClientID:     testConfidentialClientID,
			RedirectURI:  testConfidentialRedirectURI,
			ResponseType: "code",
			Scopes:       []string{"image_generation"},
			State:        "abc",
		}
		client, err := svc.ValidateAuthorizeRequest(context.Background(), authReq)
		require.NoError(t, err)
		issued, err := svc.IssueAuthorizationCode(context.Background(), client, 42, authReq)
		require.NoError(t, err)
		return issued.Code
	}

	t.Run("basic_auth_happy_path", func(t *testing.T) {
		h, svc := newConfidentialHandlerHarness(t)
		code := mintCode(t, svc)

		// Form body has grant_type/code/redirect_uri but NO client credentials —
		// they live in the Authorization: Basic header.
		form := url.Values{}
		form.Set("grant_type", "authorization_code")
		form.Set("redirect_uri", testConfidentialRedirectURI)
		form.Set("code", code)

		c, rec := newGinTestContextWithBasicAuth(form, testConfidentialClientID, testConfidentialClientSecret)
		h.Token(c)

		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		var resp map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		access, _ := resp["access_token"].(string)
		require.True(t, strings.HasPrefix(access, "sk_oauth_"), "access_token must have sk_oauth_ prefix, got %q", access)
		require.Equal(t, "Bearer", resp["token_type"])
		require.Equal(t, float64(86400), resp["expires_in"])
	})

	t.Run("basic_auth_takes_priority_over_form", func(t *testing.T) {
		// Form body carries CORRECT client_id/client_secret, but the Basic
		// header is wrong. extractClientCredentials returns Basic credentials
		// when present, so authentication must fail (401).
		h, svc := newConfidentialHandlerHarness(t)
		code := mintCode(t, svc)

		form := url.Values{}
		form.Set("grant_type", "authorization_code")
		form.Set("redirect_uri", testConfidentialRedirectURI)
		form.Set("code", code)
		form.Set("client_id", testConfidentialClientID)
		form.Set("client_secret", testConfidentialClientSecret)

		c, rec := newGinTestContextWithBasicAuth(form, testConfidentialClientID, "wrong-secret-from-basic-header")
		h.Token(c)

		require.Equal(t, http.StatusUnauthorized, rec.Code,
			"Basic header must take priority over form body; wrong Basic must reject even when form is right; body=%s", rec.Body.String())
		var resp map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Equal(t, "invalid_client", resp["error"])
	})
}

// ── User-facing grants management endpoints ─────────────────────────────────
//
// These tests exercise the JWT-protected GET/DELETE /api/v1/oauth/grants
// surface end-to-end through the gin handler, including the unauthenticated
// guard, cross-user isolation, the empty-revoke 200 idempotency contract, and
// the response shape contract the frontend depends on.

// mintAccessGrantForHandler runs through Approve + Token to plant exactly one
// active grant for (userID, testOAuthClientID) and return the issued tokens
// so revoke tests can verify subsequent /v1/* would fail.
func mintAccessGrantForHandler(t *testing.T, h *OAuthProviderHandler, userID int64) (accessToken string) {
	t.Helper()
	verifier, challenge := pkceVerifierAndChallengeForHandler("verifier-123456789-aaaaaaaaaaaaaaaaaaaaaaaaaaaa")

	// 1. Begin + Approve → get authorization code via redirect_to JSON.
	code := beginAndApproveForHandler(t, h, userID, "image_generation", "x", challenge)

	// 2. Token → exchange code for access_token.
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("redirect_uri", testOAuthRedirectURI)
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("client_id", testOAuthClientID)
	c2, rec2 := newGinTestContext(http.MethodPost, "/oauth/token", []byte(form.Encode()), "application/x-www-form-urlencoded")
	h.Token(c2)
	require.Equal(t, http.StatusOK, rec2.Code, "token failed: %s", rec2.Body.String())

	var tokenResp map[string]any
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &tokenResp))
	at, _ := tokenResp["access_token"].(string)
	require.NotEmpty(t, at)
	require.True(t, strings.HasPrefix(at, "sk_oauth_"), "access_token prefix")
	return at
}

func TestListGrants_Unauthenticated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)

	c, rec := newGinTestContext(http.MethodGet, "/api/v1/oauth/grants", nil, "")
	// Deliberately do NOT set ContextKeyUser → 401.
	h.ListGrants(c)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestListGrants_EmptyForUserWithNoGrants(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)

	c, rec := newGinTestContext(http.MethodGet, "/api/v1/oauth/grants", nil, "")
	c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 7, Concurrency: 1})
	h.ListGrants(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp.Items, "items must be a JSON array, never null")
	require.Len(t, resp.Items, 0)
}

func TestListGrants_HappyPathReturnsExpectedShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)
	const userID int64 = 17
	mintAccessGrantForHandler(t, h, userID)

	c, rec := newGinTestContext(http.MethodGet, "/api/v1/oauth/grants", nil, "")
	c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: userID, Concurrency: 1})
	h.ListGrants(c)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1, "want exactly one grant for the test client")

	g := resp.Items[0]
	require.Equal(t, testOAuthClientID, g["client_id"])
	require.Equal(t, testOAuthClientName, g["client_name"])
	require.Equal(t, false, g["client_disabled"])
	require.EqualValues(t, 1, g["active_token_count"])
	scopes, _ := g["scopes"].([]any)
	require.Equal(t, []any{"images:create"}, scopes)
	require.NotEmpty(t, g["first_authorized_at"])
	// last_used_at can be nil since we never hit /v1/*; the contract is the
	// key is present (so the frontend doesn't need to defensive-check undefined).
	_, hasLastUsed := g["last_used_at"]
	require.True(t, hasLastUsed)
}

func TestListGrants_DoesNotLeakOtherUsersGrants(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)
	mintAccessGrantForHandler(t, h, 100)
	mintAccessGrantForHandler(t, h, 200)

	c, rec := newGinTestContext(http.MethodGet, "/api/v1/oauth/grants", nil, "")
	c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 100, Concurrency: 1})
	h.ListGrants(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1, "user 100 must only see their own grant, not user 200's")
}

func TestRevokeGrant_Unauthenticated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)

	c, rec := newGinTestContext(http.MethodDelete, "/api/v1/oauth/grants/"+testOAuthClientID, nil, "")
	c.Params = gin.Params{{Key: "client_id", Value: testOAuthClientID}}
	h.RevokeGrant(c)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRevokeGrant_MissingClientIDReturns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)

	c, rec := newGinTestContext(http.MethodDelete, "/api/v1/oauth/grants/", nil, "")
	c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 1, Concurrency: 1})
	c.Params = gin.Params{{Key: "client_id", Value: ""}}
	h.RevokeGrant(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "invalid_request", resp["error"])
}

func TestRevokeGrant_HappyPathReturnsCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)
	const userID int64 = 33
	mintAccessGrantForHandler(t, h, userID)

	c, rec := newGinTestContext(http.MethodDelete, "/api/v1/oauth/grants/"+testOAuthClientID, nil, "")
	c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: userID, Concurrency: 1})
	c.Params = gin.Params{{Key: "client_id", Value: testOAuthClientID}}
	h.RevokeGrant(c)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.EqualValues(t, 1, resp["revoked"])

	// Subsequent list shows 0 grants — the revoke actually took effect.
	listC, listRec := newGinTestContext(http.MethodGet, "/api/v1/oauth/grants", nil, "")
	listC.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: userID, Concurrency: 1})
	h.ListGrants(listC)
	require.Equal(t, http.StatusOK, listRec.Code)
	var listResp struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(listRec.Body.Bytes(), &listResp))
	require.Len(t, listResp.Items, 0, "after revoke, list must be empty")
}

func TestRevokeGrant_IsIdempotentWhenNoMatchingGrant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)

	// User has no grants at all; revoking should still 200 with revoked=0.
	c, rec := newGinTestContext(http.MethodDelete, "/api/v1/oauth/grants/"+testOAuthClientID, nil, "")
	c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 999, Concurrency: 1})
	c.Params = gin.Params{{Key: "client_id", Value: testOAuthClientID}}
	h.RevokeGrant(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.EqualValues(t, 0, resp["revoked"])
}

func TestRevokeGrant_DoesNotTouchOtherUsersGrants(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)
	mintAccessGrantForHandler(t, h, 100)
	mintAccessGrantForHandler(t, h, 200)

	// User 100 attempts to revoke "their" grant — but a malicious actor could
	// pass any client_id. Verify only user 100's tokens get touched, not 200's.
	c, rec := newGinTestContext(http.MethodDelete, "/api/v1/oauth/grants/"+testOAuthClientID, nil, "")
	c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 100, Concurrency: 1})
	c.Params = gin.Params{{Key: "client_id", Value: testOAuthClientID}}
	h.RevokeGrant(c)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.EqualValues(t, 1, resp["revoked"], "only user 100's single grant should be revoked")

	// User 200's grant must still appear when 200 lists their grants.
	c2, rec2 := newGinTestContext(http.MethodGet, "/api/v1/oauth/grants", nil, "")
	c2.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 200, Concurrency: 1})
	h.ListGrants(c2)
	require.Equal(t, http.StatusOK, rec2.Code)
	var listResp struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &listResp))
	require.Len(t, listResp.Items, 1, "user 200's grant must survive user 100's revoke call")
}

func TestRevokeGrant_DoesNotEchoInternalErrors(t *testing.T) {
	// Smoke test that the handler returns the *opaque* server_error envelope
	// (not the raw err.Error()) — important so DB error strings can't leak
	// table/column/constraint names through the public API.
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)

	c, rec := newGinTestContext(http.MethodDelete, "/api/v1/oauth/grants/sakrylle-image-playground", nil, "")
	c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 1, Concurrency: 1})
	c.Params = gin.Params{{Key: "client_id", Value: "sakrylle-image-playground"}}
	h.RevokeGrant(c)
	// Even on the happy "no grants" path, the response body must not contain
	// raw service-layer error strings (no "list active refresh tokens" etc).
	body := rec.Body.String()
	require.NotContains(t, body, "list active refresh tokens")
	require.NotContains(t, body, "sql:")
	require.NotContains(t, body, "ent:")
}

// ── Phase 3 (§16): Metadata, Revoke, AuthorizedApps ─────────────────────────
//
// These tests exercise the new endpoints added in Phase 3. They reuse the
// in-memory stubs already declared above for the v1 grant API; for endpoints
// that depend on v2 oauth_access_tokens metadata (ListAuthorizedApps), we
// inject an in-memory access repo stub.

// ── Metadata (§12.1) ────────────────────────────────────────────────────────

func TestMetadata_ReturnsAllRequiredFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)

	c, rec := newGinTestContext(http.MethodGet, "/.well-known/oauth-authorization-server", nil, "")
	c.Request.Host = "sub.sakrylle.com"
	h.Metadata(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	require.Contains(t, rec.Header().Get("Cache-Control"), "max-age=60")

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

	// Required fields per §12.1.
	require.NotEmpty(t, resp["issuer"])
	issuer, _ := resp["issuer"].(string)
	require.False(t, strings.HasSuffix(issuer, "/"), "issuer must not have trailing slash (RFC 8414)")
	require.NotEmpty(t, resp["authorization_endpoint"])
	require.NotEmpty(t, resp["token_endpoint"])
	require.NotEmpty(t, resp["revocation_endpoint"])
	require.NotEmpty(t, resp["device_authorization_endpoint"])
	require.NotEmpty(t, resp["userinfo_endpoint"])

	scopes, _ := resp["scopes_supported"].([]any)
	require.Greater(t, len(scopes), 0)
	// Spot check canonical scopes are advertised.
	scopeSet := make(map[string]bool, len(scopes))
	for _, s := range scopes {
		if v, ok := s.(string); ok {
			scopeSet[v] = true
		}
	}
	require.True(t, scopeSet["models:read"])
	require.True(t, scopeSet["images:create"])
	require.True(t, scopeSet["responses:create"])
	require.True(t, scopeSet["offline_access"])

	grantTypes, _ := resp["grant_types_supported"].([]any)
	require.Contains(t, grantTypes, "authorization_code")
	require.Contains(t, grantTypes, "refresh_token")
	require.Contains(t, grantTypes, "urn:ietf:params:oauth:grant-type:device_code")

	codeMethods, _ := resp["code_challenge_methods_supported"].([]any)
	require.Equal(t, []any{"S256"}, codeMethods, "S256 only — plain is forbidden")
}

// ── Revoke (§12.9) ──────────────────────────────────────────────────────────

func TestRevoke_RejectsNonFormContentType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)

	c, rec := newGinTestContext(http.MethodPost, "/oauth/revoke",
		[]byte(`{"token":"x"}`), "application/json")
	h.Revoke(c)

	require.Equal(t, http.StatusBadRequest, rec.Code,
		"§12.9: revoke must reject JSON content type")
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "invalid_request", resp["error"])
}

func TestRevoke_UnknownToken_IsIdempotentSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)

	form := url.Values{}
	form.Set("token", "rt_unknown_token_xxx")
	form.Set("client_id", testOAuthClientID)
	c, rec := newGinTestContext(http.MethodPost, "/oauth/revoke",
		[]byte(form.Encode()), "application/x-www-form-urlencoded")
	h.Revoke(c)

	require.Equal(t, http.StatusOK, rec.Code,
		"RFC 7009: unknown token must return 200 (idempotency)")
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	require.Equal(t, "no-cache", rec.Header().Get("Pragma"))
}

func TestRevoke_RefreshToken_KillsGrantAndDoubleRevokeStillSucceeds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)

	// Mint a grant + access/refresh pair.
	const userID int64 = 444
	verifier, challenge := pkceVerifierAndChallengeForHandler("verifier-revoke-test-aaaaaaaaaaaaaaaaaaaaaaaaaa")
	code := beginAndApproveForHandler(t, h, userID, "image_generation", "x", challenge)

	tokenForm := url.Values{}
	tokenForm.Set("grant_type", "authorization_code")
	tokenForm.Set("redirect_uri", testOAuthRedirectURI)
	tokenForm.Set("code", code)
	tokenForm.Set("code_verifier", verifier)
	tokenForm.Set("client_id", testOAuthClientID)
	c2, rec2 := newGinTestContext(http.MethodPost, "/oauth/token",
		[]byte(tokenForm.Encode()), "application/x-www-form-urlencoded")
	h.Token(c2)
	require.Equal(t, http.StatusOK, rec2.Code, "token: %s", rec2.Body.String())
	var tokenResp map[string]any
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &tokenResp))
	refresh, _ := tokenResp["refresh_token"].(string)
	require.NotEmpty(t, refresh)

	// First revoke — succeeds.
	revokeForm := url.Values{}
	revokeForm.Set("token", refresh)
	revokeForm.Set("token_type_hint", "refresh_token")
	revokeForm.Set("client_id", testOAuthClientID)
	c3, rec3 := newGinTestContext(http.MethodPost, "/oauth/revoke",
		[]byte(revokeForm.Encode()), "application/x-www-form-urlencoded")
	h.Revoke(c3)
	require.Equal(t, http.StatusOK, rec3.Code)

	// Second revoke of the same token — still 200 (idempotent).
	c4, rec4 := newGinTestContext(http.MethodPost, "/oauth/revoke",
		[]byte(revokeForm.Encode()), "application/x-www-form-urlencoded")
	h.Revoke(c4)
	require.Equal(t, http.StatusOK, rec4.Code,
		"§12.9: re-revoking an already-revoked token must still return 200")
}

// ── Authorized Apps mounted endpoints (§12.10) ──────────────────────────────
//
// These don't go through the v2 access metadata path because the harness
// doesn't wire one — the goal here is to lock in the auth + path-param
// handling, not the join logic (covered in service tests where the v2 access
// repo is exercised against a real DB).

func TestListAuthorizedApps_Unauthenticated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)

	c, rec := newGinTestContext(http.MethodGet, "/api/v1/oauth/authorized-apps", nil, "")
	h.ListAuthorizedApps(c)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestListAuthorizedApps_EmptyForUserWithNoGrants(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)

	c, rec := newGinTestContext(http.MethodGet, "/api/v1/oauth/authorized-apps", nil, "")
	c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 7, Concurrency: 1})
	h.ListAuthorizedApps(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp.Items, "items must be JSON array, never null")
	require.Len(t, resp.Items, 0)
}

func TestRevokeAuthorizedApp_Unauthenticated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)

	c, rec := newGinTestContext(http.MethodDelete,
		"/api/v1/oauth/authorized-apps/some-grant-id", nil, "")
	c.Params = gin.Params{{Key: "grant_id", Value: "some-grant-id"}}
	h.RevokeAuthorizedApp(c)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRevokeAuthorizedApp_MissingGrantID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)

	c, rec := newGinTestContext(http.MethodDelete,
		"/api/v1/oauth/authorized-apps/", nil, "")
	c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 1, Concurrency: 1})
	c.Params = gin.Params{{Key: "grant_id", Value: ""}}
	h.RevokeAuthorizedApp(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "invalid_request", resp["error"])
}

func TestRevokeAuthorizedApp_NotOwner_IsIdempotent(t *testing.T) {
	// Per §12.10: revoking a grant that doesn't belong to the requesting
	// user must NOT reveal whether the grant exists. The service swallows
	// the not-owner case and returns nil; the handler returns 200.
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)

	c, rec := newGinTestContext(http.MethodDelete,
		"/api/v1/oauth/authorized-apps/some-other-users-grant-id", nil, "")
	c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 1, Concurrency: 1})
	c.Params = gin.Params{{Key: "grant_id", Value: "some-other-users-grant-id"}}
	h.RevokeAuthorizedApp(c)

	require.Equal(t, http.StatusOK, rec.Code,
		"§12.10: missing/other-user grant must be idempotent — never disclose existence")
}

func TestRevokeAuthorizedClient_Unauthenticated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)

	c, rec := newGinTestContext(http.MethodDelete,
		"/api/v1/oauth/authorized-apps/client/"+testOAuthClientID, nil, "")
	c.Params = gin.Params{{Key: "client_id", Value: testOAuthClientID}}
	h.RevokeAuthorizedClient(c)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRevokeAuthorizedClient_HappyPath_ReturnsRevokedCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)

	const userID int64 = 555
	mintAccessGrantForHandler(t, h, userID)

	c, rec := newGinTestContext(http.MethodDelete,
		"/api/v1/oauth/authorized-apps/client/"+testOAuthClientID, nil, "")
	c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: userID, Concurrency: 1})
	c.Params = gin.Params{{Key: "client_id", Value: testOAuthClientID}}
	h.RevokeAuthorizedClient(c)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.GreaterOrEqual(t, int(resp["revoked"].(float64)), 1,
		"happy path: at least one grant should be revoked")
}

func TestRevokeAuthorizedClient_NoGrants_IsIdempotent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)

	c, rec := newGinTestContext(http.MethodDelete,
		"/api/v1/oauth/authorized-apps/client/"+testOAuthClientID, nil, "")
	c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 999, Concurrency: 1})
	c.Params = gin.Params{{Key: "client_id", Value: testOAuthClientID}}
	h.RevokeAuthorizedClient(c)

	require.Equal(t, http.StatusOK, rec.Code,
		"§12.10: idempotent for users with no grants for this client")
}

// ── FIX A5: Discovery issuer priority order ─────────────────────────────────
//
// The §12.1 RFC 8414 discovery document and §12.7 device flow verification_uri
// MUST resolve their canonical origin from the same priority order so an
// operator who follows the migration 145 instruction ("set oauth_issuer per
// deployment") cannot end up with discovery quietly publishing one host while
// the device flow advertises another. These tests pin the order:
//
//  1. settings.oauth_issuer  (canonical)
//  2. settings.frontend_url  (legacy fallback)
//  3. request scheme://Host  (last-resort fallback when both are empty)

func newDiscoveryHarnessWithSettings(t *testing.T, seed map[string]string) *OAuthProviderHandler {
	t.Helper()
	settingRepo := &oauthHandlerSettingRepoStub{values: map[string]string{
		"oauth_provider_enabled": "true",
	}}
	for k, v := range seed {
		settingRepo.values[k] = v
	}
	settings := service.NewSettingService(settingRepo, &config.Config{})

	// Reuse the production handler harness's client/repo wiring; we only need
	// a working OAuthProviderService for IsEnabled() to pass through Metadata.
	groupID := int64(5)
	clientRepo := &oauthHandlerClientRepoStub{clients: map[string]*service.OAuthClient{
		testOAuthClientID: {
			ClientID:               testOAuthClientID,
			Name:                   testOAuthClientName,
			RedirectURIs:           []string{testOAuthRedirectURI},
			AllowedScopes:          []string{"image_generation", "balance:read", "models:read"},
			PKCERequired:           true,
			DefaultGroupID:         &groupID,
			AccessTokenTTLSeconds:  86400,
			RefreshTokenTTLSeconds: 2592000,
		},
	}}
	codeRepo := newOAuthHandlerCodeRepoStub()
	svc := service.NewOAuthProviderService(
		clientRepo,
		codeRepo,
		newOAuthHandlerRefreshRepoStub(),
		nil, nil,
		newOAuthHandlerAuthorizeTxRepoStub(codeRepo),
		&oauthHandlerAPIKeyOAuthAdapter{oauthHandlerAPIKeyRepoStub: newOAuthHandlerAPIKeyRepoStub()},
		nil, nil,
		settingRepo,
		nil,
	)
	return NewOAuthProviderHandler(svc, settings)
}

func metadataIssuer(t *testing.T, h *OAuthProviderHandler, host string) string {
	t.Helper()
	c, rec := newGinTestContext(http.MethodGet, "/.well-known/oauth-authorization-server", nil, "")
	c.Request.Host = host
	h.Metadata(c)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	issuer, _ := resp["issuer"].(string)
	require.False(t, strings.HasSuffix(issuer, "/"), "RFC 8414: issuer must not have trailing slash")
	return issuer
}

func TestDiscoveryIssuer_PrefersOAuthIssuerOverFrontendURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newDiscoveryHarnessWithSettings(t, map[string]string{
		"oauth_issuer": "https://canonical.example.com",
		"frontend_url": "https://legacy.example.com",
	})
	require.Equal(t, "https://canonical.example.com",
		metadataIssuer(t, h, "request-host.example"),
		"FIX A5: oauth_issuer must win over frontend_url")
}

func TestDiscoveryIssuer_FallsBackToFrontendURLWhenIssuerEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newDiscoveryHarnessWithSettings(t, map[string]string{
		"frontend_url": "https://legacy.example.com",
	})
	require.Equal(t, "https://legacy.example.com",
		metadataIssuer(t, h, "request-host.example"),
		"FIX A5: frontend_url is the legacy fallback")
}

func TestDiscoveryIssuer_FallsBackToRequestHostWhenBothEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newDiscoveryHarnessWithSettings(t, map[string]string{})
	// No TLS on httptest request, no X-Forwarded-Proto → http scheme.
	require.Equal(t, "http://request-host.example",
		metadataIssuer(t, h, "request-host.example"),
		"FIX A5: last-resort request scheme://Host fallback for misconfigured deployments")
}

func TestDiscoveryIssuer_StripsTrailingSlashFromCanonical(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newDiscoveryHarnessWithSettings(t, map[string]string{
		"oauth_issuer": "https://canonical.example.com/",
	})
	require.Equal(t, "https://canonical.example.com",
		metadataIssuer(t, h, "request-host.example"),
		"FIX A5: trailing slash must be stripped (RFC 8414 forbids it on issuer)")
}

func TestDiscoveryIssuer_WhitespaceOnlyValueFallsThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newDiscoveryHarnessWithSettings(t, map[string]string{
		"oauth_issuer": "   ", // whitespace-only must NOT win
		"frontend_url": "https://legacy.example.com",
	})
	require.Equal(t, "https://legacy.example.com",
		metadataIssuer(t, h, "request-host.example"),
		"FIX A5: whitespace-only canonical must fall through to frontend_url")
}

// ── FIX A1 / A4 wave coverage ──────────────────────────────────────────────
//
// These tests pin the v2 transaction-bound /authorize → /approve contract:
//   - body shape: only transaction_id, csrf_token, decision, group_id?
//   - subject binding: foreign JWT cannot consume another user's transaction
//   - CSRF binding: wrong csrf_token rejects
//   - /begin creates a row with the JWT subject as user_id

// TestApprove_RejectsBodyWithLegacyParams pins the §10.3 attack-surface
// closure: an approve POST that omits transaction_id + csrf_token (the only
// two fields gin's binding tags now require) MUST fail at the binding layer
// even when it carries the legacy client_id / redirect_uri / scope shape.
//
// Without this rejection, an attacker who landed an XSS on the consent page
// (or who guessed/replayed a transaction id) could re-supply scopes for a
// different client and we'd happily mint a code with attacker-chosen
// parameters. The transaction_id is the only piece of state that's bound to
// the user; everything else has to come from the row.
func TestApprove_RejectsBodyWithLegacyParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)

	legacyOnly, _ := json.Marshal(map[string]string{
		"client_id":     testOAuthClientID,
		"redirect_uri":  testOAuthRedirectURI,
		"response_type": "code",
		"scope":         "image_generation",
		"state":         "x",
		"decision":      "approve",
	})
	c, rec := newGinTestContext(http.MethodPost, "/api/v1/oauth/authorize/approve", legacyOnly, "application/json")
	c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: 11, Concurrency: 1})
	h.Approve(c)

	require.Equal(t, http.StatusBadRequest, rec.Code,
		"approve must reject the legacy body (no transaction_id / csrf_token)")
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "invalid_request", resp["error"],
		"binding failure must surface as invalid_request, not server_error")
}

// TestApprove_SubjectMismatchRejected — full HTTP path: user A opens the
// transaction via /begin, user B's JWT submits /approve with the same CSRF
// token. Expect 403 + opaque error_description (no leakage of "subject
// mismatch") and the transaction must remain unconsumed so user A can still
// complete the flow.
func TestApprove_SubjectMismatchRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)
	_, challenge := pkceVerifierAndChallengeForHandler(
		"verifier-subject-mismatch-aaaaaaaaaaaaaaaaaa")

	const userA int64 = 100
	const userB int64 = 200

	// Step 1: user A opens the transaction.
	beginBody, _ := json.Marshal(map[string]string{
		"client_id":             testOAuthClientID,
		"redirect_uri":          testOAuthRedirectURI,
		"response_type":         "code",
		"scope":                 "image_generation",
		"state":                 "abc",
		"code_challenge":        challenge,
		"code_challenge_method": "S256",
	})
	bc, brec := newGinTestContext(http.MethodPost, "/api/v1/oauth/authorize/begin", beginBody, "application/json")
	bc.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: userA, Concurrency: 1})
	h.BeginAuthorize(bc)
	require.Equal(t, http.StatusOK, brec.Code, "begin: %s", brec.Body.String())
	var beginResp map[string]any
	require.NoError(t, json.Unmarshal(brec.Body.Bytes(), &beginResp))
	txID, _ := beginResp["transaction_id"].(string)
	csrf, _ := beginResp["csrf_token"].(string)
	require.NotEmpty(t, txID)
	require.NotEmpty(t, csrf)

	// Step 2: user B (different JWT subject) tries to approve.
	approveBody, _ := json.Marshal(map[string]string{
		"transaction_id": txID,
		"csrf_token":     csrf,
		"decision":       "approve",
	})
	c, rec := newGinTestContext(http.MethodPost, "/api/v1/oauth/authorize/approve", approveBody, "application/json")
	c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: userB, Concurrency: 1})
	h.Approve(c)

	require.Equal(t, http.StatusForbidden, rec.Code,
		"foreign-subject approve must return 403; body=%s", rec.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "invalid_request", resp["error"],
		"subject mismatch is opaque — error_description must NOT reveal the mismatch reason")
	desc, _ := resp["error_description"].(string)
	require.NotContains(t, strings.ToLower(desc), "subject",
		"error_description leaks attack signal: %q", desc)
	require.NotContains(t, strings.ToLower(desc), "mismatch",
		"error_description leaks attack signal: %q", desc)

	// Step 3: user A must still be able to approve — the foreign-subject
	// attempt cannot consume the transaction.
	c2, rec2 := newGinTestContext(http.MethodPost, "/api/v1/oauth/authorize/approve", approveBody, "application/json")
	c2.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: userA, Concurrency: 1})
	h.Approve(c2)
	require.Equal(t, http.StatusOK, rec2.Code,
		"legitimate user must still be able to approve; body=%s", rec2.Body.String())
}

// TestApprove_CSRFMismatch — a wrong csrf_token rejects with 403.
func TestApprove_CSRFMismatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newOAuthProviderHandlerHarness(t)
	_, challenge := pkceVerifierAndChallengeForHandler(
		"verifier-csrf-mismatch-aaaaaaaaaaaaaaaaaaaa")

	const userID int64 = 77

	beginBody, _ := json.Marshal(map[string]string{
		"client_id":             testOAuthClientID,
		"redirect_uri":          testOAuthRedirectURI,
		"response_type":         "code",
		"scope":                 "image_generation",
		"state":                 "abc",
		"code_challenge":        challenge,
		"code_challenge_method": "S256",
	})
	bc, brec := newGinTestContext(http.MethodPost, "/api/v1/oauth/authorize/begin", beginBody, "application/json")
	bc.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: userID, Concurrency: 1})
	h.BeginAuthorize(bc)
	require.Equal(t, http.StatusOK, brec.Code, "begin: %s", brec.Body.String())
	var beginResp map[string]any
	require.NoError(t, json.Unmarshal(brec.Body.Bytes(), &beginResp))
	txID, _ := beginResp["transaction_id"].(string)
	require.NotEmpty(t, txID)

	approveBody, _ := json.Marshal(map[string]string{
		"transaction_id": txID,
		"csrf_token":     "definitely-not-the-right-csrf-token",
		"decision":       "approve",
	})
	c, rec := newGinTestContext(http.MethodPost, "/api/v1/oauth/authorize/approve", approveBody, "application/json")
	c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: userID, Concurrency: 1})
	h.Approve(c)

	require.Equal(t, http.StatusForbidden, rec.Code,
		"wrong csrf_token must return 403; body=%s", rec.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "invalid_request", resp["error"])
}

// TestAuthorize_CreatesTransaction — /begin captures the JWT subject as the
// transaction's user_id so subsequent approve POSTs can re-verify it.
func TestAuthorize_CreatesTransaction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, svc := newOAuthProviderHandlerHarness(t)
	_, challenge := pkceVerifierAndChallengeForHandler(
		"verifier-creates-tx-aaaaaaaaaaaaaaaaaaaaaa")

	const userID int64 = 1234

	beginBody, _ := json.Marshal(map[string]string{
		"client_id":             testOAuthClientID,
		"redirect_uri":          testOAuthRedirectURI,
		"response_type":         "code",
		"scope":                 "image_generation",
		"state":                 "rnd",
		"code_challenge":        challenge,
		"code_challenge_method": "S256",
	})
	c, rec := newGinTestContext(http.MethodPost, "/api/v1/oauth/authorize/begin", beginBody, "application/json")
	c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: userID, Concurrency: 1})
	h.BeginAuthorize(c)

	require.Equal(t, http.StatusOK, rec.Code, "begin body: %s", rec.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	txID, _ := resp["transaction_id"].(string)
	csrfPlain, _ := resp["csrf_token"].(string)
	require.NotEmpty(t, txID, "transaction_id missing from /begin response")
	require.NotEmpty(t, csrfPlain, "csrf_token plaintext missing from /begin response")

	// Inspect the transaction row through the service-layer load helper.
	// LoadAuthorizeTransactionForApproval re-runs the CSRF + subject check,
	// so passing the matching userID + plaintext CSRF is the public-API way
	// to confirm both fields landed on the row correctly.
	tx, _, err := svc.LoadAuthorizeTransactionForApproval(context.Background(), txID, csrfPlain, userID)
	require.NoError(t, err, "transaction must be loadable with matching csrf + subject")
	require.Equal(t, userID, tx.UserID,
		"transaction user_id must match the JWT subject from /begin")
	require.Equal(t, testOAuthClientID, tx.ClientID)

	// And the same load with the wrong subject must reject — proves the
	// row's subject is enforceable end-to-end.
	_, _, mismatchErr := svc.LoadAuthorizeTransactionForApproval(context.Background(), txID, csrfPlain, userID+1)
	require.Error(t, mismatchErr,
		"loading with a different subject must fail; got nil")
}
