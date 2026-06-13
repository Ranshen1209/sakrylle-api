// Tests for the OAuth scope middleware (§16 Phase 3 / §18.5).
//
// Goals:
//   - Manual API key passes through every gate untouched.
//   - Feature-flag-off short-circuits before any DB lookup.
//   - sk_oauth_ tokens enforce §7.3 scope matrix; missing scope returns
//     403 + WWW-Authenticate.
//   - Unlisted routes reject sk_oauth_ tokens by default while still
//     allowing manual API keys.
package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// ── stubs ──────────────────────────────────────────────────────────────────
//
// The middleware reaches into OAuthProviderService.LoadOAuthAccessMetadata,
// IsScopeEnforcementEnabled, and TouchAccessTokenLastUsed. We construct a
// real *service.OAuthProviderService via NewOAuthProviderService with the
// minimum stubs required to drive these three methods.

type middlewareSettingRepoStub struct {
	values map[string]string
}

func (s *middlewareSettingRepoStub) Get(_ context.Context, key string) (*service.Setting, error) {
	if v, ok := s.values[key]; ok {
		return &service.Setting{Key: key, Value: v}, nil
	}
	return nil, errors.New("not found")
}
func (s *middlewareSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if v, ok := s.values[key]; ok {
		return v, nil
	}
	return "", errors.New("not found")
}
func (s *middlewareSettingRepoStub) Set(_ context.Context, key, value string) error {
	s.values[key] = value
	return nil
}
func (s *middlewareSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, k := range keys {
		if v, ok := s.values[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}
func (s *middlewareSettingRepoStub) SetMultiple(_ context.Context, settings map[string]string) error {
	for k, v := range settings {
		s.values[k] = v
	}
	return nil
}
func (s *middlewareSettingRepoStub) GetAll(_ context.Context) (map[string]string, error) {
	out := make(map[string]string, len(s.values))
	for k, v := range s.values {
		out[k] = v
	}
	return out, nil
}
func (s *middlewareSettingRepoStub) Delete(_ context.Context, key string) error {
	delete(s.values, key)
	return nil
}

// middlewareAccessRepoStub records TouchAccessToken calls so a test can assert
// the throttled best-effort write happened (or didn't).
type middlewareAccessRepoStub struct {
	mu        sync.Mutex
	tokens    map[int64]*service.OAuthAccessToken
	touched   []int64
	loadCalls int
}

func newMiddlewareAccessRepoStub() *middlewareAccessRepoStub {
	return &middlewareAccessRepoStub{tokens: map[int64]*service.OAuthAccessToken{}}
}

func (s *middlewareAccessRepoStub) CreateAccessToken(_ context.Context, t *service.OAuthAccessToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *t
	s.tokens[t.APIKeyID] = &cp
	return nil
}
func (s *middlewareAccessRepoStub) GetActiveAccessTokenByAPIKeyID(_ context.Context, apiKeyID int64, now time.Time) (*service.OAuthAccessToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadCalls++
	t, ok := s.tokens[apiKeyID]
	if !ok {
		return nil, service.ErrOAuthAccessTokenNotFound
	}
	if t.RevokedAt != nil {
		return nil, service.ErrOAuthAccessTokenRevoked
	}
	if !t.ExpiresAt.After(now) {
		return nil, service.ErrOAuthAccessTokenExpired
	}
	cp := *t
	return &cp, nil
}
func (s *middlewareAccessRepoStub) RevokeAccessTokenByAPIKeyID(_ context.Context, apiKeyID int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tokens[apiKeyID]; ok {
		t.RevokedAt = &now
	}
	return nil
}
func (s *middlewareAccessRepoStub) RevokeAccessTokensByGrantID(_ context.Context, _ string, _ time.Time) ([]int64, error) {
	return nil, nil
}
func (s *middlewareAccessRepoStub) RevokeAccessTokensByTokenFamilyID(_ context.Context, _ string, _ time.Time) ([]int64, error) {
	return nil, nil
}
func (s *middlewareAccessRepoStub) TouchAccessToken(_ context.Context, apiKeyID int64, _ string, _ string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.touched = append(s.touched, apiKeyID)
	return nil
}
func (s *middlewareAccessRepoStub) ListActiveGrantsByUser(_ context.Context, _ int64, _ time.Time) ([]*service.OAuthAuthorizedGrant, error) {
	return nil, nil
}

// middlewareAPIKeyRepoStub: minimum to satisfy the OAuthAPIKeyRepository
// interface; the middleware doesn't actually exercise these branches but the
// service constructor requires the dependency.
type middlewareAPIKeyRepoStub struct{}

func (middlewareAPIKeyRepoStub) Create(_ context.Context, _ *service.APIKey) error { return nil }
func (middlewareAPIKeyRepoStub) GetByID(_ context.Context, _ int64) (*service.APIKey, error) {
	return nil, service.ErrAPIKeyNotFound
}
func (middlewareAPIKeyRepoStub) Update(_ context.Context, _ *service.APIKey) error { return nil }
func (middlewareAPIKeyRepoStub) GetKeyAndOwnerID(_ context.Context, _ int64) (string, int64, error) {
	return "", 0, nil
}
func (middlewareAPIKeyRepoStub) GetByKey(_ context.Context, _ string) (*service.APIKey, error) {
	return nil, service.ErrAPIKeyNotFound
}
func (middlewareAPIKeyRepoStub) GetByKeyForAuth(_ context.Context, _ string) (*service.APIKey, error) {
	return nil, service.ErrAPIKeyNotFound
}
func (middlewareAPIKeyRepoStub) Delete(_ context.Context, _ int64) error          { return nil }
func (middlewareAPIKeyRepoStub) DeleteWithAudit(_ context.Context, _ int64) error { return nil }
func (middlewareAPIKeyRepoStub) ListByUserID(_ context.Context, _ int64, _ pagination.PaginationParams, _ service.APIKeyListFilters) ([]service.APIKey, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (middlewareAPIKeyRepoStub) VerifyOwnership(_ context.Context, _ int64, ids []int64) ([]int64, error) {
	return ids, nil
}
func (middlewareAPIKeyRepoStub) CountByUserID(_ context.Context, _ int64) (int64, error) {
	return 0, nil
}
func (middlewareAPIKeyRepoStub) ExistsByKey(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (middlewareAPIKeyRepoStub) ListByGroupID(_ context.Context, _ int64, _ pagination.PaginationParams) ([]service.APIKey, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (middlewareAPIKeyRepoStub) SearchAPIKeys(_ context.Context, _ int64, _ string, _ int) ([]service.APIKey, error) {
	return nil, nil
}
func (middlewareAPIKeyRepoStub) ClearGroupIDByGroupID(_ context.Context, _ int64) (int64, error) {
	return 0, nil
}
func (middlewareAPIKeyRepoStub) UpdateGroupIDByUserAndGroup(_ context.Context, _, _, _ int64) (int64, error) {
	return 0, nil
}
func (middlewareAPIKeyRepoStub) CountByGroupID(_ context.Context, _ int64) (int64, error) {
	return 0, nil
}
func (middlewareAPIKeyRepoStub) ListKeysByUserID(_ context.Context, _ int64) ([]string, error) {
	return nil, nil
}
func (middlewareAPIKeyRepoStub) ListKeysByGroupID(_ context.Context, _ int64) ([]string, error) {
	return nil, nil
}
func (middlewareAPIKeyRepoStub) IncrementQuotaUsed(_ context.Context, _ int64, _ float64) (float64, error) {
	return 0, nil
}
func (middlewareAPIKeyRepoStub) UpdateLastUsed(_ context.Context, _ int64, _ time.Time) error {
	return nil
}
func (middlewareAPIKeyRepoStub) IncrementRateLimitUsage(_ context.Context, _ int64, _ float64) error {
	return nil
}
func (middlewareAPIKeyRepoStub) ResetRateLimitWindows(_ context.Context, _ int64) error { return nil }
func (middlewareAPIKeyRepoStub) GetRateLimitData(_ context.Context, _ int64) (*service.APIKeyRateLimitData, error) {
	return &service.APIKeyRateLimitData{}, nil
}
func (middlewareAPIKeyRepoStub) DisableAPIKeysByIDsReturningKeys(_ context.Context, _ []int64, _ time.Time) ([]string, error) {
	return nil, nil
}

// scopeHarness builds a service.OAuthProviderService with minimum stubs.
type scopeHarness struct {
	svc        *service.OAuthProviderService
	settings   *middlewareSettingRepoStub
	accessRepo *middlewareAccessRepoStub
}

func newScopeHarness(t *testing.T, enforcement bool) *scopeHarness {
	t.Helper()
	flagValue := "false"
	if enforcement {
		flagValue = "true"
	}
	settings := &middlewareSettingRepoStub{values: map[string]string{
		"oauth_provider_enabled":          "true",
		"oauth_scope_enforcement_enabled": flagValue,
	}}
	access := newMiddlewareAccessRepoStub()
	svc := service.NewOAuthProviderService(
		nil, nil, nil, access, nil, nil,
		middlewareAPIKeyRepoStub{},
		nil, nil, settings, nil,
	)
	return &scopeHarness{svc: svc, settings: settings, accessRepo: access}
}

func (h *scopeHarness) plantOAuthMetadata(apiKeyID int64, scopes []string) {
	h.accessRepo.mu.Lock()
	defer h.accessRepo.mu.Unlock()
	h.accessRepo.tokens[apiKeyID] = &service.OAuthAccessToken{
		APIKeyID:  apiKeyID,
		GrantID:   "grant-test",
		ClientID:  "test-client",
		AppType:   "web",
		UserID:    1,
		Scopes:    scopes,
		GroupID:   5,
		IssuedAt:  time.Now().Add(-time.Minute),
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
}

// scopeRunner wires API-key context + middleware into a one-shot gin engine
// and returns the recorder so tests can introspect headers/body.
func scopeRunner(method, path string, mw gin.HandlerFunc, apiKey *service.APIKey) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), apiKey)
		c.Next()
	})
	r.Use(mw)
	// Strip query params for route registration — gin matches on path only.
	routePath := path
	if idx := strings.Index(routePath, "?"); idx >= 0 {
		routePath = routePath[:idx]
	}
	r.Handle(method, routePath, func(c *gin.Context) { c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	r.ServeHTTP(rec, req)
	return rec
}

// ── tests ───────────────────────────────────────────────────────────────────

func TestRequireOAuthScope_ManualKey_PassesThrough(t *testing.T) {
	h := newScopeHarness(t, true)
	manualKey := &service.APIKey{ID: 7, Key: "sk-manual-aaa", GroupID: ptrInt64(5)}

	rec := scopeRunner(http.MethodGet, "/v1/models", RequireOAuthScope(h.svc), manualKey)

	require.Equal(t, http.StatusOK, rec.Code, "manual API key must pass through scope middleware untouched")
	require.Empty(t, rec.Header().Get("WWW-Authenticate"), "manual key path must not emit Bearer challenge")
}

func TestRequireOAuthScope_FlagDisabled_PassesThrough(t *testing.T) {
	h := newScopeHarness(t, false) // enforcement off
	oauthKey := &service.APIKey{ID: 8, Key: "sk_oauth_xxx", GroupID: ptrInt64(5)}
	h.plantOAuthMetadata(oauthKey.ID, []string{}) // empty scopes — would normally fail

	rec := scopeRunner(http.MethodGet, "/v1/models", RequireOAuthScope(h.svc), oauthKey)

	require.Equal(t, http.StatusOK, rec.Code, "feature flag off must short-circuit before scope check")
	require.Equal(t, 0, h.accessRepo.loadCalls, "service must not load metadata when feature flag is off")
}

func TestRequireOAuthScope_HasRequiredScope_AllowsAndTouches(t *testing.T) {
	h := newScopeHarness(t, true)
	oauthKey := &service.APIKey{ID: 9, Key: "sk_oauth_yyy", GroupID: ptrInt64(5)}
	h.plantOAuthMetadata(oauthKey.ID, []string{service.ScopeModelsRead})

	rec := scopeRunner(http.MethodGet, "/v1/models", RequireOAuthScope(h.svc), oauthKey)

	require.Equal(t, http.StatusOK, rec.Code, "token with models:read must access /v1/models")
	require.GreaterOrEqual(t, len(h.accessRepo.touched), 1, "TouchAccessToken should run on success")
	require.Equal(t, oauthKey.ID, h.accessRepo.touched[0])
}

func TestRequireOAuthScope_MissingScope_Returns403WithWWWAuthenticate(t *testing.T) {
	h := newScopeHarness(t, true)
	oauthKey := &service.APIKey{ID: 10, Key: "sk_oauth_zzz", GroupID: ptrInt64(5)}
	// Token has account:read but NOT models:read; /v1/models requires models:read.
	h.plantOAuthMetadata(oauthKey.ID, []string{service.ScopeAccountRead})

	rec := scopeRunner(http.MethodGet, "/v1/models", RequireOAuthScope(h.svc), oauthKey)

	require.Equal(t, http.StatusForbidden, rec.Code)
	auth := rec.Header().Get("WWW-Authenticate")
	require.Contains(t, auth, `error="insufficient_scope"`)
	require.Contains(t, auth, `scope="models:read"`,
		"WWW-Authenticate must list the required scope so clients can request it via re-authorization")
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	require.Contains(t, rec.Body.String(), `"error":"insufficient_scope"`)
}

func TestRequireOAuthScope_LegacyAlias_AcceptsImageGenerationForImagesCreate(t *testing.T) {
	// §7.2 / §18.2: legacy `image_generation` must satisfy `images:create`.
	h := newScopeHarness(t, true)
	oauthKey := &service.APIKey{ID: 11, Key: "sk_oauth_legacy", GroupID: ptrInt64(5)}
	h.plantOAuthMetadata(oauthKey.ID, []string{"image_generation"})

	rec := scopeRunner(http.MethodPost, "/v1/images/generations", RequireOAuthScope(h.svc), oauthKey)

	require.Equal(t, http.StatusOK, rec.Code,
		"legacy image_generation alias must satisfy images:create requirement on /v1/images/generations")
}

func TestRejectOAuthTokensForUnlistedResource_OAuthRejected(t *testing.T) {
	h := newScopeHarness(t, true)
	oauthKey := &service.APIKey{ID: 12, Key: "sk_oauth_qqq", GroupID: ptrInt64(5)}
	h.plantOAuthMetadata(oauthKey.ID, []string{service.ScopeModelsRead})

	rec := scopeRunner(http.MethodGet, "/v1beta/models",
		RejectOAuthTokensForUnlistedResource(h.svc), oauthKey)

	require.Equal(t, http.StatusForbidden, rec.Code,
		"OAuth tokens hitting Gemini-native /v1beta/* must be rejected (not in §7.3 matrix)")
	require.Contains(t, rec.Header().Get("WWW-Authenticate"), "insufficient_scope")
}

func TestRejectOAuthTokensForUnlistedResource_ManualKey_PassesThrough(t *testing.T) {
	h := newScopeHarness(t, true)
	manualKey := &service.APIKey{ID: 13, Key: "sk-manual-bbb", GroupID: ptrInt64(5)}

	rec := scopeRunner(http.MethodGet, "/v1beta/models",
		RejectOAuthTokensForUnlistedResource(h.svc), manualKey)

	require.Equal(t, http.StatusOK, rec.Code,
		"manual API keys remain unaffected by OAuth-only resource gates")
}

func TestRequireOAuthScope_UnlistedRoute_RejectsOAuthTokens(t *testing.T) {
	// A new API-key-authenticated route added without a §7.3 matrix entry must
	// reject sk_oauth_ tokens by default.
	h := newScopeHarness(t, true)
	oauthKey := &service.APIKey{ID: 14, Key: "sk_oauth_unlisted", GroupID: ptrInt64(5)}
	h.plantOAuthMetadata(oauthKey.ID, []string{service.ScopeModelsRead, service.ScopeMessagesCreate})

	rec := scopeRunner(http.MethodGet, "/v1/some-future-route-not-in-matrix",
		RequireOAuthScope(h.svc), oauthKey)

	require.Equal(t, http.StatusForbidden, rec.Code,
		"unlisted route + OAuth token must default to deny per §7.3")
}

func TestLoadOAuthMetadata_StashesMetadataForHandler(t *testing.T) {
	// /v1/me uses LoadOAuthMetadata so the handler can do field-level scope
	// cropping; confirm the metadata lands in gin context.
	h := newScopeHarness(t, true)
	oauthKey := &service.APIKey{ID: 15, Key: "sk_oauth_me", GroupID: ptrInt64(5)}
	h.plantOAuthMetadata(oauthKey.ID, []string{service.ScopeProfileRead})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), oauthKey)
		c.Next()
	})
	r.Use(LoadOAuthMetadata(h.svc))

	var seen *service.OAuthAccessToken
	r.GET("/v1/me", func(c *gin.Context) {
		if m, ok := GetOAuthAccessTokenFromContext(c); ok {
			seen = m
		}
		c.Status(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/me", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, seen, "LoadOAuthMetadata must stash *OAuthAccessToken in gin.Context for handlers")
	require.Equal(t, "test-client", seen.ClientID)
	require.Contains(t, seen.Scopes, service.ScopeProfileRead)
}

func TestWriteOAuthResourceError_FormatsBearerChallenge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)

	WriteOAuthResourceError(c, OAuthResourceError{
		Status:         http.StatusForbidden,
		Code:           OAuthErrInsufficientScope,
		Description:    "required scope: models:read",
		RequiredScopes: []string{"models:read"},
	})

	require.Equal(t, http.StatusForbidden, rec.Code)
	auth := rec.Header().Get("WWW-Authenticate")
	require.True(t, strings.HasPrefix(auth, "Bearer error="), "challenge must start with Bearer error=")
	require.Contains(t, auth, `error="insufficient_scope"`)
	require.Contains(t, auth, `scope="models:read"`)
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	require.Equal(t, "no-cache", rec.Header().Get("Pragma"))
	require.Contains(t, rec.Body.String(), `"error":"insufficient_scope"`)
}

// FIX H5: when no api key landed in context, the middleware MUST fail closed.
// A pass-through here would silently bypass scope enforcement on routes that
// mounted RequireOAuthScope before (mistakenly) running auth.
func TestRequireOAuthScope_NoAPIKeyContext_FailsClosed(t *testing.T) {
	h := newScopeHarness(t, true)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	// NOTE: no API-key-context injector here — simulate misconfigured pipeline.
	r.Use(RequireOAuthScope(h.svc))
	r.GET("/v1/models", func(c *gin.Context) { c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

	require.Equal(t, http.StatusInternalServerError, rec.Code,
		"missing api key context must fail closed (500), never pass through")
	require.Contains(t, rec.Body.String(), `"server_error"`)
}

// FIX H5: a sk_oauth_ token without an oauth_access_tokens row is corrupt
// state. With enforcement enabled the middleware must reject it as
// invalid_token rather than fall through to the gateway.
func TestRequireOAuthScope_OAuthTokenMissingMetadata_FailsClosed(t *testing.T) {
	h := newScopeHarness(t, true) // enforcement on
	oauthKey := &service.APIKey{ID: 16, Key: "sk_oauth_orphan", GroupID: ptrInt64(5)}
	// Deliberately do NOT plant metadata.

	rec := scopeRunner(http.MethodGet, "/v1/models", RequireOAuthScope(h.svc), oauthKey)

	require.Equal(t, http.StatusUnauthorized, rec.Code,
		"sk_oauth_ token without oauth_access_tokens row must reject with 401 invalid_token")
	require.Contains(t, rec.Header().Get("WWW-Authenticate"), `error="invalid_token"`)
}

// ── §7.3 endpoint scope matrix: comprehensive per-endpoint tests ─────────────
//
// Each sub-test validates that the scope policy for a given (method, path)
// accepts the correct scope(s) and rejects unrelated scopes.

func TestRequireOAuthScope_EndpointMatrix_CorrectScopeAllows(t *testing.T) {
	// Table: (method, path, scopes that MUST satisfy the policy).
	cases := []struct {
		name   string
		method string
		path   string
		scopes []string
	}{
		{"chat/completions", http.MethodPost, "/v1/chat/completions", []string{service.ScopeChatCompletionsCreate}},
		{"chat/completions-bare", http.MethodPost, "/chat/completions", []string{service.ScopeChatCompletionsCreate}},
		{"responses POST", http.MethodPost, "/v1/responses", []string{service.ScopeResponsesCreate}},
		{"responses GET", http.MethodGet, "/v1/responses/resp_abc123", []string{service.ScopeResponsesCreate}},
		{"responses DELETE", http.MethodDelete, "/v1/responses/resp_abc123", []string{service.ScopeResponsesCreate}},
		{"responses PATCH", http.MethodPatch, "/v1/responses/resp_abc123", []string{service.ScopeResponsesCreate}},
		{"responses-bare POST", http.MethodPost, "/responses", []string{service.ScopeResponsesCreate}},
		{"codex/responses POST", http.MethodPost, "/v1/codex/responses", []string{service.ScopeResponsesCreate}},
		{"messages", http.MethodPost, "/v1/messages", []string{service.ScopeMessagesCreate}},
		{"messages count_tokens", http.MethodPost, "/v1/messages/count_tokens", []string{service.ScopeMessagesCreate}},
		{"images/generations", http.MethodPost, "/v1/images/generations", []string{service.ScopeImagesCreate}},
		{"images/edits", http.MethodPost, "/v1/images/edits", []string{service.ScopeImagesCreate}},
		{"images/generations-bare", http.MethodPost, "/images/generations", []string{service.ScopeImagesCreate}},
		{"images/edits-bare", http.MethodPost, "/images/edits", []string{service.ScopeImagesCreate}},
		{"models", http.MethodGet, "/v1/models", []string{service.ScopeModelsRead}},
		{"usage", http.MethodGet, "/v1/usage", []string{service.ScopeUsageRead}},
		{"me via openid", http.MethodGet, "/v1/me", []string{service.ScopeOpenID}},
		{"me via profile:read", http.MethodGet, "/v1/me", []string{service.ScopeProfileRead}},
		{"me via account:read", http.MethodGet, "/v1/me", []string{service.ScopeAccountRead}},
		{"me via account:balance:read", http.MethodGet, "/v1/me", []string{service.ScopeAccountBalanceRead}},
		{"account/balance via balance:read", http.MethodGet, "/v1/account/balance", []string{service.ScopeAccountBalanceRead}},
		{"account/balance via account:read", http.MethodGet, "/v1/account/balance", []string{service.ScopeAccountRead}},
		{"antigravity models", http.MethodGet, "/antigravity/models", []string{service.ScopeModelsRead}},
		{"antigravity v1 models", http.MethodGet, "/antigravity/v1/models", []string{service.ScopeModelsRead}},
		{"antigravity v1 usage", http.MethodGet, "/antigravity/v1/usage", []string{service.ScopeUsageRead}},
		{"antigravity v1 messages", http.MethodPost, "/antigravity/v1/messages", []string{service.ScopeMessagesCreate}},
		{"antigravity v1 messages count_tokens", http.MethodPost, "/antigravity/v1/messages/count_tokens", []string{service.ScopeMessagesCreate}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newScopeHarness(t, true)
			oauthKey := &service.APIKey{ID: 20, Key: "sk_oauth_matrix_ok", GroupID: ptrInt64(5)}
			h.plantOAuthMetadata(oauthKey.ID, tc.scopes)

			rec := scopeRunner(tc.method, tc.path, RequireOAuthScope(h.svc), oauthKey)

			require.Equal(t, http.StatusOK, rec.Code,
				"endpoint %s %s must accept scope %v", tc.method, tc.path, tc.scopes)
		})
	}
}

func TestRequireOAuthScope_EndpointMatrix_WrongScopeRejects(t *testing.T) {
	// Table: (method, path, scope granted — which must NOT satisfy the policy).
	// We always use account:read as the "wrong" scope since it's only valid for
	// /v1/me and /v1/account/balance, not for any of the gateway endpoints.
	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"chat/completions", http.MethodPost, "/v1/chat/completions"},
		{"responses POST", http.MethodPost, "/v1/responses"},
		{"responses GET", http.MethodGet, "/v1/responses/resp_abc"},
		{"messages", http.MethodPost, "/v1/messages"},
		{"messages count_tokens", http.MethodPost, "/v1/messages/count_tokens"},
		{"images/generations", http.MethodPost, "/v1/images/generations"},
		{"images/edits", http.MethodPost, "/v1/images/edits"},
		{"models", http.MethodGet, "/v1/models"},
		{"usage", http.MethodGet, "/v1/usage"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newScopeHarness(t, true)
			oauthKey := &service.APIKey{ID: 21, Key: "sk_oauth_matrix_bad", GroupID: ptrInt64(5)}
			// account:read is NOT valid for any of these gateway endpoints.
			h.plantOAuthMetadata(oauthKey.ID, []string{service.ScopeAccountRead})

			rec := scopeRunner(tc.method, tc.path, RequireOAuthScope(h.svc), oauthKey)

			require.Equal(t, http.StatusForbidden, rec.Code,
				"endpoint %s %s must reject when token only has account:read", tc.method, tc.path)
			require.Contains(t, rec.Header().Get("WWW-Authenticate"), `error="insufficient_scope"`)
		})
	}
}

func TestRequireOAuthScope_EndpointMatrix_UnrelatedScopeRejects(t *testing.T) {
	// Verify that usage:read does NOT grant access to models, and models:read
	// does NOT grant access to usage — cross-scope isolation.
	cases := []struct {
		name       string
		method     string
		path       string
		wrongScope string
	}{
		{"models with usage:read", http.MethodGet, "/v1/models", service.ScopeUsageRead},
		{"usage with models:read", http.MethodGet, "/v1/usage", service.ScopeModelsRead},
		{"chat with messages:create", http.MethodPost, "/v1/chat/completions", service.ScopeMessagesCreate},
		{"messages with chat.completions:create", http.MethodPost, "/v1/messages", service.ScopeChatCompletionsCreate},
		{"images with models:read", http.MethodPost, "/v1/images/generations", service.ScopeModelsRead},
		{"models with images:create", http.MethodGet, "/v1/models", service.ScopeImagesCreate},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newScopeHarness(t, true)
			oauthKey := &service.APIKey{ID: 22, Key: "sk_oauth_cross_scope", GroupID: ptrInt64(5)}
			h.plantOAuthMetadata(oauthKey.ID, []string{tc.wrongScope})

			rec := scopeRunner(tc.method, tc.path, RequireOAuthScope(h.svc), oauthKey)

			require.Equal(t, http.StatusForbidden, rec.Code,
				"scope %s must NOT satisfy %s %s", tc.wrongScope, tc.method, tc.path)
		})
	}
}

func TestRejectOAuthTokensForUnlistedResource_FlagDisabled_PassesThrough(t *testing.T) {
	h := newScopeHarness(t, false) // enforcement off
	oauthKey := &service.APIKey{ID: 23, Key: "sk_oauth_unlisted_flag", GroupID: ptrInt64(5)}
	h.plantOAuthMetadata(oauthKey.ID, []string{service.ScopeModelsRead})

	rec := scopeRunner(http.MethodGet, "/v1beta/models",
		RejectOAuthTokensForUnlistedResource(h.svc), oauthKey)

	require.Equal(t, http.StatusOK, rec.Code,
		"flag disabled must allow OAuth tokens through to unlisted resources")
}

func TestRequireOAuthScope_TrailingSlash_Matches(t *testing.T) {
	// The regex patterns accept optional trailing slashes (/v1/models/?).
	h := newScopeHarness(t, true)
	oauthKey := &service.APIKey{ID: 24, Key: "sk_oauth_slash", GroupID: ptrInt64(5)}
	h.plantOAuthMetadata(oauthKey.ID, []string{service.ScopeModelsRead})

	rec := scopeRunner(http.MethodGet, "/v1/models/", RequireOAuthScope(h.svc), oauthKey)

	require.Equal(t, http.StatusOK, rec.Code,
		"trailing slash must be tolerated by the scope policy regex")
}

func TestRequireOAuthScope_WithQueryParams_Matches(t *testing.T) {
	// Query strings must be stripped before matching.
	h := newScopeHarness(t, true)
	oauthKey := &service.APIKey{ID: 25, Key: "sk_oauth_query", GroupID: ptrInt64(5)}
	h.plantOAuthMetadata(oauthKey.ID, []string{service.ScopeModelsRead})

	rec := scopeRunner(http.MethodGet, "/v1/models?limit=10&offset=0", RequireOAuthScope(h.svc), oauthKey)

	require.Equal(t, http.StatusOK, rec.Code,
		"query parameters must not interfere with scope policy matching")
}

func ptrInt64(v int64) *int64 { return &v }
