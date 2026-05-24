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
	"strings"
	"sync"
	"testing"
	"time"

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
			PKCERequired:           true,
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
		newOAuthHandlerAPIKeyRepoStub(),
		nil, // GroupRepository — unused on this code path
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
	require.Equal(t, "image_generation balance:read", resp["scope"])
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
		"client_id":     testOAuthClientID,
		"redirect_uri":  testOAuthRedirectURI,
		"response_type": "code",
		"state":         "abc",
		"decision":      "approve",
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

	body := map[string]string{
		"client_id":             testOAuthClientID,
		"redirect_uri":          testOAuthRedirectURI,
		"response_type":         "code",
		"scope":                 "image_generation",
		"state":                 "echo-this-state",
		"code_challenge":        challenge,
		"code_challenge_method": "S256",
		"decision":              "deny",
	}
	raw, _ := json.Marshal(body)
	c, rec := newGinTestContext(http.MethodPost, "/api/v1/oauth/authorize/approve", raw, "application/json")
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
	got := oauthConsentHTML(testOAuthClientName, req)

	// The smoking gun is the literal closing-script-tag-then-opening-script
	// sequence. If this substring appears in the rendered HTML the browser
	// will execute the injected payload.
	require.NotContains(t, got, "</script><script>",
		"consent HTML allows attacker-controlled state to break out of inline <script>; "+
			"untrusted query params must be encoded so '</script>' inside a JS string literal "+
			"is rendered as e.g. '<\\/script>'")
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
		newOAuthHandlerAPIKeyRepoStub(),
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

	// 1. Approve → get authorization code via redirect_to JSON.
	approveBody, _ := json.Marshal(map[string]string{
		"client_id":             testOAuthClientID,
		"redirect_uri":          testOAuthRedirectURI,
		"response_type":         "code",
		"scope":                 "image_generation",
		"state":                 "x",
		"code_challenge":        challenge,
		"code_challenge_method": "S256",
		"decision":              "approve",
	})
	c, rec := newGinTestContext(http.MethodPost, "/api/v1/oauth/authorize/approve", approveBody, "application/json")
	c.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: userID, Concurrency: 1})
	h.Approve(c)
	require.Equal(t, http.StatusOK, rec.Code, "approve failed: %s", rec.Body.String())

	var approveResp map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &approveResp))
	parsed, err := url.Parse(approveResp["redirect_to"])
	require.NoError(t, err)
	code := parsed.Query().Get("code")
	require.NotEmpty(t, code)

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
	require.Equal(t, []any{"image_generation"}, scopes)
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
