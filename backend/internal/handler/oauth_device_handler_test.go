package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// ── stubs ───────────────────────────────────────────────────────────────────

type stubHandlerDeviceRepo struct {
	mu      sync.Mutex
	rows    map[string]*service.OAuthDeviceCode
	byUC    map[string]string
	nextID  int64
	touched int
}

func newStubHandlerDeviceRepo() *stubHandlerDeviceRepo {
	return &stubHandlerDeviceRepo{
		rows: map[string]*service.OAuthDeviceCode{},
		byUC: map[string]string{},
	}
}

func (s *stubHandlerDeviceRepo) CreateDeviceCode(_ context.Context, code *service.OAuthDeviceCode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	cp := *code
	cp.ID = s.nextID
	cp.CreatedAt = time.Now()
	s.rows[code.DeviceCodeHash] = &cp
	s.byUC[code.UserCodeHash] = code.DeviceCodeHash
	return nil
}

func (s *stubHandlerDeviceRepo) GetDeviceCodeByUserCodeHashForApproval(_ context.Context, h string, now time.Time) (*service.OAuthDeviceCode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dch, ok := s.byUC[h]
	if !ok {
		return nil, service.ErrOAuthDeviceCodeNotFound
	}
	row, ok := s.rows[dch]
	if !ok || row.Status != "pending" || !row.ExpiresAt.After(now) {
		return nil, service.ErrOAuthDeviceCodeNotFound
	}
	cp := *row
	return &cp, nil
}

func (s *stubHandlerDeviceRepo) PollDeviceCodeForUpdate(_ context.Context, h string, _ time.Time) (*service.OAuthDeviceCode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[h]
	if !ok {
		return nil, service.ErrOAuthDeviceCodeNotFound
	}
	cp := *row
	return &cp, nil
}

func (s *stubHandlerDeviceRepo) ApproveDeviceCode(_ context.Context, h string, userID, groupID int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dch, ok := s.byUC[h]
	if !ok {
		return service.ErrOAuthDeviceCodeNotFound
	}
	row, ok := s.rows[dch]
	if !ok || row.Status != "pending" {
		return service.ErrOAuthDeviceCodeNotFound
	}
	row.Status = "approved"
	row.ApprovedByUserID = &userID
	row.ApprovedAt = &now
	row.GroupID = &groupID
	return nil
}

func (s *stubHandlerDeviceRepo) DenyDeviceCode(_ context.Context, h string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dch, ok := s.byUC[h]
	if !ok {
		return service.ErrOAuthDeviceCodeNotFound
	}
	row, ok := s.rows[dch]
	if !ok || row.Status != "pending" {
		return service.ErrOAuthDeviceCodeNotFound
	}
	row.Status = "denied"
	row.DeniedAt = &now
	return nil
}

func (s *stubHandlerDeviceRepo) MarkDeviceCodeConsumed(_ context.Context, h string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[h]
	if !ok || row.Status != "approved" {
		return service.ErrOAuthDeviceCodeNotFound
	}
	row.Status = "consumed"
	row.ConsumedAt = &now
	return nil
}

// ConsumeApprovedDeviceCode is the FIX H6 atomic gate stub: only the caller
// that observes status='approved' wins; concurrent callers see not-found.
func (s *stubHandlerDeviceRepo) ConsumeApprovedDeviceCode(_ context.Context, h string, now time.Time) (*service.OAuthDeviceCode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[h]
	if !ok || row.Status != "approved" {
		return nil, service.ErrOAuthDeviceCodeNotFound
	}
	row.Status = "consumed"
	row.ConsumedAt = &now
	cp := *row
	return &cp, nil
}

func (s *stubHandlerDeviceRepo) IncrementDeviceCodeFailedAttempts(_ context.Context, h string, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dch, ok := s.byUC[h]
	if !ok {
		return 0, service.ErrOAuthDeviceCodeNotFound
	}
	row, ok := s.rows[dch]
	if !ok || !row.ExpiresAt.After(now) {
		return 0, service.ErrOAuthDeviceCodeNotFound
	}
	row.FailedUserCodeAttempts++
	return row.FailedUserCodeAttempts, nil
}

func (s *stubHandlerDeviceRepo) TouchDevicePoll(_ context.Context, h string, lastPollAt time.Time, pollCount, intervalSeconds, slowDownCount int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[h]
	if !ok {
		return service.ErrOAuthDeviceCodeNotFound
	}
	row.LastPollAt = &lastPollAt
	row.PollCount = pollCount
	row.IntervalSeconds = intervalSeconds
	row.SlowDownCount = slowDownCount
	s.touched++
	return nil
}

// ── helpers ─────────────────────────────────────────────────────────────────

func newDeviceHandlerHarness(t *testing.T) (*OAuthDeviceHandler, *OAuthProviderHandler, *service.OAuthProviderService, *stubHandlerDeviceRepo) {
	t.Helper()
	groupID := int64(7)
	clientRepo := &oauthHandlerClientRepoStub{clients: map[string]*service.OAuthClient{
		"sakrylle-cli": {
			ClientID:                         "sakrylle-cli",
			Name:                             "Sakrylle CLI",
			AllowedScopes:                    []string{service.ScopeProfileRead, service.ScopeMessagesCreate, service.ScopeOfflineAccess},
			DefaultScopes:                    []string{service.ScopeProfileRead},
			PKCERequired:                     true,
			DefaultGroupID:                   &groupID,
			AccessTokenTTLSeconds:            3600,
			RefreshTokenTTLSeconds:           86400,
			DeviceFlowEnabled:                true,
			AllowRefreshWithoutOfflineAccess: false,
		},
	}}
	apiKeyAdapter := &oauthHandlerAPIKeyOAuthAdapter{oauthHandlerAPIKeyRepoStub: newOAuthHandlerAPIKeyRepoStub()}
	settingRepo := &oauthHandlerSettingRepoStub{values: map[string]string{
		"oauth_provider_enabled":    "true",
		"oauth_device_flow_enabled": "true",
		"oauth_default_group_id":    "7",
		"oauth_issuer":              "https://sub.sakrylle.example",
	}}
	deviceRepo := newStubHandlerDeviceRepo()
	svc := service.NewOAuthProviderService(
		clientRepo,
		newOAuthHandlerCodeRepoStub(),
		newOAuthHandlerRefreshRepoStub(),
		nil, // accessRepo (not used in these handler tests)
		deviceRepo,
		nil, // authzTxRepo
		apiKeyAdapter,
		nil, // groupRepo
		nil, // groupAccess
		settingRepo,
		nil,
	)
	provider := NewOAuthProviderHandler(svc, nil)
	device := NewOAuthDeviceHandler(svc)
	provider.SetDeviceHandler(device)
	return device, provider, svc, deviceRepo
}

func formCtx(method, target string, form map[string]string) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := strings.NewReader(encodeForm(form))
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.Request = req
	return c, rec
}

func encodeForm(m map[string]string) string {
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, k+"="+strings.ReplaceAll(v, " ", "+"))
	}
	return strings.Join(parts, "&")
}

func jsonCtx(method, target string, body any) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(method, target, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	return c, rec
}

func setSubject(c *gin.Context, userID int64) {
	c.Set("user", servermiddleware.AuthSubject{UserID: userID})
}

// ── tests ───────────────────────────────────────────────────────────────────

func TestDeviceAuthorize_ReturnsAllRequiredFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dev, _, _, _ := newDeviceHandlerHarness(t)

	c, rec := formCtx(http.MethodPost, "/oauth/device/code", map[string]string{
		"client_id": "sakrylle-cli",
		"scope":     "profile:read messages:create",
	})
	dev.DeviceAuthorize(c)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	require.Equal(t, "no-cache", rec.Header().Get("Pragma"))

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	for _, key := range []string{"device_code", "user_code", "verification_uri", "verification_uri_complete", "expires_in", "interval"} {
		require.NotEmpty(t, resp[key], "missing field %q", key)
	}
	uc, _ := resp["user_code"].(string)
	require.True(t, strings.HasPrefix(uc, "SKRY-"), "user_code must start with SKRY-, got %q", uc)
}

func TestDeviceAuthorize_BadClientReturns401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dev, _, _, _ := newDeviceHandlerHarness(t)

	c, rec := formCtx(http.MethodPost, "/oauth/device/code", map[string]string{
		"client_id": "ghost-client",
	})
	dev.DeviceAuthorize(c)

	// LookupClient returns ErrOAuthClientNotFound → INVALID_CLIENT → 401.
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "invalid_client", resp["error"])
}

func TestDeviceAuthorize_RejectsJSONContentType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dev, _, _, _ := newDeviceHandlerHarness(t)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/oauth/device/code", strings.NewReader(`{"client_id":"sakrylle-cli"}`))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req

	dev.DeviceAuthorize(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "invalid_request", resp["error"])
}

func TestTokenEndpoint_DeviceCodeGrant_Pending(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dev, provider, svc, _ := newDeviceHandlerHarness(t)
	_ = dev

	created, err := svc.CreateDeviceCode(context.Background(), &service.DeviceCodeRequest{
		ClientID: "sakrylle-cli",
		Scopes:   []string{service.ScopeMessagesCreate},
	})
	require.NoError(t, err)

	c, rec := formCtx(http.MethodPost, "/oauth/token", map[string]string{
		"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
		"device_code": created.DeviceCode,
		"client_id":   "sakrylle-cli",
	})
	provider.Token(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "authorization_pending", resp["error"])
}

func TestTokenEndpoint_DeviceCodeGrant_Approved(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dev, provider, svc, deviceRepo := newDeviceHandlerHarness(t)
	_ = dev

	created, err := svc.CreateDeviceCode(context.Background(), &service.DeviceCodeRequest{
		ClientID: "sakrylle-cli",
		Scopes:   []string{service.ScopeMessagesCreate},
	})
	require.NoError(t, err)
	require.NoError(t, svc.ApproveDeviceCode(context.Background(), 99, created.UserCode, nil))

	// Reset last-poll so the slow_down branch doesn't fire on first poll.
	deviceRepo.mu.Lock()
	for _, row := range deviceRepo.rows {
		row.LastPollAt = nil
	}
	deviceRepo.mu.Unlock()

	c, rec := formCtx(http.MethodPost, "/oauth/token", map[string]string{
		"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
		"device_code": created.DeviceCode,
		"client_id":   "sakrylle-cli",
	})
	provider.Token(c)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotEmpty(t, resp["access_token"])
	require.Equal(t, "Bearer", resp["token_type"])
}

func TestDeviceApprove_RequiresAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dev, _, _, _ := newDeviceHandlerHarness(t)

	c, rec := jsonCtx(http.MethodPost, "/api/v1/oauth/device/approve", map[string]string{
		"user_code":  "SKRY-BCDF-G2346",
		"csrf_token": "anything",
	})
	dev.DeviceApprove(c)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestDeviceApprove_BadUserCodeOpaque400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dev, _, _, _ := newDeviceHandlerHarness(t)

	c, rec := jsonCtx(http.MethodPost, "/api/v1/oauth/device/approve", map[string]string{
		"user_code":  "SKRY-BCDF-G2346",
		"csrf_token": "any",
	})
	setSubject(c, 99)
	// Set csrf cookie so CSRF check passes.
	hashed := hashCSRFToken("any")
	c.Request.AddCookie(&http.Cookie{Name: "sakrylle_oauth_device_csrf", Value: hashed})

	dev.DeviceApprove(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "invalid_request", resp["error"])
	desc, _ := resp["error_description"].(string)
	require.Equal(t, "user_code did not match an active device authorization", desc)
}

func TestDeviceApprove_HappyPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dev, _, svc, _ := newDeviceHandlerHarness(t)

	created, err := svc.CreateDeviceCode(context.Background(), &service.DeviceCodeRequest{
		ClientID: "sakrylle-cli",
	})
	require.NoError(t, err)

	c, rec := jsonCtx(http.MethodPost, "/api/v1/oauth/device/approve", map[string]string{
		"user_code":  created.UserCode,
		"csrf_token": "csrf-plaintext",
	})
	setSubject(c, 99)
	c.Request.AddCookie(&http.Cookie{
		Name:  "sakrylle_oauth_device_csrf",
		Value: hashCSRFToken("csrf-plaintext"),
	})

	dev.DeviceApprove(c)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, true, resp["approved"])
}

func TestDeviceApprove_CSRFMismatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dev, _, svc, _ := newDeviceHandlerHarness(t)

	created, err := svc.CreateDeviceCode(context.Background(), &service.DeviceCodeRequest{
		ClientID: "sakrylle-cli",
	})
	require.NoError(t, err)

	c, rec := jsonCtx(http.MethodPost, "/api/v1/oauth/device/approve", map[string]string{
		"user_code":  created.UserCode,
		"csrf_token": "wrong-plain",
	})
	setSubject(c, 99)
	c.Request.AddCookie(&http.Cookie{
		Name:  "sakrylle_oauth_device_csrf",
		Value: hashCSRFToken("right-plain"),
	})

	dev.DeviceApprove(c)

	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestDeviceVerificationPage_RendersHTML(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dev, _, _, _ := newDeviceHandlerHarness(t)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodGet, "/oauth/device?user_code=SKRY-BCDF-G2346", nil)
	c.Request = req

	dev.DeviceVerificationPage(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	require.Equal(t, "no-referrer", rec.Header().Get("Referrer-Policy"))
	require.Contains(t, rec.Header().Get("Cache-Control"), "no-store")
	require.Contains(t, rec.Header().Get("Content-Security-Policy"), "frame-ancestors")
	body := rec.Body.String()
	require.Contains(t, body, "SKRY-BCDF-G2346", "page must prefill user_code from query")
	require.Contains(t, body, "/api/v1/oauth/device/approve", "page must POST to approve endpoint")
	// The unauthenticated bounce must target the SPA login route (/login) with
	// the redirect query the SPA consumes — NOT /auth/login?next= (no such SPA
	// route → NotFoundView 404).
	require.Contains(t, body, `"/login?redirect="`,
		"device page must bounce to the SPA /login route with ?redirect=")
	require.NotContains(t, body, "/auth/login?next=",
		"device page must not bounce to the nonexistent /auth/login route")
}

func TestDeviceDeny_HappyPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dev, _, svc, deviceRepo := newDeviceHandlerHarness(t)

	created, err := svc.CreateDeviceCode(context.Background(), &service.DeviceCodeRequest{
		ClientID: "sakrylle-cli",
	})
	require.NoError(t, err)

	c, rec := jsonCtx(http.MethodPost, "/api/v1/oauth/device/deny", map[string]string{
		"user_code":  created.UserCode,
		"csrf_token": "csrf-plain",
	})
	setSubject(c, 99)
	c.Request.AddCookie(&http.Cookie{
		Name:  "sakrylle_oauth_device_csrf",
		Value: hashCSRFToken("csrf-plain"),
	})

	dev.DeviceDeny(c)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, true, resp["denied"])

	// Verify status flipped.
	deviceRepo.mu.Lock()
	defer deviceRepo.mu.Unlock()
	for _, row := range deviceRepo.rows {
		require.Equal(t, "denied", row.Status)
	}
}
