package service

import (
	"regexp"
	"sort"
	"strings"
)

// Canonical OAuth v2 scope identifiers. See docs/OAUTH_V2_DESIGN.md §7.1.
const (
	ScopeProfileRead           = "profile:read"
	ScopeEmailRead             = "email:read"
	ScopeAccountRead           = "account:read"
	ScopeAccountBalanceRead    = "account:balance:read"
	ScopeModelsRead            = "models:read"
	ScopeChatCompletionsCreate = "chat.completions:create"
	ScopeResponsesCreate       = "responses:create"
	ScopeMessagesCreate        = "messages:create"
	ScopeImagesCreate          = "images:create"
	ScopeUsageRead             = "usage:read"
	ScopeOfflineAccess         = "offline_access"
)

// canonicalScopes is the set of scopes the server recognises after normalization.
var canonicalScopes = map[string]struct{}{
	ScopeProfileRead:           {},
	ScopeEmailRead:             {},
	ScopeAccountRead:           {},
	ScopeAccountBalanceRead:    {},
	ScopeModelsRead:            {},
	ScopeChatCompletionsCreate: {},
	ScopeResponsesCreate:       {},
	ScopeMessagesCreate:        {},
	ScopeImagesCreate:          {},
	ScopeUsageRead:             {},
	ScopeOfflineAccess:         {},
}

// legacyScopeAliases maps deprecated v1 scope identifiers to their canonical
// v2 equivalents. See §7.2.
var legacyScopeAliases = map[string]string{
	"image_generation": ScopeImagesCreate,
	"balance:read":     ScopeAccountBalanceRead,
}

// CanonicalScopes returns a stable, sorted list of every scope this server
// recognises after normalization. Used by /.well-known/oauth-authorization-server.
func CanonicalScopes() []string {
	out := make([]string, 0, len(canonicalScopes))
	for s := range canonicalScopes {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// NormalizeScopes trims, deduplicates, and rewrites legacy aliases to their
// canonical form, preserving the input order of the first occurrence of each
// (post-rewrite) scope.
//
// See §7.2: HasScope must operate on normalized scopes so legacy rows that
// stored "image_generation" still satisfy "images:create" checks.
func NormalizeScopes(scopes []string) []string {
	out := make([]string, 0, len(scopes))
	seen := make(map[string]struct{}, len(scopes))
	for _, raw := range scopes {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if alias, ok := legacyScopeAliases[s]; ok {
			s = alias
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// IsCanonicalScope reports whether s is a server-recognised canonical scope.
func IsCanonicalScope(s string) bool {
	_, ok := canonicalScopes[s]
	return ok
}

// IsLegacyAlias reports whether s is a recognised legacy alias.
func IsLegacyAlias(s string) bool {
	_, ok := legacyScopeAliases[s]
	return ok
}

// ScopeAllowed reports whether every scope in requested appears in allowed,
// after both have been normalized.
func ScopeAllowed(allowed, requested []string) bool {
	if len(requested) == 0 {
		return true
	}
	allow := NormalizeScopes(allowed)
	set := make(map[string]struct{}, len(allow))
	for _, s := range allow {
		set[s] = struct{}{}
	}
	for _, s := range NormalizeScopes(requested) {
		if _, ok := set[s]; !ok {
			return false
		}
	}
	return true
}

// HasScope reports whether granted (post-normalization) contains required.
//
// required is treated as a canonical scope; legacy aliases are accepted in
// granted because old token rows may still store them. See §7.2.
func HasScope(granted []string, required string) bool {
	if required == "" {
		return false
	}
	target := required
	if alias, ok := legacyScopeAliases[required]; ok {
		target = alias
	}
	for _, raw := range granted {
		s := strings.TrimSpace(raw)
		if alias, ok := legacyScopeAliases[s]; ok {
			s = alias
		}
		if s == target {
			return true
		}
	}
	return false
}

// HasAnyScope reports whether granted contains at least one of any.
// Used by /v1/me where any of profile/account/balance scopes suffices.
func HasAnyScope(granted []string, any ...string) bool {
	for _, r := range any {
		if HasScope(granted, r) {
			return true
		}
	}
	return false
}

// ScopeDisplay returns the human-readable label for a scope in the given
// locale. Falls back to the raw scope identifier for unknown scopes.
//
// Locale normalization: anything starting with "zh" maps to zh; everything
// else maps to en.
func ScopeDisplay(scope, locale string) string {
	loc := normalizeDisplayLocale(locale)
	if labels, ok := scopeLabels[scope]; ok {
		if v, ok := labels[loc]; ok {
			return v
		}
		if v, ok := labels["en"]; ok {
			return v
		}
	}
	if alias, ok := legacyScopeAliases[scope]; ok {
		if labels, ok := scopeLabels[alias]; ok {
			if v, ok := labels[loc]; ok {
				return v
			}
			if v, ok := labels["en"]; ok {
				return v
			}
		}
	}
	return scope
}

func normalizeDisplayLocale(locale string) string {
	l := strings.ToLower(strings.TrimSpace(locale))
	if strings.HasPrefix(l, "zh") {
		return "zh"
	}
	return "en"
}

// scopeLabels powers the consent UI and Authorized Apps page. See §14.1.
var scopeLabels = map[string]map[string]string{
	ScopeProfileRead: {
		"zh": "查看您的档案（用户名、头像）",
		"en": "Read your profile (username, avatar)",
	},
	ScopeEmailRead: {
		"zh": "查看您的邮箱地址",
		"en": "Read your email address",
	},
	ScopeAccountRead: {
		"zh": "查看账户信息（当前分组、可用分组、配额）",
		"en": "Read account info (current group, allowed groups, quota)",
	},
	ScopeAccountBalanceRead: {
		"zh": "查看账户余额",
		"en": "Read account balance",
	},
	ScopeModelsRead: {
		"zh": "列出可用模型",
		"en": "List available models",
	},
	ScopeChatCompletionsCreate: {
		"zh": "调用聊天补全 API（/v1/chat/completions）",
		"en": "Call chat completions API",
	},
	ScopeResponsesCreate: {
		"zh": "调用 Responses API（/v1/responses）",
		"en": "Call Responses API",
	},
	ScopeMessagesCreate: {
		"zh": "调用 Messages API（/v1/messages）",
		"en": "Call Messages API",
	},
	ScopeImagesCreate: {
		"zh": "生成与编辑图片",
		"en": "Create and edit images",
	},
	ScopeUsageRead: {
		"zh": "查看用量统计",
		"en": "Read usage statistics",
	},
	ScopeOfflineAccess: {
		"zh": "保持离线访问（颁发 refresh token）",
		"en": "Offline access (issue a refresh token)",
	},
}

// ── Endpoint scope policy (§7.3) ────────────────────────────────────────────

type scopePolicyEntry struct {
	method   string
	pattern  *regexp.Regexp
	required []string
}

// oauthScopePolicies is the §7.3 endpoint scope matrix. Keep in sync with the
// design doc — every API-key-authenticated route that should accept sk_oauth_
// tokens MUST appear here. Unlisted routes reject sk_oauth_ tokens by default.
var oauthScopePolicies = []scopePolicyEntry{
	{method: "GET", pattern: regexp.MustCompile(`^/v1/me/?$`), required: []string{ScopeProfileRead, ScopeAccountRead, ScopeAccountBalanceRead}},

	{method: "GET", pattern: regexp.MustCompile(`^/v1/account/balance/?$`), required: []string{ScopeAccountBalanceRead, ScopeAccountRead}},
	{method: "GET", pattern: regexp.MustCompile(`^/v1/models/?$`), required: []string{ScopeModelsRead}},
	{method: "GET", pattern: regexp.MustCompile(`^/v1/usage/?$`), required: []string{ScopeUsageRead}},

	{method: "POST", pattern: regexp.MustCompile(`^/v1/chat/completions/?$`), required: []string{ScopeChatCompletionsCreate}},
	{method: "POST", pattern: regexp.MustCompile(`^/chat/completions/?$`), required: []string{ScopeChatCompletionsCreate}},

	{method: "POST", pattern: regexp.MustCompile(`^/v1/responses(/.*)?$`), required: []string{ScopeResponsesCreate}},
	{method: "GET", pattern: regexp.MustCompile(`^/v1/responses(/.*)?$`), required: []string{ScopeResponsesCreate}},
	{method: "POST", pattern: regexp.MustCompile(`^/responses(/.*)?$`), required: []string{ScopeResponsesCreate}},
	{method: "GET", pattern: regexp.MustCompile(`^/responses(/.*)?$`), required: []string{ScopeResponsesCreate}},
	{method: "POST", pattern: regexp.MustCompile(`^/v1/codex/responses(/.*)?$`), required: []string{ScopeResponsesCreate}},
	{method: "GET", pattern: regexp.MustCompile(`^/v1/codex/responses(/.*)?$`), required: []string{ScopeResponsesCreate}},

	{method: "POST", pattern: regexp.MustCompile(`^/v1/messages/?$`), required: []string{ScopeMessagesCreate}},
	{method: "POST", pattern: regexp.MustCompile(`^/v1/messages/count_tokens/?$`), required: []string{ScopeMessagesCreate}},

	{method: "POST", pattern: regexp.MustCompile(`^/v1/images/generations/?$`), required: []string{ScopeImagesCreate}},
	{method: "POST", pattern: regexp.MustCompile(`^/v1/images/edits/?$`), required: []string{ScopeImagesCreate}},
	{method: "POST", pattern: regexp.MustCompile(`^/images/generations/?$`), required: []string{ScopeImagesCreate}},
	{method: "POST", pattern: regexp.MustCompile(`^/images/edits/?$`), required: []string{ScopeImagesCreate}},

	{method: "GET", pattern: regexp.MustCompile(`^/antigravity/models/?$`), required: []string{ScopeModelsRead}},
	{method: "GET", pattern: regexp.MustCompile(`^/antigravity/v1/models/?$`), required: []string{ScopeModelsRead}},
	{method: "GET", pattern: regexp.MustCompile(`^/antigravity/v1/usage/?$`), required: []string{ScopeUsageRead}},
	{method: "POST", pattern: regexp.MustCompile(`^/antigravity/v1/messages/?$`), required: []string{ScopeMessagesCreate}},
	{method: "POST", pattern: regexp.MustCompile(`^/antigravity/v1/messages/count_tokens/?$`), required: []string{ScopeMessagesCreate}},
}

// OAuthScopePolicyForRequest looks up the §7.3 scope policy for an
// (HTTP method, path) pair.
//
// Returns:
//
//   - required: the scopes any of which permits the request
//   - listed: true if the route appears in the matrix; false means unlisted,
//     and the caller MUST reject sk_oauth_ tokens by default while still
//     allowing manual API keys.
func OAuthScopePolicyForRequest(method, path string) (required []string, listed bool) {
	m := strings.ToUpper(strings.TrimSpace(method))
	p := path
	if i := strings.Index(p, "?"); i >= 0 {
		p = p[:i]
	}
	for _, e := range oauthScopePolicies {
		if e.method != m {
			continue
		}
		if e.pattern.MatchString(p) {
			out := make([]string, len(e.required))
			copy(out, e.required)
			return out, true
		}
	}
	return nil, false
}
