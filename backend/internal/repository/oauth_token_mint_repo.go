package repository

import (
	"context"
	"fmt"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// oauthTokenMintRepository implements service.OAuthTokenMintRepository — the
// FIX H4 atomic mint surface that writes the api_keys + oauth_access_tokens +
// oauth_refresh_tokens trio inside a single Postgres transaction.
//
// Lives in a separate file from oauth_provider_repo.go because it crosses
// entity boundaries (api_keys is a top-level table, not an oauth_* family
// member) and shares no helpers with the per-entity repos.
type oauthTokenMintRepository struct {
	client *dbent.Client
}

// NewOAuthTokenMintRepo returns the FIX H4 atomic-mint repo view.
func NewOAuthTokenMintRepo(client *dbent.Client) service.OAuthTokenMintRepository {
	return &oauthTokenMintRepository{client: client}
}

// MintTokenSet writes the token trio in one transaction. Order:
//  1. INSERT api_keys (ID assigned).
//  2. INSERT oauth_access_tokens with api_key_id = api_keys.id (when set).
//  3. INSERT oauth_refresh_tokens with api_key_id = api_keys.id (when set).
//
// On any failure the tx rolls back so the gateway can never observe a live
// sk_oauth_* api_keys row that lacks scope metadata.
func (r *oauthTokenMintRepository) MintTokenSet(ctx context.Context, params *service.OAuthTokenMintParams) error {
	if params == nil || params.APIKey == nil {
		return fmt.Errorf("oauth mint: api_key is required")
	}
	return withTx(ctx, r.client, func(tx *dbent.Tx) error {
		// 1. api_keys
		akb := tx.APIKey.Create().
			SetUserID(params.APIKey.UserID).
			SetKey(params.APIKey.Key).
			SetName(params.APIKey.Name).
			SetStatus(params.APIKey.Status).
			SetNillableGroupID(params.APIKey.GroupID).
			SetNillableLastUsedAt(params.APIKey.LastUsedAt).
			SetQuota(params.APIKey.Quota).
			SetQuotaUsed(params.APIKey.QuotaUsed).
			SetNillableExpiresAt(params.APIKey.ExpiresAt).
			SetRateLimit5h(params.APIKey.RateLimit5h).
			SetRateLimit1d(params.APIKey.RateLimit1d).
			SetRateLimit7d(params.APIKey.RateLimit7d)
		if len(params.APIKey.IPWhitelist) > 0 {
			akb = akb.SetIPWhitelist(params.APIKey.IPWhitelist)
		}
		if len(params.APIKey.IPBlacklist) > 0 {
			akb = akb.SetIPBlacklist(params.APIKey.IPBlacklist)
		}
		created, aerr := akb.Save(ctx)
		if aerr != nil {
			return translatePersistenceError(aerr, nil, service.ErrAPIKeyExists)
		}
		params.APIKey.ID = created.ID
		params.APIKey.CreatedAt = created.CreatedAt
		params.APIKey.UpdatedAt = created.UpdatedAt

		// 2. oauth_access_tokens (optional)
		if params.AccessToken != nil {
			at := params.AccessToken
			at.APIKeyID = created.ID
			atb := tx.OAuthAccessToken.Create().
				SetAPIKeyID(at.APIKeyID).
				SetGrantID(at.GrantID).
				SetTokenFamilyID(at.TokenFamilyID).
				SetClientID(at.ClientID).
				SetUserID(at.UserID).
				SetScopes(at.Scopes).
				SetGroupID(at.GroupID).
				SetAllowedGroupsSnapshot(at.AllowedGroupIDs).
				SetAppType(at.AppType).
				SetIssuedAt(at.IssuedAt).
				SetExpiresAt(at.ExpiresAt)
			if at.DeviceID != nil {
				atb = atb.SetDeviceID(*at.DeviceID)
			}
			if at.DeviceName != nil {
				atb = atb.SetDeviceName(*at.DeviceName)
			}
			if _, aterr := atb.Save(ctx); aterr != nil {
				return fmt.Errorf("create oauth access token: %w", aterr)
			}
		}

		// 3. oauth_refresh_tokens (optional)
		if params.RefreshToken != nil {
			rt := params.RefreshToken
			rt.APIKeyID = created.ID
			rtb := tx.OAuthRefreshToken.Create().
				SetTokenHash(rt.TokenHash).
				SetClientID(rt.ClientID).
				SetUserID(rt.UserID).
				SetAPIKeyID(rt.APIKeyID).
				SetScopes(rt.Scopes).
				SetExpiresAt(rt.ExpiresAt).
				SetAllowedGroupsSnapshot(rt.AllowedGroupsSnapshot)
			if rt.GrantID != nil {
				rtb = rtb.SetGrantID(*rt.GrantID)
			}
			if rt.TokenFamilyID != nil {
				rtb = rtb.SetTokenFamilyID(*rt.TokenFamilyID)
			}
			if rt.GroupID != nil {
				rtb = rtb.SetGroupID(*rt.GroupID)
			}
			if rt.DeviceID != nil {
				rtb = rtb.SetDeviceID(*rt.DeviceID)
			}
			if rt.DeviceName != nil {
				rtb = rtb.SetDeviceName(*rt.DeviceName)
			}
			if _, rterr := rtb.Save(ctx); rterr != nil {
				return fmt.Errorf("create oauth refresh token: %w", rterr)
			}
		}
		return nil
	})
}

// Compile-time assertion.
var _ service.OAuthTokenMintRepository = (*oauthTokenMintRepository)(nil)
