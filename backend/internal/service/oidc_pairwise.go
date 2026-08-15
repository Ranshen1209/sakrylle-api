package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// ComputePairwiseSub derives a per-client pseudonymous subject identifier per
// OIDC Core §8. The algorithm is:
//
//	SHA-256(issuer + "\x00" + userID + "\x00" + sectorIdentifier)
//
// where userID is the decimal string of the stable user identifier, and
// sectorIdentifier is a stable value computed from the client's sector. The
// result is base64url-encoded (no padding).
//
// This ensures:
//   - Different clients get different subs for the same user (prevents
//     cross-RP correlation).
//   - The same user+client combination always produces the same sub.
//   - The issuer is included so a user cannot be correlated across
//     different OPs.
func ComputePairwiseSub(issuer string, userID int64, sectorIdentifier string) string {
	payload := fmt.Sprintf("%s\x00%d\x00%s", issuer, userID, sectorIdentifier)
	sum := sha256.Sum256([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// SectorIdentifierFromRedirectURIs derives a sector identifier from a client's
// set of redirect_uri hosts, per OIDC Core §8.1.
//
// The sector identifier is the sorted, comma-separated list of host[:port]
// values extracted from the redirect URIs. This matches the OIDC Registration
// spec: the sector_identifier_uri document MUST contain a JSON array of URIs,
// and the pairwise sub is computed from the hosts of those URIs.
//
// When a client has a sector_identifier_uri, the OP fetches that document;
// when absent, the redirect_uris themselves define the sector. This function
// handles the latter case.
func SectorIdentifierFromRedirectURIs(redirectURIs []string) string {
	hosts := make(map[string]struct{})
	for _, raw := range redirectURIs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		// Extract host[port] portion. We use simple string parsing to avoid
		// depending on net/url for a domain extraction that must be stable
		// (the sector identifier is a permanent identifier; using url.Parse
		// would introduce edge cases with malformed URIs).
		host, ok := extractHost(raw)
		if ok && host != "" {
			hosts[host] = struct{}{}
		}
	}

	unique := make([]string, 0, len(hosts))
	for h := range hosts {
		unique = append(unique, h)
	}
	sort.Strings(unique)
	return strings.Join(unique, ",")
}

// extractHost extracts the host[:port] from a URI string.
// Handles common redirect_uri forms: https://example.com/path, http://localhost:8080/callback, etc.
func extractHost(raw string) (string, bool) {
	// Strip scheme
	s := raw
	if idx := strings.Index(s, "://"); idx >= 0 {
		s = s[idx+3:]
	}
	// Find the first /, ?, or # to isolate host[:port]
	if idx := strings.IndexAny(s, "/?#"); idx >= 0 {
		s = s[:idx]
	}
	if s == "" {
		return "", false
	}
	// Remove brackets from IPv6 literal addresses to get a stable host
	s = strings.Trim(s, "[]")
	// Lowercase for consistent comparison
	s = strings.ToLower(s)
	return s, true
}

// ResolvePairwiseSub computes the pairwise sub for a given client+user
// combination. It uses the client's SectorIdentifierURI if set; otherwise
// derives the sector identifier from the client's redirect URIs.
func ResolvePairwiseSub(issuer string, userID int64, subjectType string, sectorIdentifierURI *string, redirectURIs []string) (string, error) {
	if subjectType != "pairwise" {
		return "", nil
	}
	var sectorID string
	if sectorIdentifierURI != nil && *sectorIdentifierURI != "" {
		// Fetch the sector_identifier_uri document to get the authoritative
		// list of redirect_uri hosts. Per OIDC Core §8.1, the sector identifier
		// is derived from the hosts in the fetched document, not the URI itself.
		fetchedURIs, err := FetchSectorIdentifierURI(*sectorIdentifierURI, redirectURIs)
		if err != nil {
			// Fail closed: do NOT fall back to hashing the raw URI string, which
			// would produce a sub on a different basis than the success path and
			// silently rotate the user's pairwise identifier during an outage.
			return "", fmt.Errorf("pairwise sub: sector_identifier_uri unresolved: %w", err)
		}
		sectorID = SectorIdentifierFromRedirectURIs(fetchedURIs)
	} else {
		sectorID = SectorIdentifierFromRedirectURIs(redirectURIs)
	}
	return ComputePairwiseSub(issuer, userID, sectorID), nil
}

// sectorIDCache caches fetched sector_identifier_uri documents to avoid
// repeated HTTP requests for the same URI within a process lifetime.
var (
	sectorIDCacheMu sync.RWMutex
	sectorIDCache   = make(map[string]sectorIDCacheEntry)
)

type sectorIDCacheEntry struct {
	uris      []string
	fetchedAt time.Time
}

const sectorIDCacheTTL = 1 * time.Hour

// FetchSectorIdentifierURI fetches the JSON document at the given
// sector_identifier_uri (OIDC Core §8.1) and validates that the client's
// redirect_uris are a subset of the URIs in the document.
//
// The document must be a JSON array of URI strings. The function:
//   - Only allows HTTPS URIs
//   - Validates that all client redirect_uris appear in the fetched list
//   - Caches results for 1 hour
//   - Returns an error on any failure (callers fail closed)
func FetchSectorIdentifierURI(sectorURI string, clientRedirectURIs []string) ([]string, error) {
	// Check cache first.
	sectorIDCacheMu.RLock()
	if entry, ok := sectorIDCache[sectorURI]; ok {
		if time.Since(entry.fetchedAt) < sectorIDCacheTTL {
			sectorIDCacheMu.RUnlock()
			return entry.uris, nil
		}
	}
	sectorIDCacheMu.RUnlock()

	// Validate URI.
	parsed, err := url.Parse(sectorURI)
	if err != nil {
		return nil, fmt.Errorf("invalid sector_identifier_uri: %w", err)
	}
	if parsed.Scheme != "https" {
		return nil, fmt.Errorf("sector_identifier_uri must use https scheme")
	}

	// Fetch with timeout and size limit.
	if DefaultHTTPClient == nil {
		return nil, fmt.Errorf("sector_identifier_uri fetch: no HTTP client configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sectorURI, nil)
	if err != nil {
		return nil, fmt.Errorf("build sector_identifier_uri request: %w", err)
	}

	resp, err := DefaultHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch sector_identifier_uri: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("sector_identifier_uri returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("read sector_identifier_uri body: %w", err)
	}

	// Parse JSON array of URIs.
	var fetchedURIs []string
	if err := json.Unmarshal(body, &fetchedURIs); err != nil {
		return nil, fmt.Errorf("parse sector_identifier_uri JSON: %w", err)
	}

	// Validate: client redirect_uris must be a subset of fetched URIs.
	if err := validateRedirectURIsSubset(clientRedirectURIs, fetchedURIs); err != nil {
		return nil, fmt.Errorf("sector_identifier_uri validation: %w", err)
	}

	// Cache the result.
	sectorIDCacheMu.Lock()
	sectorIDCache[sectorURI] = sectorIDCacheEntry{
		uris:      fetchedURIs,
		fetchedAt: time.Now(),
	}
	sectorIDCacheMu.Unlock()

	return fetchedURIs, nil
}

// validateRedirectURIsSubset checks that every URI in clientURIs appears
// in the sectorIdentifierURIs list. Per OIDC Core §8.1, the client's
// registered redirect_uris MUST be a subset of the URIs in the
// sector_identifier_uri document.
func validateRedirectURIsSubset(clientURIs, sectorURIs []string) error {
	sectorSet := make(map[string]struct{}, len(sectorURIs))
	for _, u := range sectorURIs {
		sectorSet[strings.TrimSpace(u)] = struct{}{}
	}
	for _, u := range clientURIs {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		if _, ok := sectorSet[u]; !ok {
			return fmt.Errorf("redirect_uri %q is not in sector_identifier_uri document", u)
		}
	}
	return nil
}
