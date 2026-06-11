package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// ContextKeySelectableGroups carries the active, non-subscription groups in the
// OAuth token's allowed_groups snapshot, stashed by the group-override
// middleware when a backend-proxy RP requests GET /v1/models?groups=all. The
// Models handler reads it to emit an aggregated, group-tagged listing.
const ContextKeySelectableGroups ContextKey = "selectable_groups"

// groupSelector is the slice of *service.OAuthProviderService the group-override
// middleware needs. Declaring it as an interface keeps the middleware unit
// testable with a fake.
type groupSelector interface {
	// ResolveGroupOverride validates requestedGroupID against the token's
	// allowed_groups snapshot and returns the active, non-subscription target
	// group, or a typed error.
	ResolveGroupOverride(ctx context.Context, apiKeyID, requestedGroupID int64) (*service.Group, error)
	// SelectableGroupsForAPIKey returns the token's active, non-subscription
	// allowed groups for the ?groups=all aggregated model listing.
	SelectableGroupsForAPIKey(ctx context.Context, apiKeyID int64) ([]*service.Group, error)
}

// NewGroupOverrideMiddleware returns a gin middleware that lets a backend-proxy
// RP (single OAuth token, standard OpenAI-compatible body) select a Sakrylle
// group per request by prefixing the model id with "<group_id>:".
//
// Behaviour (only for OAuth-issued sk_oauth_ tokens; manual API keys pass
// through untouched):
//
//   - POST inference body with model "<gid>:<model>": the gid is validated
//     against the token's allowed_groups snapshot, the request's effective
//     group is rebound to it (routing + billing), and the prefix is stripped
//     from the body before any handler/upstream sees it.
//   - GET /v1/models?groups=all: the token's selectable groups are stashed in
//     context for the Models handler to aggregate.
//
// The group binding for billing follows apiKey.Group / apiKey.GroupID, which
// GetByKey returns as a fresh (non-cache-shared) struct, so mutating them here
// is safe.
func NewGroupOverrideMiddleware(sel groupSelector) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey, ok := GetAPIKeyFromContext(c)
		if !ok || apiKey == nil {
			c.Next()
			return
		}
		// Per-request group selection is an OAuth-token-only contract.
		if !service.IsOAuthAccessToken(apiKey.Key) {
			c.Next()
			return
		}

		switch c.Request.Method {
		case http.MethodGet:
			if isModelsPath(c.FullPath()) && wantsAllGroups(c) {
				groups, err := sel.SelectableGroupsForAPIKey(c.Request.Context(), apiKey.ID)
				if err == nil && len(groups) > 0 {
					c.Set(string(ContextKeySelectableGroups), groups)
				}
			}
			c.Next()
			return
		case http.MethodPost:
			handlePostGroupPrefix(c, sel, apiKey)
			return
		default:
			c.Next()
			return
		}
	}
}

func handlePostGroupPrefix(c *gin.Context, sel groupSelector, apiKey *service.APIKey) {
	raw, err := readBody(c)
	if err != nil {
		// Cannot read the body — leave it to the handler to error out.
		c.Next()
		return
	}
	// Always restore the (possibly unmodified) body for downstream readers.
	obj, model, found := decodeModel(raw)
	if !found {
		restoreBody(c, raw)
		c.Next()
		return
	}
	gid, rest, isSelector := parseGroupPrefix(model)
	if !isSelector {
		restoreBody(c, raw)
		c.Next()
		return
	}

	// A subscription-billed bound group can't safely switch per request: the
	// auth middleware already enforced its subscription limits before us.
	if apiKey.Group != nil && apiKey.Group.IsSubscriptionType() {
		abortGroupOverride(c, http.StatusConflict, "GROUP_OVERRIDE_UNSUPPORTED",
			"per-request group selection is not supported for subscription-billed tokens")
		return
	}

	// Same group as already bound: just strip the prefix, no rebind needed.
	if apiKey.Group != nil && gid == apiKey.Group.ID {
		rewriteModel(c, obj, rest)
		c.Next()
		return
	}

	target, err := sel.ResolveGroupOverride(c.Request.Context(), apiKey.ID, gid)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrGroupOverrideSubscription):
			abortGroupOverride(c, http.StatusConflict, "GROUP_OVERRIDE_UNSUPPORTED",
				"per-request group selection is not supported for subscription-billed groups")
		case errors.Is(err, service.ErrGroupOverrideUnavailable):
			abortGroupOverride(c, http.StatusBadRequest, "GROUP_UNAVAILABLE",
				"requested group is unavailable")
		default:
			abortGroupOverride(c, http.StatusForbidden, "GROUP_NOT_ALLOWED",
				"requested group is not allowed for this token")
		}
		return
	}

	// Rebind the effective group for routing (getGroupPlatform reads
	// apiKey.Group) and billing (recordUsage reads apiKey.GroupID/Group).
	apiKey.Group = target
	apiKey.GroupID = &target.ID
	setGroupContext(c, target)

	rewriteModel(c, obj, rest)
	c.Next()
}

// parseGroupPrefix splits a model id of the form "<group_id>:<model>". It only
// reports ok when the left segment is a positive integer and the remainder is
// non-empty, so ordinary model ids (none of which contain a leading "<digits>:"
// in this deployment) pass through untouched.
func parseGroupPrefix(model string) (groupID int64, rest string, ok bool) {
	idx := strings.IndexByte(model, ':')
	if idx <= 0 || idx == len(model)-1 {
		return 0, "", false
	}
	left := model[:idx]
	right := model[idx+1:]
	gid, err := strconv.ParseInt(left, 10, 64)
	if err != nil || gid <= 0 {
		return 0, "", false
	}
	if strings.TrimSpace(right) == "" {
		return 0, "", false
	}
	return gid, right, true
}

// decodeModel parses the request body as a JSON object and extracts the "model"
// string, preserving every other field's raw bytes so a rewrite never disturbs
// numeric precision or unknown fields.
func decodeModel(raw []byte) (obj map[string]json.RawMessage, model string, found bool) {
	if len(raw) == 0 {
		return nil, "", false
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, "", false
	}
	modelRaw, ok := obj["model"]
	if !ok {
		return obj, "", false
	}
	if err := json.Unmarshal(modelRaw, &model); err != nil {
		return obj, "", false
	}
	if model == "" {
		return obj, "", false
	}
	return obj, model, true
}

// rewriteModel re-marshals the body with model replaced by newModel and installs
// it on the request so downstream handlers and the upstream forward see the
// clean (prefix-stripped) model.
func rewriteModel(c *gin.Context, obj map[string]json.RawMessage, newModel string) {
	nb, err := json.Marshal(newModel)
	if err != nil {
		return
	}
	obj["model"] = nb
	out, err := json.Marshal(obj)
	if err != nil {
		return
	}
	setBody(c, out)
}

func readBody(c *gin.Context) ([]byte, error) {
	if c.Request.Body == nil {
		return nil, nil
	}
	return io.ReadAll(c.Request.Body)
}

func restoreBody(c *gin.Context, raw []byte) {
	setBody(c, raw)
}

func setBody(c *gin.Context, b []byte) {
	c.Request.Body = io.NopCloser(bytes.NewReader(b))
	c.Request.ContentLength = int64(len(b))
	c.Request.Header.Set("Content-Length", strconv.Itoa(len(b)))
}

func isModelsPath(fullPath string) bool {
	return strings.HasSuffix(fullPath, "/models")
}

func wantsAllGroups(c *gin.Context) bool {
	return strings.EqualFold(strings.TrimSpace(c.Query("groups")), "all")
}

// abortGroupOverride emits the OAuth-style error envelope (RFC 6750 §3.1 shape
// used across this gateway) and aborts.
func abortGroupOverride(c *gin.Context, status int, code, message string) {
	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonAPIKeyGroupUnavailable)
	c.AbortWithStatusJSON(status, gin.H{
		"error": gin.H{
			"code":    code,
			"message": message,
		},
	})
}

// GetSelectableGroupsFromContext returns the aggregated groups stashed for
// /v1/models?groups=all, if any.
func GetSelectableGroupsFromContext(c *gin.Context) ([]*service.Group, bool) {
	value, exists := c.Get(string(ContextKeySelectableGroups))
	if !exists {
		return nil, false
	}
	groups, ok := value.([]*service.Group)
	return groups, ok
}
