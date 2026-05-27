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
	// AllowedGroupsForUser returns the IDs of all active groups the user may
	// bind. The result mirrors APIKeyService.GetAvailableGroups() filtering
	// (active + subscription + AllowedGroups + exclusive). Used both for the
	// authorize transaction's allowed_groups_snapshot and for /v1/me.
	AllowedGroupsForUser(ctx context.Context, userID int64) ([]int64, error)
	// UserHasAccessToGroup reports whether user can bind groupID under the
	// same rules used by AllowedGroupsForUser.
	UserHasAccessToGroup(ctx context.Context, userID int64, groupID int64) (bool, error)
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

func (p *defaultGroupAccessPolicy) AllowedGroupsForUser(ctx context.Context, userID int64) ([]int64, error) {
	if p.groupRepo == nil || p.userRepo == nil {
		return []int64{}, nil
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
	out := make([]int64, 0, len(allGroups))
	for i := range allGroups {
		g := &allGroups[i]
		if !p.canBind(user, g, subscribed) {
			continue
		}
		out = append(out, g.ID)
	}
	return out, nil
}

func (p *defaultGroupAccessPolicy) UserHasAccessToGroup(ctx context.Context, userID, groupID int64) (bool, error) {
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
