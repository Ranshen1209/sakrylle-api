package service

import (
	"github.com/golang-jwt/jwt/v5"
)

// ApplyClaimsConstraints filters claims in a jwt.MapClaims based on the
// OIDC §5.5 claims request parameter constraints. This is "best-effort" mode:
// unmatched claims are silently omitted rather than causing an error.
//
// The section parameter selects which part of the ClaimsRequest to apply:
// "id_token" or "userinfo".
//
// For each claim in the request's section:
//   - essential: true — include the claim if the user has it; omit if not
//   - value: "x" — include only if the user attribute matches exactly
//   - values: ["a","b"] — include only if the user attribute is in the list
//
// Claims not mentioned in the claims request are left untouched.
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

		// If the claim doesn't exist in the current claims, check if it's
		// essential — in best-effort mode we just omit it (no error).
		if !exists {
			if detail.Essential != nil && *detail.Essential {
				// essential claim not available — omit (best-effort)
			}
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
