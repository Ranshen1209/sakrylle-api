package service

import (
	"crypto/sha256"
	"encoding/base64"
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
	UserID        int64
	Email         string
	Username      string
	EmailVerified bool
}

// allowedIDTokenClaims is the exhaustive set of claim names permitted in an
// id_token (the OIDC claim set this OP issues). assertNoForbiddenClaims fails
// closed on ANY claim outside this set, so a future regression that adds a
// sensitive claim (phone_number, address, role, balance, group, ...) is caught.
var allowedIDTokenClaims = map[string]struct{}{
	"iss": {}, "sub": {}, "aud": {}, "exp": {}, "iat": {},
	"nonce": {}, "auth_time": {}, "sid": {},
	"name": {}, "preferred_username": {}, "email": {}, "email_verified": {},
	"at_hash": {}, "c_hash": {},
}

// computeHashClaim implements the OIDC hash algorithm used for at_hash and c_hash:
//
//	claim = left_half(Base64URL(SHA-256(value)))
//
// where left_half takes the first 16 bytes of the 32-byte SHA-256 digest and
// Base64URL encoding uses no padding (RFC 4648 §5).
func computeHashClaim(value string) string {
	sum := sha256.Sum256([]byte(value))
	// Left half: first 16 bytes of the 32-byte digest.
	return base64.RawURLEncoding.EncodeToString(sum[:16])
}

// ComputeAtHash computes the at_hash claim for an access_token per OIDC Core §3.1.3.8.
//
//	at_hash = left_half(Base64URL(SHA-256(access_token)))
//
// Returns the Base64URL-encoded hash value, or an error if the input is empty.
func ComputeAtHash(accessToken string) (string, error) {
	if accessToken == "" {
		return "", fmt.Errorf("oidc: access_token is required for at_hash computation")
	}
	return computeHashClaim(accessToken), nil
}

// ComputeCHash computes the c_hash claim for an authorization code per OIDC Core §3.3.2.11.
//
//	c_hash = left_half(Base64URL(SHA-256(authorization_code)))
//
// Returns the Base64URL-encoded hash value, or an error if the input is empty.
func ComputeCHash(code string) (string, error) {
	if code == "" {
		return "", fmt.Errorf("oidc: authorization_code is required for c_hash computation")
	}
	return computeHashClaim(code), nil
}

// BuildIDTokenClaims constructs the OIDC id_token claim set.
//
//   - iss is fixed to the provider issuer (https://sub.sakrylle.com).
//   - sub is the user's stable ID as a string (never email) for public clients,
//     or the pairwise pseudonym for pairwise clients (OIDC Core §8).
//   - pairwiseSub, when non-empty, overrides the public sub with the
//     per-client pseudonym. Callers should compute it via ResolvePairwiseSub.
//   - aud is a single-element array of the OAuth client_id. OIDC Core §2
//     allows a string or an array; we emit an array so strict JS/Python RP
//     libraries that always iterate aud do not throw.
//   - nonce is echoed only when the authorize request supplied one.
//   - auth_time is emitted only when known (non-zero); RPs requesting max_age
//     or doing step-up rely on it.
//   - profile scope yields name/preferred_username; email scope yields email
//     and email_verified from the user's per-user flag. OIDC Core §5.1 says an
//     absent email_verified is treated as unverified, so we always emit it.
//   - sid, when non-empty, is the OIDC session identifier for back-channel
//     logout (OIDC Back-Channel Logout 1.0 §2.4). Empty for refresh flows.
//   - hashClaims is a variadic trailing parameter for optional OIDC hash claims.
//     Pass at most two strings: [0]=at_hash (OIDC Core §3.1.3.8), [1]=c_hash
//     (OIDC Core §3.3.2.11). Empty strings are treated as "not provided".
//     Callers should compute these via ComputeAtHash / ComputeCHash.
func BuildIDTokenClaims(
	issuer, clientID string,
	u OIDCUserClaims,
	grantedScopes []string,
	nonce string,
	authTime time.Time,
	now time.Time,
	ttl time.Duration,
	pairwiseSub string,
	sid string,
	hashClaims ...string,
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

	sub := pairwiseSub
	if sub == "" {
		sub = strconv.FormatInt(u.UserID, 10)
	}

	claims := jwt.MapClaims{
		"iss": issuer,
		"sub": sub,
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
		claims["email_verified"] = u.EmailVerified
	}
	if sid != "" {
		claims["sid"] = sid
	}
	// OIDC Core §3.1.3.8: at_hash is REQUIRED when the id_token is issued
	// alongside an access_token (implicit/hybrid) and OPTIONAL for code flow.
	// We include it whenever the caller provides it.
	// OIDC Core §3.3.2.11: c_hash is REQUIRED when the id_token is issued
	// alongside an authorization_code.
	if len(hashClaims) > 0 && hashClaims[0] != "" {
		claims["at_hash"] = hashClaims[0]
	}
	if len(hashClaims) > 1 && hashClaims[1] != "" {
		claims["c_hash"] = hashClaims[1]
	}

	if err := assertNoForbiddenClaims(claims); err != nil {
		return nil, err
	}
	return claims, nil
}

// assertNoForbiddenClaims fails closed if any claim outside the standard OIDC
// allowlist is present (defense in depth against a builder regression leaking
// business/PII state into the id_token).
func assertNoForbiddenClaims(claims map[string]any) error {
	for k := range claims {
		if _, ok := allowedIDTokenClaims[k]; !ok {
			return fmt.Errorf("oidc: claim %q is not in the id_token allowlist", k)
		}
	}
	return nil
}
