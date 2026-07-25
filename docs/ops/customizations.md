# Customizations

Sakrylle API is a visual rebrand plus backend behavior changes. Core gateway logic is not meant to be casually rewritten.

## Theme

The brand color is Monet purple `#9181bd` (upstream teal was `#14b8a6`). Search `frontend/tailwind.config.js` and `frontend/src/` when extending theme usage.

The `primary-*` Tailwind palette is backed by CSS variables in `frontend/src/style.css`. Light mode uses the canonical palette; dark mode shifts the purple scale lighter for contrast. Keep new brand styling on `primary-*` utilities so it follows the active light/dark class automatically. `frontend/src/main.ts` also synchronizes native control and browser chrome colors whenever the root theme class changes.

## Backend Behavior

- `/v1/models` never falls back to `claude.DefaultModels` or `openai.DefaultModels`, preventing leakage of unconfigured models.
- SMTP nil-auth is allowed in `email_service.go`.
- Turnstile follows the site's dark mode, not the OS theme.
- `sk_oauth_` tokens may select a group per request by using `model: "<group_id>:<model>"`.
- `GET /v1/models?groups=all` lists all allowed groups with prefixed model IDs and `group{}` metadata.
- Routing and billing rebind to the selected group's `rate_multiplier`, bounded by the token's `allowed_groups` snapshot.
- Group override is OAuth-only and limited to non-subscription groups.

Implementation references:

- `server/middleware/group_override.go`
- `service/oauth_provider_group_select.go`
- mounted on the `/v1` chain after `apiKeyAuth` in `routes/gateway.go`

## Currency Policy

UI renders `￥` everywhere. This is display-only; numeric values are not converted.

Example: `users.balance = 8.94` used to render as `$8.94`; now it renders as `￥8.94`. The DB value is unchanged.

Why: avoids FX conversion bugs, price drift, audit log off-by-ones, recharge pipeline double conversion, and reconciliation drift.

Not changed:

- `/v1/models` pricing standard
- `balance_recharge_multiplier`
- DB column names such as `balance`, `quota_used`, `daily_limit_usd`
- `channel_model_pricing` rows

Regression watch:

```bash
grep -rEn '￥\{[a-zA-Z_]' frontend/src
```

`￥{identifier}` is likely mangled JS interpolation; real i18n should look like `￥{usd}` only when intentionally formatted.
