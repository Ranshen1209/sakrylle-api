package service

import (
	"context"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// Per-request group selection (backend-proxy RP contract).
//
// A backend-proxy relying party (e.g. Sakrylle Web / open-webui) stores a
// single OAuth access_token per user and forwards standard OpenAI-compatible
// requests. It cannot mint a per-group token in the browser the way the Image
// SPA does. To let such a client pick a Sakrylle group per request, the client
// prefixes the model id with "<group_id>:" (e.g. "12:claude-opus-4-6"). The
// gateway resolves that group here, bounded by the SAME allowed_groups snapshot
// the refresh-grant group switch uses — a single token can never widen its
// grant.
//
// Only non-subscription (balance-billed) groups are selectable per-request:
// the auth middleware enforces subscription limits against the token's bound
// group before any handler runs, so silently rebinding to a different
// subscription group would bypass those checks. All Sakrylle groups are
// balance-billed, so this is not a practical limitation.
var (
	// ErrGroupOverrideUnavailable — the requested group is missing, deleted, or
	// disabled.
	ErrGroupOverrideUnavailable = infraerrors.BadRequest("GROUP_UNAVAILABLE", "requested group is unavailable")
	// ErrGroupOverrideSubscription — per-request selection is not supported for
	// subscription-billed groups.
	ErrGroupOverrideSubscription = infraerrors.BadRequest("GROUP_OVERRIDE_UNSUPPORTED", "per-request group selection is not supported for subscription-billed groups")
)

// ResolveGroupOverride validates that the OAuth token bound to apiKeyID may
// target requestedGroupID per its allowed_groups snapshot, and returns the
// active, non-subscription target group ready for routing + billing.
//
// Returns:
//   - ErrOAuthGroupNotAllowed if the token carries no OAuth metadata (manual
//     key, or scope enforcement disabled) or the group is outside the snapshot.
//   - ErrGroupOverrideUnavailable if the group is missing/deleted/disabled.
//   - ErrGroupOverrideSubscription if the group is subscription-billed.
func (s *OAuthProviderService) ResolveGroupOverride(ctx context.Context, apiKeyID, requestedGroupID int64) (*Group, error) {
	if requestedGroupID <= 0 {
		return nil, ErrOAuthGroupNotAllowed
	}
	meta, err := s.LoadOAuthAccessMetadata(ctx, apiKeyID)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		// No OAuth metadata: either a manual API key (which has no per-request
		// group contract) or scope enforcement is off. Either way, the token is
		// not authorized to switch groups.
		return nil, ErrOAuthGroupNotAllowed
	}
	if !int64InSlice(meta.AllowedGroupIDs, requestedGroupID) {
		return nil, ErrOAuthGroupNotAllowed
	}
	if s.groupRepo == nil {
		return nil, ErrGroupOverrideUnavailable
	}
	g, err := s.groupRepo.GetByID(ctx, requestedGroupID)
	if err != nil || g == nil {
		return nil, ErrGroupOverrideUnavailable
	}
	if !g.IsActive() || strings.EqualFold(g.Status, "deleted") {
		return nil, ErrGroupOverrideUnavailable
	}
	if g.IsSubscriptionType() {
		return nil, ErrGroupOverrideSubscription
	}
	return g, nil
}

// SelectableGroupsForAPIKey returns the active, non-subscription groups in the
// token's allowed_groups snapshot — the set a backend-proxy RP can enumerate
// via GET /v1/models?groups=all. Returns an empty slice (not an error) when the
// token carries no OAuth metadata, so the caller falls back to single-group
// behavior.
func (s *OAuthProviderService) SelectableGroupsForAPIKey(ctx context.Context, apiKeyID int64) ([]*Group, error) {
	meta, err := s.LoadOAuthAccessMetadata(ctx, apiKeyID)
	if err != nil {
		return nil, err
	}
	if meta == nil || len(meta.AllowedGroupIDs) == 0 {
		return nil, nil
	}
	if s.groupRepo == nil {
		return nil, nil
	}
	out := make([]*Group, 0, len(meta.AllowedGroupIDs))
	for _, gid := range meta.AllowedGroupIDs {
		g, err := s.groupRepo.GetByID(ctx, gid)
		if err != nil || g == nil {
			continue
		}
		if !g.IsActive() || g.IsSubscriptionType() {
			continue
		}
		out = append(out, g)
	}
	return out, nil
}
