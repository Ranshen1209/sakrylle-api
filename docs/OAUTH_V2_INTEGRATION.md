# Sakrylle OAuth 2.0 v2 — 第三方集成指南

> Status: v2 Phase 6 (Documentation rollout). Companion documents:
> [`OAUTH_V2_ERROR_REFERENCE.md`](./OAUTH_V2_ERROR_REFERENCE.md),
> [`OAUTH_V2_DEVICE_FLOW_CLI_GUIDE.md`](./OAUTH_V2_DEVICE_FLOW_CLI_GUIDE.md),
> [`OAUTH_V2_SCOPE_MIGRATION.md`](./OAUTH_V2_SCOPE_MIGRATION.md),
> [`OAUTH_V2_FIRST_PARTY_SECURITY_CHECKLIST.md`](./OAUTH_V2_FIRST_PARTY_SECURITY_CHECKLIST.md).
> The full design contract is [`OAUTH_V2_DESIGN.md`](./OAUTH_V2_DESIGN.md).

This document is for **third-party developers** integrating against the
`https://sub.sakrylle.com` OAuth 2.0 provider. The previous v1 doc
(`OAUTH_CLIENT_INTEGRATION.md`) is kept for reference only — see
[Migration from v1](#migration-from-v1) below.

---

## 1. Overview

Sakrylle is the **authorization server**. Your app is a **client**. Once
authorized by an end user, your client receives:

- An **access token** (`sk_oauth_...`) that calls `/v1/*` resource endpoints.
- A **refresh token** (`rt_...`), if you requested `offline_access`, used to
  rotate the access token without re-prompting the user.

OAuth tokens authenticate **on behalf of a Sakrylle end user** against that
user's group/quota/balance. They are not API keys.

### v2 vs v1 at a glance

| Topic | v1 | v2 |
|---|---|---|
| Scope strings | `image_generation`, `balance:read` | Canonical: `images:create`, `account:balance:read` |
| Token endpoint | `/oauth/token` | Same path, body and content-type rules unchanged |
| Refresh rotation | One-shot rotation | Reuse-detection family revocation (`§11.5`) |
| Authorize approval | Body-replay accepted | Server-side transaction + CSRF; approve POST cannot tamper |
| Device Flow | Not supported | RFC 8628 (`/oauth/device/code`, `/oauth/device`) |
| `/v1/me` | Not available | Available; fields are scope-cropped |
| Discovery | `/.well-known/oauth-authorization-server` (limited) | Full RFC 8414 metadata |
| Authorized Apps UX | Per client only | Per device, with revoke-one or revoke-all |

Legacy aliases (`image_generation`, `balance:read`) remain accepted during the
v2 rollout window. See [`OAUTH_V2_SCOPE_MIGRATION.md`](./OAUTH_V2_SCOPE_MIGRATION.md).

---

## 2. Quick Start

1. Email `support@sakrylle.com` with the data in
   [Client registration](#client-registration).
2. Once approved you receive a `client_id` (and, for confidential clients,
   a `client_secret`).
3. Pick a flow:
   - Browser-based UI or mobile app: **Authorization Code + PKCE**
     ([§3](#authorization-code--pkce-flow)).
   - CLI / TV / shell tool / no browser: **Device Authorization** (RFC 8628)
     ([§4](#device-authorization-flow)).

### Client registration

Provide:

| Field | Notes |
|---|---|
| `name` | Human display name. Shown on the consent page. |
| `app_type` | One of `web`, `chat`, `cli`, `desktop`, `mobile`, `image`. |
| `client_type` | `public` (no secret, PKCE required) or `confidential` (BFF/server-side). |
| `redirect_uris` | Exact-match HTTPS URIs; native loopback `127.0.0.1` allowed for desktop. |
| `default_scopes` | Default scope bundle if your `/oauth/authorize` request omits `scope`. |
| `default_group_id` | Optional — Sakrylle group your tokens bind to by default. |
| `device_flow_enabled` | Boolean; only enable for CLI / headless. |

Localhost redirect URIs are accepted **only** on dev-named clients
(`<name>-dev`). Production seed rows must use HTTPS.

---

## 3. Authorization Code + PKCE Flow

### 3.1 Generate PKCE pair

```bash
# code_verifier — 43-128 unreserved characters, ≥ 256 bits entropy
code_verifier=$(openssl rand -base64 64 | tr -d '/+=' | cut -c1-128)

# code_challenge = BASE64URL(SHA256(verifier)), no padding
code_challenge=$(printf '%s' "$code_verifier" | openssl dgst -binary -sha256 \
  | openssl base64 | tr '/+' '_-' | tr -d '=')
```

### 3.2 Send the user to `/oauth/authorize`

```
GET https://sub.sakrylle.com/oauth/authorize
  ?response_type=code
  &client_id=YOUR_CLIENT_ID
  &redirect_uri=https://your.app/oauth/callback
  &scope=profile:read%20models:read%20chat.completions:create%20offline_access
  &state=RANDOM_OPAQUE_STATE
  &code_challenge=YOUR_CODE_CHALLENGE
  &code_challenge_method=S256
```

The user logs in (if needed), reviews the requested scopes, and approves or
denies. On approve, Sakrylle redirects to:

```
https://your.app/oauth/callback?code=AUTH_CODE&state=RANDOM_OPAQUE_STATE
```

**Always** verify the returned `state` equals what you sent, before proceeding.

### 3.3 Exchange the code for tokens

```bash
curl -sS https://sub.sakrylle.com/oauth/token \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=authorization_code" \
  -d "client_id=YOUR_CLIENT_ID" \
  -d "redirect_uri=https://your.app/oauth/callback" \
  -d "code=AUTH_CODE" \
  -d "code_verifier=$code_verifier"
```

Response (HTTP 200):

```json
{
  "access_token": "sk_oauth_...",
  "token_type": "Bearer",
  "expires_in": 86400,
  "refresh_token": "rt_...",
  "refresh_token_expires_in": 2592000,
  "scope": "profile:read models:read chat.completions:create offline_access"
}
```

The returned `scope` is the **canonical** form. Even if you requested legacy
aliases (`image_generation`, `balance:read`), v2 tokens echo the canonical
scopes (`images:create`, `account:balance:read`).

`Cache-Control: no-store` and `Pragma: no-cache` are set on the response.

### 3.4 Errors at the redirect_uri

If validation fails before the consent page renders, the user is redirected
back with `error=...&error_description=...&state=...`. See
[`OAUTH_V2_ERROR_REFERENCE.md`](./OAUTH_V2_ERROR_REFERENCE.md) for the full
list. If the `redirect_uri` itself is invalid, Sakrylle renders an inline
HTML page instead of redirecting (it does not act as an open redirector).

---

## 4. Device Authorization Flow

For CLI tools and headless clients. See
[`OAUTH_V2_DEVICE_FLOW_CLI_GUIDE.md`](./OAUTH_V2_DEVICE_FLOW_CLI_GUIDE.md)
for runnable examples.

Short version:

```bash
curl -sS https://sub.sakrylle.com/oauth/device/code \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "client_id=YOUR_CLIENT_ID" \
  -d "scope=profile:read messages:create offline_access"
```

```json
{
  "device_code": "...",
  "user_code": "SKRY-BCDF-G2346",
  "verification_uri": "https://sub.sakrylle.com/oauth/device",
  "verification_uri_complete": "https://sub.sakrylle.com/oauth/device?user_code=SKRY-BCDF-G2346",
  "expires_in": 600,
  "interval": 5
}
```

Display **both** `verification_uri` and `user_code` to the user (the
`verification_uri_complete` link is convenience only). Then poll
`/oauth/token` with `grant_type=urn:ietf:params:oauth:grant-type:device_code`
every `interval` seconds until you receive a token or a terminal error.

---

## 5. Refresh Token Rotation

```bash
curl -sS https://sub.sakrylle.com/oauth/token \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=refresh_token" \
  -d "client_id=YOUR_CLIENT_ID" \
  -d "refresh_token=rt_..."
```

Optional `group_id=N` to mint the new access token bound to a different
Sakrylle group. The requested `group_id` must be in
`allowed_groups_snapshot` recorded at consent time — refresh cannot widen
the grant.

### Rules

- Each refresh response **rotates** the refresh token. Discard the old one
  immediately and store the new one.
- `refresh_token_expires_in` is **family-anchored**: it inherits the
  original grant's absolute expiry. Rotation does NOT extend the session
  with `now + refresh_token_ttl_seconds`.
- Replaying a rotated (already-used) refresh token triggers
  **reuse detection**: the entire token family — refresh, access, and the
  underlying API key — is revoked, and the client is forced to re-auth.
- A refresh attempt with a refresh token issued for a different `client_id`
  returns `invalid_grant` without revealing cross-client ownership.

### Concurrency

Serialize refresh attempts per grant. If a stale concurrent refresh races
the active one, the loser is treated as reuse and the family is revoked.

---

## 6. Scopes

Canonical v2 scopes:

| Scope | What it allows | Notes |
|---|---|---|
| `profile:read` | User ID, display name, avatar, locale (no email). | Default in chat/web bundles. |
| `email:read` | User email. | Not granted to first-party clients by default. |
| `account:read` | Current/default group, allowed groups summary, quota/capability summary. | |
| `account:balance:read` | Balance and currency display only. | Strict subset of `account:read`. |
| `models:read` | List models for the current group via `/v1/models`. | |
| `chat.completions:create` | `POST /v1/chat/completions` (and `/chat/completions` alias). | |
| `responses:create` | `POST /v1/responses` (+ aliases) and `/v1/codex/responses`. | |
| `messages:create` | `POST /v1/messages` and `/v1/messages/count_tokens`. | |
| `images:create` | `POST /v1/images/generations` and `/v1/images/edits`. | |
| `usage:read` | `GET /v1/usage`. | |
| `offline_access` | Receive a refresh token. | Optional; required for persistent login. |

**Empty `scope`** in `/oauth/authorize` falls back to the client's
`default_scopes`. Unknown scopes return `invalid_scope`.

Legacy aliases (`image_generation` → `images:create`,
`balance:read` → `account:balance:read`) are accepted during the
deprecation window and rewritten to canonical before storage.

For per-endpoint scope requirements, see §7.3 of `OAUTH_V2_DESIGN.md`.

---

## 7. The `/v1/me` Endpoint

`GET /v1/me` — authenticated with the OAuth access token — returns the
current user context, **field-cropped by granted scopes**:

| Field | Required scope |
|---|---|
| `user_id` | `profile:read`, `account:read`, or `account:balance:read` |
| `username`, `display_name`, `avatar_url`, `locale` | `profile:read` |
| `email` | `email:read` |
| `current_group`, `current_group_id`, `allowed_groups`, `quota`, `capabilities` | `account:read` |
| `balance`, `currency_display` | `account:balance:read` or `account:read` |
| `granted_scopes` | always |
| `effective_capabilities` | always |

`granted_scopes` is the set of canonical scopes the token holds.
`effective_capabilities` is what the token can **actually do** given group
capabilities — they may differ if a granted scope is narrowed by the
current group.

---

## 8. Errors

OAuth endpoints return `application/json` errors with the RFC vocabulary in
the top-level `error` field; granular Sakrylle reasons live in
`error_description`, `error_uri`, and logs. Resource endpoints return
`Bearer` `WWW-Authenticate` headers per RFC 6750.

Full table: [`OAUTH_V2_ERROR_REFERENCE.md`](./OAUTH_V2_ERROR_REFERENCE.md).

---

## 9. Token Revocation

Revoke a refresh or access token (RFC 7009):

```bash
curl -sS https://sub.sakrylle.com/oauth/revoke \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "token=rt_..." \
  -d "token_type_hint=refresh_token" \
  -d "client_id=YOUR_CLIENT_ID"
```

Returns 200 even if the token is already revoked, unknown, or belongs to a
different client (idempotent / no information leak per §12.9). After revoke,
the access token fails immediately on the next `/v1/*` call — Sakrylle
publishes a Redis cache invalidation rather than waiting for TTL.

Users can also revoke from `https://sub.sakrylle.com/authorized-apps`.

---

## 10. Discovery

```bash
curl -sS https://sub.sakrylle.com/.well-known/oauth-authorization-server | jq
```

Returns RFC 8414 metadata: `issuer`, `authorization_endpoint`,
`token_endpoint`, `device_authorization_endpoint`, `revocation_endpoint`,
`response_types_supported`, `grant_types_supported`,
`code_challenge_methods_supported` (S256 only), `scopes_supported`, etc.

Cache the response client-side for at most one hour.

---

## 11. Security Requirements

- **Exact `redirect_uri` match**. No prefixes, no wildcards, no path joins.
  Native loopback may use a registered `127.0.0.1` / `[::1]` / `localhost`
  template with a dynamic port; nothing else.
- **PKCE S256 only**. `code_challenge_method=plain` is rejected.
- **`Content-Type: application/x-www-form-urlencoded`** on
  `/oauth/token`, `/oauth/revoke`, and `/oauth/device/code`. JSON bodies
  are rejected with `invalid_request`.
- **`state` is required** on `/oauth/authorize`. Verify it round-trips.
- **Refresh tokens are confidential**. Web SPAs that cannot use a BFF must
  omit `offline_access` from the bundle and re-auth.

If your client is a Sakrylle first-party app (sakrylle.com domain or
internal tool), additional rules apply — see
[`OAUTH_V2_FIRST_PARTY_SECURITY_CHECKLIST.md`](./OAUTH_V2_FIRST_PARTY_SECURITY_CHECKLIST.md).

---

## 12. Migration from v1

Existing v1 integrations:

- Continue using `image_generation` and `balance:read` while the legacy
  alias window is active. The token response will start returning the
  canonical names; surface those in your UI rather than the legacy strings.
- Drop any reliance on a stable `redirect_uri` prefix match. v2 rejects
  anything but exact match.
- If you mint long-lived refresh tokens, audit your storage. v2's reuse
  detection will revoke the entire family if the same plaintext refresh is
  ever submitted twice.
- If you operate a CLI tool, evaluate whether Device Flow replaces the
  loopback PKCE you are using today.

Full alias mapping and timeline: [`OAUTH_V2_SCOPE_MIGRATION.md`](./OAUTH_V2_SCOPE_MIGRATION.md).

---

## 13. Support

`support@sakrylle.com` for client registration, secret rotation, redirect
URI changes, and integration questions.
