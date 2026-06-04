// Tests for the /v1/me handler (§12.11 / §16 Phase 3).
//
// Covers:
//   - manual API key returns the full account/group view (not scope-cropped)
//   - OAuth tokens are scope-cropped per §12.11 field map
//   - missing required scope (none of profile:read / account:read /
//     account:balance:read) returns 403 insufficient_scope
package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// newAccountHandlerHarness builds an AccountInfoHandler with no live OAuth
// service wiring (allowed_groups will be empty in OAuth-mode tests; that's
// fine because we're testing scope cropping, not group enumeration).
func newAccountHandlerHarness() *AccountInfoHandler {
	return NewAccountInfoHandler(nil, nil)
}

// fakeAPIKey returns an APIKey populated with a User + Group so /v1/me has
// something to render. The `keyPrefix` argument controls whether the handler
// treats this as a manual key (`sk-`) or an OAuth-issued token (`sk_oauth_`)
// — though /v1/me itself decides via gin context (OAuth metadata stash), so
// the prefix on Key is informational here.
func fakeAPIKey(t *testing.T, keyPrefix string) *service.APIKey {
	t.Helper()
	return &service.APIKey{
		ID:  101,
		Key: keyPrefix + "test",
		User: &service.User{
			ID:       42,
			Username: "alice",
			Email:    "alice@example.com",
			Balance:  8.94,
		},
		Group: &service.Group{
			ID:                   3,
			Name:                 "GPT-Pro",
			RateMultiplier:       0.4,
			AllowImageGeneration: false,
		},
	}
}

func TestMe_ManualKey_ReturnsFullView(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newAccountHandlerHarness()
	apiKey := fakeAPIKey(t, "sk-")

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	c.Set(string(servermiddleware.ContextKeyAPIKey), apiKey)
	// No OAuth metadata set → manual-key path.

	h.Me(c)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "api_key", resp["auth_type"])
	user, _ := resp["user"].(map[string]any)
	require.Equal(t, "alice", user["username"])
	// Manual key path must include account/balance.
	account, _ := resp["account"].(map[string]any)
	require.NotNil(t, account)
	require.EqualValues(t, 8.94, account["credit_remaining"])
	require.Equal(t, "CNY", account["currency_display"])
	// Manual key path does NOT expose granted_scopes / oauth block.
	_, hasOAuth := resp["oauth"]
	require.False(t, hasOAuth, "manual key path must not include oauth block")
	_, hasGranted := resp["granted_scopes"]
	require.False(t, hasGranted, "manual key path must not include granted_scopes")
}

func TestMe_OAuthToken_NoMatchingScope_Returns403(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newAccountHandlerHarness()
	apiKey := fakeAPIKey(t, "sk_oauth_")

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	c.Set(string(servermiddleware.ContextKeyAPIKey), apiKey)
	// Token has only models:read — none of profile/account/balance.
	c.Set(string(servermiddleware.ContextKeyOAuthMetadata), &service.OAuthAccessToken{
		APIKeyID:  apiKey.ID,
		ClientID:  "test-client",
		AppType:   "web",
		GrantID:   "grant-1",
		UserID:    apiKey.User.ID,
		Scopes:    []string{service.ScopeModelsRead},
		ExpiresAt: time.Now().Add(time.Hour),
	})

	h.Me(c)

	require.Equal(t, http.StatusForbidden, rec.Code,
		"§12.11: token missing all of profile/account/balance must get 403")
	require.Contains(t, rec.Header().Get("WWW-Authenticate"), "insufficient_scope")
	require.Contains(t, rec.Header().Get("WWW-Authenticate"), "profile:read")
}

func TestMe_OAuthToken_BalanceOnlyScope_CropsFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newAccountHandlerHarness()
	apiKey := fakeAPIKey(t, "sk_oauth_")

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	c.Set(string(servermiddleware.ContextKeyAPIKey), apiKey)
	c.Set(string(servermiddleware.ContextKeyOAuthMetadata), &service.OAuthAccessToken{
		APIKeyID:  apiKey.ID,
		ClientID:  "test-client",
		AppType:   "image",
		GrantID:   "grant-2",
		UserID:    apiKey.User.ID,
		Scopes:    []string{service.ScopeAccountBalanceRead},
		ExpiresAt: time.Now().Add(time.Hour),
	})

	h.Me(c)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "oauth", resp["auth_type"])

	// account.credit_remaining should be present (account:balance:read grants it).
	account, _ := resp["account"].(map[string]any)
	require.NotNil(t, account, "account block must be included with balance:read")
	require.EqualValues(t, 8.94, account["credit_remaining"])

	// user block should NOT be present (no profile:read).
	_, hasUser := resp["user"]
	require.False(t, hasUser, "no profile:read → user block must be omitted")

	// current_group / allowed_groups should NOT be present (no account:read).
	_, hasCurrent := resp["current_group"]
	require.False(t, hasCurrent, "no account:read → current_group must be omitted")
	_, hasAllowed := resp["allowed_groups"]
	require.False(t, hasAllowed, "no account:read → allowed_groups must be omitted")

	// oauth block always present.
	oauthBlock, _ := resp["oauth"].(map[string]any)
	require.NotNil(t, oauthBlock)
	require.Equal(t, "test-client", oauthBlock["client_id"])
}

func TestMe_OAuthToken_FullScopes_RendersAllFamilies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newAccountHandlerHarness()
	apiKey := fakeAPIKey(t, "sk_oauth_")

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	c.Set(string(servermiddleware.ContextKeyAPIKey), apiKey)
	c.Set(string(servermiddleware.ContextKeyOAuthMetadata), &service.OAuthAccessToken{
		APIKeyID: apiKey.ID,
		ClientID: "test-client",
		AppType:  "web",
		GrantID:  "grant-3",
		UserID:   apiKey.User.ID,
		Scopes: []string{
			service.ScopeProfileRead,
			service.ScopeEmailRead,
			service.ScopeAccountRead,
			service.ScopeModelsRead,
			service.ScopeImagesCreate,
		},
		ExpiresAt: time.Now().Add(time.Hour),
	})

	h.Me(c)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

	// profile:read → user block.
	user, _ := resp["user"].(map[string]any)
	require.NotNil(t, user)
	require.Equal(t, "alice", user["username"])

	// email:read → user.email present.
	require.Equal(t, "alice@example.com", user["email"])

	// account:read → account + current_group.
	require.NotNil(t, resp["account"])
	current, _ := resp["current_group"].(map[string]any)
	require.NotNil(t, current)
	require.Equal(t, "GPT-Pro", current["name"])

	// effective_capabilities must reflect group narrowing: token has
	// images:create but group has allow_image_generation=false → false.
	caps, _ := resp["effective_capabilities"].(map[string]any)
	require.NotNil(t, caps)
	modelsRead, ok := caps["models_read"].(bool)
	require.True(t, ok)
	require.True(t, modelsRead)
	imagesCreate, ok := caps["images_create"].(bool)
	require.True(t, ok)
	require.False(t, imagesCreate,
		"§12.11: images:create scope + group.allow_image_generation=false → images_create=false in effective_capabilities")
	accountRead, ok := caps["account_read"].(bool)
	require.True(t, ok)
	require.True(t, accountRead)
	emailRead, ok := caps["email_read"].(bool)
	require.True(t, ok)
	require.True(t, emailRead)
}

func TestMe_OAuthToken_ProfileOnly_OmitsEmail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := newAccountHandlerHarness()
	apiKey := fakeAPIKey(t, "sk_oauth_")

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	c.Set(string(servermiddleware.ContextKeyAPIKey), apiKey)
	c.Set(string(servermiddleware.ContextKeyOAuthMetadata), &service.OAuthAccessToken{
		APIKeyID:  apiKey.ID,
		ClientID:  "test-client",
		AppType:   "web",
		GrantID:   "grant-4",
		UserID:    apiKey.User.ID,
		Scopes:    []string{service.ScopeProfileRead},
		ExpiresAt: time.Now().Add(time.Hour),
	})

	h.Me(c)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

	user, _ := resp["user"].(map[string]any)
	require.NotNil(t, user)
	require.Equal(t, "alice", user["username"])
	_, hasEmail := user["email"]
	require.False(t, hasEmail,
		"§12.11: email:read scope is required to expose user.email; profile:read alone must omit it")
}
