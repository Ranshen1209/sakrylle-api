package routes

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/middleware"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// RegisterOAuthDeviceRoutes mounts the RFC 8628 Device Authorization Grant
// endpoints. Lives in a separate file from RegisterOAuthRoutes so the Phase 4
// device flow stays self-contained and avoids merge conflicts with Phase 3
// (which owns oauth.go).
//
// Path layout:
//
//	POST /oauth/device/code           — public, form-encoded; mints a device+user code pair
//	GET  /oauth/device                — public; renders the verification page
//	POST /api/v1/oauth/device/approve — JWT-protected; consents the typed user_code
//	POST /api/v1/oauth/device/deny    — JWT-protected; rejects the typed user_code
//
// /oauth/token grant_type=urn:ietf:params:oauth:grant-type:device_code is
// dispatched inside OAuthProviderHandler.Token; that route is registered by
// RegisterOAuthRoutes.
//
// Rate limits per §12.7 / §12.8:
//
//	POST /oauth/device/code:           10/min/IP  fail-close
//	GET  /oauth/device:                30/min/IP  fail-close
//	POST /api/v1/oauth/device/approve:  5/15min/IP fail-close
//	POST /api/v1/oauth/device/deny:     5/15min/IP fail-close
//
// The 30/client_id sub-limit on POST /oauth/device/code from §12.7 is left
// to the service-layer brute-force counter (failed_user_code_attempts) +
// monitoring; an additional Redis bucket per client_id can land in a
// follow-up if abuse is observed in prod.
func RegisterOAuthDeviceRoutes(
	r *gin.Engine,
	v1 *gin.RouterGroup,
	h *handler.Handlers,
	jwtAuth servermiddleware.JWTAuthMiddleware,
	redisClient *redis.Client,
) {
	if h == nil || h.OAuthDevice == nil {
		return
	}

	rateLimiter := middleware.NewRateLimiter(redisClient)

	r.POST("/oauth/device/code",
		rateLimiter.LimitWithOptions("oauth-device-code", 10, time.Minute, middleware.RateLimitOptions{
			FailureMode: middleware.RateLimitFailClose,
		}),
		h.OAuthDevice.DeviceAuthorize,
	)
	r.GET("/oauth/device",
		rateLimiter.LimitWithOptions("oauth-device-page", 30, time.Minute, middleware.RateLimitOptions{
			FailureMode: middleware.RateLimitFailClose,
		}),
		h.OAuthDevice.DeviceVerificationPage,
	)

	device := v1.Group("/oauth/device")
	device.Use(gin.HandlerFunc(jwtAuth))
	{
		// 5 attempts per 15 min per IP; failed_user_code_attempts on the
		// row enforces the global per-code 5-strikes lockout (§10.6).
		device.POST("/approve",
			rateLimiter.LimitWithOptions("oauth-device-approve", 5, 15*time.Minute, middleware.RateLimitOptions{
				FailureMode: middleware.RateLimitFailClose,
			}),
			h.OAuthDevice.DeviceApprove,
		)
		device.POST("/deny",
			rateLimiter.LimitWithOptions("oauth-device-deny", 5, 15*time.Minute, middleware.RateLimitOptions{
				FailureMode: middleware.RateLimitFailClose,
			}),
			h.OAuthDevice.DeviceDeny,
		)
	}
}
