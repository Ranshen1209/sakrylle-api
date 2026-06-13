package handler

import (
	"context"
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
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
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
	// apiKeyService validates Bearer tokens for the /userinfo endpoint.
	// Nil-safe: when unset /userinfo returns 503.
	apiKeyService APIKeyLookup
}

// APIKeyLookup is the minimal interface for Bearer token validation on /userinfo.
type APIKeyLookup interface {
	GetByKey(ctx context.Context, key string) (*service.APIKey, error)
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

// SetAPIKeyService wires the API key lookup for the /userinfo endpoint.
// When unset, /userinfo returns 503.
func (h *OAuthProviderHandler) SetAPIKeyService(s APIKeyLookup) {
	if h != nil {
		h.apiKeyService = s
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
		// OIDC §5.5: voluntary claims request.
		Claims:              service.ParseClaimsParameter(formVal("claims")),
	}

	// OIDC §6: request / request_uri parameter.
	// When present, the request object JWT overrides query parameters.
	// Per OIDC Core §6.1, state, nonce, and prompt MUST remain in the query
	// even when a request object is used.
	if requestParam := formVal("request"); requestParam != "" {
		issuer := h.discoveryIssuer(c)
		// Parse the request object JWT. ParseRequestObjectJWT is currently
		// called with an empty client_secret, so it only passes when the
		// client supplies its own verifiable material; unsigned (none) and
		// empty-secret JWTs are rejected. Real verification wiring
		// (client_secret for HS, client JWKS for RS/ES) is deferred — until
		// then discovery advertises request/request_uri as unsupported.
		reqObj, err := service.ParseRequestObjectJWT(requestParam, issuer, req.ClientID, "")
		if err != nil {
			slog.Warn("oidc: request object validation failed", "error", err)
			redirectOAuthProviderError(c, req.RedirectURI, req.State,
				err)
			return
		}
		// Merge: request object takes precedence for most params, but
		// state/nonce/prompt from the query are preserved.
		req.RedirectURI = reqObj.RedirectURI
		req.ResponseType = reqObj.ResponseType
		req.Scopes = reqObj.Scopes
		req.CodeChallenge = reqObj.CodeChallenge
		req.CodeChallengeMethod = reqObj.CodeChallengeMethod
		if reqObj.Claims != nil {
			req.Claims = reqObj.Claims
		}
		// nonce: request object may provide one; query nonce preserved if not
		if reqObj.Nonce != "" && req.Nonce == "" {
			req.Nonce = reqObj.Nonce
		}
	}
	if requestURIParam := formVal("request_uri"); requestURIParam != "" {
		issuer := h.discoveryIssuer(c)
		// Look up the client to get its registered request_uris whitelist.
		ruClient, lookupErr := h.provider.LookupClient(c.Request.Context(), req.ClientID)
		if lookupErr != nil {
			redirectOAuthProviderError(c, req.RedirectURI, req.State, lookupErr)
			return
		}
		// Fetch via the SSRF-safe client; FetchRequestURI enforces https-only,
		// the client's request_uris host whitelist, timeout and a 64KB cap.
		rawObj, fetchErr := service.FetchRequestURI(requestURIParam, ruClient.RequestURIs)
		if fetchErr != nil {
			slog.Warn("oidc: request_uri fetch failed", "error", fetchErr)
			redirectOAuthProviderError(c, req.RedirectURI, req.State, fetchErr)
			return
		}
		// Verify the fetched request object exactly like the inline `request`
		// param path (HS/client_secret or unsigned), then merge.
		reqObj, parseErr := service.ParseRequestObjectJWT(rawObj, issuer, req.ClientID, "")
		if parseErr != nil {
			slog.Warn("oidc: request_uri object validation failed", "error", parseErr)
			redirectOAuthProviderError(c, req.RedirectURI, req.State, parseErr)
			return
		}
		req.RedirectURI = reqObj.RedirectURI
		req.ResponseType = reqObj.ResponseType
		req.Scopes = reqObj.Scopes
		req.CodeChallenge = reqObj.CodeChallenge
		req.CodeChallengeMethod = reqObj.CodeChallengeMethod
		if reqObj.Claims != nil {
			req.Claims = reqObj.Claims
		}
		if reqObj.Nonce != "" && req.Nonce == "" {
			req.Nonce = reqObj.Nonce
		}
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
		SID:                 service.GenerateSessionID(),
	}
	result, err := h.provider.BeginAuthorizeTransaction(c.Request.Context(), params)
	if err != nil {
		slog.Warn("oauth: begin authorize transaction failed",
			"client_id", params.ClientID,
			"user_id", params.UserID,
			"redirect_uri", params.RedirectURI,
			"err", err,
			"err_type", fmt.Sprintf("%T", err))
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

// ── Introspection (RFC 7662) ────────────────────────────────────────────────

// Introspect implements POST /oauth/introspect per RFC 7662.
//
// Only confidential clients (those with client_secret_hash) may call this
// endpoint. The client authenticates via client_secret_basic or
// client_secret_post (same as /oauth/token).
//
// Request: token (required), token_type_hint (optional)
// Response: {active, scope, client_id, token_type, sub, exp, iat, iss} or {active: false}
func (h *OAuthProviderHandler) Introspect(c *gin.Context) {
	if !h.provider.IsEnabled(c.Request.Context()) {
		c.JSON(http.StatusForbidden, gin.H{"error": "oauth_provider_disabled"})
		return
	}

	// Authenticate the client.
	clientID, clientSecret, ok := c.Request.BasicAuth()
	if !ok {
		// Try client_secret_post
		clientID = c.PostForm("client_id")
		clientSecret = c.PostForm("client_secret")
	}
	if clientID == "" || clientSecret == "" {
		c.Header("WWW-Authenticate", `Basic realm="oauth"`)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_client", "error_description": "client authentication required"})
		return
	}

	client, err := h.authenticateClientForIntrospect(c.Request.Context(), clientID, clientSecret)
	if err != nil {
		c.JSON(httpStatusForOAuthError(err), gin.H{
			"error":             oauthErrorReason(err),
			"error_description": infraerrors.Message(err),
		})
		return
	}

	// Only confidential clients may introspect.
	if !client.ClientConfidential {
		c.JSON(http.StatusForbidden, gin.H{
			"error":             "invalid_client",
			"error_description": "only confidential clients may call introspect",
		})
		return
	}

	token := strings.TrimSpace(c.PostForm("token"))
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_request",
			"error_description": "token parameter is required",
		})
		return
	}

	resp, err := h.provider.IntrospectToken(c.Request.Context(), client.ClientID, token)
	if err != nil {
		slog.Error("oauth: introspect failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server_error"})
		return
	}

	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.JSON(http.StatusOK, resp)
}

// authenticateClientForIntrospect verifies client credentials for the
// introspect endpoint. Similar to authenticateClient but returns the client
// struct directly.
func (h *OAuthProviderHandler) authenticateClientForIntrospect(ctx context.Context, clientID, clientSecret string) (*service.OAuthClient, error) {
	client, err := h.provider.LookupClient(ctx, clientID)
	if err != nil {
		return nil, err
	}
	if client.Disabled {
		return nil, service.ErrOAuthClientDisabled
	}
	if client.ClientSecretHash == "" {
		return nil, service.ErrOAuthClientNotConfidential
	}
	if err := bcrypt.CompareHashAndPassword([]byte(client.ClientSecretHash), []byte(clientSecret)); err != nil {
		return nil, service.ErrOAuthClientAuthFailed
	}
	return client, nil
}

// ── Discovery (§12.1) ───────────────────────────────────────────────────────

// commonDiscoveryMetadata builds the shared discovery fields used by both
// RFC 8414 (OAuth Authorization Server Metadata) and OIDC Discovery 1.0
// (OpenID Connect Discovery). Extracting them into a single source prevents
// the two documents from drifting apart.
func (h *OAuthProviderHandler) commonDiscoveryMetadata(issuer string) gin.H {
	return gin.H{
		"issuer":                                issuer,
		"authorization_endpoint":                issuer + "/oauth/authorize",
		"token_endpoint":                        issuer + "/oauth/token",
		"userinfo_endpoint":                     issuer + "/userinfo",
		"jwks_uri":                              issuer + "/.well-known/jwks.json",
		"end_session_endpoint":                  issuer + "/oauth/logout",
		// device_authorization_endpoint (RFC 8628 §4) is shared between both
		// discovery documents: OIDC clients that read only
		// /.well-known/openid-configuration (e.g. the Sakrylle CLI with
		// --device-auth) must be able to discover it, and grant_types_supported
		// below already advertises the device_code grant in both docs.
		"device_authorization_endpoint":         issuer + "/oauth/device/code",
		"response_types_supported":              []string{"code"},
		"response_modes_supported":              []string{"query"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token", "urn:ietf:params:oauth:grant-type:device_code"},
		"code_challenge_methods_supported":      []string{"S256"},
		"scopes_supported":                      canonicalScopesForDiscovery,
		"token_endpoint_auth_methods_supported": []string{"none", "client_secret_basic", "client_secret_post"},
		"claims_supported": []string{
			"iss", "sub", "aud", "exp", "iat", "nonce",
			"name", "preferred_username", "email", "email_verified",
			"auth_time",
		},
		"prompt_values_supported": []string{"none", "login", "consent", "select_account"},
		"service_documentation":   "https://doc.sakrylle.com/developers/oauth/",
	}
}

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
	resp := h.commonDiscoveryMetadata(issuer)
	resp["revocation_endpoint"] = issuer + "/oauth/revoke"
	// device_authorization_endpoint now comes from commonDiscoveryMetadata so
	// both discovery documents advertise it consistently.
	resp["ui_locales_supported"] = []string{"zh-CN", "en"}
	resp["revocation_endpoint_auth_methods_supported"] = []string{"none", "client_secret_basic", "client_secret_post"}
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
//
// Fields intentionally omitted because they are not implemented:
//   - userinfo_signing_alg_values_supported (UserInfo supports both unsigned JSON and signed JWT)
//
// request_parameter_supported and request_uri_parameter_supported are both
// advertised false: while the inline `request` param (ParseRequestObjectJWT)
// and the `request_uri` param (FetchRequestURI via the SSRF-safe client + the
// same verify/merge path) have their fetch+merge logic implemented, request
// object signature verification is not yet wired for real clients. Both paths
// call ParseRequestObjectJWT with an empty client_secret, which rejects every
// real client: HS algs need a non-empty client_secret, RS/ES need a client
// JWKS (not stored on oauth_clients), and none is refused. Until verification
// is wired (needs a client JWKS column / migration), we do not advertise
// support so RPs are not misled.
//
// claims_parameter_supported is advertised because the server accepts, parses,
// validates, and persists the §5.5 claims parameter (ParseClaimsParameter +
// oauth_authorize_transactions.claims) and can enforce it via
// service.ApplyClaimsConstraints. Note: UserInfo-side filtering is not yet
// active because claims are not propagated onto the access token; see
// ApplyClaimsConstraints doc. The parameter is honored without error.
func (h *OAuthProviderHandler) OpenIDConfiguration(c *gin.Context) {
	// Return 404 when OIDC signing is not wired — prevents advertising
	// capabilities the server cannot honor (JWKS would 503, id_token empty).
	if h.oidcKeys == nil {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusNotFound, gin.H{"error": "OIDC not enabled"})
		return
	}
	issuer := h.discoveryIssuer(c)
	resp := h.commonDiscoveryMetadata(issuer)
	resp["subject_types_supported"] = []string{"public", "pairwise"}
	resp["id_token_signing_alg_values_supported"] = []string{"RS256", "ES256"}
	resp["userinfo_signing_alg_values_supported"] = []string{"RS256", "ES256"}
	resp["request_parameter_supported"] = false
	resp["request_uri_parameter_supported"] = false
	resp["claims_parameter_supported"] = true
	resp["backchannel_logout_supported"] = true
	resp["backchannel_logout_session_supported"] = true
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

// ── OIDC UserInfo endpoint (§5.3) ──────────────────────────────────────────

// UserInfo handles GET /userinfo (OpenID Connect Core 1.0 §5.3).
//
// This is a dedicated OIDC UserInfo endpoint that validates the Bearer token
// directly, bypassing the /v1 gateway middleware chain (no billing, balance,
// quota, or group-assignment checks). Only OAuth access tokens (sk_oauth_) are
// accepted; manual API keys receive 401.
//
// The endpoint verifies:
//   - Bearer token is present and starts with sk_oauth_
//   - API key exists, is active, and not expired
//   - User is active (not disabled)
//   - OAuth access metadata is loadable
//
// It deliberately does NOT check: balance, quota, subscription status, or
// group assignment. OIDC UserInfo is an identity endpoint, not a billing one.
//
// Response fields are cropped by granted scopes (same field families as /v1/me).
func (h *OAuthProviderHandler) UserInfo(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")

	if h.provider == nil || !h.provider.IsEnabled(c.Request.Context()) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "OAuth provider not enabled"})
		return
	}
	if h.apiKeyService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "UserInfo not available"})
		return
	}

	// Extract Bearer token from Authorization header.
	authHeader := c.GetHeader("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		c.Header("WWW-Authenticate", `Bearer realm="sakrylle", error="invalid_token", error_description="Bearer token required"`)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token", "error_description": "Bearer token required"})
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" {
		c.Header("WWW-Authenticate", `Bearer realm="sakrylle", error="invalid_token"`)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token"})
		return
	}

	// Only OAuth access tokens are valid for UserInfo.
	if !service.IsOAuthAccessToken(token) {
		c.Header("WWW-Authenticate", `Bearer realm="sakrylle", error="invalid_token", error_description="OAuth access token required"`)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token", "error_description": "OAuth access token required"})
		return
	}

	ctx := c.Request.Context()

	// Look up the API key. This also loads the associated User.
	apiKey, err := h.apiKeyService.GetByKey(ctx, token)
	if err != nil || apiKey == nil {
		c.Header("WWW-Authenticate", `Bearer realm="sakrylle", error="invalid_token"`)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token"})
		return
	}

	// Validate: key must be active (not disabled, not expired, not quota-exhausted).
	if apiKey.Status != service.StatusAPIKeyActive {
		c.Header("WWW-Authenticate", `Bearer realm="sakrylle", error="invalid_token"`)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token", "error_description": "token is not active"})
		return
	}
	if apiKey.IsExpired() {
		c.Header("WWW-Authenticate", `Bearer realm="sakrylle", error="invalid_token"`)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token", "error_description": "token expired"})
		return
	}

	// Validate user exists.
	if apiKey.User == nil {
		c.Header("WWW-Authenticate", `Bearer realm="sakrylle", error="invalid_token"`)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token", "error_description": "user account not found"})
		return
	}

	user := apiKey.User

	// Load OAuth access metadata for scope cropping.
	meta, err := h.provider.LoadOAuthAccessMetadata(ctx, apiKey.ID)
	if err != nil {
		c.Header("WWW-Authenticate", `Bearer realm="sakrylle", error="invalid_token"`)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_token", "error_description": "failed to load token metadata"})
		return
	}
	if meta == nil {
		// Legacy OAuth token without access metadata row. Return a minimal
		// OIDC UserInfo: only auth_type marker, no claims (no scopes known).
		c.JSON(http.StatusOK, gin.H{
			"auth_type": "oauth",
			"sub":       strconv.FormatInt(user.ID, 10),
		})
		return
	}

	// Build the OIDC UserInfo response. Uses the same field families as
	// assembleOAuthMe but without the account-info handler dependency.
	scopes := meta.Scopes
	resp := gin.H{
		"auth_type": "oauth",
	}

	// OIDC standard claims.
	if service.HasScope(scopes, service.ScopeOpenID) {
		// Resolve the sub claim: public = user ID string, pairwise = per-client pseudonym.
		sub := strconv.FormatInt(user.ID, 10)
		if client, lookupErr := h.provider.LookupClient(ctx, meta.ClientID); lookupErr == nil && client != nil && client.SubjectType == "pairwise" {
			issuer := h.discoveryIssuer(c)
			pw, pwErr := service.ResolvePairwiseSub(issuer, user.ID, client.SubjectType, client.SectorIdentifierURI, client.RedirectURIs)
			if pwErr != nil {
				// Fail closed: do not emit a sub computed on an inconsistent
				// basis when the sector_identifier_uri cannot be resolved.
				c.JSON(http.StatusInternalServerError, gin.H{"error": "server_error", "error_description": "failed to resolve subject identifier"})
				return
			}
			if pw != "" {
				sub = pw
			}
		}
		resp["sub"] = sub
		if service.HasScope(scopes, service.ScopeProfile) {
			resp["name"] = user.Username
			resp["preferred_username"] = user.Username
		}
		if service.HasScope(scopes, service.ScopeEmail) {
			resp["email"] = user.Email
			resp["email_verified"] = user.EmailVerified
		}
	}

	// Commercial scopes: profile block.
	if service.HasScope(scopes, service.ScopeProfileRead) {
		userBlock := gin.H{
			"id":           user.ID,
			"username":     user.Username,
			"display_name": user.Username,
			"avatar_url":   nullableString(user.AvatarURL),
			"locale":       "zh-CN",
		}
		if service.HasScope(scopes, service.ScopeEmailRead) {
			userBlock["email"] = user.Email
		}
		resp["user"] = userBlock
	}

	// Commercial scopes: account block.
	if service.HasScope(scopes, service.ScopeAccountBalanceRead) || service.HasScope(scopes, service.ScopeAccountRead) {
		resp["account"] = gin.H{
			"credit_remaining": user.Balance,
			"currency_display": "CNY",
			"currency_symbol":  "￥",
		}
	}

	// OAuth metadata block.
	resp["oauth"] = gin.H{
		"client_id":   meta.ClientID,
		"app_type":    meta.AppType,
		"grant_id":    meta.GrantID,
		"device_id":   nullableStringPtr(meta.DeviceID),
		"device_name": nullableStringPtr(meta.DeviceName),
		"expires_at":  meta.ExpiresAt,
	}
	resp["granted_scopes"] = func() []string {
		out := service.NormalizeScopes(scopes)
		if out == nil {
			return []string{}
		}
		return out
	}()


		// OIDC Core §5.3.2: Signed UserInfo. When the RP requests
		// Accept: application/jwt, return a signed JWT instead of plain JSON.
		// Only available when OIDC keys are wired and openid scope was granted.
		acceptHeader := c.GetHeader("Accept")
		if acceptHeader == "application/jwt" && h.oidcKeys != nil && service.HasScope(scopes, service.ScopeOpenID) {
			sub, _ := resp["sub"].(string)
			username := user.Username
			email := user.Email
			// Gate email claim by scope: only include email when email scope was granted.
			emailForJWT := ""
			emailVerifiedForJWT := false
			if service.HasScope(scopes, service.ScopeEmail) {
				emailForJWT = email
				emailVerifiedForJWT = user.EmailVerified
			}
			claims := service.BuildUserInfoJWTClaims(
				h.discoveryIssuer(c), meta.ClientID,
				sub, username, emailForJWT, emailVerifiedForJWT,
				time.Now(), 0,
			)
			// Use client's signing algorithm; default to RS256 when unknown.
			alg := service.SigningAlgRS256
			if client, lookupErr := h.provider.LookupClient(ctx, meta.ClientID); lookupErr == nil && client != nil {
				alg = service.SigningAlgorithm(client.SigningAlgorithm)
				if alg != service.SigningAlgRS256 && alg != service.SigningAlgES256 {
					alg = service.SigningAlgRS256
				}
			}
			signed, signErr := h.oidcKeys.Sign(claims, alg)
			if signErr != nil {
				slog.Error("oidc userinfo jwt signing failed", "error", signErr)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "server_error", "error_description": "failed to sign UserInfo JWT"})
				return
			}
			c.Header("Content-Type", "application/jwt")
			c.String(http.StatusOK, signed)
			return
		}
	c.JSON(http.StatusOK, resp)
}

// ── RP-Initiated Logout (OIDC Session Management) ──────────────────────────

// Logout handles GET/POST /oauth/logout (OIDC Session Management §5).
//
// Query parameters:
//   - id_token_hint (recommended): previously issued id_token. When provided,
//     the JWT signature is verified using the published JWKS keys (RS256/ES256)
//     and the iss/aud/exp claims are validated. The aud claim identifies the
//     client for post_logout_redirect_uri whitelist validation.
//   - post_logout_redirect_uri (optional): where to redirect after logout.
//     MUST match an entry in the client's logout_redirect_uris whitelist.
//   - state (optional): opaque value echoed back to the RP.
//
// Behavior:
//   - id_token_hint is cryptographically verified (signature + claims).
//     Forged or expired tokens are rejected — they cannot influence the
//     redirect URI validation.
//   - Without id_token_hint, post_logout_redirect_uri is NOT accepted
//     (we cannot determine the client to validate against).
//   - Valid post_logout_redirect_uri → 302 redirect with ?state=<state>.
//   - Invalid/missing redirect URI → inline success/error page.
//   - Never redirects to an untrusted URI (open redirector defense).
//
// Server-side session cleanup: this endpoint renders a logout page and
// optionally redirects the user-agent. It does NOT perform server-side token
// revocation — RPs that need to revoke tokens should call POST /oauth/revoke
// (RFC 7009). This matches the OIDC Session Management spec where RP-Initiated
// Logout is primarily an RP-to-OP signal to clear OP session state.
//
// Cache-Control: no-store + Pragma: no-cache (sensitive flow, never cache).
func (h *OAuthProviderHandler) Logout(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")

	idTokenHint := strings.TrimSpace(c.Query("id_token_hint"))
	postLogoutRedirectURI := strings.TrimSpace(c.Query("post_logout_redirect_uri"))
	state := c.Query("state")

	var clientID string
	var sub string // verified sub from id_token_hint, used for back-channel logout
	var sid string // verified sid from id_token_hint, echoed in logout_token

	// When id_token_hint is provided, verify its signature and extract claims.
	if idTokenHint != "" {
		cid, verifiedSub, verifiedSid, err := h.verifyLogoutIDToken(c.Request.Context(), idTokenHint)
		if err != nil {
			slog.Warn("oidc logout: id_token_hint verification failed", "error", err)
			h.renderLogoutErrorPage(c, "invalid_request", "id_token_hint is invalid or expired")
			return
		}
		clientID = cid
		sub = verifiedSub
		sid = verifiedSid
	}

	// Without a verified id_token_hint, we cannot validate the redirect URI
	// against a client whitelist. Render success page (no redirect).
	if postLogoutRedirectURI == "" {
		h.renderLogoutSuccessPage(c)
		// OIDC Back-Channel Logout 1.0: asynchronously notify clients
		// that have registered a backchannel_logout_uri.
		if clientID != "" {
			h.dispatchBackchannelLogout(c, clientID, sub, sid)
		}
		return
	}

	if clientID == "" {
		// post_logout_redirect_uri provided but no (or invalid) id_token_hint.
		// Cannot validate the redirect URI without knowing the client.
		h.renderLogoutErrorPage(c, "invalid_request",
			"A valid id_token_hint is required to redirect after logout")
		return
	}

	// Validate post_logout_redirect_uri against the client's whitelist.
	valid, err := h.provider.ValidateLogoutRedirectURI(c.Request.Context(), clientID, postLogoutRedirectURI)
	if err != nil {
		slog.Error("oidc logout: redirect URI whitelist check failed",
			"client_id", clientID, "error", err)
		h.renderLogoutErrorPage(c, "server_error", "Failed to validate redirect URI")
		return
	}
	if !valid {
		slog.Warn("oidc logout: post_logout_redirect_uri not in client whitelist",
			"client_id", clientID, "uri", postLogoutRedirectURI)
		h.renderLogoutErrorPage(c, "invalid_request",
			"post_logout_redirect_uri is not registered for this client")
		return
	}

	// Build redirect URL with optional state.
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

	slog.Info("oidc logout: redirecting after logout",
		"client_id", clientID,
		"redirect_uri", postLogoutRedirectURI)

	// OIDC Back-Channel Logout 1.0: asynchronously notify clients
	// that have registered a backchannel_logout_uri.
	h.dispatchBackchannelLogout(c, clientID, sub, sid)

	c.Redirect(http.StatusFound, redirectURL.String())
}

// verifyLogoutIDToken verifies the id_token_hint JWT signature and claims.
// Returns the client_id (from aud) if valid, or an error.
//
// Verification steps:
//  1. Parse the JWT without verifying to extract the kid header.
//  2. Verify the signature using the published JWKS keys (RS256/ES256).
//  3. Validate iss matches our issuer.
//  4. Validate aud contains a recognizable client_id.
//  5. exp is checked by the JWT library (jwt.WithLeeway allows small clock skew).
func (h *OAuthProviderHandler) verifyLogoutIDToken(ctx context.Context, rawToken string) (string, string, string, error) {
	if h.oidcKeys == nil {
		return "", "", "", errors.New("OIDC key service not available")
	}

	// Get the expected issuer for validation.
	issuer := h.discoveryIssuerForLogout(ctx)

	// Parse and verify the JWT. We use jwt.Parse with a keyfunc that
	// resolves the signing key by kid from the JWKS.
	parsed, err := jwt.Parse(rawToken, func(t *jwt.Token) (any, error) {
		// Validate the algorithm header matches what we support.
		alg := t.Method.Alg()
		if alg != "RS256" && alg != "ES256" {
			return nil, fmt.Errorf("oidc logout: unsupported signing algorithm %q", alg)
		}

		// Get the kid from the token header.
		kid, _ := t.Header["kid"].(string)

		// Look up the verification key by kid from our JWKS.
		verifyKey, keyType, err := h.oidcKeys.GetVerificationKey(ctx, kid)
		if err != nil {
			return nil, fmt.Errorf("oidc logout: key not found for kid %q: %w", kid, err)
		}

		// Return the correct key type based on algorithm.
		switch keyType {
		case service.SigningKeyTypeRSA:
			if alg != "RS256" {
				return nil, fmt.Errorf("oidc logout: algorithm %q does not match key type RSA", alg)
			}
			return verifyKey, nil
		case service.SigningKeyTypeEC:
			if alg != "ES256" {
				return nil, fmt.Errorf("oidc logout: algorithm %q does not match key type EC", alg)
			}
			return verifyKey, nil
		default:
			return nil, fmt.Errorf("oidc logout: unknown key type %q for kid %q", keyType, kid)
		}
	}, jwt.WithLeeway(30*time.Second))

	if err != nil {
		return "", "", "", fmt.Errorf("id_token_hint verification failed: %w", err)
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok || !parsed.Valid {
		return "", "", "", errors.New("id_token_hint claims invalid")
	}

	// Validate iss.
	if iss, ok := claims["iss"].(string); !ok || iss != issuer {
		return "", "", "", fmt.Errorf("id_token_hint iss mismatch: got %q, want %q", iss, issuer)
	}

	// Extract client_id from aud (string or []string).
	var clientID string
	switch v := claims["aud"].(type) {
	case string:
		clientID = v
	case []any:
		if len(v) > 0 {
			if s, ok := v[0].(string); ok {
				clientID = s
			}
		}
	}
	if clientID == "" {
		return "", "", "", errors.New("id_token_hint has no valid aud claim")
	}

	// Extract sub and sid from the verified claims for back-channel logout.
	sub, _ := claims["sub"].(string)
	sid, _ := claims["sid"].(string)
	return clientID, sub, sid, nil
}

// discoveryIssuerForLogout resolves the issuer for id_token_hint validation.
// Mirrors discoveryIssuer() but with a context parameter for the key service.
func (h *OAuthProviderHandler) discoveryIssuerForLogout(ctx context.Context) string {
	if h.settings != nil {
		if iss, _ := h.settings.GetOAuthIssuer(ctx); iss != "" {
			return strings.TrimRight(iss, "/")
		}
	}
	return ""
}

// renderLogoutSuccessPage renders an inline HTML success page.
func (h *OAuthProviderHandler) renderLogoutSuccessPage(c *gin.Context) {
	page := `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>登出成功 · Sakrylle API</title>
<style>
  :root { color-scheme: light dark; --primary:#9181bd; --primary-dim:#7b6aab; --bg:#faf9fc; --fg:#1f1b2e; --card:#ffffff; --muted:#6b6481; --border:#e5e1ed; }
  @media (prefers-color-scheme: dark) {
    :root { --bg:#13111c; --fg:#ece9f5; --card:#1c1828; --muted:#a39bbf; --border:#2a2438; }
  }
  body { margin:0; font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif; background:var(--bg); color:var(--fg); display:flex; align-items:center; justify-content:center; min-height:100dvh; padding:24px; box-sizing:border-box; }
  .card { background:var(--card); border:1px solid var(--border); border-radius:16px; box-shadow:0 8px 32px rgba(145,129,189,0.08); padding:32px 24px; max-width:400px; width:100%; box-sizing:border-box; text-align:center; }
  @media (min-width:480px) { .card { padding:40px 32px; } }
  .logo { display:block; margin:0 auto 20px; }
  .badge { width:56px; height:56px; border-radius:50%; margin:0 auto 20px; display:flex; align-items:center; justify-content:center; background:rgba(145,129,189,.12); color:var(--primary); box-shadow:0 0 0 8px rgba(145,129,189,.08); }
  h1 { font-size:20px; font-weight:600; margin:0 0 8px; }
  .lead { color:var(--muted); font-size:14px; line-height:1.5; margin:0; }
</style>
</head>
<body>
<div class="card">
  <img class="logo" src="` + sakrylleLogoDataURI + `" width="28" height="28" alt="Sakrylle">
  <div class="badge">
    <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6 9 17l-5-5"/></svg>
  </div>
  <h1>登出成功</h1>
  <p class="lead">您已安全登出 Sakrylle API。<br>可以关闭此页面。</p>
</div>
</body>
</html>`
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(page))
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

// ── Front-Channel Logout (OIDC Front-Channel Logout 1.0) ───────────────────

// FrontChannelLogout implements GET /oauth/frontchannel-logout per
// OIDC Front-Channel Logout 1.0 §2.
//
// Parameters:
//   - iss (optional): the OP issuer URL
//   - sid (optional): the session ID to log out
//
// The endpoint renders an HTML page with hidden iframes for each registered
// client's frontchannel_logout_uri. Each iframe URL includes iss and sid
// as query parameters so the RP can clear its session state.
func (h *OAuthProviderHandler) FrontChannelLogout(c *gin.Context) {
	iss := c.Query("iss")
	sid := c.Query("sid")

	// Clear local session cookie if present.
	c.SetCookie("token", "", -1, "/", "", false, true)

	// Fetch clients with frontchannel_logout_uri configured.
	clients, err := h.provider.ListClientsWithFrontchannelLogout(c.Request.Context())
	if err != nil {
		slog.Error("frontchannel logout: failed to list clients", "error", err)
	}

	// Build hidden iframes for each client.
	var iframes string
	for _, client := range clients {
		if client.FrontchannelLogoutURI == nil || *client.FrontchannelLogoutURI == "" {
			continue
		}
		uri := *client.FrontchannelLogoutURI
		separator := "?"
		if strings.Contains(uri, "?") {
			separator = "&"
		}
		iframeURL := uri
		if iss != "" {
			iframeURL += separator + "iss=" + url.QueryEscape(iss)
			separator = "&"
		}
		if sid != "" {
			iframeURL += separator + "sid=" + url.QueryEscape(sid)
		}
		iframes += fmt.Sprintf(`<iframe src="%s" style="display:none"></iframe>`+"\n", html.EscapeString(iframeURL))
	}

	if iframes == "" {
		iframes = "<!-- no clients with frontchannel_logout_uri -->"
	}

	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	c.String(http.StatusOK, `<!DOCTYPE html>
<html>
<head><title>Logout</title></head>
<body>
%s
<p>Logged out. You may close this window.</p>
</body>
</html>`, iframes)
}

// handlePromptNone handles OIDC prompt=none (silent authentication).
//
// Per OIDC Core §3.1.2.1, when prompt=none is specified:
//   - If the user has a valid session → skip consent UI, issue code directly
//   - If not authenticated → error=login_required
//   - If consent needed → error=consent_required
//   - Other issues → error=interaction_required
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

	// Check for prior consent/grant for this user+client+scopes combination.
	// Two-tier trust model:
	//   - trusted_first_party=true: Sakrylle-owned clients (Web, CLI, etc.)
	//     get automatic consent without interactive prompt.
	//   - trusted_first_party=false: third-party clients MUST have prior
	//     consent on record. Without it, prompt=none returns consent_required.
	//
	// TODO: Implement proper consent tracking in oauth_grants table so
	// third-party clients with prior consent can also use prompt=none.
	if !client.TrustedFirstParty {
		slog.Info("oidc prompt=none: consent required for third-party client",
			"user_id", userID, "client_id", req.ClientID, "scopes", scopes)
		redirectError("consent_required", "user has not consented to these scopes")
		return
	}
	// Trusted first-party client — proceed with auto-approval.

	// Defense-in-depth PKCE gate (mirrors ValidateAuthorizeRequest §10.1).
	// ValidateAuthorizeRequest above already rejects an empty/non-S256
	// code_challenge, but this function is long and mints a code directly
	// without going back through the service validator. Re-assert PKCE here so
	// a future reordering of this function can never issue a silent-auth code
	// with an empty code_challenge — such a code can never be exchanged at the
	// (strict) token endpoint, which would be a silent dead-end for the RP.
	if req.CodeChallenge == "" || req.CodeChallengeMethod != "S256" {
		slog.Info("oidc prompt=none: missing or non-S256 PKCE challenge",
			"client_id", req.ClientID, "method", req.CodeChallengeMethod)
		redirectError("invalid_request", "code_challenge with method S256 is required")
		return
	}

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
		SID:                 service.GenerateSessionID(),
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
// dispatchBackchannelLogout sends a logout_token to the initiating client's
// backchannel_logout_uri asynchronously, per OIDC Back-Channel Logout 1.0.
// Failures are logged at Warn level and never propagate to the user.
func (h *OAuthProviderHandler) dispatchBackchannelLogout(c *gin.Context, clientID string, sub string, sid string) {
	if h.provider == nil || h.oidcKeys == nil {
		return
	}

	if sub == "" {
		slog.Warn("oidc backchannel logout: cannot determine sub for logout_token")
		return
	}

	issuer := h.discoveryIssuer(c)
	if issuer == "" {
		slog.Warn("oidc backchannel logout: issuer unresolved")
		return
	}

	// Notify ONLY the RP that initiated this logout. Broadcasting logout_token
	// to every client with a backchannel_logout_uri over-notifies unrelated RPs
	// and leaks this user's logout activity to them. The initiating client is
	// the audience of the verified id_token_hint.
	if clientID == "" {
		return
	}
	client, err := h.provider.LookupClient(c.Request.Context(), clientID)
	if err != nil || client == nil {
		slog.Warn("oidc backchannel logout: initiating client not found",
			"client_id", clientID, "error", err)
		return
	}
	if client.BackchannelLogoutURI == nil || *client.BackchannelLogoutURI == "" {
		return
	}
	clients := []*service.OAuthClient{client}

	now := time.Now()
	for _, client := range clients {
		if client.BackchannelLogoutURI == nil || *client.BackchannelLogoutURI == "" {
			continue
		}

		// Build and sign a logout_token per client (audience-specific).
		claims := service.BuildLogoutToken(issuer, sub, client.ClientID, sid, now, 0)
		alg := service.SigningAlgRS256
		if client.SigningAlgorithm == "ES256" {
			alg = service.SigningAlgES256
		}
		logoutToken, signErr := h.oidcKeys.Sign(claims, alg)
		if signErr != nil {
			slog.Error("oidc backchannel logout: failed to sign logout_token",
				"client_id", client.ClientID, "error", signErr)
			continue
		}

		// POST asynchronously per client.
		uri := *client.BackchannelLogoutURI
		go func(uri, token, cid string) {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("oidc backchannel logout: panic recovered",
						"uri", uri, "panic", r)
				}
			}()
			reqCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			body := url.Values{"logout_token": {token}}.Encode()
			req, reqErr := http.NewRequestWithContext(reqCtx, http.MethodPost, uri,
				strings.NewReader(body))
			if reqErr != nil {
				slog.Warn("oidc backchannel logout: failed to build request",
					"uri", uri, "error", reqErr)
				return
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			httpClient := &http.Client{}
			resp, doErr := httpClient.Do(req)
			if doErr != nil {
				slog.Warn("oidc backchannel logout: POST failed",
					"uri", uri, "error", doErr)
				return
			}
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				slog.Info("oidc backchannel logout: logout_token delivered",
					"uri", uri, "client_id", cid, "status", resp.StatusCode)
			} else {
				slog.Warn("oidc backchannel logout: unexpected response",
					"uri", uri, "client_id", cid, "status", resp.StatusCode)
			}
		}(uri, logoutToken, client.ClientID)
	}
}

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
