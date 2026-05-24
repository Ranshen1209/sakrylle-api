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
	"net/url"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"

	"golang.org/x/crypto/bcrypt"
)

// IssuedAuthorizationCode is the value returned via the redirect_uri 302.
type IssuedAuthorizationCode struct {
	Code  string
	State string
}

// IssuedToken is the response body for /oauth/token.
type IssuedToken struct {
	AccessToken  string
	TokenType    string
	ExpiresIn    int
	RefreshToken string
	Scope        string
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
}

var (
	ErrOAuthInvalidResponseType = infraerrors.BadRequest("UNSUPPORTED_RESPONSE_TYPE", "response_type must be code")
	ErrOAuthMissingState        = infraerrors.BadRequest("INVALID_REQUEST", "state is required")
	ErrOAuthGroupNotConfigured  = infraerrors.BadRequest("INVALID_CLIENT", "no default group bound to this client; set default_group_id or oauth_default_group_id setting")
)

const (
	authCodeTTL              = 10 * time.Minute
	oauthAccessTokenPrefix   = "sk_oauth_"
	oauthRefreshTokenPrefix  = "rt_"
	defaultAccessTokenSecret = 32
	defaultRefreshSecret     = 32
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
	apiKeyRepo  APIKeyRepository
	groupRepo   GroupRepository
	settingRepo SettingRepository
	apiKeySvc   *APIKeyService
}

func NewOAuthProviderService(
	clientRepo OAuthClientRepository,
	codeRepo OAuthCodeRepository,
	refreshRepo OAuthRefreshTokenRepository,
	apiKeyRepo APIKeyRepository,
	groupRepo GroupRepository,
	settingRepo SettingRepository,
	apiKeySvc *APIKeyService,
) *OAuthProviderService {
	return &OAuthProviderService{
		clientRepo:  clientRepo,
		codeRepo:    codeRepo,
		refreshRepo: refreshRepo,
		apiKeyRepo:  apiKeyRepo,
		groupRepo:   groupRepo,
		settingRepo: settingRepo,
		apiKeySvc:   apiKeySvc,
	}
}

// IsEnabled reports whether the OAuth provider is enabled in DB settings.
// Treats missing/unparseable values as enabled so a fresh install works.
func (s *OAuthProviderService) IsEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, "oauth_provider_enabled")
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

// LookupClient validates the client_id and rejects disabled clients.
//
// Also enforces that the client has at least one form of credential:
// PKCE-required (public client) OR a client_secret_hash (confidential client).
// A client with neither would make /oauth/token completely unauthenticated.
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
//
// Note: even when PKCERequired=true, a confidential client may still want to
// send client_secret. We accept that without forcing it.
func (s *OAuthProviderService) authenticateClient(client *OAuthClient, clientSecret string) error {
	if client.ClientSecretHash == "" {
		// PKCE-only — no secret to check.
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

// ValidateAuthorizeRequest enforces every check from spec §1.2.
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
	if !scopesAllowed(client.AllowedScopes, req.Scopes) {
		return nil, ErrOAuthInvalidScope
	}
	if client.PKCERequired {
		if strings.TrimSpace(req.CodeChallenge) == "" {
			return nil, ErrOAuthMissingPKCE
		}
		if req.CodeChallengeMethod != "S256" {
			return nil, ErrOAuthUnsupportedChallenge
		}
	}
	return client, nil
}

// IssueAuthorizationCode generates a one-shot code (10 min TTL) for the given user.
//
// The plaintext code is returned only here; the DB stores hex(sha256(code)).
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
		Scopes:              req.Scopes,
		CodeChallenge:       req.CodeChallenge,
		CodeChallengeMethod: req.CodeChallengeMethod,
		ExpiresAt:           now.Add(authCodeTTL),
	}); err != nil {
		return nil, fmt.Errorf("persist auth code: %w", err)
	}
	return &IssuedAuthorizationCode{Code: codePlain, State: req.State}, nil
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
		// loaded row even on ErrOAuthCodeAlreadyUsed, giving us user_id +
		// client_id to find and revoke active issuances.
		if errors.Is(err, ErrOAuthCodeAlreadyUsed) && code != nil {
			s.revokeAllTokensForUserClient(ctx, code.UserID, code.ClientID, now)
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
	return s.mintTokens(ctx, client, code.UserID, code.Scopes)
}

// revokeAllTokensForUserClient walks every active (user_id, client_id)
// refresh token, marking each one revoked along with the associated
// access_token api_keys row. Used as a defensive sweep on code replay.
//
// Errors are logged at warn level (this is a SHOULD-level RFC-6749 §10.5
// sweep, not load-bearing for the response) and the function continues so a
// single bad row doesn't strand later revocations.
func (s *OAuthProviderService) revokeAllTokensForUserClient(ctx context.Context, userID int64, clientID string, now time.Time) {
	if s.refreshRepo == nil {
		return
	}
	tokens, err := s.refreshRepo.ListActiveByUserAndClient(ctx, userID, clientID, now)
	if err != nil {
		slog.Warn("oauth: list active refresh tokens for replay revocation failed",
			"user_id", userID, "client_id", clientID, "err", err)
		return
	}
	for _, tok := range tokens {
		if rerr := s.refreshRepo.RevokeRefreshTokensByAPIKeyID(ctx, tok.APIKeyID, now); rerr != nil {
			slog.Warn("oauth: revoke refresh tokens by api_key_id failed",
				"api_key_id", tok.APIKeyID, "err", rerr)
		}
		apiKey, gerr := s.apiKeyRepo.GetByID(ctx, tok.APIKeyID)
		if gerr != nil {
			slog.Warn("oauth: load api_key for replay revocation failed",
				"api_key_id", tok.APIKeyID, "err", gerr)
			continue
		}
		if apiKey == nil {
			continue
		}
		apiKey.Status = StatusAPIKeyDisabled
		if uerr := s.apiKeyRepo.Update(ctx, apiKey); uerr != nil {
			slog.Warn("oauth: disable api_key during replay revocation failed",
				"api_key_id", tok.APIKeyID, "err", uerr)
			continue
		}
		if s.apiKeySvc != nil {
			s.apiKeySvc.InvalidateAuthCacheByKey(ctx, apiKey.Key)
		}
	}
}

// RefreshAccessToken handles /oauth/token grant_type=refresh_token.
//
// Order of operations (each step is independently durable):
//  1. ConsumeForRotation — atomically lock & revoke the old refresh row,
//     stamp it with rotated_to_hash pointing at the next token. Replays after
//     this step return ErrOAuthRefreshTokenRevoked.
//  2. Disable the old access_token api_keys row (so a leaked plaintext can't
//     outlive its refresh).
//  3. Mint a new access_token api_keys row.
//  4. Insert the new oauth_refresh_tokens row, fully populated.
//
// If steps 2–4 fail after step 1, the old refresh is gone but the user can
// re-authorize. We accept that over the alternative of giving back a still-
// usable old refresh on a partial failure. Client SDKs MUST NOT retry the
// same refresh_token on a partial-failure error from this endpoint — the old
// refresh row is already revoked. Clients should treat any error from
// /oauth/token grant_type=refresh_token as terminal for that token and start
// a fresh /oauth/authorize round trip.
func (s *OAuthProviderService) RefreshAccessToken(
	ctx context.Context,
	clientID, clientSecret, refreshTokenPlain string,
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

	newAccessKey := oauthAccessTokenPrefix + mustOpaqueToken(defaultAccessTokenSecret)
	newRefreshKey := oauthRefreshTokenPrefix + mustOpaqueToken(defaultRefreshSecret)
	newRefreshHash := hashOAuthToken(newRefreshKey)

	oldToken, err := s.refreshRepo.ConsumeForRotation(ctx, hashOAuthToken(refreshTokenPlain), newRefreshHash, now)
	if err != nil {
		return nil, err
	}

	// Disable the old access_token (its api_keys row).
	if oldKey, gerr := s.apiKeyRepo.GetByID(ctx, oldToken.APIKeyID); gerr == nil && oldKey != nil {
		oldKey.Status = StatusAPIKeyDisabled
		_ = s.apiKeyRepo.Update(ctx, oldKey)
		if s.apiKeySvc != nil {
			s.apiKeySvc.InvalidateAuthCacheByKey(ctx, oldKey.Key)
		}
	}

	// Mint the new access_token api_keys row.
	groupID, err := s.resolveDefaultGroupID(ctx, client)
	if err != nil {
		return nil, err
	}
	accessExp := now.Add(time.Duration(client.AccessTokenTTLSeconds) * time.Second)
	apiKey := &APIKey{
		UserID:    oldToken.UserID,
		Key:       newAccessKey,
		Name:      fmt.Sprintf("OAuth %s", client.ClientID),
		GroupID:   &groupID,
		Status:    StatusAPIKeyActive,
		ExpiresAt: &accessExp,
	}
	if err := s.apiKeyRepo.Create(ctx, apiKey); err != nil {
		return nil, fmt.Errorf("create access token api_key: %w", err)
	}

	// Persist the new refresh row, fully populated.
	if err := s.refreshRepo.CreateRefreshToken(ctx, &OAuthRefreshToken{
		TokenHash: newRefreshHash,
		ClientID:  client.ClientID,
		UserID:    oldToken.UserID,
		APIKeyID:  apiKey.ID,
		Scopes:    oldToken.Scopes,
		ExpiresAt: now.Add(time.Duration(client.RefreshTokenTTLSeconds) * time.Second),
	}); err != nil {
		return nil, fmt.Errorf("persist rotated refresh token: %w", err)
	}

	return &IssuedToken{
		AccessToken:  newAccessKey,
		TokenType:    "Bearer",
		ExpiresIn:    client.AccessTokenTTLSeconds,
		RefreshToken: newRefreshKey,
		Scope:        strings.Join(oldToken.Scopes, " "),
	}, nil
}

// mintTokens creates an access_token (api_keys row) + refresh_token in one go.
func (s *OAuthProviderService) mintTokens(
	ctx context.Context,
	client *OAuthClient,
	userID int64,
	scopes []string,
) (*IssuedToken, error) {
	now := time.Now()
	groupID, err := s.resolveDefaultGroupID(ctx, client)
	if err != nil {
		return nil, err
	}

	accessKey := oauthAccessTokenPrefix + mustOpaqueToken(defaultAccessTokenSecret)
	accessExp := now.Add(time.Duration(client.AccessTokenTTLSeconds) * time.Second)

	apiKey := &APIKey{
		UserID:    userID,
		Key:       accessKey,
		Name:      fmt.Sprintf("OAuth %s", client.ClientID),
		GroupID:   &groupID,
		Status:    StatusAPIKeyActive,
		ExpiresAt: &accessExp,
	}
	if err := s.apiKeyRepo.Create(ctx, apiKey); err != nil {
		return nil, fmt.Errorf("create access token api_key: %w", err)
	}

	refreshKey := oauthRefreshTokenPrefix + mustOpaqueToken(defaultRefreshSecret)
	refreshExp := now.Add(time.Duration(client.RefreshTokenTTLSeconds) * time.Second)

	if err := s.refreshRepo.CreateRefreshToken(ctx, &OAuthRefreshToken{
		TokenHash: hashOAuthToken(refreshKey),
		ClientID:  client.ClientID,
		UserID:    userID,
		APIKeyID:  apiKey.ID,
		Scopes:    scopes,
		ExpiresAt: refreshExp,
	}); err != nil {
		return nil, fmt.Errorf("persist refresh token: %w", err)
	}

	return &IssuedToken{
		AccessToken:  accessKey,
		TokenType:    "Bearer",
		ExpiresIn:    client.AccessTokenTTLSeconds,
		RefreshToken: refreshKey,
		Scope:        strings.Join(scopes, " "),
	}, nil
}

// resolveDefaultGroupID picks the group_id that issued tokens are bound to,
// then verifies the group still exists and is active so we don't issue tokens
// that point at a deleted/disabled group.
//
// Priority: client.default_group_id → setting oauth_default_group_id → error.
func (s *OAuthProviderService) resolveDefaultGroupID(ctx context.Context, client *OAuthClient) (int64, error) {
	var groupID int64
	if client.DefaultGroupID != nil && *client.DefaultGroupID > 0 {
		groupID = *client.DefaultGroupID
	} else {
		value, err := s.settingRepo.GetValue(ctx, "oauth_default_group_id")
		if err == nil {
			if id, perr := strconv.ParseInt(strings.TrimSpace(value), 10, 64); perr == nil && id > 0 {
				groupID = id
			}
		}
	}
	if groupID <= 0 {
		return 0, ErrOAuthGroupNotConfigured
	}
	if s.groupRepo != nil {
		group, err := s.groupRepo.GetByID(ctx, groupID)
		if err != nil {
			return 0, fmt.Errorf("%w: group %d not found", ErrOAuthGroupNotConfigured, groupID)
		}
		if !group.IsActive() {
			return 0, fmt.Errorf("%w: group %d disabled", ErrOAuthGroupNotConfigured, groupID)
		}
	}
	return groupID, nil
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
			return true
		}
	}
	return false
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

// mustOpaqueToken panics on rand failure (the same posture as crypto/rand callers
// elsewhere in this codebase). It only ever fails on hardware entropy issues.
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
//
// Used by the /v1/* middleware so OAuth tokens get OAuth-shaped 401s instead
// of generic API-key error envelopes.
func IsOAuthAccessToken(token string) bool {
	return strings.HasPrefix(token, oauthAccessTokenPrefix)
}
