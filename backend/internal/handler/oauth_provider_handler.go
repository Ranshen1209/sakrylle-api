package handler

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// OAuthProviderHandler exposes RFC 6749 §4.1 (Authorization Code) + RFC 7636
// (PKCE) endpoints so external apps (e.g. image.sakrylle.com) can mint
// Sakrylle access tokens scoped to a user account.
type OAuthProviderHandler struct {
	provider *service.OAuthProviderService
	settings *service.SettingService
}

func NewOAuthProviderHandler(provider *service.OAuthProviderService, settings *service.SettingService) *OAuthProviderHandler {
	return &OAuthProviderHandler{provider: provider, settings: settings}
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

	req := &service.AuthorizeRequest{
		ClientID:            strings.TrimSpace(c.Query("client_id")),
		RedirectURI:         strings.TrimSpace(c.Query("redirect_uri")),
		ResponseType:        strings.TrimSpace(c.Query("response_type")),
		Scopes:              service.ParseScopes(c.Query("scope")),
		State:               strings.TrimSpace(c.Query("state")),
		CodeChallenge:       strings.TrimSpace(c.Query("code_challenge")),
		CodeChallengeMethod: strings.TrimSpace(c.Query("code_challenge_method")),
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
	c.String(http.StatusOK, oauthConsentHTML(client.Name, req))
}

// ApproveRequest is the body of POST /api/v1/oauth/authorize/approve.
//
// All fields except Decision are echoed verbatim from the original /authorize
// query so the server re-validates against the registered client and
// does not trust the form alone.
type ApproveRequest struct {
	ClientID            string `json:"client_id" binding:"required"`
	RedirectURI         string `json:"redirect_uri" binding:"required"`
	ResponseType        string `json:"response_type" binding:"required"`
	Scope               string `json:"scope"`
	State               string `json:"state" binding:"required"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	Decision            string `json:"decision" binding:"required,oneof=approve deny"`
}

// Approve issues an authorization code (or denial) for an authenticated user.
//
// Mounted under JWT-protected /api/v1/* so only logged-in users can complete
// consent. Returns the final redirect URL in JSON; the SPA navigates to it.
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

	authReq := &service.AuthorizeRequest{
		ClientID:            strings.TrimSpace(body.ClientID),
		RedirectURI:         strings.TrimSpace(body.RedirectURI),
		ResponseType:        strings.TrimSpace(body.ResponseType),
		Scopes:              service.ParseScopes(body.Scope),
		State:               strings.TrimSpace(body.State),
		CodeChallenge:       strings.TrimSpace(body.CodeChallenge),
		CodeChallengeMethod: strings.TrimSpace(body.CodeChallengeMethod),
	}
	client, err := h.provider.ValidateAuthorizeRequest(c.Request.Context(), authReq)
	if err != nil {
		c.JSON(httpStatusForOAuthError(err), gin.H{
			"error":             oauthErrorReason(err),
			"error_description": infraerrors.Message(err),
		})
		return
	}

	if body.Decision != "approve" {
		c.JSON(http.StatusOK, gin.H{
			"redirect_to": buildOAuthErrorURL(authReq.RedirectURI, "access_denied", "user denied authorization", authReq.State),
		})
		return
	}

	issued, err := h.provider.IssueAuthorizationCode(c.Request.Context(), client, subject.UserID, authReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server_error", "error_description": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"redirect_to": buildOAuthRedirectURL(authReq.RedirectURI, issued.Code, issued.State),
	})
}

// Token handles POST /oauth/token (both authorization_code and refresh_token grants).
//
// Per spec the body MUST be application/x-www-form-urlencoded.
func (h *OAuthProviderHandler) Token(c *gin.Context) {
	if !h.provider.IsEnabled(c.Request.Context()) {
		writeOAuthError(c, http.StatusForbidden, "invalid_client", "oauth provider disabled")
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
	default:
		writeOAuthError(c, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
	}
}

func (h *OAuthProviderHandler) tokenAuthorizationCode(c *gin.Context) {
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
	c.JSON(http.StatusOK, tokenResponseJSON(issued))
}

func (h *OAuthProviderHandler) tokenRefresh(c *gin.Context) {
	clientID, clientSecret := extractClientCredentials(c)
	refreshToken := strings.TrimSpace(c.Request.PostFormValue("refresh_token"))

	issued, err := h.provider.RefreshAccessToken(c.Request.Context(), clientID, clientSecret, refreshToken)
	if err != nil {
		writeOAuthError(c, httpStatusForOAuthError(err), oauthErrorReason(err), infraerrors.Message(err))
		return
	}
	c.Header("Cache-Control", "no-store")
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
	return gin.H{
		"access_token":  issued.AccessToken,
		"token_type":    issued.TokenType,
		"expires_in":    issued.ExpiresIn,
		"refresh_token": issued.RefreshToken,
		"scope":         issued.Scope,
	}
}

func writeOAuthError(c *gin.Context, status int, code, desc string) {
	c.Header("Cache-Control", "no-store")
	c.JSON(status, gin.H{
		"error":             code,
		"error_description": desc,
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
