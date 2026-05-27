package service

import (
	"context"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ── Domain types ────────────────────────────────────────────────────────────

// OAuthClient is a registered OAuth 2.0 client.
//
// v2 fields (migration 145) extend the row with client classification,
// default scopes, optional client-level group whitelist, and a few UX URLs.
// Service-layer rules (write-once client_type, non-empty allowed_group_ids
// when present, confidential⇒secret-hash) are enforced in the service, NOT
// in Ent — see docs/OAUTH_V2_DESIGN.md §10.1.
type OAuthClient struct {
	ClientID               string
	Name                   string
	ClientSecretHash       string
	RedirectURIs           []string
	AllowedScopes          []string
	PKCERequired           bool
	DefaultGroupID         *int64
	AccessTokenTTLSeconds  int
	RefreshTokenTTLSeconds int
	Disabled               bool

	// ── v2 ────────────────────────────────────────────────────────────────

	// ClientType is "public" or "confidential". Write-once after insert; the
	// service must reject changing it in place.
	ClientType string
	// AppType categorizes UX/storage expectations: web, native, cli, image,
	// chat, server, image-playground, etc. See §8.1 for the full enum.
	AppType string
	// TrustedFirstParty marks Sakrylle-controlled clients that may bypass
	// per-grant consent under §9.
	TrustedFirstParty bool
	// DefaultScopes is used when /authorize requests no scope. Must be a
	// subset of AllowedScopes; service-layer validates.
	DefaultScopes []string
	// AllowedGroupIDs: nil ≙ "no client-level restriction"; when non-nil it
	// MUST be non-empty (DB CHECK + service-layer validation).
	AllowedGroupIDs []int64
	// AllowedOrigins is the CORS origin allowlist for the token endpoint
	// when this PKCE public client calls cross-origin from the browser.
	AllowedOrigins []string
	// LogoutRedirectURIs is the post-logout redirect_uri allowlist.
	LogoutRedirectURIs []string
	// DeviceFlowEnabled allows this client to use the RFC 8628 device grant.
	DeviceFlowEnabled bool
	// AllowRefreshWithoutOfflineAccess is the legacy compat switch that lets
	// /token mint refresh tokens even when offline_access wasn't granted.
	AllowRefreshWithoutOfflineAccess bool
	IconURL                          *string
	HomepageURL                      *string
	PrivacyURL                       *string
	TermsURL                         *string
}

// OAuthCode is a short-lived authorization code (RFC 6749 §4.1).
//
// CodeHash is hex-encoded SHA-256 of the issued code; the plaintext code is
// only ever returned via the 302 to redirect_uri.
//
// v2 (§10.2) adds GroupID/GrantID/AllowedGroupsSnapshot/Device* — new code
// rows MUST set GrantID and AllowedGroupsSnapshot.
type OAuthCode struct {
	CodeHash            string
	ClientID            string
	UserID              int64
	RedirectURI         string
	Scopes              []string
	CodeChallenge       string
	CodeChallengeMethod string
	ExpiresAt           time.Time
	UsedAt              *time.Time

	GroupID               *int64
	GrantID               *string
	AllowedGroupsSnapshot []int64
	DeviceID              *string
	DeviceName            *string
}

// OAuthRefreshToken records a refresh token bound to a specific access_token (api_keys row).
//
// v2 (§10.4) adds grant/family identity, group binding, device labels, and
// last_used / reuse_detected timestamps.
type OAuthRefreshToken struct {
	TokenHash     string
	ClientID      string
	UserID        int64
	APIKeyID      int64
	Scopes        []string
	ExpiresAt     time.Time
	RevokedAt     *time.Time
	RotatedToHash *string

	GrantID               *string
	TokenFamilyID         *string
	GroupID               *int64
	AllowedGroupsSnapshot []int64
	DeviceID              *string
	DeviceName            *string
	LastUsedAt            *time.Time
	ReuseDetectedAt       *time.Time
}

// OAuthGrant is the LEGACY user-facing aggregate view of an authorization:
// all active refresh tokens for one (user, client) pair collapsed into a
// single row. Kept for the GET /api/v1/oauth/grants compatibility endpoint.
//
// v2 prefers OAuthAuthorizedGrant (one row per device/grant); see §12.10.
type OAuthGrant struct {
	ClientID          string
	ClientName        string
	ClientDisabled    bool
	Scopes            []string
	FirstAuthorizedAt time.Time
	LastUsedAt        *time.Time
	ActiveTokenCount  int
}

// OAuthAccessToken is the v2 metadata row that lives alongside the api_keys
// row holding the access-token plaintext. See §10.5 / §11.2.
type OAuthAccessToken struct {
	ID              int64
	APIKeyID        int64
	GrantID         string
	TokenFamilyID   string
	ClientID        string
	UserID          int64
	Scopes          []string
	GroupID         int64
	AllowedGroupIDs []int64
	AppType         string
	DeviceID        *string
	DeviceName      *string
	IssuedAt        time.Time
	ExpiresAt       time.Time
	LastUsedAt      *time.Time
	LastUsedIP      *string
	LastUsedUA      *string
	RevokedAt       *time.Time
}

// OAuthDeviceCode is v2 device-flow state (RFC 8628). DeviceCodeHash and
// UserCodeHash are SHA-256(plaintext); the plaintext is returned exactly
// once at /oauth/device/code creation. See §10.6 / §11.2.
type OAuthDeviceCode struct {
	ID                     int64
	DeviceCodeHash         string
	UserCodeHash           string
	ClientID               string
	Scopes                 []string
	GrantID                *string
	GroupID                *int64
	DeviceID               *string
	DeviceName             *string
	Status                 string // pending | approved | denied | consumed | expired
	IntervalSeconds        int
	PollCount              int
	LastPollAt             *time.Time
	SlowDownCount          int
	FailedUserCodeAttempts int
	ApprovedByUserID       *int64
	ApprovedAt             *time.Time
	DeniedAt               *time.Time
	ConsumedAt             *time.Time
	ExpiresAt              time.Time
	CreatedIP              *string
	CreatedUserAgent       *string
	CreatedAt              time.Time
}

// OAuthAuthorizeTransaction holds server-side state for a validated
// /authorize request that was rendered as a consent page. CSRFHash is
// SHA-256(plaintext); plaintext is only present in the rendered HTML.
// See §10.3 / §11.2.
type OAuthAuthorizeTransaction struct {
	ID                    int64
	TransactionID         string
	CSRFHash              string
	ClientID              string
	RedirectURI           string
	ResponseType          string
	Scopes                []string
	AllowedGroupsSnapshot []int64
	State                 string
	CodeChallenge         string
	CodeChallengeMethod   string
	RequestedGroupID      *int64
	DeviceID              *string
	DeviceName            *string
	ConsumedAt            *time.Time
	ExpiresAt             time.Time
	CreatedIP             *string
	CreatedUserAgent      *string
	CreatedAt             time.Time
}

// OAuthAuthorizedGrant is the v2 user-facing per-device grant projection
// returned by GET /api/v1/oauth/authorized-apps and surfaced through the
// Authorized Apps UI. See §12.10.
type OAuthAuthorizedGrant struct {
	GrantID                 string
	ClientID                string
	ClientName              string
	ClientDisabled          bool
	AppType                 string
	IconURL                 *string
	DeviceID                *string
	DeviceName              *string
	GroupID                 int64
	GroupName               string
	Scopes                  []string
	FirstAuthorizedAt       time.Time
	LastUsedAt              *time.Time
	LastUsedIP              *string
	ActiveAccessTokenCount  int
	ActiveRefreshTokenCount int
	Status                  string // active | revoked
}

// ── Repository interfaces ───────────────────────────────────────────────────

type OAuthClientRepository interface {
	GetClientByID(ctx context.Context, clientID string) (*OAuthClient, error)
	// ListEnabledRedirectURIs returns the union of redirect_uris across every
	// non-disabled OAuth client. Used by the CORS layer to compute the
	// dynamic browser-origin allowlist for /oauth/token (public PKCE clients
	// must call the token endpoint cross-origin from the browser).
	ListEnabledRedirectURIs(ctx context.Context) ([]string, error)
}

type OAuthCodeRepository interface {
	CreateCode(ctx context.Context, code *OAuthCode) error
	// ConsumeCode atomically marks a code as used and returns it.
	//
	// On ErrOAuthCodeAlreadyUsed the loaded row IS still returned (non-nil)
	// so the caller can revoke any tokens previously derived from this code
	// (RFC 6749 §10.5: code-reuse SHOULD revoke prior issuances).
	ConsumeCode(ctx context.Context, codeHash string, now time.Time) (*OAuthCode, error)
	DeleteExpiredCodes(ctx context.Context, before time.Time) (int, error)
}

type OAuthRefreshTokenRepository interface {
	CreateRefreshToken(ctx context.Context, token *OAuthRefreshToken) error
	// ConsumeForRotation atomically locks the old refresh row, validates it,
	// marks it revoked, and stores rotated_to_hash. Returns the loaded old
	// row so the caller can mint a replacement. Replays after a successful
	// rotation return ErrOAuthRefreshTokenRevoked.
	ConsumeForRotation(ctx context.Context, oldHash, newHash string, now time.Time) (*OAuthRefreshToken, error)
	RevokeRefreshTokensByAPIKeyID(ctx context.Context, apiKeyID int64, now time.Time) error
	// ListActiveByUserAndClient returns all non-revoked, unexpired refresh
	// tokens for (user_id, client_id). Used by code-replay revocation to
	// find every token derived from the previous successful exchange.
	ListActiveByUserAndClient(ctx context.Context, userID int64, clientID string, now time.Time) ([]*OAuthRefreshToken, error)
	// ListActiveByUser returns all non-revoked, unexpired refresh tokens
	// for a user, across all clients. Used by the user-facing "authorized
	// apps" view, which groups by client_id in the service layer.
	ListActiveByUser(ctx context.Context, userID int64, now time.Time) ([]*OAuthRefreshToken, error)

	// ── v2 (§11.2) ────────────────────────────────────────────────────────

	// GetRefreshTokenByHashForUpdate returns the refresh-token row under a
	// row lock, INCLUDING revoked rows. If revoked_at != nil &&
	// rotated_to_hash != nil, the service uses token_family_id to revoke
	// the whole family. Returning only ErrOAuthRefreshTokenRevoked without
	// row data is not sufficient for v2.
	GetRefreshTokenByHashForUpdate(ctx context.Context, tokenHash string, now time.Time) (*OAuthRefreshToken, error)
	// RevokeRefreshTokensByGrantID marks all matching rows revoked and
	// returns the api_key_ids of rows that were freshly revoked, for
	// downstream cache invalidation.
	RevokeRefreshTokensByGrantID(ctx context.Context, grantID string, now time.Time) ([]int64, error)
	RevokeRefreshTokensByTokenFamilyID(ctx context.Context, tokenFamilyID string, now time.Time) ([]int64, error)
	RevokeRefreshTokensByUserAndClient(ctx context.Context, userID int64, clientID string, now time.Time) ([]int64, error)
}

// OAuthAccessTokenRepository owns the v2 oauth_access_tokens row. See §11.2.
type OAuthAccessTokenRepository interface {
	CreateAccessToken(ctx context.Context, token *OAuthAccessToken) error
	GetActiveAccessTokenByAPIKeyID(ctx context.Context, apiKeyID int64, now time.Time) (*OAuthAccessToken, error)
	RevokeAccessTokenByAPIKeyID(ctx context.Context, apiKeyID int64, now time.Time) error
	// RevokeAccessTokensByGrantID revokes all access metadata rows for a
	// grant_id and returns the api_key_ids that were freshly revoked, so
	// the caller can resolve plaintext keys and invalidate the auth cache.
	RevokeAccessTokensByGrantID(ctx context.Context, grantID string, now time.Time) ([]int64, error)
	RevokeAccessTokensByTokenFamilyID(ctx context.Context, tokenFamilyID string, now time.Time) ([]int64, error)
	// TouchAccessToken updates last_used_at/last_used_ip/last_used_user_agent
	// with a 60-second throttle. The service layer enforces the throttle so
	// the repo can stay simple; the repo just performs an UPDATE.
	TouchAccessToken(ctx context.Context, apiKeyID int64, ip, userAgent string, now time.Time) error
	// ListActiveGrantsByUser returns the per-device authorized-app
	// projection (one row per (user, grant_id)). Implementations join
	// against oauth_clients/groups for display fields.
	ListActiveGrantsByUser(ctx context.Context, userID int64, now time.Time) ([]*OAuthAuthorizedGrant, error)
}

// OAuthDeviceCodeRepository owns the v2 oauth_device_codes row. See §11.2.
type OAuthDeviceCodeRepository interface {
	CreateDeviceCode(ctx context.Context, code *OAuthDeviceCode) error
	// GetDeviceCodeByUserCodeHashForApproval returns the row matching a
	// user_code_hash submitted on the consent page, under row lock, only
	// if it is in 'pending' status and not expired. Used by approve/deny.
	GetDeviceCodeByUserCodeHashForApproval(ctx context.Context, userCodeHash string, now time.Time) (*OAuthDeviceCode, error)
	// PollDeviceCodeForUpdate returns the row matching a device_code_hash
	// under row lock, in any status; the service layer maps status into
	// authorization_pending / slow_down / access_denied / expired_token /
	// invalid_grant.
	PollDeviceCodeForUpdate(ctx context.Context, deviceCodeHash string, now time.Time) (*OAuthDeviceCode, error)
	ApproveDeviceCode(ctx context.Context, userCodeHash string, userID int64, groupID int64, now time.Time) error
	DenyDeviceCode(ctx context.Context, userCodeHash string, now time.Time) error
	MarkDeviceCodeConsumed(ctx context.Context, deviceCodeHash string, now time.Time) error
	// IncrementDeviceCodeFailedAttempts atomically bumps
	// failed_user_code_attempts on an unexpired matching row and returns
	// the new value. The service decides whether to flip status to denied
	// (≥5 failed attempts).
	IncrementDeviceCodeFailedAttempts(ctx context.Context, userCodeHash string, now time.Time) (int, error)
	// TouchDevicePoll persists last_poll_at + poll_count + interval_seconds
	// + slow_down_count for a given device_code_hash. The service layer is
	// the source of truth for slow_down semantics; this is the persistence
	// hook so a poll bumped to slow_down keeps that interval across
	// processes/replicas. Idempotent and safe to call from any status.
	TouchDevicePoll(ctx context.Context, deviceCodeHash string, lastPollAt time.Time, pollCount int, intervalSeconds int, slowDownCount int) error
}

// OAuthAuthorizeTransactionRepository owns the v2 oauth_authorize_transactions
// row used as server-side authorize state. See §10.3.
type OAuthAuthorizeTransactionRepository interface {
	CreateAuthorizeTransaction(ctx context.Context, tx *OAuthAuthorizeTransaction) error
	// GetAuthorizeTransactionForUpdate locks the row by transaction_id and
	// returns it. Approve/deny call this before validating CSRF, then mark
	// consumed_at in the same tx.
	GetAuthorizeTransactionForUpdate(ctx context.Context, transactionID string, now time.Time) (*OAuthAuthorizeTransaction, error)
	MarkAuthorizeTransactionConsumed(ctx context.Context, transactionID string, now time.Time) error
	DeleteExpiredAuthorizeTransactions(ctx context.Context, before time.Time) (int, error)
}

// OAuthAPIKeyRepository extends APIKeyRepository with the batch-disable
// helper required by OAuth grant/family revocation. The returned plaintext
// keys are used to publish apikey:auth:<sha> invalidation messages so the
// gateway forgets the auth-cache entries without N+1 read/update loops.
//
// See §11.2.
type OAuthAPIKeyRepository interface {
	APIKeyRepository
	DisableAPIKeysByIDsReturningKeys(ctx context.Context, ids []int64, now time.Time) ([]string, error)
}

// ── Errors ──────────────────────────────────────────────────────────────────

// Sentinel errors are mapped to OAuth standard error codes by the handler.
var (
	ErrOAuthClientNotFound       = infraerrors.NotFound("INVALID_CLIENT", "oauth client not found")
	ErrOAuthClientDisabled       = infraerrors.Forbidden("INVALID_CLIENT", "oauth client disabled")
	ErrOAuthClientMisconfigured  = infraerrors.Forbidden("INVALID_CLIENT", "oauth client has neither pkce_required nor client_secret_hash configured")
	ErrOAuthClientAuthFailed     = infraerrors.Forbidden("INVALID_CLIENT", "client authentication failed")
	ErrOAuthInvalidRedirectURI   = infraerrors.BadRequest("INVALID_REQUEST", "redirect_uri does not match any registered uri")
	ErrOAuthInvalidScope         = infraerrors.BadRequest("INVALID_SCOPE", "requested scope is not allowed for this client")
	ErrOAuthMissingPKCE          = infraerrors.BadRequest("INVALID_REQUEST", "code_challenge is required for this client")
	ErrOAuthUnsupportedChallenge = infraerrors.BadRequest("INVALID_REQUEST", "code_challenge_method must be S256")
	ErrOAuthInvalidGrant         = infraerrors.BadRequest("INVALID_GRANT", "authorization grant is invalid")
	ErrOAuthCodeNotFound         = infraerrors.BadRequest("INVALID_GRANT", "authorization code not found")
	ErrOAuthCodeExpired          = infraerrors.BadRequest("INVALID_GRANT", "authorization code expired")
	ErrOAuthCodeAlreadyUsed      = infraerrors.BadRequest("INVALID_GRANT", "authorization code already used")
	ErrOAuthRedirectMismatch     = infraerrors.BadRequest("INVALID_GRANT", "redirect_uri does not match the value used in /authorize")
	ErrOAuthClientMismatch       = infraerrors.BadRequest("INVALID_GRANT", "client_id does not match the value used in /authorize")
	ErrOAuthPKCEFailed           = infraerrors.BadRequest("INVALID_GRANT", "code_verifier does not match code_challenge")
	ErrOAuthRefreshTokenNotFound = infraerrors.BadRequest("INVALID_GRANT", "refresh_token not found")
	ErrOAuthRefreshTokenRevoked  = infraerrors.BadRequest("INVALID_GRANT", "refresh_token has been revoked")
	ErrOAuthRefreshTokenExpired  = infraerrors.BadRequest("INVALID_GRANT", "refresh_token expired")
	ErrOAuthUnsupportedGrant     = infraerrors.BadRequest("UNSUPPORTED_GRANT_TYPE", "grant_type is not supported")
	ErrOAuthProviderDisabled     = infraerrors.Forbidden("OAUTH_PROVIDER_DISABLED", "oauth provider is disabled")

	// ── v2 ────────────────────────────────────────────────────────────────

	ErrOAuthAccessTokenNotFound          = infraerrors.NotFound("INVALID_TOKEN", "oauth access token metadata not found")
	ErrOAuthAccessTokenRevoked           = infraerrors.Unauthorized("INVALID_TOKEN", "oauth access token has been revoked")
	ErrOAuthAccessTokenExpired           = infraerrors.Unauthorized("INVALID_TOKEN", "oauth access token expired")
	ErrOAuthDeviceCodeNotFound           = infraerrors.BadRequest("INVALID_GRANT", "device code not found")
	ErrOAuthDeviceCodeExpired            = infraerrors.BadRequest("EXPIRED_TOKEN", "device code expired")
	ErrOAuthDeviceCodePending            = infraerrors.BadRequest("AUTHORIZATION_PENDING", "device code is pending user approval")
	ErrOAuthDeviceCodeSlowDown           = infraerrors.BadRequest("SLOW_DOWN", "polling too quickly; back off")
	ErrOAuthDeviceCodeAccessDenied       = infraerrors.BadRequest("ACCESS_DENIED", "user denied the device authorization request")
	ErrOAuthDeviceCodeAlreadyConsumed    = infraerrors.BadRequest("INVALID_GRANT", "device code has already been consumed")
	ErrOAuthAuthorizeTransactionNotFound = infraerrors.NotFound("INVALID_REQUEST", "authorize transaction not found or expired")
	ErrOAuthAuthorizeTransactionConsumed = infraerrors.BadRequest("INVALID_REQUEST", "authorize transaction has already been consumed")
	ErrOAuthAuthorizeCSRFMismatch        = infraerrors.BadRequest("INVALID_REQUEST", "csrf token mismatch")
)
