package handler

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// AccountInfoHandler serves user-facing account endpoints used by OAuth-bound
// clients (image.sakrylle.com) — balance, group, currency display.
type AccountInfoHandler struct {
	userSvc      *service.UserService
	groupSvc     *service.GroupService
	oauthService *service.OAuthProviderService
}

// NewAccountInfoHandler constructs the handler. oauthService is nil-tolerant
// so test harnesses that don't need OAuth metadata can pass nil; production
// wiring threads the live service through wire.
func NewAccountInfoHandler(userSvc *service.UserService, groupSvc *service.GroupService) *AccountInfoHandler {
	return &AccountInfoHandler{userSvc: userSvc, groupSvc: groupSvc}
}

// SetOAuthService injects the OAuth provider service after construction. Used
// by the wire ProviderSet so we don't have to add a 3-arg constructor that
// would require updating every test that builds a handler harness.
func (h *AccountInfoHandler) SetOAuthService(svc *service.OAuthProviderService) {
	h.oauthService = svc
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

// Me handles GET /v1/me (§12.11).
//
// Two modes:
//
//   - OAuth token: response is cropped by granted scopes. Token MUST hold any
//     of profile:read / account:read / account:balance:read or we return 403
//     insufficient_scope (matched against §7.3).
//   - Manual API key: returns the full account/group view (§12.11 says manual
//     keys are not scope-limited; existing /v1/account/balance already
//     exposes the same fields, so /v1/me on a manual key is equivalent).
//
// `granted_scopes` lists what the token holds; `effective_capabilities` is
// (granted ∩ group capability) so a token with `images:create` bound to a
// group with allow_image_generation=false sees images_create=false.
func (h *AccountInfoHandler) Me(c *gin.Context) {
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil || apiKey.User == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "unauthorized", "message": "missing api key context"}})
		return
	}
	meta, _ := middleware.GetOAuthAccessTokenFromContext(c)

	// Manual API key path — never scope-cropped.
	if meta == nil {
		c.JSON(http.StatusOK, h.assembleManualKeyMe(apiKey))
		return
	}

	// OAuth path — at least one of the §7.3 GET /v1/me scopes is required.
	required := []string{
		service.ScopeProfileRead,
		service.ScopeAccountRead,
		service.ScopeAccountBalanceRead,
	}
	if !service.HasAnyScope(meta.Scopes, required...) {
		middleware.WriteOAuthResourceError(c, middleware.OAuthResourceError{
			Status:         http.StatusForbidden,
			Code:           middleware.OAuthErrInsufficientScope,
			Description:    "required scope: profile:read, account:read, or account:balance:read",
			RequiredScopes: required,
		})
		return
	}
	c.JSON(http.StatusOK, h.assembleOAuthMe(c, apiKey, meta))
}

// assembleManualKeyMe returns the full account/group view for manual API
// keys. No scope cropping; mirrors the existing /v1/account/balance shape
// plus user identity fields the frontend already shows.
func (h *AccountInfoHandler) assembleManualKeyMe(apiKey *service.APIKey) gin.H {
	user := apiKey.User
	resp := gin.H{
		"auth_type": "api_key",
		"user": gin.H{
			"id":           user.ID,
			"username":     user.Username,
			"display_name": user.Username,
			"avatar_url":   nullableString(user.AvatarURL),
			"locale":       "zh-CN",
		},
		"account": gin.H{
			"credit_remaining": user.Balance,
			"currency_display": "CNY",
			"currency_symbol":  "￥",
		},
	}
	if apiKey.Group != nil {
		resp["current_group"] = groupSummary(apiKey.Group, user, true)
	}
	return resp
}

// assembleOAuthMe applies §12.11 field-level scope cropping.
//
// Field families:
//
//	profile:read              → user.{id,username,display_name,avatar_url,locale}
//	email:read                → user.email (additive on top of profile)
//	account:balance:read      → account.credit_remaining + currency display
//	account:read              → account + current_group + allowed_groups +
//	                            granted_scopes + effective_capabilities
//
// `oauth` block is always included for OAuth tokens (client_id / app_type /
// grant_id / device / expiry). Per §12.11 we never expose api_keys[],
// billing records, admin fields, or full email without email:read.
func (h *AccountInfoHandler) assembleOAuthMe(c *gin.Context, apiKey *service.APIKey, meta *service.OAuthAccessToken) gin.H {
	user := apiKey.User
	hasProfile := service.HasScope(meta.Scopes, service.ScopeProfileRead)
	hasEmail := service.HasScope(meta.Scopes, service.ScopeEmailRead)
	hasBalance := service.HasScope(meta.Scopes, service.ScopeAccountBalanceRead)
	hasAccount := service.HasScope(meta.Scopes, service.ScopeAccountRead)

	resp := gin.H{
		"auth_type": "oauth",
		"granted_scopes": func() []string {
			out := service.NormalizeScopes(meta.Scopes)
			if out == nil {
				return []string{}
			}
			return out
		}(),
		"effective_capabilities": effectiveCapabilities(meta.Scopes, apiKey.Group),
		"oauth": gin.H{
			"client_id":   meta.ClientID,
			"app_type":    meta.AppType,
			"grant_id":    meta.GrantID,
			"device_id":   nullableStringPtr(meta.DeviceID),
			"device_name": nullableStringPtr(meta.DeviceName),
			"expires_at":  meta.ExpiresAt,
		},
	}

	if hasProfile {
		userBlock := gin.H{
			"id":           user.ID,
			"username":     user.Username,
			"display_name": user.Username,
			"avatar_url":   nullableString(user.AvatarURL),
			"locale":       "zh-CN",
		}
		if hasEmail {
			userBlock["email"] = user.Email
		}
		resp["user"] = userBlock
	}

	if hasBalance || hasAccount {
		resp["account"] = gin.H{
			"credit_remaining": user.Balance,
			"currency_display": "CNY",
			"currency_symbol":  "￥",
		}
	}

	if hasAccount {
		if apiKey.Group != nil {
			resp["current_group"] = groupSummary(apiKey.Group, user, false)
		}
		resp["allowed_groups"] = h.allowedGroupsForUser(c, user.ID, apiKey.Group)
	}

	return resp
}

// allowedGroupsForUser builds the §12.11 `allowed_groups` array. Returns an
// empty slice (not nil) on any error so the JSON encoder emits `[]`.
//
// `is_default` flags the user's currently bound group when present.
func (h *AccountInfoHandler) allowedGroupsForUser(c *gin.Context, userID int64, currentGroup *service.Group) []gin.H {
	out := []gin.H{}
	if h.oauthService == nil {
		return out
	}
	ids, err := h.oauthService.AllowedGroupsForUser(c.Request.Context(), userID)
	if err != nil || len(ids) == 0 {
		return out
	}
	currentID := int64(0)
	if currentGroup != nil {
		currentID = currentGroup.ID
	}
	for _, gid := range ids {
		g, err := h.oauthService.LookupGroup(c.Request.Context(), gid)
		if err != nil || g == nil {
			continue
		}
		out = append(out, gin.H{
			"id":                     g.ID,
			"name":                   g.Name,
			"rate_multiplier":        g.RateMultiplier,
			"allow_image_generation": g.AllowImageGeneration,
			"is_default":             g.ID == currentID,
		})
	}
	return out
}

// groupSummary serializes a Group for /v1/me.current_group. With user-rate
// override applied when present (manual key) — for OAuth tokens we use the
// group's base rate so the answer is consistent with what `granted_scopes`
// implies.
func groupSummary(g *service.Group, user *service.User, applyUserRate bool) gin.H {
	rate := g.RateMultiplier
	if applyUserRate && user != nil && user.GroupRates != nil {
		if override, ok := user.GroupRates[g.ID]; ok {
			rate = override
		}
	}
	return gin.H{
		"id":                     g.ID,
		"name":                   g.Name,
		"rate_multiplier":        rate,
		"allow_image_generation": g.AllowImageGeneration,
	}
}

// effectiveCapabilities computes (granted scopes ∩ group capability flags) per
// §12.11. The group context narrows what a granted scope can actually do:
// a token with `images:create` bound to a group whose
// `allow_image_generation=false` has `images_create=false` here.
func effectiveCapabilities(scopes []string, group *service.Group) gin.H {
	allowImage := false
	if group != nil {
		allowImage = group.AllowImageGeneration
	}
	return gin.H{
		"profile_read":            service.HasScope(scopes, service.ScopeProfileRead),
		"email_read":              service.HasScope(scopes, service.ScopeEmailRead),
		"account_read":            service.HasScope(scopes, service.ScopeAccountRead),
		"account_balance_read":    service.HasScope(scopes, service.ScopeAccountBalanceRead),
		"models_read":             service.HasScope(scopes, service.ScopeModelsRead),
		"chat_completions_create": service.HasScope(scopes, service.ScopeChatCompletionsCreate),
		"responses_create":        service.HasScope(scopes, service.ScopeResponsesCreate),
		"messages_create":         service.HasScope(scopes, service.ScopeMessagesCreate),
		"images_create":           service.HasScope(scopes, service.ScopeImagesCreate) && allowImage,
		"usage_read":              service.HasScope(scopes, service.ScopeUsageRead),
		"offline_access":          service.HasScope(scopes, service.ScopeOfflineAccess),
	}
}

// nullableString returns nil for the empty string so the JSON encoder emits
// `null` rather than `""`. Mirrors the frontend convention of treating
// missing optional fields as nullable.
func nullableString(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// nullableStringPtr is the *string version of nullableString.
func nullableStringPtr(s *string) any {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil
	}
	return *s
}
