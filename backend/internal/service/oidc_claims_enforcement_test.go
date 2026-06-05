package service

import (
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyClaimsConstraints_NilInputs(t *testing.T) {
	claims := jwt.MapClaims{"sub": "123", "email": "a@b.c"}

	// nil claims request → no filtering
	result := ApplyClaimsConstraints(claims, nil, "id_token")
	assert.Equal(t, claims, result)

	// nil claims → returns nil
	result = ApplyClaimsConstraints(nil, &ClaimsRequest{}, "id_token")
	assert.Nil(t, result)
}

func TestApplyClaimsConstraints_Essential(t *testing.T) {
	claims := jwt.MapClaims{"sub": "123", "email": "a@b.c"}

	// essential=true with matching claim → included
	cr := &ClaimsRequest{
		IDToken: map[string]ClaimRequestDetail{
			"email": {Essential: boolPtr(true)},
		},
	}
	result := ApplyClaimsConstraints(claims, cr, "id_token")
	assert.Equal(t, "a@b.c", result["email"])

	// essential=true with missing claim → omitted (best-effort)
	claims2 := jwt.MapClaims{"sub": "123"}
	result2 := ApplyClaimsConstraints(claims2, cr, "id_token")
	_, hasEmail := result2["email"]
	assert.False(t, hasEmail, "essential claim not available should be omitted")
}

func TestApplyClaimsConstraints_Value(t *testing.T) {
	claims := jwt.MapClaims{"sub": "123", "email": "a@b.c"}

	// value matches → included
	cr := &ClaimsRequest{
		IDToken: map[string]ClaimRequestDetail{
			"email": {Value: strPtr("a@b.c")},
		},
	}
	result := ApplyClaimsConstraints(claims, cr, "id_token")
	assert.Equal(t, "a@b.c", result["email"])

	// value doesn't match → omitted
	cr2 := &ClaimsRequest{
		IDToken: map[string]ClaimRequestDetail{
			"email": {Value: strPtr("other@example.com")},
		},
	}
	result2 := ApplyClaimsConstraints(claims, cr2, "id_token")
	_, hasEmail := result2["email"]
	assert.False(t, hasEmail, "non-matching value should omit claim")
}

func TestApplyClaimsConstraints_Values(t *testing.T) {
	claims := jwt.MapClaims{"sub": "123", "email": "a@b.c"}

	// value in list → included
	cr := &ClaimsRequest{
		IDToken: map[string]ClaimRequestDetail{
			"email": {Values: []string{"a@b.c", "x@y.z"}},
		},
	}
	result := ApplyClaimsConstraints(claims, cr, "id_token")
	assert.Equal(t, "a@b.c", result["email"])

	// value not in list → omitted
	cr2 := &ClaimsRequest{
		IDToken: map[string]ClaimRequestDetail{
			"email": {Values: []string{"x@y.z", "other@example.com"}},
		},
	}
	result2 := ApplyClaimsConstraints(claims, cr2, "id_token")
	_, hasEmail := result2["email"]
	assert.False(t, hasEmail, "value not in list should omit claim")
}

func TestApplyClaimsConstraints_SectionSelection(t *testing.T) {
	claims := jwt.MapClaims{"sub": "123", "email": "a@b.c"}

	cr := &ClaimsRequest{
		UserInfo: map[string]ClaimRequestDetail{
			"email": {Value: strPtr("other@example.com")},
		},
	}

	// id_token section → no filtering (email constraint is in userinfo)
	result := ApplyClaimsConstraints(claims, cr, "id_token")
	assert.Equal(t, "a@b.c", result["email"])

	// userinfo section → filtering applies
	result2 := ApplyClaimsConstraints(claims, cr, "userinfo")
	_, hasEmail := result2["email"]
	assert.False(t, hasEmail)
}

func TestApplyClaimsConstraints_UnmentionedClaimsUntouched(t *testing.T) {
	claims := jwt.MapClaims{"sub": "123", "email": "a@b.c", "name": "alice"}

	cr := &ClaimsRequest{
		IDToken: map[string]ClaimRequestDetail{
			"email": {Essential: boolPtr(true)},
		},
	}
	result := ApplyClaimsConstraints(claims, cr, "id_token")
	require.Equal(t, "123", result["sub"], "sub must be untouched")
	require.Equal(t, "alice", result["name"], "name must be untouched")
	require.Equal(t, "a@b.c", result["email"], "email must be present")
}
