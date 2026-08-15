// Package middleware OAuth scope enforcement.
//
// See docs/OAUTH_V2_DESIGN.md §13.1. The middleware in this file enforces the
// §7.3 endpoint scope matrix for OAuth-issued (`sk_oauth_`) tokens while
// leaving manual API keys completely untouched (acceptance criterion #1 of
// Phase 3).
//
// Three behaviours, one shared service dependency:
//
//   - RequireOAuthScope:                §7.3 listed routes — the token MUST
//     hold any one of the route's required scopes; missing scope → 403
//     `insufficient_scope` with `WWW-Authenticate: Bearer scope="..."`.
//   - LoadOAuthMetadata:                companion for /v1/me where the handler
//     does field-level scope cropping after the metadata is loaded.
//   - RejectOAuthTokensForUnlistedResource: API-key-authenticated routes
//     deliberately out of OAuth scope. Manual keys pass through; OAuth tokens
//     get 403 `insufficient_scope`.
//
// All three short-circuit when the kill-switch
// `oauth_scope_enforcement_enabled` is false, matching §13.2: production
// rolls scope enforcement out behind a setting toggle so a regression can be
// disabled with a single SQL update.
package middleware

import (
	"log/slog"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// ContextKeyOAuthMetadata stores the loaded *service.OAuthAccessToken so
// downstream handlers (e.g. /v1/me) can read it without re-querying the DB.
const ContextKeyOAuthMetadata ContextKey = "oauth_metadata"

// RequireOAuthScope returns a middleware that enforces the §7.3 endpoint
// scope matrix for OAuth-issued tokens. Manual API keys pass through.
//
// Required scopes are looked up dynamically from the route via
// service.OAuthScopePolicyForRequest, so a single middleware instance can be
// attached to a whole route group without per-route configuration. This makes
// adding a new gateway alias safer: if it isn't registered in the §7.3 matrix
// the unlisted-resource gate denies it for OAuth tokens by default.
func RequireOAuthScope(svc *service.OAuthProviderService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if svc == nil {
			c.Next()
			return
		}
		apiKey, ok := GetAPIKeyFromContext(c)
		if !ok || apiKey == nil {
			// API key auth must run before this middleware. Reaching here means
			// the route is misconfigured (this middleware mounted before auth)
			// or auth middleware silently failed without aborting. Fail closed:
			// passing through would silently bypass scope enforcement.
			slog.Error("oauth: scope middleware ran without api key context — misconfiguration",
				"path", c.Request.URL.Path,
				"method", c.Request.Method,
			)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error":             "server_error",
				"error_description": "internal authorization error",
			})
			return
		}
		if !service.IsOAuthAccessToken(apiKey.Key) {
			// Manual API key — never enforce OAuth scopes (§7.3, Phase 3 acceptance #1).
			c.Next()
			return
		}

		ctx := c.Request.Context()

		// §13.2 kill-switch: when scope enforcement is globally disabled,
		// skip the metadata load and scope check entirely. The companion
		// LoadOAuthMetadata middleware handles metadata loading for
		// downstream handlers that need it (e.g. /v1/me field-level cropping).
		if !svc.IsScopeEnforcementEnabled(ctx) {
			c.Next()
			return
		}

		meta, err := svc.LoadOAuthAccessMetadata(ctx, apiKey.ID)
		if err != nil {
			WriteOAuthResourceError(c, OAuthResourceError{
				Status:              http.StatusUnauthorized,
				Code:                OAuthErrInvalidToken,
				Description:         "failed to load oauth access metadata",
				ResourceMetadataURL: discoveryURLForRequest(c),
			})
			return
		}
		// Defense in depth: a sk_oauth_ token without an oauth_access_tokens
		// row is a corrupt/incomplete provisioning state. The FIX H4 atomic
		// mint path makes this unreachable for fresh tokens, but a stale row
		// (or a future migration) could surface it. Reject with invalid_token
		// per RFC 6750 rather than fall through to the gateway.
		if meta == nil {
			slog.Warn("oauth: sk_oauth_ token has no oauth_access_tokens row — failing closed",
				"api_key_id", apiKey.ID,
				"path", c.Request.URL.Path,
			)
			WriteOAuthResourceError(c, OAuthResourceError{
				Status:              http.StatusUnauthorized,
				Code:                OAuthErrInvalidToken,
				Description:         "oauth access metadata missing",
				ResourceMetadataURL: discoveryURLForRequest(c),
			})
			return
		}

		// Stash metadata for handlers that want it (e.g. /v1/me).
		c.Set(string(ContextKeyOAuthMetadata), meta)
		svc.TouchAccessTokenLastUsed(ctx, apiKey.ID, c.ClientIP(), c.Request.UserAgent())

		method := c.Request.Method
		path := requestPath(c)
		required, listed := service.OAuthScopePolicyForRequest(method, path)
		if !listed {
			// Unlisted route + OAuth token = reject by default (§7.3).
			WriteOAuthResourceError(c, OAuthResourceError{
				Status:              http.StatusForbidden,
				Code:                OAuthErrInsufficientScope,
				Description:         "this resource is not exposed to OAuth tokens",
				ResourceMetadataURL: discoveryURLForRequest(c),
			})
			return
		}
		if !service.HasAnyScope(meta.Scopes, required...) {
			WriteOAuthResourceError(c, OAuthResourceError{
				Status:              http.StatusForbidden,
				Code:                OAuthErrInsufficientScope,
				Description:         "required scope not granted",
				RequiredScopes:      required,
				ResourceMetadataURL: discoveryURLForRequest(c),
			})
			return
		}

		c.Next()
	}
}

// LoadOAuthMetadata loads OAuth metadata into gin context but does NOT
// enforce a scope check. Used for /v1/me and /userinfo where the handler
// crops fields by scope itself.
//
// This middleware always loads OAuth metadata for sk_oauth_ tokens regardless
// of the oauth_scope_enforcement_enabled kill-switch. The kill-switch controls
// scope ENFORCEMENT (rejecting requests with insufficient scopes), not metadata
// AVAILABILITY. Without metadata, the /v1/me handler cannot distinguish OAuth
// tokens from manual API keys and falls through to the full-account-view path.
//
// Manual API keys pass through unchanged.
func LoadOAuthMetadata(svc *service.OAuthProviderService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if svc == nil {
			c.Next()
			return
		}
		apiKey, ok := GetAPIKeyFromContext(c)
		if !ok || apiKey == nil {
			c.Next()
			return
		}
		if !service.IsOAuthAccessToken(apiKey.Key) {
			c.Next()
			return
		}

		// Always load OAuth metadata for sk_oauth_ tokens. The kill-switch
		// controls scope enforcement, not metadata availability — without
		// metadata the /v1/me handler treats OAuth tokens as manual API keys
		// and returns the full account view without OIDC sub claim.
		meta, err := svc.LoadOAuthAccessMetadata(c.Request.Context(), apiKey.ID)
		if err != nil {
			WriteOAuthResourceError(c, OAuthResourceError{
				Status:              http.StatusUnauthorized,
				Code:                OAuthErrInvalidToken,
				Description:         "failed to load oauth access metadata",
				ResourceMetadataURL: discoveryURLForRequest(c),
			})
			return
		}
		if meta != nil {
			c.Set(string(ContextKeyOAuthMetadata), meta)
			svc.TouchAccessTokenLastUsed(c.Request.Context(), apiKey.ID, c.ClientIP(), c.Request.UserAgent())
		}
		c.Next()
	}
}

// RejectOAuthTokensForUnlistedResource is attached to API-key-authenticated
// route groups that are deliberately NOT exposed to OAuth tokens (e.g.
// `/v1beta/*` Gemini-native, `/antigravity/v1beta/*`). Manual API keys pass;
// OAuth tokens are rejected with 403 `insufficient_scope`.
//
// This is the explicit form of §7.3's unlisted-route default. Routes that
// already run `RequireOAuthScope` get the same protection from the same
// matrix lookup; this middleware is for groups that don't run scope
// enforcement at all.
func RejectOAuthTokensForUnlistedResource(svc *service.OAuthProviderService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if svc == nil {
			c.Next()
			return
		}
		apiKey, ok := GetAPIKeyFromContext(c)
		if !ok || apiKey == nil {
			c.Next()
			return
		}
		if !service.IsOAuthAccessToken(apiKey.Key) {
			c.Next()
			return
		}
		meta, err := svc.LoadOAuthAccessMetadata(c.Request.Context(), apiKey.ID)
		if err != nil {
			WriteOAuthResourceError(c, OAuthResourceError{
				Status:              http.StatusUnauthorized,
				Code:                OAuthErrInvalidToken,
				Description:         "failed to load oauth access metadata",
				ResourceMetadataURL: discoveryURLForRequest(c),
			})
			return
		}
		if meta == nil {
			// Legacy sk_oauth_ row without v2 metadata. Pass through; v1 tokens
			// predate the matrix.
			c.Next()
			return
		}

		// §13.2 kill-switch: when scope enforcement is globally disabled,
		// allow OAuth tokens through to unlisted resources.
		if !svc.IsScopeEnforcementEnabled(c.Request.Context()) {
			c.Next()
			return
		}

		WriteOAuthResourceError(c, OAuthResourceError{
			Status:              http.StatusForbidden,
			Code:                OAuthErrInsufficientScope,
			Description:         "this resource is not exposed to OAuth tokens",
			ResourceMetadataURL: discoveryURLForRequest(c),
		})
	}
}

// GetOAuthAccessTokenFromContext returns the OAuth metadata stashed by
// RequireOAuthScope or LoadOAuthMetadata. (manual API key, feature flag off,
// or legacy v1 sk_oauth_ row).
func GetOAuthAccessTokenFromContext(c *gin.Context) (*service.OAuthAccessToken, bool) {
	value, exists := c.Get(string(ContextKeyOAuthMetadata))
	if !exists {
		return nil, false
	}
	meta, ok := value.(*service.OAuthAccessToken)
	return meta, ok && meta != nil
}

// requestPath returns the URL path with the query string stripped, so
// OAuthScopePolicyForRequest can match against the raw path even when the
// request carries query params. Strips trailing slashes the same way the
// matrix patterns tolerate (the regex patterns already accept optional `/?`).
func requestPath(c *gin.Context) string {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return ""
	}
	return c.Request.URL.Path
}

// discoveryURLForRequest builds the RFC 9728 `resource_metadata` URL for the
// current request's host. Best-effort: returns "" if the request has no
// scheme/host hint, which is acceptable per §12.12 (the parameter is optional).
func discoveryURLForRequest(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	scheme := "https"
	if c.Request.TLS == nil && c.Request.Header.Get("X-Forwarded-Proto") != "https" {
		scheme = "http"
	}
	// Allowlist X-Forwarded-Proto to {http, https} so a malformed or hostile
	// proxy header cannot inject a scheme into the resource_metadata URL we
	// echo into WWW-Authenticate.
	if proto := c.Request.Header.Get("X-Forwarded-Proto"); proto == "http" || proto == "https" {
		scheme = proto
	}
	host := c.Request.Host
	if host == "" {
		return ""
	}
	return scheme + "://" + host + "/.well-known/oauth-protected-resource"
}
