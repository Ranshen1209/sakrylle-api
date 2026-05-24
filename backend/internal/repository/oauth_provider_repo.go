package repository

import (
	"context"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/oauthclient"
	"github.com/Wei-Shaw/sub2api/ent/oauthcode"
	"github.com/Wei-Shaw/sub2api/ent/oauthrefreshtoken"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// oauthProviderRepository is an Ent-backed implementation of the three OAuth
// provider repository interfaces (clients, codes, refresh tokens).
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

// ── OAuthCodeRepository ─────────────────────────────────────────────────────

func (r *oauthProviderRepository) CreateCode(ctx context.Context, code *service.OAuthCode) error {
	_, err := r.client.OAuthCode.Create().
		SetCodeHash(code.CodeHash).
		SetClientID(code.ClientID).
		SetUserID(code.UserID).
		SetRedirectURI(code.RedirectURI).
		SetScopes(code.Scopes).
		SetCodeChallenge(code.CodeChallenge).
		SetCodeChallengeMethod(code.CodeChallengeMethod).
		SetExpiresAt(code.ExpiresAt).
		Save(ctx)
	if err != nil {
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
	_, err := r.client.OAuthRefreshToken.Create().
		SetTokenHash(token.TokenHash).
		SetClientID(token.ClientID).
		SetUserID(token.UserID).
		SetAPIKeyID(token.APIKeyID).
		SetScopes(token.Scopes).
		SetExpiresAt(token.ExpiresAt).
		Save(ctx)
	if err != nil {
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
		ClientID:               row.ClientID,
		Name:                   row.Name,
		RedirectURIs:           row.RedirectUris,
		AllowedScopes:          row.AllowedScopes,
		PKCERequired:           row.PkceRequired,
		AccessTokenTTLSeconds:  row.AccessTokenTTLSeconds,
		RefreshTokenTTLSeconds: row.RefreshTokenTTLSeconds,
		Disabled:               row.Disabled,
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
	return &service.OAuthCode{
		CodeHash:            row.CodeHash,
		ClientID:            row.ClientID,
		UserID:              row.UserID,
		RedirectURI:         row.RedirectURI,
		Scopes:              row.Scopes,
		CodeChallenge:       row.CodeChallenge,
		CodeChallengeMethod: row.CodeChallengeMethod,
		ExpiresAt:           row.ExpiresAt,
		UsedAt:              row.UsedAt,
	}
}

func entOAuthRefreshTokenToService(row *dbent.OAuthRefreshToken) *service.OAuthRefreshToken {
	return &service.OAuthRefreshToken{
		TokenHash:     row.TokenHash,
		ClientID:      row.ClientID,
		UserID:        row.UserID,
		APIKeyID:      row.APIKeyID,
		Scopes:        row.Scopes,
		ExpiresAt:     row.ExpiresAt,
		RevokedAt:     row.RevokedAt,
		RotatedToHash: row.RotatedToHash,
	}
}

// Compile-time interface assertions.
var (
	_ service.OAuthClientRepository       = (*oauthProviderRepository)(nil)
	_ service.OAuthCodeRepository         = (*oauthProviderRepository)(nil)
	_ service.OAuthRefreshTokenRepository = (*oauthProviderRepository)(nil)
)
