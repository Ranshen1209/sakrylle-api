package handler_test

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOIDCDiscovery verifies /.well-known/openid-configuration returns valid OIDC metadata
// per OpenID Connect Discovery 1.0 §3.
func TestOIDCDiscovery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := setupTestOAuthHandler(t, true) // with OIDC enabled

	tests := []struct {
		name       string
		oidcWired  bool
		wantStatus int
		wantError  bool
	}{
		{
			name:       "oidc enabled",
			oidcWired:  true,
			wantStatus: http.StatusOK,
			wantError:  false,
		},
		{
			name:       "oidc disabled",
			oidcWired:  false,
			wantStatus: http.StatusNotFound,
			wantError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.oidcWired {
				h.SetOIDCKeyService(nil) // disable OIDC
			}

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/.well-known/openid-configuration", nil)
			c.Request.Host = "sub.sakrylle.com"

			h.OpenIDConfiguration(c)

			assert.Equal(t, tt.wantStatus, w.Code)

			if !tt.wantError {
				var discovery map[string]any
				err := json.Unmarshal(w.Body.Bytes(), &discovery)
				require.NoError(t, err, "discovery must be valid JSON")

				// Required fields per OIDC Discovery §3
				requiredFields := []string{
					"issuer",
					"authorization_endpoint",
					"token_endpoint",
					"jwks_uri",
					"response_types_supported",
					"subject_types_supported",
					"id_token_signing_alg_values_supported",
				}

				for _, field := range requiredFields {
					assert.Contains(t, discovery, field, "missing required field: %s", field)
				}

				// Verify issuer value
				issuer, ok := discovery["issuer"].(string)
				require.True(t, ok, "issuer must be string")
				assert.Equal(t, "https://sub.sakrylle.com", issuer)

				// Verify algorithms
				algs, ok := discovery["id_token_signing_alg_values_supported"].([]any)
				require.True(t, ok, "algs must be array")
				assert.Contains(t, algs, "RS256", "must support RS256")

				// Verify subject_types
				subjectTypes, ok := discovery["subject_types_supported"].([]any)
				require.True(t, ok, "subject_types must be array")
				assert.Contains(t, subjectTypes, "public")

				// Verify scopes
				scopes, ok := discovery["scopes_supported"].([]any)
				require.True(t, ok, "scopes must be array")
				assert.Contains(t, scopes, "openid")
				assert.Contains(t, scopes, "profile")
				assert.Contains(t, scopes, "email")

				// Verify claims
				claims, ok := discovery["claims_supported"].([]any)
				require.True(t, ok, "claims must be array")
				requiredClaims := []string{"iss", "sub", "aud", "exp", "iat"}
				for _, claim := range requiredClaims {
					assert.Contains(t, claims, claim)
				}
			}
		})
	}
}

// TestJWKS verifies /.well-known/jwks.json returns valid JWKS per RFC 7517
func TestJWKS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := setupTestOAuthHandler(t, true)

	tests := []struct {
		name       string
		oidcWired  bool
		wantStatus int
		minKeys    int
	}{
		{
			name:       "jwks with keys",
			oidcWired:  true,
			wantStatus: http.StatusOK,
			minKeys:    1,
		},
		{
			name:       "jwks unavailable",
			oidcWired:  false,
			wantStatus: http.StatusServiceUnavailable,
			minKeys:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.oidcWired {
				h.SetOIDCKeyService(nil)
			}

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/.well-known/jwks.json", nil)

			h.JWKS(c)

			assert.Equal(t, tt.wantStatus, w.Code)

			if tt.minKeys > 0 {
				var jwks map[string]any
				err := json.Unmarshal(w.Body.Bytes(), &jwks)
				require.NoError(t, err, "jwks must be valid JSON")

				keys, ok := jwks["keys"].([]any)
				require.True(t, ok, "keys must be array")
				assert.GreaterOrEqual(t, len(keys), tt.minKeys)

				// Verify first key structure
				if len(keys) > 0 {
					key, ok := keys[0].(map[string]any)
					require.True(t, ok)
					assert.Contains(t, key, "kty", "key must have kty")
					assert.Contains(t, key, "kid", "key must have kid")
					assert.Contains(t, key, "alg", "key must have alg")
					assert.Contains(t, key, "use", "key must have use")

					// Verify no private components
					assert.NotContains(t, key, "d", "public JWKS must not contain private key component d")
				}

				// Verify Cache-Control header
				cacheControl := w.Header().Get("Cache-Control")
				assert.Contains(t, cacheControl, "max-age", "JWKS should be cacheable")
			}
		})
	}
}

// TestIDTokenIssuance verifies id_token is issued when openid scope is requested
func TestIDTokenIssuance(t *testing.T) {
	provider := setupTestProvider(t, true)

	// Create test client with OIDC scopes
	client := &service.OAuthClient{
		ClientID:      "test-client",
		AllowedScopes: []string{"openid", "profile", "email", "account:read"},
	}

	tests := []struct {
		name        string
		scopes      []string
		wantIDToken bool
		withNonce   bool
		expectedKid string
		expectedAlg string
	}{
		{
			name:        "openid scope - should issue id_token",
			scopes:      []string{"openid", "profile"},
			wantIDToken: true,
			withNonce:   true,
		},
		{
			name:        "no openid scope - no id_token",
			scopes:      []string{"account:read"},
			wantIDToken: false,
		},
		{
			name:        "openid only - minimal id_token",
			scopes:      []string{"openid"},
			wantIDToken: true,
			withNonce:   false,
		},
		{
			name:        "openid + email - includes email claims",
			scopes:      []string{"openid", "email"},
			wantIDToken: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Issue authorization code
			nonce := ""
			if tt.withNonce {
				nonce = randomString(32)
			}

			issued, err := provider.IssueAuthorizationCode(t.Context(), client, 1, &service.AuthorizeRequest{
				ClientID:     client.ClientID,
				RedirectURI:  "https://app.example.com/callback",
				ResponseType: "code",
				Scopes:       tt.scopes,
				State:        "test-state",
				Nonce:        nonce,
			})
			require.NoError(t, err)

			// Exchange code for tokens
			tokens, err := provider.ExchangeAuthorizationCode(t.Context(), client.ClientID, "", issued.Code, "https://app.example.com/callback", "")
			require.NoError(t, err)

			if tt.wantIDToken {
				assert.NotEmpty(t, tokens.IDToken, "id_token must be present when openid scope granted")

				// Decode and verify id_token structure (don't verify signature in unit test)
				parts := strings.Split(tokens.IDToken, ".")
				require.Equal(t, 3, len(parts), "id_token must have 3 parts (header.payload.signature)")

				// Decode header
				headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
				require.NoError(t, err)
				var header map[string]any
				err = json.Unmarshal(headerJSON, &header)
				require.NoError(t, err)

				// Verify header
				assert.Contains(t, []string{"RS256", "ES256"}, header["alg"], "algorithm must be RS256 or ES256")
				assert.Equal(t, "JWT", header["typ"])
				assert.NotEmpty(t, header["kid"], "kid must be present")

				// Decode payload
				payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
				require.NoError(t, err)
				var payload map[string]any
				err = json.Unmarshal(payloadJSON, &payload)
				require.NoError(t, err)

				// Verify required claims per OIDC Core §2
				assert.Equal(t, "https://sub.sakrylle.com", payload["iss"])
				assert.NotEmpty(t, payload["sub"])
				assert.NotEmpty(t, payload["aud"])
				assert.NotEmpty(t, payload["exp"])
				assert.NotEmpty(t, payload["iat"])

				// Verify nonce if provided
				if tt.withNonce {
					assert.Equal(t, nonce, payload["nonce"], "nonce must match request")
				}

				// Verify scope-gated claims
				if contains(tt.scopes, "profile") {
					assert.NotEmpty(t, payload["name"], "name claim required with profile scope")
				}

				if contains(tt.scopes, "email") {
					assert.NotEmpty(t, payload["email"], "email claim required with email scope")
					assert.Contains(t, payload, "email_verified")
				}

				// Verify forbidden claims are NOT present
				assert.NotContains(t, payload, "balance", "business claims forbidden in id_token")
				assert.NotContains(t, payload, "quota_used", "business claims forbidden in id_token")
				assert.NotContains(t, payload, "group_id", "business claims forbidden in id_token")

			} else {
				assert.Empty(t, tokens.IDToken, "id_token must be absent without openid scope")
			}
		})
	}
}

// TestNonceEcho verifies nonce is correctly echoed in id_token (OIDC Core §3.1.2.1)
func TestNonceEcho(t *testing.T) {
	provider := setupTestProvider(t, true)

	nonce := randomString(32)

	// Lookup test client
	client, err := provider.LookupClient(t.Context(), "test-client")
	require.NoError(t, err)

	// Issue code with nonce
	issued, err := provider.IssueAuthorizationCode(t.Context(), client, 1, &service.AuthorizeRequest{
		ClientID:     "test-client",
		RedirectURI:  "https://app.example.com/callback",
		ResponseType: "code",
		Scopes:       []string{"openid"},
		State:        "test-state",
		Nonce:        nonce,
	})
	require.NoError(t, err)

	// Exchange code
	tokens, err := provider.ExchangeAuthorizationCode(t.Context(), "test-client", "", issued.Code, "https://app.example.com/callback", "")
	require.NoError(t, err)
	require.NotEmpty(t, tokens.IDToken)

	// Decode id_token
	parts := strings.Split(tokens.IDToken, ".")
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)

	var payload map[string]any
	err = json.Unmarshal(payloadJSON, &payload)
	require.NoError(t, err)

	assert.Equal(t, nonce, payload["nonce"], "nonce must match exactly")
}

// TestUserInfoWithOpenID verifies /v1/me returns sub claim when openid scope granted
func TestUserInfoWithOpenID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := setupTestMeHandler(t)

	tests := []struct {
		name      string
		scopes    []string
		wantSub   bool
		wantEmail bool
		wantName  bool
	}{
		{
			name:      "openid scope - returns sub",
			scopes:    []string{"openid"},
			wantSub:   true,
			wantEmail: false,
			wantName:  false,
		},
		{
			name:      "openid + profile - returns sub and name",
			scopes:    []string{"openid", "profile"},
			wantSub:   true,
			wantName:  true,
			wantEmail: false,
		},
		{
			name:      "openid + email - returns sub and email",
			scopes:    []string{"openid", "email"},
			wantSub:   true,
			wantEmail: true,
			wantName:  false,
		},
		{
			name:      "no openid - no sub",
			scopes:    []string{"account:read"},
			wantSub:   false,
			wantEmail: false,
			wantName:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create access token with scopes
			token := createTestAccessToken(t, 1, tt.scopes)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/v1/me", nil)
			c.Request.Header.Set("Authorization", "Bearer "+token)

			// Inject user context (simulating JWT middleware)
			c.Set("user_id", int64(1))
			c.Set("scopes", tt.scopes)

			h.GetCurrentUser(c)

			assert.Equal(t, http.StatusOK, w.Code)

			var response map[string]any
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			if tt.wantSub {
				assert.Contains(t, response, "sub")
				assert.Equal(t, "1", response["sub"], "sub must be string user_id")
			} else {
				assert.NotContains(t, response, "sub")
			}

			if tt.wantName {
				assert.Contains(t, response, "name")
			}

			if tt.wantEmail {
				assert.Contains(t, response, "email")
			}
		})
	}
}

// TestLogoutFlow verifies RP-Initiated Logout (OIDC Session Management §5)
func TestLogoutFlow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := setupTestOAuthHandler(t, true)

	validIDToken := createTestIDToken(t, "test-client", 1)
	validPostLogoutURI := "https://app.example.com/"

	tests := []struct {
		name               string
		idTokenHint        string
		postLogoutURI      string
		state              string
		clientHasWhitelist bool
		wantStatus         int
		wantRedirect       bool
		wantStateInURL     bool
	}{
		{
			name:               "valid logout with redirect",
			idTokenHint:        validIDToken,
			postLogoutURI:      validPostLogoutURI,
			state:              "test-state-123",
			clientHasWhitelist: true,
			wantStatus:         http.StatusFound,
			wantRedirect:       true,
			wantStateInURL:     true,
		},
		{
			name:          "no post_logout_redirect_uri - success page",
			idTokenHint:   validIDToken,
			postLogoutURI: "",
			wantStatus:    http.StatusOK,
			wantRedirect:  false,
		},
		{
			name:               "post_logout_uri not whitelisted - error",
			idTokenHint:        validIDToken,
			postLogoutURI:      "https://evil.com/",
			clientHasWhitelist: true,
			wantStatus:         http.StatusBadRequest,
			wantRedirect:       false,
		},
		{
			name:          "no id_token_hint with redirect - error",
			idTokenHint:   "",
			postLogoutURI: validPostLogoutURI,
			wantStatus:    http.StatusBadRequest,
			wantRedirect:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup client whitelist if needed
			if tt.clientHasWhitelist {
				setupLogoutWhitelist(t, "test-client", []string{validPostLogoutURI})
			}

			// Build request
			reqURL := "/oauth/logout?"
			if tt.idTokenHint != "" {
				reqURL += "id_token_hint=" + url.QueryEscape(tt.idTokenHint) + "&"
			}
			if tt.postLogoutURI != "" {
				reqURL += "post_logout_redirect_uri=" + url.QueryEscape(tt.postLogoutURI) + "&"
			}
			if tt.state != "" {
				reqURL += "state=" + url.QueryEscape(tt.state)
			}

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", reqURL, nil)

			h.Logout(c)

			assert.Equal(t, tt.wantStatus, w.Code)

			if tt.wantRedirect {
				location := w.Header().Get("Location")
				assert.NotEmpty(t, location, "redirect must have Location header")
				assert.Contains(t, location, tt.postLogoutURI)

				if tt.wantStateInURL {
					assert.Contains(t, location, "state="+tt.state)
				}
			} else if tt.wantStatus == http.StatusOK {
				// Success page should be HTML
				assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
				assert.Contains(t, w.Body.String(), "登出成功")
			}
		})
	}
}

// TestPromptNone verifies prompt=none silent authentication (OIDC Core §3.1.2.1)
func TestPromptNone(t *testing.T) {
	t.Skip("OAuth handler test wiring not implemented")
}

// Helper functions

func setupTestOAuthHandler(t *testing.T, withOIDC bool) *handler.OAuthProviderHandler {
	provider := setupTestProvider(t, withOIDC)
	h := handler.NewOAuthProviderHandler(provider, nil)

	if withOIDC {
		oidcKeys := setupTestOIDCKeys(t)
		h.SetOIDCKeyService(oidcKeys)

		authService := setupTestAuthService(t)
		h.SetAuthService(authService)
	}

	return h
}

func setupTestProvider(t *testing.T, withOIDC bool) *service.OAuthProviderService {
	// Skeleton: full provider wiring (testcontainers/mocks) was never implemented.
	// The OIDC behaviors these tests target are covered by runnable tests:
	// discovery/JWKS in oidc_provider_handler_test.go (TestOpenIDConfiguration,
	// TestJWKSEndpoint), /v1/me scope-cropping in oauth_provider_account_handler_test.go
	// (TestMe_OAuth*), and id_token signing in service/oidc_token_wiring_test.go.
	t.Skip("OAuthProviderService test wiring not implemented; see runnable OIDC tests for coverage")
	return nil
}

func setupTestOIDCKeys(t *testing.T) *service.OIDCKeyService {
	t.Skip("OIDC key service test wiring not implemented; see service/oidc_key_service_test.go")
	return nil
}

func setupTestAuthService(t *testing.T) *service.AuthService {
	t.Skip("AuthService test wiring not implemented")
	return nil
}

func setupTestMeHandler(t *testing.T) *handler.AuthHandler {
	t.Skip("Me handler test wiring not implemented; /v1/me covered by TestMe_OAuth* in oauth_provider_account_handler_test.go")
	return nil
}

func createTestAccessToken(t *testing.T, userID int64, scopes []string) string {
	t.Skip("access token test factory not implemented")
	return ""
}

func createTestIDToken(t *testing.T, clientID string, userID int64) string {
	// Create minimal id_token for testing
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "https://sub.sakrylle.com",
		"sub": "1",
		"aud": clientID,
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})

	// Sign with test key (not validated in this test)
	signed, _ := token.SignedString([]byte("test-secret"))
	return signed
}

func setupLogoutWhitelist(t *testing.T, clientID string, uris []string) {
	t.Skip("logout whitelist test setup not implemented")
}

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
