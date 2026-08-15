package service

import (
	"github.com/golang-jwt/jwt/v5"
)

// ApplyClaimsConstraints filters an existing jwt.MapClaims according to the
// OIDC §5.5 claims request parameter constraints. It is purely subtractive:
// it can keep or delete keys that are already present, but it NEVER adds a
// claim that is not already in the input. This is what makes the claims
// parameter safe to honor — an RP cannot use it to surface a forbidden
// internal field (e.g. balance); see the forbidden-claim invariant test.
// Unmatched claims are silently omitted rather than causing an error.
//
// The section parameter selects which part of the ClaimsRequest to apply:
// "id_token" or "userinfo".
//
// For each claim in the request's section:
//   - essential: true — keep the claim if already present; omit if not
//   - value: "x" — keep only if the existing attribute matches exactly
//   - values: ["a","b"] — keep only if the existing attribute is in the list
//
// Claims not mentioned in the claims request are left untouched.
//
// Wiring status: the capability is ready (this filter + ParseClaimsParameter +
// persistence of the parsed request into oauth_authorize_transactions.claims).
// Enforcement on the UserInfo response is NOT yet active: the claims constraint
// is persisted only on the short-lived authorize transaction and is not
// propagated onto OAuthCode / OAuthAccessToken, which is what the UserInfo
// endpoint loads. Activating UserInfo-side filtering requires carrying claims
// through to the access-token storage (a schema migration), tracked separately.
// discovery advertises claims_parameter_supported=true because the server
// accepts, parses, validates, and stores the parameter without error.
func ApplyClaimsConstraints(claims jwt.MapClaims, claimsReq *ClaimsRequest, section string) jwt.MapClaims {
	if claimsReq == nil || claims == nil {
		return claims
	}

	var sectionMap map[string]ClaimRequestDetail
	switch section {
	case "id_token":
		sectionMap = claimsReq.IDToken
	case "userinfo":
		sectionMap = claimsReq.UserInfo
	default:
		return claims
	}

	if len(sectionMap) == 0 {
		return claims
	}

	result := jwt.MapClaims{}
	for k, v := range claims {
		result[k] = v
	}

	for claimName, detail := range sectionMap {
		val, exists := result[claimName]

		// If the claim doesn't exist in the current claims, omit it. In
		// best-effort mode we do this even for essential claims, without
		// raising an error (the essential flag is intentionally not acted on
		// here).
		if !exists {
			continue
		}

		// value constraint: exact match required
		if detail.Value != nil {
			claimStr, ok := val.(string)
			if !ok || claimStr != *detail.Value {
				delete(result, claimName)
			}
			continue
		}

		// values constraint: must be in the list
		if len(detail.Values) > 0 {
			claimStr, ok := val.(string)
			if !ok {
				delete(result, claimName)
				continue
			}
			found := false
			for _, allowed := range detail.Values {
				if claimStr == allowed {
					found = true
					break
				}
			}
			if !found {
				delete(result, claimName)
			}
			continue
		}

		// essential only (no value/values constraint): include if present
		// — already in result, nothing to do
	}

	return result
}
