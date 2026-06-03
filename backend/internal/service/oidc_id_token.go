package service

import (
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// DefaultOIDCIDTokenTTL is the lifetime of an issued id_token. id_tokens are
// short-lived identity assertions; relying parties re-fetch live state from
// the UserInfo endpoint rather than trusting a cached id_token.
const DefaultOIDCIDTokenTTL = time.Hour

// OIDCUserClaims is the identity input used to build an id_token. It carries
// ONLY stable identity fields — never balance, group, quota, or other mutable
// business state (those live behind /v1/me + the gateway, not in a bearer
// token the client can decode and cache).
type OIDCUserClaims struct {
	UserID   int64
	Email    string
	Username string
}

// forbiddenIDTokenClaims are claim names that must never appear in an id_token
// because they expose mutable commercial state or permissions. This is a
// defense-in-depth allowlist guard: the builder only ever sets standard OIDC
// claims, but assertNoForbiddenClaims fails closed if that ever regresses.
var forbiddenIDTokenClaims = map[string]struct{}{
	"balance":         {},
	"group":           {},
	"group_id":        {},
	"rate_multiplier": {},
	"quota":           {},
	"quota_used":      {},
	"daily_limit_usd": {},
	"model_mapping":   {},
	"models":          {},
	"restrict_models": {},
	"capabilities":    {},
	"allowed_groups":  {},
}

// BuildIDTokenClaims constructs the OIDC id_token claim set.
//
//   - iss is fixed to the provider issuer (https://sub.sakrylle.com).
//   - sub is the user's stable ID as a string (never email).
//   - aud is a single-element array of the OAuth client_id. OIDC Core §2
//     allows a string or an array; we emit an array so strict JS/Python RP
//     libraries that always iterate aud do not throw.
//   - nonce is echoed only when the authorize request supplied one.
//   - auth_time is emitted only when known (non-zero); RPs requesting max_age
//     or doing step-up rely on it.
//   - profile scope yields name/preferred_username; email scope yields email.
//     When email is emitted we also emit email_verified=false: there is no
//     per-user verification flag in the data model, and OIDC Core §5.1 says an
//     absent email_verified is treated as unverified, so we state it honestly
//     rather than letting an RP guess.
func BuildIDTokenClaims(
	issuer, clientID string,
	u OIDCUserClaims,
	grantedScopes []string,
	nonce string,
	authTime time.Time,
	now time.Time,
	ttl time.Duration,
) (jwt.MapClaims, error) {
	if issuer == "" {
		return nil, fmt.Errorf("oidc: issuer is required for id_token iss claim")
	}
	if clientID == "" {
		return nil, fmt.Errorf("oidc: client_id is required for id_token aud claim")
	}
	if u.UserID <= 0 {
		return nil, fmt.Errorf("oidc: user id is required for id_token sub claim")
	}
	if ttl <= 0 {
		ttl = DefaultOIDCIDTokenTTL
	}

	claims := jwt.MapClaims{
		"iss": issuer,
		"sub": strconv.FormatInt(u.UserID, 10),
		"aud": []string{clientID},
		"iat": now.Unix(),
		"exp": now.Add(ttl).Unix(),
	}
	if nonce != "" {
		claims["nonce"] = nonce
	}
	if !authTime.IsZero() {
		claims["auth_time"] = authTime.Unix()
	}
	if HasScope(grantedScopes, ScopeProfile) && u.Username != "" {
		claims["name"] = u.Username
		claims["preferred_username"] = u.Username
	}
	if HasScope(grantedScopes, ScopeEmail) && u.Email != "" {
		claims["email"] = u.Email
		claims["email_verified"] = false
	}

	if err := assertNoForbiddenClaims(claims); err != nil {
		return nil, err
	}
	return claims, nil
}

// assertNoForbiddenClaims fails closed if any business/PII claim is present.
func assertNoForbiddenClaims(claims map[string]any) error {
	for k := range claims {
		if _, bad := forbiddenIDTokenClaims[k]; bad {
			return fmt.Errorf("oidc: forbidden business claim %q must not appear in id_token", k)
		}
	}
	return nil
}
