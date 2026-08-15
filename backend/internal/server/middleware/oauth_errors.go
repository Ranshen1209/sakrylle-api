// Package middleware OAuth resource error helpers.
//
// See docs/OAUTH_V2_DESIGN.md §12.12: every OAuth-protected resource failure
// must emit a Bearer-compatible response with a `WWW-Authenticate` header
// listing required scopes (when applicable), `Cache-Control: no-store`,
// `Pragma: no-cache`, and a JSON body using RFC 6750 vocabulary
// (`invalid_token`, `insufficient_scope`).
//
// `api_key_auth.go` and the OAuth scope middleware MUST share this writer so
// the contract stays consistent across both authentication paths.
package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// OAuth resource error codes (RFC 6750 vocabulary). Granular Sakrylle reasons
// belong in `error_description`, not in the top-level `error` field.
const (
	OAuthErrInvalidToken      = "invalid_token"
	OAuthErrInsufficientScope = "insufficient_scope"
)

// OAuthResourceError is the inputs to writeOAuthResourceError.
//
//   - Status: HTTP status (typically 401 for invalid_token, 403 for insufficient_scope).
//   - Code: top-level OAuth error code (RFC 6750 vocabulary only).
//   - Description: human-readable detail; safe to leak; no plaintext token data.
//   - RequiredScopes: when Code=insufficient_scope, the scope list to advertise
//     in the `WWW-Authenticate: Bearer scope="..."` parameter (space-separated
//     per RFC 6750 §3.1).
//   - ResourceMetadataURL: optional RFC 9728 `resource_metadata` parameter; we
//     set it to the discovery URL so clients can re-derive the issuer config
//     after a 401.
type OAuthResourceError struct {
	Status              int
	Code                string
	Description         string
	RequiredScopes      []string
	ResourceMetadataURL string
}

// WriteOAuthResourceError emits the §12.12 contract. Idempotent against the
// gin response writer (caller MUST also call c.Abort()).
func WriteOAuthResourceError(c *gin.Context, e OAuthResourceError) {
	if c == nil {
		return
	}
	if e.Status == 0 {
		e.Status = http.StatusUnauthorized
	}
	if e.Code == "" {
		e.Code = OAuthErrInvalidToken
	}

	auth := buildBearerChallenge(e.Code, e.Description, e.RequiredScopes, e.ResourceMetadataURL)
	c.Header("WWW-Authenticate", auth)
	c.Header("Content-Type", "application/json")
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")

	body := gin.H{"error": e.Code}
	if e.Description != "" {
		body["error_description"] = e.Description
	}
	c.AbortWithStatusJSON(e.Status, body)
}

// buildBearerChallenge formats a `WWW-Authenticate: Bearer ...` value per RFC
// 6750 §3.1, with `scope="..."` only when scopes are present (the RFC requires
// `scope` to appear ONLY for insufficient_scope challenges, but emitting it
// when supplied is harmless and matches the §12.12 example).
func buildBearerChallenge(errorCode, description string, scopes []string, resourceMetadata string) string {
	var b strings.Builder
	_, _ = b.WriteString("Bearer error=")
	_, _ = b.WriteString(quoteBearerParam(errorCode))
	if description != "" {
		_, _ = b.WriteString(", error_description=")
		_, _ = b.WriteString(quoteBearerParam(description))
	}
	if len(scopes) > 0 {
		_, _ = b.WriteString(", scope=")
		_, _ = b.WriteString(quoteBearerParam(strings.Join(scopes, " ")))
	}
	if resourceMetadata != "" {
		_, _ = b.WriteString(", resource_metadata=")
		_, _ = b.WriteString(quoteBearerParam(resourceMetadata))
	}
	return b.String()
}

// quoteBearerParam wraps v in double quotes and escapes embedded `"` and `\`
// characters per RFC 7235 §2.2 quoted-string ABNF. The bearer auth-scheme uses
// quoted-string for all parameter values.
func quoteBearerParam(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(v) + `"`
}
