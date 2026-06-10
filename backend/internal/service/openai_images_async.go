package service

import (
	"strconv"
	"strings"
)

// IsAsyncImage reports whether this account routes image requests through the
// async task bridge (12ai cdn task API) instead of the synchronous endpoint.
func (a *Account) IsAsyncImage() bool {
	return strings.EqualFold(strings.TrimSpace(a.GetCredential("async_enabled")), "true")
}

// AsyncImageBaseURL is the async task host, e.g. https://cdn.12ai.org (no trailing slash).
func (a *Account) AsyncImageBaseURL() string {
	return strings.TrimRight(strings.TrimSpace(a.GetCredential("async_base_url")), "/")
}

func (a *Account) asyncIntCredential(key string, def int) int {
	v := strings.TrimSpace(a.GetCredential(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// AsyncPollIntervalMs is the poll interval in ms (default 3000).
func (a *Account) AsyncPollIntervalMs() int { return a.asyncIntCredential("poll_interval_ms", 3000) }

// AsyncMaxWaitMs is the max total poll wait in ms (default 240000).
func (a *Account) AsyncMaxWaitMs() int { return a.asyncIntCredential("max_wait_ms", 240000) }
