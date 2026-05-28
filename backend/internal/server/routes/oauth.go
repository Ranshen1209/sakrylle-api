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
//	POST /oauth/token                             — public, RFC 6749 token endpoint, form-encoded (rate-limited 20/min per IP)
//	POST /oauth/revoke                            — public, RFC 7009 revocation endpoint, form-encoded (rate-limited 30/min per IP)
//	POST /api/v1/oauth/authorize/approve          — JWT-protected, called by consent page JS
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

	r.GET("/oauth/authorize",
		rateLimiter.LimitWithOptions("oauth-authorize", 30, time.Minute, middleware.RateLimitOptions{
			FailureMode: middleware.RateLimitFailClose,
		}),
		h.OAuthProvider.Authorize,
	)
	r.POST("/oauth/token",
		rateLimiter.LimitWithOptions("oauth-token", 20, time.Minute, middleware.RateLimitOptions{
			FailureMode: middleware.RateLimitFailClose,
		}),
		h.OAuthProvider.Token,
	)
	// §12.9: revoke runs idempotency + bcrypt for confidential clients, so a
	// per-IP fail-close limit at 30/min matches the authorize cadence.
	r.POST("/oauth/revoke",
		rateLimiter.LimitWithOptions("oauth-revoke", 30, time.Minute, middleware.RateLimitOptions{
			FailureMode: middleware.RateLimitFailClose,
		}),
		h.OAuthProvider.Revoke,
	)

	oauth := v1.Group("/oauth")
	oauth.Use(gin.HandlerFunc(jwtAuth))
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
		oauth.GET("/authorized-apps", h.OAuthProvider.ListAuthorizedApps)
		oauth.DELETE("/authorized-apps/:grant_id", h.OAuthProvider.RevokeAuthorizedApp)
		oauth.DELETE("/authorized-apps/client/:client_id", h.OAuthProvider.RevokeAuthorizedClient)
	}
}
