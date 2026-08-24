package service

import "github.com/tidwall/gjson"

// applyDeepSeekClaudeUsageAliases supplements the Anthropic-shaped usage
// parser with DeepSeek's OpenAI-style prompt cache fields. The standard
// Anthropic fields remain authoritative when present; aliases are only a
// fallback, so other Anthropic providers keep their existing semantics.
func applyDeepSeekClaudeUsageAliases(node gjson.Result, usage *ClaudeUsage) {
	if usage == nil || !node.Exists() || !node.IsObject() {
		return
	}
	hit := node.Get("prompt_cache_hit_tokens")
	miss := node.Get("prompt_cache_miss_tokens")
	prompt := node.Get("prompt_tokens")
	if !hit.Exists() && !miss.Exists() && !prompt.Exists() {
		return
	}

	hitTokens := deepSeekAliasTokenCount(hit)
	missTokens := deepSeekAliasTokenCount(miss)
	if hit.Exists() {
		// An explicit zero is meaningful: it must not be replaced by a generic
		// cached_tokens fallback that may describe a different provider field.
		usage.CacheReadInputTokens = hitTokens
	} else if miss.Exists() {
		// A miss-only payload is also authoritative. Clear any canonical or
		// compatibility cached_tokens value parsed before this alias pass.
		usage.CacheReadInputTokens = 0
	}

	// Anthropic's canonical positive input_tokens excludes cache buckets. Keep
	// that provider-native interpretation when a relay also includes DeepSeek's
	// aliases. A zero placeholder is allowed to fall through to a nonzero alias.
	canonicalInput := node.Get("input_tokens")
	if canonicalInput.Exists() && canonicalInput.Int() > 0 {
		usage.inputTokensIncludeCache = false
		return
	}
	if usage.InputTokens != 0 {
		return
	}
	if prompt.Exists() {
		// DeepSeek prompt_tokens is the total (cache hit + cache miss). Keep the
		// total in the shared usage object for the OpenAI-compatible adapter; the
		// generic Anthropic billing path normalizes it exactly once later.
		usage.InputTokens = deepSeekAliasTokenCount(prompt)
		usage.inputTokensIncludeCache = true
		return
	}
	if hit.Exists() || miss.Exists() {
		usage.InputTokens = deepSeekAliasTokenSum(hitTokens, missTokens)
		usage.inputTokensIncludeCache = true
	}
}

// deepSeekAliasTokenCount converts an alias value to a bounded, non-negative
// int. Upstream usage is trusted for accounting, but malformed negative values
// must never turn into a refund or a negative cache bucket.
func deepSeekAliasTokenCount(value gjson.Result) int {
	if !value.Exists() {
		return 0
	}
	n := value.Int()
	if n <= 0 {
		return 0
	}
	maxInt := int64(^uint(0) >> 1)
	if n > maxInt {
		return int(maxInt)
	}
	return int(n)
}

func deepSeekAliasTokenSum(left, right int) int {
	maxInt := int64(^uint(0) >> 1)
	left64 := int64(left)
	right64 := int64(right)
	if left64 >= maxInt || right64 >= maxInt || left64 > maxInt-right64 {
		return int(maxInt)
	}
	return int(left64 + right64)
}

// normalizeDeepSeekClaudeUsageForBilling converts the OpenAI-style total into
// Anthropic's disjoint billing buckets. It is deliberately called only by the
// generic GatewayService path; the native OpenAI-compatible adapter still
// needs the total so its own RecordUsage subtraction remains correct.
func normalizeDeepSeekClaudeUsageForBilling(usage *ClaudeUsage) {
	if usage == nil || !usage.inputTokensIncludeCache {
		return
	}

	input := usage.InputTokens
	if input < 0 {
		input = 0
	}
	hit := usage.CacheReadInputTokens
	if hit < 0 {
		hit = 0
	}
	creation := usage.CacheCreationInputTokens
	if creation < 0 {
		creation = 0
	}
	if creation == 0 {
		creation = deepSeekAliasTokenSum(maxIntOrZero(usage.CacheCreation5mTokens), maxIntOrZero(usage.CacheCreation1hTokens))
	}
	usage.CacheReadInputTokens = hit
	usage.CacheCreationInputTokens = creation
	remaining := input
	if hit >= remaining {
		remaining = 0
	} else {
		remaining -= hit
	}
	if creation >= remaining {
		remaining = 0
	} else {
		remaining -= creation
	}
	usage.InputTokens = remaining
	usage.inputTokensIncludeCache = false
}

// normalizeDeepSeekClaudeUsageForAccount gates the DeepSeek-specific total
// prompt-token conversion at the account boundary. The Claude-shaped parser
// is shared by Anthropic-compatible providers, so applying this conversion to
// every account could subtract legitimate native cache buckets twice.
func normalizeDeepSeekClaudeUsageForAccount(account *Account, usage *ClaudeUsage) {
	if account == nil || account.Platform != PlatformDeepseek {
		return
	}
	normalizeDeepSeekClaudeUsageForBilling(usage)
}

func maxIntOrZero(value int) int {
	if value < 0 {
		return 0
	}
	return value
}
