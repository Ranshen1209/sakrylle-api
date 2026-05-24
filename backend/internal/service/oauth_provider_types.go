package service

import (
	"context"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ── Domain types ────────────────────────────────────────────────────────────

// OAuthClient is a registered OAuth 2.0 client.
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
}

// OAuthCode is a short-lived authorization code (RFC 6749 §4.1).
//
// CodeHash is hex-encoded SHA-256 of the issued code; the plaintext code is
// only ever returned via the 302 to redirect_uri.
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
}

// OAuthRefreshToken records a refresh token bound to a specific access_token (api_keys row).
type OAuthRefreshToken struct {
	TokenHash     string
	ClientID      string
	UserID        int64
	APIKeyID      int64
	Scopes        []string
	ExpiresAt     time.Time
	RevokedAt     *time.Time
	RotatedToHash *string
}

// OAuthGrant is the user-facing aggregate view of an authorization: all
// active refresh tokens for one (user, client) pair collapsed into a single
// row, with the union of scopes and the freshest last_used timestamp.
type OAuthGrant struct {
	ClientID          string
	ClientName        string
	ClientDisabled    bool
	Scopes            []string
	FirstAuthorizedAt time.Time
	LastUsedAt        *time.Time
	ActiveTokenCount  int
}

// ── Repository interfaces ───────────────────────────────────────────────────

type OAuthClientRepository interface {
	GetClientByID(ctx context.Context, clientID string) (*OAuthClient, error)
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
)
