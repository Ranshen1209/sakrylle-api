package middleware

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// RequireRecentAuth enforces step-up authentication by rejecting requests
// whose JWT was issued more than maxAge ago. This is a phase-1 step-up gate:
// the client must re-authenticate (obtain a fresh token) before performing
// sensitive mutations such as revoking OAuth authorized apps.
//
// Returns 403 with error "step_up_required" when the token is too old, so
// the frontend can prompt the user to re-enter their password and re-login.
func RequireRecentAuth(maxAge time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		subject, ok := GetAuthSubjectFromContext(c)
		if !ok || subject.UserID == 0 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":             "unauthorized",
				"error_description": "authentication required",
			})
			return
		}
		if subject.IssuedAt.IsZero() || time.Since(subject.IssuedAt) > maxAge {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":             "step_up_required",
				"error_description": "recent authentication required for this action; please re-login and try again",
			})
			return
		}
		c.Next()
	}
}
