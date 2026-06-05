package routes

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/middleware"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// RegisterOAuthRoutes mounts the OAuth 2.0 provider endpoints.
//
// Path layout:
//
//	GET  /.well-known/oauth-authorization-server  — public discovery (RFC 8414)
//	GET  /oauth/authorize                         — public, renders consent page (rate-limited 30/min per IP)
//	POST /oauth/authorize                         — public, form-encoded consent (rate-limited 30/min per IP)
//	POST /oauth/token                             — public, RFC 6749 token endpoint, form-encoded (rate-limited 20/min per IP)
//	POST /oauth/revoke                            — public, RFC 7009 revocation endpoint, form-encoded (rate-limited 30/min per IP)
//	POST /oauth/device/code                       — public, RFC 8628 device authorization (rate-limited 20/min per IP)
//	GET  /oauth/device                            — public, renders device verification page (rate-limited 30/min per IP)
//	POST /api/v1/oauth/authorize/approve          — JWT-protected, called by consent page JS
//	POST /api/v1/oauth/device/approve             — JWT-protected, approves a device user_code
//	POST /api/v1/oauth/device/deny                — JWT-protected, denies a device user_code
//	GET  /api/v1/oauth/authorized-apps            — JWT-protected, lists per-device grants (§12.10)
//	DELETE /api/v1/oauth/authorized-apps/:grant_id — JWT-protected, revokes one device grant
//	DELETE /api/v1/oauth/authorized-apps/client/:client_id — JWT-protected, revokes all devices for a client
//	GET  /api/v1/oauth/grants                     — JWT-protected, legacy compatibility view
//	DELETE /api/v1/oauth/grants/:client_id        — JWT-protected, legacy revoke-by-client
//
// /oauth/token, /oauth/revoke, and /.well-known/* are intentionally NOT under
// /api/v1 so external clients hit `https://sub.sakrylle.com/<path>` directly
// per docs/SAKRYLLE_API_SPEC.md §1.
//
// The public POST endpoints use Redis-backed per-IP rate limits with FailClose
// semantics (matching auth endpoints): if Redis is unavailable, requests are
// rejected rather than allowing unauthenticated bursts through. The token
// endpoint runs bcrypt + a `FOR UPDATE` row lock per call, so its limit is
// stricter than authorize's HTML-rendering GET.
func RegisterOAuthRoutes(
	r *gin.Engine,
	v1 *gin.RouterGroup,
	h *handler.Handlers,
	jwtAuth servermiddleware.JWTAuthMiddleware,
	redisClient *redis.Client,
) {
	if h == nil || h.OAuthProvider == nil {
		return
	}

	rateLimiter := middleware.NewRateLimiter(redisClient)

	// Discovery is public read-only and cacheable; no rate limit.
	r.GET("/.well-known/oauth-authorization-server", h.OAuthProvider.Metadata)
	// OIDC discovery + JWKS (public, cacheable). issuer is https://sub.sakrylle.com.
	r.GET("/.well-known/openid-configuration", h.OAuthProvider.OpenIDConfiguration)
	r.GET("/.well-known/jwks.json", h.OAuthProvider.JWKS)

	// OIDC UserInfo endpoint (§5.3). Outside /v1 so it is not gated on billing,
	// balance, quota, or group assignment. Only validates Bearer token identity.
	r.GET("/userinfo", h.OAuthProvider.UserInfo)
	r.POST("/userinfo", h.OAuthProvider.UserInfo)

	// RP-Initiated Logout (OIDC Session Management §5)
	// GET/POST /oauth/logout accepts id_token_hint + post_logout_redirect_uri.
	// No rate limit (logout is user-initiated, low volume).
	r.GET("/oauth/logout", h.OAuthProvider.Logout)
	r.POST("/oauth/logout", h.OAuthProvider.Logout)

	// GET renders the consent page; POST accepts form-encoded body for the
	// same flow (some clients POST the authorization request directly).
	authorizeLimit := rateLimiter.LimitWithOptions("oauth-authorize", 30, time.Minute, middleware.RateLimitOptions{
		FailureMode: middleware.RateLimitFailClose,
	})
	r.GET("/oauth/authorize", authorizeLimit, h.OAuthProvider.Authorize)
	r.POST("/oauth/authorize", authorizeLimit, h.OAuthProvider.Authorize)

	r.POST("/oauth/token",
		rateLimiter.LimitWithOptions("oauth-token", 20, time.Minute, middleware.RateLimitOptions{
			FailureMode: middleware.RateLimitFailClose,
		}),
		servermiddleware.RequestBodyLimit(32*1024),
		h.OAuthProvider.Token,
	)
	// §12.9: revoke runs idempotency + bcrypt for confidential clients, so a
	// per-IP fail-close limit at 30/min matches the authorize cadence.
	r.POST("/oauth/revoke",
		rateLimiter.LimitWithOptions("oauth-revoke", 30, time.Minute, middleware.RateLimitOptions{
			FailureMode: middleware.RateLimitFailClose,
		}),
		servermiddleware.RequestBodyLimit(32*1024),
		h.OAuthProvider.Revoke,
	)
	// RFC 7662: token introspection for confidential clients.
	r.POST("/oauth/introspect",
		rateLimiter.LimitWithOptions("oauth-introspect", 30, time.Minute, middleware.RateLimitOptions{
			FailureMode: middleware.RateLimitFailClose,
		}),
		servermiddleware.RequestBodyLimit(32*1024),
		h.OAuthProvider.Introspect,
	)

	oauth := v1.Group("/oauth")
	oauth.Use(gin.HandlerFunc(jwtAuth))
	oauth.Use(servermiddleware.RequestBodyLimit(32 * 1024))
	{
		// FIX A1 / §10.3: /begin opens the server-side authorize transaction
		// for the JWT subject. The consent page POSTs the original /authorize
		// query params here; the server validates them, captures user_id, and
		// returns transaction_id + plaintext csrf_token (one-time). /approve
		// then consumes the transaction by id alone — clients cannot tamper
		// with client_id / redirect_uri / scopes between begin and approve.
		oauth.POST("/authorize/begin", h.OAuthProvider.BeginAuthorize)
		oauth.POST("/authorize/approve", h.OAuthProvider.Approve)

		// Legacy v1 client-aggregate endpoints kept for compatibility (§12.10).
		oauth.GET("/grants", h.OAuthProvider.ListGrants)
		oauth.DELETE("/grants/:client_id", h.OAuthProvider.RevokeGrant)

		// v2 per-device authorized apps (§12.10).
		// DELETE endpoints require step-up auth (JWT issued within 15 min) to
		// prevent session-hijacking attacks from revoking a user's app grants.
		oauth.GET("/authorized-apps", h.OAuthProvider.ListAuthorizedApps)
		oauth.DELETE("/authorized-apps/:grant_id",
			gin.HandlerFunc(servermiddleware.RequireRecentAuth(15*time.Minute)),
			h.OAuthProvider.RevokeAuthorizedApp,
		)
		oauth.DELETE("/authorized-apps/client/:client_id",
			gin.HandlerFunc(servermiddleware.RequireRecentAuth(15*time.Minute)),
			h.OAuthProvider.RevokeAuthorizedClient,
		)
	}
}
