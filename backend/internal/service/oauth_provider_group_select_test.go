package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// mapGroupRepo serves groups from an in-memory map; every other GroupRepository
// method is nil (embedded interface) so an unexpected call panics loudly.
type mapGroupRepo struct {
	GroupRepository
	rows map[int64]*Group
}

func (r *mapGroupRepo) GetByID(_ context.Context, id int64) (*Group, error) {
	if g, ok := r.rows[id]; ok {
		return g, nil
	}
	return nil, ErrGroupOverrideUnavailable
}

func newGroupSelectService(t *testing.T, allowed []int64, rows map[int64]*Group, enforcement bool) *OAuthProviderService {
	t.Helper()
	access := newStubAccessRepo()
	if allowed != nil {
		access.rows[100] = &OAuthAccessToken{APIKeyID: 100, GroupID: allowed[0], AllowedGroupIDs: allowed}
	}
	settings := map[string]string{}
	if enforcement {
		settings["oauth_scope_enforcement_enabled"] = "true"
	}
	groupRepo := &mapGroupRepo{rows: rows}
	return NewOAuthProviderService(
		nil, nil, nil,
		access,
		nil, nil, nil,
		groupRepo,
		nil,
		&stubSettingRepo{values: settings},
		nil,
	)
}

func activeGroup(id int64) *Group  { return &Group{ID: id, Name: "g", Status: StatusActive, RateMultiplier: 1} }
func subGroup(id int64) *Group     { g := activeGroup(id); g.SubscriptionType = SubscriptionTypeSubscription; return g }
func disabledGroup(id int64) *Group { g := activeGroup(id); g.Status = "disabled"; return g }

func TestResolveGroupOverride_Allowed(t *testing.T) {
	svc := newGroupSelectService(t, []int64{7, 12}, map[int64]*Group{12: activeGroup(12)}, true)
	g, err := svc.ResolveGroupOverride(context.Background(), 100, 12)
	require.NoError(t, err)
	require.Equal(t, int64(12), g.ID)
}

func TestResolveGroupOverride_NotInSnapshot(t *testing.T) {
	svc := newGroupSelectService(t, []int64{7, 12}, map[int64]*Group{99: activeGroup(99)}, true)
	_, err := svc.ResolveGroupOverride(context.Background(), 100, 99)
	require.ErrorIs(t, err, ErrOAuthGroupNotAllowed)
}

func TestResolveGroupOverride_SubscriptionGroup(t *testing.T) {
	svc := newGroupSelectService(t, []int64{16}, map[int64]*Group{16: subGroup(16)}, true)
	_, err := svc.ResolveGroupOverride(context.Background(), 100, 16)
	require.ErrorIs(t, err, ErrGroupOverrideSubscription)
}

func TestResolveGroupOverride_InactiveGroup(t *testing.T) {
	svc := newGroupSelectService(t, []int64{13}, map[int64]*Group{13: disabledGroup(13)}, true)
	_, err := svc.ResolveGroupOverride(context.Background(), 100, 13)
	require.ErrorIs(t, err, ErrGroupOverrideUnavailable)
}

func TestResolveGroupOverride_NoMetadata(t *testing.T) {
	// No access row seeded → LoadOAuthAccessMetadata returns nil meta.
	svc := newGroupSelectService(t, nil, map[int64]*Group{12: activeGroup(12)}, true)
	_, err := svc.ResolveGroupOverride(context.Background(), 100, 12)
	require.ErrorIs(t, err, ErrOAuthGroupNotAllowed)
}

func TestResolveGroupOverride_EnforcementDisabled(t *testing.T) {
	// Enforcement off → LoadOAuthAccessMetadata returns nil → not allowed.
	svc := newGroupSelectService(t, []int64{12}, map[int64]*Group{12: activeGroup(12)}, false)
	_, err := svc.ResolveGroupOverride(context.Background(), 100, 12)
	require.ErrorIs(t, err, ErrOAuthGroupNotAllowed)
}

func TestSelectableGroupsForAPIKey_FiltersInactiveAndSubscription(t *testing.T) {
	rows := map[int64]*Group{
		7:  activeGroup(7),
		12: activeGroup(12),
		16: subGroup(16),      // filtered: subscription
		13: disabledGroup(13), // filtered: inactive
	}
	svc := newGroupSelectService(t, []int64{7, 12, 16, 13}, rows, true)
	groups, err := svc.SelectableGroupsForAPIKey(context.Background(), 100)
	require.NoError(t, err)
	require.Len(t, groups, 2)
	ids := []int64{groups[0].ID, groups[1].ID}
	require.ElementsMatch(t, []int64{7, 12}, ids)
}

func TestSelectableGroupsForAPIKey_NoMetadataReturnsNil(t *testing.T) {
	svc := newGroupSelectService(t, nil, map[int64]*Group{}, true)
	groups, err := svc.SelectableGroupsForAPIKey(context.Background(), 100)
	require.NoError(t, err)
	require.Nil(t, groups)
}

// Guard: the sentinel errors are distinct so the middleware can branch on them.
func TestGroupOverrideSentinelsDistinct(t *testing.T) {
	require.False(t, errors.Is(ErrGroupOverrideSubscription, ErrGroupOverrideUnavailable))
	require.False(t, errors.Is(ErrGroupOverrideUnavailable, ErrOAuthGroupNotAllowed))
}
