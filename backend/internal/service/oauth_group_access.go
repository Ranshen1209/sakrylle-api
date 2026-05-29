package service

import (
	"context"
	"fmt"
)

// GroupAccessPolicy describes the policy used to evaluate which groups a user
// may bind via OAuth. It is intentionally narrow so the OAuth service can
// reuse the existing API-key group-binding rules (subscription groups,
// exclusive groups, AllowedGroups) without depending on APIKeyService directly.
//
// See docs/OAUTH_V2_DESIGN.md §11.4.
type GroupAccessPolicy interface {
	// CanUserUseGroup reports whether user can bind groupID under the same
	// rules used by ListUserAllowedGroupsForOAuth.
	CanUserUseGroup(ctx context.Context, userID int64, groupID int64) (bool, error)
	// ListUserAllowedGroupsForOAuth returns the rich group objects for all
	// active groups the user may bind, filtered by:
	//   - client.AllowedGroupIDs (when non-nil)
	//   - scope-based capability: image-only clients see only image groups;
	//     chat-only clients exclude image-only groups.
	// Pass nil client and empty scopes for non-OAuth paths (no extra filtering).
	ListUserAllowedGroupsForOAuth(ctx context.Context, userID int64, client *OAuthClient, scopes []string) ([]OAuthAllowedGroup, error)
}

// defaultGroupAccessPolicy implements GroupAccessPolicy by reusing the
// existing user/group/subscription bindings the API-key service already
// owns. It is the only production wiring; tests inject their own stub.
type defaultGroupAccessPolicy struct {
	userRepo    UserRepository
	groupRepo   GroupRepository
	userSubRepo UserSubscriptionRepository
}

// NewDefaultGroupAccessPolicy constructs the production GroupAccessPolicy.
//
// userRepo / groupRepo / userSubRepo MUST be non-nil; the OAuth service
// will not work meaningfully without them, but we tolerate nils in test
// harnesses by returning a deny-all policy at runtime.
func NewDefaultGroupAccessPolicy(
	userRepo UserRepository,
	groupRepo GroupRepository,
	userSubRepo UserSubscriptionRepository,
) GroupAccessPolicy {
	return &defaultGroupAccessPolicy{
		userRepo:    userRepo,
		groupRepo:   groupRepo,
		userSubRepo: userSubRepo,
	}
}

func (p *defaultGroupAccessPolicy) ListUserAllowedGroupsForOAuth(
	ctx context.Context,
	userID int64,
	client *OAuthClient,
	scopes []string,
) ([]OAuthAllowedGroup, error) {
	if p.groupRepo == nil || p.userRepo == nil {
		return []OAuthAllowedGroup{}, nil
	}
	user, err := p.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("oauth group access: load user %d: %w", userID, err)
	}
	allGroups, err := p.groupRepo.ListActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("oauth group access: list active groups: %w", err)
	}
	subscribed := map[int64]bool{}
	if p.userSubRepo != nil {
		subs, ferr := p.userSubRepo.ListActiveByUserID(ctx, userID)
		if ferr != nil {
			return nil, fmt.Errorf("oauth group access: list active subscriptions: %w", ferr)
		}
		for _, s := range subs {
			subscribed[s.GroupID] = true
		}
	}

	// Build client-level group allowlist for fast lookup (nil = no restriction).
	var clientAllowSet map[int64]struct{}
	if client != nil && len(client.AllowedGroupIDs) > 0 {
		clientAllowSet = make(map[int64]struct{}, len(client.AllowedGroupIDs))
		for _, id := range client.AllowedGroupIDs {
			clientAllowSet[id] = struct{}{}
		}
	}

	// Determine scope-based capability filter.
	wantsImage := hasAnyScope(scopes, ScopeImagesCreate)
	wantsChat := hasAnyScope(scopes,
		ScopeChatCompletionsCreate,
		ScopeMessagesCreate,
		ScopeResponsesCreate,
	)

	out := make([]OAuthAllowedGroup, 0, len(allGroups))
	for i := range allGroups {
		g := &allGroups[i]
		if !p.canBind(user, g, subscribed) {
			continue
		}
		// Client-level group restriction.
		if clientAllowSet != nil {
			if _, ok := clientAllowSet[g.ID]; !ok {
				continue
			}
		}
		// Scope-based capability filter:
		//   - image-only scope (images:create, no chat): only image-capable groups.
		//   - chat-only scope (no images:create): exclude image-only groups.
		//   - both or neither: include all accessible groups.
		if wantsImage && !wantsChat && !g.AllowImageGeneration {
			continue
		}
		if wantsChat && !wantsImage && g.AllowImageGeneration {
			// Exclude groups that are image-only. For phase 1 we use
			// AllowImageGeneration as the proxy: a group with
			// allow_image_generation=true is treated as image-capable; chat
			// clients that don't request images:create skip it.
			continue
		}
		out = append(out, OAuthAllowedGroup{
			ID:                   g.ID,
			Name:                 g.Name,
			RateMultiplier:       g.RateMultiplier,
			AllowImageGeneration: g.AllowImageGeneration,
		})
	}
	return out, nil
}

// hasAnyScope returns true if scopes contains any of the targets.
func hasAnyScope(scopes []string, targets ...string) bool {
	for _, s := range scopes {
		for _, t := range targets {
			if s == t {
				return true
			}
		}
	}
	return false
}

func (p *defaultGroupAccessPolicy) CanUserUseGroup(ctx context.Context, userID, groupID int64) (bool, error) {
	if groupID <= 0 {
		return false, nil
	}
	if p.groupRepo == nil || p.userRepo == nil {
		return false, nil
	}
	user, err := p.userRepo.GetByID(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("oauth group access: load user %d: %w", userID, err)
	}
	group, err := p.groupRepo.GetByID(ctx, groupID)
	if err != nil {
		return false, nil
	}
	if !group.IsActive() {
		return false, nil
	}
	subscribed := map[int64]bool{}
	if p.userSubRepo != nil && group.IsSubscriptionType() {
		_, gerr := p.userSubRepo.GetActiveByUserIDAndGroupID(ctx, userID, group.ID)
		if gerr == nil {
			subscribed[group.ID] = true
		}
	}
	return p.canBind(user, group, subscribed), nil
}

// canBind mirrors APIKeyService.canUserBindGroupInternal: subscription groups
// require a current active subscription, otherwise standard groups follow
// the AllowedGroups + IsExclusive rules.
func (p *defaultGroupAccessPolicy) canBind(user *User, group *Group, subscribed map[int64]bool) bool {
	if user == nil || group == nil {
		return false
	}
	if !group.IsActive() {
		return false
	}
	if group.IsSubscriptionType() {
		return subscribed[group.ID]
	}
	return user.CanBindGroup(group.ID, group.IsExclusive)
}
