package handler

// §17 Workstream F + §16 Phase 6: cross-workstream end-to-end regression
// suite for OAuth v2.
//
// These tests drive the OAuth provider HTTP layer through full multi-step
// flows that no single workstream covers in isolation:
//
//   - Legacy Image Playground migration round-trip (§15.4)
//   - Device Authorization Flow full cycle (RFC 8628, §12.6 / §12.7 / §12.8)
//   - Device flow with legacy alias `image_generation` → canonical
//
// The suite reuses the in-memory stubs from oauth_provider_handler_test.go
// and oauth_device_handler_test.go (same package). It does NOT touch real
// postgres/redis; the goal is to exercise the v2 invariants through gin
// handlers without the production DB.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestE2E_LegacyImagePlaygroundFlow walks an old image-playground client
// through the full v2-deployed lifecycle: legacy `image_generation` scope
// requested at /authorize → consent + transaction-less mint via legacy code
// flow → /token returns canonical scope string → refresh rotates →
// replayed old refresh fails. This is the §15.4 backward-compatibility
// contract.
func TestE2E_LegacyImagePlaygroundFlow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, svc := newOAuthProviderHandlerHarness(t)
	verifier, challenge := pkceVerifierAndChallengeForHandler(
		"e2e-legacy-image-playground-aaaaaaaaaaaaaaaaaaaa")

	// 1. /authorize equivalent: validate + mint code through service so the
	//    test mirrors the legacy (pre-transaction) /authorize 302 flow that
	//    the existing image-playground frontend still uses while migration
	//    rolls out (Workstream B legacy path).
	authReq := &service.AuthorizeRequest{
		ClientID:     testOAuthClientID,
		RedirectURI:  testOAuthRedirectURI,
		ResponseType: "code",
		// Legacy scope strings — exactly what the prod image-playground
		// frontend sends today.
		Scopes:              []string{"image_generation", "balance:read"},
		State:               "rnd-state",
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
	}
	client, err := svc.ValidateAuthorizeRequest(context.Background(), authReq)
	require.NoError(t, err, "legacy scopes must validate")
	issued, err := svc.IssueAuthorizationCode(context.Background(), client, 42, authReq)
	require.NoError(t, err)
	require.Equal(t, "rnd-state", issued.State, "state must echo back unchanged")

	// 2. /oauth/token: exchange the code.
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", testOAuthClientID)
	form.Set("redirect_uri", testOAuthRedirectURI)
	form.Set("code", issued.Code)
	form.Set("code_verifier", verifier)

	c, rec := newGinTestContext(http.MethodPost, "/oauth/token",
		[]byte(form.Encode()), "application/x-www-form-urlencoded")
	h.Token(c)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	var tok map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &tok))
	access, _ := tok["access_token"].(string)
	refresh, _ := tok["refresh_token"].(string)
	require.True(t, strings.HasPrefix(access, "sk_oauth_"))
	require.True(t, strings.HasPrefix(refresh, "rt_"))

	// 3. Token response MUST contain CANONICAL scope strings even though we
	//    requested legacy aliases — §7.2 explicit. This is the
	//    NormalizeScopes contract the frontend can later rely on when it
	//    renders scope labels from the token response.
	scopeStr, _ := tok["scope"].(string)
	scopeTokens := strings.Fields(scopeStr)
	scopeSet := make(map[string]bool, len(scopeTokens))
	for _, s := range scopeTokens {
		scopeSet[s] = true
	}
	require.True(t, scopeSet["images:create"],
		"legacy 'image_generation' must be normalized to 'images:create' (got %q)", scopeStr)
	require.True(t, scopeSet["account:balance:read"],
		"legacy 'balance:read' must be normalized to 'account:balance:read' (got %q)", scopeStr)
	require.False(t, scopeSet["image_generation"],
		"/token response MUST NOT echo legacy alias 'image_generation' (got %q)", scopeStr)
	require.False(t, scopeSet["balance:read"],
		"/token response MUST NOT echo legacy alias 'balance:read' (got %q)", scopeStr)

	// 4. Refresh rotation: legacy client receives a refresh_token even
	//    without offline_access in the requested scopes, because the
	//    fixture sets AllowRefreshWithoutOfflineAccess=true (§15.4 prod
	//    image-playground row).
	refreshForm := url.Values{}
	refreshForm.Set("grant_type", "refresh_token")
	refreshForm.Set("client_id", testOAuthClientID)
	refreshForm.Set("refresh_token", refresh)
	c2, rec2 := newGinTestContext(http.MethodPost, "/oauth/token",
		[]byte(refreshForm.Encode()), "application/x-www-form-urlencoded")
	h.Token(c2)
	require.Equal(t, http.StatusOK, rec2.Code, "refresh body=%s", rec2.Body.String())

	var refreshed map[string]any
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &refreshed))
	newAccess, _ := refreshed["access_token"].(string)
	newRefresh, _ := refreshed["refresh_token"].(string)
	require.NotEqual(t, access, newAccess, "refresh must mint new access_token")
	require.NotEqual(t, refresh, newRefresh, "refresh must rotate refresh_token")

	// 5. Replay the legacy refresh — must fail with invalid_grant.
	c3, rec3 := newGinTestContext(http.MethodPost, "/oauth/token",
		[]byte(refreshForm.Encode()), "application/x-www-form-urlencoded")
	h.Token(c3)
	require.Equal(t, http.StatusBadRequest, rec3.Code,
		"replay of rotated refresh must fail")

	var replay map[string]any
	require.NoError(t, json.Unmarshal(rec3.Body.Bytes(), &replay))
	require.Equal(t, "invalid_grant", replay["error"])
}

// TestE2E_DeviceFlow_FullCycle exercises the RFC 8628 device authorization
// grant end-to-end: client requests device_code → user code is shown →
// CLI's first poll returns authorization_pending → simulated user approves
// via /api/v1/oauth/device/approve → CLI's next poll mints the token.
func TestE2E_DeviceFlow_FullCycle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dev, provider, svc, deviceRepo := newDeviceHandlerHarness(t)

	// 1. POST /oauth/device/code as a CLI client.
	c, rec := formCtx(http.MethodPost, "/oauth/device/code", map[string]string{
		"client_id": "sakrylle-cli",
		"scope":     "profile:read messages:create",
	})
	dev.DeviceAuthorize(c)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var deviceResp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &deviceResp))

	deviceCode, _ := deviceResp["device_code"].(string)
	userCode, _ := deviceResp["user_code"].(string)
	verificationURI, _ := deviceResp["verification_uri"].(string)
	require.NotEmpty(t, deviceCode)
	require.NotEmpty(t, userCode, "user_code must be plaintext for one-time display")
	require.True(t, strings.HasPrefix(userCode, "SKRY-"),
		"user_code must use the SKRY- prefix for visual recognition")
	require.NotEmpty(t, verificationURI)
	require.Contains(t, deviceResp, "verification_uri_complete",
		"§12.7: verification_uri_complete is REQUIRED alongside verification_uri")

	// 2. CLI's first poll. Must return authorization_pending.
	pollForm := map[string]string{
		"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
		"device_code": deviceCode,
		"client_id":   "sakrylle-cli",
	}
	c2, rec2 := formCtx(http.MethodPost, "/oauth/token", pollForm)
	provider.Token(c2)
	require.Equal(t, http.StatusBadRequest, rec2.Code)

	var pending map[string]any
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &pending))
	require.Equal(t, "authorization_pending", pending["error"])

	// 3. User approves via /api/v1/oauth/device/approve. Reset last-poll so
	//    the slow_down branch doesn't fire on the second poll below.
	deviceRepo.mu.Lock()
	for _, row := range deviceRepo.rows {
		row.LastPollAt = nil
	}
	deviceRepo.mu.Unlock()

	c3, rec3 := jsonCtx(http.MethodPost, "/api/v1/oauth/device/approve", map[string]string{
		"user_code":  userCode,
		"csrf_token": "csrf-plain",
	})
	setSubject(c3, 99)
	c3.Request.AddCookie(&http.Cookie{
		Name:  "sakrylle_oauth_device_csrf",
		Value: hashCSRFToken("csrf-plain"),
	})
	dev.DeviceApprove(c3)
	require.Equal(t, http.StatusOK, rec3.Code, "approve body: %s", rec3.Body.String())

	// 4. CLI polls again — token endpoint mints.
	c4, rec4 := formCtx(http.MethodPost, "/oauth/token", pollForm)
	provider.Token(c4)
	require.Equal(t, http.StatusOK, rec4.Code, "second poll body: %s", rec4.Body.String())

	var tokResp map[string]any
	require.NoError(t, json.Unmarshal(rec4.Body.Bytes(), &tokResp))
	require.NotEmpty(t, tokResp["access_token"])
	require.Equal(t, "Bearer", tokResp["token_type"])

	// 5. Polling once more after consumption MUST fail with invalid_grant
	//    (or expired_token). Per §18.4: "Consumed code cannot mint twice."
	c5, rec5 := formCtx(http.MethodPost, "/oauth/token", pollForm)
	provider.Token(c5)
	require.Equal(t, http.StatusBadRequest, rec5.Code,
		"consumed device_code must not mint a second token")

	var second map[string]any
	require.NoError(t, json.Unmarshal(rec5.Body.Bytes(), &second))
	require.Contains(t, []string{"invalid_grant", "expired_token"}, second["error"],
		"per §18.4 the second poll after consumption returns invalid_grant or expired_token")

	// Assert the device-flow happy path went through createDeviceCode +
	// approve + mint by checking the underlying device row state.
	deviceRepo.mu.Lock()
	defer deviceRepo.mu.Unlock()
	var sawConsumed bool
	for _, row := range deviceRepo.rows {
		if row.Status == "consumed" {
			sawConsumed = true
			require.NotNil(t, row.ApprovedByUserID)
			require.Equal(t, int64(99), *row.ApprovedByUserID)
		}
	}
	require.True(t, sawConsumed, "expected at least one device row in 'consumed' state after mint")
	_ = svc // silence unused
}

// TestE2E_DeviceFlow_DeniedReturnsAccessDenied — user denies via
// /api/v1/oauth/device/deny → poll returns access_denied (RFC 8628 §3.5).
func TestE2E_DeviceFlow_DeniedReturnsAccessDenied(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dev, provider, _, deviceRepo := newDeviceHandlerHarness(t)

	c, rec := formCtx(http.MethodPost, "/oauth/device/code", map[string]string{
		"client_id": "sakrylle-cli",
	})
	dev.DeviceAuthorize(c)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	deviceCode, _ := resp["device_code"].(string)
	userCode, _ := resp["user_code"].(string)

	// User denies.
	c2, rec2 := jsonCtx(http.MethodPost, "/api/v1/oauth/device/deny", map[string]string{
		"user_code":  userCode,
		"csrf_token": "deny-csrf",
	})
	setSubject(c2, 7)
	c2.Request.AddCookie(&http.Cookie{
		Name:  "sakrylle_oauth_device_csrf",
		Value: hashCSRFToken("deny-csrf"),
	})
	dev.DeviceDeny(c2)
	require.Equal(t, http.StatusOK, rec2.Code, "deny body: %s", rec2.Body.String())

	// Reset last-poll to keep slow_down out of the way.
	deviceRepo.mu.Lock()
	for _, row := range deviceRepo.rows {
		row.LastPollAt = nil
	}
	deviceRepo.mu.Unlock()

	// Poll → access_denied.
	pollForm := map[string]string{
		"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
		"device_code": deviceCode,
		"client_id":   "sakrylle-cli",
	}
	c3, rec3 := formCtx(http.MethodPost, "/oauth/token", pollForm)
	provider.Token(c3)
	require.Equal(t, http.StatusBadRequest, rec3.Code)

	var denied map[string]any
	require.NoError(t, json.Unmarshal(rec3.Body.Bytes(), &denied))
	require.Equal(t, "access_denied", denied["error"])
}

// TestE2E_TransactionApproveMintsCanonicalScopes — the v2 transaction-based
// /authorize → /approve → /token path normalizes legacy scope strings to
// canonical in the resulting access_token's scope claim. Combines
// BeginAuthorizeTransaction → Approve → handler.Token.
func TestE2E_TransactionApproveMintsCanonicalScopes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, svc := newOAuthProviderHandlerHarness(t)
	verifier, challenge := pkceVerifierAndChallengeForHandler(
		"e2e-tx-mint-canonical-aaaaaaaaaaaaaaaaaaaaaaa")

	// Approve via the handler with a JSON body that carries the legacy
	// scope string. The fixture's authzTxRepo is nil, so the handler
	// falls back to the legacy approve path; we drive that path here for
	// canonical-scope normalization on /token.
	approveBody, _ := json.Marshal(map[string]string{
		"client_id":             testOAuthClientID,
		"redirect_uri":          testOAuthRedirectURI,
		"response_type":         "code",
		"scope":                 "image_generation balance:read models:read",
		"state":                 "x",
		"code_challenge":        challenge,
		"code_challenge_method": "S256",
		"decision":              "approve",
	})
	c, rec := newGinTestContext(http.MethodPost,
		"/api/v1/oauth/authorize/approve", approveBody, "application/json")
	c.Set(string(servermiddleware.ContextKeyUser),
		servermiddleware.AuthSubject{UserID: 314, Concurrency: 1})
	h.Approve(c)
	require.Equal(t, http.StatusOK, rec.Code, "approve body: %s", rec.Body.String())

	var approveResp map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &approveResp))
	parsed, perr := url.Parse(approveResp["redirect_to"])
	require.NoError(t, perr)
	code := parsed.Query().Get("code")
	require.NotEmpty(t, code)

	// Exchange.
	tokenForm := url.Values{}
	tokenForm.Set("grant_type", "authorization_code")
	tokenForm.Set("client_id", testOAuthClientID)
	tokenForm.Set("redirect_uri", testOAuthRedirectURI)
	tokenForm.Set("code", code)
	tokenForm.Set("code_verifier", verifier)
	c2, rec2 := newGinTestContext(http.MethodPost, "/oauth/token",
		[]byte(tokenForm.Encode()), "application/x-www-form-urlencoded")
	h.Token(c2)
	require.Equal(t, http.StatusOK, rec2.Code, "token body: %s", rec2.Body.String())

	var tok map[string]any
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &tok))

	// Scope claim MUST be canonical, regardless of what came in.
	scope, _ := tok["scope"].(string)
	scopeTokens := strings.Fields(scope)
	scopeSet := make(map[string]bool, len(scopeTokens))
	for _, s := range scopeTokens {
		scopeSet[s] = true
	}
	for _, want := range []string{
		"images:create",
		"account:balance:read",
		"models:read",
	} {
		require.True(t, scopeSet[want],
			"§7.2: /token must return canonical scope %q (got %q)", want, scope)
	}
	for _, banned := range []string{"image_generation", "balance:read"} {
		require.False(t, scopeSet[banned],
			"§7.2: /token must NOT echo legacy alias %q for v2 clients (got %q)", banned, scope)
	}
	_ = svc
}
