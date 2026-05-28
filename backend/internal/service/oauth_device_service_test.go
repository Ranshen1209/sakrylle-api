package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── stubs ───────────────────────────────────────────────────────────────────

type stubDeviceCodeRepo struct {
	mu             sync.Mutex
	rows           map[string]*OAuthDeviceCode // keyed by device_code_hash
	byUserCodeHash map[string]string           // user_code_hash → device_code_hash
	nextID         int64
}

func newStubDeviceCodeRepo() *stubDeviceCodeRepo {
	return &stubDeviceCodeRepo{
		rows:           map[string]*OAuthDeviceCode{},
		byUserCodeHash: map[string]string{},
	}
}

func (s *stubDeviceCodeRepo) CreateDeviceCode(_ context.Context, code *OAuthDeviceCode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	cp := *code
	cp.ID = s.nextID
	cp.CreatedAt = time.Now()
	s.rows[code.DeviceCodeHash] = &cp
	s.byUserCodeHash[code.UserCodeHash] = code.DeviceCodeHash
	return nil
}

func (s *stubDeviceCodeRepo) GetDeviceCodeByUserCodeHashForApproval(_ context.Context, userCodeHash string, now time.Time) (*OAuthDeviceCode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dch, ok := s.byUserCodeHash[userCodeHash]
	if !ok {
		return nil, ErrOAuthDeviceCodeNotFound
	}
	row, ok := s.rows[dch]
	if !ok {
		return nil, ErrOAuthDeviceCodeNotFound
	}
	if row.Status != "pending" {
		return nil, ErrOAuthDeviceCodeNotFound
	}
	if !row.ExpiresAt.After(now) {
		return nil, ErrOAuthDeviceCodeNotFound
	}
	cp := *row
	return &cp, nil
}

func (s *stubDeviceCodeRepo) PollDeviceCodeForUpdate(_ context.Context, deviceCodeHash string, _ time.Time) (*OAuthDeviceCode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[deviceCodeHash]
	if !ok {
		return nil, ErrOAuthDeviceCodeNotFound
	}
	cp := *row
	return &cp, nil
}

func (s *stubDeviceCodeRepo) ApproveDeviceCode(_ context.Context, userCodeHash string, userID int64, groupID int64, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dch, ok := s.byUserCodeHash[userCodeHash]
	if !ok {
		return ErrOAuthDeviceCodeNotFound
	}
	row, ok := s.rows[dch]
	if !ok {
		return ErrOAuthDeviceCodeNotFound
	}
	if row.Status != "pending" {
		return ErrOAuthDeviceCodeNotFound
	}
	row.Status = "approved"
	row.ApprovedByUserID = &userID
	row.ApprovedAt = &now
	row.GroupID = &groupID
	return nil
}

func (s *stubDeviceCodeRepo) DenyDeviceCode(_ context.Context, userCodeHash string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dch, ok := s.byUserCodeHash[userCodeHash]
	if !ok {
		return ErrOAuthDeviceCodeNotFound
	}
	row, ok := s.rows[dch]
	if !ok {
		return ErrOAuthDeviceCodeNotFound
	}
	if row.Status != "pending" {
		return ErrOAuthDeviceCodeNotFound
	}
	row.Status = "denied"
	row.DeniedAt = &now
	return nil
}

func (s *stubDeviceCodeRepo) MarkDeviceCodeConsumed(_ context.Context, deviceCodeHash string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[deviceCodeHash]
	if !ok {
		return ErrOAuthDeviceCodeNotFound
	}
	if row.Status != "approved" {
		return ErrOAuthDeviceCodeNotFound
	}
	row.Status = "consumed"
	row.ConsumedAt = &now
	return nil
}

// ConsumeApprovedDeviceCode is the FIX H6 atomic gate: only the caller that
// observes status='approved' wins; any concurrent caller sees not-found.
func (s *stubDeviceCodeRepo) ConsumeApprovedDeviceCode(_ context.Context, deviceCodeHash string, now time.Time) (*OAuthDeviceCode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[deviceCodeHash]
	if !ok {
		return nil, ErrOAuthDeviceCodeNotFound
	}
	if row.Status != "approved" {
		return nil, ErrOAuthDeviceCodeNotFound
	}
	row.Status = "consumed"
	row.ConsumedAt = &now
	cp := *row
	return &cp, nil
}

func (s *stubDeviceCodeRepo) IncrementDeviceCodeFailedAttempts(_ context.Context, userCodeHash string, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dch, ok := s.byUserCodeHash[userCodeHash]
	if !ok {
		return 0, ErrOAuthDeviceCodeNotFound
	}
	row, ok := s.rows[dch]
	if !ok {
		return 0, ErrOAuthDeviceCodeNotFound
	}
	if !row.ExpiresAt.After(now) {
		return 0, ErrOAuthDeviceCodeNotFound
	}
	row.FailedUserCodeAttempts++
	return row.FailedUserCodeAttempts, nil
}

func (s *stubDeviceCodeRepo) TouchDevicePoll(_ context.Context, deviceCodeHash string, lastPollAt time.Time, pollCount int, intervalSeconds int, slowDownCount int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[deviceCodeHash]
	if !ok {
		return ErrOAuthDeviceCodeNotFound
	}
	row.LastPollAt = &lastPollAt
	row.PollCount = pollCount
	row.IntervalSeconds = intervalSeconds
	row.SlowDownCount = slowDownCount
	return nil
}

// stubAccessRepo is a minimal OAuthAccessTokenRepository for device-flow
// minting tests. We only need CreateAccessToken to succeed; the rest is
// no-op.
type stubAccessRepo struct {
	mu    sync.Mutex
	rows  map[int64]*OAuthAccessToken
	calls int
}

func newStubAccessRepo() *stubAccessRepo {
	return &stubAccessRepo{rows: map[int64]*OAuthAccessToken{}}
}

func (s *stubAccessRepo) CreateAccessToken(_ context.Context, t *OAuthAccessToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	cp := *t
	s.rows[t.APIKeyID] = &cp
	return nil
}

func (s *stubAccessRepo) GetActiveAccessTokenByAPIKeyID(_ context.Context, apiKeyID int64, _ time.Time) (*OAuthAccessToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.rows[apiKeyID]; ok {
		cp := *r
		return &cp, nil
	}
	return nil, ErrOAuthAccessTokenNotFound
}

func (s *stubAccessRepo) RevokeAccessTokenByAPIKeyID(_ context.Context, _ int64, _ time.Time) error {
	return nil
}

func (s *stubAccessRepo) RevokeAccessTokensByGrantID(_ context.Context, _ string, _ time.Time) ([]int64, error) {
	return nil, nil
}

func (s *stubAccessRepo) RevokeAccessTokensByTokenFamilyID(_ context.Context, _ string, _ time.Time) ([]int64, error) {
	return nil, nil
}

func (s *stubAccessRepo) TouchAccessToken(_ context.Context, _ int64, _, _ string, _ time.Time) error {
	return nil
}

func (s *stubAccessRepo) ListActiveGrantsByUser(_ context.Context, _ int64, _ time.Time) ([]*OAuthAuthorizedGrant, error) {
	return nil, nil
}

// ── helpers ─────────────────────────────────────────────────────────────────

func newDeviceServiceUnderTest(t *testing.T) (
	*OAuthProviderService,
	*stubClientRepo,
	*stubDeviceCodeRepo,
	*stubAPIKeyRepo,
	*stubRefreshRepo,
	*stubAccessRepo,
) {
	t.Helper()
	groupID := int64(7)
	clientRepo := &stubClientRepo{clients: map[string]*OAuthClient{
		"sakrylle-cli": {
			ClientID:                         "sakrylle-cli",
			Name:                             "Sakrylle CLI",
			RedirectURIs:                     []string{},
			AllowedScopes:                    []string{ScopeProfileRead, ScopeAccountRead, ScopeModelsRead, ScopeMessagesCreate, ScopeOfflineAccess},
			DefaultScopes:                    []string{ScopeProfileRead, ScopeModelsRead},
			PKCERequired:                     true,
			DefaultGroupID:                   &groupID,
			AccessTokenTTLSeconds:            3600,
			RefreshTokenTTLSeconds:           86400,
			DeviceFlowEnabled:                true,
			AllowRefreshWithoutOfflineAccess: false,
		},
		"web-only-client": {
			ClientID:               "web-only-client",
			Name:                   "Web Only",
			RedirectURIs:           []string{"https://example.com/cb"},
			AllowedScopes:          []string{ScopeAccountRead},
			PKCERequired:           true,
			DefaultGroupID:         &groupID,
			AccessTokenTTLSeconds:  3600,
			RefreshTokenTTLSeconds: 86400,
			DeviceFlowEnabled:      false, // device flow disabled
		},
		"disabled-cli": {
			ClientID:               "disabled-cli",
			Name:                   "Disabled CLI",
			Disabled:               true,
			DeviceFlowEnabled:      true,
			PKCERequired:           true,
			AccessTokenTTLSeconds:  3600,
			RefreshTokenTTLSeconds: 86400,
		},
	}}
	apiKeyRepo := newStubAPIKeyRepo()
	refreshRepo := newStubRefreshRepo()
	deviceRepo := newStubDeviceCodeRepo()
	accessRepo := newStubAccessRepo()
	settingRepo := &stubSettingRepo{values: map[string]string{
		"oauth_provider_enabled":    "true",
		"oauth_device_flow_enabled": "true",
		"oauth_default_group_id":    "7",
		"oauth_issuer":              "https://sub.sakrylle.example",
	}}
	svc := NewOAuthProviderService(
		clientRepo,
		newStubCodeRepo(),
		refreshRepo,
		accessRepo,
		deviceRepo,
		nil, // authzTxRepo
		oauthAPIKeyOrAdapter(apiKeyRepo, nil),
		nil, // groupRepo
		nil, // groupAccess
		settingRepo,
		nil,
	)
	return svc, clientRepo, deviceRepo, apiKeyRepo, refreshRepo, accessRepo
}

// ── tests: CreateDeviceCode ─────────────────────────────────────────────────

func TestCreateDeviceCode_GeneratesValidUserCode(t *testing.T) {
	svc, _, _, _, _, _ := newDeviceServiceUnderTest(t)
	ctx := context.Background()
	got, err := svc.CreateDeviceCode(ctx, &DeviceCodeRequest{
		ClientID: "sakrylle-cli",
		Scopes:   []string{ScopeMessagesCreate, ScopeOfflineAccess},
	})
	if err != nil {
		t.Fatalf("CreateDeviceCode failed: %v", err)
	}
	if got.DeviceCode == "" {
		t.Fatalf("device_code should not be empty")
	}
	if got.UserCode == "" {
		t.Fatalf("user_code should not be empty")
	}
	if !strings.HasPrefix(got.UserCode, "SKRY-") {
		t.Fatalf("user_code must start with SKRY-, got %q", got.UserCode)
	}
	// SKRY-XXXX-XXXXX  → len = 4 + 1 + 4 + 1 + 5 = 15
	if len(got.UserCode) != 15 {
		t.Fatalf("user_code expected 15 chars, got %d (%q)", len(got.UserCode), got.UserCode)
	}
	parts := strings.Split(got.UserCode, "-")
	if len(parts) != 3 {
		t.Fatalf("user_code expected 3 dash-separated parts, got %d (%q)", len(parts), got.UserCode)
	}
	if parts[0] != "SKRY" {
		t.Fatalf("user_code prefix must be SKRY, got %q", parts[0])
	}
	if len(parts[1]) != 4 || len(parts[2]) != 5 {
		t.Fatalf("user_code body lengths %d/%d, want 4/5", len(parts[1]), len(parts[2]))
	}
	for _, ch := range parts[1] + parts[2] {
		if !strings.ContainsRune(userCodeAlphabet, ch) {
			t.Fatalf("user_code char %q not in canonical alphabet (%q)", string(ch), got.UserCode)
		}
	}
	if got.ExpiresIn != 600 {
		t.Fatalf("expires_in expected 600, got %d", got.ExpiresIn)
	}
	if got.Interval != 5 {
		t.Fatalf("interval expected 5, got %d", got.Interval)
	}
	if got.VerificationURI == "" {
		t.Fatalf("verification_uri must not be empty")
	}
	if !strings.HasSuffix(got.VerificationURIComplete, got.UserCode) {
		t.Fatalf("verification_uri_complete must include user_code")
	}
}

func TestCreateDeviceCode_NormalizesScopes(t *testing.T) {
	svc, _, deviceRepo, _, _, _ := newDeviceServiceUnderTest(t)
	ctx := context.Background()
	got, err := svc.CreateDeviceCode(ctx, &DeviceCodeRequest{
		ClientID: "sakrylle-cli",
		// duplicates + whitespace
		Scopes: []string{"profile:read", " profile:read", "messages:create"},
	})
	if err != nil {
		t.Fatalf("CreateDeviceCode failed: %v", err)
	}
	row := lookupStubDeviceCodeByPlain(deviceRepo, got.UserCode)
	if row == nil {
		t.Fatalf("expected stored row for newly created code")
	}
	if len(row.Scopes) != 2 {
		t.Fatalf("expected normalized scopes len=2, got %v", row.Scopes)
	}
}

func TestCreateDeviceCode_EmptyScopeUsesClientDefaults(t *testing.T) {
	svc, _, deviceRepo, _, _, _ := newDeviceServiceUnderTest(t)
	ctx := context.Background()
	got, err := svc.CreateDeviceCode(ctx, &DeviceCodeRequest{
		ClientID: "sakrylle-cli",
		Scopes:   nil,
	})
	if err != nil {
		t.Fatalf("CreateDeviceCode failed: %v", err)
	}
	row := lookupStubDeviceCodeByPlain(deviceRepo, got.UserCode)
	if row == nil {
		t.Fatalf("missing stored row")
	}
	want := []string{ScopeProfileRead, ScopeModelsRead}
	if !equalStrSlices(row.Scopes, want) {
		t.Fatalf("expected default scopes %v, got %v", want, row.Scopes)
	}
}

func TestCreateDeviceCode_DisallowedScopeRejected(t *testing.T) {
	svc, _, _, _, _, _ := newDeviceServiceUnderTest(t)
	_, err := svc.CreateDeviceCode(context.Background(), &DeviceCodeRequest{
		ClientID: "sakrylle-cli",
		Scopes:   []string{"images:create"}, // not in AllowedScopes
	})
	if !errors.Is(err, ErrOAuthInvalidScope) {
		t.Fatalf("expected ErrOAuthInvalidScope, got %v", err)
	}
}

func TestCreateDeviceCode_DisabledClient(t *testing.T) {
	svc, _, _, _, _, _ := newDeviceServiceUnderTest(t)
	_, err := svc.CreateDeviceCode(context.Background(), &DeviceCodeRequest{
		ClientID: "disabled-cli",
	})
	if !errors.Is(err, ErrOAuthClientDisabled) {
		t.Fatalf("expected ErrOAuthClientDisabled, got %v", err)
	}
}

func TestCreateDeviceCode_DeviceFlowDisabledForClient(t *testing.T) {
	svc, _, _, _, _, _ := newDeviceServiceUnderTest(t)
	_, err := svc.CreateDeviceCode(context.Background(), &DeviceCodeRequest{
		ClientID: "web-only-client",
	})
	if !errors.Is(err, ErrOAuthDeviceFlowDisabled) {
		t.Fatalf("expected ErrOAuthDeviceFlowDisabled, got %v", err)
	}
}

// ── tests: ApproveDeviceCode / ExchangeDeviceCode ───────────────────────────

func TestApproveAndExchange_HappyPath(t *testing.T) {
	svc, _, _, apiKeyRepo, refreshRepo, accessRepo := newDeviceServiceUnderTest(t)
	ctx := context.Background()

	created, err := svc.CreateDeviceCode(ctx, &DeviceCodeRequest{
		ClientID: "sakrylle-cli",
		Scopes:   []string{ScopeMessagesCreate, ScopeOfflineAccess},
	})
	if err != nil {
		t.Fatalf("CreateDeviceCode failed: %v", err)
	}

	// First poll while pending → authorization_pending.
	if _, err := svc.ExchangeDeviceCode(ctx, "sakrylle-cli", "", created.DeviceCode); !errors.Is(err, ErrOAuthDeviceCodePending) {
		t.Fatalf("expected ErrOAuthDeviceCodePending, got %v", err)
	}

	// Approve.
	if err := svc.ApproveDeviceCode(ctx, 42, created.UserCode, nil); err != nil {
		t.Fatalf("ApproveDeviceCode failed: %v", err)
	}

	// Second poll after approval needs to wait for the interval to
	// elapse — but the interval check uses LastPollAt. Construct a
	// "wait" by sleeping just past the interval. To keep tests fast, we
	// reach into the stub and zero out LastPollAt.
	deviceRepo := svc.deviceRepo.(*stubDeviceCodeRepo)
	deviceRepo.mu.Lock()
	for _, row := range deviceRepo.rows {
		row.LastPollAt = nil
	}
	deviceRepo.mu.Unlock()

	issued, err := svc.ExchangeDeviceCode(ctx, "sakrylle-cli", "", created.DeviceCode)
	if err != nil {
		t.Fatalf("ExchangeDeviceCode failed: %v", err)
	}
	if issued == nil || issued.AccessToken == "" {
		t.Fatalf("expected access token issued")
	}
	if issued.RefreshToken == "" {
		t.Fatalf("expected refresh token (offline_access requested)")
	}
	if issued.GroupID != 7 {
		t.Fatalf("expected group_id=7, got %d", issued.GroupID)
	}
	if accessRepo.calls != 1 {
		t.Fatalf("expected 1 access metadata create, got %d", accessRepo.calls)
	}
	// One api_keys row + one refresh token row.
	apiKeyRepo.mu.Lock()
	apiKeyCount := len(apiKeyRepo.rows)
	apiKeyRepo.mu.Unlock()
	if apiKeyCount != 1 {
		t.Fatalf("expected 1 api_key, got %d", apiKeyCount)
	}
	refreshRepo.mu.Lock()
	rtCount := len(refreshRepo.tokens)
	refreshRepo.mu.Unlock()
	if rtCount != 1 {
		t.Fatalf("expected 1 refresh token, got %d", rtCount)
	}

	// Replay the same device_code → invalid_grant (consumed).
	if _, err := svc.ExchangeDeviceCode(ctx, "sakrylle-cli", "", created.DeviceCode); !errors.Is(err, ErrOAuthDeviceCodeAlreadyConsumed) {
		t.Fatalf("expected ErrOAuthDeviceCodeAlreadyConsumed on second poll, got %v", err)
	}
}

func TestExchangeDeviceCode_DeniedReturnsAccessDenied(t *testing.T) {
	svc, _, _, _, _, _ := newDeviceServiceUnderTest(t)
	ctx := context.Background()
	created, err := svc.CreateDeviceCode(ctx, &DeviceCodeRequest{
		ClientID: "sakrylle-cli",
	})
	if err != nil {
		t.Fatalf("CreateDeviceCode failed: %v", err)
	}
	if err := svc.DenyDeviceCode(ctx, 42, created.UserCode); err != nil {
		t.Fatalf("DenyDeviceCode failed: %v", err)
	}
	if _, err := svc.ExchangeDeviceCode(ctx, "sakrylle-cli", "", created.DeviceCode); !errors.Is(err, ErrOAuthDeviceCodeAccessDenied) {
		t.Fatalf("expected ErrOAuthDeviceCodeAccessDenied, got %v", err)
	}
}

func TestExchangeDeviceCode_ExpiredReturnsExpiredToken(t *testing.T) {
	svc, _, deviceRepo, _, _, _ := newDeviceServiceUnderTest(t)
	ctx := context.Background()
	created, err := svc.CreateDeviceCode(ctx, &DeviceCodeRequest{
		ClientID: "sakrylle-cli",
	})
	if err != nil {
		t.Fatalf("CreateDeviceCode failed: %v", err)
	}
	// Force-expire the row.
	deviceRepo.mu.Lock()
	for _, row := range deviceRepo.rows {
		row.ExpiresAt = time.Now().Add(-time.Minute)
	}
	deviceRepo.mu.Unlock()
	if _, err := svc.ExchangeDeviceCode(ctx, "sakrylle-cli", "", created.DeviceCode); !errors.Is(err, ErrOAuthDeviceCodeExpired) {
		t.Fatalf("expected ErrOAuthDeviceCodeExpired, got %v", err)
	}
}

func TestExchangeDeviceCode_SlowDownBumpsInterval(t *testing.T) {
	svc, _, deviceRepo, _, _, _ := newDeviceServiceUnderTest(t)
	ctx := context.Background()
	created, err := svc.CreateDeviceCode(ctx, &DeviceCodeRequest{
		ClientID: "sakrylle-cli",
	})
	if err != nil {
		t.Fatalf("CreateDeviceCode failed: %v", err)
	}
	// First poll: pending, no last_poll_at yet → succeeds without slow_down.
	if _, err := svc.ExchangeDeviceCode(ctx, "sakrylle-cli", "", created.DeviceCode); !errors.Is(err, ErrOAuthDeviceCodePending) {
		t.Fatalf("first poll expected pending, got %v", err)
	}
	// Second poll immediately after → slow_down + bump interval.
	if _, err := svc.ExchangeDeviceCode(ctx, "sakrylle-cli", "", created.DeviceCode); !errors.Is(err, ErrOAuthDeviceCodeSlowDown) {
		t.Fatalf("expected ErrOAuthDeviceCodeSlowDown on fast repeat, got %v", err)
	}
	// Verify interval was bumped from 5 → 10.
	deviceRepo.mu.Lock()
	var found *OAuthDeviceCode
	for _, row := range deviceRepo.rows {
		found = row
	}
	deviceRepo.mu.Unlock()
	if found == nil {
		t.Fatalf("expected a stored device code row")
	}
	if found.IntervalSeconds != 10 {
		t.Fatalf("expected interval bumped to 10, got %d", found.IntervalSeconds)
	}
	if found.SlowDownCount != 1 {
		t.Fatalf("expected SlowDownCount=1, got %d", found.SlowDownCount)
	}
}

func TestExchangeDeviceCode_WrongClientReturnsInvalidGrant(t *testing.T) {
	svc, clientRepo, _, _, _, _ := newDeviceServiceUnderTest(t)
	ctx := context.Background()
	// Add a second client also with device_flow_enabled.
	clientRepo.clients["other-cli"] = &OAuthClient{
		ClientID:               "other-cli",
		Name:                   "Other CLI",
		AllowedScopes:          []string{ScopeProfileRead},
		PKCERequired:           true,
		DeviceFlowEnabled:      true,
		AccessTokenTTLSeconds:  3600,
		RefreshTokenTTLSeconds: 86400,
	}
	created, err := svc.CreateDeviceCode(ctx, &DeviceCodeRequest{
		ClientID: "sakrylle-cli",
	})
	if err != nil {
		t.Fatalf("CreateDeviceCode failed: %v", err)
	}
	// Polling with the wrong client_id must NOT reveal the row's actual
	// client.
	if _, err := svc.ExchangeDeviceCode(ctx, "other-cli", "", created.DeviceCode); !errors.Is(err, ErrOAuthInvalidGrant) {
		t.Fatalf("expected ErrOAuthInvalidGrant, got %v", err)
	}
}

// ── tests: GetDeviceCodeMetadata + brute-force lockout ──────────────────────

func TestGetDeviceCodeMetadata_FoundAndMissOpaque(t *testing.T) {
	svc, _, _, _, _, _ := newDeviceServiceUnderTest(t)
	ctx := context.Background()
	created, err := svc.CreateDeviceCode(ctx, &DeviceCodeRequest{
		ClientID: "sakrylle-cli",
		Scopes:   []string{ScopeMessagesCreate},
	})
	if err != nil {
		t.Fatalf("CreateDeviceCode failed: %v", err)
	}
	meta, err := svc.GetDeviceCodeMetadata(ctx, created.UserCode, 42)
	if err != nil {
		t.Fatalf("GetDeviceCodeMetadata failed: %v", err)
	}
	if meta == nil || meta.ClientName != "Sakrylle CLI" {
		t.Fatalf("unexpected metadata: %+v", meta)
	}
	if _, err := svc.GetDeviceCodeMetadata(ctx, "SKRY-AAAA-BBBBB", 42); !errors.Is(err, ErrOAuthDeviceUserCodeMismatch) {
		t.Fatalf("expected miss to return ErrOAuthDeviceUserCodeMismatch, got %v", err)
	}
}

func TestApproveDeviceCode_FailedAttemptsLockoutAt5(t *testing.T) {
	svc, _, deviceRepo, _, _, _ := newDeviceServiceUnderTest(t)
	ctx := context.Background()
	created, err := svc.CreateDeviceCode(ctx, &DeviceCodeRequest{
		ClientID: "sakrylle-cli",
	})
	if err != nil {
		t.Fatalf("CreateDeviceCode failed: %v", err)
	}
	// Simulate 5 wrong-format attempts that still hit the same hash by
	// using IncrementDeviceCodeFailedAttempts directly via approve with
	// the right user_code from the perspective of the row but a wrong
	// resolution (we drive failed_user_code_attempts up by deliberately
	// approving with a lookup miss against a tampered code).
	//
	// Easier path: set the failed_user_code_attempts directly to 4 then
	// trigger one more failed attempt and verify the row is denied.
	deviceRepo.mu.Lock()
	for _, row := range deviceRepo.rows {
		row.FailedUserCodeAttempts = 4
	}
	deviceRepo.mu.Unlock()

	// Approve with a wrong user_code → maybeBumpFailedAttempts is called
	// with the WRONG hash, so it won't match. To simulate the brute-force
	// guess that DOES hit the right user_code_hash, we call approve with
	// userID=0 to trigger ErrOAuthSubjectMismatch — but that path doesn't
	// touch failed_user_code_attempts. The actual failure path is the
	// one where the lookup itself fails. The repo's increment helper is
	// called by approve only in that branch.
	//
	// To get coverage of the lockout path without re-exercising guess
	// arithmetic, we directly increment via the repo and assert the
	// service then denies on the next attempt. That's what would happen
	// in practice once 5 misses accumulate.
	hash := hashOAuthToken("anything-that-doesnt-match")
	_, _ = deviceRepo.IncrementDeviceCodeFailedAttempts(ctx, hash, time.Now())

	// Now bump real-row's count to 5 to verify we transition to denied.
	row := lookupStubDeviceCodeByPlain(deviceRepo, created.UserCode)
	if row == nil {
		t.Fatalf("expected stored row")
	}
	deviceRepo.mu.Lock()
	row.FailedUserCodeAttempts = 5
	deviceRepo.mu.Unlock()

	// Service helper denies the row.
	svc.maybeBumpFailedAttempts(ctx, hashOAuthToken(created.UserCode), time.Now())

	row = lookupStubDeviceCodeByPlain(deviceRepo, created.UserCode)
	if row == nil {
		t.Fatalf("expected stored row")
	}
	if row.Status != "denied" {
		t.Fatalf("expected status=denied after lockout, got %s", row.Status)
	}
}

// ── tests: normalizeUserCode ───────────────────────────────────────────────

func TestNormalizeUserCode_AcceptsCanonicalAndLooseForms(t *testing.T) {
	cases := map[string]struct {
		in      string
		want    string
		wantErr bool
	}{
		"canonical":          {"SKRY-BCDF-G2346", "SKRY-BCDF-G2346", false},
		"with extra space":   {" SKRY BCDF G2346 ", "SKRY-BCDF-G2346", false},
		"lowercase":          {"skry-bcdf-g2346", "SKRY-BCDF-G2346", false},
		"missing prefix":     {"BCDF-G2346", "", true},
		"wrong length":       {"SKRY-BCDF-G234", "", true},
		"forbidden char (a)": {"SKRY-ACDF-G2346", "", true},
		"forbidden char (1)": {"SKRY-BCDF-G2316", "", true},
	}
	for name, c := range cases {
		got, err := normalizeUserCode(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("%s: expected error, got %q", name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", name, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
}

// ── tests: globally disabled ───────────────────────────────────────────────

func TestCreateDeviceCode_GloballyDisabled(t *testing.T) {
	svc, _, _, _, _, _ := newDeviceServiceUnderTest(t)
	settingRepo := svc.settingRepo.(*stubSettingRepo)
	settingRepo.values["oauth_device_flow_enabled"] = "false"

	_, err := svc.CreateDeviceCode(context.Background(), &DeviceCodeRequest{
		ClientID: "sakrylle-cli",
	})
	if !errors.Is(err, ErrOAuthDeviceFlowGloballyDisabled) {
		t.Fatalf("expected ErrOAuthDeviceFlowGloballyDisabled, got %v", err)
	}
}

// ── helpers ─────────────────────────────────────────────────────────────────

func lookupStubDeviceCodeByPlain(repo *stubDeviceCodeRepo, userCodePlain string) *OAuthDeviceCode {
	hash := hashOAuthToken(userCodePlain)
	repo.mu.Lock()
	defer repo.mu.Unlock()
	dch, ok := repo.byUserCodeHash[hash]
	if !ok {
		return nil
	}
	row := repo.rows[dch]
	if row == nil {
		return nil
	}
	cp := *row
	return &cp
}

func equalStrSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestExchangeDeviceCode_RaceDoesNotDoubleMint — FIX H6 regression. Two
// concurrent goroutines exchange the same approved device_code; the atomic
// approved→consumed flip MUST guarantee at most one mint. The loser sees
// invalid_grant (already_consumed). Without the atomic gate this test fails
// because both pollers race past the status check before either consumes.
func TestExchangeDeviceCode_RaceDoesNotDoubleMint(t *testing.T) {
	svc, _, deviceRepo, apiKeyRepo, refreshRepo, accessRepo := newDeviceServiceUnderTest(t)
	ctx := context.Background()

	created, err := svc.CreateDeviceCode(ctx, &DeviceCodeRequest{
		ClientID: "sakrylle-cli",
		Scopes:   []string{ScopeMessagesCreate, ScopeOfflineAccess},
	})
	if err != nil {
		t.Fatalf("CreateDeviceCode: %v", err)
	}
	if err := svc.ApproveDeviceCode(ctx, 42, created.UserCode, nil); err != nil {
		t.Fatalf("ApproveDeviceCode: %v", err)
	}
	// Pre-clear LastPollAt so neither contender hits slow_down throttling.
	deviceRepo.mu.Lock()
	for _, row := range deviceRepo.rows {
		row.LastPollAt = nil
	}
	deviceRepo.mu.Unlock()

	type result struct {
		token *IssuedToken
		err   error
	}
	const goroutines = 8
	results := make(chan result, goroutines)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			tok, err := svc.ExchangeDeviceCode(ctx, "sakrylle-cli", "", created.DeviceCode)
			results <- result{token: tok, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	successes := 0
	consumed := 0
	slowDown := 0
	for r := range results {
		switch {
		case r.err == nil && r.token != nil && r.token.AccessToken != "":
			successes++
		case errors.Is(r.err, ErrOAuthDeviceCodeAlreadyConsumed):
			consumed++
		case errors.Is(r.err, ErrOAuthDeviceCodeSlowDown):
			// Legitimate transient response when a faster goroutine has just
			// touched LastPollAt; bucket separately so a regression that
			// surfaces a different error type (e.g. a panic-recover landing
			// in 500/internal_server_error) cannot hide here.
			slowDown++
		default:
			// FIX A6 / T-2(b): bound the loser-error set. Anything outside
			// {already_consumed, slow_down} indicates a regression — fail
			// loudly with the actual error so it cannot be masked as "other".
			t.Errorf("FIX A6: unexpected loser error type: %T %v (token=%v)", r.err, r.err, r.token)
		}
	}
	if successes != 1 {
		t.Fatalf("FIX H6: expected exactly one successful mint across %d racers, got successes=%d already_consumed=%d slow_down=%d",
			goroutines, successes, consumed, slowDown)
	}
	// Closed-set invariant: every goroutine result must fall into one of the
	// three accepted buckets. Any drift means we silently swallowed an error.
	if total := successes + consumed + slowDown; total != goroutines {
		t.Fatalf("FIX A6: bucket arithmetic mismatch — successes=%d + consumed=%d + slow_down=%d = %d, want %d",
			successes, consumed, slowDown, total, goroutines)
	}

	// One api_keys row, one access metadata row, one refresh token.
	apiKeyRepo.mu.Lock()
	apiKeyCount := len(apiKeyRepo.rows)
	apiKeyRepo.mu.Unlock()
	if apiKeyCount != 1 {
		t.Fatalf("expected 1 api_key from race, got %d", apiKeyCount)
	}
	if accessRepo.calls != 1 {
		t.Fatalf("expected 1 access metadata create, got %d", accessRepo.calls)
	}
	refreshRepo.mu.Lock()
	rtCount := len(refreshRepo.tokens)
	refreshRepo.mu.Unlock()
	if rtCount != 1 {
		t.Fatalf("expected 1 refresh token from race, got %d", rtCount)
	}

	// FIX A6 / T-2(d): assert the device code row is in its terminal state.
	// "At most one mint succeeds" is necessary but not sufficient — without
	// reading back the row we cannot prove the consume actually committed
	// (a regression that mints a token without flipping status would still
	// pass the success-count check above).
	row := lookupStubDeviceCodeByPlain(deviceRepo, created.UserCode)
	if row == nil {
		t.Fatalf("FIX A6: device code row missing after race — expected approved→consumed transition")
	}
	if row.Status != "consumed" {
		t.Fatalf("FIX A6: device code Status = %q, want %q", row.Status, "consumed")
	}
	if row.ConsumedAt == nil {
		t.Fatalf("FIX A6: device code ConsumedAt is nil after consume; status=%q", row.Status)
	}
	if row.ConsumedAt.IsZero() {
		t.Fatalf("FIX A6: device code ConsumedAt is the zero time")
	}
	now := time.Now()
	// Allow +5s skew so a slow CI box (clock granularity, scheduler latency)
	// doesn't trip this. The point of the upper bound is to catch a
	// regression that writes a sentinel-future timestamp, not to assert
	// real-time precision.
	if row.ConsumedAt.After(now.Add(5 * time.Second)) {
		t.Fatalf("FIX A6: device code ConsumedAt %v is in the future relative to now %v", *row.ConsumedAt, now)
	}
	// And it must not predate the test (the row was created moments ago).
	if row.ConsumedAt.Before(now.Add(-1 * time.Minute)) {
		t.Fatalf("FIX A6: device code ConsumedAt %v is implausibly old relative to now %v", *row.ConsumedAt, now)
	}
}
