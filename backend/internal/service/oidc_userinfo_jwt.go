package service

import (
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// DefaultUserInfoJWT TTL is the lifetime of a signed UserInfo JWT. It is
// deliberately short (5 minutes) because the JWT is a point-in-time assertion;
// RPs should fetch a fresh UserInfo when they need current claims.
const DefaultUserInfoJWTTTL = 5 * time.Minute

// BuildUserInfoJWTClaims constructs the claims for a signed UserInfo JWT
// (OIDC Core §5.3.2). Only standard OIDC claims are included; commercial
// claims (balance, group, quota, rate_multiplier, etc.) MUST NOT appear here.
//
// The sub claim mirrors the id_token's sub: the stable user ID for public
// clients, or the pairwise pseudonym for pairwise clients. Callers are
// responsible for computing the correct sub value.
//
// Claims emitted:
//   - sub (always, required by OIDC)
//   - name, preferred_username (when sub is set and username is non-empty)
//   - email, email_verified (when email is non-empty)
func BuildUserInfoJWTClaims(
	sub string,
	username string,
	email string,
	now time.Time,
	ttl time.Duration,
) jwt.MapClaims {
	if ttl <= 0 {
		ttl = DefaultUserInfoJWTTTL
	}
	claims := jwt.MapClaims{
		"sub": sub,
		"iat": now.Unix(),
		"exp": now.Add(ttl).Unix(),
	}
	if username != "" {
		claims["name"] = username
		claims["preferred_username"] = username
	}
	if email != "" {
		claims["email"] = email
		claims["email_verified"] = false
	}
	return claims
}

// UserInfoSubForOAuthToken resolves the correct sub value for a UserInfo
// response, handling both public and pairwise subject types.
//
// Parameters:
//   - userID: the stable user identifier
//   - client: the OAuth client (nil means use public sub)
//   - issuer: the OP issuer URL (needed for pairwise computation)
//
// Returns the sub string to use in UserInfo responses and signed JWTs.
func UserInfoSubForOAuthToken(userID int64, client *OAuthClient, issuer string) string {
	if client != nil && client.SubjectType == "pairwise" {
		if pw := ResolvePairwiseSub(issuer, userID, client.SubjectType, client.SectorIdentifierURI, client.RedirectURIs); pw != "" {
			return pw
		}
	}
	return strconv.FormatInt(userID, 10)
}
