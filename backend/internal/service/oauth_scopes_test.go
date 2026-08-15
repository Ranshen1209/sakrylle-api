package service

import (
	"reflect"
	"testing"
)

func TestNormalizeScopes_LegacyAliases(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"image_generation aliases", []string{"image_generation"}, []string{ScopeImagesCreate}},
		{"balance:read aliases", []string{"balance:read"}, []string{ScopeAccountBalanceRead}},
		{"both aliases", []string{"image_generation", "balance:read"}, []string{ScopeImagesCreate, ScopeAccountBalanceRead}},
		{"alias dedup with canonical", []string{"image_generation", "images:create"}, []string{ScopeImagesCreate}},
		{"trim and dedup", []string{" profile:read ", "profile:read"}, []string{ScopeProfileRead}},
		{"empty entries skipped", []string{"", " ", "models:read"}, []string{ScopeModelsRead}},
		{"order preserved", []string{"models:read", "profile:read"}, []string{ScopeModelsRead, ScopeProfileRead}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeScopes(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("NormalizeScopes(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestHasScope_AcceptsLegacyStored(t *testing.T) {
	if !HasScope([]string{"image_generation"}, ScopeImagesCreate) {
		t.Errorf("legacy image_generation should satisfy images:create check")
	}
	if !HasScope([]string{"balance:read"}, ScopeAccountBalanceRead) {
		t.Errorf("legacy balance:read should satisfy account:balance:read check")
	}
	if !HasScope([]string{ScopeImagesCreate}, "image_generation") {
		t.Errorf("canonical images:create should satisfy legacy image_generation request")
	}
	if HasScope([]string{ScopeProfileRead}, ScopeAccountRead) {
		t.Errorf("profile:read must NOT satisfy account:read")
	}
}

func TestHasAnyScope(t *testing.T) {
	if !HasAnyScope([]string{ScopeProfileRead}, ScopeProfileRead, ScopeAccountRead) {
		t.Errorf("expected match")
	}
	if HasAnyScope([]string{ScopeUsageRead}, ScopeProfileRead, ScopeAccountRead) {
		t.Errorf("expected no match")
	}
}

func TestScopeAllowed(t *testing.T) {
	allowed := []string{ScopeProfileRead, ScopeImagesCreate}
	if !ScopeAllowed(allowed, []string{"image_generation"}) {
		t.Errorf("legacy alias should be normalized then allowed")
	}
	if !ScopeAllowed(allowed, nil) {
		t.Errorf("empty requested must always be allowed")
	}
	if ScopeAllowed(allowed, []string{ScopeUsageRead}) {
		t.Errorf("disallowed scope must be rejected")
	}
}

func TestIsCanonicalScope(t *testing.T) {
	if !IsCanonicalScope(ScopeChatCompletionsCreate) {
		t.Errorf("chat.completions:create should be canonical")
	}
	if IsCanonicalScope("image_generation") {
		t.Errorf("image_generation is a legacy alias, not canonical")
	}
}

func TestIsLegacyAlias(t *testing.T) {
	if !IsLegacyAlias("image_generation") {
		t.Errorf("image_generation should be a legacy alias")
	}
	if IsLegacyAlias(ScopeImagesCreate) {
		t.Errorf("images:create is canonical, not legacy")
	}
}

func TestCanonicalScopes_StableSorted(t *testing.T) {
	got := CanonicalScopes()
	if len(got) != 14 {
		t.Fatalf("expected 14 canonical scopes, got %d: %v", len(got), got)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Errorf("CanonicalScopes not sorted at index %d: %q >= %q", i, got[i-1], got[i])
		}
	}
}

func TestScopeDisplay(t *testing.T) {
	if got := ScopeDisplay(ScopeProfileRead, "zh-CN"); got == ScopeProfileRead {
		t.Errorf("expected zh-CN translation, got raw scope: %q", got)
	}
	if got := ScopeDisplay(ScopeProfileRead, "en"); got == ScopeProfileRead {
		t.Errorf("expected en translation, got raw scope: %q", got)
	}
	if got := ScopeDisplay("nonexistent:scope", "en"); got != "nonexistent:scope" {
		t.Errorf("unknown scope should fall back to itself, got %q", got)
	}
	if got := ScopeDisplay("image_generation", "en"); got == "image_generation" {
		t.Errorf("legacy alias should display as its canonical label")
	}
}

func TestOAuthScopePolicyForRequest(t *testing.T) {
	tests := []struct {
		method, path string
		wantListed   bool
		wantAny      string
	}{
		{"GET", "/v1/models", true, ScopeModelsRead},
		{"GET", "/v1/models/", true, ScopeModelsRead},
		{"GET", "/v1/usage", true, ScopeUsageRead},
		{"GET", "/v1/me", true, ScopeProfileRead},
		{"GET", "/v1/account/balance", true, ScopeAccountBalanceRead},
		{"POST", "/v1/chat/completions", true, ScopeChatCompletionsCreate},
		{"POST", "/chat/completions", true, ScopeChatCompletionsCreate},
		{"POST", "/v1/responses", true, ScopeResponsesCreate},
		{"POST", "/v1/responses/abc/cancel", true, ScopeResponsesCreate},
		{"POST", "/v1/live", true, ScopeResponsesCreate},
		{"GET", "/v1/live/call_abc", true, ScopeResponsesCreate},
		{"POST", "/v1/codex/responses", true, ScopeResponsesCreate},
		{"POST", "/v1/codex/responses/abc", true, ScopeResponsesCreate},
		{"POST", "/v1/messages", true, ScopeMessagesCreate},
		{"POST", "/v1/messages/count_tokens", true, ScopeMessagesCreate},
		{"POST", "/v1/images/generations", true, ScopeImagesCreate},
		{"POST", "/v1/images/edits", true, ScopeImagesCreate},
		{"POST", "/images/generations", true, ScopeImagesCreate},
		{"GET", "/antigravity/models", true, ScopeModelsRead},
		{"GET", "/antigravity/v1/models", true, ScopeModelsRead},
		{"POST", "/antigravity/v1/messages", true, ScopeMessagesCreate},
		{"POST", "/antigravity/v1/messages/count_tokens", true, ScopeMessagesCreate},
		{"GET", "/v1beta/models", false, ""},
		{"POST", "/v1beta/messages", false, ""},
		{"GET", "/healthz", false, ""},
		{"POST", "/v1/models", false, ""},
		{"GET", "/v1/models?fields=id", true, ScopeModelsRead},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			required, listed := OAuthScopePolicyForRequest(tt.method, tt.path)
			if listed != tt.wantListed {
				t.Fatalf("listed=%v, want %v (required=%v)", listed, tt.wantListed, required)
			}
			if !tt.wantListed {
				if required != nil {
					t.Errorf("unlisted route must return nil required, got %v", required)
				}
				return
			}
			found := false
			for _, r := range required {
				if r == tt.wantAny {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("required %v does not contain expected scope %q", required, tt.wantAny)
			}
		})
	}
}
