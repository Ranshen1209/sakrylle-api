package handler

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// AccountInfoHandler serves user-facing account endpoints used by OAuth-bound
// clients (image.sakrylle.com) — balance, group, currency display.
type AccountInfoHandler struct {
	userSvc  *service.UserService
	groupSvc *service.GroupService
}

func NewAccountInfoHandler(userSvc *service.UserService, groupSvc *service.GroupService) *AccountInfoHandler {
	return &AccountInfoHandler{userSvc: userSvc, groupSvc: groupSvc}
}

// Balance handles GET /v1/account/balance.
//
// Returns credit_remaining as a numeric value with currency_display as the
// rendering hint. Per CLAUDE.md the value is not FX-converted; clients render
// it with the symbol implied by currency_display.
func (h *AccountInfoHandler) Balance(c *gin.Context) {
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil || apiKey.User == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "unauthorized", "message": "missing api key context"}})
		return
	}

	user := apiKey.User
	rateMultiplier := 1.0
	groupID := int64(0)
	groupName := ""
	allowImage := false
	if apiKey.Group != nil {
		rateMultiplier = apiKey.Group.RateMultiplier
		groupID = apiKey.Group.ID
		groupName = apiKey.Group.Name
		allowImage = apiKey.Group.AllowImageGeneration
		if user.GroupRates != nil {
			if override, exists := user.GroupRates[apiKey.Group.ID]; exists {
				rateMultiplier = override
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"user_id":                user.ID,
		"username":               user.Username,
		"credit_remaining":       user.Balance,
		"currency_display":       "CNY",
		"rate_multiplier":        rateMultiplier,
		"group_id":               groupID,
		"group_name":             groupName,
		"allow_image_generation": allowImage,
	})
}
