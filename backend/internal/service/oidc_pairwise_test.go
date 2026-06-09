package service

import (
	"testing"
)

// TestResolvePairwiseSub_FailClosedOnFetchError verifies that when a
// sector_identifier_uri cannot be fetched/validated, ResolvePairwiseSub fails
// closed (returns an error) rather than falling back to hashing the raw URI
// string, which would silently rotate the user's pairwise sub.
//
// The .invalid TLD is reserved by RFC 2606 and guarantees DNS resolution
// failure, so this test does not depend on outbound network access.
func TestResolvePairwiseSub_FailClosedOnFetchError(t *testing.T) {
	uri := "https://nonexistent.invalid/sector.json"
	_, err := ResolvePairwiseSub("https://sub.sakrylle.com", 42, "pairwise", &uri,
		[]string{"https://rp.example.com/cb"})
	if err == nil {
		t.Fatal("expected error when sector_identifier_uri fetch fails (fail-closed)")
	}
}

// TestResolvePairwiseSub_NoSectorURI_UsesRedirectHosts verifies the success
// path: with no sector_identifier_uri, the sector is derived from the redirect
// URI hosts and a deterministic pairwise sub is produced.
func TestResolvePairwiseSub_NoSectorURI_UsesRedirectHosts(t *testing.T) {
	sub, err := ResolvePairwiseSub("https://sub.sakrylle.com", 42, "pairwise", nil,
		[]string{"https://rp.example.com/cb"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sub == "" {
		t.Fatal("expected a deterministic pairwise sub from redirect hosts")
	}
}
