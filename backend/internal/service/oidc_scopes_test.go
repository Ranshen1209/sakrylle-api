package service

import "testing"

// OIDC standard scopes (openid/profile/email) must be recognised as canonical
// so NormalizeScopes does not silently drop them and discovery advertises them.
func TestOIDCScopesAreCanonical(t *testing.T) {
	for _, s := range []string{ScopeOpenID, ScopeProfile, ScopeEmail} {
		if !IsCanonicalScope(s) {
			t.Errorf("scope %q should be canonical", s)
		}
	}
}

func TestNormalizeScopesPreservesOpenID(t *testing.T) {
	got := NormalizeScopes([]string{"openid", "profile", "email", "openid"})
	want := []string{"openid", "profile", "email"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

// The OIDC `email`/`profile` scopes are distinct from Sakrylle's commercial
// `email:read`/`profile:read` permissions and must NOT be aliased to them.
func TestOIDCScopesNotAliasedToCommercial(t *testing.T) {
	if HasScope([]string{ScopeProfile}, ScopeProfileRead) {
		t.Error("openid `profile` must not satisfy commercial `profile:read`")
	}
	if HasScope([]string{ScopeEmail}, ScopeEmailRead) {
		t.Error("openid `email` must not satisfy commercial `email:read`")
	}
}

func TestCanonicalScopesIncludesOIDC(t *testing.T) {
	all := CanonicalScopes()
	set := make(map[string]struct{}, len(all))
	for _, s := range all {
		set[s] = struct{}{}
	}
	for _, s := range []string{ScopeOpenID, ScopeProfile, ScopeEmail} {
		if _, ok := set[s]; !ok {
			t.Errorf("CanonicalScopes() missing %q", s)
		}
	}
}
