package middleware

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestParseGroupPrefix(t *testing.T) {
	cases := []struct {
		in   string
		gid  int64
		rest string
		ok   bool
	}{
		{"12:claude-opus-4-6", 12, "claude-opus-4-6", true},
		{"5:gpt-image-2-async", 5, "gpt-image-2-async", true},
		{"claude-opus-4-6", 0, "", false},             // no prefix
		{"gpt-5.4", 0, "", false},                     // dotted, no colon selector
		{"12:", 0, "", false},                         // empty model
		{":foo", 0, "", false},                        // empty gid
		{"abc:foo", 0, "", false},                     // non-numeric gid
		{"-1:foo", 0, "", false},                      // negative
		{"0:foo", 0, "", false},                       // zero is not a valid group id
		{"12:claude:weird", 12, "claude:weird", true}, // only first colon splits
	}
	for _, tc := range cases {
		gid, rest, ok := parseGroupPrefix(tc.in)
		require.Equalf(t, tc.ok, ok, "ok for %q", tc.in)
		if tc.ok {
			require.Equalf(t, tc.gid, gid, "gid for %q", tc.in)
			require.Equalf(t, tc.rest, rest, "rest for %q", tc.in)
		}
	}
}

type fakeGroupSelector struct {
	resolve    func(ctx context.Context, apiKeyID, gid int64) (*service.Group, error)
	selectable func(ctx context.Context, apiKeyID int64) ([]*service.Group, error)
}

func (f fakeGroupSelector) ResolveGroupOverride(ctx context.Context, apiKeyID, gid int64) (*service.Group, error) {
	return f.resolve(ctx, apiKeyID, gid)
}

func (f fakeGroupSelector) SelectableGroupsForAPIKey(ctx context.Context, apiKeyID int64) ([]*service.Group, error) {
	return f.selectable(ctx, apiKeyID)
}

// buildRouter wires the group-override middleware behind a stub that injects the
// given apiKey into context, then a terminal handler that records the body the
// handler actually sees and the effective group.
func buildRouter(sel groupSelector, apiKey *service.APIKey) (*gin.Engine, *capturedRequest) {
	gin.SetMode(gin.TestMode)
	cap := &capturedRequest{}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), apiKey)
		c.Next()
	})
	r.Use(NewGroupOverrideMiddleware(sel))
	terminal := func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		cap.body = string(body)
		if ak, ok := GetAPIKeyFromContext(c); ok && ak.Group != nil {
			cap.effectiveGroupID = ak.Group.ID
		}
		if groups, ok := GetSelectableGroupsFromContext(c); ok {
			cap.selectable = groups
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
	r.POST("/v1/chat/completions", terminal)
	r.GET("/v1/models", terminal)
	return r, cap
}

type capturedRequest struct {
	body             string
	effectiveGroupID int64
	selectable       []*service.Group
}

func oauthKey(group *service.Group) *service.APIKey {
	var gid *int64
	if group != nil {
		gid = &group.ID
	}
	return &service.APIKey{
		ID:      100,
		Key:     "sk_oauth_test",
		Status:  service.StatusActive,
		GroupID: gid,
		Group:   group,
	}
}

func grp(id int64, name string) *service.Group {
	return &service.Group{ID: id, Name: name, Platform: service.PlatformOpenAI, Status: service.StatusActive, RateMultiplier: 1}
}

func TestGroupOverride_NonOAuthKeyPassesThrough(t *testing.T) {
	sel := fakeGroupSelector{
		resolve: func(context.Context, int64, int64) (*service.Group, error) {
			t.Fatal("resolver must not be called for a manual key")
			return nil, nil
		},
	}
	apiKey := &service.APIKey{ID: 1, Key: "sk-manual", Status: service.StatusActive, Group: grp(7, "Code")}
	r, cap := buildRouter(sel, apiKey)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"12:claude-opus-4-6"}`))
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	// Manual keys: body untouched (prefix not interpreted), group unchanged.
	require.Contains(t, cap.body, `"12:claude-opus-4-6"`)
	require.Equal(t, int64(7), cap.effectiveGroupID)
}

func TestGroupOverride_NoPrefixPassesThrough(t *testing.T) {
	sel := fakeGroupSelector{
		resolve: func(context.Context, int64, int64) (*service.Group, error) {
			t.Fatal("resolver must not be called without a prefix")
			return nil, nil
		},
	}
	r, cap := buildRouter(sel, oauthKey(grp(7, "Code")))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"claude-opus-4-6","stream":true}`))
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, cap.body, `"claude-opus-4-6"`)
	require.Contains(t, cap.body, `"stream":true`)
	require.Equal(t, int64(7), cap.effectiveGroupID)
}

func TestGroupOverride_ValidPrefixRebindsAndStrips(t *testing.T) {
	target := grp(12, "Claude-Max")
	target.Platform = service.PlatformOpenAI
	var gotAPIKeyID, gotGID int64
	sel := fakeGroupSelector{
		resolve: func(_ context.Context, apiKeyID, gid int64) (*service.Group, error) {
			gotAPIKeyID, gotGID = apiKeyID, gid
			return target, nil
		},
	}
	r, cap := buildRouter(sel, oauthKey(grp(7, "Code")))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"12:claude-opus-4-6","max_tokens":1000000}`))
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, int64(100), gotAPIKeyID)
	require.Equal(t, int64(12), gotGID)
	require.Equal(t, int64(12), cap.effectiveGroupID, "group must be rebound to the target")

	// Prefix stripped; other fields preserved byte-exact (no 1e+06).
	var decoded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(cap.body), &decoded))
	require.JSONEq(t, `"claude-opus-4-6"`, string(decoded["model"]))
	require.JSONEq(t, `1000000`, string(decoded["max_tokens"]))
}

func TestGroupOverride_SameGroupJustStrips(t *testing.T) {
	sel := fakeGroupSelector{
		resolve: func(context.Context, int64, int64) (*service.Group, error) {
			t.Fatal("resolver must not be called when prefix equals bound group")
			return nil, nil
		},
	}
	r, cap := buildRouter(sel, oauthKey(grp(7, "Code")))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"7:gpt-5.4"}`))
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, cap.body, `"gpt-5.4"`)
	require.NotContains(t, cap.body, `"7:gpt-5.4"`)
	require.Equal(t, int64(7), cap.effectiveGroupID)
}

func TestGroupOverride_NotAllowedReturns403(t *testing.T) {
	sel := fakeGroupSelector{
		resolve: func(context.Context, int64, int64) (*service.Group, error) {
			return nil, service.ErrOAuthGroupNotAllowed
		},
	}
	r, _ := buildRouter(sel, oauthKey(grp(7, "Code")))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"99:claude-opus-4-6"}`))
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "GROUP_NOT_ALLOWED")
}

func TestGroupOverride_SubscriptionTargetReturns409(t *testing.T) {
	sel := fakeGroupSelector{
		resolve: func(context.Context, int64, int64) (*service.Group, error) {
			return nil, service.ErrGroupOverrideSubscription
		},
	}
	r, _ := buildRouter(sel, oauthKey(grp(7, "Code")))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"16:claude-fable-5"}`))
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusConflict, w.Code)
	require.Contains(t, w.Body.String(), "GROUP_OVERRIDE_UNSUPPORTED")
}

func TestGroupOverride_UnavailableTargetReturns400(t *testing.T) {
	sel := fakeGroupSelector{
		resolve: func(context.Context, int64, int64) (*service.Group, error) {
			return nil, service.ErrGroupOverrideUnavailable
		},
	}
	r, _ := buildRouter(sel, oauthKey(grp(7, "Code")))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"13:gpt-image-2-async"}`))
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "GROUP_UNAVAILABLE")
}

func TestGroupOverride_ModelsAllStashesSelectableGroups(t *testing.T) {
	groups := []*service.Group{grp(7, "Code"), grp(12, "Claude-Max")}
	sel := fakeGroupSelector{
		selectable: func(context.Context, int64) ([]*service.Group, error) {
			return groups, nil
		},
	}
	r, cap := buildRouter(sel, oauthKey(grp(7, "Code")))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models?groups=all", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, cap.selectable, 2)
	require.Equal(t, int64(7), cap.selectable[0].ID)
	require.Equal(t, int64(12), cap.selectable[1].ID)
}

func TestGroupOverride_ModelsWithoutGroupsAllDoesNotStash(t *testing.T) {
	sel := fakeGroupSelector{
		selectable: func(context.Context, int64) ([]*service.Group, error) {
			t.Fatal("selectable must not be called without ?groups=all")
			return nil, nil
		},
	}
	r, cap := buildRouter(sel, oauthKey(grp(7, "Code")))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Nil(t, cap.selectable)
}
