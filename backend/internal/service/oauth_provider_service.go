package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// IssuedAuthorizationCode is the value returned via the redirect_uri 302.
type IssuedAuthorizationCode struct {
	Code  string
	State string
}

// IssuedToken is the response body for /oauth/token.
type IssuedToken struct {
	AccessToken           string
	TokenType             string
	ExpiresIn             int
	RefreshToken          string
	RefreshTokenExpiresIn int
	Scope                 string
	GroupID               int64
	GroupName             string
	AdditionalTokens      []AdditionalToken
	// IDToken is a signed OIDC id_token, set only when the granted scopes
	// include `openid` and the OIDC signer is wired. Empty otherwise.
	IDToken string
}

type AdditionalToken struct {
	AccessToken string
	GroupID     int64
	GroupName   string
	ExpiresIn   int
}

// AuthorizeRequest is the validated form of /oauth/authorize query parameters.
type AuthorizeRequest struct {
	ClientID            string
	RedirectURI         string
	ResponseType        string
	Scopes              []string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	Nonce               string
	// Claims is the parsed OIDC §5.5 voluntary claims request.
	// nil when the RP did not send the claims parameter.
	Claims *ClaimsRequest
}

// BeginAuthorizeParams is the input to BeginAuthorizeTransaction.
//
// See §10.3 / §12.2: the validated authorize request is captured server-side
// before the consent page is rendered, so the approve POST cannot tamper with
// client_id / redirect_uri / scopes / PKCE / state.
type BeginAuthorizeParams struct {
	ClientID            string
	RedirectURI         string
	ResponseType        string
	Scopes              []string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	RequestedGroupID    *int64
	DeviceID            *string
	DeviceName          *string
	UserID              int64 // logged-in user; required so allowed_groups_snapshot is computed
	CreatedIP           *string
	CreatedUserAgent    *string
	Nonce               string // OIDC nonce captured from the authorize request
}

// BeginAuthorizeResult carries the rendering payload + plaintext CSRF token
// for the consent page. The CSRF plaintext is returned ONCE; the table only
// stores SHA-256 hex.
type BeginAuthorizeResult struct {
	Transaction          *OAuthAuthorizeTransaction
	CSRFTokenPlaintext   string
	Client               *OAuthClient
	AllowedGroupsForUser []OAuthAllowedGroup
}

// ApproveAuthorizationResult is what /api/v1/oauth/authorize/approve returns.
type ApproveAuthorizationResult struct {
	RedirectURI string
	Code        string
	State       string
	GroupID     int64
}

var (
	ErrOAuthInvalidResponseType = infraerrors.BadRequest("UNSUPPORTED_RESPONSE_TYPE", "response_type must be code")
	ErrOAuthMissingState        = infraerrors.BadRequest("INVALID_REQUEST", "state is required")
	ErrOAuthGroupNotConfigured  = infraerrors.BadRequest("INVALID_CLIENT", "no default group bound to this client; set default_group_id or oauth_default_group_id setting")
	ErrOAuthGroupNotAllowed     = infraerrors.BadRequest("INVALID_GRANT", "group not allowed for this grant")
	ErrOAuthInvalidGroup        = infraerrors.BadRequest("INVALID_REQUEST", "no eligible group for user")
	ErrOAuthRefreshReuse        = infraerrors.BadRequest("INVALID_GRANT", "refresh token reuse detected")
	ErrOAuthAccessDenied        = infraerrors.Forbidden("ACCESS_DENIED", "user denied authorization")
	ErrOAuthSubjectMismatch     = infraerrors.Forbidden("UNAUTHORIZED_CLIENT", "authenticated subject does not match transaction owner")
	ErrOAuthPKCEFormat          = infraerrors.BadRequest("INVALID_REQUEST", "code_challenge format invalid")
)

const (
	authCodeTTL              = 10 * time.Minute
	authorizeTransactionTTL  = 10 * time.Minute
	oauthAccessTokenPrefix   = "sk_oauth_"
	oauthRefreshTokenPrefix  = "rt_"
	defaultAccessTokenSecret = 32
	defaultRefreshSecret     = 32
	csrfTokenBytes           = 32
	transactionIDBytes       = 32
	pkceMinLength            = 43
	pkceMaxLength            = 128
	lastUsedTouchInterval    = 60 * time.Second
)

// OAuthProviderService implements OAuth 2.0 Authorization Code + PKCE issuance.
//
// Issued access_tokens live as rows in api_keys with prefix sk_oauth_ and a
// short expires_at, so the existing /v1/* middleware, billing pipeline,
// rate-limiting, and Redis cache invalidation work unchanged.
type OAuthProviderService struct {
	clientRepo  OAuthClientRepository
	codeRepo    OAuthCodeRepository
	refreshRepo OAuthRefreshTokenRepository
	accessRepo  OAuthAccessTokenRepository
	deviceRepo  OAuthDeviceCodeRepository
	authzTxRepo OAuthAuthorizeTransactionRepository
	apiKeyRepo  APIKeyRepository
	oauthAPIKey OAuthAPIKeyRepository // batch disable; nil-safe
	// mintRepo is the FIX H4 atomic-write surface for the api_keys +
	// oauth_access_tokens + oauth_refresh_tokens trio. When non-nil it is
	// used for both mintTokensFromCode and RefreshAccessToken; when nil
	// (legacy tests) the service falls back to per-row writes.
	mintRepo    OAuthTokenMintRepository
	groupRepo   GroupRepository
	groupAccess GroupAccessPolicy
	settingRepo SettingRepository
	// authCache is the auth-cache invalidator (Redis Pub/Sub). Typed as an
	// interface (not *APIKeyService) so tests can inject a spy and verify
	// the 60-second `apikey:auth:<sha>` cache is busted on every revocation.
	authCache APIKeyAuthCacheInvalidator

	// lastUsedThrottle tracks per-api_key_id "next-allowed touch" timestamps so
	// TouchAccessTokenLastUsed can short-circuit at 60s granularity without a
	// DB hit. See §11.6.
	lastUsedThrottle sync.Map // map[int64]time.Time

	// OIDC id_token issuance. All three are nil unless WithOIDC is called.
	// When nil, no id_token is issued (the OAuth flows are unaffected). oidcSign
	// is algorithm-aware: maybeSignIDToken selects RS256 or ES256 per client.
	oidcSign   func(claims jwt.MapClaims, alg SigningAlgorithm) (string, error)
	oidcIssuer func(ctx context.Context) string
	oidcUser   func(ctx context.Context, userID int64) (OIDCUserClaims, error)
}

// NewOAuthProviderService is the v2 constructor. See §11.2.
//
// All repo dependencies must be non-nil for production wiring; tests may pass
// nils for unused branches (e.g. accessRepo when only the legacy refresh path
// is exercised), but a production server depends on all of them.
func NewOAuthProviderService(
	clientRepo OAuthClientRepository,
	codeRepo OAuthCodeRepository,
	refreshRepo OAuthRefreshTokenRepository,
	accessRepo OAuthAccessTokenRepository,
	deviceRepo OAuthDeviceCodeRepository,
	authzTxRepo OAuthAuthorizeTransactionRepository,
	apiKeyRepo OAuthAPIKeyRepository,
	groupRepo GroupRepository,
	groupAccess GroupAccessPolicy,
	settingRepo SettingRepository,
	authCache APIKeyAuthCacheInvalidator,
) *OAuthProviderService {
	var (
		baseAPIKey  APIKeyRepository = apiKeyRepo
		oauthAPIKey OAuthAPIKeyRepository
	)
	if apiKeyRepo != nil {
		oauthAPIKey = apiKeyRepo
	}
	return &OAuthProviderService{
		clientRepo:  clientRepo,
		codeRepo:    codeRepo,
		refreshRepo: refreshRepo,
		accessRepo:  accessRepo,
		deviceRepo:  deviceRepo,
		authzTxRepo: authzTxRepo,
		apiKeyRepo:  baseAPIKey,
		oauthAPIKey: oauthAPIKey,
		groupRepo:   groupRepo,
		groupAccess: groupAccess,
		settingRepo: settingRepo,
		authCache:   authCache,
	}
}

// WithTokenMintRepo wires the FIX H4 atomic-mint repo into an existing
// service instance. Production wiring (cmd/server) calls this once during
// startup; tests that need to exercise the atomic path can do the same.
// Calling with nil is a no-op (preserves the legacy non-atomic mint path).
func (s *OAuthProviderService) WithTokenMintRepo(repo OAuthTokenMintRepository) *OAuthProviderService {
	if s != nil {
		s.mintRepo = repo
	}
	return s
}

// WithOIDC wires OIDC id_token issuance into the service.
//
//   - sign signs a claim set with the requested algorithm (e.g. OIDCKeyService.Sign).
//     maybeSignIDToken picks RS256 or ES256 per the client's signing_algorithm.
//   - issuer resolves the fixed provider issuer (https://sub.sakrylle.com).
//   - userClaims fetches identity fields for profile/email claims by user id.
//
// When this is not called, no id_token is ever issued and the OAuth flows are
// completely unaffected.
func (s *OAuthProviderService) WithOIDC(
	sign func(claims jwt.MapClaims, alg SigningAlgorithm) (string, error),
	issuer func(ctx context.Context) string,
	userClaims func(ctx context.Context, userID int64) (OIDCUserClaims, error),
) *OAuthProviderService {
	if s != nil {
		s.oidcSign = sign
		s.oidcIssuer = issuer
		s.oidcUser = userClaims
	}
	return s
}

// resolveSigningAlgorithm maps a client's stored signing_algorithm to a
// supported SigningAlgorithm, defaulting to RS256. The DB CHECK constraint
// (migration 151) already restricts the column to RS256/ES256, but the service
// fails safe to RS256 for empty or unrecognised values (e.g. a legacy row
// predating the column, or a future value not yet supported at runtime) and
// logs the fallback so an operator can spot a misconfiguration.
func resolveSigningAlgorithm(raw string) SigningAlgorithm {
	switch SigningAlgorithm(raw) {
	case SigningAlgRS256:
		return SigningAlgRS256
	case SigningAlgES256:
		return SigningAlgES256
	case "":
		return SigningAlgRS256
	default:
		slog.Warn("oidc: unrecognised client signing_algorithm; defaulting to RS256", "signing_algorithm", raw)
		return SigningAlgRS256
	}
}

// maybeSignIDToken returns a signed id_token when (and only when) the granted
// scopes include `openid` and the OIDC signer is wired.
//
// Fail-closed: if OIDC is wired and `openid` was granted but the issuer is
// unresolved or signing fails, it returns an error so the token request fails
// loudly rather than emitting a malformed identity assertion. When OIDC is not
// wired at all (feature off), it returns ("", nil) and the OAuth flow proceeds.
func (s *OAuthProviderService) maybeSignIDToken(ctx context.Context, client *OAuthClient, userID int64, scopes []string, nonce string, authTime time.Time) (string, error) {
	if s.oidcSign == nil || s.oidcIssuer == nil || !HasScope(scopes, ScopeOpenID) {
		return "", nil
	}
	issuer := s.oidcIssuer(ctx)
	if issuer == "" {
		return "", fmt.Errorf("oidc: issuer unresolved; cannot sign id_token")
	}
	// Compute pairwise sub when the client requires it (OIDC Core §8).
	var pairwiseSub string
	if client != nil && client.SubjectType == "pairwise" {
		pairwiseSub = ResolvePairwiseSub(issuer, userID, client.SubjectType, client.SectorIdentifierURI, client.RedirectURIs)
	}
	// Claim scopes drive which identity claims we promise. If we cannot load
	// the user (MEDIUM-1), do NOT emit a token that advertises profile/email
	// scope but carries no name/email claim — a strict RP would treat that as
	// a provider defect. Instead log and strip those scopes so the id_token
	// honestly reflects what it contains.
	claimScopes := scopes
	u := OIDCUserClaims{UserID: userID}
	if s.oidcUser != nil && HasAnyScope(scopes, ScopeProfile, ScopeEmail) {
		got, err := s.oidcUser(ctx, userID)
		if err != nil {
			slog.Warn("oidc: user claim lookup failed; stripping profile/email from id_token",
				"user_id", userID, "client_id", client.ClientID, "err", err)
			claimScopes = stripScopes(scopes, ScopeProfile, ScopeEmail)
		} else {
			got.UserID = userID
			u = got
		}
	}
	claims, err := BuildIDTokenClaims(issuer, client.ClientID, u, claimScopes, nonce, authTime, time.Now(), DefaultOIDCIDTokenTTL, pairwiseSub)
	if err != nil {
		return "", fmt.Errorf("oidc: build id_token claims: %w", err)
	}
	return s.oidcSign(claims, resolveSigningAlgorithm(client.SigningAlgorithm))
}

// IsEnabled reports whether the OAuth provider is enabled in DB settings.
//
// Fails closed on read errors and missing/unparseable values: when the
// settings row cannot be loaded we cannot prove the provider is supposed to
// be on, so we refuse to issue tokens. Operators must explicitly set
// `oauth_provider_enabled=true` (truthy) — the migration seeds this on
// fresh installs.
func (s *OAuthProviderService) IsEnabled(ctx context.Context) bool {
	if s.settingRepo == nil {
		return false
	}
	value, err := s.settingRepo.GetValue(ctx, "oauth_provider_enabled")
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "on", "yes":
		return true
	default:
		return false
	}
}

// IsScopeEnforcementEnabled reports whether v2 scope enforcement is on.
// Defaults to false when missing/unparseable so the migration's seeded
// false stays effective on a fresh install.
//
// See §10.7. The kill-switch lives in the settings table so direct SQL can
// flip it without restarting the server.
func (s *OAuthProviderService) IsScopeEnforcementEnabled(ctx context.Context) bool {
	if s.settingRepo == nil {
		return false
	}
	value, err := s.settingRepo.GetValue(ctx, "oauth_scope_enforcement_enabled")
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "on", "yes":
		return true
	default:
		return false
	}
}

// AllowedClientOrigins returns the deduped, sorted set of browser origins
// (scheme://host[:port]) collected from every enabled OAuth client's
// redirect_uris. See parseBrowserOrigin for details.
func (s *OAuthProviderService) AllowedClientOrigins(ctx context.Context) ([]string, error) {
	uris, err := s.clientRepo.ListEnabledRedirectURIs(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(uris))
	for _, raw := range uris {
		origin, ok := parseBrowserOrigin(raw)
		if !ok {
			continue
		}
		seen[origin] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for origin := range seen {
		out = append(out, origin)
	}
	sort.Strings(out)
	return out, nil
}

// parseBrowserOrigin extracts the CORS-style scheme://host[:port] tuple from
// an HTTP(S) redirect_uri, returning ok=false for any URI that cannot match a
// browser Origin header (non-HTTP scheme, malformed, host-less).
func parseBrowserOrigin(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	if u.Host == "" {
		return "", false
	}
	return scheme + "://" + u.Host, true
}

// LookupClient resolves a client_id, returning ErrOAuthClientDisabled when
// disabled and ErrOAuthClientMisconfigured when neither PKCE nor a secret is
// configured.
func (s *OAuthProviderService) LookupClient(ctx context.Context, clientID string) (*OAuthClient, error) {
	if strings.TrimSpace(clientID) == "" {
		return nil, ErrOAuthClientNotFound
	}
	client, err := s.clientRepo.GetClientByID(ctx, clientID)
	if err != nil {
		return nil, err
	}
	if client.Disabled {
		return nil, ErrOAuthClientDisabled
	}
	if !client.PKCERequired && client.ClientSecretHash == "" {
		// Misconfigured client: would allow code → token exchange with no auth.
		// Treat as INVALID_CLIENT so admins notice during onboarding.
		return nil, ErrOAuthClientMisconfigured
	}
	return client, nil
}

// authenticateClient verifies client credentials at the /oauth/token endpoint.
//
//	PKCE-only client → no further check; PKCE verifier validation happens later.
//	Confidential client → bcrypt(client_secret) must match client_secret_hash.
func (s *OAuthProviderService) authenticateClient(client *OAuthClient, clientSecret string) error {
	if client.ClientSecretHash == "" {
		return nil
	}
	if strings.TrimSpace(clientSecret) == "" {
		return ErrOAuthClientAuthFailed
	}
	if err := bcrypt.CompareHashAndPassword([]byte(client.ClientSecretHash), []byte(clientSecret)); err != nil {
		return ErrOAuthClientAuthFailed
	}
	return nil
}

// ValidateAuthorizeRequest enforces every check from §12.2.
//
// PKCE is now mandatory for ALL clients (public and confidential per §10.1) —
// we still tolerate legacy confidential clients that didn't set PKCERequired
// for backward compatibility, but the v2 contract requires it.
func (s *OAuthProviderService) ValidateAuthorizeRequest(ctx context.Context, req *AuthorizeRequest) (*OAuthClient, error) {
	if req.ResponseType != "code" {
		return nil, ErrOAuthInvalidResponseType
	}
	if strings.TrimSpace(req.State) == "" {
		return nil, ErrOAuthMissingState
	}
	client, err := s.LookupClient(ctx, req.ClientID)
	if err != nil {
		return nil, err
	}
	if !redirectURIAllowed(client.RedirectURIs, req.RedirectURI) {
		return nil, ErrOAuthInvalidRedirectURI
	}
	requestedScopes := NormalizeScopes(req.Scopes)
	if len(requestedScopes) == 0 {
		requestedScopes = NormalizeScopes(client.DefaultScopes)
	}
	if !ScopeAllowed(client.AllowedScopes, requestedScopes) {
		return nil, ErrOAuthInvalidScope
	}
	if client.PKCERequired {
		if strings.TrimSpace(req.CodeChallenge) == "" {
			return nil, ErrOAuthMissingPKCE
		}
		if req.CodeChallengeMethod != "S256" {
			return nil, ErrOAuthUnsupportedChallenge
		}
		if !validatePKCEChallenge(req.CodeChallenge) {
			return nil, ErrOAuthPKCEFormat
		}
	}
	return client, nil
}

// IssueAuthorizationCode generates a one-shot code (10 min TTL) for the given user.
//
// The plaintext code is returned only here; the DB stores hex(sha256(code)).
//
// Note: the v2 atomic-write path is ApproveAuthorization; this legacy entry
// point is kept for tests and the v1 PKCE flow that doesn't need
// allowed_groups_snapshot.
func (s *OAuthProviderService) IssueAuthorizationCode(
	ctx context.Context,
	client *OAuthClient,
	userID int64,
	req *AuthorizeRequest,
) (*IssuedAuthorizationCode, error) {
	codePlain, err := generateOpaqueToken(32)
	if err != nil {
		return nil, fmt.Errorf("generate auth code: %w", err)
	}
	now := time.Now()
	if err := s.codeRepo.CreateCode(ctx, &OAuthCode{
		CodeHash:            hashOAuthToken(codePlain),
		ClientID:            client.ClientID,
		UserID:              userID,
		RedirectURI:         req.RedirectURI,
		Scopes:              NormalizeScopes(req.Scopes),
		CodeChallenge:       req.CodeChallenge,
		CodeChallengeMethod: req.CodeChallengeMethod,
		ExpiresAt:           now.Add(authCodeTTL),
	}); err != nil {
		return nil, fmt.Errorf("persist auth code: %w", err)
	}
	return &IssuedAuthorizationCode{Code: codePlain, State: req.State}, nil
}

// CreateAuthorizationCode persists a pre-built authorization code.
//
// Used by prompt=none silent authentication to issue codes without the full
// BeginAuthorize/ApproveAuthorization transaction flow. The caller is
// responsible for validating all fields (client_id, user_id, redirect_uri,
// scopes, PKCE, expiry) before calling this method.
func (s *OAuthProviderService) CreateAuthorizationCode(ctx context.Context, code *OAuthCode) error {
	if s.codeRepo == nil {
		return fmt.Errorf("code repository not configured")
	}
	return s.codeRepo.CreateCode(ctx, code)
}

// ResolveOAuthGroup implements §9 group resolution strictly:
//
//  1. If requestedGroupID is non-nil, validate user can access + client allows + group enabled.
//  2. Else if client.DefaultGroupID is set, validate user can access + group enabled.
//  3. Else return ErrOAuthInvalidGroup.
//
// Crucially, the global `oauth_default_group_id` setting is NOT consulted for
// v2 — it remains a v1 fallback only and v2 must fail closed.
func (s *OAuthProviderService) ResolveOAuthGroup(
	ctx context.Context,
	userID int64,
	client *OAuthClient,
	requestedGroupID *int64,
) (int64, error) {
	if client == nil {
		return 0, ErrOAuthClientNotFound
	}
	var candidate int64
	if requestedGroupID != nil && *requestedGroupID > 0 {
		candidate = *requestedGroupID
	} else if client.DefaultGroupID != nil && *client.DefaultGroupID > 0 {
		candidate = *client.DefaultGroupID
	} else {
		return 0, ErrOAuthInvalidGroup
	}
	if !clientAllowsGroup(client, candidate) {
		return 0, ErrOAuthGroupNotAllowed
	}
	if s.groupRepo != nil {
		group, err := s.groupRepo.GetByID(ctx, candidate)
		if err != nil {
			return 0, fmt.Errorf("%w: group %d not found", ErrOAuthInvalidGroup, candidate)
		}
		if !group.IsActive() {
			return 0, fmt.Errorf("%w: group %d disabled", ErrOAuthInvalidGroup, candidate)
		}
	}
	if s.groupAccess != nil {
		ok, err := s.groupAccess.CanUserUseGroup(ctx, userID, candidate)
		if err != nil {
			return 0, fmt.Errorf("group access policy: %w", err)
		}
		if !ok {
			return 0, ErrOAuthGroupNotAllowed
		}
	}
	return candidate, nil
}

func clientAllowsGroup(client *OAuthClient, groupID int64) bool {
	if client.AllowedGroupIDs == nil {
		return true // nil means no client-level restriction
	}
	for _, id := range client.AllowedGroupIDs {
		if id == groupID {
			return true
		}
	}
	return false
}

// BeginAuthorizeTransaction validates the /authorize request, computes the
// allowed_groups snapshot for the logged-in user, persists a server-side
// transaction with a SHA-256 hash of the CSRF token, and returns the plaintext
// CSRF token + transaction ID for the consent page.
//
// See §10.3 / §12.2: the approve POST may submit only transaction_id +
// csrf_token + decision + group_id, never client_id / redirect_uri / scopes /
// PKCE. All those fields come from this transaction row on the approve path.
func (s *OAuthProviderService) BeginAuthorizeTransaction(
	ctx context.Context,
	params *BeginAuthorizeParams,
) (*BeginAuthorizeResult, error) {
	if params == nil {
		return nil, ErrOAuthInvalidGrant
	}
	if params.UserID <= 0 {
		return nil, ErrOAuthSubjectMismatch
	}
	authReq := &AuthorizeRequest{
		ClientID:            params.ClientID,
		RedirectURI:         params.RedirectURI,
		ResponseType:        params.ResponseType,
		Scopes:              params.Scopes,
		State:               params.State,
		CodeChallenge:       params.CodeChallenge,
		CodeChallengeMethod: params.CodeChallengeMethod,
	}
	client, err := s.ValidateAuthorizeRequest(ctx, authReq)
	if err != nil {
		return nil, err
	}
	scopes := NormalizeScopes(params.Scopes)
	if len(scopes) == 0 {
		scopes = NormalizeScopes(client.DefaultScopes)
	}

	// Compute allowed_groups_snapshot using the same policy /v1/me uses.
	var allowedGroups []OAuthAllowedGroup
	if s.groupAccess != nil {
		allowedGroups, err = s.groupAccess.ListUserAllowedGroupsForOAuth(ctx, params.UserID, client, scopes)
		if err != nil {
			return nil, err
		}
	}
	allowed := oauthAllowedGroupIDs(allowedGroups)
	allowed = filterAllowedByClient(client, allowed)
	allowedGroups = filterAllowedGroupsByIDs(allowedGroups, allowed)

	// Pre-validate requestedGroupID is at least in the snapshot when supplied.
	// Final ResolveOAuthGroup happens at approval time.
	if params.RequestedGroupID != nil && *params.RequestedGroupID > 0 {
		if !int64InSlice(allowed, *params.RequestedGroupID) {
			return nil, ErrOAuthGroupNotAllowed
		}
	}

	csrfPlain, err := generateOpaqueToken(csrfTokenBytes)
	if err != nil {
		return nil, fmt.Errorf("generate csrf token: %w", err)
	}
	txID, err := generateOpaqueToken(transactionIDBytes)
	if err != nil {
		return nil, fmt.Errorf("generate transaction id: %w", err)
	}
	now := time.Now()
	tx := &OAuthAuthorizeTransaction{
		TransactionID:         txID,
		CSRFHash:              hashOAuthToken(csrfPlain),
		UserID:                params.UserID,
		ClientID:              client.ClientID,
		RedirectURI:           params.RedirectURI,
		ResponseType:          authReq.ResponseType,
		Scopes:                scopes,
		AllowedGroupsSnapshot: allowed,
		State:                 params.State,
		CodeChallenge:         params.CodeChallenge,
		CodeChallengeMethod:   params.CodeChallengeMethod,
		RequestedGroupID:      params.RequestedGroupID,
		DeviceID:              params.DeviceID,
		DeviceName:            params.DeviceName,
		ExpiresAt:             now.Add(authorizeTransactionTTL),
		CreatedIP:             params.CreatedIP,
		CreatedUserAgent:      params.CreatedUserAgent,
		Nonce:                 params.Nonce,
	}
	if err := s.authzTxRepo.CreateAuthorizeTransaction(ctx, tx); err != nil {
		return nil, fmt.Errorf("create authorize transaction: %w", err)
	}
	return &BeginAuthorizeResult{
		Transaction:          tx,
		CSRFTokenPlaintext:   csrfPlain,
		Client:               client,
		AllowedGroupsForUser: allowedGroups,
	}, nil
}

// LoadAuthorizeTransactionForApproval rehydrates a transaction by ID,
// validates CSRF + JWT subject, and confirms it is unconsumed/unexpired.
//
// userIDFromJWT binds the transaction to the currently logged-in user — even
// if the cookie is replayed by another session, we refuse to surface the
// transaction.
//
// FIX C1 (§18.7): the transaction row stores the user_id captured at
// /authorize time. This method rejects with ErrOAuthSubjectMismatch when the
// JWT subject differs — that's a real attack signal (CSRF / IDOR replay) and
// is logged at warn so observability picks it up.
func (s *OAuthProviderService) LoadAuthorizeTransactionForApproval(
	ctx context.Context,
	txID, csrfToken string,
	userIDFromJWT int64,
) (*OAuthAuthorizeTransaction, *OAuthClient, error) {
	if strings.TrimSpace(txID) == "" {
		return nil, nil, ErrOAuthAuthorizeTransactionNotFound
	}
	if userIDFromJWT <= 0 {
		return nil, nil, ErrOAuthSubjectMismatch
	}
	tx, err := s.authzTxRepo.GetAuthorizeTransactionForApproval(ctx, txID, time.Now())
	if err != nil {
		return nil, nil, err
	}
	if tx.ConsumedAt != nil {
		return nil, nil, ErrOAuthAuthorizeTransactionConsumed
	}
	if subtle.ConstantTimeCompare([]byte(hashOAuthToken(csrfToken)), []byte(tx.CSRFHash)) != 1 {
		return nil, nil, ErrOAuthAuthorizeCSRFMismatch
	}
	// FIX C1: subject binding. The transaction was created against a
	// specific authenticated user; an approve POST from a different JWT
	// subject (even with a stolen CSRF cookie) must be rejected. Constant-
	// time compare not needed for int64 equality, but log loudly so SOC
	// alerts on the divergence.
	//
	// FIX A3 (zero-sentinel): we do NOT exempt rows where tx.UserID == 0.
	// BeginAuthorizeTransaction now rejects params.UserID <= 0, so the only
	// way a zero-user row reaches us is the migration-146 backfill of
	// pre-existing legacy rows. Those expire within the 10-minute
	// authorizeTransactionTTL window, so after that window any zero-user row
	// is suspect — fail closed. Pre-A3 this branch was open ("if tx.UserID
	// > 0 && ...") which would silently accept any JWT subject on legacy
	// rows; that's a fail-open hole.
	if tx.UserID != userIDFromJWT {
		slog.Warn("oauth: authorize transaction subject mismatch",
			"transaction_id", tx.TransactionID,
			"client_id", tx.ClientID,
			"transaction_user_id", tx.UserID,
			"jwt_user_id", userIDFromJWT,
		)
		return nil, nil, ErrOAuthSubjectMismatch
	}
	client, err := s.LookupClient(ctx, tx.ClientID)
	if err != nil {
		return nil, nil, err
	}
	return tx, client, nil
}

// ApproveAuthorization consumes a pending /authorize transaction and mints an
// authorization code bound to the resolved group.
//
// userIDFromJWT is the AUTHORITATIVE subject identity for this approval —
// taken from the consent-page JWT cookie/header at the handler boundary.
// LoadAuthorizeTransactionForApproval enforces that this matches the user_id
// captured when the transaction was first opened (FIX C1 §18.7). Downstream
// code mint, grant ID issuance, and group resolution all use this value.
//
// finalGroupID may override the transaction's RequestedGroupID (the user can
// switch group on the consent page); both must pass ResolveOAuthGroup against
// the current user's permissions AND be in the snapshot stored on the
// transaction.
//
// FIX C2: the consume-then-issue pair runs inside a single repository
// transaction so a code-insert failure leaves the authorize transaction
// unconsumed and re-runnable. See OAuthAtomicMintRepository.
func (s *OAuthProviderService) ApproveAuthorization(
	ctx context.Context,
	txID, csrfToken string,
	userIDFromJWT int64,
	finalGroupID *int64,
	groupIDs []int64,
) (*ApproveAuthorizationResult, error) {
	if userIDFromJWT <= 0 {
		return nil, ErrOAuthSubjectMismatch
	}
	tx, client, err := s.LoadAuthorizeTransactionForApproval(ctx, txID, csrfToken, userIDFromJWT)
	if err != nil {
		return nil, err
	}

	// Multi-group: validate each selected group against the snapshot.
	// Use the first as the primary group for the authorization code.
	var selectedGroups []int64
	if len(groupIDs) > 0 {
		for _, gid := range groupIDs {
			if !int64InSlice(tx.AllowedGroupsSnapshot, gid) {
				return nil, ErrOAuthGroupNotAllowed
			}
		}
		selectedGroups = groupIDs
		first := groupIDs[0]
		finalGroupID = &first
	}

	requested := tx.RequestedGroupID
	if finalGroupID != nil && *finalGroupID > 0 {
		requested = finalGroupID
	}
	if requested != nil && *requested > 0 {
		if !int64InSlice(tx.AllowedGroupsSnapshot, *requested) {
			return nil, ErrOAuthGroupNotAllowed
		}
	}
	resolvedGroup, err := s.ResolveOAuthGroup(ctx, userIDFromJWT, client, requested)
	if err != nil {
		return nil, err
	}

	// FIX C2: consume-and-issue must be atomic. Without a shared tx, a code
	// insert failure (FK violation, conn drop, etc.) leaves the authorize
	// row consumed but no code emitted, burning the user's consent. The
	// repo's ConsumeAuthorizeTransactionAndIssueCode wraps both writes in
	// one Postgres tx with a row lock on the transaction.
	now := time.Now()
	codePlain, err := generateOpaqueToken(32)
	if err != nil {
		return nil, fmt.Errorf("generate authorization code: %w", err)
	}
	grantID := uuid.NewString()
	code := &OAuthCode{
		CodeHash:              hashOAuthToken(codePlain),
		ClientID:              client.ClientID,
		UserID:                userIDFromJWT,
		RedirectURI:           tx.RedirectURI,
		Scopes:                tx.Scopes,
		CodeChallenge:         tx.CodeChallenge,
		CodeChallengeMethod:   tx.CodeChallengeMethod,
		ExpiresAt:             now.Add(authCodeTTL),
		GroupID:               &resolvedGroup,
		GrantID:               &grantID,
		AllowedGroupsSnapshot: selectedGroupsOrDefault(selectedGroups, tx.AllowedGroupsSnapshot),
		DeviceID:              tx.DeviceID,
		DeviceName:            tx.DeviceName,
		Nonce:                 tx.Nonce,
		CreatedAt:             now,
	}
	if atomic, ok := s.authzTxRepo.(OAuthAuthorizeAtomicRepository); ok {
		// FIX A4: pass userIDFromJWT so the atomic consume re-checks the
		// subject inside the row lock. LoadAuthorizeTransactionForApproval
		// already checked it, but its lock is released before we get here;
		// without this re-check there is a (theoretical) TOCTOU window
		// where row.user_id could mutate between the two reads.
		if err := atomic.ConsumeAuthorizeTransactionAndIssueCode(ctx, txID, userIDFromJWT, now, code); err != nil {
			return nil, err
		}
	} else {
		// Legacy / test fallback: best-effort sequence. Marks consumed first
		// to preserve the v1 single-mint guarantee — atomic repo path is the
		// production fix.
		if err := s.authzTxRepo.MarkAuthorizeTransactionConsumed(ctx, txID, now); err != nil {
			return nil, fmt.Errorf("mark authorize transaction consumed: %w", err)
		}
		if err := s.codeRepo.CreateCode(ctx, code); err != nil {
			return nil, fmt.Errorf("persist authorization code: %w", err)
		}
	}
	return &ApproveAuthorizationResult{
		RedirectURI: tx.RedirectURI,
		Code:        codePlain,
		State:       tx.State,
		GroupID:     resolvedGroup,
	}, nil
}

// DenyAuthorization marks the transaction consumed and signals the consent
// page to redirect with `error=access_denied`.
func (s *OAuthProviderService) DenyAuthorization(
	ctx context.Context,
	txID, csrfToken string,
	userID int64,
) (*OAuthAuthorizeTransaction, error) {
	tx, _, err := s.LoadAuthorizeTransactionForApproval(ctx, txID, csrfToken, userID)
	if err != nil {
		return nil, err
	}
	if err := s.authzTxRepo.MarkAuthorizeTransactionConsumed(ctx, txID, time.Now()); err != nil {
		return nil, fmt.Errorf("mark authorize transaction consumed: %w", err)
	}
	return tx, nil
}

// ExchangeAuthorizationCode handles /oauth/token grant_type=authorization_code.
//
// clientSecret is optional for PKCE-only clients (public clients) but required
// when the client has a client_secret_hash on file.
func (s *OAuthProviderService) ExchangeAuthorizationCode(
	ctx context.Context,
	clientID, clientSecret, codePlain, redirectURI, codeVerifier string,
) (*IssuedToken, error) {
	if strings.TrimSpace(codePlain) == "" {
		return nil, ErrOAuthCodeNotFound
	}
	client, err := s.LookupClient(ctx, clientID)
	if err != nil {
		return nil, err
	}
	if err := s.authenticateClient(client, clientSecret); err != nil {
		return nil, err
	}
	now := time.Now()
	code, err := s.codeRepo.ConsumeCode(ctx, hashOAuthToken(codePlain), now)
	if err != nil {
		// RFC 6749 §10.5: a replayed code SHOULD trigger revocation of all
		// tokens previously derived from that code. The repo returns the
		// loaded row even on ErrOAuthCodeAlreadyUsed.
		if errors.Is(err, ErrOAuthCodeAlreadyUsed) && code != nil {
			if code.GrantID != nil && *code.GrantID != "" {
				_ = s.revokeGrantInternal(ctx, *code.GrantID)
			} else {
				_, _ = s.RevokeUserGrant(ctx, code.UserID, code.ClientID, now)
			}
		}
		return nil, err
	}
	if code.ClientID != client.ClientID {
		return nil, ErrOAuthClientMismatch
	}
	if code.RedirectURI != redirectURI {
		return nil, ErrOAuthRedirectMismatch
	}
	if client.PKCERequired || code.CodeChallenge != "" {
		if !verifyPKCES256(code.CodeChallenge, codeVerifier) {
			return nil, ErrOAuthPKCEFailed
		}
	}
	return s.mintTokensFromCode(ctx, client, code)
}

// RefreshAccessToken handles /oauth/token grant_type=refresh_token with the
// full v2 contract: replay detection, family revocation, group-switch
// validation against the refresh row's allowed_groups_snapshot, and absolute
// expiry inheritance from the original family-anchored expires_at.
func (s *OAuthProviderService) RefreshAccessToken(
	ctx context.Context,
	clientID, clientSecret, refreshTokenPlain string,
	requestedGroupID *int64,
) (*IssuedToken, error) {
	if strings.TrimSpace(refreshTokenPlain) == "" {
		return nil, ErrOAuthRefreshTokenNotFound
	}
	client, err := s.LookupClient(ctx, clientID)
	if err != nil {
		return nil, err
	}
	if err := s.authenticateClient(client, clientSecret); err != nil {
		return nil, err
	}
	now := time.Now()
	tokenHash := hashOAuthToken(refreshTokenPlain)

	// §11.5 / §12.5: replay detection comes BEFORE the ordinary
	// revoked-without-rotation rejection, because the row stores
	// rotated_to_hash exactly to make this distinction visible.
	row, err := s.refreshRepo.GetRefreshTokenByHashForUpdate(ctx, tokenHash, now)
	if err != nil {
		return nil, err
	}
	if row.ClientID != client.ClientID {
		// §12.5: do not reveal whether the row exists for a different client.
		return nil, ErrOAuthInvalidGrant
	}
	if row.RevokedAt != nil && row.RotatedToHash != nil {
		// Reuse detected — the same plaintext was already used to mint a new pair.
		s.handleRefreshReuse(ctx, row)
		return nil, ErrOAuthRefreshReuse
	}
	if row.RevokedAt != nil {
		return nil, ErrOAuthRefreshTokenRevoked
	}
	if !row.ExpiresAt.After(now) {
		return nil, ErrOAuthRefreshTokenExpired
	}

	// Group resolution: must stay within the snapshot stored on this refresh row.
	var targetGroup int64
	switch {
	case requestedGroupID != nil && *requestedGroupID > 0:
		if len(row.AllowedGroupsSnapshot) > 0 && !int64InSlice(row.AllowedGroupsSnapshot, *requestedGroupID) {
			return nil, ErrOAuthGroupNotAllowed
		}
		if !clientAllowsGroup(client, *requestedGroupID) {
			return nil, ErrOAuthGroupNotAllowed
		}
		if s.groupRepo != nil {
			g, gerr := s.groupRepo.GetByID(ctx, *requestedGroupID)
			if gerr != nil || !g.IsActive() {
				return nil, ErrOAuthGroupNotAllowed
			}
		}
		targetGroup = *requestedGroupID
	case row.GroupID != nil && *row.GroupID > 0:
		targetGroup = *row.GroupID
	default:
		// Legacy refresh row without group binding — fall back to client default
		// only if user can use it. Otherwise the client must reauthorize.
		resolved, rerr := s.ResolveOAuthGroup(ctx, row.UserID, client, nil)
		if rerr != nil {
			return nil, rerr
		}
		targetGroup = resolved
	}

	// Mint replacement keys.
	newAccessKey := oauthAccessTokenPrefix + mustOpaqueToken(defaultAccessTokenSecret)
	newRefreshKey := oauthRefreshTokenPrefix + mustOpaqueToken(defaultRefreshSecret)
	newRefreshHash := hashOAuthToken(newRefreshKey)

	// Atomically rotate the refresh row using the legacy ConsumeForRotation
	// (it stamps revoked_at + rotated_to_hash under FOR UPDATE).
	oldToken, err := s.refreshRepo.ConsumeForRotation(ctx, tokenHash, newRefreshHash, now)
	if err != nil {
		// Treat a race that already revoked as reuse.
		if errors.Is(err, ErrOAuthRefreshTokenRevoked) {
			s.handleRefreshReuse(ctx, row)
			return nil, ErrOAuthRefreshReuse
		}
		return nil, err
	}

	// Disable the old access_token api_keys row + invalidate cache.
	if oldKey, gerr := s.apiKeyRepo.GetByID(ctx, oldToken.APIKeyID); gerr == nil && oldKey != nil {
		oldKey.Status = StatusAPIKeyDisabled
		_ = s.apiKeyRepo.Update(ctx, oldKey)
		if s.authCache != nil {
			s.authCache.InvalidateAuthCacheByKey(ctx, oldKey.Key)
		}
	}
	if s.accessRepo != nil {
		_ = s.accessRepo.RevokeAccessTokenByAPIKeyID(ctx, oldToken.APIKeyID, now)
	}

	// Build the replacement trio. FIX H4: when mintRepo is wired the three
	// inserts run in one Postgres tx — same atomicity guarantee as the
	// authorization-code mint path. Without it (legacy tests) we fall back
	// to per-row writes.
	accessExp := now.Add(time.Duration(client.AccessTokenTTLSeconds) * time.Second)
	apiKey := &APIKey{
		UserID:    oldToken.UserID,
		Key:       newAccessKey,
		Name:      fmt.Sprintf("OAuth %s", client.ClientID),
		GroupID:   &targetGroup,
		Status:    StatusAPIKeyActive,
		ExpiresAt: &accessExp,
	}

	scopes := NormalizeScopes(oldToken.Scopes)
	familyID := stringValueOrEmpty(oldToken.TokenFamilyID)
	grantID := stringValueOrEmpty(oldToken.GrantID)

	var accessMeta *OAuthAccessToken
	if s.accessRepo != nil && familyID != "" && grantID != "" {
		accessMeta = &OAuthAccessToken{
			GrantID:         grantID,
			TokenFamilyID:   familyID,
			ClientID:        client.ClientID,
			UserID:          oldToken.UserID,
			Scopes:          scopes,
			GroupID:         targetGroup,
			AllowedGroupIDs: oldToken.AllowedGroupsSnapshot,
			AppType:         client.AppType,
			DeviceID:        oldToken.DeviceID,
			DeviceName:      oldToken.DeviceName,
			IssuedAt:        now,
			ExpiresAt:       accessExp,
		}
	}

	// New oauth_refresh_tokens row — KEY: inherits old expires_at.
	familyExpiry := oldToken.ExpiresAt
	rtExpiresIn := int(time.Until(familyExpiry).Seconds())
	if rtExpiresIn < 0 {
		rtExpiresIn = 0
	}
	refreshMeta := &OAuthRefreshToken{
		TokenHash:             newRefreshHash,
		ClientID:              client.ClientID,
		UserID:                oldToken.UserID,
		Scopes:                scopes,
		ExpiresAt:             familyExpiry,
		GrantID:               oldToken.GrantID,
		TokenFamilyID:         oldToken.TokenFamilyID,
		GroupID:               &targetGroup,
		AllowedGroupsSnapshot: oldToken.AllowedGroupsSnapshot,
		DeviceID:              oldToken.DeviceID,
		DeviceName:            oldToken.DeviceName,
	}

	if s.mintRepo != nil {
		mintParams := &OAuthTokenMintParams{
			APIKey:       apiKey,
			AccessToken:  accessMeta,
			RefreshToken: refreshMeta,
		}
		if err := s.mintRepo.MintTokenSet(ctx, mintParams); err != nil {
			return nil, fmt.Errorf("mint refreshed oauth token set: %w", err)
		}
	} else {
		// Legacy fallback for tests without the atomic repo wired.
		if err := s.apiKeyRepo.Create(ctx, apiKey); err != nil {
			return nil, fmt.Errorf("create access token api_key: %w", err)
		}
		if accessMeta != nil {
			accessMeta.APIKeyID = apiKey.ID
			_ = s.accessRepo.CreateAccessToken(ctx, accessMeta)
		}
		refreshMeta.APIKeyID = apiKey.ID
		if err := s.refreshRepo.CreateRefreshToken(ctx, refreshMeta); err != nil {
			return nil, fmt.Errorf("persist rotated refresh token: %w", err)
		}
	}

	// OIDC Core §12.2: an id_token issued from a refresh grant SHOULD NOT
	// carry a nonce claim (nonce binds the original authentication request,
	// not this rotation) and has no fresh auth_time (no re-authentication
	// happened here). So we deliberately pass empty nonce + zero authTime.
	idTok, idErr := s.maybeSignIDToken(ctx, client, row.UserID, scopes, "", time.Time{})
	if idErr != nil {
		return nil, idErr
	}
	return &IssuedToken{
		AccessToken:           newAccessKey,
		TokenType:             "Bearer",
		ExpiresIn:             client.AccessTokenTTLSeconds,
		RefreshToken:          newRefreshKey,
		RefreshTokenExpiresIn: rtExpiresIn,
		Scope:                 strings.Join(scopes, " "),
		GroupID:               targetGroup,
		IDToken:               idTok,
	}, nil
}

// handleRefreshReuse executes the §11.5 reuse-containment cascade.
//
// On reuse: revoke every access + refresh row in the family, disable every
// associated api_keys row, invalidate the auth cache for each plaintext key,
// and log a redacted warning so observability picks it up.
func (s *OAuthProviderService) handleRefreshReuse(ctx context.Context, row *OAuthRefreshToken) {
	if row == nil {
		// FIX M2: defensive — reuse handler invoked without a usable row.
		// Without (user_id, client_id) we cannot scope a fallback revoke,
		// so just log and bail. The replay still surfaces ErrOAuthRefreshReuse
		// to the caller.
		slog.Warn("oauth: refresh token reuse detected with nil row — cannot revoke; check upstream call path")
		return
	}
	if row.TokenFamilyID == nil || *row.TokenFamilyID == "" {
		// Best-effort: revoke all (user, client) when family unknown (legacy).
		_, _ = s.RevokeUserGrant(ctx, row.UserID, row.ClientID, time.Now())
		slog.Warn("oauth: refresh token reuse detected (no family id)",
			"user_id", row.UserID, "client_id", row.ClientID)
		return
	}
	now := time.Now()
	// Stamp reuse_detected_at on the replayed row before revoking the family.
	// Best-effort: log on failure but do not abort the containment cascade.
	if s.refreshRepo != nil && row.TokenHash != "" {
		if err := s.refreshRepo.MarkReuseDetected(ctx, row.TokenHash, now); err != nil {
			slog.Warn("oauth: mark reuse_detected_at failed",
				"token_hash_prefix", row.TokenHash[:8], "err", err)
		}
	}
	family := *row.TokenFamilyID
	var apiKeyIDs []int64
	if s.refreshRepo != nil {
		ids, err := s.refreshRepo.RevokeRefreshTokensByTokenFamilyID(ctx, family, now)
		if err != nil {
			slog.Warn("oauth: revoke refresh tokens by family failed",
				"family_id", family, "err", err)
		}
		apiKeyIDs = append(apiKeyIDs, ids...)
	}
	if s.accessRepo != nil {
		ids, err := s.accessRepo.RevokeAccessTokensByTokenFamilyID(ctx, family, now)
		if err != nil {
			slog.Warn("oauth: revoke access tokens by family failed",
				"family_id", family, "err", err)
		}
		apiKeyIDs = append(apiKeyIDs, ids...)
	}
	apiKeyIDs = dedupeInt64s(apiKeyIDs)
	s.disableAndInvalidate(ctx, apiKeyIDs, now)
	slog.Warn("oauth: refresh token reuse detected — family revoked",
		"family_id", family,
		"client_id", row.ClientID,
		"user_id", row.UserID,
		"api_keys_disabled", len(apiKeyIDs),
	)
}

// disableAndInvalidate disables api_keys rows by ID and publishes auth-cache
// invalidations for each plaintext key.
func (s *OAuthProviderService) disableAndInvalidate(ctx context.Context, ids []int64, now time.Time) {
	if len(ids) == 0 {
		return
	}
	if s.oauthAPIKey != nil {
		plaintexts, err := s.oauthAPIKey.DisableAPIKeysByIDsReturningKeys(ctx, ids, now)
		if err != nil {
			slog.Warn("oauth: batch disable api_keys failed", "err", err, "ids", len(ids))
		}
		if s.authCache != nil {
			for _, k := range plaintexts {
				s.authCache.InvalidateAuthCacheByKey(ctx, k)
			}
		}
		return
	}
	// Fallback: per-key disable via the base APIKeyRepository.
	for _, id := range ids {
		key, gerr := s.apiKeyRepo.GetByID(ctx, id)
		if gerr != nil || key == nil {
			continue
		}
		if s.authCache != nil {
			s.authCache.InvalidateAuthCacheByKey(ctx, key.Key)
		}
		key.Status = StatusAPIKeyDisabled
		_ = s.apiKeyRepo.Update(ctx, key)
	}
}

// RevokeAccessOrRefreshToken implements RFC 7009 with v2 device-aware semantics.
//
//   - access token: revoke that single oauth_access_tokens row + disable the
//     api_keys row, no other family members touched.
//   - refresh token: revoke the entire grant_id (all access + refresh in that
//     grant), disable every associated api_keys, invalidate cache.
//
// Returns nil on success, including for unknown / wrong-client / already
// revoked tokens (RFC 7009 idempotency: do not leak existence to other clients).
func (s *OAuthProviderService) RevokeAccessOrRefreshToken(
	ctx context.Context,
	clientID, clientSecret, tokenPlain, hint string,
) error {
	if strings.TrimSpace(tokenPlain) == "" {
		return nil
	}
	client, err := s.LookupClient(ctx, clientID)
	if err != nil {
		// Idempotent: pretend success.
		return nil
	}
	if err := s.authenticateClient(client, clientSecret); err != nil {
		return ErrOAuthClientAuthFailed
	}
	now := time.Now()

	tryRefresh := func() bool {
		row, ferr := s.refreshRepo.GetRefreshTokenByHashForUpdate(ctx, hashOAuthToken(tokenPlain), now)
		if ferr != nil {
			return false
		}
		if row.ClientID != client.ClientID {
			return true // pretend success: do not leak existence
		}
		grantID := stringValueOrEmpty(row.GrantID)
		if grantID != "" {
			_ = s.revokeGrantInternal(ctx, grantID)
		} else {
			_, _ = s.RevokeUserGrant(ctx, row.UserID, row.ClientID, now)
		}
		return true
	}
	tryAccess := func() bool {
		// We cannot look up access_tokens by plaintext; the access plaintext lives
		// in api_keys.key. Resolve via api_keys.
		key, ferr := s.apiKeyRepo.GetByKey(ctx, tokenPlain)
		if ferr != nil || key == nil {
			return false
		}
		if !strings.HasPrefix(key.Key, oauthAccessTokenPrefix) {
			return true
		}
		// Verify access_tokens row points at the same client.
		if s.accessRepo != nil {
			meta, gerr := s.accessRepo.GetActiveAccessTokenByAPIKeyID(ctx, key.ID, now)
			if gerr == nil && meta != nil && meta.ClientID != client.ClientID {
				return true // do not leak cross-client existence
			}
		}
		if s.accessRepo != nil {
			_ = s.accessRepo.RevokeAccessTokenByAPIKeyID(ctx, key.ID, now)
		}
		s.disableAndInvalidate(ctx, []int64{key.ID}, now)
		return true
	}

	if strings.EqualFold(hint, "refresh_token") {
		if tryRefresh() {
			return nil
		}
		_ = tryAccess()
		return nil
	}
	if strings.EqualFold(hint, "access_token") {
		if tryAccess() {
			return nil
		}
		_ = tryRefresh()
		return nil
	}
	if !tryRefresh() {
		_ = tryAccess()
	}
	return nil
}

// RevokeGrant revokes every token in a (userID, grantID) and is the surface
// behind DELETE /api/v1/oauth/authorized-apps/:grant_id.
//
// Verifies grant ownership: refuses to revoke a grant that doesn't belong to
// the requesting user. Returns nil for unknown grants so the API stays
// idempotent (matches §12.10 contract for missing/other-user grants).
func (s *OAuthProviderService) RevokeGrant(ctx context.Context, userID int64, grantID string) error {
	if strings.TrimSpace(grantID) == "" {
		return nil
	}
	if userID <= 0 {
		return nil
	}
	// Verify the grant belongs to the user before revoking.
	if s.accessRepo != nil {
		grants, _ := s.accessRepo.ListActiveGrantsByUser(ctx, userID, time.Now())
		owned := false
		for _, g := range grants {
			if g.GrantID == grantID {
				owned = true
				break
			}
		}
		if !owned {
			return nil // idempotent: missing or other-user grant
		}
	}
	return s.revokeGrantInternal(ctx, grantID)
}

func (s *OAuthProviderService) revokeGrantInternal(ctx context.Context, grantID string) error {
	if strings.TrimSpace(grantID) == "" {
		return nil
	}
	now := time.Now()
	var apiKeyIDs []int64
	if s.refreshRepo != nil {
		ids, err := s.refreshRepo.RevokeRefreshTokensByGrantID(ctx, grantID, now)
		if err != nil {
			return fmt.Errorf("revoke refresh tokens by grant: %w", err)
		}
		apiKeyIDs = append(apiKeyIDs, ids...)
	}
	if s.accessRepo != nil {
		ids, err := s.accessRepo.RevokeAccessTokensByGrantID(ctx, grantID, now)
		if err != nil {
			return fmt.Errorf("revoke access tokens by grant: %w", err)
		}
		apiKeyIDs = append(apiKeyIDs, ids...)
	}
	s.disableAndInvalidate(ctx, dedupeInt64s(apiKeyIDs), now)
	return nil
}

// RevokeClientAuthorizations revokes every grant that belongs to (userID, clientID).
// Surface behind DELETE /api/v1/oauth/authorized-apps/client/:client_id.
func (s *OAuthProviderService) RevokeClientAuthorizations(ctx context.Context, userID int64, clientID string) (int, error) {
	if strings.TrimSpace(clientID) == "" || userID <= 0 {
		return 0, nil
	}
	now := time.Now()
	var apiKeyIDs []int64
	if s.refreshRepo != nil {
		ids, err := s.refreshRepo.RevokeRefreshTokensByUserAndClient(ctx, userID, clientID, now)
		if err != nil {
			return 0, fmt.Errorf("revoke refresh tokens by user/client: %w", err)
		}
		apiKeyIDs = append(apiKeyIDs, ids...)
	}
	// access tokens via grants list
	if s.accessRepo != nil {
		grants, _ := s.accessRepo.ListActiveGrantsByUser(ctx, userID, now)
		for _, g := range grants {
			if g.ClientID != clientID {
				continue
			}
			ids, err := s.accessRepo.RevokeAccessTokensByGrantID(ctx, g.GrantID, now)
			if err != nil {
				// FIX M3: don't silently swallow — surface the failure so
				// operators can spot partial revokes. Continue to the next
				// grant rather than aborting the whole revoke.
				slog.Warn("oauth: revoke access tokens by grant failed during client revoke",
					"grant_id", g.GrantID,
					"user_id", userID,
					"client_id", clientID,
					"err", err,
				)
				continue
			}
			apiKeyIDs = append(apiKeyIDs, ids...)
		}
	}
	apiKeyIDs = dedupeInt64s(apiKeyIDs)
	s.disableAndInvalidate(ctx, apiKeyIDs, now)
	return len(apiKeyIDs), nil
}

// ListAuthorizedAppsForUser returns the v2 per-grant projection.
func (s *OAuthProviderService) ListAuthorizedAppsForUser(ctx context.Context, userID int64) ([]*OAuthAuthorizedGrant, error) {
	if s.accessRepo == nil {
		return []*OAuthAuthorizedGrant{}, nil
	}
	out, err := s.accessRepo.ListActiveGrantsByUser(ctx, userID, time.Now())
	if err != nil {
		return nil, fmt.Errorf("list authorized apps: %w", err)
	}
	if out == nil {
		out = []*OAuthAuthorizedGrant{}
	}
	if s.groupRepo != nil {
		for _, g := range out {
			if g.GroupID > 0 && g.GroupName == "" {
				if grp, err := s.groupRepo.GetByIDLite(ctx, g.GroupID); err == nil && grp != nil {
					g.GroupName = grp.Name
				}
			}
		}
	}
	return out, nil
}

// LoadOAuthAccessMetadata loads OAuth metadata for a given api_key_id. When
// scope enforcement is disabled (feature flag), returns (nil, nil) so the
// middleware short-circuits without touching the access metadata table.
func (s *OAuthProviderService) LoadOAuthAccessMetadata(ctx context.Context, apiKeyID int64) (*OAuthAccessToken, error) {
	if !s.IsScopeEnforcementEnabled(ctx) {
		return nil, nil
	}
	if s.accessRepo == nil {
		return nil, nil
	}
	meta, err := s.accessRepo.GetActiveAccessTokenByAPIKeyID(ctx, apiKeyID, time.Now())
	if err != nil {
		if errors.Is(err, ErrOAuthAccessTokenNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return meta, nil
}

// AllowedGroupsForUser returns the rich group objects for every active group
// the authenticated user may bind. Wraps GroupAccessPolicy so handlers can
// build the §12.11 `allowed_groups` array without depending on internal types.
//
// client and scopes are forwarded to ListUserAllowedGroupsForOAuth for
// scope-based and client-level filtering. Pass nil/empty for non-OAuth paths.
//
// Returns an empty slice (not nil) when the policy is unconfigured.
func (s *OAuthProviderService) AllowedGroupsForUser(ctx context.Context, userID int64, client *OAuthClient, scopes []string) ([]OAuthAllowedGroup, error) {
	if s.groupAccess == nil {
		return []OAuthAllowedGroup{}, nil
	}
	out, err := s.groupAccess.ListUserAllowedGroupsForOAuth(ctx, userID, client, scopes)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []OAuthAllowedGroup{}
	}
	return out, nil
}

// oauthAllowedGroupIDs extracts the ID slice from a []OAuthAllowedGroup.
// Used to build the int64 snapshot stored on authorize transactions.
func oauthAllowedGroupIDs(groups []OAuthAllowedGroup) []int64 {
	if len(groups) == 0 {
		return []int64{}
	}
	ids := make([]int64, len(groups))
	for i, g := range groups {
		ids[i] = g.ID
	}
	return ids
}

// filterAllowedGroupsByIDs filters a []OAuthAllowedGroup to only those whose
// ID appears in the allowedIDs slice. Used after filterAllowedByClient to
// apply client-level restrictions to the rich group objects.
func filterAllowedGroupsByIDs(groups []OAuthAllowedGroup, allowedIDs []int64) []OAuthAllowedGroup {
	if len(groups) == 0 || len(allowedIDs) == 0 {
		return []OAuthAllowedGroup{}
	}
	idSet := make(map[int64]bool, len(allowedIDs))
	for _, id := range allowedIDs {
		idSet[id] = true
	}
	out := make([]OAuthAllowedGroup, 0, len(groups))
	for _, g := range groups {
		if idSet[g.ID] {
			out = append(out, g)
		}
	}
	return out
}

// LookupGroup returns a group by ID using the same repo the OAuth resolver
// uses. Used by /v1/me to compose the `current_group` and `allowed_groups`
// payloads without injecting GroupService into the OAuth handler.
func (s *OAuthProviderService) LookupGroup(ctx context.Context, groupID int64) (*Group, error) {
	if s.groupRepo == nil {
		return nil, ErrOAuthInvalidGroup
	}
	return s.groupRepo.GetByID(ctx, groupID)
}

// LookupClient is the package-level export for the existing internal
// LookupClient — see line 274. The exposed signature lets the handler render
// `oauth.client_id` / `oauth.app_type` on /v1/me without importing the
// repo layer directly. Existing usage in this file is unaffected.

// TouchAccessTokenLastUsed updates last_used_at + last_used_ip + last_used_user_agent
// with a 60-second per-token throttle (§11.6). Failures are logged at warn but
// never propagate to the caller — gateway requests must not fail because the
// last-used metadata write is slow.
func (s *OAuthProviderService) TouchAccessTokenLastUsed(ctx context.Context, apiKeyID int64, ip, ua string) {
	if s.accessRepo == nil || apiKeyID <= 0 {
		return
	}
	now := time.Now()
	if v, ok := s.lastUsedThrottle.Load(apiKeyID); ok {
		if next, ok := v.(time.Time); ok && now.Before(next) {
			return
		}
	}
	s.lastUsedThrottle.Store(apiKeyID, now.Add(lastUsedTouchInterval))
	if err := s.accessRepo.TouchAccessToken(ctx, apiKeyID, ip, ua, now); err != nil {
		slog.Warn("oauth: touch access token last_used failed",
			"api_key_id", apiKeyID, "err", err)
	}
}

// ── Legacy v1 surfaces kept for compatibility ───────────────────────────────

// RevokeUserGrant walks every active (user_id, client_id) refresh token,
// marking each one revoked along with the associated access_token api_keys
// row, and publishes Redis Pub/Sub cache invalidations.
//
// v2 callers should prefer RevokeGrant or RevokeClientAuthorizations.
func (s *OAuthProviderService) RevokeUserGrant(ctx context.Context, userID int64, clientID string, now time.Time) (int, error) {
	if s.refreshRepo == nil {
		return 0, nil
	}
	tokens, err := s.refreshRepo.ListActiveByUserAndClient(ctx, userID, clientID, now)
	if err != nil {
		slog.Warn("oauth: list active refresh tokens for revocation failed",
			"user_id", userID, "client_id", clientID, "err", err)
		return 0, fmt.Errorf("list active refresh tokens: %w", err)
	}
	revoked := 0
	for _, tok := range tokens {
		if rerr := s.refreshRepo.RevokeRefreshTokensByAPIKeyID(ctx, tok.APIKeyID, now); rerr != nil {
			slog.Warn("oauth: revoke refresh tokens by api_key_id failed",
				"api_key_id", tok.APIKeyID, "err", rerr)
			continue
		}
		revoked++

		apiKey, gerr := s.apiKeyRepo.GetByID(ctx, tok.APIKeyID)
		if gerr != nil || apiKey == nil {
			if gerr != nil {
				slog.Warn("oauth: load api_key for revocation failed",
					"api_key_id", tok.APIKeyID, "err", gerr)
			}
			continue
		}
		if s.authCache != nil {
			s.authCache.InvalidateAuthCacheByKey(ctx, apiKey.Key)
		}
		apiKey.Status = StatusAPIKeyDisabled
		if uerr := s.apiKeyRepo.Update(ctx, apiKey); uerr != nil {
			slog.Warn("oauth: disable api_key during revocation failed",
				"api_key_id", tok.APIKeyID, "err", uerr)
		}
		// v2 access metadata
		if s.accessRepo != nil {
			_ = s.accessRepo.RevokeAccessTokenByAPIKeyID(ctx, tok.APIKeyID, now)
		}
	}
	return revoked, nil
}

// ListUserGrants returns the user's active OAuth authorizations, one entry
// per client_id (legacy compat shape).
func (s *OAuthProviderService) ListUserGrants(ctx context.Context, userID int64) ([]*OAuthGrant, error) {
	if s.refreshRepo == nil {
		return nil, nil
	}
	now := time.Now()
	tokens, err := s.refreshRepo.ListActiveByUser(ctx, userID, now)
	if err != nil {
		return nil, fmt.Errorf("list active refresh tokens: %w", err)
	}
	if len(tokens) == 0 {
		return []*OAuthGrant{}, nil
	}

	type accumulator struct {
		grant           *OAuthGrant
		scopeSeen       map[string]struct{}
		earliestExpires time.Time
	}
	groups := make(map[string]*accumulator, len(tokens))
	apiKeyIDsByClient := make(map[string][]int64, len(tokens))

	for _, tok := range tokens {
		acc, ok := groups[tok.ClientID]
		if !ok {
			acc = &accumulator{
				grant: &OAuthGrant{
					ClientID:         tok.ClientID,
					Scopes:           []string{},
					ActiveTokenCount: 0,
				},
				scopeSeen:       make(map[string]struct{}),
				earliestExpires: tok.ExpiresAt,
			}
			groups[tok.ClientID] = acc
		}
		acc.grant.ActiveTokenCount++
		for _, sc := range tok.Scopes {
			if _, seen := acc.scopeSeen[sc]; seen {
				continue
			}
			acc.scopeSeen[sc] = struct{}{}
			acc.grant.Scopes = append(acc.grant.Scopes, sc)
		}
		if tok.ExpiresAt.Before(acc.earliestExpires) {
			acc.earliestExpires = tok.ExpiresAt
		}
		apiKeyIDsByClient[tok.ClientID] = append(apiKeyIDsByClient[tok.ClientID], tok.APIKeyID)
	}

	out := make([]*OAuthGrant, 0, len(groups))
	for clientID, acc := range groups {
		client, cerr := s.clientRepo.GetClientByID(ctx, clientID)
		if cerr != nil || client == nil {
			acc.grant.ClientName = ""
			acc.grant.ClientDisabled = true
			acc.grant.FirstAuthorizedAt = now
		} else {
			acc.grant.ClientName = client.Name
			acc.grant.ClientDisabled = client.Disabled
			acc.grant.FirstAuthorizedAt = acc.earliestExpires.Add(
				-time.Duration(client.RefreshTokenTTLSeconds) * time.Second,
			)
		}

		var lastUsed *time.Time
		var earliestCreated *time.Time
		for _, apiKeyID := range apiKeyIDsByClient[clientID] {
			apiKey, gerr := s.apiKeyRepo.GetByID(ctx, apiKeyID)
			if gerr != nil || apiKey == nil {
				continue
			}
			if apiKey.LastUsedAt != nil {
				if lastUsed == nil || apiKey.LastUsedAt.After(*lastUsed) {
					lu := *apiKey.LastUsedAt
					lastUsed = &lu
				}
			}
			ca := apiKey.CreatedAt
			if !ca.IsZero() {
				if earliestCreated == nil || ca.Before(*earliestCreated) {
					ec := ca
					earliestCreated = &ec
				}
			}
		}
		acc.grant.LastUsedAt = lastUsed
		if earliestCreated != nil {
			acc.grant.FirstAuthorizedAt = *earliestCreated
		}

		out = append(out, acc.grant)
	}

	sort.Slice(out, func(i, j int) bool {
		li := timeOrZero(out[i].LastUsedAt)
		lj := timeOrZero(out[j].LastUsedAt)
		if !li.Equal(lj) {
			return li.After(lj)
		}
		return out[i].ClientID < out[j].ClientID
	})
	return out, nil
}

func timeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// mintTokensFromCode is the v2 atomic-write path called after
// ConsumeCode succeeds. It creates api_keys + oauth_access_tokens +
// (optional) oauth_refresh_tokens.
//
// FIX H4: when the mintRepo is wired, all three writes happen inside one
// Postgres tx so a failure between writes can never leave a live sk_oauth_*
// api_keys row that lacks scope metadata. Legacy callers (test fakes) get
// the per-row write fallback.
func (s *OAuthProviderService) mintTokensFromCode(
	ctx context.Context,
	client *OAuthClient,
	code *OAuthCode,
) (*IssuedToken, error) {
	now := time.Now()

	// Group binding: prefer the resolved group on the code; legacy codes
	// without group_id fall back to ResolveOAuthGroup against the user's
	// current permissions.
	var groupID int64
	if code.GroupID != nil && *code.GroupID > 0 {
		groupID = *code.GroupID
	} else {
		resolved, err := s.ResolveOAuthGroup(ctx, code.UserID, client, nil)
		if err != nil {
			return nil, err
		}
		groupID = resolved
	}

	scopes := NormalizeScopes(code.Scopes)

	// Identity for the token family (v2). For legacy codes without grant_id,
	// generate fresh identifiers so the new tokens have proper bookkeeping.
	grantID := stringValueOrEmpty(code.GrantID)
	if grantID == "" {
		grantID = uuid.NewString()
	}
	familyID := uuid.NewString()

	accessKey := oauthAccessTokenPrefix + mustOpaqueToken(defaultAccessTokenSecret)
	accessExp := now.Add(time.Duration(client.AccessTokenTTLSeconds) * time.Second)

	apiKey := &APIKey{
		UserID:    code.UserID,
		Key:       accessKey,
		Name:      fmt.Sprintf("OAuth %s", client.ClientID),
		GroupID:   &groupID,
		Status:    StatusAPIKeyActive,
		ExpiresAt: &accessExp,
	}

	allowedSnap := code.AllowedGroupsSnapshot
	if len(allowedSnap) == 0 {
		allowedSnap = []int64{groupID}
	}

	wantsRefresh := HasScope(scopes, ScopeOfflineAccess) || client.AllowRefreshWithoutOfflineAccess
	var refreshKey string
	var refreshExpiresIn int

	// Build the optional metadata rows up front so the atomic path can
	// pass them to MintTokenSet. APIKeyID is stamped after the api_keys
	// insert succeeds (either inside MintTokenSet or in the fallback
	// branch below).
	accessMeta := &OAuthAccessToken{
		GrantID:         grantID,
		TokenFamilyID:   familyID,
		ClientID:        client.ClientID,
		UserID:          code.UserID,
		Scopes:          scopes,
		GroupID:         groupID,
		AllowedGroupIDs: allowedSnap,
		AppType:         client.AppType,
		DeviceID:        code.DeviceID,
		DeviceName:      code.DeviceName,
		IssuedAt:        now,
		ExpiresAt:       accessExp,
	}
	var refreshMeta *OAuthRefreshToken
	if wantsRefresh {
		refreshKey = oauthRefreshTokenPrefix + mustOpaqueToken(defaultRefreshSecret)
		refreshExp := now.Add(time.Duration(client.RefreshTokenTTLSeconds) * time.Second)
		refreshExpiresIn = client.RefreshTokenTTLSeconds
		grantPtr := grantID
		familyPtr := familyID
		gid := groupID
		refreshMeta = &OAuthRefreshToken{
			TokenHash:             hashOAuthToken(refreshKey),
			ClientID:              client.ClientID,
			UserID:                code.UserID,
			Scopes:                scopes,
			ExpiresAt:             refreshExp,
			GrantID:               &grantPtr,
			TokenFamilyID:         &familyPtr,
			GroupID:               &gid,
			AllowedGroupsSnapshot: allowedSnap,
			DeviceID:              code.DeviceID,
			DeviceName:            code.DeviceName,
		}
	}

	// FIX H4 atomic path: one Postgres tx for all three rows.
	if s.mintRepo != nil {
		mintParams := &OAuthTokenMintParams{
			APIKey:       apiKey,
			RefreshToken: refreshMeta,
		}
		if s.accessRepo != nil {
			mintParams.AccessToken = accessMeta
		}
		if err := s.mintRepo.MintTokenSet(ctx, mintParams); err != nil {
			return nil, fmt.Errorf("mint oauth token set: %w", err)
		}
	} else {
		// Legacy fallback for tests that don't wire the atomic repo.
		// Write order is access_token metadata FIRST so a partial failure
		// never produces an api_keys row without scope metadata (which
		// scope middleware would treat as legacy/pass-through). Refresh
		// metadata follows the api_keys row so its api_key_id FK is valid.
		if err := s.apiKeyRepo.Create(ctx, apiKey); err != nil {
			return nil, fmt.Errorf("create access token api_key: %w", err)
		}
		if s.accessRepo != nil {
			accessMeta.APIKeyID = apiKey.ID
			if err := s.accessRepo.CreateAccessToken(ctx, accessMeta); err != nil {
				return nil, fmt.Errorf("create oauth access token metadata: %w", err)
			}
		}
		if refreshMeta != nil {
			refreshMeta.APIKeyID = apiKey.ID
			if err := s.refreshRepo.CreateRefreshToken(ctx, refreshMeta); err != nil {
				return nil, fmt.Errorf("persist refresh token: %w", err)
			}
		}
	}

	// Resolve primary group name for the response.
	var primaryGroupName string
	if s.groupRepo != nil {
		if g, err := s.groupRepo.GetByID(ctx, groupID); err == nil {
			primaryGroupName = g.Name
		}
	}

	// Multi-group: mint additional tokens for extra groups in the snapshot.
	var additionalTokens []AdditionalToken
	if len(allowedSnap) > 1 {
		for _, extraGID := range allowedSnap {
			if extraGID == groupID {
				continue
			}
			extraKey := oauthAccessTokenPrefix + mustOpaqueToken(defaultAccessTokenSecret)
			extraExp := now.Add(time.Duration(client.AccessTokenTTLSeconds) * time.Second)
			extraAPIKey := &APIKey{
				UserID:    code.UserID,
				Key:       extraKey,
				Name:      fmt.Sprintf("OAuth %s", client.ClientID),
				GroupID:   &extraGID,
				Status:    StatusAPIKeyActive,
				ExpiresAt: &extraExp,
			}
			extraAccess := &OAuthAccessToken{
				GrantID:         grantID,
				TokenFamilyID:   familyID,
				ClientID:        client.ClientID,
				UserID:          code.UserID,
				Scopes:          scopes,
				GroupID:         extraGID,
				AllowedGroupIDs: allowedSnap,
				AppType:         client.AppType,
				DeviceID:        code.DeviceID,
				DeviceName:      code.DeviceName,
				IssuedAt:        now,
				ExpiresAt:       extraExp,
			}
			if s.mintRepo != nil {
				mintP := &OAuthTokenMintParams{APIKey: extraAPIKey}
				if s.accessRepo != nil {
					mintP.AccessToken = extraAccess
				}
				if err := s.mintRepo.MintTokenSet(ctx, mintP); err != nil {
					return nil, fmt.Errorf("mint additional oauth token (group %d): %w", extraGID, err)
				}
			} else {
				if err := s.apiKeyRepo.Create(ctx, extraAPIKey); err != nil {
					return nil, fmt.Errorf("create additional api_key (group %d): %w", extraGID, err)
				}
				if s.accessRepo != nil {
					extraAccess.APIKeyID = extraAPIKey.ID
					if err := s.accessRepo.CreateAccessToken(ctx, extraAccess); err != nil {
						return nil, fmt.Errorf("create additional access token metadata (group %d): %w", extraGID, err)
					}
				}
			}
			var extraName string
			if s.groupRepo != nil {
				if g, err := s.groupRepo.GetByID(ctx, extraGID); err == nil {
					extraName = g.Name
				}
			}
			additionalTokens = append(additionalTokens, AdditionalToken{
				AccessToken: extraKey,
				GroupID:     extraGID,
				GroupName:   extraName,
				ExpiresIn:   client.AccessTokenTTLSeconds,
			})
		}
	}

	idTok, idErr := s.maybeSignIDToken(ctx, client, code.UserID, scopes, code.Nonce, code.CreatedAt)
	if idErr != nil {
		return nil, idErr
	}
	return &IssuedToken{
		AccessToken:           accessKey,
		TokenType:             "Bearer",
		ExpiresIn:             client.AccessTokenTTLSeconds,
		RefreshToken:          refreshKey,
		RefreshTokenExpiresIn: refreshExpiresIn,
		Scope:                 strings.Join(scopes, " "),
		GroupID:               groupID,
		GroupName:             primaryGroupName,
		AdditionalTokens:      additionalTokens,
		IDToken:               idTok,
	}, nil
}

// ── pure helpers ────────────────────────────────────────────────────────────

func redirectURIAllowed(allowed []string, candidate string) bool {
	target := strings.TrimSpace(candidate)
	if target == "" {
		return false
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	for _, uri := range allowed {
		if uri == target {
			// Exact match is sufficient for non-loopback URIs.
			// Loopback URIs registered without an explicit port must follow
			// RFC 8252 port-relaxation rules — a redirect without a port is
			// not valid and must be rejected by isLoopbackRedirect below.
			if u, err := parseLoopbackRedirectURL(uri); err != nil || u.Port() != "" {
				return true
			}
		}
		if isLoopbackRedirect(uri, target) {
			return true
		}
	}
	return false
}

// isLoopbackRedirect returns true if candidate is a valid RFC 8252 loopback
// redirect for the given registered URI. The port relaxation applies only to
// registered HTTP loopback URIs without an explicit port; path, query, and host
// must still match exactly after URL parsing/normalization.
func isLoopbackRedirect(registered, candidate string) bool {
	registeredURL, err := parseLoopbackRedirectURL(registered)
	if err != nil {
		return false
	}
	candidateURL, err := parseLoopbackRedirectURL(candidate)
	if err != nil {
		return false
	}
	if registeredURL.Fragment != "" || candidateURL.Fragment != "" {
		return false
	}
	if registeredURL.Port() != "" {
		return registeredURL.String() == candidateURL.String()
	}
	if candidateURL.Port() == "" {
		return false
	}
	return sameLoopbackHost(registeredURL.Hostname(), candidateURL.Hostname()) &&
		registeredURL.EscapedPath() == candidateURL.EscapedPath() &&
		registeredURL.RawQuery == candidateURL.RawQuery
}

func parseLoopbackRedirectURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" || u.Hostname() == "" || u.User != nil {
		return nil, ErrOAuthInvalidRedirectURI
	}
	if u.Port() != "" {
		if _, err := net.LookupPort("tcp", u.Port()); err != nil {
			return nil, err
		}
	}
	if !isAllowedOAuthLoopbackHost(u.Hostname()) {
		return nil, ErrOAuthInvalidRedirectURI
	}
	return u, nil
}

func sameLoopbackHost(a, b string) bool {
	if strings.EqualFold(a, "localhost") || strings.EqualFold(b, "localhost") {
		return strings.EqualFold(a, b)
	}
	return net.ParseIP(a).Equal(net.ParseIP(b))
}

func isAllowedOAuthLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.Equal(net.ParseIP("127.0.0.1")) || ip.Equal(net.IPv6loopback))
}

func scopesAllowed(allowed, requested []string) bool {
	if len(requested) == 0 {
		return true
	}
	set := make(map[string]struct{}, len(allowed))
	for _, s := range allowed {
		set[s] = struct{}{}
	}
	for _, s := range requested {
		if _, ok := set[s]; !ok {
			return false
		}
	}
	return true
}

// validatePKCEChallenge enforces RFC 7636 character/length rules.
//
// length: 43..128, charset: base64url unreserved (RFC 4648 §5 alphabet plus
// `-` `_`, no padding).
func validatePKCEChallenge(challenge string) bool {
	n := len(challenge)
	if n < pkceMinLength || n > pkceMaxLength {
		return false
	}
	for i := 0; i < n; i++ {
		c := challenge[i]
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '-' || c == '_':
		default:
			return false
		}
	}
	return true
}

// ParseScopes splits the OAuth scope string (space-delimited per RFC 6749 §3.3).
func ParseScopes(raw string) []string {
	fields := strings.Fields(raw)
	out := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	return out
}

// HashOAuthToken computes SHA256 hash of an OAuth token for storage.
// Exported for use by handlers that need to generate authorization codes.
func HashOAuthToken(token string) string {
	return hashOAuthToken(token)
}

// GenerateOpaqueToken generates a cryptographically secure random token.
// Exported for use by handlers that need to generate authorization codes.
func GenerateOpaqueToken(nbytes int) (string, error) {
	return generateOpaqueToken(nbytes)
}

func hashOAuthToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func generateOpaqueToken(nbytes int) (string, error) {
	buf := make([]byte, nbytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func mustOpaqueToken(nbytes int) string {
	t, err := generateOpaqueToken(nbytes)
	if err != nil {
		panic(fmt.Sprintf("oauth: crypto/rand unavailable: %v", err))
	}
	return t
}

// verifyPKCES256 checks that BASE64URL(SHA256(verifier)) == challenge.
func verifyPKCES256(challenge, verifier string) bool {
	if challenge == "" || verifier == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(strings.TrimRight(challenge, "="))) == 1
}

// IsOAuthAccessToken returns true if the bearer string is one this provider issued.
func IsOAuthAccessToken(token string) bool {
	return strings.HasPrefix(token, oauthAccessTokenPrefix)
}

// ValidateLogoutRedirectURI checks if the redirect URI is in the client's
// logout_redirect_uris whitelist. Returns true if valid, false otherwise.
func (s *OAuthProviderService) ValidateLogoutRedirectURI(ctx context.Context, clientID, redirectURI string) (bool, error) {
	client, err := s.clientRepo.GetClientByID(ctx, clientID)
	if err != nil {
		return false, fmt.Errorf("get client: %w", err)
	}

	if client == nil {
		return false, nil
	}

	// Empty whitelist means no redirect URIs allowed
	if len(client.LogoutRedirectURIs) == 0 {
		return false, nil
	}

	// Exact match only (no wildcards, no prefix matching)
	for _, uri := range client.LogoutRedirectURIs {
		if uri == redirectURI {
			return true, nil
		}
	}

	return false, nil
}

func filterAllowedByClient(client *OAuthClient, ids []int64) []int64 {
	if client.AllowedGroupIDs == nil {
		return ids
	}
	allow := make(map[int64]struct{}, len(client.AllowedGroupIDs))
	for _, id := range client.AllowedGroupIDs {
		allow[id] = struct{}{}
	}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, ok := allow[id]; ok {
			out = append(out, id)
		}
	}
	return out
}

func int64InSlice(haystack []int64, needle int64) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

func selectedGroupsOrDefault(selected, fallback []int64) []int64 {
	if len(selected) > 0 {
		return selected
	}
	return fallback
}

func dedupeInt64s(in []int64) []int64 {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(in))
	out := make([]int64, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func stringValueOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
