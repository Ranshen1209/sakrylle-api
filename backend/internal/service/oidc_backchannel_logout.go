package service

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// DefaultLogoutTokenTTL is the lifetime of a back-channel logout token.
// Per OIDC Back-Channel Logout 1.0, logout tokens SHOULD have a short
// expiration to limit the window for replay.
const DefaultLogoutTokenTTL = 2 * time.Minute

// BuildLogoutToken constructs a signed logout_token JWT for back-channel
// logout notification (OIDC Back-Channel Logout 1.0 §2.6).
//
// Claims:
//   - iss: the OP issuer
//   - sub: the user's stable identifier (or pairwise sub)
//   - aud: the RP's client_id (single string, per spec)
//   - iat: issued-at timestamp
//   - exp: expiration (short TTL, default 2 minutes)
//   - events: {"http://schemas.openid.net/event/backchannel-logout": {}}
//   - sid: optional session ID (included when non-empty)
//
// The token is signed with the OP's RS256 or ES256 key, matching the
// id_token signing algorithm for the client.
func BuildLogoutToken(issuer, sub, clientID, sid string, now time.Time, ttl time.Duration) jwt.MapClaims {
	if ttl <= 0 {
		ttl = DefaultLogoutTokenTTL
	}
	claims := jwt.MapClaims{
		"iss": issuer,
		"sub": sub,
		"aud": clientID,
		"iat": now.Unix(),
		"exp": now.Add(ttl).Unix(),
		"events": map[string]any{
			"http://schemas.openid.net/event/backchannel-logout": struct{}{},
		},
	}
	if sid != "" {
		claims["sid"] = sid
	}
	return claims
}

// GenerateSessionID creates a unique session identifier for back-channel
// logout session tracking. The sid is included in the id_token (when the
// client has backchannel_logout_session_required=true) and echoed in the
// logout_token so the RP can identify which session ended.
func GenerateSessionID() string {
	sid, err := GenerateOpaqueToken(16)
	if err != nil {
		// crypto/rand failure is extremely rare; fallback to a timestamp-
		// based value that is still unique enough for session tracking.
		return fmt.Sprintf("sid_%d", time.Now().UnixNano())
	}
	return "sid_" + sid
}
