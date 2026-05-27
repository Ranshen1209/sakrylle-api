# Sakrylle OAuth 2.0 v2 — Scope Migration Guide

> Companion to [`OAUTH_V2_INTEGRATION.md`](./OAUTH_V2_INTEGRATION.md). For the
> full canonical scope registry, see §7.1 of
> [`OAUTH_V2_DESIGN.md`](./OAUTH_V2_DESIGN.md).

This document is for client developers whose code today requests v1 scope
strings (`image_generation`, `balance:read`). It explains how Sakrylle's
v2 deployment treats those strings, when they will stop working, and what
to change in your code.

The legacy alias dictionary lives in
`backend/internal/service/oauth_scopes.go` as `legacyScopeAliases` — the
mapping below is reproduced verbatim from that source.

---

## 1. Legacy alias map (source of truth)

| Legacy v1 scope | Canonical v2 scope | Notes |
|---|---|---|
| `image_generation` | `images:create` | Existing Image Playground scope; covers `/v1/images/generations` and `/v1/images/edits`. |
| `balance:read` | `account:balance:read` | Strictly the balance/currency-display field. Does NOT widen to `account:read`. |

That's the entire dictionary. Any other v1 scope identifier is treated as
unknown and rejected with `invalid_scope`. `models:read` was already
canonical in v1 and is unchanged in v2.

> If your client requests `account:read` and expects to see the user's
> balance, that has always required either `account:balance:read` or the
> umbrella `account:read` scope. v2 doesn't change that — it just renames
> the legacy `balance:read` shorthand.

---

## 2. What the server does with a legacy scope today

`NormalizeScopes` runs at every entry point that handles scope strings:

- `/oauth/authorize` request validation: legacy values are accepted and
  rewritten to canonical before the consent page renders.
- `/api/v1/oauth/authorize/approve`: scopes stored on the resulting
  authorization code are canonical.
- `/oauth/token` exchange: minted access/refresh tokens carry canonical
  scopes; the response `scope` field is canonical.
- `/oauth/token` refresh: rotation preserves the canonical scope list.
- Resource gate (§7.3 endpoint matrix): `HasScope` accepts both legacy
  and canonical inputs against a canonical `required` value, so legacy
  rows on disk continue to authorize their corresponding endpoints.

In other words: today, legacy scopes are silently rewritten everywhere
they appear. Existing v1 clients with persistent refresh tokens whose
stored scopes contain `image_generation` continue to call
`/v1/images/generations` after the v2 deployment with no changes.

---

## 3. Choose a migration path

### Path A — do nothing (recommended during the deprecation window)

Keep your client requesting `image_generation` and `balance:read`. Sakrylle
rewrites them server-side. The token response will start returning the
canonical strings (`images:create`, `account:balance:read`). If your code
displays those strings to users (scope labels in a UI, audit logs, etc.),
you will see the canonical names appear.

This is fine for the deprecation window. It is **not** fine after the
sunset date — see [§5](#5-deprecation-timeline) below.

### Path B — upgrade to canonical now (recommended for active development)

Replace the legacy strings in your `/oauth/authorize` requests:

```diff
- scope=image_generation balance:read models:read
+ scope=images:create account:balance:read models:read
```

Optional: also adjust your client UI to display canonical names. The
`/oauth/token` response is already canonical regardless of what you sent,
so this only affects what your `/oauth/authorize` URL contains and any
hardcoded checks in your code.

If you have any client-side validation that hardcodes the legacy scope
strings, replace it with the canonical names. Example: a check like

```python
if "balance:read" in granted_scopes:
    show_balance_widget()
```

should become

```python
if "account:balance:read" in granted_scopes:
    show_balance_widget()
```

You don't need to support both forms. Sakrylle issues canonical scopes in
v2 token responses, including for grants whose request used aliases.

---

## 4. What changes in `/oauth/token` responses

Before v2 rollout, calling `/oauth/token` with `scope=image_generation`
might have echoed back `"scope": "image_generation"` (depending on v1
implementation details). v2 explicitly returns canonical:

```json
{
  "access_token": "sk_oauth_...",
  "scope": "images:create account:balance:read",
  ...
}
```

Even if your `/oauth/authorize` request used `image_generation balance:read`,
the response is canonical. Surface the canonical names in any UI you
build that displays "what this app can do."

The `/v1/me` endpoint's `granted_scopes` field is the same: canonical
strings only. So is the `scope` parameter on Bearer
`WWW-Authenticate: insufficient_scope` headers.

---

## 5. Deprecation timeline

The aliases are accepted indefinitely as a transitional convenience. The
explicit sunset rule (per §7.2):

> Sunset for new authorize requests is **90 days** after the production
> deploy that enables `oauth_scope_enforcement_enabled=true`, recorded in
> release notes and external docs. After the sunset, new authorize
> requests containing `image_generation` or `balance:read` return
> `invalid_scope`, while already-issued tokens remain accepted until their
> refresh-token family expires or is revoked.

The exact sunset date is announced via release notes and the
`doc.sakrylle.com` developer changelog. Until then:

| Phase | New `/oauth/authorize` requests | Existing tokens with stored legacy scopes |
|---|---|---|
| Pre-enforcement | Accepted, rewritten to canonical. | Accepted. |
| Enforcement on (current) | Accepted, rewritten to canonical. | Accepted. |
| Post-sunset | `invalid_scope`. | Accepted until family expiry / revocation. |

Once the sunset passes, your client cannot mint **new** tokens with the
legacy strings, but tokens it already holds keep working until the
underlying refresh family expires (default 30 days) or is revoked.

Plan the cut-over for your client at least one refresh-token TTL before
the sunset date. Migrating to Path B early gives you the longest possible
buffer.

---

## 6. Migration testing checklist

- [ ] Client sends `images:create` (or `image_generation`) — both 200.
- [ ] Token response `scope` is canonical for new tokens.
- [ ] Client UI displays canonical scope labels (or maps both forms to
      the same display string).
- [ ] `/v1/me.granted_scopes` is canonical.
- [ ] `/oauth/token` refresh keeps canonical scopes.
- [ ] After sunset (in staging if available), legacy request returns
      `invalid_scope`.
- [ ] Existing customer tokens still work after the deploy. Smoke-test
      with a long-lived refresh token sample.

---

## 7. Reading the alias map directly

If you script against the canonical scope registry, the authoritative
list is exposed via discovery:

```bash
curl -sS https://sub.sakrylle.com/.well-known/oauth-authorization-server \
  | jq '.scopes_supported'
```

`scopes_supported` returns the **canonical** list only (legacy aliases are
not advertised as first-class scopes). The legacy aliases are documented
exclusively in this guide and §7.2 of `OAUTH_V2_DESIGN.md`.

If you need the alias map programmatically inside Sakrylle's own codebase,
the canonical source is `legacyScopeAliases` in
`backend/internal/service/oauth_scopes.go`. Do not duplicate the map
elsewhere; reference the registry directly.
