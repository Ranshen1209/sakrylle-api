package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// canonicalScopesForDiscovery is the §12.1 `scopes_supported` advertisement.
// Sourced from service.CanonicalScopes() so the discovery doc stays in lock
// step with the scope registry.
var canonicalScopesForDiscovery = service.CanonicalScopes()

// OAuthProviderHandler exposes RFC 6749 §4.1 (Authorization Code) + RFC 7636
// (PKCE) endpoints so external apps (e.g. image.sakrylle.com) can mint
// Sakrylle access tokens scoped to a user account.
type OAuthProviderHandler struct {
	provider *service.OAuthProviderService
	settings *service.SettingService
	device   *OAuthDeviceHandler
	// oidcKeys signs id_tokens and publishes JWKS. Nil-safe: when unset the
	// JWKS endpoint reports unavailable and no id_token is issued (the OAuth
	// flows are unaffected).
	oidcKeys *service.OIDCKeyService
	// authService validates JWT tokens for prompt=none silent authentication.
	// Nil-safe: when unset prompt=none returns interaction_required.
	authService *service.AuthService
}

// SetOIDCKeyService wires the OIDC signing key service post-construction
// (mirrors SetDeviceHandler) so DI need not thread it through the constructor.
func (h *OAuthProviderHandler) SetOIDCKeyService(k *service.OIDCKeyService) {
	if h != nil {
		h.oidcKeys = k
	}
}

// SetAuthService wires the AuthService post-construction for prompt=none
// silent authentication. When unset, prompt=none returns interaction_required.
func (h *OAuthProviderHandler) SetAuthService(a *service.AuthService) {
	if h != nil {
		h.authService = a
	}
}

func NewOAuthProviderHandler(provider *service.OAuthProviderService, settings *service.SettingService) *OAuthProviderHandler {
	h := &OAuthProviderHandler{provider: provider, settings: settings}
	// One-shot soft check at boot: warn loudly when neither oauth_issuer nor
	// frontend_url is configured, because RFC 8414 discovery and §12.7 device
	// verification_uri will fall back to request scheme://Host (unstable
	// across reverse-proxy configurations and TLS terminators).
	//
	// Deliberately *not* fail-closed: the setup wizard runs before either key
	// is written on a fresh install, so a hard error here would break first
	// boot. context.Background() is fine — this fires once during DI wiring,
	// not on a request path.
	if settings != nil {
		settings.WarnIfOAuthIssuerMissing(context.Background())
	}
	return h
}

// SetDeviceHandler wires the device-grant branch into Token.
//
// The device handler is a separate handler struct so the Phase 4 device flow
// stays self-contained; we attach it post-construction so wire DI doesn't
// have to hold a circular relationship between the two handlers.
func (h *OAuthProviderHandler) SetDeviceHandler(d *OAuthDeviceHandler) {
	h.device = d
}

// Authorize renders the consent page for /oauth/authorize.
//
// Query parameters are validated up front. Errors that *can* safely redirect
// back to the client (per RFC 6749 §4.1.2.1: invalid_scope, server_error,
// etc.) do; everything else (bad client_id, bad redirect_uri) renders an
// inline HTML error so we never bounce a user to an attacker-controlled URL.
func (h *OAuthProviderHandler) Authorize(c *gin.Context) {
	if !h.provider.IsEnabled(c.Request.Context()) {
		renderOAuthInlineError(c, http.StatusForbidden, "oauth provider is currently disabled")
		return
	}

	// Support both GET (query params) and POST (form-encoded body) per §12.2.
	if c.Request.Method == http.MethodPost {
		_ = c.Request.ParseForm()
	}
	formVal := func(key string) string {
		if c.Request.Method == http.MethodPost {
			return strings.TrimSpace(c.Request.FormValue(key))
		}
		return strings.TrimSpace(c.Query(key))
	}

	req := &service.AuthorizeRequest{
		ClientID:            formVal("client_id"),
		RedirectURI:         formVal("redirect_uri"),
		ResponseType:        formVal("response_type"),
		Scopes:              service.ParseScopes(formVal("scope")),
		State:               formVal("state"),
		CodeChallenge:       formVal("code_challenge"),
		CodeChallengeMethod: formVal("code_challenge_method"),
		Nonce:               formVal("nonce"),
	}

	// Handle prompt=none (OIDC Core §3.1.2.1): silent authentication.
	// If the user has a valid session, skip consent UI and issue code directly.
	// Otherwise return error=login_required or error=interaction_required.
	if formVal("prompt") == "none" {
		h.handlePromptNone(c, req)
		return
	}

	client, err := h.provider.ValidateAuthorizeRequest(c.Request.Context(), req)
	if err != nil {
		// Bad client_id / redirect_uri → inline error (don't redirect to an
		// unverified URL). Other errors are safe to redirect.
		if errors.Is(err, service.ErrOAuthClientNotFound) ||
			errors.Is(err, service.ErrOAuthClientDisabled) ||
			errors.Is(err, service.ErrOAuthInvalidRedirectURI) {
			renderOAuthInlineError(c, http.StatusBadRequest, infraerrors.Message(err))
			return
		}
		redirectOAuthProviderError(c, req.RedirectURI, req.State, err)
		return
	}

	// Render consent page. The page POSTs back to /api/v1/oauth/authorize/approve.
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("X-Frame-Options", "DENY")
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Content-Security-Policy", "frame-ancestors 'none'")
	c.String(http.StatusOK, oauthConsentHTML(client.Name, req, middleware.GetNonceFromContext(c)))
}

// ApproveRequest is the body of POST /api/v1/oauth/authorize/approve.
//
// FIX A1 / §10.3: the approve POST carries ONLY the transaction id, the
// CSRF token returned by /authorize/begin, the decision, and an optional
// group override. Every other parameter (client_id, redirect_uri, scopes,
// state, code_challenge, ...) is read from the server-side transaction row
// keyed by transaction_id. Allowing the client to re-supply those values is
// exactly the §10.3 attack surface the transaction model exists to close —
// the consent page user sees scopes for client A, but the JS could submit
// scopes for client B and we'd happily mint a code.
type ApproveRequest struct {
	TransactionID string  `json:"transaction_id" binding:"required"`
	CSRFToken     string  `json:"csrf_token" binding:"required"`
	Decision      string  `json:"decision" binding:"required,oneof=approve deny"`
	GroupID       *int64  `json:"group_id"`
	GroupIDs      []int64 `json:"group_ids"`
}

// BeginAuthorizeRequest is the body of POST /api/v1/oauth/authorize/begin.
//
// The consent page (rendered by GET /oauth/authorize) reads the JWT from
// localStorage and posts these fields to /begin, which authenticates the
// JWT subject, calls ValidateAuthorizeRequest, then opens a server-side
// transaction owned by that subject. The /authorize GET cannot do this
// itself because it is a top-level browser navigation without a JWT cookie
// — auth lives in localStorage and is sent only on XHR.
type BeginAuthorizeRequest struct {
	ClientID            string `json:"client_id" binding:"required"`
	RedirectURI         string `json:"redirect_uri" binding:"required"`
	ResponseType        string `json:"response_type" binding:"required"`
	Scope               string `json:"scope"`
	State               string `json:"state" binding:"required"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	GroupID             *int64 `json:"group_id"`
	DeviceName          string `json:"device_name"`
	Nonce               string `json:"nonce"`
}

// BeginAuthorize creates the server-side authorize transaction for the
// currently logged-in user.
//
// Mounted under JWT-protected /api/v1/* so every transaction row carries an
// authoritative user_id (the JWT subject). The plaintext CSRF token is
// returned ONCE — only the SHA-256 hex hash lives in the DB. The consent
// page stores it in memory and submits it on /approve.
func (h *OAuthProviderHandler) BeginAuthorize(c *gin.Context) {
	if !h.provider.IsEnabled(c.Request.Context()) {
		c.JSON(http.StatusForbidden, gin.H{"error": "oauth_provider_disabled"})
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var body BeginAuthorizeRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "error_description": err.Error()})
		return
	}
	params := &service.BeginAuthorizeParams{
		ClientID:            strings.TrimSpace(body.ClientID),
		RedirectURI:         strings.TrimSpace(body.RedirectURI),
		ResponseType:        strings.TrimSpace(body.ResponseType),
		Scopes:              service.ParseScopes(body.Scope),
		State:               strings.TrimSpace(body.State),
		CodeChallenge:       strings.TrimSpace(body.CodeChallenge),
		CodeChallengeMethod: strings.TrimSpace(body.CodeChallengeMethod),
		RequestedGroupID:    body.GroupID,
		DeviceName:          stringPtrIfNotEmpty(strings.TrimSpace(body.DeviceName)),
		UserID:              subject.UserID,
		Nonce:               strings.TrimSpace(body.Nonce),
	}
	result, err := h.provider.BeginAuthorizeTransaction(c.Request.Context(), params)
	if err != nil {
		c.JSON(httpStatusForOAuthError(err), gin.H{
			"error":             oauthErrorReason(err),
			"error_description": infraerrors.Message(err),
		})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"transaction_id": result.Transaction.TransactionID,
		"csrf_token":     result.CSRFTokenPlaintext,
		"client_name":    result.Client.Name,
		"scopes":         result.Transaction.Scopes,
		"redirect_uri":   result.Transaction.RedirectURI,
		"expires_at":     result.Transaction.ExpiresAt,
		"allowed_groups": result.AllowedGroupsForUser,
	})
}

// Approve issues an authorization code (or denial) for an authenticated user.
//
// Mounted under JWT-protected /api/v1/* so only logged-in users can complete
// consent. The body MUST be the v2 transaction-bound shape; legacy fields
// (client_id, redirect_uri, scope, ...) are ignored — those values live on
// the server-side transaction row, keyed by transaction_id.
//
// Returns the final redirect URL in JSON; the SPA navigates to it.
func (h *OAuthProviderHandler) Approve(c *gin.Context) {
	if !h.provider.IsEnabled(c.Request.Context()) {
		c.JSON(http.StatusForbidden, gin.H{"error": "oauth_provider_disabled"})
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var body ApproveRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "error_description": err.Error()})
		return
	}
	txID := strings.TrimSpace(body.TransactionID)
	csrf := strings.TrimSpace(body.CSRFToken)

	if body.Decision != "approve" {
		// FIX A1: deny still requires the transaction lookup so we can echo
		// the registered redirect_uri + state from the server-side row, never
		// trusting client-supplied values. CSRF + subject are validated as
		// part of the load.
		tx, _, err := h.provider.LoadAuthorizeTransactionForApproval(c.Request.Context(), txID, csrf, subject.UserID)
		if err != nil {
			h.writeApproveError(c, err, txID)
			return
		}
		// Mark consumed so the same transaction can't be re-approved later.
		if _, err := h.provider.DenyAuthorization(c.Request.Context(), txID, csrf, subject.UserID); err != nil {
			// DenyAuthorization is idempotent in spirit but may surface a
			// repo-level error; opaque server_error is the right shape here
			// because we already validated the transaction above.
			slog.Warn("oauth: deny authorization mark-consumed failed",
				"err", err, "user_id", subject.UserID, "transaction_id", txID)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":             "server_error",
				"error_description": "deny processing failed",
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"redirect_to": buildOAuthErrorURL(tx.RedirectURI, "access_denied", "user denied authorization", tx.State),
		})
		return
	}

	result, err := h.provider.ApproveAuthorization(c.Request.Context(), txID, csrf, subject.UserID, body.GroupID, body.GroupIDs)
	if err != nil {
		h.writeApproveError(c, err, txID)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"redirect_to": buildOAuthRedirectURL(result.RedirectURI, result.Code, result.State),
	})
}

// writeApproveError maps service-layer errors from approve/deny to OAuth-spec
// JSON responses without leaking attacker signal.
//
// Subject mismatch is treated as opaque invalid_request (a 4xx the legitimate
// user can act on) — never reveal that subject A's transaction was probed by
// subject B; that's a CSRF/IDOR signal we log server-side instead.
//
// CSRF mismatch + transaction not found / consumed / expired all return 400
// with the canonical OAuth error mapping; the consent page surfaces the
// description verbatim to the user.
func (h *OAuthProviderHandler) writeApproveError(c *gin.Context, err error, txID string) {
	if errors.Is(err, service.ErrOAuthSubjectMismatch) {
		// Log loudly: this is a real attack signal (someone replayed a
		// transaction id with a different JWT). Return opaque to the client.
		slog.Warn("oauth: approve subject mismatch",
			"transaction_id", txID, "err", err)
		c.JSON(http.StatusForbidden, gin.H{
			"error":             "invalid_request",
			"error_description": "authorization could not be completed",
		})
		return
	}
	if errors.Is(err, service.ErrOAuthAuthorizeCSRFMismatch) {
		c.JSON(http.StatusForbidden, gin.H{
			"error":             "invalid_request",
			"error_description": infraerrors.Message(err),
		})
		return
	}
	if errors.Is(err, service.ErrOAuthAuthorizeTransactionNotFound) ||
		errors.Is(err, service.ErrOAuthAuthorizeTransactionConsumed) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_request",
			"error_description": infraerrors.Message(err),
		})
		return
	}
	// Known OAuth-spec errors (group not allowed, scope not allowed, etc.)
	// keep their canonical reason string.
	if reason := infraerrors.Reason(err); reason != "" && reason != "INTERNAL" {
		c.JSON(httpStatusForOAuthError(err), gin.H{
			"error":             oauthErrorReason(err),
			"error_description": infraerrors.Message(err),
		})
		return
	}
	// Anything else: opaque server_error. Don't echo internal text — DB
	// strings can leak schema names. (FIX H3 pattern.)
	slog.Warn("oauth: approve unexpected error",
		"transaction_id", txID, "err", err)
	c.JSON(http.StatusInternalServerError, gin.H{
		"error":             "server_error",
		"error_description": "authorization could not be completed",
	})
}

// Token handles POST /oauth/token (both authorization_code and refresh_token grants).
//
// Per spec the body MUST be application/x-www-form-urlencoded.
func (h *OAuthProviderHandler) Token(c *gin.Context) {
	if !h.provider.IsEnabled(c.Request.Context()) {
		writeOAuthError(c, http.StatusServiceUnavailable, "temporarily_unavailable", "oauth provider disabled")
		return
	}
	// Issue 3: reject non-form-encoded bodies before ParseForm, matching Revoke.
	if !isFormEncoded(c) {
		writeOAuthError(c, http.StatusBadRequest, "invalid_request", "Content-Type must be application/x-www-form-urlencoded")
		return
	}
	if err := c.Request.ParseForm(); err != nil {
		writeOAuthError(c, http.StatusBadRequest, "invalid_request", "form parse failed")
		return
	}

	grantType := strings.TrimSpace(c.Request.PostFormValue("grant_type"))
	switch grantType {
	case "authorization_code":
		h.tokenAuthorizationCode(c)
	case "refresh_token":
		h.tokenRefresh(c)
	case "urn:ietf:params:oauth:grant-type:device_code":
		if h.device == nil {
			writeOAuthError(c, http.StatusBadRequest, "unsupported_grant_type", "device flow handler not wired")
			return
		}
		h.device.HandleDeviceCodeGrant(c)
	default:
		writeOAuthError(c, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code, refresh_token, or urn:ietf:params:oauth:grant-type:device_code")
	}
}

func (h *OAuthProviderHandler) tokenAuthorizationCode(c *gin.Context) {
	// Issue 25: group_id is only valid on refresh_token grants; reject it here.
	if c.Request.PostFormValue("group_id") != "" {
		writeOAuthError(c, http.StatusBadRequest, "invalid_request", "group_id is not allowed on authorization_code grant")
		return
	}
	clientID, clientSecret := extractClientCredentials(c)
	code := strings.TrimSpace(c.Request.PostFormValue("code"))
	redirectURI := strings.TrimSpace(c.Request.PostFormValue("redirect_uri"))
	codeVerifier := strings.TrimSpace(c.Request.PostFormValue("code_verifier"))

	issued, err := h.provider.ExchangeAuthorizationCode(c.Request.Context(), clientID, clientSecret, code, redirectURI, codeVerifier)
	if err != nil {
		writeOAuthError(c, httpStatusForOAuthError(err), oauthErrorReason(err), infraerrors.Message(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.JSON(http.StatusOK, tokenResponseJSON(issued))
}

func (h *OAuthProviderHandler) tokenRefresh(c *gin.Context) {
	clientID, clientSecret := extractClientCredentials(c)
	refreshToken := strings.TrimSpace(c.Request.PostFormValue("refresh_token"))

	var requestedGroupID *int64
	if raw := strings.TrimSpace(c.Request.PostFormValue("group_id")); raw != "" {
		if v, perr := strconv.ParseInt(raw, 10, 64); perr == nil && v > 0 {
			requestedGroupID = &v
		}
	}

	issued, err := h.provider.RefreshAccessToken(c.Request.Context(), clientID, clientSecret, refreshToken, requestedGroupID)
	if err != nil {
		writeOAuthError(c, httpStatusForOAuthError(err), oauthErrorReason(err), infraerrors.Message(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.JSON(http.StatusOK, tokenResponseJSON(issued))
}

// extractClientCredentials reads (client_id, client_secret) preferring HTTP
// Basic auth (RFC 6749 §2.3.1, the recommended way for confidential clients)
// and falling back to form-encoded body parameters (also spec-compliant).
//
// PKCE-only clients won't send a secret in either form, which is fine.
func extractClientCredentials(c *gin.Context) (clientID, clientSecret string) {
	if user, pass, ok := c.Request.BasicAuth(); ok {
		clientID = strings.TrimSpace(user)
		clientSecret = strings.TrimSpace(pass)
		return
	}
	clientID = strings.TrimSpace(c.Request.PostFormValue("client_id"))
	clientSecret = strings.TrimSpace(c.Request.PostFormValue("client_secret"))
	return
}

// ── response helpers ───────────────────────────────────────────────────────

func tokenResponseJSON(issued *service.IssuedToken) gin.H {
	resp := gin.H{
		"access_token": issued.AccessToken,
		"token_type":   issued.TokenType,
		"expires_in":   issued.ExpiresIn,
		"scope":        issued.Scope,
	}
	if issued.RefreshToken != "" {
		resp["refresh_token"] = issued.RefreshToken
		if issued.RefreshTokenExpiresIn > 0 {
			resp["refresh_token_expires_in"] = issued.RefreshTokenExpiresIn
		}
	}
	if issued.IDToken != "" {
		resp["id_token"] = issued.IDToken
	}
	if issued.GroupID > 0 {
		resp["group"] = gin.H{"id": issued.GroupID, "name": issued.GroupName}
	}
	if len(issued.AdditionalTokens) > 0 {
		extra := make([]gin.H, 0, len(issued.AdditionalTokens))
		for _, t := range issued.AdditionalTokens {
			extra = append(extra, gin.H{
				"access_token": t.AccessToken,
				"expires_in":   t.ExpiresIn,
				"group":        gin.H{"id": t.GroupID, "name": t.GroupName},
			})
		}
		resp["additional_tokens"] = extra
	}
	return resp
}

// writeOAuthError emits a RFC 6749-compliant error response.
// error_uri points to the Sakrylle docs anchor for the given error code so
// clients can surface a stable help link. The docs page need not exist yet —
// the URI is a stable pointer for future documentation.
func writeOAuthError(c *gin.Context, status int, code, desc string) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.JSON(status, gin.H{
		"error":             code,
		"error_description": desc,
		"error_uri":         "https://doc.sakrylle.com/developers/oauth/errors#" + code,
	})
}

// httpStatusForOAuthError maps service-layer sentinels to OAuth-spec status codes.
func httpStatusForOAuthError(err error) int {
	switch infraerrors.Reason(err) {
	case "INVALID_CLIENT":
		return http.StatusUnauthorized
	case "INVALID_GRANT", "INVALID_REQUEST", "INVALID_SCOPE",
		"UNSUPPORTED_GRANT_TYPE", "UNSUPPORTED_RESPONSE_TYPE":
		return http.StatusBadRequest
	default:
		return http.StatusBadRequest
	}
}

func oauthErrorReason(err error) string {
	return strings.ToLower(infraerrors.Reason(err))
}

func buildOAuthRedirectURL(redirectURI, code, state string) string {
	parsed, err := url.Parse(redirectURI)
	if err != nil {
		return redirectURI
	}
	q := parsed.Query()
	q.Set("code", code)
	q.Set("state", state)
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

func buildOAuthErrorURL(redirectURI, errCode, errDesc, state string) string {
	parsed, err := url.Parse(redirectURI)
	if err != nil {
		return redirectURI
	}
	q := parsed.Query()
	q.Set("error", errCode)
	if errDesc != "" {
		q.Set("error_description", errDesc)
	}
	if state != "" {
		q.Set("state", state)
	}
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

// redirectOAuthProviderError 302s the user back to their redirect_uri with the OAuth error.
// Only safe when redirect_uri is already validated against the client whitelist.
func redirectOAuthProviderError(c *gin.Context, redirectURI, state string, err error) {
	if redirectURI == "" {
		renderOAuthInlineError(c, http.StatusBadRequest, infraerrors.Message(err))
		return
	}
	c.Redirect(http.StatusFound, buildOAuthErrorURL(redirectURI, oauthErrorReason(err), infraerrors.Message(err), state))
}

// renderOAuthInlineError shows an HTML error page when redirecting would be unsafe.
func renderOAuthInlineError(c *gin.Context, status int, message string) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	c.String(status, oauthInlineErrorHTML(message))
}

// ── User-facing grants management ───────────────────────────────────────────

// ListGrants returns the OAuth authorizations the current user has issued,
// one row per third-party app. Mounted under JWT-protected /api/v1/.
func (h *OAuthProviderHandler) ListGrants(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	grants, err := h.provider.ListUserGrants(c.Request.Context(), subject.UserID)
	if err != nil {
		// Don't echo internal error text — DB error strings can leak schema
		// names (table/column/constraint). Log server-side, return opaque.
		slog.Warn("oauth: list user grants failed",
			"user_id", subject.UserID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":             "server_error",
			"error_description": "failed to load authorized apps",
		})
		return
	}
	out := make([]gin.H, 0, len(grants))
	for _, g := range grants {
		out = append(out, gin.H{
			"client_id":           g.ClientID,
			"client_name":         g.ClientName,
			"client_disabled":     g.ClientDisabled,
			"scopes":              g.Scopes,
			"first_authorized_at": g.FirstAuthorizedAt,
			"last_used_at":        g.LastUsedAt,
			"active_token_count":  g.ActiveTokenCount,
		})
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

// RevokeGrant revokes every active token a user holds for the given client.
// Idempotent: revoking a non-existent or already-revoked grant returns 200
// with `{"revoked": 0}`. Mounted under JWT-protected /api/v1/.
func (h *OAuthProviderHandler) RevokeGrant(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	clientID := strings.TrimSpace(c.Param("client_id"))
	if clientID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_request",
			"error_description": "client_id is required",
		})
		return
	}
	revoked, err := h.provider.RevokeUserGrant(c.Request.Context(), subject.UserID, clientID, time.Now())
	if err != nil {
		slog.Warn("oauth: revoke user grant failed",
			"user_id", subject.UserID, "client_id", clientID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":             "server_error",
			"error_description": "failed to revoke authorization",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"revoked": revoked})
}

// ── Discovery (§12.1) ───────────────────────────────────────────────────────

// Metadata serves GET /.well-known/oauth-authorization-server.
//
// The issuer is derived from `frontend_url` (DB setting → config fallback)
// per docs/OAUTH_V2_DESIGN.md §12.1. Trailing slashes are stripped so the
// `issuer` value is exactly the registered authority — RFC 8414 forbids a
// trailing slash on the issuer claim.
//
// Cache-Control mirrors §12.1: `public, max-age=60`. Discovery is intended
// for clients to cache; we keep the TTL low so a config change rolls out in
// minutes, not hours.
func (h *OAuthProviderHandler) Metadata(c *gin.Context) {
	issuer := h.discoveryIssuer(c)
	resp := gin.H{
		"issuer":                                     issuer,
		"authorization_endpoint":                     issuer + "/oauth/authorize",
		"token_endpoint":                             issuer + "/oauth/token",
		"revocation_endpoint":                        issuer + "/oauth/revoke",
		"device_authorization_endpoint":              issuer + "/oauth/device/code",
		"userinfo_endpoint":                          issuer + "/v1/me",
		"response_types_supported":                   []string{"code"},
		"response_modes_supported":                   []string{"query"},
		"ui_locales_supported":                       []string{"zh-CN", "en"},
		"grant_types_supported":                      []string{"authorization_code", "refresh_token", "urn:ietf:params:oauth:grant-type:device_code"},
		"code_challenge_methods_supported":           []string{"S256"},
		"token_endpoint_auth_methods_supported":      []string{"none", "client_secret_basic", "client_secret_post"},
		"revocation_endpoint_auth_methods_supported": []string{"none", "client_secret_basic", "client_secret_post"},
		"scopes_supported":                           canonicalScopesForDiscovery,
		"service_documentation":                      "https://doc.sakrylle.com/developers/oauth/",
	}
	c.Header("Content-Type", "application/json")
	c.Header("Cache-Control", "public, max-age=60")
	c.JSON(http.StatusOK, resp)
}

// OpenIDConfiguration serves GET /.well-known/openid-configuration
// (OpenID Connect Discovery 1.0). It reuses the same issuer resolver as the
// RFC 8414 OAuth metadata so `iss` in id_tokens, the device verification_uri,
// and both discovery documents can never disagree.
//
// id_token_signing_alg_values_supported advertises both RS256 and ES256.
// subject_types_supported is "public": sub is the stable user id, not a
// pairwise pseudonym.
func (h *OAuthProviderHandler) OpenIDConfiguration(c *gin.Context) {
	// Return 404 when OIDC signing is not wired — prevents advertising
	// capabilities the server cannot honor (JWKS would 503, id_token empty).
	if h.oidcKeys == nil {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusNotFound, gin.H{"error": "OIDC not enabled"})
		return
	}
	issuer := h.discoveryIssuer(c)
	resp := gin.H{
		"issuer":                                issuer,
		"authorization_endpoint":                issuer + "/oauth/authorize",
		"token_endpoint":                        issuer + "/oauth/token",
		"userinfo_endpoint":                     issuer + "/v1/me",
		"jwks_uri":                              issuer + "/.well-known/jwks.json",
		"response_types_supported":              []string{"code"},
		"response_modes_supported":              []string{"query"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token", "urn:ietf:params:oauth:grant-type:device_code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256", "ES256"},
		"scopes_supported":                      canonicalScopesForDiscovery,
		"token_endpoint_auth_methods_supported": []string{"none", "client_secret_basic", "client_secret_post"},
		"code_challenge_methods_supported":      []string{"S256"},
		"claims_supported": []string{
			"iss", "sub", "aud", "exp", "iat", "nonce",
			"name", "preferred_username", "email", "email_verified",
			"auth_time",
		},
		"service_documentation": "https://doc.sakrylle.com/developers/oauth/",
	}
	c.Header("Content-Type", "application/json")
	c.Header("Cache-Control", "public, max-age=60")
	c.JSON(http.StatusOK, resp)
}

// JWKS serves GET /.well-known/jwks.json — the public id_token verification
// keys (RS256 RSA + ES256 EC).
//
// Only public key material is published (the JWK type cannot carry private
// components). Cache-Control max-age is 3600s: longer than the discovery TTL
// to allow relying parties to cache the key set across multiple requests.
// Key rotation is implemented for both algorithms (OIDCKeyService.RotateKey /
// RotateECKey / CleanupExpiredKeys): during the grace period the JWKS includes
// both the previous and current key (dual-kid) for the rotated algorithm so RPs
// can verify tokens signed with either key. Rotation is operator-triggered (no
// automatic background scheduler is wired yet).
func (h *OAuthProviderHandler) JWKS(c *gin.Context) {
	if h.oidcKeys == nil {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "oidc signing key unavailable"})
		return
	}
	jwks, err := h.oidcKeys.PublicJWKS(c.Request.Context())
	if err != nil {
		// Never include key material or the raw error detail in the response.
		slog.Error("oidc jwks unavailable", "error", err)
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "oidc signing key unavailable"})
		return
	}
	c.Header("Content-Type", "application/json")
	c.Header("Cache-Control", "public, max-age=3600")
	c.JSON(http.StatusOK, jwks)
}

// ── RP-Initiated Logout (OIDC Session Management) ──────────────────────────

// Logout handles GET /oauth/logout (OIDC Session Management §5).
//
// Query parameters:
//   - id_token_hint (optional): id_token previously issued to the RP. If
//     provided, we extract the client_id from the payload (without signature
//     verification) to validate the post_logout_redirect_uri.
//   - post_logout_redirect_uri (optional): where to redirect after logout.
//     MUST be in the client's logout_redirect_uris whitelist or we render an
//     inline error page (not a redirect, to avoid being an open redirector).
//   - state (optional): opaque value echoed back to the RP.
//
// Behavior:
//   - If post_logout_redirect_uri is valid → clear server-side session (if any)
//     → 302 to the RP with ?state=<state>.
//   - If post_logout_redirect_uri is invalid or missing → render success page.
//   - Never redirects to an untrusted URI (defense against open redirector).
//
// Cache-Control: no-store + Pragma: no-cache (sensitive flow, never cache).
func (h *OAuthProviderHandler) Logout(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")

	idTokenHint := strings.TrimSpace(c.Query("id_token_hint"))
	postLogoutRedirectURI := strings.TrimSpace(c.Query("post_logout_redirect_uri"))
	state := c.Query("state") // preserve exactly as sent

	var clientID string

	// If id_token_hint provided, extract client_id from payload (no signature verification)
	if idTokenHint != "" {
		parts := strings.Split(idTokenHint, ".")
		if len(parts) == 3 {
			// Decode payload (second part)
			payload, err := base64.RawURLEncoding.DecodeString(parts[1])
			if err == nil {
				var claims map[string]interface{}
				if json.Unmarshal(payload, &claims) == nil {
					if aud, ok := claims["aud"]; ok {
						// aud can be string or []string per OIDC Core §2
						switch v := aud.(type) {
						case string:
							clientID = v
						case []interface{}:
							if len(v) > 0 {
								if s, ok := v[0].(string); ok {
									clientID = s
								}
							}
						}
					}
				}
			}
		}
		if clientID == "" {
			slog.Warn("oidc logout: failed to extract client_id from id_token_hint")
		}
	}

	// If no post_logout_redirect_uri, render success page
	if postLogoutRedirectURI == "" {
		h.renderLogoutSuccessPage(c)
		return
	}

	// Validate redirect URI against client whitelist
	if clientID == "" {
		// No client_id → cannot validate → render error
		h.renderLogoutErrorPage(c, "invalid_request", "Cannot validate post_logout_redirect_uri without id_token_hint")
		return
	}

	valid, err := h.provider.ValidateLogoutRedirectURI(c.Request.Context(), clientID, postLogoutRedirectURI)
	if err != nil {
		slog.Error("oidc logout: whitelist check failed", "client_id", clientID, "error", err)
		h.renderLogoutErrorPage(c, "server_error", "Failed to validate redirect URI")
		return
	}

	if !valid {
		slog.Warn("oidc logout: post_logout_redirect_uri not in whitelist",
			"client_id", clientID, "uri", postLogoutRedirectURI)
		h.renderLogoutErrorPage(c, "invalid_request", "post_logout_redirect_uri not registered for this client")
		return
	}

	// Server-side session cleanup: revoke the grant referenced by id_token_hint
	// This invalidates all access/refresh tokens issued under that authorization grant.
	// Per OIDC Core §5, logout with id_token_hint should end the RP's session at the IdP.
	if idTokenHint != "" {
		parts := strings.Split(idTokenHint, ".")
		if len(parts) == 3 {
			payload, err := base64.RawURLEncoding.DecodeString(parts[1])
			if err == nil {
				var claims map[string]interface{}
				if json.Unmarshal(payload, &claims) == nil {
					// Extract both grant_id and sub (user_id) from id_token claims
					grantID, _ := claims["grant_id"].(string)
					subStr, _ := claims["sub"].(string)
					if grantID != "" && subStr != "" {
						if userID, err := strconv.ParseInt(subStr, 10, 64); err == nil {
							revokeErr := h.provider.RevokeGrant(c.Request.Context(), userID, grantID)
							if revokeErr != nil {
								slog.Warn("oidc logout: failed to revoke grant", "grant_id", grantID, "user_id", userID, "error", revokeErr)
								// Continue with logout - client-side revocation still succeeds
							} else {
								slog.Info("oidc logout: revoked grant", "grant_id", grantID, "user_id", userID, "client_id", clientID)
							}
						}
					}
				}
			}
		}
	}

	// Build redirect URL
	redirectURL, err := url.Parse(postLogoutRedirectURI)
	if err != nil {
		h.renderLogoutErrorPage(c, "invalid_request", "Malformed post_logout_redirect_uri")
		return
	}

	if state != "" {
		q := redirectURL.Query()
		q.Set("state", state)
		redirectURL.RawQuery = q.Encode()
	}

	c.Redirect(http.StatusFound, redirectURL.String())
}

// renderLogoutSuccessPage renders an inline HTML success page.
func (h *OAuthProviderHandler) renderLogoutSuccessPage(c *gin.Context) {
	html := `<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>登出成功 - Sakrylle API</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
               background: linear-gradient(135deg, #9181bd 0%, #7a6ba8 100%);
               margin: 0; padding: 0; display: flex; align-items: center; justify-content: center; min-height: 100vh; }
        .card { background: white; border-radius: 16px; box-shadow: 0 20px 60px rgba(0,0,0,0.3);
                padding: 48px; max-width: 400px; text-align: center; }
        .icon { font-size: 64px; margin-bottom: 24px; }
        h1 { color: #2d3748; font-size: 24px; margin: 0 0 16px; }
        p { color: #718096; font-size: 16px; line-height: 1.6; margin: 0; }
    </style>
</head>
<body>
    <div class="card">
        <div class="icon">✓</div>
        <h1>登出成功</h1>
        <p>您已安全登出 Sakrylle API。<br>可以关闭此页面。</p>
    </div>
</body>
</html>`
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}

// renderLogoutErrorPage renders an inline HTML error page (not a redirect).
func (h *OAuthProviderHandler) renderLogoutErrorPage(c *gin.Context, errorCode, errorDesc string) {
	// Use string concatenation to avoid fmt.Sprintf % escaping issues
	escapedCode := html.EscapeString(errorCode)
	escapedDesc := html.EscapeString(errorDesc)

	htmlContent := `<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>登出失败 - Sakrylle API</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
               background: linear-gradient(135deg, #9181bd 0%, #7a6ba8 100%);
               margin: 0; padding: 0; display: flex; align-items: center; justify-content: center; min-height: 100vh; }
        .card { background: white; border-radius: 16px; box-shadow: 0 20px 60px rgba(0,0,0,0.3);
                padding: 48px; max-width: 400px; text-align: center; }
        .icon { font-size: 64px; margin-bottom: 24px; color: #e53e3e; }
        h1 { color: #2d3748; font-size: 24px; margin: 0 0 16px; }
        .error-code { color: #e53e3e; font-family: monospace; font-size: 14px; margin: 16px 0 8px; }
        .error-desc { color: #718096; font-size: 14px; line-height: 1.6; margin: 0; }
    </style>
</head>
<body>
    <div class="card">
        <div class="icon">✗</div>
        <h1>登出失败</h1>
        <div class="error-code">` + escapedCode + `</div>
        <div class="error-desc">` + escapedDesc + `</div>
    </div>
</body>
</html>`
	c.Data(http.StatusBadRequest, "text/html; charset=utf-8", []byte(htmlContent))
}

// handlePromptNone handles OIDC prompt=none (silent authentication).
//
// Per OIDC Core §3.1.2.1, when prompt=none is specified:
//  - If the user has a valid session → skip consent UI, issue code directly
//  - If not authenticated → error=login_required
//  - If consent needed → error=consent_required
//  - Other issues → error=interaction_required
//
// This implementation checks for a valid JWT in the Authorization header or
// cookie. If valid, we auto-approve the authorization. Otherwise, redirect
// with the appropriate error.
func (h *OAuthProviderHandler) handlePromptNone(c *gin.Context, req *service.AuthorizeRequest) {
	redirectURI := req.RedirectURI
	state := req.State

	// Helper to redirect with OAuth error
	redirectError := func(errorCode, errorDesc string) {
		if redirectURI != "" {
			c.Redirect(http.StatusFound, buildOAuthErrorURL(redirectURI, errorCode, errorDesc, state))
		} else {
			renderOAuthInlineError(c, http.StatusBadRequest, fmt.Sprintf("%s: %s", errorCode, errorDesc))
		}
	}

	// Validate the request first (client_id, redirect_uri, etc.)
	client, err := h.provider.ValidateAuthorizeRequest(c.Request.Context(), req)
	if err != nil {
		// Bad client_id / redirect_uri → inline error (don't redirect to unverified URL)
		if errors.Is(err, service.ErrOAuthClientNotFound) ||
			errors.Is(err, service.ErrOAuthClientDisabled) ||
			errors.Is(err, service.ErrOAuthInvalidRedirectURI) {
			renderOAuthInlineError(c, http.StatusBadRequest, infraerrors.Message(err))
			return
		}
		// Other validation errors can be redirected
		redirectError("invalid_request", infraerrors.Message(err))
		return
	}

	// Check if authService is wired
	if h.authService == nil {
		slog.Warn("oidc prompt=none: authService not wired", "client_id", req.ClientID)
		redirectError("interaction_required", "silent authentication not available")
		return
	}

	// Extract user_id from JWT (check Authorization header or cookie)
	var userID int64
	var tokenString string

	// Try Authorization header first
	authHeader := c.GetHeader("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		tokenString = strings.TrimPrefix(authHeader, "Bearer ")
	}

	// If no header, try cookie
	if tokenString == "" {
		if tokenCookie, err := c.Cookie("token"); err == nil && tokenCookie != "" {
			tokenString = tokenCookie
		}
	}

	// Validate token and extract claims
	if tokenString != "" {
		if claims, err := h.authService.ValidateToken(tokenString); err == nil && claims != nil && claims.UserID > 0 {
			userID = claims.UserID
		}
	}

	// No valid session → login_required
	if userID == 0 {
		slog.Info("oidc prompt=none: no valid session", "client_id", req.ClientID)
		redirectError("login_required", "user is not authenticated")
		return
	}

	// User is authenticated. Per OIDC Core §3.1.2.1, prompt=none requires checking
	// for prior consent. If the user hasn't previously authorized this client for
	// these scopes, return consent_required.
	scopes := service.NormalizeScopes(req.Scopes)
	if len(scopes) == 0 {
		scopes = service.NormalizeScopes(client.DefaultScopes)
	}

	// Check for prior consent/grant for this user+client+scopes combination
	// Note: In production, implement CheckUserConsent in OAuthProviderService.
	// For now, we implement a simplified first-party trust policy:
	// - First-party clients (those without client_secret_hash) are auto-trusted
	// - Third-party clients require explicit consent (which would be stored in oauth_grants)
	if client.ClientSecretHash != "" {
		// Third-party client - should check oauth_grants table for prior consent
		// TODO: Implement proper consent tracking in oauth_grants table
		// For now, return consent_required for third-party clients with prompt=none
		slog.Info("oidc prompt=none: consent required for third-party client",
			"user_id", userID, "client_id", req.ClientID, "scopes", scopes)
		redirectError("consent_required", "user has not consented to these scopes")
		return
	}
	// First-party client (public client) - trusted, proceed with auto-approval

	// Resolve group for this user
	resolvedGroup, err := h.provider.ResolveOAuthGroup(c.Request.Context(), userID, client, nil)
	if err != nil {
		slog.Warn("oidc prompt=none: group resolution failed",
			"user_id", userID, "client_id", req.ClientID, "err", err)
		redirectError("interaction_required", "group selection required")
		return
	}

	// Generate authorization code directly
	codePlain, err := service.GenerateOpaqueToken(32)
	if err != nil {
		slog.Error("oidc prompt=none: failed to generate code",
			"user_id", userID, "client_id", req.ClientID, "err", err)
		redirectError("server_error", "failed to generate authorization code")
		return
	}

	now := time.Now()
	code := &service.OAuthCode{
		CodeHash:            service.HashOAuthToken(codePlain),
		ClientID:            client.ClientID,
		UserID:              userID,
		RedirectURI:         req.RedirectURI,
		Scopes:              scopes,
		CodeChallenge:       req.CodeChallenge,
		CodeChallengeMethod: req.CodeChallengeMethod,
		ExpiresAt:           now.Add(10 * time.Minute), // authCodeTTL
		GroupID:             &resolvedGroup,
		Nonce:               req.Nonce,
		CreatedAt:           now,
	}

	// Persist the code via the service's code repository
	if err := h.provider.CreateAuthorizationCode(c.Request.Context(), code); err != nil {
		slog.Error("oidc prompt=none: failed to persist code",
			"user_id", userID, "client_id", req.ClientID, "err", err)
		redirectError("server_error", "failed to issue authorization code")
		return
	}

	// Success: redirect to RP with code
	redirectURL, err := url.Parse(req.RedirectURI)
	if err != nil {
		redirectError("invalid_request", "malformed redirect_uri")
		return
	}

	q := redirectURL.Query()
	q.Set("code", codePlain)
	if state != "" {
		q.Set("state", state)
	}
	redirectURL.RawQuery = q.Encode()

	slog.Info("oidc prompt=none: code issued",
		"user_id", userID, "client_id", req.ClientID, "scopes", scopes)
	c.Redirect(http.StatusFound, redirectURL.String())
}

// discoveryIssuer resolves the discovery `issuer` URL.
//
// Source order (kept in lockstep with §12.7 device flow verification_uri via
// SettingService.GetOAuthIssuer so the two endpoints cannot disagree):
//  1. settings.oauth_issuer  (canonical; per migration 145 deployments MUST set)
//  2. settings.frontend_url  (legacy fallback)
//  3. request scheme://host  (last-resort fallback for misconfigured deployments)
//
// Always strips a trailing slash because RFC 8414 forbids it on the issuer.
// The X-Forwarded-Proto header is allowlisted to {http, https} so a malformed
// or hostile proxy header (e.g. "javascript") cannot land inside the issuer
// URL we publish in discovery and trust elsewhere.
func (h *OAuthProviderHandler) discoveryIssuer(c *gin.Context) string {
	if h.settings != nil {
		if v, _ := h.settings.GetOAuthIssuer(c.Request.Context()); v != "" {
			return v
		}
	}
	scheme := "https"
	if c != nil && c.Request != nil {
		if c.Request.TLS == nil && c.Request.Header.Get("X-Forwarded-Proto") != "https" {
			scheme = "http"
		}
		// Allowlist X-Forwarded-Proto to {http, https} so a malformed or
		// hostile proxy header (e.g. "javascript") cannot land inside the
		// issuer URL we publish in discovery and trust elsewhere.
		if proto := c.Request.Header.Get("X-Forwarded-Proto"); proto == "http" || proto == "https" {
			scheme = proto
		}
		if host := c.Request.Host; host != "" {
			return strings.TrimRight(scheme+"://"+host, "/")
		}
	}
	return ""
}

// ── Revocation (§12.9) ──────────────────────────────────────────────────────

// Revoke handles POST /oauth/revoke (RFC 7009).
//
// Wire-level contract:
//   - form-encoded body only; JSON / missing Content-Type → invalid_request.
//   - response is HTTP 200 with empty body on success, including for unknown,
//     already-revoked, wrong-client tokens (idempotency, RFC 7009 §2.2).
//   - confidential client missing/wrong secret → 401 invalid_client.
//
// Cache-Control: no-store + Pragma: no-cache always. Per §12.1, sensitive
// OAuth responses must never be cached.
func (h *OAuthProviderHandler) Revoke(c *gin.Context) {
	if !h.provider.IsEnabled(c.Request.Context()) {
		writeOAuthError(c, http.StatusServiceUnavailable, "temporarily_unavailable", "oauth provider disabled")
		return
	}
	// §12.9: form-encoded only.
	if !isFormEncoded(c) {
		writeOAuthError(c, http.StatusBadRequest, "invalid_request", "Content-Type must be application/x-www-form-urlencoded")
		return
	}
	if err := c.Request.ParseForm(); err != nil {
		writeOAuthError(c, http.StatusBadRequest, "invalid_request", "form parse failed")
		return
	}

	clientID, clientSecret := extractClientCredentials(c)
	tokenPlain := strings.TrimSpace(c.Request.PostFormValue("token"))
	hint := strings.TrimSpace(c.Request.PostFormValue("token_type_hint"))

	if tokenPlain == "" {
		// RFC 7009 §2.1: server MAY return 200 even without `token`. Treat as
		// success to keep the endpoint idempotent and avoid hint-leakage.
		writeRevokeSuccess(c)
		return
	}

	err := h.provider.RevokeAccessOrRefreshToken(c.Request.Context(), clientID, clientSecret, tokenPlain, hint)
	if err != nil {
		// RFC 7009: only invalid_client is allowed to surface; everything else
		// is idempotent success. The service layer already swallows token-
		// existence/cross-client errors.
		if errors.Is(err, service.ErrOAuthClientAuthFailed) {
			c.Header("Cache-Control", "no-store")
			c.Header("Pragma", "no-cache")
			c.Header("Content-Type", "application/json")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":             "invalid_client",
				"error_description": "client authentication failed",
			})
			return
		}
		slog.Warn("oauth: revoke unexpected error", "err", err)
		// Fall through to idempotent 200 — leaking server-side bugs through a
		// revoke response would tell attackers which tokens triggered DB errors.
	}
	writeRevokeSuccess(c)
}

// writeRevokeSuccess emits §12.9's empty 200 with no-cache headers.
// RFC 7009 §2.2 requires an empty response body on success.
func writeRevokeSuccess(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.Status(http.StatusOK)
}

// isFormEncoded reports whether the request Content-Type is
// application/x-www-form-urlencoded (with or without parameters).
func isFormEncoded(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	ct := strings.ToLower(strings.TrimSpace(c.Request.Header.Get("Content-Type")))
	if ct == "" {
		return false
	}
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	return ct == "application/x-www-form-urlencoded"
}

// ── Authorized Apps (§12.10) ────────────────────────────────────────────────

// ListAuthorizedApps serves GET /api/v1/oauth/authorized-apps.
//
// JWT-protected. Returns one row per (user, grant_id), with client/group
// names joined for display. Per §12.10, never expose another user's grants.
// The query in the service layer is owner-scoped; this handler only re-asserts
// the auth subject so a stray test that bypasses the JWT middleware can't
// leak data.
func (h *OAuthProviderHandler) ListAuthorizedApps(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	grants, err := h.provider.ListAuthorizedAppsForUser(c.Request.Context(), subject.UserID)
	if err != nil {
		slog.Warn("oauth: list authorized apps failed",
			"user_id", subject.UserID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":             "server_error",
			"error_description": "failed to load authorized apps",
		})
		return
	}
	out := make([]gin.H, 0, len(grants))
	for _, g := range grants {
		row := gin.H{
			"grant_id":                   g.GrantID,
			"client_id":                  g.ClientID,
			"client_name":                g.ClientName,
			"client_disabled":            g.ClientDisabled,
			"app_type":                   g.AppType,
			"icon_url":                   stringPtrOrNil(g.IconURL),
			"device_id":                  stringPtrOrNil(g.DeviceID),
			"device_name":                stringPtrOrNil(g.DeviceName),
			"group_id":                   g.GroupID,
			"group_name":                 g.GroupName,
			"scopes":                     emptyStringSlice(g.Scopes),
			"first_authorized_at":        g.FirstAuthorizedAt,
			"last_used_at":               g.LastUsedAt,
			"last_used_ip":               stringPtrOrNil(g.LastUsedIP),
			"active_access_token_count":  g.ActiveAccessTokenCount,
			"active_refresh_token_count": g.ActiveRefreshTokenCount,
			"status":                     defaultGrantStatus(g.Status),
		}
		out = append(out, row)
	}
	c.JSON(http.StatusOK, gin.H{"items": out})
}

// RevokeAuthorizedApp serves DELETE /api/v1/oauth/authorized-apps/:grant_id.
//
// Per §12.10, ownership is checked in the service layer (`RevokeGrant`
// returns nil for non-existent or other-user grants). The handler returns
// 200 in both cases — exposing whether a grant exists for a different user
// would let an attacker enumerate other users' device/grant IDs.
func (h *OAuthProviderHandler) RevokeAuthorizedApp(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	grantID := strings.TrimSpace(c.Param("grant_id"))
	if grantID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_request",
			"error_description": "grant_id is required",
		})
		return
	}
	if err := h.provider.RevokeGrant(c.Request.Context(), subject.UserID, grantID); err != nil {
		slog.Warn("oauth: revoke authorized app failed",
			"user_id", subject.UserID, "grant_id", grantID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":             "server_error",
			"error_description": "failed to revoke authorized app",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"revoked": true})
}

// RevokeAuthorizedClient serves DELETE
// /api/v1/oauth/authorized-apps/client/:client_id.
//
// Revokes every grant the current user has for the named client. Idempotent:
// returns the count of api_keys disabled (0 when the user has no grants for
// that client; matches §12.10 contract).
func (h *OAuthProviderHandler) RevokeAuthorizedClient(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	clientID := strings.TrimSpace(c.Param("client_id"))
	if clientID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_request",
			"error_description": "client_id is required",
		})
		return
	}
	count, err := h.provider.RevokeClientAuthorizations(c.Request.Context(), subject.UserID, clientID)
	if err != nil {
		slog.Warn("oauth: revoke authorized client failed",
			"user_id", subject.UserID, "client_id", clientID, "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":             "server_error",
			"error_description": "failed to revoke authorized client",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"revoked": count})
}

// ── helpers ─────────────────────────────────────────────────────────────────

// stringPtrOrNil returns the dereferenced pointer or nil for JSON
// serialization, so a missing optional field renders as JSON `null` rather
// than empty string.
func stringPtrOrNil(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// stringPtrIfNotEmpty returns a pointer to s if s is non-empty, otherwise nil.
// Used to convert optional string form fields to *string service params.
func stringPtrIfNotEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// emptyStringSlice ensures `scopes: []` is JSON-encoded as `[]` not `null`,
// which matters for the frontend `Array.from` calls.
func emptyStringSlice(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// defaultGrantStatus normalizes blank status values to "active". The service
// layer fills this in for live grants; legacy rows that pre-date the column
// default to active when surfaced.
func defaultGrantStatus(s string) string {
	if strings.TrimSpace(s) == "" {
		return "active"
	}
	return s
}
