package oauth

import (
	"testing"
)

func TestIsLoopbackRedirect(t *testing.T) {
	tests := []struct {
		name       string
		registered string
		candidate  string
		want       bool
	}{
		// Happy path: valid loopback redirects
		{
			name:       "localhost without port matches localhost with port",
			registered: "http://localhost/callback",
			candidate:  "http://localhost:8080/callback",
			want:       true,
		},
		{
			name:       "127.0.0.1 without port matches 127.0.0.1 with port",
			registered: "http://127.0.0.1/callback",
			candidate:  "http://127.0.0.1:3000/callback",
			want:       true,
		},
		{
			name:       "IPv6 ::1 without port matches ::1 with port",
			registered: "http://[::1]/callback",
			candidate:  "http://[::1]:8080/callback",
			want:       true,
		},
		{
			name:       "candidate port 80 matches registered without port",
			registered: "http://localhost/callback",
			candidate:  "http://localhost:80/callback",
			want:       true,
		},
		{
			name:       "candidate high port 65535 matches registered without port",
			registered: "http://127.0.0.1/callback",
			candidate:  "http://127.0.0.1:65535/callback",
			want:       true,
		},
		{
			name:       "both URIs without port match",
			registered: "http://localhost/callback",
			candidate:  "http://localhost/callback",
			want:       true,
		},
		{
			name:       "matching paths with trailing slashes",
			registered: "http://localhost/callback/",
			candidate:  "http://localhost:8080/callback/",
			want:       true,
		},
		{
			name:       "matching nested paths",
			registered: "http://localhost/oauth/v1/callback",
			candidate:  "http://localhost:3000/oauth/v1/callback",
			want:       true,
		},
		{
			name:       "matching query strings",
			registered: "http://localhost/callback?foo=bar",
			candidate:  "http://localhost:8080/callback?foo=bar",
			want:       true,
		},
		{
			name:       "matching complex query strings",
			registered: "http://localhost/callback?foo=bar&baz=qux",
			candidate:  "http://localhost:8080/callback?foo=bar&baz=qux",
			want:       true,
		},
		{
			name:       "empty path matches",
			registered: "http://localhost",
			candidate:  "http://localhost:8080",
			want:       true,
		},
		{
			name:       "case insensitive localhost",
			registered: "http://localhost/callback",
			candidate:  "http://LOCALHOST:8080/callback",
			want:       true,
		},
		{
			name:       "case insensitive localhost (mixed case registered)",
			registered: "http://LocalHost/callback",
			candidate:  "http://localhost:8080/callback",
			want:       true,
		},

		// Security validations: scheme restrictions
		{
			name:       "https registered rejected",
			registered: "https://localhost/callback",
			candidate:  "https://localhost:8080/callback",
			want:       false,
		},
		{
			name:       "https candidate rejected",
			registered: "http://localhost/callback",
			candidate:  "https://localhost:8080/callback",
			want:       false,
		},
		{
			name:       "custom scheme rejected",
			registered: "myapp://localhost/callback",
			candidate:  "myapp://localhost:8080/callback",
			want:       false,
		},

		// Security validations: host must be loopback
		{
			name:       "non-loopback host rejected (registered)",
			registered: "http://example.com/callback",
			candidate:  "http://example.com:8080/callback",
			want:       false,
		},
		{
			name:       "non-loopback host rejected (candidate)",
			registered: "http://localhost/callback",
			candidate:  "http://example.com:8080/callback",
			want:       false,
		},
		{
			name:       "192.168.x.x private IP rejected",
			registered: "http://192.168.1.1/callback",
			candidate:  "http://192.168.1.1:8080/callback",
			want:       false,
		},
		{
			name:       "10.x.x.x private IP rejected",
			registered: "http://10.0.0.1/callback",
			candidate:  "http://10.0.0.1:8080/callback",
			want:       false,
		},
		{
			name:       "0.0.0.0 rejected",
			registered: "http://0.0.0.0/callback",
			candidate:  "http://0.0.0.0:8080/callback",
			want:       false,
		},

		// Security validations: host must match exactly
		{
			name:       "127.0.0.1 does not match localhost",
			registered: "http://127.0.0.1/callback",
			candidate:  "http://localhost:8080/callback",
			want:       false,
		},
		{
			name:       "localhost does not match 127.0.0.1",
			registered: "http://localhost/callback",
			candidate:  "http://127.0.0.1:8080/callback",
			want:       false,
		},
		{
			name:       "127.0.0.1 does not match ::1",
			registered: "http://127.0.0.1/callback",
			candidate:  "http://[::1]:8080/callback",
			want:       false,
		},
		{
			name:       "::1 does not match localhost",
			registered: "http://[::1]/callback",
			candidate:  "http://localhost:8080/callback",
			want:       false,
		},

		// Port handling: registered URI with explicit port rejects exemption
		{
			name:       "registered with port requires exact match (loopback exemption disabled)",
			registered: "http://localhost:3000/callback",
			candidate:  "http://localhost:8080/callback",
			want:       false,
		},
		{
			name:       "registered port 80 requires exact match",
			registered: "http://localhost:80/callback",
			candidate:  "http://localhost:8080/callback",
			want:       false,
		},
		{
			name:       "registered with port rejects missing port in candidate",
			registered: "http://localhost:3000/callback",
			candidate:  "http://localhost/callback",
			want:       false,
		},

		// Path mismatch validations
		{
			name:       "different paths rejected",
			registered: "http://localhost/callback",
			candidate:  "http://localhost:8080/different",
			want:       false,
		},
		{
			name:       "path with trailing slash vs without",
			registered: "http://localhost/callback",
			candidate:  "http://localhost:8080/callback/",
			want:       false,
		},
		{
			name:       "nested path mismatch",
			registered: "http://localhost/oauth/callback",
			candidate:  "http://localhost:8080/callback",
			want:       false,
		},
		{
			name:       "case sensitive paths",
			registered: "http://localhost/callback",
			candidate:  "http://localhost:8080/Callback",
			want:       false,
		},

		// Query string validations
		{
			name:       "missing query string in candidate",
			registered: "http://localhost/callback?foo=bar",
			candidate:  "http://localhost:8080/callback",
			want:       false,
		},
		{
			name:       "extra query string in candidate",
			registered: "http://localhost/callback",
			candidate:  "http://localhost:8080/callback?foo=bar",
			want:       false,
		},
		{
			name:       "different query parameter values",
			registered: "http://localhost/callback?foo=bar",
			candidate:  "http://localhost:8080/callback?foo=baz",
			want:       false,
		},
		{
			name:       "query parameter order matters (RawQuery comparison)",
			registered: "http://localhost/callback?foo=bar&baz=qux",
			candidate:  "http://localhost:8080/callback?baz=qux&foo=bar",
			want:       false,
		},
		{
			name:       "query string encoding preserved",
			registered: "http://localhost/callback?redirect_uri=https%3A%2F%2Fexample.com",
			candidate:  "http://localhost:8080/callback?redirect_uri=https%3A%2F%2Fexample.com",
			want:       true,
		},
		{
			name:       "query string encoding mismatch",
			registered: "http://localhost/callback?redirect_uri=https%3A%2F%2Fexample.com",
			candidate:  "http://localhost:8080/callback?redirect_uri=https://example.com",
			want:       false,
		},

		// Edge cases: malformed URIs
		{
			name:       "malformed registered URI",
			registered: "://localhost/callback",
			candidate:  "http://localhost:8080/callback",
			want:       false,
		},
		{
			name:       "malformed candidate URI",
			registered: "http://localhost/callback",
			candidate:  "://localhost:8080/callback",
			want:       false,
		},
		{
			name:       "empty registered URI",
			registered: "",
			candidate:  "http://localhost:8080/callback",
			want:       false,
		},
		{
			name:       "empty candidate URI",
			registered: "http://localhost/callback",
			candidate:  "",
			want:       false,
		},
		{
			name:       "both URIs empty",
			registered: "",
			candidate:  "",
			want:       false,
		},
		{
			name:       "registered URI with invalid characters",
			registered: "http://local host/callback",
			candidate:  "http://localhost:8080/callback",
			want:       false,
		},
		{
			name:       "candidate URI with invalid characters",
			registered: "http://localhost/callback",
			candidate:  "http://local host:8080/callback",
			want:       false,
		},

		// Edge cases: IPv6 normalization
		{
			name:       "IPv6 without brackets in registered (url.Parse handles this)",
			registered: "http://::1/callback",
			candidate:  "http://[::1]:8080/callback",
			want:       true,
		},
		{
			name:       "IPv6 with brackets in both",
			registered: "http://[::1]/callback",
			candidate:  "http://[::1]:8080/callback",
			want:       true,
		},
		{
			name:       "IPv6 full form vs compressed",
			registered: "http://[0000:0000:0000:0000:0000:0000:0000:0001]/callback",
			candidate:  "http://[::1]:8080/callback",
			want:       true,
		},

		// Edge cases: port edge values
		{
			name:       "candidate port 1",
			registered: "http://localhost/callback",
			candidate:  "http://localhost:1/callback",
			want:       true,
		},
		{
			name:       "candidate invalid port rejected by url.Parse",
			registered: "http://localhost/callback",
			candidate:  "http://localhost:999999/callback",
			want:       false,
		},

		// Edge cases: fragment identifiers (not part of redirect_uri comparison per OAuth 2.0)
		{
			name:       "fragment in registered ignored",
			registered: "http://localhost/callback#fragment",
			candidate:  "http://localhost:8080/callback",
			want:       false, // Fragment becomes part of path comparison
		},
		{
			name:       "fragment in candidate ignored",
			registered: "http://localhost/callback",
			candidate:  "http://localhost:8080/callback#fragment",
			want:       false, // Fragment becomes part of path comparison
		},

		// Edge cases: relative paths
		{
			name:       "registered relative URI rejected by url.Parse scheme check",
			registered: "/callback",
			candidate:  "http://localhost:8080/callback",
			want:       false,
		},
		{
			name:       "candidate relative URI rejected by url.Parse scheme check",
			registered: "http://localhost/callback",
			candidate:  "/callback",
			want:       false,
		},

		// Additional loopback address variations
		{
			name:       "127.0.0.2 rejected (only 127.0.0.1 is loopback)",
			registered: "http://127.0.0.2/callback",
			candidate:  "http://127.0.0.2:8080/callback",
			want:       false,
		},
		{
			name:       "::2 rejected (only ::1 is loopback)",
			registered: "http://[::2]/callback",
			candidate:  "http://[::2]:8080/callback",
			want:       false,
		},

		// Path and query combinations
		{
			name:       "matching path and query",
			registered: "http://localhost/callback?state=xyz",
			candidate:  "http://localhost:8080/callback?state=xyz",
			want:       true,
		},
		{
			name:       "matching path, different query",
			registered: "http://localhost/callback?state=xyz",
			candidate:  "http://localhost:8080/callback?state=abc",
			want:       false,
		},
		{
			name:       "root path with query",
			registered: "http://localhost?foo=bar",
			candidate:  "http://localhost:8080?foo=bar",
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isLoopbackRedirect(tt.registered, tt.candidate)
			if got != tt.want {
				t.Errorf("isLoopbackRedirect(%q, %q) = %v, want %v",
					tt.registered, tt.candidate, got, tt.want)
			}
		})
	}
}

// TestIsLoopbackRedirectSecurityContract validates the security guarantees
// documented in the function contract
func TestIsLoopbackRedirectSecurityContract(t *testing.T) {
	t.Run("never allows https", func(t *testing.T) {
		httpsURIs := []string{
			"https://localhost/callback",
			"https://127.0.0.1/callback",
			"https://[::1]/callback",
		}
		for _, registered := range httpsURIs {
			for _, candidate := range httpsURIs {
				if isLoopbackRedirect(registered, candidate) {
					t.Errorf("https should never be allowed: registered=%q, candidate=%q",
						registered, candidate)
				}
			}
		}
	})

	t.Run("registered URI with port disables exemption", func(t *testing.T) {
		registeredWithPort := "http://localhost:3000/callback"
		candidatesWithDifferentPorts := []string{
			"http://localhost:8080/callback",
			"http://localhost:80/callback",
			"http://localhost:443/callback",
			"http://localhost/callback", // Even missing port fails
		}
		for _, candidate := range candidatesWithDifferentPorts {
			if isLoopbackRedirect(registeredWithPort, candidate) {
				t.Errorf("registered URI with explicit port should reject all candidates with different ports: candidate=%q",
					candidate)
			}
		}
	})

	t.Run("only loopback addresses allowed", func(t *testing.T) {
		nonLoopbackAddresses := []string{
			"http://example.com/callback",
			"http://192.168.1.1/callback",
			"http://10.0.0.1/callback",
			"http://0.0.0.0/callback",
			"http://8.8.8.8/callback",
			"http://[2001:db8::1]/callback",
		}
		for _, nonLoopback := range nonLoopbackAddresses {
			if isLoopbackRedirect(nonLoopback, "http://localhost:8080/callback") {
				t.Errorf("non-loopback address should be rejected as registered: %q", nonLoopback)
			}
			if isLoopbackRedirect("http://localhost/callback", nonLoopback) {
				t.Errorf("non-loopback address should be rejected as candidate: %q", nonLoopback)
			}
		}
	})

	t.Run("hosts must match exactly after normalization", func(t *testing.T) {
		mismatchPairs := []struct {
			registered string
			candidate  string
		}{
			{"http://127.0.0.1/callback", "http://localhost:8080/callback"},
			{"http://localhost/callback", "http://127.0.0.1:8080/callback"},
			{"http://127.0.0.1/callback", "http://[::1]:8080/callback"},
			{"http://[::1]/callback", "http://localhost:8080/callback"},
		}
		for _, pair := range mismatchPairs {
			if isLoopbackRedirect(pair.registered, pair.candidate) {
				t.Errorf("different loopback hosts should not match: registered=%q, candidate=%q",
					pair.registered, pair.candidate)
			}
		}
	})

	t.Run("path and query must match exactly", func(t *testing.T) {
		base := "http://localhost/callback"
		mismatchCandidates := []string{
			"http://localhost:8080/different",
			"http://localhost:8080/callback/",
			"http://localhost:8080/callback?extra=param",
			"http://localhost:8080/Callback", // Case sensitive
		}
		for _, candidate := range mismatchCandidates {
			if isLoopbackRedirect(base, candidate) {
				t.Errorf("path/query mismatch should be rejected: candidate=%q", candidate)
			}
		}
	})
}

// TestIsLoopbackRedirectRFC8252Compliance validates compliance with RFC 8252 Section 7.3
func TestIsLoopbackRedirectRFC8252Compliance(t *testing.T) {
	t.Run("RFC 8252 example: dynamic port binding", func(t *testing.T) {
		// RFC 8252 Section 7.3: "the authorization server MUST allow any port to be specified"
		// when the redirect URI uses the loopback interface
		registered := "http://127.0.0.1/redirect"
		validCandidates := []string{
			"http://127.0.0.1:8080/redirect",
			"http://127.0.0.1:61024/redirect", // Ephemeral port
			"http://127.0.0.1:1/redirect",     // Min port
			"http://127.0.0.1:65535/redirect", // Max port
		}
		for _, candidate := range validCandidates {
			if !isLoopbackRedirect(registered, candidate) {
				t.Errorf("RFC 8252 dynamic port binding should be allowed: registered=%q, candidate=%q",
					registered, candidate)
			}
		}
	})

	t.Run("RFC 8252: loopback addresses", func(t *testing.T) {
		// RFC 8252 Section 7.3: "the loopback IP address (127.0.0.1 for IPv4, ::1 for IPv6),
		// or the 'localhost' hostname"
		loopbackAddresses := []string{
			"http://127.0.0.1/callback",
			"http://[::1]/callback",
			"http://localhost/callback",
		}
		for _, registered := range loopbackAddresses {
			candidate := registered[:len(registered)-len("/callback")] + ":8080/callback"
			if !isLoopbackRedirect(registered, candidate) {
				t.Errorf("RFC 8252 loopback address should be allowed: registered=%q", registered)
			}
		}
	})
}
