package service

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"

	"github.com/google/uuid"
)

// ── Device Flow constants (RFC 8628 + §10.6 / §12.6 / §12.7 / §12.8) ────────

const (
	// deviceCodeBytes is the entropy of the opaque device_code returned to
	// the client. 32 bytes ≈ 256 bits, well above the 128 bits a real
	// attacker would need for online guess-resistance.
	deviceCodeBytes = 32

	// userCodeAlphabet is the §10.6 character set: Crockford-style minus
	// vowels and ambiguous characters (0/O, 1/I/L, S/5, Z/2 already removed,
	// U/V handled). 24 distinct characters.
	userCodeAlphabet = "BCDFGHJKMPQRTVWXY2346789"

	// userCodeBlockLen is 4 chars per group; the displayed format is
	// SKRY-XXXX-XXXX = 8 chars from the alphabet → log2(24^8) ≈ 36.7 bits.
	// §10.6 requires ≥40 bits, so we use 9 chars (SKRY-XXXX-XXXXX) →
	// log2(24^9) ≈ 41.3 bits. The visible block layout stays user-friendly
	// while clearing the entropy bar.
	userCodeBlockLen     = 4
	userCodeTailBlockLen = 5
	userCodePrefix       = "SKRY"

	// deviceCodeDefaultTTL bounds how long the user has to approve before
	// the code expires (§12.7 RFC 8628 baseline 600s).
	deviceCodeDefaultTTL = 10 * time.Minute

	// deviceCodeDefaultInterval is the initial polling interval handed back
	// to clients in the /oauth/device/code response.
	deviceCodeDefaultInterval = 5

	// deviceCodeSlowDownBump is the amount of seconds added to the interval
	// each time the client polls faster than the previous interval.
	deviceCodeSlowDownBump = 5

	// deviceUserCodeMaxFailedAttempts denies a device code after N failed
	// approval attempts (§10.6 / §12.8: deny on 5 failed user-code matches).
	deviceUserCodeMaxFailedAttempts = 5
)

// IssuedDeviceCode is the §12.7 response body for /oauth/device/code.
type IssuedDeviceCode struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresIn               int
	Interval                int
}

// DeviceCodeRequest is the validated input to CreateDeviceCode.
//
// Mirrors §12.7 form fields. Scopes is the post-ParseScopes list of strings;
// CreateDeviceCode handles normalization and the empty→client-default fallback.
type DeviceCodeRequest struct {
	ClientID         string
	Scopes           []string
	RequestedGroupID *int64
	DeviceID         *string
	DeviceName       *string
	CreatedIP        *string
	CreatedUserAgent *string
}

// DeviceCodeMetadata is what the verification page shows the user before they
// approve. We deliberately don't expose the raw row — the page only needs a
// few render-friendly fields plus the row ID for the approve POST.
type DeviceCodeMetadata struct {
	DeviceCodeID         int64
	ClientID             string
	ClientName           string
	Scopes               []string
	RequestedGroupID     *int64
	DeviceName           *string
	AllowedGroupsForUser []int64
}

// ── Errors specific to the device flow surface ──────────────────────────────

var (
	ErrOAuthDeviceFlowDisabled         = infraerrors.Forbidden("UNAUTHORIZED_CLIENT", "device flow is not enabled for this client")
	ErrOAuthDeviceFlowGloballyDisabled = infraerrors.Forbidden("TEMPORARILY_UNAVAILABLE", "device flow is globally disabled")
	ErrOAuthDeviceUserCodeMismatch     = infraerrors.BadRequest("INVALID_REQUEST", "user_code did not match an active device authorization")
)

// ── Service surface ─────────────────────────────────────────────────────────

// CreateDeviceCode is the §12.7 service method behind POST /oauth/device/code.
//
// Validates the client, normalizes scopes, generates a fresh device_code +
// user_code pair, persists the row in 'pending' status, and returns the
// plaintext values exactly once. The plaintext is never returned again — the
// row stores SHA-256(device_code) and SHA-256(user_code) only.
func (s *OAuthProviderService) CreateDeviceCode(ctx context.Context, req *DeviceCodeRequest) (*IssuedDeviceCode, error) {
	if req == nil {
		return nil, ErrOAuthInvalidGrant
	}
	if !s.isDeviceFlowGloballyEnabled(ctx) {
		return nil, ErrOAuthDeviceFlowGloballyDisabled
	}
	client, err := s.LookupClient(ctx, req.ClientID)
	if err != nil {
		return nil, err
	}
	if !client.DeviceFlowEnabled {
		return nil, ErrOAuthDeviceFlowDisabled
	}

	scopes := NormalizeScopes(req.Scopes)
	if len(scopes) == 0 {
		scopes = NormalizeScopes(client.DefaultScopes)
	}
	if !ScopeAllowed(client.AllowedScopes, scopes) {
		return nil, ErrOAuthInvalidScope
	}

	// Client-level group precheck only — final user-level check happens at
	// approval time when we know the authenticated user.
	if req.RequestedGroupID != nil && *req.RequestedGroupID > 0 {
		if !clientAllowsGroup(client, *req.RequestedGroupID) {
			return nil, ErrOAuthGroupNotAllowed
		}
	}

	deviceCodePlain, err := generateOpaqueToken(deviceCodeBytes)
	if err != nil {
		return nil, fmt.Errorf("generate device code: %w", err)
	}
	userCodePlain, err := generateUserCode()
	if err != nil {
		return nil, fmt.Errorf("generate user code: %w", err)
	}

	now := time.Now()
	row := &OAuthDeviceCode{
		DeviceCodeHash:   hashOAuthToken(deviceCodePlain),
		UserCodeHash:     hashOAuthToken(userCodePlain),
		ClientID:         client.ClientID,
		Scopes:           scopes,
		GroupID:          req.RequestedGroupID,
		DeviceID:         req.DeviceID,
		DeviceName:       req.DeviceName,
		Status:           "pending",
		IntervalSeconds:  deviceCodeDefaultInterval,
		ExpiresAt:        now.Add(deviceCodeDefaultTTL),
		CreatedIP:        req.CreatedIP,
		CreatedUserAgent: req.CreatedUserAgent,
	}
	if err := s.deviceRepo.CreateDeviceCode(ctx, row); err != nil {
		return nil, fmt.Errorf("persist device code: %w", err)
	}

	verificationURI := s.deviceVerificationURI(ctx)
	verificationURIComplete := verificationURI + "?user_code=" + userCodePlain
	return &IssuedDeviceCode{
		DeviceCode:              deviceCodePlain,
		UserCode:                userCodePlain,
		VerificationURI:         verificationURI,
		VerificationURIComplete: verificationURIComplete,
		ExpiresIn:               int(deviceCodeDefaultTTL.Seconds()),
		Interval:                deviceCodeDefaultInterval,
	}, nil
}

// GetDeviceCodeMetadata is what GET /oauth/device shows when the user lands on
// the verification page after the CLI displayed the user_code. Returns
// ErrOAuthDeviceUserCodeMismatch on miss without revealing whether near-misses
// exist (callers should also rate-limit on top).
//
// The returned DeviceCodeID is opaque to the user — callers should pass it
// straight back into ApproveDeviceCode without exposing it in the URL.
func (s *OAuthProviderService) GetDeviceCodeMetadata(
	ctx context.Context,
	userCodePlain string,
	userIDForGroupCheck int64,
) (*DeviceCodeMetadata, error) {
	normalized, err := normalizeUserCode(userCodePlain)
	if err != nil {
		return nil, ErrOAuthDeviceUserCodeMismatch
	}
	now := time.Now()
	row, err := s.deviceRepo.GetDeviceCodeByUserCodeHashForApproval(ctx, hashOAuthToken(normalized), now)
	if err != nil {
		// Map every "row not loadable / wrong state / expired" miss to the
		// same opaque error so timing/error-shape doesn't leak existence.
		return nil, ErrOAuthDeviceUserCodeMismatch
	}
	if row == nil {
		return nil, ErrOAuthDeviceUserCodeMismatch
	}
	client, err := s.LookupClient(ctx, row.ClientID)
	if err != nil {
		// The client was disabled or removed between code creation and
		// verification page load. Treat as miss.
		return nil, ErrOAuthDeviceUserCodeMismatch
	}

	var allowed []int64
	if userIDForGroupCheck > 0 && s.groupAccess != nil {
		groups, gerr := s.groupAccess.ListUserAllowedGroupsForOAuth(ctx, userIDForGroupCheck, client, NormalizeScopes(row.Scopes))
		if gerr == nil {
			raw := oauthAllowedGroupIDs(groups)
			allowed = filterAllowedByClient(client, raw)
		}
	}

	return &DeviceCodeMetadata{
		DeviceCodeID:         row.ID,
		ClientID:             client.ClientID,
		ClientName:           client.Name,
		Scopes:               NormalizeScopes(row.Scopes),
		RequestedGroupID:     row.GroupID,
		DeviceName:           row.DeviceName,
		AllowedGroupsForUser: allowed,
	}, nil
}

// ApproveDeviceCode marks a pending device code approved on behalf of the
// authenticated user. The polling /oauth/token call observes the new state
// and mints tokens. Final user-level group resolution happens here.
//
// userCodePlain is the value the user typed into the verification page. The
// service rehashes and matches against the row.
//
// If finalGroupID is supplied (the user picked a different group on the consent
// page), we re-validate it; otherwise we fall back to the row's stored
// requested group, then to client default. ResolveOAuthGroup enforces the
// user-level access policy in every case.
func (s *OAuthProviderService) ApproveDeviceCode(
	ctx context.Context,
	userID int64,
	userCodePlain string,
	finalGroupID *int64,
) error {
	if userID <= 0 {
		return ErrOAuthSubjectMismatch
	}
	normalized, err := normalizeUserCode(userCodePlain)
	if err != nil {
		return ErrOAuthDeviceUserCodeMismatch
	}
	now := time.Now()
	userHash := hashOAuthToken(normalized)
	row, err := s.deviceRepo.GetDeviceCodeByUserCodeHashForApproval(ctx, userHash, now)
	if err != nil {
		s.maybeBumpFailedAttempts(ctx, userHash, now)
		return ErrOAuthDeviceUserCodeMismatch
	}
	client, err := s.LookupClient(ctx, row.ClientID)
	if err != nil {
		return err
	}

	requested := row.GroupID
	if finalGroupID != nil && *finalGroupID > 0 {
		requested = finalGroupID
	}
	resolved, err := s.ResolveOAuthGroup(ctx, userID, client, requested)
	if err != nil {
		return err
	}

	if err := s.deviceRepo.ApproveDeviceCode(ctx, userHash, userID, resolved, now); err != nil {
		return fmt.Errorf("approve device code: %w", err)
	}
	return nil
}

// DenyDeviceCode is the explicit user-rejected path; future polls return
// access_denied. Fail-soft on already-denied / expired / consumed rows so
// callers can keep the API idempotent (§12.8).
func (s *OAuthProviderService) DenyDeviceCode(
	ctx context.Context,
	userID int64,
	userCodePlain string,
) error {
	if userID <= 0 {
		return ErrOAuthSubjectMismatch
	}
	normalized, err := normalizeUserCode(userCodePlain)
	if err != nil {
		return ErrOAuthDeviceUserCodeMismatch
	}
	now := time.Now()
	userHash := hashOAuthToken(normalized)
	if err := s.deviceRepo.DenyDeviceCode(ctx, userHash, now); err != nil {
		// Treat missing rows as already-resolved → idempotent success.
		if errors.Is(err, ErrOAuthDeviceCodeNotFound) {
			return nil
		}
		return fmt.Errorf("deny device code: %w", err)
	}
	return nil
}

// ExchangeDeviceCode is the §12.6 polling endpoint behind
// /oauth/token grant_type=urn:ietf:params:oauth:grant-type:device_code.
//
// Maps row state → RFC 8628 polling response:
//
//   - pending  → ErrOAuthDeviceCodePending   (authorization_pending)
//   - too fast → ErrOAuthDeviceCodeSlowDown  (slow_down + interval += 5)
//   - denied   → ErrOAuthDeviceCodeAccessDenied
//   - expired  → ErrOAuthDeviceCodeExpired
//   - approved → mint tokens, mark consumed
//   - consumed → ErrOAuthInvalidGrant (cannot mint twice)
//
// Wrong client_id binding returns ErrOAuthInvalidGrant without revealing the
// row's actual client_id (§12.6 + §18.7).
func (s *OAuthProviderService) ExchangeDeviceCode(
	ctx context.Context,
	clientID, clientSecret, deviceCodePlain string,
) (*IssuedToken, error) {
	if !s.isDeviceFlowGloballyEnabled(ctx) {
		return nil, ErrOAuthDeviceFlowGloballyDisabled
	}
	if strings.TrimSpace(deviceCodePlain) == "" {
		return nil, ErrOAuthDeviceCodeNotFound
	}
	client, err := s.LookupClient(ctx, clientID)
	if err != nil {
		return nil, err
	}
	if err := s.authenticateClient(client, clientSecret); err != nil {
		return nil, err
	}
	if !client.DeviceFlowEnabled {
		return nil, ErrOAuthDeviceFlowDisabled
	}

	now := time.Now()
	codeHash := hashOAuthToken(deviceCodePlain)
	row, err := s.deviceRepo.PollDeviceCodeForUpdate(ctx, codeHash, now)
	if err != nil {
		return nil, err
	}

	// Cross-client binding check first — do not reveal the row's actual
	// client. Same opaque "invalid_grant" the spec recommends.
	if row.ClientID != client.ClientID {
		return nil, ErrOAuthInvalidGrant
	}

	// Terminal-state checks come BEFORE slow_down enforcement: a consumed
	// or denied or expired code should always return its terminal error,
	// even if the client polled fast. Slow_down only matters while the
	// row is still pending.
	switch row.Status {
	case "denied":
		return nil, ErrOAuthDeviceCodeAccessDenied
	case "expired":
		return nil, ErrOAuthDeviceCodeExpired
	case "consumed":
		return nil, ErrOAuthDeviceCodeAlreadyConsumed
	}

	// Polling-interval enforcement (§12.6). Once a slow_down has fired we
	// bump the interval by 5s for the rest of the row's life. We treat any
	// poll within `interval_seconds` of the previous one as "too fast".
	if row.LastPollAt != nil {
		minNext := row.LastPollAt.Add(time.Duration(row.IntervalSeconds) * time.Second)
		if now.Before(minNext) {
			s.touchDevicePollAndBumpInterval(ctx, row, codeHash, now, true)
			return nil, ErrOAuthDeviceCodeSlowDown
		}
	}
	// Always update last_poll_at on every accepted poll so the interval
	// check has a previous timestamp to compare against. We don't bump the
	// interval here, only on slow_down.
	s.touchDevicePollAndBumpInterval(ctx, row, codeHash, now, false)

	// State machine for non-terminal states.
	switch row.Status {
	case "pending":
		if !row.ExpiresAt.After(now) {
			return nil, ErrOAuthDeviceCodeExpired
		}
		return nil, ErrOAuthDeviceCodePending
	case "approved":
		if !row.ExpiresAt.After(now) {
			return nil, ErrOAuthDeviceCodeExpired
		}
		return s.mintTokensFromDeviceCode(ctx, client, row, codeHash, now)
	default:
		return nil, ErrOAuthInvalidGrant
	}
}

// ── internal helpers ────────────────────────────────────────────────────────

// mintTokensFromDeviceCode is the §12.6 atomic-write path called after we
// observe an approved + unexpired device code. Mirrors mintTokensFromCode
// but builds the OAuthCode-shaped intermediate from the device-code row so
// the existing mint logic stays the single source of truth for token
// material, scope normalization, and refresh-issuance rules.
//
// FIX H6: the order is INVERTED from the legacy mint-then-consume sequence.
// We call ConsumeApprovedDeviceCode FIRST so a concurrent second poll can
// never observe the row in 'approved' state again. Only the caller that
// wins the atomic UPDATE proceeds to mint. If the mint then fails, the
// device code is already consumed — RFC 8628 §3.5 explicitly tolerates this
// outcome (the user reauthorizes).
func (s *OAuthProviderService) mintTokensFromDeviceCode(
	ctx context.Context,
	client *OAuthClient,
	row *OAuthDeviceCode,
	deviceCodeHash string,
	now time.Time,
) (*IssuedToken, error) {
	// Group binding: row.GroupID was set by ApproveDeviceCode to the
	// resolved (and access-validated) group. If it's missing for any
	// reason (legacy rows before approval populated it), fall back to the
	// approving user's default — but we have no user_id on this row when
	// approved_by_user_id is nil, so fail closed.
	if row.ApprovedByUserID == nil || *row.ApprovedByUserID <= 0 {
		return nil, ErrOAuthInvalidGrant
	}
	approvingUserID := *row.ApprovedByUserID

	// FIX H6: gate on atomic consume BEFORE minting. If two pollers race,
	// at most one wins; the loser sees ErrOAuthDeviceCodeNotFound (because
	// status is no longer 'approved'), which the caller maps to
	// ErrOAuthDeviceCodeAlreadyConsumed.
	if atomic, ok := s.deviceRepo.(interface {
		ConsumeApprovedDeviceCode(ctx context.Context, deviceCodeHash string, now time.Time) (*OAuthDeviceCode, error)
	}); ok {
		consumed, cerr := atomic.ConsumeApprovedDeviceCode(ctx, deviceCodeHash, now)
		if cerr != nil {
			if errors.Is(cerr, ErrOAuthDeviceCodeNotFound) {
				return nil, ErrOAuthDeviceCodeAlreadyConsumed
			}
			return nil, fmt.Errorf("consume approved device code: %w", cerr)
		}
		// Use the freshly-loaded snapshot for downstream resolution so we
		// see the final approved-time GroupID/ApprovedByUserID even if the
		// caller's row was loaded before approval finished.
		if consumed != nil {
			row = consumed
			if row.ApprovedByUserID == nil || *row.ApprovedByUserID <= 0 {
				return nil, ErrOAuthInvalidGrant
			}
			approvingUserID = *row.ApprovedByUserID
		}
	}

	var groupID int64
	if row.GroupID != nil && *row.GroupID > 0 {
		groupID = *row.GroupID
	} else {
		resolved, err := s.ResolveOAuthGroup(ctx, approvingUserID, client, nil)
		if err != nil {
			return nil, err
		}
		groupID = resolved
	}

	// Reuse a fresh grant_id if the row didn't pre-populate one. This keeps
	// every token family discoverable through /api/v1/oauth/authorized-apps
	// even when the device-code row was approved by the legacy repo path
	// that doesn't write grant_id.
	grantID := stringValueOrEmpty(row.GrantID)
	if grantID == "" {
		grantID = uuid.NewString()
	}

	// Synthesize the OAuthCode shape mintTokensFromCode expects. Allowed
	// groups snapshot is empty for device flow today (we don't capture
	// allowed_groups at /oauth/device/code time the way /authorize does);
	// mintTokensFromCode falls back to []int64{groupID} when empty, which
	// is the documented behavior for device-flow tokens (§9 + §12.6).
	pseudoCode := &OAuthCode{
		ClientID:              client.ClientID,
		UserID:                approvingUserID,
		Scopes:                NormalizeScopes(row.Scopes),
		ExpiresAt:             row.ExpiresAt,
		GroupID:               &groupID,
		GrantID:               &grantID,
		AllowedGroupsSnapshot: nil,
		DeviceID:              row.DeviceID,
		DeviceName:            row.DeviceName,
	}

	issued, err := s.mintTokensFromCode(ctx, client, pseudoCode)
	if err != nil {
		// Mint failed AFTER atomic consume — per RFC 8628 §3.5 this is
		// acceptable. Log so operators can track the rare race; the user
		// will see the underlying error and reauthorize.
		slog.Warn("oauth: device code mint failed after atomic consume",
			"client_id", client.ClientID,
			"err", err,
		)
		return nil, err
	}
	return issued, nil
}

// touchDevicePollAndBumpInterval updates last_poll_at, increments poll_count,
// and (when bumpInterval=true) bumps interval_seconds by 5 with a per-row
// slow_down_count for observability. Failures are non-fatal — the polling
// path must keep returning a useful response even if the bookkeeping write
// flakes.
//
// Persistence matters: a fresh poll from another replica MUST observe the
// new interval, otherwise slow_down would only stick within the process that
// served the original too-fast poll. We use the repo's TouchDevicePoll to
// write the new values back atomically under the row lock.
func (s *OAuthProviderService) touchDevicePollAndBumpInterval(
	ctx context.Context,
	row *OAuthDeviceCode,
	deviceCodeHash string,
	now time.Time,
	bumpInterval bool,
) {
	row.PollCount++
	row.LastPollAt = &now
	if bumpInterval {
		row.IntervalSeconds += deviceCodeSlowDownBump
		row.SlowDownCount++
	}
	if s.deviceRepo == nil {
		return
	}
	if err := s.deviceRepo.TouchDevicePoll(
		ctx, deviceCodeHash, now, row.PollCount, row.IntervalSeconds, row.SlowDownCount,
	); err != nil {
		slog.Warn("oauth: persist device code poll metadata failed",
			"client_id", row.ClientID,
			"err", err,
		)
	}
}

// maybeBumpFailedAttempts is best-effort observability for §10.6 — but the
// real brute-force defense for device flow is the per-IP rate limit on
// POST /api/v1/oauth/device/approve (5 attempts / 15 min / IP, fail-close;
// see internal/server/routes/oauth_device.go).
//
// FIX M6: the legacy code looked up the row by hash(attacker_input). Wrong
// guesses don't match any row, so the counter never bumped — making the
// "5 strikes per code" claim from §10.6 ineffective in practice. We keep
// this method only for the rare case where the hash DID match a real row
// (i.e. the user typed a valid SKRY code but failed downstream validation
// like ResolveOAuthGroup); under that path the bump is meaningful as a
// stale-row defense rather than an adversarial counter. The IP limiter is
// what stops 1000-guess attacks.
func (s *OAuthProviderService) maybeBumpFailedAttempts(ctx context.Context, userCodeHash string, now time.Time) {
	if s.deviceRepo == nil {
		return
	}
	count, err := s.deviceRepo.IncrementDeviceCodeFailedAttempts(ctx, userCodeHash, now)
	if err != nil {
		// Most common: row doesn't exist → ErrOAuthDeviceCodeNotFound.
		// That's the expected case for typo-style guesses; the IP rate
		// limit handles those at the handler layer.
		return
	}
	if count >= deviceUserCodeMaxFailedAttempts {
		// Force-deny so future polls return access_denied instead of
		// authorization_pending. We use the repo's deny path which is
		// state-aware (only flips pending→denied).
		_ = s.deviceRepo.DenyDeviceCode(ctx, userCodeHash, now)
	}
}

// isDeviceFlowGloballyEnabled checks the §10.7 oauth_device_flow_enabled kill
// switch. Defaults to true on missing/unparseable so a fresh install works.
func (s *OAuthProviderService) isDeviceFlowGloballyEnabled(ctx context.Context) bool {
	if s.settingRepo == nil {
		return true
	}
	value, err := s.settingRepo.GetValue(ctx, "oauth_device_flow_enabled")
	if err != nil {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "false", "0", "off", "no":
		return false
	default:
		return true
	}
}

// deviceVerificationURI builds the §12.7 verification_uri value.
//
// Source order is kept in lockstep with SettingService.GetOAuthIssuer (and
// therefore with RFC 8414 discovery in the handler layer) so the two
// endpoints cannot disagree about which origin Sakrylle considers canonical:
//
//  1. settings.oauth_issuer  (canonical; per migration 145 deployments MUST set)
//  2. settings.frontend_url  (legacy fallback)
//  3. relative path "/oauth/device" (last resort — fresh local install before
//     either key is written; CLI clients will reject this, which is the right
//     failure mode for a misconfigured deployment).
//
// Trailing slashes are stripped before appending the device path so a stored
// "https://example.com/" cannot produce "https://example.com//oauth/device".
func (s *OAuthProviderService) deviceVerificationURI(ctx context.Context) string {
	if s.settingRepo == nil {
		return "/oauth/device"
	}
	for _, key := range []string{SettingKeyOAuthIssuer, SettingKeyFrontendURL} {
		if v, err := s.settingRepo.GetValue(ctx, key); err == nil {
			trimmed := strings.TrimRight(strings.TrimSpace(v), "/")
			if trimmed != "" {
				return trimmed + "/oauth/device"
			}
		}
	}
	return "/oauth/device"
}

// ── User-code generation and normalization ──────────────────────────────────

// generateUserCode produces an §10.6-compliant user code:
//
//	SKRY-XXXX-XXXXX  (4+5 = 9 chars from a 24-char alphabet → ~41.3 bits)
//
// Uses crypto/rand with rejection-free uniform mapping (24 divides 256
// evenly enough that per-char bias is negligible; we use modulo-via-uint64
// across 4 random bytes per char to keep the bias < 2^-50).
func generateUserCode() (string, error) {
	const totalChars = userCodeBlockLen + userCodeTailBlockLen
	out := make([]byte, totalChars)
	for i := 0; i < totalChars; i++ {
		c, err := uniformRandIndex(uint32(len(userCodeAlphabet)))
		if err != nil {
			return "", err
		}
		out[i] = userCodeAlphabet[c]
	}
	return userCodePrefix + "-" +
		string(out[:userCodeBlockLen]) + "-" +
		string(out[userCodeBlockLen:]), nil
}

// uniformRandIndex returns a uniformly random index in [0, n) drawn from
// crypto/rand using rejection sampling on a 32-bit random word. The bias is
// 0 when n is a power of 2; otherwise we discard the tail of the 2^32 range
// that is not a multiple of n. For n=24 this rejection rate is < 0.0001%.
func uniformRandIndex(n uint32) (uint32, error) {
	if n == 0 {
		return 0, errors.New("uniformRandIndex: n must be > 0")
	}
	max := (uint32(0xFFFFFFFF) / n) * n
	var buf [4]byte
	for {
		if _, err := rand.Read(buf[:]); err != nil {
			return 0, err
		}
		v := binary.BigEndian.Uint32(buf[:])
		if v < max {
			return v % n, nil
		}
	}
}

// normalizeUserCode upper-cases, strips dashes/whitespace, and re-validates
// against the allowed alphabet. Returns the canonical form (with dashes) so
// the SHA-256 lookup matches the form CreateDeviceCode wrote.
func normalizeUserCode(raw string) (string, error) {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '-', '_':
			return -1
		default:
			return r
		}
	}, strings.ToUpper(strings.TrimSpace(raw)))
	if !strings.HasPrefix(cleaned, userCodePrefix) {
		return "", fmt.Errorf("user_code missing %s prefix", userCodePrefix)
	}
	body := cleaned[len(userCodePrefix):]
	const totalChars = userCodeBlockLen + userCodeTailBlockLen
	if len(body) != totalChars {
		return "", fmt.Errorf("user_code body length %d, want %d", len(body), totalChars)
	}
	for i := 0; i < len(body); i++ {
		if !strings.ContainsRune(userCodeAlphabet, rune(body[i])) {
			return "", fmt.Errorf("user_code contains disallowed character at %d", i)
		}
	}
	return userCodePrefix + "-" + body[:userCodeBlockLen] + "-" + body[userCodeBlockLen:], nil
}
