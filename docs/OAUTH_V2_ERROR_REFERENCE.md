# Sakrylle OAuth 2.0 v2 — Error Reference

> Companion to [`OAUTH_V2_INTEGRATION.md`](./OAUTH_V2_INTEGRATION.md). Source
> of truth for error vocabulary is §12.12 of
> [`OAUTH_V2_DESIGN.md`](./OAUTH_V2_DESIGN.md).

All Sakrylle OAuth endpoints return errors with the RFC top-level vocabulary
only. Sakrylle-specific reasons live in `error_description`, `error_uri`,
and server-side logs — never in the top-level `error` field.

## Response shapes

### OAuth endpoint (token / authorize / device / revoke)

```json
{
  "error": "invalid_grant",
  "error_description": "authorization code expired",
  "error_uri": "https://doc.sakrylle.com/developers/oauth/errors#invalid_grant",
  "request_id": "optional-correlation-id"
}
```

`Cache-Control: no-store` and `Pragma: no-cache` are set on every error
response that may carry sensitive context.

### Resource endpoint (`/v1/*`)

Responses follow RFC 6750:

```http
HTTP/1.1 403 Forbidden
WWW-Authenticate: Bearer error="insufficient_scope", scope="images:create"
Content-Type: application/json

{
  "error": "insufficient_scope",
  "error_description": "required scope: images:create"
}
```

The `scope=...` parameter on `WWW-Authenticate` is the **canonical** signal
of the required scope. `insufficient_scope` does not include a top-level
JSON `scope` field.

---

## OAuth endpoint errors

### `invalid_request`

`HTTP 400`. The request is missing a required parameter, includes an
unsupported parameter or repetition, or is otherwise malformed.

| Trigger | `error_description` example |
|---|---|
| Missing `grant_type` | `grant_type is required` |
| Missing PKCE for public client | `pkce S256 required` |
| `code_challenge_method=plain` | `pkce plain is not supported` |
| Malformed `redirect_uri` | `redirect_uri mismatch` |
| Missing `state` on `/oauth/authorize` | `state is required` |
| `Content-Type` not `application/x-www-form-urlencoded` | `Content-Type must be application/x-www-form-urlencoded` |
| Unknown `transaction_id` on `/api/v1/oauth/authorize/approve` | `authorize transaction not found or expired` |
| CSRF mismatch on approve | `csrf token mismatch` |
| Authorize transaction already consumed | `authorize transaction has already been consumed` |
| Missing `grant_id` on revoke endpoints | `grant_id is required` |

**Client action**: Fix the request and retry. Do not auto-retry without
correcting the input — the same input will always fail.

### `invalid_client`

`HTTP 401` (Bearer / Basic) or `HTTP 400` (form post). Client
authentication failed or the client is unknown/disabled.

| Trigger | `error_description` example |
|---|---|
| Unknown `client_id` | `oauth client not found` |
| Disabled client | `client disabled` |
| Wrong `client_secret` for confidential client | `client authentication failed` |
| Public client sent `client_secret` (treated as misuse) | `client authentication failed` |
| Confidential client missing `client_secret` | `client authentication failed` |

**Client action**: Verify your `client_id`, `client_secret`, and that the
client row is enabled. Confidential clients must include credentials on
every token-endpoint call.

### `invalid_grant`

`HTTP 400`. The grant (authorization code, refresh token, or device code)
is invalid, expired, revoked, or doesn't match the supplied client.

| Trigger | `error_description` example |
|---|---|
| Authorization code unknown / expired | `authorization code expired` |
| Authorization code already used | `authorization code already used` |
| `redirect_uri` doesn't match the value used in `/authorize` | `redirect_uri does not match the value used in /authorize` |
| `client_id` doesn't match the code's client | `client_id does not match the value used in /authorize` |
| PKCE verifier doesn't match challenge | `code_verifier does not match code_challenge` |
| Refresh token unknown / expired | `refresh_token expired` |
| Refresh token revoked | `refresh_token has been revoked` |
| **Refresh token reuse detected** | `refresh token reuse detected` |
| Refresh token belongs to a different client | (no description — explicit `error: invalid_grant` only) |
| Group not allowed for grant | `group not allowed for this grant` |
| Device code unknown | `device code not found` |
| Device code already consumed | `device code has already been consumed` |
| Device flow polling client mismatch | (no description — explicit `error: invalid_grant` only) |

**Client action**:
- For PKCE / redirect mismatch: a coding error; fix and start a new auth flow.
- For "refresh token reuse detected": treat as token theft. Discard all
  local OAuth state and force the user through `/oauth/authorize` again.
  The entire token family has been revoked server-side.
- For "expired" / "revoked" / "not found": prompt re-auth.

### `unauthorized_client`

`HTTP 400`. The client is not allowed to use this grant type.

| Trigger | `error_description` example |
|---|---|
| Client `device_flow_enabled=false` requesting device grant | `device flow not enabled for this client` |
| Disabled client requesting any grant | `client disabled` |

**Client action**: Contact Sakrylle support to enable the grant type or use
a different `client_id`.

### `unsupported_grant_type`

`HTTP 400`. The token endpoint received a `grant_type` it doesn't
recognize.

Supported values:
- `authorization_code`
- `refresh_token`
- `urn:ietf:params:oauth:grant-type:device_code`

**Client action**: Verify your token-endpoint request body.

### `unsupported_response_type`

`HTTP 302` (redirected back to the registered `redirect_uri`). Sakrylle
only supports `response_type=code` for `/oauth/authorize`.

**Client action**: Use Authorization Code flow only. Implicit grant
(`response_type=token`) is intentionally unsupported.

### `invalid_scope`

`HTTP 400`. The requested scope is unknown or not granted to this client.

| Trigger | `error_description` example |
|---|---|
| Unknown scope identifier | `scope not allowed: <scope>` |
| Scope not in client's `allowed_scopes` | `scope not allowed: <scope>` |
| Legacy alias requested **after** the alias sunset date | `scope not allowed: <scope>` |

**Client action**: Trim the scope list to the client's `default_scopes` or
explicitly allowed scopes. See
[`OAUTH_V2_SCOPE_MIGRATION.md`](./OAUTH_V2_SCOPE_MIGRATION.md) if you are
relying on legacy aliases.

### `access_denied`

`HTTP 302` (redirect back to `redirect_uri`) or `HTTP 400` (device flow).
The user denied the authorization request.

**Client action**: Show a friendly "permission denied" message. You may
restart the flow, but only after the user explicitly retries — auto-retry
loops are user-hostile.

### `server_error`

`HTTP 500`. Internal Sakrylle error. The request was well-formed but
processing failed.

**Client action**: Retry with exponential backoff (initial delay ≥ 1s).
If it persists, contact `support@sakrylle.com` with the `request_id`.

### `temporarily_unavailable`

`HTTP 503`. The OAuth provider is in maintenance or has been disabled
(`oauth_provider_enabled=false`).

**Client action**: Back off for at least 60 seconds before retrying.
Display "Service temporarily unavailable" to the user.

---

## Device Flow polling errors

These appear only on `/oauth/token` with
`grant_type=urn:ietf:params:oauth:grant-type:device_code` (RFC 8628 §3.5).

### `authorization_pending`

`HTTP 400`. The user has not yet approved or denied the device. **This is
the expected polling response** while waiting for user action.

**Client action**: Sleep for `interval` seconds and poll again.

### `slow_down`

`HTTP 400`. The client is polling faster than the server-issued
`interval`. The interval has been increased by 5 seconds.

**Client action**: Add 5 seconds to your local poll interval and continue.
The increased interval is sticky for the lifetime of the device code.

### `expired_token`

`HTTP 400`. The device code has passed `expires_in` without being approved.

**Client action**: Discard the device code and create a new one via
`/oauth/device/code`. Users typically need a longer time window or a
clearer CTA.

---

## Resource endpoint errors

`/v1/*` Bearer-token errors (RFC 6750):

### `invalid_token` (HTTP 401)

| Sub-condition | `WWW-Authenticate` | `error_description` |
|---|---|---|
| Missing token | `Bearer error="invalid_token", error_description="missing bearer token"` | `missing bearer token` |
| Malformed / unknown token | `Bearer error="invalid_token"` | `token format invalid` |
| OAuth access metadata missing for `sk_oauth_` key | `Bearer error="invalid_token"` | (none) |
| Expired access token | `Bearer error="invalid_token", error_description="token expired"` | `token expired` |
| Revoked access token | `Bearer error="invalid_token", error_description="token revoked"` | `token revoked` |

**Client action**:
- For "expired": rotate via `/oauth/token` with `grant_type=refresh_token`.
- For "revoked" or anything else: discard local state and re-auth.

### `insufficient_scope` (HTTP 403)

`WWW-Authenticate: Bearer error="insufficient_scope", scope="<required>"`.

The token is valid, but doesn't carry the scope that the requested route
requires (per §7.3 endpoint matrix).

**Client action**: Re-authorize asking for the additional scope. Do not
silently re-prompt without explaining why.

---

## Manual API key parity

Manual API keys (`sk_*`, not `sk_oauth_*`) bypass the OAuth scope checks.
If you see `invalid_token` or `insufficient_scope` on a request that uses a
manual API key, it is a Sakrylle bug — please report.

---

## Cross-reference table

| HTTP | RFC code | Endpoint family |
|---:|---|---|
| 400 | `invalid_request`, `invalid_grant`, `invalid_scope`, `unsupported_grant_type`, `authorization_pending`, `slow_down`, `expired_token` | `/oauth/token`, `/oauth/device/code` |
| 401 | `invalid_client`, `invalid_token` | `/oauth/token`, `/oauth/revoke`, `/v1/*` |
| 403 | `access_denied`, `insufficient_scope` | `/api/v1/oauth/authorize/approve`, `/v1/*` |
| 500 | `server_error` | any |
| 503 | `temporarily_unavailable` | any |
| 302 | `unsupported_response_type`, `access_denied`, `invalid_scope` | `/oauth/authorize` |

---

## Logging conventions

Sakrylle never writes plaintext tokens, authorization codes, device codes,
user codes, PKCE verifiers, or client secrets to logs. Every audit event
records `user_id`, `client_id`, `grant_id`, `token_family_id` (when
applicable), and a redacted IP/User-Agent pair plus the request ID. Cite
the `request_id` returned with `error_description` when filing a support
ticket.
