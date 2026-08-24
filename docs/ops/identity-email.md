# Identity, OAuth, And Email

## SMTP

SMTP uses Resend (`smtp.resend.com:465`) since 2026-05-24.

DB `settings` keys:

- `smtp_host`
- `smtp_port=465`
- `smtp_username=resend`
- `smtp_password=<re_...>`
- `smtp_from=support@sakrylle.com`
- `smtp_use_tls=true`

Update via direct SQL because the admin PUT path can reset host/port, then restart `sub2api`.

DNS exists for SPF/DKIM/DMARC on `sakrylle.com` and `send.sakrylle.com`. Outlook/Gmail/163/iCloud deliver. QQ can still be delayed due to new-domain reputation; workaround is controlled by DB key `qq_email_warning_enabled`.

## OAuth Provider

Sakrylle issues tokens from `/oauth/*`; clients live in `oauth_clients`.

Important settings:

- `oauth_provider_enabled`
- `oauth_default_group_id=5` (GPT-Image)

Embedded frontend gotcha: `backend/internal/web/embed_on.go` SPA fallback must bypass `/oauth/`, otherwise OAuth routes get intercepted.

## v2 OAuth Client Default Group

v2 OAuth clients must carry `default_group_id`. They do not consume global `oauth_default_group_id`.

Without a client-level default group, login closes with `invalid_group` from `ResolveOAuthGroup`.

`sakrylle-cli` was rebuilt on 2026-06-12 with `default_group_id=3` (GPT-Pro). Migration 163 persists CLI registration for loopback `/callback` and `/auth/callback`, OIDC 8-scope support, and device flow. The default group is still operationally set, not hard-coded by the migration.

`device_authorization_endpoint` was moved into `commonDiscoveryMetadata` so OIDC discovery exposes device auth as well as RFC 8414.

## Sakrylle Studio OIDC Client

Production has `sakrylle-studio`:

- issuer: `settings.oauth_issuer=https://oidc1.sakrylle.com`
- `platform.sakrylle.com` exposes the same OAuth/OIDC routes, but discovery intentionally continues to advertise `oidc1.sakrylle.com` so existing strict-issuer clients remain compatible.
- public desktop client
- PKCE required
- `default_group_id=3`
- redirect URIs: `http://127.0.0.1/callback`, `http://localhost/callback`
- scopes: `openid`, `profile`, `email`, `models:read`, `responses:create`, `messages:create`, `usage:read`, `offline_access`

DeepSeek Files API routes (`/v1/files` and `/files`) accept either `messages:create` or `responses:create`; no separate Files scope is required. Existing Studio tokens therefore retain access to file-backed vision requests after reauthentication.

Studio uses `http://127.0.0.1:<port>/callback`. Random loopback ports are allowed, but the path must be exactly `/callback`; `/oauth/callback` is rejected.

DB-only client changes need `docker compose restart sub2api`.

## GitHub OAuth Login

Sakrylle-as-client login uses:

- `github_oauth_enabled`
- `github_oauth_client_id`
- `github_oauth_client_secret`
- redirect URL settings

Manage via direct SQL when needed.

## Password Reset

Password reset requires `frontend_url=https://ai1.sakrylle.com` in settings. Missing setting causes 500.

## Sakrylle Web OIDC SSO

`chat.sakrylle.com` runs open-webui `0.9.6` from `/opt/stack/sakrylle-web/.env`. It consumes the single-token RP contract: users call the gateway with their own OIDC token, not a static key.

Required `.env` details:

- `OPENAI_API_KEYS=` must stay empty. A stale static key makes model loading return empty due to 401.
- `OPENAI_API_CONFIGS={"0":{"auth_type":"system_oauth","model_list_query":{"groups":"all"}}}`. `model_list_query` is required; otherwise model dropdown only sees the default group.
- `OAUTH_SCOPES` must include `chat.completions:create responses:create messages:create usage:read`; otherwise listing models works but chat returns 403 `insufficient_scope`.
- After scope changes, users must log in again to get new scopes.
- `OAUTH_CODE_CHALLENGE_METHOD=S256` is required because the client is confidential but PKCE is still enforced.
- Client `default_group_id` is required; current active GPT-Pro is `14` (avoid soft-deleted same-name group `13`).
- `client_secret` is bcrypt-only in DB. Generate server-side and use psql stdin dollar quoting so `$2b$` is not eaten by the shell.
- Nginx upstream inside compose must use `proxy_pass http://sakrylle-web:8080`, not `127.0.0.1`.

Cross-group calls use body `model:"<gid>:<model>"`; billing and routing bind to the prefixed group if it is in `allowed_groups_snapshot`.

After `.env` changes:

```bash
docker compose up -d --force-recreate sakrylle-web
```

## Notification Email Templates

There are 24 templates: 12 events times 2 locales. DB key format:

```text
notification_email_template:<event>:<locale>
```

Design: Monet purple header, inline styles, dark-mode support. Historical one-off generator was `/tmp/sakrylle_email_templates.py`.
