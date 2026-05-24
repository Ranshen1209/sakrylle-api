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
//	GET  /oauth/authorize                 — public, renders consent page (rate-limited 30/min per IP)
//	POST /oauth/token                     — public, RFC 6749 token endpoint, form-encoded (rate-limited 20/min per IP)
//	POST /api/v1/oauth/authorize/approve  — JWT-protected, called by consent page JS
//
// /oauth/token is intentionally NOT under /api/v1 so external clients hit
// `https://sub.sakrylle.com/oauth/token` directly per docs/SAKRYLLE_API_SPEC.md §1.
//
// The two public endpoints use Redis-backed per-IP rate limits with FailClose
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

	oauth := v1.Group("/oauth")
	oauth.Use(gin.HandlerFunc(jwtAuth))
	{
		oauth.POST("/authorize/approve", h.OAuthProvider.Approve)
	}
}
