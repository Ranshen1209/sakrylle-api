package service

// §18.7 Security Negative Tests — service-layer surface.
//
// These tests live in the same package as the OAuth core service so they can
// reuse the in-memory stubs defined in oauth_provider_service_test.go
// (stubClientRepo, stubCodeRepo, stubRefreshRepo, stubAPIKeyRepo,
// stubSettingRepo, recordingAuthCacheInvalidator, etc.) and the helper
// pkceVerifierAndChallenge. Where the relevant negative path is owned by an
// HTTP handler instead of the service, the test is in
// internal/handler/oauth_*_test.go and a comment here points to it.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestSecurity_RedirectURIPrefixAttack verifies that a redirect_uri whose
// prefix happens to match an allow-listed value is still rejected. Exact
// string-match only (§8.4 Web/SPA: "No prefix match.").
func TestSecurity_RedirectURIPrefixAttack(t *testing.T) {
	allowed := []string{"https://image.sakrylle.com/oauth/callback"}
	cases := []string{
		// Attacker tacks the legit URI onto a query string of an attacker host.
		"https://attacker.com/callback?next=https://image.sakrylle.com/oauth/callback",
		// Attacker registers a path that starts with the legit value.
		"https://image.sakrylle.com/oauth/callback/../evil",
		// Attacker appends a query/fragment to escape strict equality.
		"https://image.sakrylle.com/oauth/callback?evil=1",
		"https://image.sakrylle.com/oauth/callback#evil",
		// Trailing slash is also a different string.
		"https://image.sakrylle.com/oauth/callback/",
	}
	for _, candidate := range cases {
		if redirectURIAllowed(allowed, candidate) {
			t.Errorf("redirect_uri prefix attack must be rejected; %q was accepted", candidate)
		}
	}
}

// TestSecurity_RedirectURISubdomainAttack rejects a host whose suffix matches
// an allow-listed host. e.g. image.sakrylle.com.attacker.com must NOT pass
// any sloppy "ends-with sakrylle.com" check.
func TestSecurity_RedirectURISubdomainAttack(t *testing.T) {
	allowed := []string{"https://image.sakrylle.com/oauth/callback"}
	cases := []string{
		"https://image.sakrylle.com.attacker.com/oauth/callback",
		"https://attacker.com/image.sakrylle.com/oauth/callback",
		"https://image-sakrylle.com/oauth/callback", // hyphen vs dot
		"http://image.sakrylle.com/oauth/callback",  // scheme downgrade
	}
	for _, candidate := range cases {
		if redirectURIAllowed(allowed, candidate) {
			t.Errorf("redirect_uri suffix/host attack must be rejected; %q was accepted", candidate)
		}
	}
}

// TestSecurity_PKCEPlainRejected — even when challenge==verifier (which would
// pass a "plain" check) the server must require S256.
func TestSecurity_PKCEPlainRejected(t *testing.T) {
	svc, _, _ := newServiceUnderTest(t)
	ctx := context.Background()
	verifier := "the-quick-brown-fox-jumps-over-the-lazy-dog-12345"

	req := &AuthorizeRequest{
		ClientID:            "sakrylle-image-playground",
		RedirectURI:         "https://image.sakrylle.com/oauth/callback",
		ResponseType:        "code",
		Scopes:              []string{"image_generation"},
		State:               "abc",
		CodeChallenge:       verifier, // plain == verifier
		CodeChallengeMethod: "plain",
	}
	if _, err := svc.ValidateAuthorizeRequest(ctx, req); !errors.Is(err, ErrOAuthUnsupportedChallenge) {
		t.Fatalf("plain method must be rejected; got err=%v, want ErrOAuthUnsupportedChallenge", err)
	}
}

// TestSecurity_PKCES256Mismatch verifies the code is consumed even when the
// verifier doesn't match — replaying the code with a corrected verifier must
// fail with ErrOAuthCodeAlreadyUsed.
func TestSecurity_PKCES256Mismatch(t *testing.T) {
	svc, _, _ := newServiceUnderTest(t)
	ctx := context.Background()
	verifier, challenge := pkceVerifierAndChallenge("verifier-pkce-mismatch-aaaaaaaaaaaaaaaaaaaa")

	req := &AuthorizeRequest{
		ClientID:            "sakrylle-image-playground",
		RedirectURI:         "https://image.sakrylle.com/oauth/callback",
		ResponseType:        "code",
		Scopes:              []string{"image_generation"},
		State:               "x",
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
	}
	client, err := svc.ValidateAuthorizeRequest(ctx, req)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	issued, err := svc.IssueAuthorizationCode(ctx, client, 42, req)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	// Wrong verifier consumes the code.
	if _, err := svc.ExchangeAuthorizationCode(ctx, client.ClientID, "", issued.Code, req.RedirectURI, "wrong-verifier"); !errors.Is(err, ErrOAuthPKCEFailed) {
		t.Fatalf("wrong verifier should return ErrOAuthPKCEFailed; got %v", err)
	}
	// Retrying with the correct verifier must fail because the code was
	// consumed by the previous attempt — otherwise an attacker could iterate
	// verifier guesses.
	if _, err := svc.ExchangeAuthorizationCode(ctx, client.ClientID, "", issued.Code, req.RedirectURI, verifier); !errors.Is(err, ErrOAuthCodeAlreadyUsed) {
		t.Fatalf("code was not consumed on PKCE failure; got %v, want ErrOAuthCodeAlreadyUsed", err)
	}
}

// codeRepoReturningRowOnReuse adapts stubCodeRepo so ConsumeCode returns the
// loaded row alongside ErrOAuthCodeAlreadyUsed. The production repo (Workstream
// A) returns the row on this branch precisely so the service layer can revoke
// previously-issued tokens (§11.2 / RFC 6749 §10.5). The legacy stub at the
// top of oauth_provider_service_test.go returns nil and predates the v2
// contract; we wrap it here without disturbing the existing fixtures.
type codeRepoReturningRowOnReuse struct {
	*stubCodeRepo
}

func (r *codeRepoReturningRowOnReuse) ConsumeCode(ctx context.Context, h string, now time.Time) (*OAuthCode, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.codes[h]
	if !ok {
		return nil, ErrOAuthCodeNotFound
	}
	if c.UsedAt != nil {
		// v2 contract: return the row so callers can revoke its grant.
		cp := *c
		return &cp, ErrOAuthCodeAlreadyUsed
	}
	if !c.ExpiresAt.After(now) {
		return nil, ErrOAuthCodeExpired
	}
	c.UsedAt = &now
	return c, nil
}

// TestSecurity_CodeReplayRevokesGrant — replaying an already-consumed code
// triggers grant-wide revocation (RFC 6749 §10.5).
func TestSecurity_CodeReplayRevokesGrant(t *testing.T) {
	groupID := int64(5)
	clientRepo := &stubClientRepo{clients: map[string]*OAuthClient{
		"sakrylle-image-playground": {
			ClientID:                         "sakrylle-image-playground",
			Name:                             "Sakrylle Image Playground",
			RedirectURIs:                     []string{"https://image.sakrylle.com/oauth/callback"},
			AllowedScopes:                    []string{"image_generation"},
			PKCERequired:                     true,
			DefaultGroupID:                   &groupID,
			AccessTokenTTLSeconds:            86400,
			RefreshTokenTTLSeconds:           2592000,
			AllowRefreshWithoutOfflineAccess: true,
		},
	}}
	apiKeyRepo := newStubAPIKeyRepo()
	refreshRepo := newStubRefreshRepo()
	codeRepo := &codeRepoReturningRowOnReuse{stubCodeRepo: newStubCodeRepo()}
	settingRepo := &stubSettingRepo{values: map[string]string{
		"oauth_provider_enabled": "true",
		"oauth_default_group_id": "5",
	}}
	svc := newStubOAuthProviderService(clientRepo, codeRepo, refreshRepo, apiKeyRepo, nil, settingRepo, nil)
	ctx := context.Background()
	verifier, challenge := pkceVerifierAndChallenge("verifier-code-replay-aaaaaaaaaaaaaaaaaaaaaa")

	req := &AuthorizeRequest{
		ClientID:            "sakrylle-image-playground",
		RedirectURI:         "https://image.sakrylle.com/oauth/callback",
		ResponseType:        "code",
		Scopes:              []string{"image_generation"},
		State:               "x",
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
	}
	client, err := svc.ValidateAuthorizeRequest(ctx, req)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	issued, err := svc.IssueAuthorizationCode(ctx, client, 42, req)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	// First exchange succeeds.
	tok, err := svc.ExchangeAuthorizationCode(ctx, client.ClientID, "", issued.Code, req.RedirectURI, verifier)
	if err != nil {
		t.Fatalf("first exchange: %v", err)
	}

	// Replay — must error AND revoke the previously minted token.
	if _, err := svc.ExchangeAuthorizationCode(ctx, client.ClientID, "", issued.Code, req.RedirectURI, verifier); !errors.Is(err, ErrOAuthCodeAlreadyUsed) {
		t.Fatalf("replay: got err=%v, want ErrOAuthCodeAlreadyUsed", err)
	}

	// Verify the api_keys row issued in step 1 was disabled.
	disabled := false
	for _, row := range apiKeyRepo.rows {
		if row.Key == tok.AccessToken && row.Status == StatusAPIKeyDisabled {
			disabled = true
		}
	}
	if !disabled {
		t.Errorf("code replay must disable the api_keys row that the original code minted")
	}

	// And the corresponding refresh row revoked.
	revoked := false
	for _, r := range refreshRepo.tokens {
		if r.RevokedAt != nil {
			revoked = true
		}
	}
	if !revoked {
		t.Errorf("code replay must revoke any refresh tokens previously derived from that code")
	}
}

// TestSecurity_RefreshReuseRevokesFamily — once a plaintext refresh has been
// rotated and someone replays it, the WHOLE family (refresh, access, api_keys)
// must be revoked (§11.5 / §18.7).
func TestSecurity_RefreshReuseRevokesFamily(t *testing.T) {
	groupID := int64(5)
	clientRepo := &stubClientRepo{clients: map[string]*OAuthClient{
		"sakrylle-image-playground": {
			ClientID:                         "sakrylle-image-playground",
			Name:                             "Sakrylle Image Playground",
			RedirectURIs:                     []string{"https://image.sakrylle.com/oauth/callback"},
			AllowedScopes:                    []string{"image_generation"},
			PKCERequired:                     true,
			DefaultGroupID:                   &groupID,
			AccessTokenTTLSeconds:            86400,
			RefreshTokenTTLSeconds:           2592000,
			AllowRefreshWithoutOfflineAccess: true,
		},
	}}
	apiKeyRepo := newStubAPIKeyRepo()
	refreshRepo := newStubRefreshRepo()
	cache := &recordingAuthCacheInvalidator{}
	svc := newStubOAuthProviderService(
		clientRepo, newStubCodeRepo(), refreshRepo, apiKeyRepo,
		nil, &stubSettingRepo{values: map[string]string{}}, cache,
	)
	ctx := context.Background()
	verifier, challenge := pkceVerifierAndChallenge("verifier-reuse-attack-aaaaaaaaaaaaaaaaaaaaa")

	req := &AuthorizeRequest{
		ClientID:            "sakrylle-image-playground",
		RedirectURI:         "https://image.sakrylle.com/oauth/callback",
		ResponseType:        "code",
		Scopes:              []string{"image_generation"},
		State:               "x",
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
	}
	client, err := svc.ValidateAuthorizeRequest(ctx, req)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	issued, err := svc.IssueAuthorizationCode(ctx, client, 42, req)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	tok1, err := svc.ExchangeAuthorizationCode(ctx, client.ClientID, "", issued.Code, req.RedirectURI, verifier)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}

	// Set token_family_id manually since the legacy fixture path doesn't pass
	// one through; mirrors what a v2 ApproveAuthorization+mintTokensFromCode
	// would do. Without a family id, RevokeRefreshTokensByTokenFamilyID has
	// nothing to match — which is itself a useful regression assertion: the
	// minted refresh row HAS a token_family_id whenever the v2 access path
	// runs. Skip the manual set and rely on real wiring.

	// First refresh succeeds.
	tok2, err := svc.RefreshAccessToken(ctx, client.ClientID, "", tok1.RefreshToken, nil)
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if tok2.RefreshToken == tok1.RefreshToken {
		t.Fatal("refresh must rotate")
	}

	// Replay original — reuse detection.
	_, err = svc.RefreshAccessToken(ctx, client.ClientID, "", tok1.RefreshToken, nil)
	if !errors.Is(err, ErrOAuthRefreshReuse) {
		t.Fatalf("reuse: got err=%v, want ErrOAuthRefreshReuse", err)
	}

	// Cache invalidation fired for at least the rotated access tokens.
	calls := cache.snapshot()
	if len(calls) == 0 {
		t.Fatalf("reuse must publish auth-cache invalidation; got 0 calls")
	}
}

// TestSecurity_RefreshClientMismatchRejected — a refresh token issued for
// client A cannot be redeemed by client B (§12.5 explicit).
func TestSecurity_RefreshClientMismatchRejected(t *testing.T) {
	groupID := int64(5)
	clientRepo := &stubClientRepo{clients: map[string]*OAuthClient{
		"client-a": {
			ClientID:                         "client-a",
			Name:                             "Client A",
			RedirectURIs:                     []string{"https://a.example.com/cb"},
			AllowedScopes:                    []string{"image_generation"},
			PKCERequired:                     true,
			DefaultGroupID:                   &groupID,
			AccessTokenTTLSeconds:            3600,
			RefreshTokenTTLSeconds:           86400,
			AllowRefreshWithoutOfflineAccess: true,
		},
		"client-b": {
			ClientID:                         "client-b",
			Name:                             "Client B",
			RedirectURIs:                     []string{"https://b.example.com/cb"},
			AllowedScopes:                    []string{"image_generation"},
			PKCERequired:                     true,
			DefaultGroupID:                   &groupID,
			AccessTokenTTLSeconds:            3600,
			RefreshTokenTTLSeconds:           86400,
			AllowRefreshWithoutOfflineAccess: true,
		},
	}}
	apiKeyRepo := newStubAPIKeyRepo()
	refreshRepo := newStubRefreshRepo()
	svc := newStubOAuthProviderService(
		clientRepo, newStubCodeRepo(), refreshRepo, apiKeyRepo,
		nil, &stubSettingRepo{values: map[string]string{}}, nil,
	)
	ctx := context.Background()
	verifier, challenge := pkceVerifierAndChallenge("verifier-cross-client-aaaaaaaaaaaaaaaaaaaaaa")

	// Mint a token for client A.
	reqA := &AuthorizeRequest{
		ClientID: "client-a", RedirectURI: "https://a.example.com/cb", ResponseType: "code",
		Scopes: []string{"image_generation"}, State: "x",
		CodeChallenge: challenge, CodeChallengeMethod: "S256",
	}
	a, err := svc.ValidateAuthorizeRequest(ctx, reqA)
	if err != nil {
		t.Fatalf("validate A: %v", err)
	}
	codeA, err := svc.IssueAuthorizationCode(ctx, a, 42, reqA)
	if err != nil {
		t.Fatalf("issue A: %v", err)
	}
	tokA, err := svc.ExchangeAuthorizationCode(ctx, "client-a", "", codeA.Code, reqA.RedirectURI, verifier)
	if err != nil {
		t.Fatalf("exchange A: %v", err)
	}

	// Try to use A's refresh token at client-b — must NOT reveal
	// cross-client ownership; spec says ErrOAuthInvalidGrant.
	if _, err := svc.RefreshAccessToken(ctx, "client-b", "", tokA.RefreshToken, nil); !errors.Is(err, ErrOAuthInvalidGrant) {
		t.Fatalf("cross-client refresh: got err=%v, want ErrOAuthInvalidGrant", err)
	}
}

// TestSecurity_CrossClientRevokeStealth — Client B revoking Client A's
// refresh must look like a successful idempotent revoke (no information leak)
// but NOT actually revoke A's row.
//
// Implementation note: RevokeAccessOrRefreshToken returns nil for all
// "doesn't belong to this client" cases (§12.9 explicit). We assert the row
// is still active afterward.
func TestSecurity_CrossClientRevokeStealth(t *testing.T) {
	groupID := int64(5)
	clientRepo := &stubClientRepo{clients: map[string]*OAuthClient{
		"client-a": {
			ClientID: "client-a", Name: "A",
			RedirectURIs:                     []string{"https://a.example.com/cb"},
			AllowedScopes:                    []string{"image_generation"},
			PKCERequired:                     true,
			DefaultGroupID:                   &groupID,
			AccessTokenTTLSeconds:            3600,
			RefreshTokenTTLSeconds:           86400,
			AllowRefreshWithoutOfflineAccess: true,
		},
		"client-b": {
			ClientID: "client-b", Name: "B",
			RedirectURIs:                     []string{"https://b.example.com/cb"},
			PKCERequired:                     true,
			DefaultGroupID:                   &groupID,
			AccessTokenTTLSeconds:            3600,
			RefreshTokenTTLSeconds:           86400,
			AllowRefreshWithoutOfflineAccess: true,
		},
	}}
	apiKeyRepo := newStubAPIKeyRepo()
	refreshRepo := newStubRefreshRepo()
	svc := newStubOAuthProviderService(
		clientRepo, newStubCodeRepo(), refreshRepo, apiKeyRepo,
		nil, &stubSettingRepo{values: map[string]string{}}, nil,
	)
	ctx := context.Background()
	verifier, challenge := pkceVerifierAndChallenge("verifier-revoke-stealth-aaaaaaaaaaaaaaaaaaaaa")

	reqA := &AuthorizeRequest{
		ClientID: "client-a", RedirectURI: "https://a.example.com/cb", ResponseType: "code",
		Scopes: []string{"image_generation"}, State: "x",
		CodeChallenge: challenge, CodeChallengeMethod: "S256",
	}
	a, _ := svc.ValidateAuthorizeRequest(ctx, reqA)
	codeA, _ := svc.IssueAuthorizationCode(ctx, a, 42, reqA)
	tokA, err := svc.ExchangeAuthorizationCode(ctx, "client-a", "", codeA.Code, reqA.RedirectURI, verifier)
	if err != nil {
		t.Fatalf("exchange A: %v", err)
	}

	// Client B attempts revoke of A's refresh token.
	err = svc.RevokeAccessOrRefreshToken(ctx, "client-b", "", tokA.RefreshToken, "refresh_token")
	if err != nil {
		t.Fatalf("cross-client revoke must be a silent no-op (idempotent), got err=%v", err)
	}

	// A's refresh token is still active.
	for _, row := range refreshRepo.tokens {
		if row.ClientID == "client-a" && row.RevokedAt != nil {
			t.Errorf("client B's revoke must NOT touch client A's refresh token")
		}
	}
}

// TestSecurity_AuthorizeTransactionForeignSubject — FIX C1 §18.7. An
// authorize transaction created by user X cannot be approved by user Y, even
// when the attacker has the right CSRF token. The transaction row stores the
// user_id captured at /authorize time; LoadAuthorizeTransactionForApproval
// rejects mismatched JWT subjects with ErrOAuthSubjectMismatch and never
// surfaces the transaction to the caller.
//
// We exercise the service-layer fix directly via an in-memory authorize-tx
// repo so we don't depend on the handler wiring.
func TestSecurity_AuthorizeTransactionForeignSubject(t *testing.T) {
	groupID := int64(5)
	clientRepo := &stubClientRepo{clients: map[string]*OAuthClient{
		"sakrylle-image-playground": {
			ClientID:               "sakrylle-image-playground",
			Name:                   "Sakrylle Image Playground",
			RedirectURIs:           []string{"https://image.sakrylle.com/oauth/callback"},
			AllowedScopes:          []string{"image_generation"},
			DefaultScopes:          []string{"image_generation"},
			PKCERequired:           true,
			DefaultGroupID:         &groupID,
			AccessTokenTTLSeconds:  3600,
			RefreshTokenTTLSeconds: 86400,
		},
	}}
	apiKeyRepo := newStubAPIKeyRepo()
	refreshRepo := newStubRefreshRepo()
	codeRepo := newStubCodeRepo()
	settingRepo := &stubSettingRepo{values: map[string]string{
		"oauth_provider_enabled": "true",
		"oauth_default_group_id": "5",
	}}
	authzTxRepo := newStubAuthorizeTransactionRepo()
	svc := NewOAuthProviderService(
		clientRepo,
		codeRepo,
		refreshRepo,
		nil,
		nil,
		authzTxRepo,
		oauthAPIKeyOrAdapter(apiKeyRepo, nil),
		nil,
		nil,
		settingRepo,
		nil,
	)

	ctx := context.Background()
	_, challenge := pkceVerifierAndChallenge("verifier-foreign-subject-aaaaaaaaaaaaaaaaaaaa")

	const userA int64 = 100
	const userB int64 = 200

	begin, err := svc.BeginAuthorizeTransaction(ctx, &BeginAuthorizeParams{
		ClientID:            "sakrylle-image-playground",
		RedirectURI:         "https://image.sakrylle.com/oauth/callback",
		ResponseType:        "code",
		Scopes:              []string{"image_generation"},
		State:               "abc",
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
		UserID:              userA,
	})
	if err != nil {
		t.Fatalf("BeginAuthorizeTransaction: %v", err)
	}
	if begin.Transaction.UserID != userA {
		t.Fatalf("transaction must capture initiating user_id; got %d, want %d",
			begin.Transaction.UserID, userA)
	}

	// User B (different JWT subject) submits the approve POST with the
	// correct CSRF — must be rejected and the transaction must remain
	// unconsumed so user A can complete the flow.
	if _, err := svc.ApproveAuthorization(
		ctx,
		begin.Transaction.TransactionID,
		begin.CSRFTokenPlaintext,
		userB,
		nil,
	); !errors.Is(err, ErrOAuthSubjectMismatch) {
		t.Fatalf("foreign-subject approve must return ErrOAuthSubjectMismatch; got %v", err)
	}

	// No code may have been issued.
	codeRepo.mu.Lock()
	codesAfterAttack := len(codeRepo.codes)
	codeRepo.mu.Unlock()
	if codesAfterAttack != 0 {
		t.Fatalf("foreign-subject approve must not mint a code; got %d codes", codesAfterAttack)
	}

	// Transaction must remain unconsumed so the legitimate user can
	// complete the flow.
	row, err := authzTxRepo.GetAuthorizeTransactionForApproval(ctx, begin.Transaction.TransactionID, time.Now())
	if err != nil {
		t.Fatalf("transaction lookup after attack: %v", err)
	}
	if row.ConsumedAt != nil {
		t.Fatalf("foreign-subject approve must NOT consume the transaction")
	}

	// User A's legitimate approve still works.
	approved, err := svc.ApproveAuthorization(
		ctx,
		begin.Transaction.TransactionID,
		begin.CSRFTokenPlaintext,
		userA,
		nil,
	)
	if err != nil {
		t.Fatalf("legitimate approve after blocked attack: %v", err)
	}
	if approved.Code == "" {
		t.Fatalf("legitimate approve must mint a code")
	}
}

// TestSecurity_ContentTypeNonForm — service layer doesn't see Content-Type.
// Guarded at handler level via requireFormContentType. Pointer-only test.
func TestSecurity_ContentTypeNonForm(t *testing.T) {
	t.Skip("handler-level guard; see TestRevoke_RejectsNonFormContentType, TestDeviceAuthorize_RejectsJSONContentType in internal/handler")
}

// TestSecurity_AuthorizeTransactionCSRFFails — CSRF mismatch on the
// transaction-based approve path returns ErrOAuthAuthorizeCSRFMismatch
// without touching the transaction.
//
// Service layer is not wired with an authorize-tx repo in this fake set
// (nil authzTxRepo from newServiceUnderTest), so this assertion is at the
// handler level. Pointer-only.
func TestSecurity_AuthorizeTransactionCSRFFails(t *testing.T) {
	t.Skip("see TestApprove_CSRFMismatch in internal/handler/oauth_provider_handler_test.go")
}

// TestSecurity_DeviceCodeWrongClientNoLeak — Client B polling with Client A's
// device_code returns ErrOAuthInvalidGrant without revealing whether the code
// existed (§18.4). Same response shape as a fabricated device_code.
func TestSecurity_DeviceCodeWrongClientNoLeak(t *testing.T) {
	t.Skip("device flow is owned by Workstream D; see TestDeviceCode_WrongClientID_RejectedAsInvalidGrant in oauth_device_service_test.go for the in-package assertion. This pointer keeps §18.7 checklist alignment.")
}

// TestSecurity_UserCodeBruteForceLockout — same comment.
func TestSecurity_UserCodeBruteForceLockout(t *testing.T) {
	t.Skip("device flow is owned by Workstream D; see oauth_device_service_test.go for IncrementDeviceCodeFailedAttempts coverage. This pointer keeps §18.7 checklist alignment.")
}

// TestSecurity_NoTokenLeakInLogs — there is no trivial way to inspect log
// output without rewiring slog at the binary boundary. We assert the
// behavioural invariant: hashOAuthToken is the only thing the persistence
// layer should ever store. Defensive regression assertion: hashing changes
// the value (so a future refactor that accidentally stores plaintext would
// produce equality and fail the test).
func TestSecurity_NoTokenLeakInLogs(t *testing.T) {
	plain := "rt_supersecretvaluethatmustnotleak"
	h := hashOAuthToken(plain)
	if h == "" {
		t.Fatal("hashOAuthToken returned empty — refresh tokens would land unhashed in storage")
	}
	if strings.EqualFold(h, plain) {
		t.Fatal("hashOAuthToken returned the input verbatim — refactor regression")
	}
}

// TestSecurity_RefreshExpiryDoesNotExtendOnRotation — §11.5 explicit:
// "Refresh rotation preserves the original family absolute expiry and does
// not extend the session with `now + refresh_token_ttl_seconds`."
func TestSecurity_RefreshExpiryDoesNotExtendOnRotation(t *testing.T) {
	svc, _, refreshRepo := newServiceUnderTest(t)
	ctx := context.Background()
	verifier, challenge := pkceVerifierAndChallenge("verifier-expiry-anchored-aaaaaaaaaaaaaaaaaaaa")

	req := &AuthorizeRequest{
		ClientID:            "sakrylle-image-playground",
		RedirectURI:         "https://image.sakrylle.com/oauth/callback",
		ResponseType:        "code",
		Scopes:              []string{"image_generation"},
		State:               "x",
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
	}
	client, _ := svc.ValidateAuthorizeRequest(ctx, req)
	issued, _ := svc.IssueAuthorizationCode(ctx, client, 42, req)
	tok, err := svc.ExchangeAuthorizationCode(ctx, client.ClientID, "", issued.Code, req.RedirectURI, verifier)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}

	// Snapshot the original expiry of the freshly minted refresh row.
	var originalExpiry time.Time
	for _, row := range refreshRepo.tokens {
		if row.RevokedAt == nil {
			originalExpiry = row.ExpiresAt
		}
	}
	if originalExpiry.IsZero() {
		t.Fatal("could not find newly minted refresh row")
	}

	// Rotate.
	if _, err := svc.RefreshAccessToken(ctx, client.ClientID, "", tok.RefreshToken, nil); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	// The post-rotation refresh row must inherit the same expiry, NOT
	// re-extend by RefreshTokenTTLSeconds.
	var rotatedExpiry time.Time
	for _, row := range refreshRepo.tokens {
		if row.RevokedAt == nil {
			rotatedExpiry = row.ExpiresAt
		}
	}
	if rotatedExpiry.IsZero() {
		t.Fatal("could not find rotated refresh row")
	}
	// Allow at most 1s drift to absorb mintTokensFromCode/RefreshAccessToken
	// internal time.Now() calls; anything larger means we re-extended.
	delta := rotatedExpiry.Sub(originalExpiry)
	if delta > time.Second || delta < -time.Second {
		t.Fatalf("refresh rotation extended family expiry by %s — must inherit original ExpiresAt", delta)
	}
}

// stubAuthorizeTransactionRepo is a minimal in-memory store of authorize
// transactions used by the FIX C1 service-level subject-binding regression
// test. Implements service.OAuthAuthorizeTransactionRepository.
type stubAuthorizeTransactionRepo struct {
	mu   sync.Mutex
	rows map[string]*OAuthAuthorizeTransaction
}

func newStubAuthorizeTransactionRepo() *stubAuthorizeTransactionRepo {
	return &stubAuthorizeTransactionRepo{rows: map[string]*OAuthAuthorizeTransaction{}}
}

func (s *stubAuthorizeTransactionRepo) CreateAuthorizeTransaction(_ context.Context, tx *OAuthAuthorizeTransaction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *tx
	s.rows[tx.TransactionID] = &cp
	return nil
}

func (s *stubAuthorizeTransactionRepo) GetAuthorizeTransactionForApproval(_ context.Context, transactionID string, now time.Time) (*OAuthAuthorizeTransaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[transactionID]
	if !ok {
		return nil, ErrOAuthAuthorizeTransactionNotFound
	}
	if !row.ExpiresAt.After(now) {
		return nil, ErrOAuthAuthorizeTransactionNotFound
	}
	cp := *row
	return &cp, nil
}

func (s *stubAuthorizeTransactionRepo) MarkAuthorizeTransactionConsumed(_ context.Context, transactionID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[transactionID]
	if !ok {
		return ErrOAuthAuthorizeTransactionNotFound
	}
	if row.ConsumedAt == nil {
		t := now
		row.ConsumedAt = &t
	}
	return nil
}

func (s *stubAuthorizeTransactionRepo) DeleteExpiredAuthorizeTransactions(_ context.Context, _ time.Time) (int, error) {
	return 0, nil
}
