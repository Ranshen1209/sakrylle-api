package repository

import (
	"context"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/oauthaccesstoken"
	"github.com/Wei-Shaw/sub2api/ent/oauthauthorizetransaction"
	"github.com/Wei-Shaw/sub2api/ent/oauthclient"
	"github.com/Wei-Shaw/sub2api/ent/oauthcode"
	"github.com/Wei-Shaw/sub2api/ent/oauthdevicecode"
	"github.com/Wei-Shaw/sub2api/ent/oauthrefreshtoken"
	"github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// oauthProviderRepository is an Ent-backed implementation of every OAuth
// provider repository interface (clients, codes, refresh tokens, access
// metadata, device codes, authorize transactions).
//
// One struct backs all interfaces because they share an *ent.Client and a
// withTx helper; the interface views are returned by the New*Repo factory
// functions so the service layer composes only the surface area it needs.
type oauthProviderRepository struct {
	client *dbent.Client
}

// NewOAuthClientRepo returns the OAuth client repository view.
func NewOAuthClientRepo(client *dbent.Client) service.OAuthClientRepository {
	return &oauthProviderRepository{client: client}
}

// NewOAuthCodeRepo returns the OAuth authorization code repository view.
func NewOAuthCodeRepo(client *dbent.Client) service.OAuthCodeRepository {
	return &oauthProviderRepository{client: client}
}

// NewOAuthRefreshTokenRepo returns the OAuth refresh token repository view.
func NewOAuthRefreshTokenRepo(client *dbent.Client) service.OAuthRefreshTokenRepository {
	return &oauthProviderRepository{client: client}
}

// NewOAuthAccessTokenRepo returns the OAuth access-token metadata repository view.
func NewOAuthAccessTokenRepo(client *dbent.Client) service.OAuthAccessTokenRepository {
	return &oauthProviderRepository{client: client}
}

// NewOAuthDeviceCodeRepo returns the OAuth device-code repository view.
func NewOAuthDeviceCodeRepo(client *dbent.Client) service.OAuthDeviceCodeRepository {
	return &oauthProviderRepository{client: client}
}

// NewOAuthAuthorizeTransactionRepo returns the OAuth authorize-transaction repository view.
func NewOAuthAuthorizeTransactionRepo(client *dbent.Client) service.OAuthAuthorizeTransactionRepository {
	return &oauthProviderRepository{client: client}
}

// ── OAuthClientRepository ───────────────────────────────────────────────────

func (r *oauthProviderRepository) GetClientByID(ctx context.Context, clientID string) (*service.OAuthClient, error) {
	row, err := r.client.OAuthClient.Query().
		Where(oauthclient.ClientIDEQ(clientID)).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrOAuthClientNotFound
		}
		return nil, fmt.Errorf("query oauth client: %w", err)
	}
	return entOAuthClientToService(row), nil
}

// ListEnabledRedirectURIs returns the flat union of redirect_uris across every
// non-disabled client. Order is not guaranteed; the service layer normalizes
// (parse → origin → dedupe → sort) before exposing the list.
//
// Selecting only the redirect_uris column keeps this cheap to call on a
// 5-minute refresh tick even with many clients registered.
func (r *oauthProviderRepository) ListEnabledRedirectURIs(ctx context.Context) ([]string, error) {
	rows, err := r.client.OAuthClient.Query().
		Where(oauthclient.DisabledEQ(false)).
		Select(oauthclient.FieldRedirectUris).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list oauth client redirect_uris: %w", err)
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.RedirectUris...)
	}
	return out, nil
}

// ── OAuthCodeRepository ─────────────────────────────────────────────────────

func (r *oauthProviderRepository) CreateCode(ctx context.Context, code *service.OAuthCode) error {
	q := r.client.OAuthCode.Create().
		SetCodeHash(code.CodeHash).
		SetClientID(code.ClientID).
		SetUserID(code.UserID).
		SetRedirectURI(code.RedirectURI).
		SetScopes(code.Scopes).
		SetCodeChallenge(code.CodeChallenge).
		SetCodeChallengeMethod(code.CodeChallengeMethod).
		SetExpiresAt(code.ExpiresAt).
		SetAllowedGroupsSnapshot(code.AllowedGroupsSnapshot)
	if code.GroupID != nil {
		q = q.SetGroupID(*code.GroupID)
	}
	if code.GrantID != nil {
		q = q.SetGrantID(*code.GrantID)
	}
	if code.DeviceID != nil {
		q = q.SetDeviceID(*code.DeviceID)
	}
	if code.DeviceName != nil {
		q = q.SetDeviceName(*code.DeviceName)
	}
	if code.Nonce != "" {
		q = q.SetNonce(code.Nonce)
	}
	if _, err := q.Save(ctx); err != nil {
		return fmt.Errorf("create oauth code: %w", err)
	}
	return nil
}

func (r *oauthProviderRepository) ConsumeCode(ctx context.Context, codeHash string, now time.Time) (*service.OAuthCode, error) {
	var consumed *service.OAuthCode
	err := withTx(ctx, r.client, func(tx *dbent.Tx) error {
		row, qerr := tx.OAuthCode.Query().
			Where(oauthcode.CodeHashEQ(codeHash)).
			ForUpdate().
			Only(ctx)
		if qerr != nil {
			if dbent.IsNotFound(qerr) {
				return service.ErrOAuthCodeNotFound
			}
			return fmt.Errorf("lock oauth code: %w", qerr)
		}
		// Capture the row state BEFORE returning so the caller has access to
		// user_id/client_id for replay-driven token revocation.
		consumed = entOAuthCodeToService(row)
		if row.UsedAt != nil {
			return service.ErrOAuthCodeAlreadyUsed
		}
		if !row.ExpiresAt.After(now) {
			return service.ErrOAuthCodeExpired
		}
		if _, uerr := row.Update().SetUsedAt(now).Save(ctx); uerr != nil {
			return fmt.Errorf("mark oauth code used: %w", uerr)
		}
		return nil
	})
	return consumed, err
}

func (r *oauthProviderRepository) DeleteExpiredCodes(ctx context.Context, before time.Time) (int, error) {
	n, err := r.client.OAuthCode.Delete().
		Where(oauthcode.ExpiresAtLT(before)).
		Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("delete expired oauth codes: %w", err)
	}
	return n, nil
}

// ── OAuthRefreshTokenRepository ─────────────────────────────────────────────

func (r *oauthProviderRepository) CreateRefreshToken(ctx context.Context, token *service.OAuthRefreshToken) error {
	q := r.client.OAuthRefreshToken.Create().
		SetTokenHash(token.TokenHash).
		SetClientID(token.ClientID).
		SetUserID(token.UserID).
		SetAPIKeyID(token.APIKeyID).
		SetScopes(token.Scopes).
		SetExpiresAt(token.ExpiresAt).
		SetAllowedGroupsSnapshot(token.AllowedGroupsSnapshot)
	if token.GrantID != nil {
		q = q.SetGrantID(*token.GrantID)
	}
	if token.TokenFamilyID != nil {
		q = q.SetTokenFamilyID(*token.TokenFamilyID)
	}
	if token.GroupID != nil {
		q = q.SetGroupID(*token.GroupID)
	}
	if token.DeviceID != nil {
		q = q.SetDeviceID(*token.DeviceID)
	}
	if token.DeviceName != nil {
		q = q.SetDeviceName(*token.DeviceName)
	}
	if _, err := q.Save(ctx); err != nil {
		return fmt.Errorf("create oauth refresh token: %w", err)
	}
	return nil
}

func (r *oauthProviderRepository) ConsumeForRotation(
	ctx context.Context,
	oldHash, newHash string,
	now time.Time,
) (*service.OAuthRefreshToken, error) {
	var oldToken *service.OAuthRefreshToken
	err := withTx(ctx, r.client, func(tx *dbent.Tx) error {
		row, qerr := tx.OAuthRefreshToken.Query().
			Where(oauthrefreshtoken.TokenHashEQ(oldHash)).
			ForUpdate().
			Only(ctx)
		if qerr != nil {
			if dbent.IsNotFound(qerr) {
				return service.ErrOAuthRefreshTokenNotFound
			}
			return fmt.Errorf("lock oauth refresh token: %w", qerr)
		}
		if row.RevokedAt != nil {
			return service.ErrOAuthRefreshTokenRevoked
		}
		if !row.ExpiresAt.After(now) {
			return service.ErrOAuthRefreshTokenExpired
		}
		if _, uerr := row.Update().
			SetRevokedAt(now).
			SetRotatedToHash(newHash).
			Save(ctx); uerr != nil {
			return fmt.Errorf("revoke old refresh token: %w", uerr)
		}
		oldToken = entOAuthRefreshTokenToService(row)
		return nil
	})
	return oldToken, err
}

func (r *oauthProviderRepository) RevokeRefreshTokensByAPIKeyID(ctx context.Context, apiKeyID int64, now time.Time) error {
	_, err := r.client.OAuthRefreshToken.Update().
		Where(
			oauthrefreshtoken.APIKeyIDEQ(apiKeyID),
			oauthrefreshtoken.RevokedAtIsNil(),
		).
		SetRevokedAt(now).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("revoke refresh tokens for api key %d: %w", apiKeyID, err)
	}
	return nil
}

func (r *oauthProviderRepository) ListActiveByUserAndClient(ctx context.Context, userID int64, clientID string, now time.Time) ([]*service.OAuthRefreshToken, error) {
	rows, err := r.client.OAuthRefreshToken.Query().
		Where(
			oauthrefreshtoken.UserIDEQ(userID),
			oauthrefreshtoken.ClientIDEQ(clientID),
			oauthrefreshtoken.RevokedAtIsNil(),
			oauthrefreshtoken.ExpiresAtGT(now),
		).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active refresh tokens: %w", err)
	}
	out := make([]*service.OAuthRefreshToken, 0, len(rows))
	for _, row := range rows {
		out = append(out, entOAuthRefreshTokenToService(row))
	}
	return out, nil
}

func (r *oauthProviderRepository) ListActiveByUser(ctx context.Context, userID int64, now time.Time) ([]*service.OAuthRefreshToken, error) {
	rows, err := r.client.OAuthRefreshToken.Query().
		Where(
			oauthrefreshtoken.UserIDEQ(userID),
			oauthrefreshtoken.RevokedAtIsNil(),
			oauthrefreshtoken.ExpiresAtGT(now),
		).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active refresh tokens by user: %w", err)
	}
	out := make([]*service.OAuthRefreshToken, 0, len(rows))
	for _, row := range rows {
		out = append(out, entOAuthRefreshTokenToService(row))
	}
	return out, nil
}

// GetRefreshTokenByHashForUpdate returns the row under FOR UPDATE lock,
// INCLUDING revoked rows. The service layer uses revoked+rotated row data
// to detect refresh-token reuse and revoke the entire token family.
func (r *oauthProviderRepository) GetRefreshTokenByHashForUpdate(ctx context.Context, tokenHash string, _ time.Time) (*service.OAuthRefreshToken, error) {
	var got *service.OAuthRefreshToken
	err := withTx(ctx, r.client, func(tx *dbent.Tx) error {
		row, qerr := tx.OAuthRefreshToken.Query().
			Where(oauthrefreshtoken.TokenHashEQ(tokenHash)).
			ForUpdate().
			Only(ctx)
		if qerr != nil {
			if dbent.IsNotFound(qerr) {
				return service.ErrOAuthRefreshTokenNotFound
			}
			return fmt.Errorf("lock oauth refresh token for read: %w", qerr)
		}
		got = entOAuthRefreshTokenToService(row)
		return nil
	})
	return got, err
}

// revokeRefreshTokensInline is the typed shared body for grant/family/
// user-client revocation paths: SELECT api_key_ids of unrevoked rows
// matching the predicates, UPDATE them to revoked, return the deduped
// api_key_ids freshly revoked. Callers can resolve plaintext keys for
// auth-cache invalidation from the returned IDs.
func (r *oauthProviderRepository) revokeRefreshTokensInline(
	ctx context.Context,
	now time.Time,
	preds ...predicate.OAuthRefreshToken,
) ([]int64, error) {
	var freshlyRevoked []int64
	err := withTx(ctx, r.client, func(tx *dbent.Tx) error {
		q := tx.OAuthRefreshToken.Query()
		for _, p := range preds {
			q = q.Where(p)
		}
		q = q.Where(oauthrefreshtoken.RevokedAtIsNil())
		rows, qerr := q.Select(oauthrefreshtoken.FieldID, oauthrefreshtoken.FieldAPIKeyID).All(ctx)
		if qerr != nil {
			return fmt.Errorf("list refresh tokens to revoke: %w", qerr)
		}
		if len(rows) == 0 {
			return nil
		}
		ids := make([]int64, 0, len(rows))
		seen := make(map[int64]struct{}, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
			if _, ok := seen[row.APIKeyID]; !ok {
				seen[row.APIKeyID] = struct{}{}
				freshlyRevoked = append(freshlyRevoked, row.APIKeyID)
			}
		}
		if _, uerr := tx.OAuthRefreshToken.Update().
			Where(oauthrefreshtoken.IDIn(ids...)).
			SetRevokedAt(now).
			Save(ctx); uerr != nil {
			return fmt.Errorf("revoke refresh tokens: %w", uerr)
		}
		return nil
	})
	return freshlyRevoked, err
}

// RevokeRefreshTokensByGrantID marks all unrevoked rows with grant_id=g
// revoked and returns the deduped api_key_ids freshly revoked.
func (r *oauthProviderRepository) RevokeRefreshTokensByGrantID(ctx context.Context, grantID string, now time.Time) ([]int64, error) {
	return r.revokeRefreshTokensInline(ctx, now, oauthrefreshtoken.GrantIDEQ(grantID))
}

// RevokeRefreshTokensByTokenFamilyID marks all unrevoked rows with
// token_family_id=f revoked and returns the deduped api_key_ids freshly
// revoked. Used by refresh-token reuse detection.
func (r *oauthProviderRepository) RevokeRefreshTokensByTokenFamilyID(ctx context.Context, tokenFamilyID string, now time.Time) ([]int64, error) {
	return r.revokeRefreshTokensInline(ctx, now, oauthrefreshtoken.TokenFamilyIDEQ(tokenFamilyID))
}

// RevokeRefreshTokensByUserAndClient marks all unrevoked rows for the
// (user_id, client_id) pair revoked and returns the deduped api_key_ids
// freshly revoked. Used by "Revoke all devices for this app".
func (r *oauthProviderRepository) RevokeRefreshTokensByUserAndClient(ctx context.Context, userID int64, clientID string, now time.Time) ([]int64, error) {
	return r.revokeRefreshTokensInline(
		ctx, now,
		oauthrefreshtoken.UserIDEQ(userID),
		oauthrefreshtoken.ClientIDEQ(clientID),
	)
}

// MarkReuseDetected stamps reuse_detected_at on the specific row identified by
// tokenHash. Best-effort: the caller logs failures but does not abort the
// reuse-containment cascade on error.
func (r *oauthProviderRepository) MarkReuseDetected(ctx context.Context, tokenHash string, now time.Time) error {
	_, err := r.client.OAuthRefreshToken.Update().
		Where(oauthrefreshtoken.TokenHashEQ(tokenHash)).
		SetReuseDetectedAt(now).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("mark reuse_detected_at: %w", err)
	}
	return nil
}

// ── OAuthAccessTokenRepository ──────────────────────────────────────────────

func (r *oauthProviderRepository) CreateAccessToken(ctx context.Context, token *service.OAuthAccessToken) error {
	q := r.client.OAuthAccessToken.Create().
		SetAPIKeyID(token.APIKeyID).
		SetGrantID(token.GrantID).
		SetTokenFamilyID(token.TokenFamilyID).
		SetClientID(token.ClientID).
		SetUserID(token.UserID).
		SetScopes(token.Scopes).
		SetGroupID(token.GroupID).
		SetAllowedGroupsSnapshot(token.AllowedGroupIDs).
		SetAppType(token.AppType).
		SetIssuedAt(token.IssuedAt).
		SetExpiresAt(token.ExpiresAt)
	if token.DeviceID != nil {
		q = q.SetDeviceID(*token.DeviceID)
	}
	if token.DeviceName != nil {
		q = q.SetDeviceName(*token.DeviceName)
	}
	if _, err := q.Save(ctx); err != nil {
		return fmt.Errorf("create oauth access token: %w", err)
	}
	return nil
}

func (r *oauthProviderRepository) GetActiveAccessTokenByAPIKeyID(ctx context.Context, apiKeyID int64, now time.Time) (*service.OAuthAccessToken, error) {
	row, err := r.client.OAuthAccessToken.Query().
		Where(
			oauthaccesstoken.APIKeyIDEQ(apiKeyID),
			oauthaccesstoken.RevokedAtIsNil(),
			oauthaccesstoken.ExpiresAtGT(now),
		).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrOAuthAccessTokenNotFound
		}
		return nil, fmt.Errorf("query oauth access token: %w", err)
	}
	return entOAuthAccessTokenToService(row), nil
}

func (r *oauthProviderRepository) RevokeAccessTokenByAPIKeyID(ctx context.Context, apiKeyID int64, now time.Time) error {
	_, err := r.client.OAuthAccessToken.Update().
		Where(
			oauthaccesstoken.APIKeyIDEQ(apiKeyID),
			oauthaccesstoken.RevokedAtIsNil(),
		).
		SetRevokedAt(now).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("revoke oauth access token for api key %d: %w", apiKeyID, err)
	}
	return nil
}

// revokeAccessTokensWhere shares the body for grant/family revocation:
// SELECT api_key_ids of unrevoked rows matching predicates, UPDATE to
// revoked, return the deduped api_key_ids freshly revoked.
func (r *oauthProviderRepository) revokeAccessTokensInline(
	ctx context.Context,
	now time.Time,
	matcher func(q *dbent.OAuthAccessTokenQuery) *dbent.OAuthAccessTokenQuery,
) ([]int64, error) {
	var freshlyRevoked []int64
	err := withTx(ctx, r.client, func(tx *dbent.Tx) error {
		q := matcher(tx.OAuthAccessToken.Query()).Where(oauthaccesstoken.RevokedAtIsNil())
		rows, qerr := q.Select(oauthaccesstoken.FieldID, oauthaccesstoken.FieldAPIKeyID).All(ctx)
		if qerr != nil {
			return fmt.Errorf("list oauth access tokens to revoke: %w", qerr)
		}
		if len(rows) == 0 {
			return nil
		}
		ids := make([]int64, 0, len(rows))
		seen := make(map[int64]struct{}, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
			if _, ok := seen[row.APIKeyID]; !ok {
				seen[row.APIKeyID] = struct{}{}
				freshlyRevoked = append(freshlyRevoked, row.APIKeyID)
			}
		}
		if _, uerr := tx.OAuthAccessToken.Update().
			Where(oauthaccesstoken.IDIn(ids...)).
			SetRevokedAt(now).
			Save(ctx); uerr != nil {
			return fmt.Errorf("revoke oauth access tokens: %w", uerr)
		}
		return nil
	})
	return freshlyRevoked, err
}

func (r *oauthProviderRepository) RevokeAccessTokensByGrantID(ctx context.Context, grantID string, now time.Time) ([]int64, error) {
	return r.revokeAccessTokensInline(ctx, now, func(q *dbent.OAuthAccessTokenQuery) *dbent.OAuthAccessTokenQuery {
		return q.Where(oauthaccesstoken.GrantIDEQ(grantID))
	})
}

func (r *oauthProviderRepository) RevokeAccessTokensByTokenFamilyID(ctx context.Context, tokenFamilyID string, now time.Time) ([]int64, error) {
	return r.revokeAccessTokensInline(ctx, now, func(q *dbent.OAuthAccessTokenQuery) *dbent.OAuthAccessTokenQuery {
		return q.Where(oauthaccesstoken.TokenFamilyIDEQ(tokenFamilyID))
	})
}

// TouchAccessToken updates last_used_at/last_used_ip/last_used_user_agent.
// The service layer enforces the 60s throttle; the repo just performs the
// UPDATE. Empty ip / userAgent are skipped (the access token's last value
// is preserved).
func (r *oauthProviderRepository) TouchAccessToken(ctx context.Context, apiKeyID int64, ip, userAgent string, now time.Time) error {
	q := r.client.OAuthAccessToken.Update().
		Where(
			oauthaccesstoken.APIKeyIDEQ(apiKeyID),
			oauthaccesstoken.RevokedAtIsNil(),
		).
		SetLastUsedAt(now)
	if ip != "" {
		q = q.SetLastUsedIP(ip)
	}
	if userAgent != "" {
		q = q.SetLastUsedUserAgent(userAgent)
	}
	if _, err := q.Save(ctx); err != nil {
		return fmt.Errorf("touch oauth access token for api key %d: %w", apiKeyID, err)
	}
	return nil
}

// ListActiveGrantsByUser returns one OAuthAuthorizedGrant per active
// (user_id, grant_id). Display fields client_name, group_name, icon_url,
// app_type are filled by joining oauth_clients via in-memory lookup; the
// projection's group_name field is left empty when the group lookup is not
// available — Workstream B will inject the group repo if needed.
func (r *oauthProviderRepository) ListActiveGrantsByUser(ctx context.Context, userID int64, now time.Time) ([]*service.OAuthAuthorizedGrant, error) {
	rows, err := r.client.OAuthAccessToken.Query().
		Where(
			oauthaccesstoken.UserIDEQ(userID),
			oauthaccesstoken.RevokedAtIsNil(),
			oauthaccesstoken.ExpiresAtGT(now),
		).
		Order(dbent.Asc(oauthaccesstoken.FieldGrantID), dbent.Desc(oauthaccesstoken.FieldIssuedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active access tokens for user: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}

	// Pre-fetch client display fields for all referenced client_ids.
	clientIDs := make([]string, 0, len(rows))
	seenClient := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if _, ok := seenClient[row.ClientID]; ok {
			continue
		}
		seenClient[row.ClientID] = struct{}{}
		clientIDs = append(clientIDs, row.ClientID)
	}
	clients, err := r.client.OAuthClient.Query().
		Where(oauthclient.ClientIDIn(clientIDs...)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("query oauth clients for grants: %w", err)
	}
	clientLookup := make(map[string]*dbent.OAuthClient, len(clients))
	for _, c := range clients {
		clientLookup[c.ClientID] = c
	}

	// Count active refresh tokens per (user_id, grant_id) so we can fill
	// active_refresh_token_count without a separate query per row.
	refreshRows, err := r.client.OAuthRefreshToken.Query().
		Where(
			oauthrefreshtoken.UserIDEQ(userID),
			oauthrefreshtoken.RevokedAtIsNil(),
			oauthrefreshtoken.ExpiresAtGT(now),
		).
		Select(oauthrefreshtoken.FieldGrantID).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active refresh tokens for grants: %w", err)
	}
	refreshCount := make(map[string]int, len(refreshRows))
	for _, row := range refreshRows {
		if row.GrantID == nil {
			continue
		}
		refreshCount[*row.GrantID]++
	}

	// Fold rows into one entry per grant_id, keeping the freshest
	// last_used / issued metadata.
	grouped := make(map[string]*service.OAuthAuthorizedGrant, len(rows))
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		entry, ok := grouped[row.GrantID]
		if !ok {
			entry = &service.OAuthAuthorizedGrant{
				GrantID:                row.GrantID,
				ClientID:               row.ClientID,
				AppType:                row.AppType,
				GroupID:                row.GroupID,
				Scopes:                 row.Scopes,
				FirstAuthorizedAt:      row.IssuedAt,
				ActiveAccessTokenCount: 0,
				Status:                 "active",
			}
			if c, ok := clientLookup[row.ClientID]; ok {
				entry.ClientName = c.Name
				entry.ClientDisabled = c.Disabled
				entry.IconURL = c.IconURL
			}
			entry.ActiveRefreshTokenCount = refreshCount[row.GrantID]
			grouped[row.GrantID] = entry
			order = append(order, row.GrantID)
		}
		entry.ActiveAccessTokenCount++
		if row.IssuedAt.Before(entry.FirstAuthorizedAt) {
			entry.FirstAuthorizedAt = row.IssuedAt
		}
		if row.LastUsedAt != nil && (entry.LastUsedAt == nil || row.LastUsedAt.After(*entry.LastUsedAt)) {
			entry.LastUsedAt = row.LastUsedAt
			entry.LastUsedIP = row.LastUsedIP
		}
		if row.DeviceID != nil && entry.DeviceID == nil {
			entry.DeviceID = row.DeviceID
		}
		if row.DeviceName != nil && entry.DeviceName == nil {
			entry.DeviceName = row.DeviceName
		}
	}

	out := make([]*service.OAuthAuthorizedGrant, 0, len(order))
	for _, gid := range order {
		out = append(out, grouped[gid])
	}
	return out, nil
}

// ── OAuthDeviceCodeRepository ───────────────────────────────────────────────

func (r *oauthProviderRepository) CreateDeviceCode(ctx context.Context, code *service.OAuthDeviceCode) error {
	q := r.client.OAuthDeviceCode.Create().
		SetDeviceCodeHash(code.DeviceCodeHash).
		SetUserCodeHash(code.UserCodeHash).
		SetClientID(code.ClientID).
		SetScopes(code.Scopes).
		SetIntervalSeconds(code.IntervalSeconds).
		SetStatus(code.Status).
		SetExpiresAt(code.ExpiresAt)
	if code.GrantID != nil {
		q = q.SetGrantID(*code.GrantID)
	}
	if code.GroupID != nil {
		q = q.SetGroupID(*code.GroupID)
	}
	if code.DeviceID != nil {
		q = q.SetDeviceID(*code.DeviceID)
	}
	if code.DeviceName != nil {
		q = q.SetDeviceName(*code.DeviceName)
	}
	if code.CreatedIP != nil {
		q = q.SetCreatedIP(*code.CreatedIP)
	}
	if code.CreatedUserAgent != nil {
		q = q.SetCreatedUserAgent(*code.CreatedUserAgent)
	}
	if _, err := q.Save(ctx); err != nil {
		return fmt.Errorf("create oauth device code: %w", err)
	}
	return nil
}

// GetDeviceCodeByUserCodeHashForApproval looks up the row by user_code_hash
// under FOR UPDATE lock, restricted to pending+unexpired rows. Approve and
// deny both call this; failed lookups return ErrOAuthDeviceCodeNotFound.
func (r *oauthProviderRepository) GetDeviceCodeByUserCodeHashForApproval(ctx context.Context, userCodeHash string, now time.Time) (*service.OAuthDeviceCode, error) {
	var got *service.OAuthDeviceCode
	err := withTx(ctx, r.client, func(tx *dbent.Tx) error {
		row, qerr := tx.OAuthDeviceCode.Query().
			Where(
				oauthdevicecode.UserCodeHashEQ(userCodeHash),
				oauthdevicecode.StatusEQ("pending"),
				oauthdevicecode.ExpiresAtGT(now),
			).
			ForUpdate().
			Only(ctx)
		if qerr != nil {
			if dbent.IsNotFound(qerr) {
				return service.ErrOAuthDeviceCodeNotFound
			}
			return fmt.Errorf("lock oauth device code: %w", qerr)
		}
		got = entOAuthDeviceCodeToService(row)
		return nil
	})
	return got, err
}

// PollDeviceCodeForUpdate returns the row by device_code_hash under FOR
// UPDATE in any status. The service layer maps row state into RFC 8628
// poll responses (authorization_pending / slow_down / access_denied /
// expired_token / invalid_grant).
func (r *oauthProviderRepository) PollDeviceCodeForUpdate(ctx context.Context, deviceCodeHash string, _ time.Time) (*service.OAuthDeviceCode, error) {
	var got *service.OAuthDeviceCode
	err := withTx(ctx, r.client, func(tx *dbent.Tx) error {
		row, qerr := tx.OAuthDeviceCode.Query().
			Where(oauthdevicecode.DeviceCodeHashEQ(deviceCodeHash)).
			ForUpdate().
			Only(ctx)
		if qerr != nil {
			if dbent.IsNotFound(qerr) {
				return service.ErrOAuthDeviceCodeNotFound
			}
			return fmt.Errorf("lock oauth device code by device hash: %w", qerr)
		}
		// Service layer increments poll_count / slow_down_count / last_poll_at
		// after deciding the response; the repo doesn't side-effect here.
		got = entOAuthDeviceCodeToService(row)
		return nil
	})
	return got, err
}

func (r *oauthProviderRepository) ApproveDeviceCode(ctx context.Context, userCodeHash string, userID int64, groupID int64, now time.Time) error {
	return withTx(ctx, r.client, func(tx *dbent.Tx) error {
		row, qerr := tx.OAuthDeviceCode.Query().
			Where(
				oauthdevicecode.UserCodeHashEQ(userCodeHash),
				oauthdevicecode.StatusEQ("pending"),
				oauthdevicecode.ExpiresAtGT(now),
			).
			ForUpdate().
			Only(ctx)
		if qerr != nil {
			if dbent.IsNotFound(qerr) {
				return service.ErrOAuthDeviceCodeNotFound
			}
			return fmt.Errorf("lock oauth device code for approve: %w", qerr)
		}
		if _, uerr := row.Update().
			SetStatus("approved").
			SetApprovedByUserID(userID).
			SetApprovedAt(now).
			SetGroupID(groupID).
			Save(ctx); uerr != nil {
			return fmt.Errorf("approve oauth device code: %w", uerr)
		}
		return nil
	})
}

func (r *oauthProviderRepository) DenyDeviceCode(ctx context.Context, userCodeHash string, now time.Time) error {
	return withTx(ctx, r.client, func(tx *dbent.Tx) error {
		row, qerr := tx.OAuthDeviceCode.Query().
			Where(
				oauthdevicecode.UserCodeHashEQ(userCodeHash),
				oauthdevicecode.StatusEQ("pending"),
				oauthdevicecode.ExpiresAtGT(now),
			).
			ForUpdate().
			Only(ctx)
		if qerr != nil {
			if dbent.IsNotFound(qerr) {
				return service.ErrOAuthDeviceCodeNotFound
			}
			return fmt.Errorf("lock oauth device code for deny: %w", qerr)
		}
		if _, uerr := row.Update().
			SetStatus("denied").
			SetDeniedAt(now).
			Save(ctx); uerr != nil {
			return fmt.Errorf("deny oauth device code: %w", uerr)
		}
		return nil
	})
}

func (r *oauthProviderRepository) MarkDeviceCodeConsumed(ctx context.Context, deviceCodeHash string, now time.Time) error {
	return withTx(ctx, r.client, func(tx *dbent.Tx) error {
		row, qerr := tx.OAuthDeviceCode.Query().
			Where(
				oauthdevicecode.DeviceCodeHashEQ(deviceCodeHash),
				oauthdevicecode.StatusEQ("approved"),
			).
			ForUpdate().
			Only(ctx)
		if qerr != nil {
			if dbent.IsNotFound(qerr) {
				return service.ErrOAuthDeviceCodeNotFound
			}
			return fmt.Errorf("lock oauth device code for consume: %w", qerr)
		}
		if _, uerr := row.Update().
			SetStatus("consumed").
			SetConsumedAt(now).
			Save(ctx); uerr != nil {
			return fmt.Errorf("consume oauth device code: %w", uerr)
		}
		return nil
	})
}

// ConsumeApprovedDeviceCode atomically flips status approved → consumed and
// returns the row that was just consumed. FIX H6: gate the device-code
// minting path with this call so a concurrent double-poll cannot win twice.
func (r *oauthProviderRepository) ConsumeApprovedDeviceCode(
	ctx context.Context,
	deviceCodeHash string,
	now time.Time,
) (*service.OAuthDeviceCode, error) {
	var consumed *service.OAuthDeviceCode
	err := withTx(ctx, r.client, func(tx *dbent.Tx) error {
		row, qerr := tx.OAuthDeviceCode.Query().
			Where(
				oauthdevicecode.DeviceCodeHashEQ(deviceCodeHash),
				oauthdevicecode.StatusEQ("approved"),
			).
			ForUpdate().
			Only(ctx)
		if qerr != nil {
			if dbent.IsNotFound(qerr) {
				return service.ErrOAuthDeviceCodeNotFound
			}
			return fmt.Errorf("lock oauth device code for atomic consume: %w", qerr)
		}
		updated, uerr := row.Update().
			SetStatus("consumed").
			SetConsumedAt(now).
			Save(ctx)
		if uerr != nil {
			return fmt.Errorf("atomic consume oauth device code: %w", uerr)
		}
		consumed = entOAuthDeviceCodeToService(updated)
		return nil
	})
	return consumed, err
}

func (r *oauthProviderRepository) IncrementDeviceCodeFailedAttempts(ctx context.Context, userCodeHash string, now time.Time) (int, error) {
	var newCount int
	err := withTx(ctx, r.client, func(tx *dbent.Tx) error {
		row, qerr := tx.OAuthDeviceCode.Query().
			Where(
				oauthdevicecode.UserCodeHashEQ(userCodeHash),
				oauthdevicecode.ExpiresAtGT(now),
			).
			ForUpdate().
			Only(ctx)
		if qerr != nil {
			if dbent.IsNotFound(qerr) {
				return service.ErrOAuthDeviceCodeNotFound
			}
			return fmt.Errorf("lock oauth device code for failed-attempt bump: %w", qerr)
		}
		newCount = row.FailedUserCodeAttempts + 1
		if _, uerr := row.Update().
			SetFailedUserCodeAttempts(newCount).
			Save(ctx); uerr != nil {
			return fmt.Errorf("increment device code failed attempts: %w", uerr)
		}
		return nil
	})
	return newCount, err
}

// TouchDevicePoll persists last_poll_at + poll_count + interval_seconds +
// slow_down_count after a polling decision. The service layer chooses the
// values; this method is a single-row UPDATE under the same row lock the
// poll path already takes via PollDeviceCodeForUpdate. Idempotent.
func (r *oauthProviderRepository) TouchDevicePoll(
	ctx context.Context,
	deviceCodeHash string,
	lastPollAt time.Time,
	pollCount int,
	intervalSeconds int,
	slowDownCount int,
) error {
	return withTx(ctx, r.client, func(tx *dbent.Tx) error {
		row, qerr := tx.OAuthDeviceCode.Query().
			Where(oauthdevicecode.DeviceCodeHashEQ(deviceCodeHash)).
			ForUpdate().
			Only(ctx)
		if qerr != nil {
			if dbent.IsNotFound(qerr) {
				return service.ErrOAuthDeviceCodeNotFound
			}
			return fmt.Errorf("lock oauth device code for touch poll: %w", qerr)
		}
		if _, uerr := row.Update().
			SetLastPollAt(lastPollAt).
			SetPollCount(pollCount).
			SetIntervalSeconds(intervalSeconds).
			SetSlowDownCount(slowDownCount).
			Save(ctx); uerr != nil {
			return fmt.Errorf("touch device code poll: %w", uerr)
		}
		return nil
	})
}

// ── OAuthAuthorizeTransactionRepository ─────────────────────────────────────

func (r *oauthProviderRepository) CreateAuthorizeTransaction(ctx context.Context, tx *service.OAuthAuthorizeTransaction) error {
	q := r.client.OAuthAuthorizeTransaction.Create().
		SetTransactionID(tx.TransactionID).
		SetCsrfHash(tx.CSRFHash).
		SetUserID(tx.UserID).
		SetClientID(tx.ClientID).
		SetRedirectURI(tx.RedirectURI).
		SetResponseType(tx.ResponseType).
		SetScopes(tx.Scopes).
		SetAllowedGroupsSnapshot(tx.AllowedGroupsSnapshot).
		SetState(tx.State).
		SetCodeChallenge(tx.CodeChallenge).
		SetCodeChallengeMethod(tx.CodeChallengeMethod).
		SetExpiresAt(tx.ExpiresAt)
	if tx.RequestedGroupID != nil {
		q = q.SetRequestedGroupID(*tx.RequestedGroupID)
	}
	if tx.DeviceID != nil {
		q = q.SetDeviceID(*tx.DeviceID)
	}
	if tx.DeviceName != nil {
		q = q.SetDeviceName(*tx.DeviceName)
	}
	if tx.CreatedIP != nil {
		q = q.SetCreatedIP(*tx.CreatedIP)
	}
	if tx.CreatedUserAgent != nil {
		q = q.SetCreatedUserAgent(*tx.CreatedUserAgent)
	}
	if tx.Nonce != "" {
		q = q.SetNonce(tx.Nonce)
	}
	if _, err := q.Save(ctx); err != nil {
		return fmt.Errorf("create oauth authorize transaction: %w", err)
	}
	return nil
}

// GetAuthorizeTransactionForApproval loads the row by transaction_id for the
// consent-page approval pre-checks (CSRF + subject). The lookup runs inside a
// short-lived transaction but the row lock is released on commit before this
// method returns — so this is effectively a non-locking advisory load. The
// atomic consume path (ConsumeAuthorizeTransactionAndIssueCode) re-checks
// every invariant under FOR UPDATE inside its own tx, so callers can rely on
// the consume call to gate the actual mint.
//
// Renamed in FIX A4 from the misleading GetAuthorizeTransactionForUpdate to
// reflect that the held lock is not durable across the call boundary.
func (r *oauthProviderRepository) GetAuthorizeTransactionForApproval(ctx context.Context, transactionID string, now time.Time) (*service.OAuthAuthorizeTransaction, error) {
	var got *service.OAuthAuthorizeTransaction
	err := withTx(ctx, r.client, func(tx *dbent.Tx) error {
		row, qerr := tx.OAuthAuthorizeTransaction.Query().
			Where(
				oauthauthorizetransaction.TransactionIDEQ(transactionID),
				oauthauthorizetransaction.ConsumedAtIsNil(),
				oauthauthorizetransaction.ExpiresAtGT(now),
			).
			Only(ctx)
		if qerr != nil {
			if dbent.IsNotFound(qerr) {
				return service.ErrOAuthAuthorizeTransactionNotFound
			}
			return fmt.Errorf("load oauth authorize transaction: %w", qerr)
		}
		got = entOAuthAuthorizeTransactionToService(row)
		return nil
	})
	return got, err
}

func (r *oauthProviderRepository) MarkAuthorizeTransactionConsumed(ctx context.Context, transactionID string, now time.Time) error {
	_, err := r.client.OAuthAuthorizeTransaction.Update().
		Where(
			oauthauthorizetransaction.TransactionIDEQ(transactionID),
			oauthauthorizetransaction.ConsumedAtIsNil(),
		).
		SetConsumedAt(now).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("mark oauth authorize transaction consumed: %w", err)
	}
	return nil
}

// ConsumeAuthorizeTransactionAndIssueCode is the FIX C2 atomic consume+issue
// path. SELECT ... FOR UPDATE the transaction row, validate it is unconsumed
// AND owned by expectedUserID (FIX A4 TOCTOU close), stamp consumed_at, then
// insert the authorization code — all inside one repository transaction. On
// any failure the transaction stays unconsumed so the user can retry.
//
// FIX A4: expectedUserID is the JWT subject from the approve POST; we re-verify
// it inside the row lock here because LoadAuthorizeTransactionForApproval's
// pre-check is advisory (lock released before this call). Subject mismatch
// returns ErrOAuthSubjectMismatch and rolls back without consuming.
func (r *oauthProviderRepository) ConsumeAuthorizeTransactionAndIssueCode(
	ctx context.Context,
	transactionID string,
	expectedUserID int64,
	now time.Time,
	code *service.OAuthCode,
) error {
	if code == nil {
		return fmt.Errorf("consume+issue: code argument is nil")
	}
	if expectedUserID <= 0 {
		return service.ErrOAuthSubjectMismatch
	}
	return withTx(ctx, r.client, func(tx *dbent.Tx) error {
		row, qerr := tx.OAuthAuthorizeTransaction.Query().
			Where(
				oauthauthorizetransaction.TransactionIDEQ(transactionID),
				oauthauthorizetransaction.ConsumedAtIsNil(),
				oauthauthorizetransaction.ExpiresAtGT(now),
			).
			ForUpdate().
			Only(ctx)
		if qerr != nil {
			if dbent.IsNotFound(qerr) {
				return service.ErrOAuthAuthorizeTransactionNotFound
			}
			return fmt.Errorf("lock authorize transaction for consume+issue: %w", qerr)
		}
		// FIX A4: subject re-check inside row lock. Defense in depth — the
		// service-layer pre-check is advisory; this is the authoritative one.
		if row.UserID != expectedUserID {
			return service.ErrOAuthSubjectMismatch
		}
		if _, uerr := row.Update().SetConsumedAt(now).Save(ctx); uerr != nil {
			return fmt.Errorf("mark authorize transaction consumed: %w", uerr)
		}
		cq := tx.OAuthCode.Create().
			SetCodeHash(code.CodeHash).
			SetClientID(code.ClientID).
			SetUserID(code.UserID).
			SetRedirectURI(code.RedirectURI).
			SetScopes(code.Scopes).
			SetCodeChallenge(code.CodeChallenge).
			SetCodeChallengeMethod(code.CodeChallengeMethod).
			SetExpiresAt(code.ExpiresAt).
			SetAllowedGroupsSnapshot(code.AllowedGroupsSnapshot)
		if code.GroupID != nil {
			cq = cq.SetGroupID(*code.GroupID)
		}
		if code.GrantID != nil {
			cq = cq.SetGrantID(*code.GrantID)
		}
		if code.DeviceID != nil {
			cq = cq.SetDeviceID(*code.DeviceID)
		}
		if code.DeviceName != nil {
			cq = cq.SetDeviceName(*code.DeviceName)
		}
		if code.Nonce != "" {
			cq = cq.SetNonce(code.Nonce)
		}
		if _, cerr := cq.Save(ctx); cerr != nil {
			return fmt.Errorf("persist authorization code: %w", cerr)
		}
		return nil
	})
}

func (r *oauthProviderRepository) DeleteExpiredAuthorizeTransactions(ctx context.Context, before time.Time) (int, error) {
	n, err := r.client.OAuthAuthorizeTransaction.Delete().
		Where(oauthauthorizetransaction.ExpiresAtLT(before)).
		Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("delete expired oauth authorize transactions: %w", err)
	}
	return n, nil
}

// ── helpers ─────────────────────────────────────────────────────────────────

// withTx wraps a closure in a transaction, committing on nil error and rolling back otherwise.
func withTx(ctx context.Context, client *dbent.Client, fn func(tx *dbent.Tx) error) error {
	tx, err := client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func entOAuthClientToService(row *dbent.OAuthClient) *service.OAuthClient {
	out := &service.OAuthClient{
		ClientID:                         row.ClientID,
		Name:                             row.Name,
		RedirectURIs:                     row.RedirectUris,
		AllowedScopes:                    row.AllowedScopes,
		PKCERequired:                     row.PkceRequired,
		AccessTokenTTLSeconds:            row.AccessTokenTTLSeconds,
		RefreshTokenTTLSeconds:           row.RefreshTokenTTLSeconds,
		Disabled:                         row.Disabled,
		ClientType:                       row.ClientType,
		AppType:                          row.AppType,
		TrustedFirstParty:                row.TrustedFirstParty,
		DefaultScopes:                    row.DefaultScopes,
		AllowedGroupIDs:                  row.AllowedGroupIds,
		AllowedOrigins:                   row.AllowedOrigins,
		LogoutRedirectURIs:               row.LogoutRedirectUris,
		DeviceFlowEnabled:                row.DeviceFlowEnabled,
		AllowRefreshWithoutOfflineAccess: row.AllowRefreshWithoutOfflineAccess,
		IconURL:                          row.IconURL,
		HomepageURL:                      row.HomepageURL,
		PrivacyURL:                       row.PrivacyURL,
		TermsURL:                         row.TermsURL,
	}
	if row.ClientSecretHash != nil {
		out.ClientSecretHash = *row.ClientSecretHash
	}
	if row.DefaultGroupID != nil {
		out.DefaultGroupID = row.DefaultGroupID
	}
	return out
}

func entOAuthCodeToService(row *dbent.OAuthCode) *service.OAuthCode {
	out := &service.OAuthCode{
		CodeHash:              row.CodeHash,
		ClientID:              row.ClientID,
		UserID:                row.UserID,
		RedirectURI:           row.RedirectURI,
		Scopes:                row.Scopes,
		CodeChallenge:         row.CodeChallenge,
		CodeChallengeMethod:   row.CodeChallengeMethod,
		ExpiresAt:             row.ExpiresAt,
		UsedAt:                row.UsedAt,
		AllowedGroupsSnapshot: row.AllowedGroupsSnapshot,
		GroupID:               row.GroupID,
		GrantID:               row.GrantID,
		DeviceID:              row.DeviceID,
		DeviceName:            row.DeviceName,
		Nonce:                 row.Nonce,
		CreatedAt:             row.CreatedAt,
	}
	return out
}

func entOAuthRefreshTokenToService(row *dbent.OAuthRefreshToken) *service.OAuthRefreshToken {
	return &service.OAuthRefreshToken{
		TokenHash:             row.TokenHash,
		ClientID:              row.ClientID,
		UserID:                row.UserID,
		APIKeyID:              row.APIKeyID,
		Scopes:                row.Scopes,
		ExpiresAt:             row.ExpiresAt,
		RevokedAt:             row.RevokedAt,
		RotatedToHash:         row.RotatedToHash,
		GrantID:               row.GrantID,
		TokenFamilyID:         row.TokenFamilyID,
		GroupID:               row.GroupID,
		AllowedGroupsSnapshot: row.AllowedGroupsSnapshot,
		DeviceID:              row.DeviceID,
		DeviceName:            row.DeviceName,
		LastUsedAt:            row.LastUsedAt,
		ReuseDetectedAt:       row.ReuseDetectedAt,
	}
}

func entOAuthAccessTokenToService(row *dbent.OAuthAccessToken) *service.OAuthAccessToken {
	return &service.OAuthAccessToken{
		ID:              row.ID,
		APIKeyID:        row.APIKeyID,
		GrantID:         row.GrantID,
		TokenFamilyID:   row.TokenFamilyID,
		ClientID:        row.ClientID,
		UserID:          row.UserID,
		Scopes:          row.Scopes,
		GroupID:         row.GroupID,
		AllowedGroupIDs: row.AllowedGroupsSnapshot,
		AppType:         row.AppType,
		DeviceID:        row.DeviceID,
		DeviceName:      row.DeviceName,
		IssuedAt:        row.IssuedAt,
		ExpiresAt:       row.ExpiresAt,
		LastUsedAt:      row.LastUsedAt,
		LastUsedIP:      row.LastUsedIP,
		LastUsedUA:      row.LastUsedUserAgent,
		RevokedAt:       row.RevokedAt,
	}
}

func entOAuthDeviceCodeToService(row *dbent.OAuthDeviceCode) *service.OAuthDeviceCode {
	return &service.OAuthDeviceCode{
		ID:                     row.ID,
		DeviceCodeHash:         row.DeviceCodeHash,
		UserCodeHash:           row.UserCodeHash,
		ClientID:               row.ClientID,
		Scopes:                 row.Scopes,
		GrantID:                row.GrantID,
		GroupID:                row.GroupID,
		DeviceID:               row.DeviceID,
		DeviceName:             row.DeviceName,
		Status:                 row.Status,
		IntervalSeconds:        row.IntervalSeconds,
		PollCount:              row.PollCount,
		LastPollAt:             row.LastPollAt,
		SlowDownCount:          row.SlowDownCount,
		FailedUserCodeAttempts: row.FailedUserCodeAttempts,
		ApprovedByUserID:       row.ApprovedByUserID,
		ApprovedAt:             row.ApprovedAt,
		DeniedAt:               row.DeniedAt,
		ConsumedAt:             row.ConsumedAt,
		ExpiresAt:              row.ExpiresAt,
		CreatedIP:              row.CreatedIP,
		CreatedUserAgent:       row.CreatedUserAgent,
		CreatedAt:              row.CreatedAt,
	}
}

func entOAuthAuthorizeTransactionToService(row *dbent.OAuthAuthorizeTransaction) *service.OAuthAuthorizeTransaction {
	return &service.OAuthAuthorizeTransaction{
		ID:                    row.ID,
		TransactionID:         row.TransactionID,
		CSRFHash:              row.CsrfHash,
		UserID:                row.UserID,
		ClientID:              row.ClientID,
		RedirectURI:           row.RedirectURI,
		ResponseType:          row.ResponseType,
		Scopes:                row.Scopes,
		AllowedGroupsSnapshot: row.AllowedGroupsSnapshot,
		State:                 row.State,
		CodeChallenge:         row.CodeChallenge,
		CodeChallengeMethod:   row.CodeChallengeMethod,
		RequestedGroupID:      row.RequestedGroupID,
		DeviceID:              row.DeviceID,
		DeviceName:            row.DeviceName,
		ConsumedAt:            row.ConsumedAt,
		ExpiresAt:             row.ExpiresAt,
		CreatedIP:             row.CreatedIP,
		CreatedUserAgent:      row.CreatedUserAgent,
		CreatedAt:             row.CreatedAt,
		Nonce:                 row.Nonce,
	}
}

// Compile-time interface assertions.
var (
	_ service.OAuthClientRepository                = (*oauthProviderRepository)(nil)
	_ service.OAuthCodeRepository                  = (*oauthProviderRepository)(nil)
	_ service.OAuthRefreshTokenRepository          = (*oauthProviderRepository)(nil)
	_ service.OAuthAccessTokenRepository           = (*oauthProviderRepository)(nil)
	_ service.OAuthDeviceCodeRepository            = (*oauthProviderRepository)(nil)
	_ service.OAuthAuthorizeTransactionRepository  = (*oauthProviderRepository)(nil)
	_ service.OAuthAuthorizeAtomicRepository       = (*oauthProviderRepository)(nil)
)
