# Sakrylle OAuth v2 Design

Status: proposed design for implementation
Date: 2026-05-27
Repository: `Ranshen1209/sub2api`, branch `theme/monet-purple`
Primary production issuer: `https://sub.sakrylle.com`

This document is the implementation contract for upgrading the current Sakrylle OAuth provider from an Image Playground-oriented token issuer into the first-party authorization layer for Sakrylle Web, Sakrylle Chat, Sakrylle CLI, Sakrylle Desktop, Sakrylle Mobile, and Image Playground.

The intended implementation style is multi-Agent parallel work. Sections marked "Workstream" define file ownership, output contracts, dependencies, and acceptance criteria so multiple agents can work without inventing incompatible shapes.

## 1. Executive Summary

OAuth v2 keeps the current winning design:

```text
OAuth access_token -> sk_oauth_... row in api_keys -> existing /v1/* auth, billing, rate limits, usage logs
```

The upgrade adds the missing OAuth layer around it:

- OAuth access metadata keyed by `api_keys.id`.
- Capability-oriented v2 scopes and legacy scope aliases.
- Endpoint-level scope enforcement for `sk_oauth_` tokens.
- Device/app grants for user-facing authorized-app management.
- Group-aware token minting without changing `api_keys.group_id` from a single value.
- Device Authorization Flow for CLI.
- Token revocation endpoint and immediate auth-cache invalidation.
- Authorization-server discovery metadata.
- First-party OAuth client seeds for the Sakrylle ecosystem.

The core compatibility rule is:

```text
Manual API keys keep existing behavior. Only sk_oauth_ tokens receive OAuth v2 scope enforcement.
```

## 2. Standards Baseline

Implementation must follow these standards and security baselines:

- OAuth 2.0 Security Best Current Practice: https://www.rfc-editor.org/rfc/rfc9700.html
- OAuth 2.0 Bearer Token Usage: https://www.rfc-editor.org/rfc/rfc6750.html
- OAuth 2.0 Token Revocation: https://www.rfc-editor.org/rfc/rfc7009.html
- OAuth 2.0 Authorization Server Metadata: https://www.rfc-editor.org/rfc/rfc8414.html
- OAuth 2.0 for Native Apps: https://www.rfc-editor.org/rfc/rfc8252.html
- OAuth 2.0 Device Authorization Grant: https://www.rfc-editor.org/rfc/rfc8628.html
- PKCE: https://www.rfc-editor.org/rfc/rfc7636.html
- OAuth 2.0 Token Introspection, intentionally out of scope for phase 1: https://www.rfc-editor.org/rfc/rfc7662.html
- OAuth 2.0 Dynamic Client Registration, deferred: https://www.rfc-editor.org/rfc/rfc7591.html
- JWT Profile for OAuth 2.0 Access Tokens, intentionally not used because Sakrylle keeps opaque `sk_oauth_` API-key-backed tokens: https://www.rfc-editor.org/rfc/rfc9068.html

Phase 1 aligns with the OAuth 2.1 draft direction where it overlaps this design: no implicit flow, no password grant, exact redirect matching, and PKCE for authorization-code clients.

Normative Sakrylle decisions:

- Support Authorization Code + PKCE.
- Support Refresh Token.
- Support Device Authorization Grant.
- Do not support Implicit Flow.
- Do not support Resource Owner Password Credentials.
- Do not allow public native/mobile/CLI clients to use embedded static client secrets.
- Do not allow embedded WebView login in first-party apps.
- Require `S256` PKCE for every Authorization Code client, public and confidential.
- Reject `code_challenge_method=plain` and missing PKCE with RFC 6749 `invalid_request`.
- Use exact redirect URI matching, except native loopback redirect may vary only by port.
- Rotate refresh tokens on every use.
- Treat refresh-token reuse as a replay event and revoke the affected token family/grant.
- Do not log plaintext access tokens, refresh tokens, authorization codes, device codes, user codes, code verifiers, or client secrets.

## 3. Current State

Existing provider files:

- `backend/internal/server/routes/oauth.go`
- `backend/internal/handler/oauth_provider_handler.go`
- `backend/internal/handler/oauth_provider_consent.go`
- `backend/internal/handler/oauth_provider_account_handler.go`
- `backend/internal/service/oauth_provider_service.go`
- `backend/internal/service/oauth_provider_types.go`
- `backend/internal/repository/oauth_provider_repo.go`
- `backend/ent/schema/oauth_client.go`
- `backend/ent/schema/oauth_code.go`
- `backend/ent/schema/oauth_refresh_token.go`
- `backend/migrations/143_oauth_provider.sql`
- `backend/migrations/144_oauth_seed_sakrylle.sql`
- `frontend/src/views/user/AuthorizedAppsView.vue`

Existing behavior:

- `GET /oauth/authorize`
- `POST /oauth/token`
- `POST /api/v1/oauth/authorize/approve`
- `GET /api/v1/oauth/grants`
- `DELETE /api/v1/oauth/grants/:client_id`
- `GET /v1/account/balance`
- `sk_oauth_...` access tokens are stored as rows in `api_keys`.
- Refresh tokens are rotated and stored as SHA-256 hashes.
- Authorization codes are one-shot and stored as SHA-256 hashes.
- User API key list hides `sk_oauth_` keys.
- `/v1/*` does not yet enforce OAuth scopes at runtime.
- Authorized apps are grouped by `client_id`, not device/grant.

Current gaps:

- No OAuth access metadata table.
- No endpoint-level scope enforcement.
- No Device Authorization Flow.
- No token revocation endpoint.
- No authorization-server discovery endpoint.
- No app/device-level authorized-app management.
- Scope vocabulary is legacy and image-oriented: `image_generation`, `balance:read`, `models:read`.
- `api_keys.group_id` is single-valued, but ecosystem apps need group switching.

## 4. Goals

OAuth v2 must support:

- Sakrylle Web, based on Open WebUI, Sakrylle OAuth only.
- Sakrylle Chat, based on Kelivo desktop, Sakrylle OAuth only.
- Sakrylle CLI, Claude Code-like CLI, Sakrylle OAuth only.
- Sakrylle Desktop, GUI tooling for Sakrylle CLI, Sakrylle OAuth only.
- Sakrylle Mobile, based on Kelivo mobile, Sakrylle OAuth only.
- Image Playground, migrated from legacy image scopes.

Implementation goals:

- Keep existing gateway, billing, rate limit, Redis auth cache, usage log, and group behavior.
- Add OAuth runtime authorization without breaking manual API keys.
- Make every v2 behavior testable at service, route, and integration levels.
- Keep v1 Image Playground tokens working through alias and migration backfill.
- Let users revoke one device/session without revoking all devices for an app.
- Let clients discover endpoints and supported scopes from metadata.
- Let clients switch groups by using a refresh token to mint a new group-bound access token.

## 5. Non-Goals

Do not implement these in OAuth v2 phase 1:

- OIDC `id_token`, JWKS, `openid`, `profile`, or `email` claims.
- Third-party developer self-service client registration.
- `keys:manage` scope.
- Multi-group access tokens.
- Sender-constrained tokens such as DPoP or mTLS.
- Token introspection endpoint (RFC 7662). Sakrylle resource servers use direct DB/cache metadata lookup for opaque `sk_oauth_` tokens in phase 1.
- Fine-grained per-model scopes.

OIDC may be added later if first-party apps need standard login claims. OAuth v2 should still expose `/v1/me` for first-party account context.

## 6. Terminology

Access token:

`sk_oauth_...` bearer token stored in `api_keys`. It is short-lived and bound to one group.

Refresh token:

`rt_...` opaque token stored only as SHA-256 hash. It represents the right for one app/device grant to mint new access tokens.

Grant:

One user authorization for one client on one device/session. It has a server-generated `grant_id`.

Token family:

The rotation chain for one grant's refresh token. It has a `token_family_id`. Refresh reuse revokes the family.

Device:

Display context for a grant. For CLI/desktop/mobile it maps to an installed app/device. For Web it maps to a browser/server session.

Group:

Sakrylle API billing/model group. Access tokens remain single-group because `api_keys.group_id` is single-valued and already drives billing and model visibility.

## 7. OAuth v2 Scopes

### 7.1 Canonical Scope Registry

| Scope | Meaning | Default bundle |
|---|---|---|
| `profile:read` | User ID, username/display name, avatar, locale. No email. | Web, Chat, CLI, Desktop, Mobile |
| `email:read` | User email. Not granted by default. | none |
| `account:read` | Account context beyond balance: current/default group, allowed groups summary, quota/capability summary. | Web, Chat, CLI, Desktop, Mobile |
| `account:balance:read` | Balance and currency display only. | Image |
| `models:read` | List available models for the current group. | Web, Chat, CLI, Desktop, Mobile, Image |
| `chat.completions:create` | Call `/v1/chat/completions` aliases. | Web, Chat, Desktop, Mobile |
| `responses:create` | Call `/v1/responses` aliases, including Image Playground Agent conversations. | Web, Chat, CLI, Desktop, Mobile, Image |
| `messages:create` | Call `/v1/messages` and `/v1/messages/count_tokens`. | Web, Chat, CLI, Desktop, Mobile |
| `images:create` | Call `/v1/images/generations` and `/v1/images/edits` aliases. | Image |
| `usage:read` | Call `/v1/usage`. | Web, Chat, CLI, Desktop, Mobile |
| `offline_access` | Receive refresh tokens. | First-party bundles that need persistent login |

`offline_access` is not a browser-storage permission. A public SPA may request it only if the refresh token is never exposed to browser JavaScript, for example because a BFF holds tokens server-side. If Sakrylle Web ships as a pure SPA, its default bundle must omit `offline_access` and use short-lived access tokens plus re-auth.

### 7.2 Legacy Aliases

Legacy scopes must remain accepted during migration:

| Legacy scope | Canonical scope | Notes |
|---|---|---|
| `image_generation` | `images:create` | Existing Image Playground scope. |
| `balance:read` | `account:balance:read` | Existing balance endpoint scope; do not widen legacy Image Playground grants to full account context. |
| `models:read` | `models:read` | Already canonical. |

Rules:

- New v2 clients must request canonical scopes.
- `NormalizeScopes` must map aliases to canonical scopes before storage in new `oauth_access_tokens`.
- Enforcement must treat legacy scopes on old rows as equivalent to canonical scopes.
- Token responses must return canonical scopes for newly issued v2 tokens.
- Existing v1 Image Playground tokens must keep working after enforcement is enabled.
- Legacy aliases have a finite migration window. Sunset for new authorize requests is 90 days after the production deploy that enables `oauth_scope_enforcement_enabled=true`, recorded in release notes and external docs. After the sunset, new authorize requests containing `image_generation` or `balance:read` return `invalid_scope`, while already-issued tokens remain accepted until their refresh-token family expires or is revoked.

### 7.3 Endpoint Scope Matrix

Only `sk_oauth_` tokens are checked by this matrix. Manual API keys continue existing behavior.

| Endpoint | Required scope |
|---|---|
| `GET /v1/me` | `profile:read`, `account:read`, or `account:balance:read`; response fields are cropped by scope. |
| `GET /v1/account/balance` | `account:balance:read` or `account:read` |
| `GET /v1/models` | `models:read` |
| `GET /v1/usage` | `usage:read` |
| `POST /v1/chat/completions` | `chat.completions:create` |
| `POST /chat/completions` | `chat.completions:create` |
| `POST /v1/responses` | `responses:create` |
| `POST /v1/responses/*subpath` | `responses:create` |
| `GET /v1/responses` | `responses:create` |
| `POST /responses` | `responses:create` |
| `POST /responses/*subpath` | `responses:create` |
| `GET /responses` | `responses:create` |
| `POST /v1/codex/responses` | `responses:create` |
| `POST /v1/codex/responses/*subpath` | `responses:create` |
| `GET /v1/codex/responses` | `responses:create` |
| `POST /v1/messages` | `messages:create` |
| `POST /v1/messages/count_tokens` | `messages:create` |
| `POST /v1/images/generations` | `images:create` |
| `POST /v1/images/edits` | `images:create` |
| `POST /images/generations` | `images:create` |
| `POST /images/edits` | `images:create` |
| `GET /antigravity/models` | `models:read` |
| `GET /antigravity/v1/models` | `models:read` |
| `GET /antigravity/v1/usage` | `usage:read` |
| `POST /antigravity/v1/messages` | `messages:create` |
| `POST /antigravity/v1/messages/count_tokens` | `messages:create` |

Routes not listed in this matrix are denied for `sk_oauth_` tokens by default, even if a manual API key can call them today. This avoids accidentally expanding OAuth authority when new gateway aliases are added.

This is an implementation requirement, not only a policy statement. Every API-key-authenticated resource route must run through an OAuth-aware resource gate. Listed routes pass only when the token has the configured scope. Unlisted routes pass manual API keys but reject `sk_oauth_` tokens with `403 insufficient_scope` or `401 invalid_token` as appropriate. Do not implement the matrix only by adding middleware to known allowed routes; doing so would leave unlisted route groups reachable by OAuth tokens.

OAuth protocol and account-management endpoints are not resource-server routes and must not accept OAuth bearer authority unless explicitly documented. `POST /oauth/revoke` uses RFC 7009 client binding, `/.well-known/*` is public read-only discovery, and `DELETE /api/v1/oauth/authorized-apps/*` / compatibility grant deletes require JWT user auth plus step-up checks.

Gemini-native `/v1beta/*` is out of scope for the first v2 rollout and must reject OAuth tokens as an unlisted resource route. If later exposed to OAuth v2, add it to this matrix with a new scope such as `gemini:generate` and tests before enabling traffic.

## 8. Client Matrix

### 8.1 Client Types

`client_type` values:

- `public`: no client secret; must use PKCE for authorization code flow.
- `confidential`: has `client_secret_hash`; must authenticate at token/revoke endpoint.

`app_type` values:

- `web`
- `chat`
- `cli`
- `desktop`
- `mobile`
- `image`

`admin` and `unknown` are not phase-1 app types. Unknown legacy rows must be migrated to a concrete value or reported before enabling enforcement.

First-party client invariants:

- `client_type` is immutable after creation. Changing public/confidential status requires creating a new `client_id` and migrating clients deliberately.
- Authorization Code flow requires PKCE S256 for both public and confidential clients.
- `offline_access` on public clients is allowed only when the token storage model is native/CLI secure storage. Browser-only public clients must omit `offline_access` unless a documented BFF flag/client is used.
- Default UI bundles for Web, Chat, Desktop, and Mobile include the same user-facing account/model/chat/messages/usage scopes so product UX does not diverge accidentally.
- Native apps must use the system browser or platform auth session APIs. Embedded WebView login is forbidden: use ASWebAuthenticationSession on Apple platforms, Chrome Custom Tabs on Android, and the system browser with loopback on desktop.

### 8.2 First-Party Clients

These are Sakrylle production first-party client rows, not portable schema assumptions. Group names below are the product defaults; numeric IDs shown in examples are Sakrylle production values from current operations notes, not product-contract constants.

Implementation rule:

- The forward-only schema migration (`145_oauth_v2.sql`) must not fail a fresh or forked database merely because Sakrylle production groups are absent.
- Sakrylle first-party client seeding belongs in a Sakrylle-specific seed step, seed migration, or operator SQL that resolves group IDs by name or explicit operator override.
- If a required Sakrylle production group cannot be resolved during that seed step, fail the seed step closed or create the affected client disabled with no default group. Do not silently bind to the wrong numeric ID.
- Tests for generic migrations should assert schema compatibility. Tests for Sakrylle production seed data should run against fixtures that contain the expected group names or explicit overrides.

| Client ID | Name | Client type | App type | Flow | Default scopes | Default group | Allowed groups |
|---|---|---|---|---|---|---|---|
| `sakrylle-web` | Sakrylle Web | `confidential` for the preferred phase 1 BFF/server-side token store | `web` | Authorization Code + PKCE through BFF | `profile:read account:read models:read chat.completions:create responses:create messages:create usage:read offline_access` | GPT-Pro | `NULL` means capability-filtered user-allowed chat groups |
| `sakrylle-chat-desktop` | Sakrylle Chat | `public` | `chat` | Authorization Code + PKCE | Web bundle | GPT-Pro | `NULL` |
| `sakrylle-cli` | Sakrylle CLI | `public` | `cli` | Device Authorization Flow; optional loopback PKCE later | `profile:read account:read models:read responses:create messages:create usage:read offline_access` | Codex | `NULL` |
| `sakrylle-desktop` | Sakrylle Desktop | `public` | `desktop` | Authorization Code + PKCE | `profile:read account:read models:read chat.completions:create responses:create messages:create usage:read offline_access` | Codex | `NULL` |
| `sakrylle-mobile` | Sakrylle Mobile | `public` | `mobile` | Authorization Code + PKCE | Web bundle | GPT-Pro | `NULL` |
| `sakrylle-image-playground-v2` | Sakrylle Image Playground | `public` | `image` | Authorization Code + PKCE | `account:balance:read models:read images:create responses:create offline_access` | GPT-Image for Images API; response-capable group selected for Agent conversations over Responses API | GPT-Image plus configured response-capable groups, resolved by name/operator override |

If Sakrylle Web is deployed as a pure browser SPA instead of the BFF model, use a separate public client such as `sakrylle-web-spa`, remove `offline_access` from its default scopes, and never issue a browser-JS-readable persistent refresh token. The BFF model stores refresh tokens server-side and exposes only an HTTP-only, SameSite session cookie plus CSRF-protected app APIs to the browser.

Keep existing `sakrylle-image-playground`:

- Do not delete.
- Mark `app_type='image'`.
- Keep legacy scopes allowed.
- Add canonical scopes.
- Set `allow_refresh_without_offline_access=true` for compatibility until the Image Playground frontend migrates to request `offline_access`.

The v2 `app_type` for the existing `sakrylle-image-playground` is deliberately `image`, not `web`. Current code has no `app_type` column yet, but the existing seed registers `Sakrylle Image Playground` with `image_generation balance:read models:read` and GPT-Image group binding, so the v2 migration should classify it by capability/product role rather than by browser delivery mechanism. The app type does not mean Images API only: Image Playground v2 is allowed to request both `images:create` and `responses:create` so its gallery image flows can use Images API and its Agent conversation workspace can use Responses API under one Sakrylle OAuth grant.

### 8.3 Seed Redirect URIs and Origins

Production first-party seeds must contain only controlled HTTPS redirect URIs and origins. Localhost and loopback values belong to separate dev-only clients, disabled-by-default seed rows, or test fixtures; do not mix them into production client rows.

| Client ID | Redirect URIs | Allowed origins |
|---|---|---|
| `sakrylle-web` | `["https://web.sakrylle.com/oauth/callback"]` | `["https://web.sakrylle.com"]` |
| `sakrylle-chat-desktop` | `["http://127.0.0.1/oauth/callback", "http://[::1]/oauth/callback", "http://localhost/oauth/callback"]` | `[]` |
| `sakrylle-cli` | `[]` | `[]` |
| `sakrylle-desktop` | `["http://127.0.0.1/oauth/callback", "http://[::1]/oauth/callback", "http://localhost/oauth/callback"]` | `[]` |
| `sakrylle-mobile` | `["https://mobile.sakrylle.com/oauth/callback", "com.sakrylle.mobile:/oauth/callback"]` | `["https://mobile.sakrylle.com"]` |
| `sakrylle-image-playground-v2` | `["https://image.sakrylle.com/oauth/callback"]` | `["https://image.sakrylle.com"]` |
| `sakrylle-image-playground` | Keep existing values and add none automatically. | Derived from existing redirect URIs. |

Suggested dev rows, if seeded at all, should be clearly named (`sakrylle-web-dev`, `sakrylle-image-playground-dev`), disabled outside non-production, and may include `http://localhost:*` values only for local development. Final Product Decisions must not lock production behavior to localhost.

### 8.4 Redirect URI Rules

Web/SPA:

- Exact match only.
- No wildcard domains.
- No prefix match.

Desktop loopback:

- A registered redirect URI without an explicit port, such as `http://127.0.0.1/oauth/callback`, acts as the loopback template.
- Allow only registered host and path.
- Port may vary.
- Allowed examples:
  - `http://127.0.0.1:{dynamic_port}/oauth/callback`
  - `http://[::1]:{dynamic_port}/oauth/callback`
- `localhost` is accepted for native loopback redirects to match RFC 8252-compatible client libraries, alongside `127.0.0.1` and `[::1]`.
- Reject non-loopback hosts for dynamic-port matching.

Mobile:

- Prefer Universal Links / App Links.
- Custom scheme is fallback only, must use reverse-domain form such as `com.sakrylle.mobile:/oauth/callback`, and may be allowed only as a registered exact URI.

Desktop / Chat native:

- Prefer loopback + PKCE using `127.0.0.1`, `[::1]`, or `localhost`.
- Do not seed custom-scheme redirect URIs for desktop/chat clients in phase 1. Add them later only with a platform-specific interception analysis.
- Do not use broad product schemes such as `sakrylle://...` for new native clients.

CLI:

- Device Flow is the default.
- Loopback PKCE is optional for later.

## 9. Group Strategy

Do not make access tokens multi-group in v2. Access tokens stay bound to exactly one `api_keys.group_id`.

Group resolution:

1. If the authorization approval or device approval selected a group, use it.
2. Else use `oauth_clients.default_group_id`.
3. Else reject with `group_not_allowed`.

Do not use the global `settings.oauth_default_group_id` fallback for v2 clients. It remains only for legacy provider compatibility and must not decide a first-party ecosystem client's group silently.

Group validation:

- Group must exist.
- Group must be active.
- User must be allowed to use the group under existing group access rules.
- If `oauth_clients.allowed_group_ids` is `NULL`, there is no client-specific group restriction.
- If `oauth_clients.allowed_group_ids` is a JSON array, requested/default group must be in that array.
- Empty array means no group is allowed and the client is misconfigured.

Client/group capability filtering:

- `allowed_group_ids=NULL` means "no explicit ID allowlist", not "all groups are valid for every product".
- After ID allowlist and user access checks, filter groups by the effective granted scopes and group capability.
- Chat/Web/Desktop/Mobile clients that have chat/Responses/messages scopes but no `images:create` must not show or switch into image-only groups.
- Image clients with only `images:create` must only show groups where `groups.allow_image_generation=true` and the group has image-capable models.
- Image clients granted both `images:create` and `responses:create` may show both image-capable groups and response-capable groups, but each operation must still require a group that supports that operation. A token with `responses:create` bound to GPT-Image still cannot run an Agent conversation over Responses API if that group has no response-capable model, and a token bound to GPT-Pro still cannot call Images API unless the group allows image generation.
- Admin-only, disabled, or operational groups must be excluded unless a future admin client explicitly grants an admin scope.
- `/v1/me.allowed_groups` and `ResolveOAuthGroup` must use the same filtering helper so the UI cannot present a group that refresh later rejects.
- Group capability may come from explicit future group flags such as `allow_chat`, `allow_responses`, and `allow_messages`, or be derived from active `channel_model_pricing` rows and existing group flags. Whichever source is chosen must be shared by `/v1/me.allowed_groups` and token minting.

Group switching:

- `POST /oauth/token` with `grant_type=refresh_token` may include optional `group_id`.
- The refresh token represents the user/client/device grant.
- The newly minted access token binds to the requested group after validation.
- The requested group must be in the consented `allowed_groups_snapshot` stored on the grant/refresh row when the user approved the authorization. Refresh must not use the user's current broader group list to expand an already-consented grant.
- If a user's access to a previously consented group is later revoked, phase 1 rejects refresh into that group. Existing access tokens continue to be blocked by normal group/API-key checks if the group or user access becomes invalid.
- The old access token is disabled and auth cache is invalidated.
- Group switching is session-wide for that grant/family: any previously issued access token for the same grant is kicked out, even if another tab/device process still holds it.
- Clients must serialize refresh and group-switch operations per grant. Concurrent refreshes should be treated as a race; losers must discard their stale token and retry from the newest stored refresh token or re-auth if reuse detection revoked the family.
- `/v1/me` returns `allowed_groups` so clients can present group switching.

Authorization-code exchange:

- The initial group is selected during approval, not during `/oauth/token`.
- If `group_id` is sent to `/oauth/token` with `grant_type=authorization_code`, it must be ignored or rejected. To avoid silent surprises, implement rejection with `invalid_request`.

## 10. Data Model

Use a new forward-only migration:

```text
backend/migrations/145_oauth_v2.sql
```

Do not modify applied migrations `143_oauth_provider.sql` or `144_oauth_seed_sakrylle.sql`.

Schema policy:

- OAuth v2 follows the existing OAuth provider style and does not add hard database foreign keys between OAuth tables, `api_keys`, `users`, or `groups`.
- Use indexes plus service-level validation instead. This avoids FK conflicts with soft deletion, historical token retention, and cleanup jobs.
- Ent schemas must still model field types and indexes consistently with SQL migrations.

### 10.1 Extend `oauth_clients`

Add columns:

```sql
ALTER TABLE oauth_clients
    ADD COLUMN IF NOT EXISTS client_type VARCHAR(32) NOT NULL DEFAULT 'public',
    ADD COLUMN IF NOT EXISTS app_type VARCHAR(32) NOT NULL DEFAULT 'unknown',
    ADD COLUMN IF NOT EXISTS trusted_first_party BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS default_scopes JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS allowed_group_ids JSONB,
    ADD COLUMN IF NOT EXISTS allowed_origins JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS logout_redirect_uris JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS device_flow_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS allow_refresh_without_offline_access BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS icon_url TEXT,
    ADD COLUMN IF NOT EXISTS homepage_url TEXT,
    ADD COLUMN IF NOT EXISTS privacy_url TEXT,
    ADD COLUMN IF NOT EXISTS terms_url TEXT;
```

Application-level validation:

- `client_type` must be one of `public`, `confidential`.
- `app_type` must be one of the values in section 8.1.
- All Authorization Code clients must have `pkce_required=true`; this applies to public and confidential clients.
- `client_type` is write-once after insert. Admin UI, seeds, and service update methods must reject changing it in place.
- Confidential clients must have non-empty `client_secret_hash`.
- `device_flow_enabled=true` is allowed only for public clients unless a future confidential device client is explicitly designed.
- If `allowed_group_ids` is an array, every item must be an integer.
- If `allowed_group_ids` is an array, it must be non-empty. Empty arrays are invalid because they look intentional but make every grant fail.
- Add a database CHECK constraint mirroring the application rule: `allowed_group_ids IS NULL OR jsonb_array_length(allowed_group_ids) > 0`.
- If `default_group_id` is set and `allowed_group_ids` is an array, it must appear in `allowed_group_ids`.
- If `client_type='public'` and `default_scopes` contains `offline_access`, the client must be a native/CLI/app-shell client with documented secure storage, not a browser-only SPA. Browser BFF clients should be `confidential`.

### 10.2 Extend `oauth_codes`

Add columns:

```sql
ALTER TABLE oauth_codes
    ADD COLUMN IF NOT EXISTS group_id BIGINT,
    ADD COLUMN IF NOT EXISTS grant_id VARCHAR(64),
    ADD COLUMN IF NOT EXISTS allowed_groups_snapshot JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS device_id VARCHAR(128),
    ADD COLUMN IF NOT EXISTS device_name VARCHAR(200);
```

Meaning:

- `group_id`: group selected during approval.
- `grant_id`: generated at approval time and carried to token minting.
- `allowed_groups_snapshot`: group IDs the user saw and consented to for this grant after client/user/capability filtering.
- `device_id`: optional client-provided install/session identifier.
- `device_name`: sanitized display name.

New authorization codes must always have non-empty `grant_id` and non-empty `allowed_groups_snapshot`. Legacy rows created before migration may be consumed only through the compatibility path and must not mint broader group access than their stored `group_id`.

### 10.3 New `oauth_authorize_transactions`

Create a server-side transaction table for consent approval. This is the phase 1 requirement; do not replace it with a stateless signed nonce unless this document is deliberately revised with an equivalent replay and CSRF contract.

```sql
CREATE TABLE IF NOT EXISTS oauth_authorize_transactions (
    id BIGSERIAL PRIMARY KEY,
    transaction_id VARCHAR(96) NOT NULL UNIQUE,
    csrf_hash VARCHAR(64) NOT NULL,
    client_id VARCHAR(128) NOT NULL,
    redirect_uri TEXT NOT NULL,
    response_type VARCHAR(32) NOT NULL DEFAULT 'code',
    scopes JSONB NOT NULL DEFAULT '[]'::jsonb,
    allowed_groups_snapshot JSONB NOT NULL DEFAULT '[]'::jsonb,
    state TEXT NOT NULL,
    code_challenge VARCHAR(128) NOT NULL,
    code_challenge_method VARCHAR(10) NOT NULL DEFAULT 'S256',
    requested_group_id BIGINT,
    device_id VARCHAR(128),
    device_name VARCHAR(200),
    consumed_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL,
    created_ip VARCHAR(64),
    created_user_agent TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_oauth_authorize_transactions_expires_at
    ON oauth_authorize_transactions(expires_at);
CREATE INDEX IF NOT EXISTS idx_oauth_authorize_transactions_client_id
    ON oauth_authorize_transactions(client_id);
```

Rules:

- `transaction_id` is a random opaque value with at least 32 bytes of entropy before base64url encoding.
- The plaintext CSRF token is returned only in the rendered consent page; the table stores SHA-256 hex.
- The row stores the normalized authorize request after validation. Approval must not accept client ID, redirect URI, scopes, state, or PKCE fields from the browser body.
- On the logged-in consent render, compute and store `allowed_groups_snapshot` using the same filtering policy as `/v1/me.allowed_groups`. The consent group selector is rendered from that snapshot, not recomputed client-side.
- TTL is short; recommended 10 minutes, matching authorization code TTL.
- Approval and denial both mark `consumed_at` under row lock before returning a redirect target.
- Cleanup may delete expired or consumed rows after an operationally useful retention window.

### 10.4 Extend `oauth_refresh_tokens`

Add columns:

```sql
ALTER TABLE oauth_refresh_tokens
    ADD COLUMN IF NOT EXISTS grant_id VARCHAR(64),
    ADD COLUMN IF NOT EXISTS token_family_id VARCHAR(64),
    ADD COLUMN IF NOT EXISTS group_id BIGINT,
    ADD COLUMN IF NOT EXISTS allowed_groups_snapshot JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS device_id VARCHAR(128),
    ADD COLUMN IF NOT EXISTS device_name VARCHAR(200),
    ADD COLUMN IF NOT EXISTS last_used_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS reuse_detected_at TIMESTAMPTZ;
```

Backfill:

```sql
UPDATE oauth_refresh_tokens rt
SET
    grant_id = COALESCE(grant_id, 'legacy-grant-' || rt.id::text),
    token_family_id = COALESCE(token_family_id, 'legacy-family-' || rt.id::text),
    group_id = COALESCE(group_id, ak.group_id),
    allowed_groups_snapshot = CASE
        WHEN allowed_groups_snapshot IS NULL OR allowed_groups_snapshot = '[]'::jsonb THEN jsonb_build_array(ak.group_id)
        ELSE allowed_groups_snapshot
    END,
    device_name = COALESCE(device_name, 'Legacy session')
FROM api_keys ak
WHERE rt.api_key_id = ak.id
  AND (rt.grant_id IS NULL OR rt.token_family_id IS NULL OR rt.group_id IS NULL OR rt.device_name IS NULL);
```

After backfill, service code must require non-empty `grant_id`, `token_family_id`, `group_id`, and `allowed_groups_snapshot` for all newly created rows.

Indexes required by grant and family revocation:

```sql
CREATE INDEX IF NOT EXISTS idx_oauth_refresh_tokens_grant_id ON oauth_refresh_tokens(grant_id);
CREATE INDEX IF NOT EXISTS idx_oauth_refresh_tokens_token_family_id ON oauth_refresh_tokens(token_family_id);
CREATE INDEX IF NOT EXISTS idx_oauth_refresh_tokens_user_client_active
    ON oauth_refresh_tokens(user_id, client_id, expires_at)
    WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_oauth_refresh_tokens_grant_active
    ON oauth_refresh_tokens(grant_id, expires_at)
    WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_oauth_refresh_tokens_family_active
    ON oauth_refresh_tokens(token_family_id, expires_at)
    WHERE revoked_at IS NULL;
```

### 10.5 New `oauth_access_tokens`

Create table:

```sql
CREATE TABLE IF NOT EXISTS oauth_access_tokens (
    id BIGSERIAL PRIMARY KEY,
    api_key_id BIGINT NOT NULL UNIQUE,
    grant_id VARCHAR(64) NOT NULL,
    token_family_id VARCHAR(64) NOT NULL,
    client_id VARCHAR(128) NOT NULL,
    user_id BIGINT NOT NULL,
    scopes JSONB NOT NULL DEFAULT '[]'::jsonb,
    group_id BIGINT NOT NULL,
    allowed_groups_snapshot JSONB NOT NULL DEFAULT '[]'::jsonb,
    app_type VARCHAR(32) NOT NULL DEFAULT 'unknown',
    device_id VARCHAR(128),
    device_name VARCHAR(200),
    issued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ,
    last_used_ip VARCHAR(64),
    last_used_user_agent TEXT,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_oauth_access_tokens_client_id ON oauth_access_tokens(client_id);
CREATE INDEX IF NOT EXISTS idx_oauth_access_tokens_user_id ON oauth_access_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_oauth_access_tokens_grant_id ON oauth_access_tokens(grant_id);
CREATE INDEX IF NOT EXISTS idx_oauth_access_tokens_token_family_id ON oauth_access_tokens(token_family_id);
CREATE INDEX IF NOT EXISTS idx_oauth_access_tokens_expires_at ON oauth_access_tokens(expires_at);
CREATE INDEX IF NOT EXISTS idx_oauth_access_tokens_revoked_at ON oauth_access_tokens(revoked_at);
CREATE INDEX IF NOT EXISTS idx_oauth_access_tokens_user_active
    ON oauth_access_tokens(user_id, expires_at)
    WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_oauth_access_tokens_user_client_active
    ON oauth_access_tokens(user_id, client_id, expires_at)
    WHERE revoked_at IS NULL;
```

Backfill / reconciliation from active and historical `api_keys` rows:

- The reconciliation source of truth is `api_keys.key LIKE 'sk_oauth_%'`, not only `oauth_refresh_tokens`. Historical partial failures or rollback windows may have created an access key before refresh metadata was inserted.
- Prefer refresh-token metadata when present; otherwise generate conservative legacy metadata so the row can be audited and can pass or fail enforcement intentionally.
- For legacy `sakrylle-image-playground` rows with empty `scopes`, fill the legacy bundle `image_generation balance:read models:read` before normalization so old access tokens keep working until normal expiry.
- For legacy rows without `allowed_groups_snapshot`, backfill the snapshot to exactly `[api_keys.group_id]`. Do not infer all groups from the user's current permissions.
- Reconciliation must be idempotent and safe to run before enabling enforcement, after rollback, and immediately before re-enabling v2 after any rollback window.
- Before flipping `oauth_scope_enforcement_enabled=true`, operators must run a report for active `sk_oauth_` keys without access metadata and either reconcile, expire, or disable them.

Example reconciliation shape:

```sql
INSERT INTO oauth_access_tokens (
    api_key_id,
    grant_id,
    token_family_id,
    client_id,
    user_id,
    scopes,
    group_id,
    allowed_groups_snapshot,
    app_type,
    device_id,
    device_name,
    issued_at,
    expires_at,
    revoked_at,
    created_at,
    updated_at
)
SELECT
    ak.id,
    COALESCE(rt.grant_id, 'legacy-grant-ak-' || ak.id::text),
    COALESCE(rt.token_family_id, 'legacy-family-ak-' || ak.id::text),
    COALESCE(rt.client_id, 'sakrylle-image-playground'),
    ak.user_id,
    CASE
        WHEN rt.scopes IS NULL OR rt.scopes = '[]'::jsonb THEN '["image_generation", "balance:read", "models:read"]'::jsonb
        ELSE rt.scopes
    END,
    COALESCE(rt.group_id, ak.group_id),
    CASE
        WHEN rt.allowed_groups_snapshot IS NULL OR rt.allowed_groups_snapshot = '[]'::jsonb THEN jsonb_build_array(ak.group_id)
        ELSE rt.allowed_groups_snapshot
    END,
    COALESCE(oc.app_type, 'unknown'),
    rt.device_id,
    COALESCE(rt.device_name, 'Legacy session'),
    ak.created_at,
    COALESCE(ak.expires_at, rt.expires_at, ak.created_at + INTERVAL '24 hours'),
    CASE WHEN ak.status <> 'active' THEN NOW() ELSE NULL END,
    NOW(),
    NOW()
FROM api_keys ak
LEFT JOIN LATERAL (
    SELECT rt.*
    FROM oauth_refresh_tokens rt
    WHERE rt.api_key_id = ak.id
    ORDER BY rt.revoked_at NULLS FIRST, rt.updated_at DESC, rt.expires_at DESC, rt.id DESC
    LIMIT 1
) rt ON TRUE
LEFT JOIN oauth_clients oc ON oc.client_id = rt.client_id
WHERE ak.key LIKE 'sk_oauth_%'
  AND ak.group_id IS NOT NULL
ON CONFLICT (api_key_id) DO NOTHING;
```

Implementation note:

- If the actual `api_keys.status` values differ from `'active'`, use the constants represented by Ent/service values.
- Reconciliation must also emit a report of active `sk_oauth_` rows where both `api_keys.expires_at` and refresh-token expiry are missing. The example uses a 24-hour legacy grace window for auditability, but production operators must either accept that grace explicitly or disable those rows before enforcement. Do not use `NOW()` as a silent fallback expiry.
- The migration must be tested against a real local database before deploy.

### 10.6 New `oauth_device_codes`

Create table:

```sql
CREATE TABLE IF NOT EXISTS oauth_device_codes (
    id BIGSERIAL PRIMARY KEY,
    device_code_hash VARCHAR(64) NOT NULL UNIQUE,
    user_code_hash VARCHAR(64) NOT NULL,
    client_id VARCHAR(128) NOT NULL,
    scopes JSONB NOT NULL DEFAULT '[]'::jsonb,
    grant_id VARCHAR(64),
    group_id BIGINT,
    device_id VARCHAR(128),
    device_name VARCHAR(200),
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    interval_seconds INT NOT NULL DEFAULT 5,
    poll_count INT NOT NULL DEFAULT 0,
    last_poll_at TIMESTAMPTZ,
    slow_down_count INT NOT NULL DEFAULT 0,
    failed_user_code_attempts INT NOT NULL DEFAULT 0,
    approved_by_user_id BIGINT,
    approved_at TIMESTAMPTZ,
    denied_at TIMESTAMPTZ,
    consumed_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL,
    created_ip VARCHAR(64),
    created_user_agent TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_oauth_device_codes_client_id ON oauth_device_codes(client_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_oauth_device_codes_user_code_active_unique
    ON oauth_device_codes(user_code_hash)
    WHERE status IN ('pending', 'approved');
CREATE INDEX IF NOT EXISTS idx_oauth_device_codes_user_code_hash ON oauth_device_codes(user_code_hash);
CREATE INDEX IF NOT EXISTS idx_oauth_device_codes_expires_at ON oauth_device_codes(expires_at);
CREATE INDEX IF NOT EXISTS idx_oauth_device_codes_approved_by_user_id ON oauth_device_codes(approved_by_user_id);
CREATE INDEX IF NOT EXISTS idx_oauth_device_codes_status_expires_at ON oauth_device_codes(status, expires_at);
CREATE INDEX IF NOT EXISTS idx_oauth_device_codes_grant_id ON oauth_device_codes(grant_id);
```

Device code storage:

- Store SHA-256 hashes only.
- Plaintext `device_code` and `user_code` are returned once.
- `user_code` format: `SKRY-XXXX-XXXX`, uppercase characters from `BCDFGHJKMPQRTVWXY2346789` only. This is Crockford-style but removes vowels and ambiguous characters.
- `device_code` entropy: at least 32 random bytes before base64url encoding.
- `user_code` must have at least 40 bits of entropy.
- `status` must be one of `pending`, `approved`, `denied`, `consumed`, `expired`.
- Approval writes `grant_id` and changes status to `approved`; token exchange consumes only an approved, unconsumed row and changes status to `consumed`.
- Add database CHECK constraints where practical so state and timestamps cannot contradict each other, including `CHECK ((status='consumed') = (consumed_at IS NOT NULL))` plus equivalent approved/denied checks.
- `failed_user_code_attempts` increments only for unexpired matching rows when approval validation fails.
- A device code is denied after 5 failed approval attempts; future polls return `access_denied`.

### 10.7 New Settings

Add settings with `ON CONFLICT DO NOTHING`:

| Key | Default | Meaning |
|---|---|---|
| `oauth_issuer` | `https://sub.sakrylle.com` in production | Issuer used in discovery. Local dev may override. |
| `oauth_scope_enforcement_enabled` | `false` in migration, `true` only after smoke | Emergency kill-switch for OAuth scope enforcement. |
| `oauth_device_flow_enabled` | `true` | Global Device Flow toggle. |
| `oauth_v2_ui_enabled` | `false` during backend rollout, `true` after frontend smoke | Emergency kill-switch for the new Authorized Apps / consent UI surface. |

`oauth_scope_enforcement_enabled=false` must bypass scope checks but must not bypass normal API key authentication, billing, or group checks.

`backend/migrations/145_oauth_v2.sql` must seed `oauth_scope_enforcement_enabled=false` with `ON CONFLICT DO NOTHING`. The value `true` in production operations is the final post-smoke state, not the migration default.

No new environment variables are required for OAuth v2 phase 1. Runtime toggles live in the `settings` table so direct SQL can change them without rebuilding `/opt/stack/sub2api/.env`.

### 10.8 Cleanup Function

Add an idempotent cleanup function and invoke it from the existing scheduler pattern, `pg_cron`, or an external maintenance loop:

```sql
SELECT oauth_cleanup_expired();
```

It may delete expired/consumed rows from `oauth_authorize_transactions` and expired terminal `oauth_device_codes` after a short retention window. It must not hard-delete token rows needed for audit; expired/revoked `oauth_access_tokens` and `oauth_refresh_tokens` should be retained long enough for authorized-app history and security investigation, then pruned only by an explicit retention policy.

### 10.9 Reconciliation Command

Workstream A owns a small idempotent reconciliation command:

```text
backend/cmd/oauth-reconcile/main.go
```

Modes:

- `--report-only`: print counts and sample redacted IDs for active `sk_oauth_` rows missing access metadata, rows missing expiry source, legacy rows using fallback scopes, and rows that would be disabled. Exit non-zero if enforcement would be unsafe.
- `--apply`: perform the idempotent `oauth_access_tokens` backfill, write audit events for every fallback decision, and then print the same report.

Required behavior:

- Running `--apply` twice is a no-op on the second run except for report timestamps.
- Rows with no reliable expiry source use the explicit grace policy from section 10.5 only when the operator passes the documented apply mode; otherwise they are reported and enforcement stays blocked.
- The command must be safe after rollback windows where old code minted old-shape `sk_oauth_` rows.

## 11. Service Contracts

### 11.1 Scope Registry

Add a service-level registry, preferably in `backend/internal/service/oauth_scopes.go`:

```go
const (
    ScopeProfileRead       = "profile:read"
    ScopeEmailRead         = "email:read"
    ScopeAccountRead       = "account:read"
    ScopeAccountBalanceRead = "account:balance:read"
    ScopeModelsRead        = "models:read"
    ScopeChatCompletionsCreate   = "chat.completions:create"
    ScopeResponsesCreate   = "responses:create"
    ScopeMessagesCreate    = "messages:create"
    ScopeImagesCreate      = "images:create"
    ScopeUsageRead         = "usage:read"
    ScopeOfflineAccess     = "offline_access"
)
```

Required helpers:

```go
func ParseScopes(raw string) []string
func NormalizeScopes(scopes []string) []string
func ScopeAllowed(allowed, requested []string) bool
func HasScope(granted []string, required string) bool
func ScopeDisplay(scope string, locale string) string
func OAuthScopePolicyForRequest(method, path string) (required []string, listed bool)
```

`NormalizeScopes` must:

- Trim whitespace.
- Deduplicate while preserving stable order.
- Map `image_generation` to `images:create`.
- Map `balance:read` to `account:balance:read`.
- `HasScope(granted, required)` must normalize `granted` before comparison, so old stored aliases work even if rows were inserted before backfill.
- Reject unknown scopes during authorize unless they appear in a client's allowed scopes and are explicitly registered.

### 11.2 OAuth Access Metadata

Add service type:

```go
type OAuthAccessToken struct {
    ID             int64
    APIKeyID       int64
    GrantID        string
    TokenFamilyID  string
    ClientID       string
    UserID         int64
    Scopes         []string
    GroupID        int64
    AllowedGroupIDs []int64
    AppType        string
    DeviceID       *string
    DeviceName     *string
    IssuedAt       time.Time
    ExpiresAt      time.Time
    LastUsedAt     *time.Time
    LastUsedIP     *string
    LastUsedUA     *string
    RevokedAt      *time.Time
}
```

Repository interface:

```go
type OAuthAccessTokenRepository interface {
    CreateAccessToken(ctx context.Context, token *OAuthAccessToken) error
    GetActiveAccessTokenByAPIKeyID(ctx context.Context, apiKeyID int64, now time.Time) (*OAuthAccessToken, error)
    RevokeAccessTokenByAPIKeyID(ctx context.Context, apiKeyID int64, now time.Time) error
    RevokeAccessTokensByGrantID(ctx context.Context, grantID string, now time.Time) ([]int64, error)
    RevokeAccessTokensByTokenFamilyID(ctx context.Context, tokenFamilyID string, now time.Time) ([]int64, error)
    TouchAccessToken(ctx context.Context, apiKeyID int64, ip, userAgent string, now time.Time) error
    ListActiveGrantsByUser(ctx context.Context, userID int64, now time.Time) ([]*OAuthAuthorizedGrant, error)
}
```

API key mutation helpers required by OAuth revocation:

```go
type OAuthAPIKeyRepository interface {
    APIKeyRepository
    DisableAPIKeysByIDsReturningKeys(ctx context.Context, ids []int64, now time.Time) ([]string, error)
}
```

`DisableAPIKeysByIDsReturningKeys` must disable only matching `api_keys` rows and return the plaintext keys that were disabled or were already disabled but still need auth-cache invalidation. Grant, client, and family revoke paths use this to publish `apikey:auth:<sha>` invalidations without N+1 read/update loops.

Runtime invariants:

- For every live `sk_oauth_` request, OAuth metadata and the authenticated `api_keys` row must describe the same subject: `oauth_access_tokens.api_key_id == apiKeys.ID`, `user_id == apiKeys.User.ID`, and `group_id == apiKeys.Group.ID`.
- Both expiry clocks are authoritative. If either `api_keys.expires_at` or `oauth_access_tokens.expires_at` is expired, the request fails with `401 invalid_token`.
- Metadata mismatch is not a recoverable authorization fallback. Treat it as `401 invalid_token`, log a warning with redacted IDs, and require reconciliation or token reissue.
- Refresh, revoke, grant revoke, family revoke, and reconciliation code must update or invalidate both the backing `api_keys` row and `oauth_access_tokens` row. A live `sk_oauth_` API key without matching active metadata is invalid once migrations and reconciliation have completed.

Add a device-code repository:

```go
type OAuthDeviceCodeRepository interface {
    CreateDeviceCode(ctx context.Context, code *OAuthDeviceCode) error
    GetDeviceCodeByUserCodeHashForApproval(ctx context.Context, userCodeHash string, now time.Time) (*OAuthDeviceCode, error)
    PollDeviceCodeForUpdate(ctx context.Context, deviceCodeHash string, now time.Time) (*OAuthDeviceCode, error)
    ApproveDeviceCode(ctx context.Context, userCodeHash string, userID int64, groupID int64, now time.Time) error
    DenyDeviceCode(ctx context.Context, userCodeHash string, now time.Time) error
    MarkDeviceCodeConsumed(ctx context.Context, deviceCodeHash string, now time.Time) error
    IncrementDeviceCodeFailedAttempts(ctx context.Context, userCodeHash string, now time.Time) (int, error)
}
```

Refresh-token repository additions required by replay containment:

```go
type OAuthRefreshTokenRepository interface {
    // Existing methods omitted.
    GetRefreshTokenByHashForUpdate(ctx context.Context, tokenHash string, now time.Time) (*OAuthRefreshToken, error)
    RevokeRefreshTokensByGrantID(ctx context.Context, grantID string, now time.Time) ([]int64, error)
    RevokeRefreshTokensByTokenFamilyID(ctx context.Context, tokenFamilyID string, now time.Time) ([]int64, error)
    RevokeRefreshTokensByUserAndClient(ctx context.Context, userID int64, clientID string, now time.Time) ([]int64, error)
}
```

`GetRefreshTokenByHashForUpdate` must return revoked rows too. If `revoked_at != nil && rotated_to_hash != nil`, service code uses the row's `token_family_id` to revoke the whole family. Returning only `ErrOAuthRefreshTokenRevoked` without row data is not sufficient for v2.

Update `OAuthProviderService` construction and all dependency injection providers:

```go
func NewOAuthProviderService(
    clientRepo OAuthClientRepository,
    codeRepo OAuthCodeRepository,
    refreshRepo OAuthRefreshTokenRepository,
    accessRepo OAuthAccessTokenRepository,
    deviceRepo OAuthDeviceCodeRepository,
    apiKeyRepo APIKeyRepository,
    groupRepo GroupRepository,
    groupAccess GroupAccessPolicy,
    settingRepo SettingRepository,
    authCache APIKeyAuthCacheInvalidator,
) *OAuthProviderService
```

All tests that directly call `NewOAuthProviderService` must pass explicit stubs for the new repositories.

Errors:

- Missing access metadata for `sk_oauth_` token: treat as `invalid_token`.
- Revoked access metadata: `invalid_token`.
- Expired access metadata: `invalid_token`.

### 11.3 Grant IDs and Token Family IDs

Generate IDs in Go with `github.com/google/uuid`.

Rules:

- New authorization approval creates a new `grant_id`.
- New Device Flow approval creates a new `grant_id`.
- First access/refresh mint for a grant creates a new `token_family_id`.
- Refresh rotation preserves `grant_id` and `token_family_id`.
- Refresh reuse revokes all access and refresh rows with that `token_family_id`.
- User revoking one device revokes all access and refresh rows with that `grant_id`.
- User revoking all devices for one app revokes all rows matching `(user_id, client_id)`.

### 11.4 Group Validation Helper

Add one service function and reuse it everywhere tokens are minted:

```go
func ResolveOAuthGroup(ctx context.Context, userID int64, client *OAuthClient, requestedGroupID *int64) (int64, error)
```

It must implement section 9 exactly and must not duplicate stale group-access rules. Inject a policy that reuses the existing API-key group binding semantics, including exclusive groups and subscription groups:

```go
type GroupAccessPolicy interface {
    CanUserUseGroup(ctx context.Context, userID int64, group *Group) (bool, error)
    ListUserAllowedGroupsForOAuth(ctx context.Context, userID int64, client *OAuthClient, scopes []string) ([]Group, error)
}
```

`ResolveOAuthGroup` and `/v1/me.allowed_groups` must call the same policy. The policy may be backed by `UserRepository + UserSubscriptionRepository`, by `APIKeyService`, or by a small adapter around the existing `canUserBindGroup` rules, but it must not silently allow exclusive or subscription groups just because they are active.

### 11.4.1 Atomic Token Writes

Authorization-code exchange, refresh rotation, and device-code exchange must create or update these records atomically:

- `api_keys`
- `oauth_access_tokens`
- `oauth_refresh_tokens` when issued
- `oauth_codes` or `oauth_device_codes` consumed/used state

Implementation must use one database transaction through the repo's established transaction boundary, preferably `ent.Client.Tx(ctx)`, spanning all relevant rows. Do not ship a best-effort compensation branch for phase 1 token minting; partial token creation is exactly the failure mode v2 is removing. Startup reconciliation remains the only compatibility path for rows created by older code or rollback windows.

### 11.5 Refresh Token Rules

Refresh tokens are issued only when either:

- `offline_access` is in the effective granted scopes, or
- `oauth_clients.allow_refresh_without_offline_access=true` for legacy compatibility.

If neither condition is true:

- Token response must omit `refresh_token`.
- The service must not create `oauth_refresh_tokens`.
- The access token still works until `expires_at`.

For the current ecosystem clients, persistent-login bundles include `offline_access` only when the client has an acceptable refresh-token storage model: BFF/confidential storage for Web, OS/mobile secure storage for native clients, or CLI secret storage. A pure browser SPA bundle must omit `offline_access` by default.

Refresh lifetime contract:

- Each refresh token row has an absolute expiry from `oauth_clients.refresh_token_ttl_seconds`.
- Token-family/session lifetime is bounded by the first token's absolute expiry unless a later migration adds an explicit `max_session_lifetime_seconds` column.
- Idle expiry is optional for phase 1. If implemented, it must be stored server-side and enforced in addition to absolute expiry.
- Rotation does not extend a family beyond its absolute lifetime. In phase 1, the replacement refresh-token row must inherit the old row's `expires_at`; it must not use `now + refresh_token_ttl_seconds` on every rotation.
- Token responses may include `refresh_token_expires_in` when a refresh token is issued. If present, it is seconds until the server-enforced absolute refresh expiry.
- Refresh exchange must verify that the refresh token row belongs to the submitted or authenticated `client_id`. A public client `client_id` identifies the client; it is not authentication.

### 11.6 Last Used Updates

`apiKeyService.TouchLastUsed` already updates `api_keys.last_used_at`.

OAuth v2 additionally needs `oauth_access_tokens.last_used_at`, IP, and User-Agent for authorized-app display.

Rules:

- Update OAuth access metadata only for `sk_oauth_` tokens.
- Throttle writes per token to avoid updating on every request. Recommended threshold: update if `last_used_at` is nil or older than 60 seconds.
- Do not block gateway requests on a failed OAuth last-used update; log at warn level with redacted identifiers.

## 12. HTTP API Contracts

### 12.1 Discovery

Endpoint:

```text
GET /.well-known/oauth-authorization-server
```

Response:

```json
{
  "issuer": "https://sub.sakrylle.com",
  "authorization_endpoint": "https://sub.sakrylle.com/oauth/authorize",
  "token_endpoint": "https://sub.sakrylle.com/oauth/token",
  "revocation_endpoint": "https://sub.sakrylle.com/oauth/revoke",
  "device_authorization_endpoint": "https://sub.sakrylle.com/oauth/device/code",
  "userinfo_endpoint": "https://sub.sakrylle.com/v1/me",
  "response_types_supported": ["code"],
  "response_modes_supported": ["query"],
  "ui_locales_supported": ["zh-CN", "en"],
  "grant_types_supported": [
    "authorization_code",
    "refresh_token",
    "urn:ietf:params:oauth:grant-type:device_code"
  ],
  "code_challenge_methods_supported": ["S256"],
  "token_endpoint_auth_methods_supported": ["none", "client_secret_basic", "client_secret_post"],
  "revocation_endpoint_auth_methods_supported": ["none", "client_secret_basic", "client_secret_post"],
  "scopes_supported": [
    "profile:read",
    "email:read",
    "account:read",
    "account:balance:read",
    "models:read",
    "chat.completions:create",
    "responses:create",
    "messages:create",
    "images:create",
    "usage:read",
    "offline_access"
  ],
  "service_documentation": "https://doc.sakrylle.com/developers/oauth/"
}
```

Headers:

```text
Cache-Control: public, max-age=60
Content-Type: application/json
```

Use `Cache-Control: public, max-age=60` in production for phase 1 so endpoint metadata can be rolled back quickly. The `issuer` value is exactly `https://sub.sakrylle.com` with no trailing slash.

Integration tests must assert `Content-Type: application/json` for `GET /.well-known/oauth-authorization-server`. Returning embedded SPA HTML from this path is a release blocker.

Sensitive OAuth responses:

- `/oauth/token`, `/oauth/revoke`, and `/oauth/device/code` success and error responses must set `Cache-Control: no-store`, `Pragma: no-cache`, and `Content-Type: application/json` unless the endpoint deliberately returns an empty body.
- Token and device endpoints must reject non-`application/x-www-form-urlencoded` requests with `invalid_request`. Do not rely on `ParseForm` accepting JSON or an empty content type.
- JSON request bodies are not accepted by `/oauth/token`, `/oauth/revoke`, or `/oauth/device/code` in phase 1.

### 12.2 Authorization Endpoint

Endpoint:

```text
GET /oauth/authorize
POST /oauth/authorize
```

Both GET and POST are supported. POST accepts the same parameters as `application/x-www-form-urlencoded` body fields. JSON authorize requests are not accepted.

Required query parameters:

- `client_id`
- `redirect_uri`
- `response_type=code`
- `state`
- `code_challenge`
- `code_challenge_method=S256`

Optional query parameters:

- `scope`: space-delimited scopes. If empty, use `oauth_clients.default_scopes`.
- `group_id`: requested initial group.
- `device_id`: client-generated stable installation/session ID. Max 128 chars.
- `device_name`: display name. Max 200 chars after sanitization.

Validation:

- Provider must be enabled.
- Client must exist and not be disabled.
- Redirect URI must match section 8.4.
- Requested scopes after normalization must be allowed by the client.
- Every client, public and confidential, must use PKCE S256.
- `code_challenge_method=plain`, a missing `code_challenge_method`, or missing `code_challenge` returns `invalid_request`.
- PKCE verifier/challenge contract:
  - `code_verifier` is 43 to 128 characters.
  - Allowed verifier characters are RFC 7636 unreserved characters only: `A-Z`, `a-z`, `0-9`, `-`, `.`, `_`, `~`.
  - Client verifier generation must provide at least 256 bits of entropy. The server validates length, charset, and S256 challenge equality; it must not pretend to measure randomness from the submitted string.
  - `code_challenge` for `S256` is `BASE64URL-ENCODE(SHA256(ASCII(code_verifier)))` with no padding.
- `state` is mandatory.
- Unknown authorize parameters are ignored unless this document names them as required or explicitly unsupported. `prompt=none` is unsupported in phase 1 and returns `interaction_required` through the RFC-compatible error path.
- The authorize endpoint may create a transaction before login, but consent rendering and group snapshot creation happen only after JWT identity is available. If the user is not logged in, redirect to the normal login page with a server-side return transaction ID; do not serialize the full authorize request into a browser-visible login redirect URL.

Safe redirect behavior:

- Invalid `client_id`, disabled client, or invalid `redirect_uri`: render inline HTML error; do not redirect.
- Other authorization errors: redirect to validated `redirect_uri` with OAuth error params and original `state`.

Consent page:

- Must show app name, icon if present, scopes with human-readable descriptions, requested/default group, and device name.
- If user may choose among multiple groups, show a group selector.
- The authorize GET must create a server-side authorization transaction in `oauth_authorize_transactions` with short TTL and return only the opaque `transaction_id` plus plaintext CSRF token to the rendered page. The table stores the CSRF hash and the validated authorize request fields.
- The approve POST must include only `transaction_id`, `decision`, optional selected `group_id`, and a CSRF token bound to the transaction. It must not trust a resubmitted copy of all authorize parameters from the browser.
- Consent HTML responses must set `Cache-Control: no-store, must-revalidate`, `Pragma: no-cache`, `Referrer-Policy: no-referrer`, and `Content-Security-Policy: frame-ancestors 'none'`.

### 12.3 Approval Endpoint

Endpoint:

```text
POST /api/v1/oauth/authorize/approve
```

Auth:

- Existing JWT user auth.

Body:

```json
{
  "transaction_id": "opaque-authorize-transaction-id",
  "decision": "approve",
  "group_id": 3,
  "csrf_token": "authorize-page-csrf"
}
```

Rules:

- Load the server-side authorization transaction by `transaction_id`; reject missing, expired, consumed, or mismatched transactions.
- Verify CSRF token against the transaction using constant-time comparison. The CSRF token is bound to both the transaction ID and authenticated JWT subject; also check `Origin`/`Referer` as defense in depth when present.
- Re-run client enabled, redirect URI, scope, and PKCE-method validation from the stored transaction before issuing a code.
- Resolve and validate group using the authenticated user and the transaction's `allowed_groups_snapshot`; reject selected groups outside that snapshot.
- On `decision=deny`, return `redirect_to` with `error=access_denied`.
- Mark the transaction consumed for both approve and deny decisions.
- On approve, create one authorization code with:
  - code hash
  - normalized scopes from the transaction
  - user ID
  - client ID from the transaction
  - redirect URI from the transaction
  - PKCE challenge from the transaction
  - group ID
  - grant ID
  - allowed groups snapshot
  - device fields from the transaction
- Authorization code TTL: 10 minutes.

### 12.4 Token Endpoint: Authorization Code

Endpoint:

```text
POST /oauth/token
Content-Type: application/x-www-form-urlencoded
```

Body:

```text
grant_type=authorization_code
code=<code>
redirect_uri=<same redirect URI>
client_id=<client ID>
code_verifier=<PKCE verifier>
```

Confidential clients may use HTTP Basic auth or `client_secret` form field.

Rules:

- Require `Content-Type: application/x-www-form-urlencoded`; reject JSON, missing, or unsupported content types.
- Parse only form-encoded body.
- Verify client. Public clients are identified by `client_id`; confidential clients authenticate with Basic auth or `client_secret`.
- If HTTP Basic auth and form `client_id` are both present, they must identify the same client or the request fails with `invalid_request`. Prefer Basic credentials when they match.
- Consume authorization code under row lock.
- Reject reused, expired, or missing code.
- Code replay must revoke the prior grant if the row is known.
- Verify code row `client_id` matches the identified/authenticated client.
- Verify redirect URI equals code row.
- Verify PKCE with constant-time comparison.
- Reject `group_id` extension parameter with `invalid_request`.
- Mint access token:
  - create `api_keys` row with `sk_oauth_` token
  - create `oauth_access_tokens` row
  - create `oauth_refresh_tokens` row only when refresh is allowed
- Return `Cache-Control: no-store`, `Pragma: no-cache`, and `Content-Type: application/json`.

Response:

```json
{
  "access_token": "sk_oauth_...",
  "token_type": "Bearer",
  "expires_in": 3600,
  "refresh_token": "rt_...",
  "refresh_token_expires_in": 2592000,
  "scope": "profile:read account:read models:read offline_access"
}
```

If no refresh token is issued, omit `refresh_token` and `refresh_token_expires_in`.

Implementation function:

```go
func (h *OAuthProviderHandler) tokenAuthorizationCode(c *gin.Context)
```

### 12.5 Token Endpoint: Refresh Token

Body:

```text
grant_type=refresh_token
refresh_token=<rt_...>
client_id=<client ID>
group_id=<optional target group>
```

Rules:

- Require `Content-Type: application/x-www-form-urlencoded`; reject JSON, missing, or unsupported content types.
- Verify client. Public clients are identified by `client_id`; confidential clients authenticate with Basic auth or `client_secret`.
- Lock refresh row with `FOR UPDATE`.
- If row was already revoked and `rotated_to_hash` is set, treat as reuse before any ordinary revoked-token rejection:
  - set `reuse_detected_at`
  - revoke all refresh rows with `token_family_id`
  - revoke all access metadata rows with `token_family_id`
  - disable all associated `api_keys`
  - publish auth-cache invalidation for every disabled plaintext access token that can be loaded
  - return `invalid_grant`
- Reject missing, expired, revoked-without-rotation, wrong-client, or consumed tokens.
- Resolve `group_id` using section 9 and the refresh row's `allowed_groups_snapshot`; a requested group outside the snapshot fails with RFC `invalid_grant` and `error_description="group not allowed for this grant"`.
- Mark old refresh revoked and set `rotated_to_hash`.
- Disable old access `api_keys` row.
- Revoke old `oauth_access_tokens` row.
- Publish auth-cache invalidation for old access token.
- Mint new `sk_oauth_` access token and metadata row.
- Mint new refresh token preserving `grant_id` and `token_family_id`.
- Return `Cache-Control: no-store`, `Pragma: no-cache`, and `Content-Type: application/json`.

Client rule:

- Any refresh error is terminal for that refresh token. Clients must clear local tokens and re-authorize.

Implementation functions:

```go
func (h *OAuthProviderHandler) tokenRefresh(c *gin.Context)
func (s *OAuthProviderService) RefreshAccessToken(ctx context.Context, clientID, clientSecret, refreshTokenPlain string, requestedGroupID *int64) (*IssuedToken, error)
```

### 12.6 Token Endpoint: Device Code

Body:

```text
grant_type=urn:ietf:params:oauth:grant-type:device_code
device_code=<device_code>
client_id=<client ID>
```

Rules:

- Require `Content-Type: application/x-www-form-urlencoded`; reject JSON, missing, or unsupported content types.
- Verify client exists, not disabled, and has `device_flow_enabled=true`. Public clients are identified by `client_id`; confidential clients authenticate with Basic auth or `client_secret` when configured.
- Look up device code by hash.
- Verify device code row `client_id` matches the identified/authenticated client.
- Enforce polling interval.
- If polled too quickly, return `slow_down` and increase interval by 5 seconds.
- If pending, return `authorization_pending`.
- If denied, return `access_denied`.
- If expired, return `expired_token`.
- If approved and unconsumed, consume once and mint tokens.
- On successful exchange, mark device code consumed.
- Return `Cache-Control: no-store`, `Pragma: no-cache`, and `Content-Type: application/json` for every response.

Error examples:

```json
{ "error": "authorization_pending", "error_description": "authorization pending" }
```

```json
{ "error": "slow_down", "error_description": "polling too quickly" }
```

Implementation functions:

```go
func (h *OAuthProviderHandler) tokenDeviceCode(c *gin.Context)
func (s *OAuthProviderService) ExchangeDeviceCode(ctx context.Context, clientID, clientSecret, deviceCodePlain string) (*IssuedToken, error)
```

### 12.7 Device Authorization Endpoint

Endpoint:

```text
POST /oauth/device/code
Content-Type: application/x-www-form-urlencoded
```

Body:

```text
client_id=sakrylle-cli
scope=profile:read account:read models:read responses:create messages:create usage:read offline_access
group_id=7
device_id=optional-install-id
device_name=Ariel%27s%20MacBook%20Pro
```

Validation:

- Require `Content-Type: application/x-www-form-urlencoded`; reject JSON, missing, or unsupported content types.
- Verify client exists, is not disabled, and has `device_flow_enabled=true`.
- If `scope` is empty, use `oauth_clients.default_scopes`.
- Normalize requested scopes and reject unknown or client-disallowed scopes with `invalid_scope`.
- If `group_id` is provided, perform a client-level precheck against `oauth_clients.default_group_id` and `allowed_group_ids`; final user-level resolution happens only during approval.
- Store normalized scopes, requested group, device ID/name, client ID, and expiry on the device-code row.

Response:

```json
{
  "device_code": "opaque-device-code",
  "user_code": "SKRY-7QKJ-2M8P",
  "verification_uri": "https://sub.sakrylle.com/oauth/device",
  "verification_uri_complete": "https://sub.sakrylle.com/oauth/device?user_code=SKRY-7QKJ-2M8P",
  "expires_in": 600,
  "interval": 5
}
```

Client UX rule:

- Clients must display `verification_uri` and `user_code` even when `verification_uri_complete` is present. `verification_uri_complete` is a convenience link/QR target, not a replacement for showing the code.

Rate limits:

- `POST /oauth/device/code`: 10/min/IP and 30/min/client fail-close.
- `POST /oauth/token` device polling: existing token rate limit plus per-device-code interval enforcement.
- User-code lookup/approval: 5 attempts per 15 minutes per IP/user, and lock/deny after 10 failed attempts for the code globally.

Implementation functions:

```go
func (h *OAuthProviderHandler) DeviceCode(c *gin.Context)
func (s *OAuthProviderService) CreateDeviceCode(ctx context.Context, req *DeviceCodeRequest) (*IssuedDeviceCode, error)
```

### 12.8 Device Verification Page and Approval

Endpoints:

```text
GET  /oauth/device
POST /api/v1/oauth/device/approve
```

`GET /oauth/device`:

- Renders a page to enter or confirm `user_code`.
- If `user_code` query param exists, prefill it.
- Requires logged-in user before approval; unauthenticated users must be sent through normal login and return to the device page.
- Is rendered by the Go OAuth handler, not the Vue SPA. It stays behind the `/oauth/` embedded-SPA bypass, uses server-generated CSRF, and must set `Referrer-Policy: no-referrer`, `Cache-Control: no-store, must-revalidate`, and `Content-Security-Policy: frame-ancestors 'none'`.

`POST /api/v1/oauth/device/approve` body:

```json
{
  "user_code": "SKRY-7QKJ-2M8P",
  "decision": "approve",
  "group_id": 7,
  "csrf_token": "device-page-csrf"
}
```

Rules:

- Verify CSRF using constant-time comparison and bind it to the authenticated JWT subject plus the submitted user code. Also check `Origin`/`Referer` when present.
- Normalize user code.
- Compare code hash.
- Reject expired, consumed, denied, or already approved code.
- Re-resolve stored scopes and requested/selected group against the authenticated user. A group accepted by client-level precheck at device-code creation can still be rejected here if the user cannot use it.
- Store `approved_by_user_id`, `approved_at`, and selected `group_id`.
- Denial stores `denied_at`.
- Five failed approval attempts deny the device code and future polls return `access_denied`.

Implementation functions:

```go
func (h *OAuthProviderHandler) DeviceApprovePage(c *gin.Context)
func (h *OAuthProviderHandler) ApproveDevice(c *gin.Context)
func (s *OAuthProviderService) ApproveDeviceCode(ctx context.Context, userID int64, userCode, csrfToken string, groupID *int64) error
func (s *OAuthProviderService) DenyDeviceCode(ctx context.Context, userID int64, userCode, csrfToken string) error
```

### 12.9 Token Revocation

Endpoint:

```text
POST /oauth/revoke
Content-Type: application/x-www-form-urlencoded
```

Body:

```text
token=<access or refresh token>
token_type_hint=refresh_token
client_id=<client ID>
client_secret=<optional for confidential clients>
```

Rules:

- Require `Content-Type: application/x-www-form-urlencoded`; reject JSON, missing, or unsupported content types.
- Rate limit `POST /oauth/revoke` at 30/min/IP and 120/min/token fingerprint fail-closed. RFC 7009 idempotency still applies after a request passes rate limiting.
- Public clients are identified by `client_id`; `client_id` is not authentication.
- Confidential clients must authenticate with secret.
- RFC 7009 idempotency: unknown token, already revoked token, wrong token/client binding, or token not owned by the requesting client returns HTTP 200 success with an empty body. Do not reveal whether a token exists for a different client.
- If the token is known and belongs to a confidential client, the authenticated client must match the token's `client_id`.
- If the token is known and belongs to a public client, the submitted `client_id` must match the token's `client_id`.
- For refresh tokens:
  - revoke the entire grant (`grant_id`) because user intent is logout for this device/session
  - revoke active refresh rows for grant
  - revoke active access metadata for grant
  - disable associated `api_keys`
  - invalidate auth cache for every disabled access token loaded
- For access tokens:
  - revoke only that access token and disable its `api_keys` row
  - clients should send refresh token for complete logout
  - do not revoke the refresh token or grant unless the submitted token is a refresh token

Response:

```text
HTTP 200
Cache-Control: no-store
Pragma: no-cache
```

Response body must be empty. Tests should assert HTTP 200, `Cache-Control: no-store`, `Pragma: no-cache`, and no JSON body.

Implementation function:

```go
func (h *OAuthProviderHandler) Revoke(c *gin.Context)
```

### 12.10 Authorized Apps API

Replace current client-level grants API with app/device-level grants while keeping compatibility aliases.

New endpoints:

```text
GET    /api/v1/oauth/authorized-apps
DELETE /api/v1/oauth/authorized-apps/:grant_id
DELETE /api/v1/oauth/authorized-apps/client/:client_id
```

Compatibility endpoints:

```text
GET    /api/v1/oauth/grants
DELETE /api/v1/oauth/grants/:client_id
```

Compatibility behavior:

- `GET /api/v1/oauth/grants` may return the old aggregate shape until frontend migration is complete.
- After frontend migration, keep it as a wrapper over client aggregates for one release.
- `DELETE /api/v1/oauth/grants/:client_id` revokes all devices for that client, matching old behavior.

Auth and ownership:

- All Authorized Apps endpoints require existing JWT user auth; OAuth bearer tokens are not sufficient for managing grants.
- Mutating Authorized Apps endpoints require step-up authentication: recent password/MFA confirmation within an implementation-defined short window, or an existing equivalent high-risk-action guard. Phase 1 may gate this behind the same UX used for other sensitive account actions, but it must not be callable by a stale browser session alone.
- Every list and revoke query must include `WHERE user_id = current_user.id` or an equivalent join through token rows owned by the current user.
- `GET /api/v1/oauth/authorized-apps` returns only grants for the current user.
- `DELETE /api/v1/oauth/authorized-apps/:grant_id` revokes only when the grant belongs to the current user. A missing or other-user `grant_id` returns `404` with the normal API error shape.
- `DELETE /api/v1/oauth/authorized-apps/client/:client_id` and `DELETE /api/v1/oauth/grants/:client_id` revoke only rows matching `(current_user.id, client_id)`. Unknown or other-user client IDs are idempotent and may return `200` with zero revoked rows.
- Never expose another user's `grant_id`, device metadata, scopes, IPs, or token counts through aggregate joins.

New response:

```json
{
  "items": [
    {
      "grant_id": "uuid",
      "client_id": "sakrylle-cli",
      "client_name": "Sakrylle CLI",
      "client_disabled": false,
      "app_type": "cli",
      "icon_url": "https://sub.sakrylle.com/static/sakrylle-icon-192.png",
      "device_id": "optional-client-device-id",
      "device_name": "Ariel's MacBook Pro",
      "group_id": 7,
      "group_name": "Codex",
      "scopes": ["profile:read", "account:read", "models:read", "responses:create", "messages:create", "usage:read", "offline_access"],
      "first_authorized_at": "2026-05-27T12:00:00Z",
      "last_used_at": "2026-05-27T12:30:00Z",
      "last_used_ip": "203.0.113.1",
      "active_access_token_count": 1,
      "active_refresh_token_count": 1,
      "status": "active"
    }
  ]
}
```

### 12.11 `/v1/me`

Endpoint:

```text
GET /v1/me
Authorization: Bearer sk_oauth_...
```

Manual API keys may use `/v1/me` too, but response must omit OAuth grant metadata when token is not OAuth.

Scope behavior:

- If token has `profile:read`, include `user`.
- If token has `email:read`, include `user.email`.
- If token has `account:balance:read`, include only balance and currency-display account fields.
- If token has `account:read`, include `account`, `current_group`, `allowed_groups`, `granted_scopes`, and `effective_capabilities`.
- If token has none of `profile:read`, `account:balance:read`, or `account:read`, return `403 insufficient_scope`.

Manual API-key behavior:

- Manual API keys are not scope-limited. If the authenticated user and key pass existing API-key auth, `/v1/me` returns the same account/current-group shape needed by clients, plus `auth_type: "api_key"`.
- Manual API-key responses omit `oauth`, `granted_scopes`, and OAuth grant/device fields.
- `allowed_groups` for manual keys may be omitted in phase 1 unless the implementation can reuse the same group-access policy without weakening existing API-key semantics. If included, it must be computed from the same user/group access rules used for key binding.
- `effective_capabilities` for manual keys is derived from the current `api_keys.group_id`, group feature flags, and gateway capabilities, not from OAuth scopes.

Field map:

| Field family | Required OAuth scope | Manual API key behavior |
|---|---|---|
| `user.id`, `user.username`, `user.display_name`, `user.avatar_url`, `user.locale` | `profile:read` | Included when existing auth succeeds. |
| `user.email` | `email:read` | Omitted unless a future manual-key account endpoint deliberately exposes it. |
| `account.credit_remaining`, currency display | `account:balance:read` or `account:read` | Included when existing auth succeeds. |
| `current_group`, `allowed_groups`, quota/capability summary | `account:read` | May be included only if computed from existing group binding rules. |
| `api_keys[]`, billing records, admin fields | Never exposed to OAuth tokens | Not included in `/v1/me`; use existing account/admin APIs. |

`allowed_groups` must be filtered by section 9 client/group capability rules. A chat client must not see GPT-Image-only groups just because `allowed_group_ids` is `NULL`, and an image client must see only groups that support at least one of its granted operation scopes. For Image Playground v2, that means GPT-Image-style groups for Images API and response-capable groups for Responses API; admin-only groups remain hidden.

Expose two concepts separately:

- `granted_scopes`: canonical OAuth scopes actually granted to this token.
- `effective_capabilities`: what the token can do after combining scopes, current group properties, and gateway feature flags. For example, `images_create` is false when the token has `images:create` but the current group has `allow_image_generation=false`.

Response:

```json
{
  "user": {
    "id": 123,
    "username": "alice",
    "display_name": "alice",
    "avatar_url": null,
    "locale": "zh-CN"
  },
  "account": {
    "credit_remaining": 8.94,
    "currency_display": "CNY",
    "currency_symbol": "￥"
  },
  "current_group": {
    "id": 3,
    "name": "GPT-Pro",
    "rate_multiplier": 0.4,
    "allow_image_generation": false
  },
  "allowed_groups": [
    {
      "id": 3,
      "name": "GPT-Pro",
      "rate_multiplier": 0.4,
      "allow_image_generation": false,
      "is_default": true
    }
  ],
  "granted_scopes": ["profile:read", "account:read", "models:read", "chat.completions:create", "responses:create", "messages:create", "offline_access"],
  "effective_capabilities": {
    "profile_read": true,
    "email_read": false,
    "account_read": true,
    "models_read": true,
    "chat_completions_create": true,
    "responses_create": true,
    "messages_create": true,
    "images_create": false,
    "usage_read": false,
    "offline_access": true
  },
  "oauth": {
    "client_id": "sakrylle-web",
    "app_type": "web",
    "grant_id": "uuid",
    "device_name": "Web session",
    "expires_at": "2026-05-27T13:00:00Z"
  }
}
```

Do not include:

- API keys.
- Billing records.
- Full email unless `email:read`.
- Admin-only fields.

### 12.12 OAuth Error Format

OAuth endpoints return:

```json
{
  "error": "invalid_grant",
  "error_description": "authorization code expired",
  "error_uri": "https://doc.sakrylle.com/developers/oauth/errors#invalid_grant",
  "request_id": "optional-request-id"
}
```

Resource endpoints with OAuth failures return Bearer-compatible responses:

```http
HTTP/1.1 403 Forbidden
WWW-Authenticate: Bearer error="insufficient_scope", scope="images:create"
Content-Type: application/json
```

```json
{
  "error": "insufficient_scope",
  "error_description": "required scope: images:create"
}
```

Bearer error contract for gateway/resource endpoints:

| Condition | HTTP | `WWW-Authenticate` | JSON error |
|---|---:|---|---|
| Missing bearer/API key where OAuth auth is required | 401 | `Bearer error="invalid_token", error_description="missing bearer token"` | `invalid_token` |
| Malformed or unknown OAuth access token | 401 | `Bearer error="invalid_token"` | `invalid_token` |
| OAuth access metadata missing for `sk_oauth_` key | 401 | `Bearer error="invalid_token"` | `invalid_token` |
| Expired OAuth access token | 401 | `Bearer error="invalid_token", error_description="token expired"` | `invalid_token` |
| Revoked OAuth access token | 401 | `Bearer error="invalid_token", error_description="token revoked"` | `invalid_token` |
| Valid token missing required scope | 403 | `Bearer error="insufficient_scope", scope="<required scopes>"` | `insufficient_scope` |

Implementation note: update `backend/internal/server/middleware/api_key_auth.go` and the OAuth scope middleware to use one shared OAuth resource error writer, so `invalid_token` and `insufficient_scope` have consistent status, JSON body, `Content-Type: application/json`, and `WWW-Authenticate` headers.

External error codes must use RFC vocabulary only. Granular Sakrylle reasons belong in `error_description`, `error_uri`, logs, and metrics labels, not in the top-level `error` field.

Allowed top-level error codes:

- OAuth endpoints: `invalid_request`, `invalid_client`, `invalid_grant`, `unauthorized_client`, `unsupported_grant_type`, `unsupported_response_type`, `invalid_scope`, `access_denied`, `server_error`, `temporarily_unavailable`.
- Device polling endpoint: the RFC 8628 additions `authorization_pending`, `slow_down`, and `expired_token`.
- Resource endpoints: RFC 6750 `invalid_token` and `insufficient_scope`.

Mapping for common Sakrylle conditions:

| Internal condition | Top-level error | `error_description` example |
|---|---|---|
| Provider disabled | `temporarily_unavailable` | `oauth provider disabled` |
| Client disabled | `unauthorized_client` | `client disabled` |
| Missing PKCE | `invalid_request` | `pkce S256 required` |
| `code_challenge_method=plain` | `invalid_request` | `pkce plain is not supported` |
| PKCE verification failed | `invalid_grant` | `pkce verification failed` |
| Redirect URI mismatch after valid client lookup | `invalid_request` | `redirect_uri mismatch` |
| Requested scope not allowed for client | `invalid_scope` | `scope not allowed: <scope>` |
| Requested/selected group not allowed | `invalid_grant` | `group not allowed for this grant` |
| Refresh token reuse detected | `invalid_grant` | `refresh token reuse detected` |
| Revoked refresh token without reuse | `invalid_grant` | `refresh token revoked` |
| Revoked/expired access token at resource | `invalid_token` | `token revoked` or `token expired` |

For `insufficient_scope`, do not include a top-level JSON `scope` field. The canonical required-scope signal is the `scope="..."` parameter on `WWW-Authenticate`.

## 13. Middleware and Route Design

### 13.1 New Middleware

Add middleware in `backend/internal/server/middleware/oauth_scope.go`.

Suggested shape:

```go
func RequireOAuthScope(oauthSvc *service.OAuthProviderService, required ...string) gin.HandlerFunc
func LoadOAuthMetadata(oauthSvc *service.OAuthProviderService) gin.HandlerFunc
func RejectOAuthTokensForUnlistedResource(oauthSvc *service.OAuthProviderService) gin.HandlerFunc
```

Behavior:

1. Get `apiKey` from context.
2. If no API key context, return `401 invalid_token`.
3. If key is not `sk_oauth_`, call `Next`.
4. If `oauth_scope_enforcement_enabled=false`, call `Next`.
5. Load active OAuth access metadata by `apiKey.ID`.
6. If missing/revoked/expired, return `401 invalid_token`.
7. Store OAuth access metadata in Gin context.
8. `RequireOAuthScope` must panic or fail router setup when `required` is empty. Empty required-scope lists are a bug.
9. For `RequireOAuthScope` on a listed route, if granted scopes include any required scope or alias, call `Next`.
10. For `RejectOAuthTokensForUnlistedResource`, reject `sk_oauth_` with `403 insufficient_scope`; manual API keys still call `Next`.
11. Else return `403 insufficient_scope` with `WWW-Authenticate`.

Use `LoadOAuthMetadata` only for routes such as `/v1/me` where the handler deliberately performs field-level scope cropping after metadata validation. This split makes accidental empty-scope route wiring impossible.

The implementation may either attach `RejectOAuthTokensForUnlistedResource` to route groups that are intentionally out of scope, or use `OAuthScopePolicyForRequest` in a single resource gate after API-key auth. The acceptance requirement is the same: adding a new API-key-authenticated resource route must not accidentally make it callable with OAuth tokens until the route is added to section 7.3 and tested.

Add helper:

```go
func GetOAuthAccessTokenFromContext(c *gin.Context) (*service.OAuthAccessToken, bool)
```

### 13.2 Cache Strategy

The scope middleware must use a small cache inside `OAuthProviderService`.

Cache key:

```text
oauth:access:<api_key_id>
```

TTL:

- Min of 60 seconds and token expiry.
- No caching if token expires within 5 seconds.

Invalidation:

- Whenever an OAuth access token is revoked or its backing API key is disabled, invalidate:
  - existing `apikey:auth:<sha>` cache via `InvalidateAuthCacheByKey`
  - `oauth:access:<api_key_id>` metadata cache
- OAuth metadata cache invalidation must work across app instances. Publish JSON on Redis channel `oauth:cache:invalidate` with envelope `{"type":"oauth_access|oauth_grant|oauth_family","key":"<api_key_id|grant_id|token_family_id>"}`.
- Existing API-key auth cache invalidation keeps using `auth:cache:invalidate` with the full plaintext API key string payload because that is the current production contract. OAuth revoke paths must publish both channels when a backing `api_keys` row is disabled.
- Every refresh rotation, `/oauth/revoke`, authorized-app revoke, client revoke, and refresh-reuse family revoke path must delete local in-process metadata cache and publish distributed invalidation for all affected access rows.
- Do not rely on the 60-second metadata TTL for revocation correctness.

Production implementation must add this metadata cache before enabling OAuth v2 for high-QPS first-party clients. Unit tests may use a direct repository lookup stub.

### 13.3 Route Wiring

Dependency injection:

- `SetupRouter`, `registerRoutes`, and `RegisterGatewayRoutes` must receive or be able to resolve `*service.OAuthProviderService`.
- Do not create a second OAuth service instance inside route registration; route handlers, token endpoints, scope middleware, and cache invalidation must share the same service dependencies.
- Tests that construct routers must pass an explicit OAuth service stub when routes or middleware need it.

OAuth endpoint middleware order:

| Endpoint family | Middleware order |
|---|---|
| `/.well-known/*` | SecurityHeaders -> CORS -> Handler |
| `/oauth/authorize`, `/oauth/device` HTML | SecurityHeaders including `frame-ancestors 'none'` -> RateLimit -> Handler |
| `/oauth/token`, `/oauth/revoke`, `/oauth/device/code` | SecurityHeaders -> CORS -> RequestBodyLimit 32KB -> RateLimit -> Handler |
| `/api/v1/oauth/authorize/approve`, `/api/v1/oauth/device/approve` | SecurityHeaders -> RequestBodyLimit 32KB -> JWTAuth -> CSRF/Origin check -> Handler |
| `/api/v1/oauth/authorized-apps*` | SecurityHeaders -> RequestBodyLimit 32KB -> JWTAuth -> StepUpAuth for DELETE -> Handler |

CORS is allowed for `/oauth/token`, `/oauth/revoke`, `/oauth/device/code`, and `/.well-known/*` only from configured OAuth client origins or public discovery-safe origins. Consent/device HTML approval posts are same-origin user-account flows.

Update `backend/internal/server/routes/gateway.go`:

- Insert `RequireOAuthScope` after API key auth and group assignment.
- Scope middleware must run before handlers and before costly upstream calls.
- Manual API keys must pass through unchanged.
- API-key-authenticated route groups that are out of OAuth v2 scope, including `/v1beta/*` and `/antigravity/v1beta/*`, must explicitly reject `sk_oauth_` tokens rather than merely omitting `RequireOAuthScope`.

Examples:

```go
gateway.GET("/models", requireScope(ScopeModelsRead), h.Gateway.Models)
gateway.GET("/usage", requireScope(ScopeUsageRead), h.Gateway.Usage)
gateway.GET("/account/balance", requireScopeEither(ScopeAccountBalanceRead, ScopeAccountRead), h.Account.Balance)
gateway.GET("/me", LoadOAuthMetadata(oauthSvc), h.Account.Me)
gateway.POST("/chat/completions", requireScope(ScopeChatCompletionsCreate), ...)
gateway.POST("/responses", requireScope(ScopeResponsesCreate), ...)
gateway.POST("/messages", requireScope(ScopeMessagesCreate), ...)
gateway.POST("/images/generations", requireScope(ScopeImagesCreate), ...)
```

For `/v1/me`, implement either:

```go
RequireOAuthAnyScope(oauthSvc, ScopeProfileRead, ScopeAccountRead, ScopeAccountBalanceRead)
```

or let the handler do field-level scope decisions after loading metadata. If handler handles it, route still needs a metadata-loading middleware.

### 13.4 Root Route and Embedded SPA Bypass

Update `backend/internal/server/routes/oauth.go`:

```text
GET  /.well-known/oauth-authorization-server        -> OAuthProviderHandler.Metadata
GET  /oauth/authorize                               -> OAuthProviderHandler.Authorize
POST /oauth/token                                   -> OAuthProviderHandler.Token
POST /oauth/device/code                             -> OAuthProviderHandler.DeviceCode
GET  /oauth/device                                  -> OAuthProviderHandler.DeviceApprovePage
POST /oauth/revoke                                  -> OAuthProviderHandler.Revoke
POST /api/v1/oauth/authorize/approve                -> OAuthProviderHandler.ApproveAuthorization
POST /api/v1/oauth/device/approve                   -> OAuthProviderHandler.ApproveDevice
GET  /api/v1/oauth/authorized-apps                  -> OAuthProviderHandler.ListAuthorizedApps
DELETE /api/v1/oauth/authorized-apps/:grant_id      -> OAuthProviderHandler.RevokeAuthorizedApp
DELETE /api/v1/oauth/authorized-apps/client/:client_id -> OAuthProviderHandler.RevokeAuthorizedClient
```

Update embedded frontend fallback in `backend/internal/web/embed_on.go`:

- `/oauth/` is already bypassed and must stay bypassed.
- `/.well-known/` must be bypassed so discovery returns JSON instead of SPA HTML. This is a hard acceptance check for OAuth v2; a discovery request returning the embedded SPA HTML is a release blocker.

Update CORS origin calculation:

- Prefer `oauth_clients.allowed_origins` when non-empty.
- Otherwise derive origins from HTTP(S) `redirect_uris` as v1 currently does.
- Native custom schemes and loopback redirect URIs must not become CORS origins.

## 14. Frontend Design

### 14.1 Authorized Apps Page

Upgrade `frontend/src/views/user/AuthorizedAppsView.vue` from client aggregate to device rows.

Display columns:

- App
- Device
- Group
- Scopes
- First authorized
- Last used
- Sessions/tokens
- Actions

Actions:

- Revoke this device: `DELETE /api/v1/oauth/authorized-apps/:grant_id`
- Revoke all devices for this app: `DELETE /api/v1/oauth/authorized-apps/client/:client_id`
- Rename device may be added later; not required for v2 phase 1.

UX:

- App icon if `icon_url` exists.
- Disabled client badge.
- Scope labels should be human-readable.
- Raw scope should remain visible in a tooltip or compact secondary text.
- Empty state should mention that OAuth-connected Sakrylle apps will appear here after login.
- Required states: loading, empty, API error, partial data with stale/disabled client, revoke-in-progress, and revoke failure.
- Revoke actions must show a confirmation dialog that distinguishes "this device" from "all devices for this app".
- Match the existing Monet purple theme, dark mode, zh/en i18n, and WCAG 2.1 AA contrast/focus requirements.

### 14.2 Consent and Device Pages

Consent page:

- Render app name and icon.
- Render device name if provided.
- Render group selector when multiple groups are available.
- Render scope descriptions.
- Store authorize request details only in the server-side transaction. The browser form sends `transaction_id`, `decision`, selected group, and CSRF token.
- Avoid raw `@` in vue-i18n locale strings unless escaped because vue-i18n treats `@` as linked-message syntax.

Device page:

- Allow entering code.
- If `verification_uri_complete` includes code, prefill.
- Client instructions and CLI docs must still show `verification_uri` plus `user_code`; do not rely only on the complete URI.
- Show app name, device name, scopes, group selector, approve/deny buttons.
- Handle expired/invalid code clearly.

### 14.3 First-Party Token Storage Guidance

Web:

- Preferred phase 1 Sakrylle Web uses a BFF/confidential client. Refresh tokens live only in the server-side token store; browser state is an HTTP-only, Secure, SameSite session cookie.
- A pure SPA client must not get `offline_access` by default and must not store refresh tokens in `localStorage`, `sessionStorage`, IndexedDB, JS-readable cookies, or other browser-JS-readable persistence.
- SPA access tokens, if any, should be memory-only and short-lived.

Native desktop / Chat / Desktop wrapper:

- Store refresh tokens in OS secret storage: macOS Keychain, Windows Credential Manager, or Linux Secret Service/libsecret.
- Electron renderers must never hold refresh tokens. Keep refresh/token exchange in the main process or a local broker and send only short-lived access-token results or proxied API calls to renderer code.
- Loopback PKCE is preferred; reverse-domain custom scheme is fallback.

CLI:

- Prefer OS secret storage when available.
- Fallback file storage must use a private config directory (`0700`) and token file (`0600`), refuse world/group-readable files, and avoid printing token values in logs.

Mobile:

- Store refresh tokens in platform keystore/keychain APIs, for example iOS Keychain or Android Keystore-backed encrypted storage.
- Prefer Universal Links / App Links. Reverse-domain custom scheme is fallback only.

## 15. Migration and Backward Compatibility

### 15.1 Compatibility Guarantees

Must remain true after deployment:

- Existing manual API keys keep working.
- Existing `sk_oauth_` Image Playground access tokens keep working until normal expiry.
- Existing Image Playground refresh tokens keep rotating.
- Existing `image_generation` scope grants can call `/v1/images/*`.
- Existing `balance:read` scope grants can call `/v1/account/balance`.
- Existing `models:read` scope grants can call `/v1/models`.
- `/api/v1/oauth/grants` continues to work until frontend migration is complete.
- Existing user API key list continues to hide `sk_oauth_`.

### 15.2 Migration Sequence

Production-safe sequence:

1. Deploy DB migration adding nullable/defaulted columns and new tables.
2. Backfill `oauth_refresh_tokens` v2 metadata.
3. Run `backend/cmd/oauth-reconcile --report-only`; do not continue if it reports active rows that cannot be safely reconciled.
4. Run `backend/cmd/oauth-reconcile --apply` to perform the idempotent `api_keys`-based OAuth reconciliation/backfill for `oauth_access_tokens`.
4. Run Sakrylle-specific first-party client seed or operator SQL after verifying required group-name resolution/overrides.
5. Deploy backend service code with scope aliases and access metadata reads.
6. Keep `oauth_scope_enforcement_enabled=false` for smoke test if desired.
7. Run `backend/cmd/oauth-reconcile --report-only` again and verify zero active `sk_oauth_` rows lack access metadata.
8. Run production smoke tests for legacy Image Playground.
9. Enable `oauth_scope_enforcement_enabled=true` by explicit operator action.
10. Deploy frontend authorized-apps page with `oauth_v2_ui_enabled=false`, smoke it, then set `oauth_v2_ui_enabled=true`.
11. Update external docs and Image Playground client to canonical scopes.
12. Later, turn `allow_refresh_without_offline_access=false` on legacy client only after waiting `refresh_token_ttl_seconds` past the step-11 deployment timestamp, or after forced re-auth/revocation of all legacy refresh-token families.

If backend and DB migrations ship in one image, the code must tolerate missing access metadata until migration completes only during startup. After startup migrations complete, missing access metadata for `sk_oauth_` is `invalid_token`.

The reconciliation job must remain available after rollout. If the system is rolled back to a pre-v2 image, that image can continue creating old-shape OAuth rows. Before redeploying v2 or re-enabling enforcement after such a rollback window, operators must rerun reconciliation and the missing-metadata report.

### 15.3 Rollback Strategy

Forward-only DB migration means rollback is operational:

- If v2 code fails before enforcement is enabled, redeploy previous image. New tables/columns are inert.
- If enforcement causes breakage, set `oauth_scope_enforcement_enabled=false` directly in `settings`.
- If Device Flow fails, set `oauth_device_flow_enabled=false`.
- If the frontend Authorized Apps or consent/device UI fails after Phase 5, set `oauth_v2_ui_enabled=false` or redeploy the previous frontend image. Backend token endpoints remain controlled by the backend kill-switches above.
- If any rollback runs old code that can mint OAuth tokens, rerun v2 reconciliation before enabling enforcement again.
- Do not delete v2 tables during rollback.
- Do not modify migration checksums.

### 15.4 Image Playground Migration

Legacy client:

```text
client_id=sakrylle-image-playground
legacy scopes=image_generation balance:read models:read
default_group_id=5
```

Sakrylle Image Playground seed/migration must:

- Preserve the row.
- Add canonical allowed scopes:
  - `account:balance:read`
  - `models:read`
  - `images:create`
  - `responses:create`
  - `offline_access`
- Set `app_type='image'`.
- Set `allowed_group_ids` to the resolved GPT-Image group plus configured response-capable groups such as GPT-Pro/GPT-Plus. Resolve by group name or operator override; do not hard-code production numeric IDs in the generic migration.
- Set `allow_refresh_without_offline_access=true`.

Image Playground frontend should later request:

```text
account:balance:read models:read images:create responses:create offline_access
```

The frontend must choose or refresh into a group that supports the selected API mode: Images API calls require an image-capable group, while Agent conversations over Responses API require a response-capable group. A single OAuth grant may cover both, but each access token remains bound to one current group at a time.

When that deployment is verified:

- Set `allow_refresh_without_offline_access=false`.
- Optionally stop documenting `image_generation`, but keep alias enforcement until old refresh tokens expire or are revoked.

## 16. Implementation Plan and Acceptance Checklist

This section is written as the implementation/testing SubAgent contract. Every phase has explicit acceptance criteria.

### Phase 0: Preflight and Ownership

Tasks:

- Confirm branch is `theme/monet-purple`.
- Confirm no applied migration file is modified.
- Re-check the migration directory immediately before implementation; if `145` is already taken, bump this document and the PR to the next unused number before coding.
- Take a production DB backup before rollout, for example `/opt/stack/backups/sub2api-db-<date>.sql`, and verify it is non-empty before applying migrations.
- Freeze this document as the contract for parallel agents.
- Create tracking issue or checklist with the workstreams in section 17.

Acceptance:

- `git status --short` shows only intentional design/implementation files.
- No changes to migrations `001` through `144`.
- Production `pg_dump` backup exists and is non-zero before any production migration.
- All agents agree on DTO names, table names, and route paths from this document before coding.

### Phase 1: Schema, Ent, and Repository

Tasks:

- Add migration `backend/migrations/145_oauth_v2.sql`.
- Add Ent schemas:
  - `backend/ent/schema/oauth_authorize_transaction.go`
  - `backend/ent/schema/oauth_access_token.go`
  - `backend/ent/schema/oauth_device_code.go`
- Extend Ent schemas:
  - `oauth_client.go`
  - `oauth_code.go`
  - `oauth_refresh_token.go`
- Run Ent code generation using the repo's established generator.
- Extend repository implementation.
- Add `backend/cmd/oauth-reconcile/main.go` with `--report-only` and `--apply` modes from section 10.9.
- Add migration regression tests.

Acceptance:

- Fresh database applies migrations 1 through 145.
- Existing database with v1 OAuth rows migrates successfully.
- Active legacy `sk_oauth_` rows get `oauth_access_tokens` backfill.
- Active `sk_oauth_` rows without refresh rows are reconciled from `api_keys` or reported and blocked before enforcement.
- Legacy Image Playground rows with empty stored scopes receive the legacy scope bundle during reconciliation or pass equivalent compatibility enforcement.
- Existing `sakrylle-image-playground` row remains present.
- `oauth_scope_enforcement_enabled` is seeded `false` by migration and flipped only after smoke tests.
- Generic schema migration does not require Sakrylle production groups to exist.
- Sakrylle-specific first-party seed step is idempotent and either resolves required groups by name/operator override or leaves affected clients disabled without binding an unsafe numeric fallback.
- Ent generated code compiles.
- `backend/cmd/oauth-reconcile --report-only` reports unsafe rows before enforcement; `--apply` is idempotent.
- `cd backend && go test ./migrations ./internal/repository` passes.

### Phase 2: Scope Registry and OAuth Core Service

Tasks:

- Add canonical scope constants and alias normalization.
- Extend `OAuthClient`, `OAuthCode`, `OAuthRefreshToken` service types.
- Add `OAuthAccessToken` and `OAuthDeviceCode` service types.
- Implement `ResolveOAuthGroup`.
- Update authorize validation to use default scopes when request scope is empty.
- Add authorize transaction/CSRF storage with short TTL using `oauth_authorize_transactions`.
- Update approval to load the transaction, verify CSRF, and persist group/device/grant metadata.
- Update token minting to write `api_keys`, `oauth_access_tokens`, and `oauth_refresh_tokens`.
- Update refresh to preserve grant/family and support optional group switching.
- Implement refresh reuse detection and family revocation.

Acceptance:

- Unit tests prove `image_generation -> images:create` and `balance:read -> account:balance:read`.
- Empty scope request uses `oauth_clients.default_scopes`.
- Unknown scope is rejected.
- PKCE verifier length, charset, and S256 no-padding challenge validation are covered by unit tests; entropy remains client-generation guidance, not server-side measurement.
- Authorization approval stores group/device/grant.
- Authorization approval cannot alter stored `client_id`, redirect URI, scopes, state, or PKCE challenge by resubmitting body fields.
- Authorization-code token exchange mints access metadata.
- Refresh token rotation disables old access token, revokes old access metadata, and mints new metadata.
- Refresh token exchange rejects wrong token/client binding while treating public `client_id` as identification, not authentication.
- Token responses include `refresh_token_expires_in` when the server returns a refresh token and can compute the expiry.
- Refresh with valid `group_id` mints new access token bound to that group.
- Refresh with disallowed group returns RFC `invalid_grant` with a group-specific `error_description`.
- Refresh token reuse revokes the whole token family.
- `cd backend && go test ./internal/service` passes.

### Phase 3: Scope Enforcement, `/v1/me`, and Revocation

Tasks:

- Add OAuth scope middleware.
- Wire scope checks into gateway routes.
- Implement `GET /v1/me`.
- Implement `POST /oauth/revoke`.
- Implement metadata cache with clear invalidation hooks.
- Extend auth error responses with the shared OAuth resource error writer, including `WWW-Authenticate` for missing, invalid, expired, revoked, and insufficient-scope tokens.

Acceptance:

- Manual API key can still call endpoints without OAuth metadata.
- OAuth token with `models:read` can call `/v1/models`.
- OAuth token without `models:read` gets `403 insufficient_scope` and correct `WWW-Authenticate`.
- OAuth missing/expired/revoked token cases get `401 invalid_token` and correct `WWW-Authenticate`.
- Legacy Image Playground token with `image_generation` can call `/v1/images/generations`.
- Legacy Image Playground token cannot call `/v1/chat/completions`.
- `/v1/me` crops fields based on scopes.
- `/oauth/revoke` revokes refresh grant and invalidates old access token immediately.
- `/oauth/revoke` is idempotent for unknown, already revoked, and wrong-client tokens and does not disclose cross-client token existence.
- `/oauth/token`, `/oauth/revoke`, and `/oauth/device/code` reject JSON and unsupported content types.
- Auth cache invalidation spy observes invalidation on refresh, revoke, grant revoke, and reuse.
- OAuth metadata-cache invalidation spy observes local and distributed invalidation on refresh, revoke, grant revoke, client revoke, and reuse.
- Batch API-key disable helper returns affected plaintext keys for immediate `apikey:auth:<sha>` invalidation.
- `cd backend && go test ./internal/server/middleware ./internal/server/routes ./internal/handler ./internal/service` passes.

### Phase 4: Device Authorization Flow

Tasks:

- Implement `POST /oauth/device/code`.
- Implement `GET /oauth/device`.
- Implement `POST /api/v1/oauth/device/approve`.
- Extend `/oauth/token` for device-code grant.
- Add rate limits and polling interval enforcement.
- Validate device-code creation scopes and client-level group permissions before issuing a code.
- Add device-code cleanup for expired rows if the repo has scheduled cleanup patterns.

Acceptance:

- CLI happy path:
  - device code created
  - pending poll returns `authorization_pending`
  - user approves
  - poll returns access/refresh token
  - second poll returns `invalid_grant` or `expired_token` after consumption, per test contract
- Fast polling returns `slow_down` and increases interval.
- Expired device code returns `expired_token`.
- Denied device code returns `access_denied`.
- Empty device-code scope uses client defaults; disallowed scopes and disallowed client-level groups are rejected at create time.
- Approval re-resolves group access for the authenticated user.
- Device-code polling rejects wrong token/client binding.
- Device-code response and CLI docs show `verification_uri` plus `user_code` even when `verification_uri_complete` exists.
- Invalid user code cannot reveal whether similar codes exist.
- Device code and user code plaintext never appear in logs.
- `cd backend && go test ./internal/handler ./internal/service ./internal/repository` passes.

### Phase 5: Frontend Authorized Apps and Consent UX

Tasks:

- Add/extend API client for authorized apps.
- Update `frontend/src/types/index.ts` authorized app DTOs.
- Update `frontend/src/views/user/AuthorizedAppsView.vue`.
- Add device-level revoke and app-level revoke actions.
- Add i18n strings for scopes and device/app labels.
- Update consent/device pages if they are frontend-owned; if consent remains backend HTML, update backend templates.

Acceptance:

- Authorized apps page shows one row per grant/device.
- Loading, empty, error, partial/stale-client, revoke confirmation, and revoke-in-progress states render correctly.
- Revoke one device removes only that row.
- Revoke all devices for app removes all rows with the client.
- Disabled clients render clearly.
- Scope labels are readable in zh/en.
- No vue-i18n linked-message parse errors from `@`.
- Dark mode follows the existing theme and meets WCAG 2.1 AA contrast/focus expectations.
- `cd frontend && npm run typecheck` passes.
- `cd frontend && npm run test:run` passes for updated tests.

### Phase 6: Documentation and Rollout

Tasks:

- Update `docs/OAUTH_CLIENT_INTEGRATION.md` or add a v2 integration guide.
- Update external Sakrylle docs in `sakrylle-docs`.
- Document migration from legacy scopes to canonical scopes.
- Document Device Flow for CLI.
- Document `/v1/me`.
- Document error codes.
- Document group switching via refresh token.
- Document security checklist for first-party app implementers.

Acceptance:

- Docs show canonical scopes.
- Docs mention legacy `image_generation` only under migration.
- CLI Device Flow docs include `authorization_pending`, `slow_down`, `access_denied`, `expired_token`.
- Image Playground docs use `account:balance:read models:read images:create responses:create offline_access`.
- Sakrylle Web docs describe BFF/confidential storage for `offline_access`, or SPA without refresh tokens.
- Sakrylle Web/Chat/Desktop/Mobile docs do not suggest storing refresh tokens in localStorage or renderer-accessible storage.
- Native/CLI docs mention Keychain, Credential Manager, Secret Service, mobile keystore, and `0600`/`0700` file fallback rules.
- Docs include no stale `balance:read` as a recommended v2 scope.

### Phase 7: Production Smoke Test

Tasks:

- Deploy migration and backend to production with `oauth_scope_enforcement_enabled=false`, run smoke tests, then flip enforcement by explicit operator action.
- Verify legacy Image Playground.
- Verify one new first-party client flow.
- Enable enforcement.
- Run endpoint smoke tests.

Acceptance:

- `GET /.well-known/oauth-authorization-server` returns correct issuer and endpoints.
- Legacy Image Playground can refresh and generate an image.
- Token with image scopes cannot call chat endpoints.
- Token with chat scopes cannot call image endpoints.
- CLI device flow can mint a token.
- Revoked token fails immediately, not after Redis TTL.
- `/v1/models` still returns only group-configured models.
- `/v1/models` never falls back to default model constants.

## 17. Parallel Agent Workstreams

### Workstream A: Schema and Repository Agent

Owns:

- `backend/migrations/145_oauth_v2.sql`
- Ent schemas for OAuth tables.
- Repository methods for clients, codes, refresh tokens, access metadata, device codes.
- Migration tests.

Must not own:

- Route wiring.
- Frontend views.
- Scope policy decisions beyond constants imported from service.

Outputs:

- Migration applies and backfills.
- Ent code generated.
- Repository tests pass.

Dependency:

- None, but must use table/field names from this document.
- Workstream A merges first and is the only workstream that commits `backend/ent/*` generated files. Other workstreams may compile against stubs or local generated output while developing, but must drop those local generated-file changes before merge.

### Workstream B: OAuth Core Service Agent

Owns:

- `backend/internal/service/oauth_provider_service.go`
- `backend/internal/service/oauth_provider_types.go`
- new `oauth_scopes.go`
- group resolution
- mint/refresh/reuse/revoke service logic

Must not own:

- Frontend.
- Device page UI.
- Route-specific middleware wiring except service helpers.

Outputs:

- Unit-tested service logic for scopes, group switching, token family, and metadata creation.

Dependency:

- Workstream A repository interfaces may be stubbed until merged.
- After Workstream A merges, Workstream B rebases and uses A's committed Ent output. Workstream B must not regenerate and commit `backend/ent/*` unless the schema contract is deliberately changed through Workstream A.

### Workstream C: Middleware and Gateway Agent

Owns:

- `backend/internal/server/middleware/oauth_scope.go`
- `backend/internal/server/routes/gateway.go`
- OAuth resource error responses
- `/v1/me` route wiring

Outputs:

- Scope enforcement matrix from section 7.3.
- Tests proving manual API keys are unaffected.

Dependency:

- Workstream B scope constants and metadata lookup interface.

### Workstream D: Device Flow Agent

Owns:

- `POST /oauth/device/code`
- `GET /oauth/device`
- `POST /api/v1/oauth/device/approve`
- `/oauth/token` device-code grant branch
- Device flow tests

Outputs:

- RFC 8628-compatible polling behavior.
- CLI-ready response contract.

Dependency:

- Workstream A device code table.
- Workstream B token minting.

### Workstream E: Frontend Agent

Owns:

- `frontend/src/views/user/AuthorizedAppsView.vue`
- authorized apps API module
- frontend types
- i18n labels
- frontend tests

Outputs:

- Device-level authorized-apps page.
- Revoke device/app flows.

Dependency:

- Backend DTO from section 12.10.

### Workstream F: Test and Acceptance Agent

Owns:

- Cross-workstream test plan.
- Regression tests for legacy Image Playground.
- Negative security tests.
- Production smoke checklist.

Outputs:

- Tests in backend and frontend.
- Manual smoke script or documented curl sequence.

Dependency:

- All implementation workstreams.

### Workstream G: Docs Agent

Owns:

- Internal docs in this repo.
- External docs update plan for `sakrylle-docs`.
- Migration notes for Image Playground and first-party app implementers.

Outputs:

- OAuth v2 integration guide.
- Scope reference.
- Device Flow guide.
- Error reference.

Dependency:

- Stable route and DTO contracts from this document.

## 18. Test Matrix

### 18.1 Migration and Backward Compatibility

Tests:

- Fresh DB applies all migrations.
- DB with existing `oauth_clients`, `oauth_refresh_tokens`, and `api_keys` migrates.
- Existing `sakrylle-image-playground` row remains.
- Existing refresh rows gain `grant_id`, `token_family_id`, `group_id`, `device_name`.
- Existing active `sk_oauth_` API key gains `oauth_access_tokens` metadata.
- Existing disabled `sk_oauth_` API key backfills metadata with `revoked_at=NOW()` so future audit queries can explain why it is unusable.
- Migration is idempotent with `IF NOT EXISTS` and `ON CONFLICT`.

Acceptance:

- No migration checksum changes to old files.
- Backfilled legacy token can pass scope enforcement for legacy scopes.

### 18.2 Legacy Scopes

Tests:

- `NormalizeScopes(["image_generation"]) == ["images:create"]`.
- `NormalizeScopes(["balance:read"]) == ["account:balance:read"]`.
- Enforcement accepts old stored `image_generation` for `images:create`.
- Enforcement accepts old stored `balance:read` for `account:balance:read` and `/v1/account/balance`.
- Consent page displays canonical meaning for legacy requests.
- Token response for v2 clients returns canonical scopes.

Acceptance:

- Image Playground v1 can continue using its old scope string during migration.

### 18.3 First-Party Client Seeds

Tests:

- Generic migration tests do not require Sakrylle production groups or first-party clients.
- Sakrylle seed tests run against fixtures or overrides that define GPT-Pro, Codex, and GPT-Image group targets.
- `sakrylle-web` exists with expected default scopes.
- `sakrylle-chat-desktop` exists with expected default scopes.
- `sakrylle-cli` exists with `device_flow_enabled=true`.
- `sakrylle-desktop` exists.
- `sakrylle-mobile` exists.
- Production first-party redirect URIs and origins contain only controlled HTTPS values, except native loopback/custom-scheme values for native clients.
- Localhost redirect URIs exist only on dev/test clients or fixtures, not production seed rows.
- Production group IDs are resolved by group name or operator override; tests must not assume every environment uses Sakrylle prod IDs.
- Missing production group resolution disables or fails only the Sakrylle seed step; it must not break the generic schema migration.
- `sakrylle-image-playground-v2` exists and is restricted to the configured GPT-Image group plus configured response-capable groups.
- Legacy `sakrylle-image-playground` has both legacy and canonical allowed scopes.

Acceptance:

- No public first-party client requires a client secret.
- Confidential Web/BFF client requires token endpoint authentication.
- Public clients require PKCE for authorization code flow.

### 18.4 Device Flow

Tests:

- Create device code with valid CLI client.
- Reject device code creation for disabled client.
- Reject device code creation for client with `device_flow_enabled=false`.
- Empty `scope` uses client default scopes.
- Reject unknown scope.
- Reject client-disallowed scope.
- Reject disallowed group.
- Create-time group validation is client-level; approval rechecks authenticated user's group access.
- Device-code create rejects JSON or unsupported content type.
- Pending poll returns `authorization_pending`.
- Fast repeated poll returns `slow_down`.
- Poll with wrong `client_id` returns `invalid_grant` without consuming or disclosing the code.
- Approved code returns token.
- Denied code returns `access_denied`.
- Expired code returns `expired_token`.
- Consumed code cannot mint twice.
- User-code brute force is rate-limited.

Acceptance:

- CLI can implement login using only documented responses.
- CLI displays both `verification_uri` and `user_code`, with `verification_uri_complete` as optional convenience.

### 18.5 Scope Enforcement

Tests:

- Manual API key bypasses OAuth scope middleware.
- OAuth token missing metadata returns `401 invalid_token`.
- OAuth token with revoked metadata returns `401 invalid_token`.
- OAuth token with expired metadata returns `401 invalid_token`.
- OAuth metadata whose user/group/expiry does not match the authenticated `api_keys` context returns `401 invalid_token`.
- OAuth token with `models:read` can call `/v1/models`.
- OAuth token without `models:read` cannot call `/v1/models`.
- OAuth token with `responses:create` can call every responses alias.
- OAuth token without `responses:create` cannot call responses aliases.
- OAuth token with `messages:create` can call `/v1/messages`.
- OAuth token with `chat.completions:create` can call chat completions.
- OAuth token with `images:create` can call images endpoints only if group also allows image generation.
- OAuth token with `images:create` but group `allow_image_generation=false` still fails at existing group image check.
- OAuth token for an unlisted resource route, including `/v1beta/*`, is rejected by default.
- A newly added API-key-authenticated resource route without an explicit matrix entry rejects `sk_oauth_` tokens by default while still allowing manual API keys.
- `/v1/me.allowed_groups` excludes image-only groups for chat clients and filters image clients by their granted operation scopes: Image Playground v2 may see image-capable groups for `images:create` and response-capable groups for `responses:create`, while admin-only groups remain hidden.
- Refresh group switching rejects a group that `/v1/me.allowed_groups` would not show for that client/scopes combination.
- `/v1/me` returns both `granted_scopes` and `effective_capabilities` and they differ when group capabilities narrow a granted scope.

Acceptance:

- Scope checks do not change group-level and billing-level safety checks.
- Route aliases added later require an explicit scope-matrix test before OAuth access is enabled.

### 18.6 Revoke and Cache Invalidation

Tests:

- Refresh rotation invalidates old access token cache.
- `/oauth/revoke` with refresh token invalidates current access token cache.
- User revoking a grant invalidates all access tokens for that grant.
- User revoking a client invalidates all access tokens for that client/user.
- Refresh reuse invalidates all access tokens for token family.
- Redis auth cache hit before revoke does not keep token alive after revoke.
- Missing Redis does not silently skip DB revocation.

Acceptance:

- A revoked OAuth access token fails immediately in the next request.

### 18.7 Security Negative Tests

Tests:

- Invalid `redirect_uri` renders inline error and never redirects.
- `redirect_uri` prefix attack is rejected.
- Dynamic loopback port allowed only for registered loopback host/path.
- `code_challenge_method=plain` rejected.
- Missing `state` rejected.
- Missing PKCE for public client rejected.
- Invalid PKCE verifier length or charset rejected.
- S256 challenge with padding or wrong SHA-256 base64url value rejected.
- Authorization code replay revokes prior grant.
- Authorize approval transaction cannot be approved twice, cannot be used after expiry, stores only a CSRF hash, and consumes on both approve and deny.
- Approval POST cannot tamper with client, redirect URI, scopes, state, or PKCE challenge from the authorize transaction.
- Refresh token replay revokes token family.
- Refresh rotation preserves the original family absolute expiry and does not extend the session with `now + refresh_token_ttl_seconds`.
- Refresh token for another client is rejected without revealing cross-client ownership.
- Revocation of another client's token returns idempotent success and performs no revoke.
- Token/revoke/device endpoints reject JSON content type and set `Pragma: no-cache` on sensitive responses.
- Public client with `client_secret` ignored or rejected consistently; it must not elevate trust.
- Confidential client without secret rejected.
- Device Flow user code timing does not reveal near misses.
- API key in query parameter remains rejected.
- Logs do not contain plaintext tokens, codes, verifiers, or secrets.

Acceptance:

- Negative tests exercise both handler-level and service-level paths.

### 18.8 Frontend Authorized Apps

Tests:

- Renders empty state.
- Renders multiple devices for same client.
- Renders disabled client badge.
- Renders group and device columns.
- Revoke one grant calls `DELETE /authorized-apps/:grant_id`.
- Revoke all app devices calls `DELETE /authorized-apps/client/:client_id`.
- Handles API error with toast.
- Scope labels render in zh/en.

Acceptance:

- User can revoke one CLI device without revoking Mobile/Web.

### 18.9 Documentation Rollout

Tests:

- Internal docs mention canonical scopes.
- External docs mention canonical scopes.
- Device Flow docs include all polling errors.
- Image Playground docs are migrated.
- Security checklist forbids localStorage refresh tokens for web and renderer-readable refresh tokens for Electron.
- Web docs explain BFF/confidential `offline_access`; SPA docs omit default `offline_access`.
- Native/CLI docs include Keychain/Credential Manager/Secret Service/mobile keystore and `0600`/`0700` fallback guidance.
- Docs state production seeds use controlled HTTPS only and localhost belongs to dev-only clients.
- Security checklist forbids embedded WebView login.

Acceptance:

- A new first-party app implementer can complete OAuth integration from docs alone.

## 19. Security Checklist

Implementation must satisfy:

- `S256` PKCE only.
- PKCE verifier is 43-128 unreserved characters; clients must generate at least 256 bits of entropy; challenge is SHA-256 base64url without padding.
- State required.
- Exact redirect matching.
- Authorization approval uses a server-side transaction or signed nonce plus CSRF; approval never trusts resubmitted authorize parameters.
- Loopback exception is host/path exact and port-only dynamic.
- Refresh rotation.
- Refresh token absolute expiry and token-family/session lifetime are enforced; idle expiry, if added, is additive.
- Refresh reuse detection.
- Token family revocation.
- Token/client binding checked for authorization-code exchange, refresh, device-code polling, and revocation.
- Device-level revocation.
- Authorized Apps endpoints require JWT auth and current-user ownership filters.
- CSRF protection on consent approval and device approval.
- `Cache-Control: no-store` and `Pragma: no-cache` on token, revoke, device, and error responses with sensitive context.
- Token, revoke, and device endpoints require form content type and reject JSON.
- No plaintext token/code/secret logging.
- No browser-JS-readable persistent refresh token for Web.
- No refresh token in Electron renderer process storage.
- Native refresh tokens stored in OS/mobile secure storage; CLI file fallback is private dir `0700` and token file `0600`.
- Web `offline_access` requires BFF/confidential storage, otherwise the SPA client omits refresh tokens by default.
- Production first-party web/image redirect origins are controlled HTTPS only; localhost is dev-only.
- `WWW-Authenticate` on OAuth resource failures: missing, invalid, expired, revoked, and insufficient-scope tokens.
- `api_key_auth.go` and OAuth scope middleware share the same OAuth resource error writer.
- OAuth access token expiry enforced by both `api_keys.expires_at` and `oauth_access_tokens.expires_at`.
- User disabled/inactive still blocks all tokens through existing API key auth.
- Group disabled/inactive still blocks all tokens.
- Billing and balance checks stay unchanged.
- User API key list hides `sk_oauth_`.

## 20. Operational Notes

Production settings:

These are the desired final production values after migration, reconciliation, and smoke testing. The migration default for `oauth_scope_enforcement_enabled` is `false`.

```text
oauth_provider_enabled=true
oauth_issuer=https://sub.sakrylle.com
oauth_scope_enforcement_enabled=true
oauth_device_flow_enabled=true
```

Cache and configuration runbook:

- After updating API key user/group bindings or revoking tokens, publish Redis invalidation using existing auth cache invalidation path. Do not rely on 60-second TTL expiry.
- Existing auth cache invalidation channel: `auth:cache:invalidate`, payload is the full plaintext API key string. This matches the current production contract used by API-key auth.
- OAuth metadata invalidation channel: `oauth:cache:invalidate`, payload is JSON `{"type":"oauth_access|oauth_grant|oauth_family","key":"<api_key_id|grant_id|token_family_id>"}`.
- Revoke smoke test must prove immediate failure: mint token -> call `/v1/models` 200 -> revoke -> call `/v1/models` again and get `401 invalid_token` within 1 second, not after Redis TTL.
- If a deployment adds or changes groups/channels/pricing rows that OAuth group resolution depends on, restart `sub2api` after the DB change so in-memory channel/pricing caches do not serve stale group/model data.
- For Anthropic-backed groups such as Deepseek, keep `groups.platform` and `channel_model_pricing.platform` aligned. `/v1/models` uses group platform during pricing lookup; a platform mismatch can make model lists empty even when calls appear to route.
- When changing an API key's `user_id`, `group_id`, or disabled state directly in DB, publish the existing `auth:cache:invalidate` payload for that full API key string. Disabled keys can otherwise work until auth-cache expiry.

Observability and audit:

- Metrics: `oauth_token_requests_total{grant_type,client_id,result}`, `oauth_refresh_reuse_detected_total{client_id}`, `oauth_device_code_requests_total{client_id,result}`, and p99 latency for `/oauth/token`.
- Page immediately if `oauth_refresh_reuse_detected_total > 0` over 5 minutes for first-party clients, because refresh reuse means either token theft or client storage/rotation bugs.
- Audit events: grant create, refresh rotation, revoke, family revoke, authorized-app revoke, device approve, and device deny. Each event records `user_id`, `client_id`, `grant_id`, `token_family_id` when applicable, request ID, and redacted IP/User-Agent context. Never write plaintext tokens, authorization codes, device codes, user codes, PKCE verifiers, or client secrets.

Deployment smoke examples:

```bash
curl -sS https://sub.sakrylle.com/.well-known/oauth-authorization-server
curl -sS https://sub.sakrylle.com/health
```

Manual DB edits should use direct SQL for OAuth settings if admin `PUT /api/v1/admin/settings` would clobber sibling keys.

## 21. Final Product Decisions for v2 Phase 1

These decisions remove ambiguity for implementation:

1. `sakrylle-web` phase 1 prefers BFF/confidential storage when it needs `offline_access`. If shipped as a pure SPA, use a separate public PKCE client and omit `offline_access` by default.
2. Production seed redirect URIs and origins are the controlled HTTPS values in section 8.3. Localhost belongs only to dev-only clients, disabled non-production seed rows, or tests.
3. Sakrylle Desktop uses its own `sakrylle-desktop` client ID. It must not share `sakrylle-cli`, so user-facing authorized-app rows remain clear.
4. `email:read` is not granted to any first-party client by default.
5. `/v1beta/*` Gemini-native endpoints are out of scope for v2 phase 1 and reject OAuth tokens until a `gemini:generate`-style scope is designed and tested.
6. Production group IDs are not product-contract constants. The generic schema migration does not depend on Sakrylle production groups; Sakrylle-specific seed/operator SQL resolves default/restricted groups by name or override and fails closed or disables affected clients when the target group is missing.
