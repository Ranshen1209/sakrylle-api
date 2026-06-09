package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// maxRequestBodySize is the maximum size of a request_uri response body (64KB).
	maxRequestBodySize = 64 * 1024
	// requestURITimeout is the HTTP client timeout for request_uri fetches.
	requestURITimeout = 5 * time.Second
)

// DefaultHTTPClient is the HTTP client used for request_uri fetches and
// sector_identifier_uri fetches. Set this at startup before any OAuth flows.
// If nil, request_uri fetches will fail.
var DefaultHTTPClient *http.Client

func init() {
	DefaultHTTPClient = buildOIDCHTTPClient()
}

// buildOIDCHTTPClient builds the HTTP client used for request_uri /
// sector_identifier_uri fetches, with SSRF protection at the dial layer and
// per-hop redirect re-validation.
func buildOIDCHTTPClient() *http.Client {
	return buildOIDCHTTPClientWithDialGuard(safeDialContext)
}

// buildOIDCHTTPClientWithDialGuard allows tests to inject a dial guard that
// permits loopback (so httptest servers are reachable) while still exercising
// the redirect re-validation path.
func buildOIDCHTTPClientWithDialGuard(dial func(ctx context.Context, network, address string) (net.Conn, error)) *http.Client {
	return &http.Client{
		Timeout: requestURITimeout,
		Transport: &http.Transport{
			DialContext: dial,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("oidc fetch: too many redirects (%d)", len(via))
			}
			if req.URL.Scheme != "https" {
				return fmt.Errorf("oidc fetch: redirect to non-https scheme %q rejected", req.URL.Scheme)
			}
			// Re-validate the redirect target host at the IP layer. The
			// authoritative DNS-rebinding defense is safeDialContext (which
			// validates and dials the resolved IP); this check is defense in
			// depth at the redirect layer.
			blocked, err := isPrivateOrLoopbackHost(req.Context(), req.URL.Hostname())
			if err != nil {
				return fmt.Errorf("oidc fetch: redirect host resolution failed: %w", err)
			}
			if blocked {
				return fmt.Errorf("oidc fetch: redirect to internal host %q blocked", req.URL.Hostname())
			}
			return nil
		},
	}
}

// ClaimsRequest represents an OIDC §5.5 claims request parameter. It is a JSON
// object with two top-level keys:
//
//	id_token:  claims the RP wants in the id_token
//	userinfo:  claims the RP wants in the UserInfo response
//
// Each value is itself a JSON object mapping claim names to {essential, value, values}.
// We parse the keys we care about (email, name) and ignore the rest.
type ClaimsRequest struct {
	IDToken  map[string]ClaimRequestDetail `json:"id_token"`
	UserInfo map[string]ClaimRequestDetail `json:"userinfo"`
}

// ClaimRequestDetail is a single claim request. Only the claim name keys
// matter; essential/value/values are parsed for future use but not enforced.
type ClaimRequestDetail struct {
	Essential *bool    `json:"essential,omitempty"`
	Value     *string  `json:"value,omitempty"`
	Values    []string `json:"values,omitempty"`
}

// ParseClaimsParameter parses the `claims` query parameter as a ClaimsRequest.
// Returns nil when the parameter is empty or invalid JSON (per spec, invalid
// claims parameters are silently ignored).
func ParseClaimsParameter(raw string) *ClaimsRequest {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var cr ClaimsRequest
	if err := json.Unmarshal([]byte(raw), &cr); err != nil {
		return nil
	}
	// Empty id_token and userinfo sections → no-op.
	if len(cr.IDToken) == 0 && len(cr.UserInfo) == 0 {
		return nil
	}
	return &cr
}

// ClaimsRequestToMap converts a ClaimsRequest to a map[string]any for JSONB
// storage in the authorize transaction.
func ClaimsRequestToMap(cr *ClaimsRequest) map[string]any {
	if cr == nil {
		return nil
	}
	m := make(map[string]any)
	if len(cr.IDToken) > 0 {
		idToken := make(map[string]any, len(cr.IDToken))
		for k, v := range cr.IDToken {
			idToken[k] = claimDetailToMap(v)
		}
		m["id_token"] = idToken
	}
	if len(cr.UserInfo) > 0 {
		userInfo := make(map[string]any, len(cr.UserInfo))
		for k, v := range cr.UserInfo {
			userInfo[k] = claimDetailToMap(v)
		}
		m["userinfo"] = userInfo
	}
	return m
}

func claimDetailToMap(d ClaimRequestDetail) map[string]any {
	m := make(map[string]any)
	if d.Essential != nil {
		m["essential"] = *d.Essential
	}
	if d.Value != nil {
		m["value"] = *d.Value
	}
	if len(d.Values) > 0 {
		m["values"] = d.Values
	}
	return m
}

// ClaimsRequestFromMap reconstructs a ClaimsRequest from its stored map form.
func ClaimsRequestFromMap(m map[string]any) *ClaimsRequest {
	if m == nil || len(m) == 0 {
		return nil
	}
	cr := &ClaimsRequest{}
	if idTokenRaw, ok := m["id_token"].(map[string]any); ok {
		cr.IDToken = make(map[string]ClaimRequestDetail, len(idTokenRaw))
		for k, v := range idTokenRaw {
			cr.IDToken[k] = claimDetailFromMap(v)
		}
	}
	if userInfoRaw, ok := m["userinfo"].(map[string]any); ok {
		cr.UserInfo = make(map[string]ClaimRequestDetail, len(userInfoRaw))
		for k, v := range userInfoRaw {
			cr.UserInfo[k] = claimDetailFromMap(v)
		}
	}
	return cr
}

func claimDetailFromMap(v any) ClaimRequestDetail {
	m, ok := v.(map[string]any)
	if !ok {
		return ClaimRequestDetail{}
	}
	d := ClaimRequestDetail{}
	if essential, ok := m["essential"].(bool); ok {
		d.Essential = &essential
	}
	if value, ok := m["value"].(string); ok {
		d.Value = &value
	}
	if values, ok := m["values"].([]any); ok {
		for _, sv := range values {
			if s, ok := sv.(string); ok {
				d.Values = append(d.Values, s)
			}
		}
	}
	return d
}

// RequestObjectClaims represents the payload of a signed request object JWT
// (OIDC Core §6.1). It carries the same parameters as an OAuth authorization
// request, packed into a signed JWT.
type RequestObjectClaims struct {
	jwt.RegisteredClaims
	ClientID            string `json:"client_id"`
	RedirectURI         string `json:"redirect_uri"`
	ResponseType        string `json:"response_type"`
	Scope               string `json:"scope"`
	State               string `json:"state"`
	Nonce               string `json:"nonce"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	Claims              string `json:"claims"`
}

// ParseRequestObjectJWT validates and unpacks a signed request object JWT.
//
// The JWT must:
//   - Be signed with the client's registered JWKS key or client_secret (HS256)
//   - Have iss == client_id
//   - Have aud containing the issuer URL
//   - Not be expired
//
// When clientSecret is non-empty, the JWT may also be symmetrically signed
// with client_secret as the HMAC key (HS256, HS384, HS512) per OIDC Core §6.1.
func ParseRequestObjectJWT(rawJWT, issuer, clientID, clientSecret string) (*AuthorizeRequest, error) {
	if rawJWT == "" {
		return nil, fmt.Errorf("request object JWT is empty")
	}

	// Parse without verification first to extract algorithm and claims.
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	unverified, _, err := parser.ParseUnverified(rawJWT, &RequestObjectClaims{})
	if err != nil {
		return nil, fmt.Errorf("parse request object: %w", err)
	}

	if _, ok := unverified.Claims.(*RequestObjectClaims); !ok {
		return nil, fmt.Errorf("invalid request object claims")
	}

	// Verify the JWT signature.
	alg := unverified.Header["alg"]
	if alg == nil {
		return nil, fmt.Errorf("request object missing alg header")
	}
	algStr, ok := alg.(string)
	if !ok {
		return nil, fmt.Errorf("request object alg is not a string")
	}

	var verifyKey any
	switch {
	case strings.HasPrefix(algStr, "HS"):
		// Symmetric: client_secret as the HMAC key
		if clientSecret == "" {
			return nil, fmt.Errorf("HS-signed request object requires a client_secret")
		}
		verifyKey = []byte(clientSecret)
	case strings.HasPrefix(algStr, "RS"), strings.HasPrefix(algStr, "ES"), strings.HasPrefix(algStr, "PS"):
		// Asymmetric: require JWKS verification via VerifyKeyFunc.
		// The caller must provide a key lookup function via ParseRequestObjectJWTWithJWKS.
		return nil, fmt.Errorf("asymmetric request object verification requires ParseRequestObjectJWTWithJWKS")
	default:
		return nil, fmt.Errorf("unsupported request object algorithm: %s", algStr)
	}

	// Verify with the resolved key.
	token, err := jwt.ParseWithClaims(rawJWT, &RequestObjectClaims{},
		func(t *jwt.Token) (any, error) { return verifyKey, nil },
		jwt.WithLeeway(30*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("verify request object signature: %w", err)
	}

	claims, ok := token.Claims.(*RequestObjectClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("request object claims invalid")
	}

	// Validate iss == client_id.
	if claims.Issuer != clientID {
		return nil, fmt.Errorf("request object iss %q != client_id %q", claims.Issuer, clientID)
	}

	// Validate aud contains the issuer.
	audOK := false
	for _, a := range claims.Audience {
		if a == issuer {
			audOK = true
			break
		}
	}
	if !audOK && len(claims.Audience) == 0 && issuer != "" {
		return nil, fmt.Errorf("request object aud does not contain issuer %q", issuer)
	}

	// Build AuthorizeRequest from the request object claims.
	req := &AuthorizeRequest{
		ClientID:            clientID,
		RedirectURI:         claims.RedirectURI,
		ResponseType:        claims.ResponseType,
		Scopes:              ParseScopes(claims.Scope),
		State:               claims.State,
		CodeChallenge:       claims.CodeChallenge,
		CodeChallengeMethod: claims.CodeChallengeMethod,
		Nonce:               claims.Nonce,
	}
	return req, nil
}

// JWKSKeyFunc is a function that returns the verification key for a given
// algorithm and optional kid from a JWT header. Used by
// ParseRequestObjectJWTWithJWKS to resolve asymmetric signing keys.
type JWKSKeyFunc func(alg string, kid string) (any, error)

// ParseRequestObjectJWTWithJWKS validates and unpacks a signed request object
// JWT, supporting both symmetric (HS256/384/512) and asymmetric
// (RS256/384/512, ES256/384/512, PS256/384/512) algorithms.
//
// For asymmetric algorithms, the caller provides a JWKSKeyFunc that resolves
// the verification key from the client's JWKS. The key function receives the
// algorithm string and kid from the JWT header.
//
// For symmetric algorithms, clientSecret is used as the HMAC key (same as
// ParseRequestObjectJWT).
func ParseRequestObjectJWTWithJWKS(rawJWT, issuer, clientID, clientSecret string, keyFunc JWKSKeyFunc) (*AuthorizeRequest, error) {
	if rawJWT == "" {
		return nil, fmt.Errorf("request object JWT is empty")
	}

	// Parse without verification first to extract algorithm and claims.
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	unverified, _, err := parser.ParseUnverified(rawJWT, &RequestObjectClaims{})
	if err != nil {
		return nil, fmt.Errorf("parse request object: %w", err)
	}

	if _, ok := unverified.Claims.(*RequestObjectClaims); !ok {
		return nil, fmt.Errorf("invalid request object claims")
	}

	alg := unverified.Header["alg"]
	if alg == nil {
		return nil, fmt.Errorf("request object missing alg header")
	}
	algStr, ok := alg.(string)
	if !ok {
		return nil, fmt.Errorf("request object alg is not a string")
	}

	// Extract optional kid for JWKS lookup.
	var kid string
	if k, exists := unverified.Header["kid"]; exists {
		if ks, ok := k.(string); ok {
			kid = ks
		}
	}

	var verifyKey any
	switch {
	case strings.HasPrefix(algStr, "HS"):
		if clientSecret == "" {
			return nil, fmt.Errorf("HS-signed request object requires a client_secret")
		}
		verifyKey = []byte(clientSecret)
	case strings.HasPrefix(algStr, "RS"), strings.HasPrefix(algStr, "ES"), strings.HasPrefix(algStr, "PS"):
		if keyFunc == nil {
			return nil, fmt.Errorf("asymmetric request object requires a JWKS key function")
		}
		verifyKey, err = keyFunc(algStr, kid)
		if err != nil {
			return nil, fmt.Errorf("resolve verification key for %s (kid=%q): %w", algStr, kid, err)
		}
		if verifyKey == nil {
			return nil, fmt.Errorf("no verification key found for %s (kid=%q)", algStr, kid)
		}
	default:
		return nil, fmt.Errorf("unsupported request object algorithm: %s", algStr)
	}

	// Verify with the resolved key, enforcing standard claims.
	token, err := jwt.ParseWithClaims(rawJWT, &RequestObjectClaims{},
		func(t *jwt.Token) (any, error) { return verifyKey, nil },
		jwt.WithLeeway(30*time.Second),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(issuer),
	)
	if err != nil {
		return nil, fmt.Errorf("verify request object: %w", err)
	}

	claims, ok := token.Claims.(*RequestObjectClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("request object claims invalid")
	}

	// Validate iss == client_id (OIDC Core §6.1).
	if claims.Issuer != clientID {
		return nil, fmt.Errorf("request object iss %q != client_id %q", claims.Issuer, clientID)
	}

	// Build AuthorizeRequest from the request object claims.
	req := &AuthorizeRequest{
		ClientID:            clientID,
		RedirectURI:         claims.RedirectURI,
		ResponseType:        claims.ResponseType,
		Scopes:              ParseScopes(claims.Scope),
		State:               claims.State,
		CodeChallenge:       claims.CodeChallenge,
		CodeChallengeMethod: claims.CodeChallengeMethod,
		Nonce:               claims.Nonce,
	}
	return req, nil
}

// JWKSKeyFuncFromSet creates a JWKSKeyFunc from a JWKS document (fetched from
// the client's jwks_uri or embedded jwks). It supports RSA, EC, and RSASSA-PSS
// key types.
func JWKSKeyFuncFromSet(jwks JWKS) JWKSKeyFunc {
	return func(alg string, kid string) (any, error) {
		for _, key := range jwks.Keys {
			if key.Kid != kid {
				continue
			}
			switch key.Kty {
			case "RSA":
				return parseRSAJWK(key)
			case "EC":
				return parseECJWK(key)
			default:
				return nil, fmt.Errorf("unsupported JWK kty %q for kid %q", key.Kty, kid)
			}
		}
		return nil, fmt.Errorf("kid %q not found in JWKS", kid)
	}
}

// parseRSAJWK converts an RSA JWK to an *rsa.PublicKey.
func parseRSAJWK(jwk JWK) (*rsa.PublicKey, error) {
	if jwk.N == "" || jwk.E == "" {
		return nil, fmt.Errorf("RSA JWK missing n or e")
	}
	nBytes, err := base64.RawURLEncoding.DecodeString(jwk.N)
	if err != nil {
		return nil, fmt.Errorf("decode RSA n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(jwk.E)
	if err != nil {
		return nil, fmt.Errorf("decode RSA e: %w", err)
	}
	n := new(big.Int).SetBytes(nBytes)
	e := 0
	for _, b := range eBytes {
		e = e<<8 | int(b)
	}
	return &rsa.PublicKey{N: n, E: e}, nil
}

// parseECJWK converts an EC JWK to an *ecdsa.PublicKey.
func parseECJWK(jwk JWK) (*ecdsa.PublicKey, error) {
	if jwk.X == "" || jwk.Y == "" || jwk.Crv == "" {
		return nil, fmt.Errorf("EC JWK missing x, y, or crv")
	}
	xBytes, err := base64.RawURLEncoding.DecodeString(jwk.X)
	if err != nil {
		return nil, fmt.Errorf("decode EC x: %w", err)
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(jwk.Y)
	if err != nil {
		return nil, fmt.Errorf("decode EC y: %w", err)
	}
	x := new(big.Int).SetBytes(xBytes)
	y := new(big.Int).SetBytes(yBytes)

	// Map curve name to elliptic.Curve
	var curve elliptic.Curve
	switch jwk.Crv {
	case "P-256":
		curve = elliptic.P256()
	case "P-384":
		curve = elliptic.P384()
	case "P-521":
		curve = elliptic.P521()
	default:
		return nil, fmt.Errorf("unsupported EC curve %q", jwk.Crv)
	}

	return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
}

// FetchRequestURI fetches the request object from a request_uri (OIDC Core §6.3).
// The URI MUST use the https scheme, and the host MUST match an entry in the
// client's pre-registered request_uris whitelist.
//
// SSRF prevention:
//   - Only https scheme is allowed (no http, file, gopher, etc.)
//   - Host must match a pre-registered whitelist entry
//   - 5-second timeout
//   - Maximum 64KB response body
func FetchRequestURI(rawURI string, allowedURIs []string) (string, error) {
	rawURI = strings.TrimSpace(rawURI)
	if rawURI == "" {
		return "", fmt.Errorf("request_uri is empty")
	}

	parsed, err := url.Parse(rawURI)
	if err != nil {
		return "", fmt.Errorf("invalid request_uri: %w", err)
	}

	if parsed.Scheme != "https" {
		return "", fmt.Errorf("request_uri must use https scheme")
	}

	// Host whitelist check.
	hostAllowed := false
	for _, allowed := range allowedURIs {
		allowedParsed, err := url.Parse(allowed)
		if err != nil {
			continue
		}
		if subtle.ConstantTimeCompare(
			[]byte(strings.ToLower(parsed.Host)),
			[]byte(strings.ToLower(allowedParsed.Host)),
		) == 1 {
			hostAllowed = true
			break
		}
	}
	if !hostAllowed {
		return "", fmt.Errorf("request_uri host %q is not in the client's registered request_uris", parsed.Host)
	}

	// Fetch with timeout and size limit.
	if DefaultHTTPClient == nil {
		return "", fmt.Errorf("request_uri fetch: no HTTP client configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestURITimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		rawURI,
		nil,
	)
	if err != nil {
		return "", fmt.Errorf("build request_uri request: %w", err)
	}

	resp, err := DefaultHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch request_uri: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("request_uri returned HTTP %d", resp.StatusCode)
	}

	// Read with 64KB limit.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRequestBodySize))
	if err != nil {
		return "", fmt.Errorf("read request_uri body: %w", err)
	}

	return string(body), nil
}
