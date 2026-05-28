# Sakrylle OAuth 2.0 v2 — First-Party Security Checklist

> Companion to [`OAUTH_V2_INTEGRATION.md`](./OAUTH_V2_INTEGRATION.md). For
> the full security baseline see §19 of
> [`OAUTH_V2_DESIGN.md`](./OAUTH_V2_DESIGN.md).

This checklist is for **first-party** Sakrylle clients — apps that ship
under sakrylle.com domains or are distributed by the Sakrylle team
(`sakrylle-web`, `sakrylle-cli`, `sakrylle-image-playground-v2`,
`sakrylle-mobile`, `sakrylle-desktop`, `sakrylle-chat-desktop`). The bar
is higher than for third-party integrations because these clients can
request capability scopes by default and their bundles ship to a wide
user base.

If you are a **third-party** developer, follow
[`OAUTH_V2_INTEGRATION.md`](./OAUTH_V2_INTEGRATION.md) and the public
security section in §11 of that doc.

---

## 1. Choose the client classification

| Decision | Default for first-party | Why |
|---|---|---|
| `client_type=public` (no secret, PKCE required) | All native + SPA | Cannot keep a secret. PKCE is the binding. |
| `client_type=confidential` (secret + PKCE) | BFF only | Only when the secret lives server-side. |
| `app_type=web` (BFF) | `sakrylle-web` | Server-side token store. |
| `app_type=web-spa` | `sakrylle-web-spa` if BFF unavailable | Browser-only; **must omit `offline_access`**. |
| `app_type=chat` | `sakrylle-chat-desktop` | Loopback PKCE on desktop. |
| `app_type=cli` | `sakrylle-cli` | Device Flow only. No loopback unless explicitly added. |
| `app_type=desktop` | `sakrylle-desktop` | Loopback PKCE; system browser only. |
| `app_type=mobile` | `sakrylle-mobile` | Universal/App Links preferred; custom scheme is fallback. |
| `app_type=image` | `sakrylle-image-playground-v2` | GPT-Image group plus response-capable groups. |

Once registered, `client_type` is **write-once**. To switch a client from
public to confidential or vice versa, register a new `client_id` and
migrate users explicitly.

---

## 2. Redirect URIs

- [ ] Production seed values are **HTTPS** with the controlled Sakrylle
      domain (`web.sakrylle.com`, `image.sakrylle.com`, etc.). No
      localhost in production rows.
- [ ] Localhost / loopback values live on dev-named clients only
      (`sakrylle-web-dev`, `sakrylle-image-playground-dev`).
- [ ] Native clients use the registered loopback template
      (`http://127.0.0.1/oauth/callback`,
      `http://[::1]/oauth/callback`, or `http://localhost/oauth/callback`)
      with dynamic ports — only the registered host and path are matched.
- [ ] Mobile clients use Universal Links (Apple) or App Links (Android)
      where possible. Custom schemes are fallback only and must use
      reverse-domain form (`com.sakrylle.mobile:/oauth/callback`).
- [ ] No broad product schemes (`sakrylle://...`) for new native clients.
- [ ] Redirect URIs are exact-match — no prefix, no wildcard, no path
      suffix. Sakrylle's whitelist is character-for-character.
- [ ] Adding a new redirect URI goes through ECC change management (the
      `oauth_clients` row is edited via direct SQL, not the admin PUT).

---

## 3. PKCE

- [ ] Every authorization-code flow generates a fresh PKCE pair.
- [ ] `code_verifier` is **256+ bits** of cryptographic entropy, encoded
      as 43-128 unreserved characters.
- [ ] `code_challenge_method=S256`. **Never** `plain` (Sakrylle rejects).
- [ ] The verifier is held in memory or session-bound storage on the
      authorizing process; it is not persisted past code-for-token
      exchange.
- [ ] PKCE is required even for confidential first-party clients. The
      design contract is "PKCE for both `public` and `confidential`."

---

## 4. State and session binding

- [ ] `state` is generated per authorization, ≥ 128 bits entropy, opaque.
- [ ] Verified to match on the redirect-back. Mismatch aborts the flow
      with no further server contact.
- [ ] The authorize transaction CSRF cookie that Sakrylle sets on
      `/oauth/authorize` is `HttpOnly`, `SameSite=Lax`, `Secure`. Don't
      strip it.
- [ ] Approve POST submits only `transaction_id`, `csrf_token`, optional
      `group_id`, and `decision`. Never resubmit `client_id`,
      `redirect_uri`, `scopes`, `state`, or `code_challenge` — Sakrylle
      ignores them in v2 anyway, but cleaner code never sends them.

---

## 5. Token storage

The cardinal rules — these block PR review:

| Client class | Refresh token storage | Forbidden |
|---|---|---|
| Web BFF | Server-side only; HttpOnly cookie carries a session id | Browser-readable storage |
| Web SPA | **Omit `offline_access`** | localStorage, sessionStorage, IndexedDB |
| Electron / desktop | OS keychain (Keychain / Credential Manager / Secret Service) | Renderer-process storage |
| iOS | Keychain | UserDefaults |
| Android | EncryptedSharedPreferences or Android Keystore | Plain SharedPreferences |
| CLI | OS keychain when available; file fallback `0600` in `0700` dir | Shell history, env vars, world-readable files |

Specifically:

- [ ] Web SPA clients **must** omit `offline_access` from the default
      bundle. Use short-lived access tokens + re-auth.
- [ ] Electron clients store refresh tokens in the main process via the
      OS keychain. The renderer never sees the refresh plaintext.
- [ ] CLI clients write to `~/.config/sakrylle/credentials` with mode
      `0600` and the parent dir mode `0700`. Never `~/.bash_history`
      or `~/.netrc`.
- [ ] Refresh tokens are masked in any debug log (`rt_***...***xyz`).
- [ ] Access tokens are masked in user-visible logs (`sk_oauth_***...***xyz`).
- [ ] No token of any kind in URL query strings (Sakrylle rejects
      `?api_key=` for resource calls).

---

## 6. Refresh rotation

- [ ] Treat the refresh token as **single-use**. Discard the previous
      value the moment a rotated value arrives.
- [ ] If `/oauth/token` returns `invalid_grant` with
      `error_description="refresh token reuse detected"`, **discard all
      OAuth state for this user** and force re-auth. Reuse detection is
      a tamper signal — assume token theft until proven otherwise.
- [ ] Serialize refresh attempts per grant. Two concurrent refreshes are
      a race — losers must discard their stale tokens and retry from the
      latest stored refresh, or re-auth if the family was revoked.
- [ ] Honor the family-anchored `refresh_token_expires_in`. If the family
      expiry is approaching, prompt re-auth rather than burning the last
      refresh on a doomed rotation.

---

## 7. Step-up authentication for sensitive actions

`DELETE /api/v1/oauth/authorized-apps/*` (revoke a grant) requires
recent strong authentication:

- [ ] First-party clients that surface revoke flows must verify the user
      is freshly authenticated (recent password / MFA / passkey
      challenge) before calling the revoke endpoint.
- [ ] The Authorized Apps page on `sub.sakrylle.com` enforces this at
      the BFF/JWT layer; embedded webviews / PWAs that wrap that page
      must not weaken it.

---

## 8. UI / UX

- [ ] Native apps use the **system browser** (ASWebAuthenticationSession
      on Apple, Chrome Custom Tabs on Android, default browser on
      desktop). **Embedded WebView login is forbidden.**
- [ ] Consent page is rendered by Sakrylle, not your app. Don't try to
      pre-populate scopes by URL parameter manipulation.
- [ ] Display the consent app name, scope labels, and group selector
      that Sakrylle returns — don't substitute your own copy.
- [ ] When showing scope labels in your own UI (Authorized Apps list,
      onboarding), use the canonical zh/en strings from
      `oauth_scopes.go::scopeLabels` (mirrored to the frontend i18n
      bundles).

---

## 9. Token TTL configuration

For first-party clients, defaults are:

| Field | Value | Notes |
|---|---|---|
| `access_token_ttl_seconds` | 3600 (CLI) / 86400 (Web/Image) | Override per-client only with security review |
| `refresh_token_ttl_seconds` | 86400 (CLI) / 2592000 (Web/Image) | Family-anchored |
| `device_flow_enabled` | `true` only for CLI clients | All others must be `false` |
| `allow_refresh_without_offline_access` | `false` for new clients | Legacy compat for `sakrylle-image-playground` only |

If your client needs different values, document the threat model and the
storage guarantees that justify the deviation. The default short access
token TTL is what makes the family-revocation guarantees meaningful.

---

## 10. Monitoring and runbooks

- [ ] Subscribe to the `oauth:cache:invalidate` Redis channel via the
      gateway; never serve a `sk_oauth_*` request after the channel says
      the access token is gone.
- [ ] Subscribe to the `auth:cache:invalidate` channel for API key state
      changes, including OAuth-backed `api_keys` rows.
- [ ] Page on `oauth_refresh_reuse_detected_total > 0` over 5 minutes
      for first-party clients. Reuse means either token theft or a
      client-side rotation bug.
- [ ] Alert on elevated `oauth_token_requests_total{result="invalid_grant"}`
      bucketed by `client_id`. A spike suggests a deploy regression.
- [ ] Verify revoke smoke tests: mint → call `/v1/models` 200 → revoke →
      call `/v1/models` returns `401 invalid_token` within 1 second
      (NOT after the 60s Redis TTL).
- [ ] Log redaction is on for plaintext tokens, codes, verifiers, and
      secrets at every layer: gateway, app handler, audit pipeline.

---

## 11. Pre-launch review

Before a new first-party client ships:

- [ ] Threat model written: what happens if the device is shared / stolen
      / coerced / reset?
- [ ] Token storage path documented (which OS facility, which encryption,
      who holds the key).
- [ ] Revoke flow documented and reachable from a settings page or
      equivalent.
- [ ] Legal: privacy URL and terms URL configured on the
      `oauth_clients` row.
- [ ] Internationalization: scope labels render correctly in zh and en.
- [ ] Accessibility: WCAG 2.1 AA contrast on consent / authorize redirect
      pages (the consent page itself is Sakrylle's responsibility, but
      your post-callback UI is yours).
- [ ] Penetration test or security review covering: redirect URI
      validation in your callback handler, PKCE generation entropy,
      refresh storage, log redaction.

---

## 12. Anti-patterns to reject in code review

- WebView-based login flows (use the system browser).
- Storing the refresh token in `localStorage`, `sessionStorage`, or any
  browser-JS-readable surface for a public client.
- Building a `redirect_uri` allowlist that uses `String.startsWith`
  (Sakrylle requires exact match; mirror that on the client side).
- Hardcoding scope strings in multiple files. Reference one canonical
  list and import from there.
- Catching `invalid_grant` from refresh as "expired, retry once" — it
  may be reuse detection. Discard state and re-auth instead.
- Auto-retrying on `access_denied`. The user said no.
- Sending `client_secret` from a public client. PKCE is your binding.
- Embedding `offline_access` in a browser SPA bundle without a BFF.

---

## 13. Sakrylle-specific operational notes

- Production `oauth_clients` rows are seeded by Sakrylle-specific
  migrations / operator SQL, not the generic schema migration. Group IDs
  are resolved by name or operator override.
- The `sub.sakrylle.com` BFF is the default trust boundary for
  `sakrylle-web`. If you fork it, replicate the BFF or change the client
  to `sakrylle-web-spa` with refresh tokens stripped.
- Restart `sub2api` after editing `oauth_clients`,
  `channel_model_pricing`, or any other group resolution dependency —
  in-memory caches don't reload automatically (CLAUDE.md, "Deepseek
  双协议入口与缓存陷阱").
- Redis cache invalidation channels are part of the contract: revokes
  must publish on `auth:cache:invalidate` (full plaintext API key) AND
  `oauth:cache:invalidate` (envelope JSON). Skipping either leaves
  zombie tokens for ~60s.

---

## 14. Device flow brute-force defense (§10.6 clarification)

The §10.6 baseline mentions a per-code 5-strikes lockout via
`failed_user_code_attempts`. In practice that counter is **only**
incremented when a wrong guess happens to hash to a real `user_code_hash`
— which never happens for adversarial guesses, because the attacker
doesn't know the canonical user code. The counter exists for defense in
depth against a stale row that gets repeatedly mis-routed at the service
layer (e.g. ResolveOAuthGroup failures), not as the primary brute-force
guard.

The **real** brute-force defense for device flow on Sakrylle is the
per-IP rate limit on the verification endpoints (see
`internal/server/routes/oauth_device.go`):

| Endpoint | Limit | Window | Failure mode |
|---|---|---|---|
| `POST /oauth/device/code` | 10 | 1 min | fail-close |
| `GET  /oauth/device` | 30 | 1 min | fail-close |
| `POST /api/v1/oauth/device/approve` | 5 | 15 min | fail-close |
| `POST /api/v1/oauth/device/deny` | 5 | 15 min | fail-close |

If you fork the routing, preserve those limits — the user-code lockout
counter is not a substitute.
