# Customizations

Sakrylle API is a visual rebrand plus backend behavior changes. Core gateway logic is not meant to be casually rewritten.

## Theme

The brand color is Monet purple `#9181bd` (upstream teal was `#14b8a6`). Search `frontend/tailwind.config.js` and `frontend/src/` when extending theme usage.

The `primary-*` Tailwind palette is backed by CSS variables in `frontend/src/style.css`. Light mode uses the canonical palette; dark mode shifts the purple scale lighter for contrast. Keep new brand styling on `primary-*` utilities so it follows the active light/dark class automatically. Theme toggles go through `frontend/src/composables/useTheme.ts` with a Telegram-style compositor ripple anchored to the actual click point. Do not use root View Transitions here: repeated full-viewport snapshots can crash Chromium on dense dashboard pages. The active theme follows the browser's live `prefers-color-scheme` value; manual toggles are temporary and are not persisted across refreshes or later browser preference changes. `color-scheme` is owned by CSS `:root` / `:root.dark` only — do not write inline `color-scheme` during toggles.

OpenAI keys imported through the CC-Switch deeplink default to `gpt-5.6-sol`. The imported custom Codex provider name is intentionally `OpenAI`, which is CC-Switch's activation signal for remote compaction.

## Backend Behavior

- `/v1/models` never falls back to `claude.DefaultModels` or `openai.DefaultModels`, preventing leakage of unconfigured models.
- SMTP nil-auth is allowed in `email_service.go`.
- Turnstile follows the site's dark mode, not the OS theme.
- `sk_oauth_` tokens may select a group per request by using `model: "<group_id>:<model>"`.
- `GET /v1/models?groups=all` lists all allowed groups with prefixed model IDs and `group{}` metadata.
- Routing and billing rebind to the selected group's `rate_multiplier`, bounded by the token's `allowed_groups` snapshot.
- Group override is OAuth-only and limited to non-subscription groups.
- Channel token price cards support future-effective, timezone-aware peak/off-peak versions. Admin Channel Pricing owns the editor; Available Channels and Model Plaza show the current backend-resolved period and schedule.
- Channel Management also exposes the Sakrylle-only `channels.features` product metadata and `features_config.image_input_ratio` display ratio. The latter is display-only and must match the account `async_image_synth.image_input_ratio` used for billing.

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

When adding Sakrylle-only configuration, update every explicit field boundary in the same change: admin request/response DTOs, form/API conversion, edit hydration, persistence, user-facing whitelist DTOs where relevant, and i18n. Several older custom fields reached the backend but were absent from edit forms because only one of these boundaries was updated.

## Custom Configuration Frontend Coverage

The fork-only configuration audit compares `upstream/main...HEAD` and the local non-merge commit history. All administrator-safe runtime fields found by that audit have a frontend owner:

| Configuration | Frontend owner |
| --- | --- |
| Channel peak/off-peak price versions | Admin Channel Pricing; current period and schedule also appear in Available Channels and Model Plaza |
| `channels.features`, `features_config.image_input_ratio` | Admin Channel create/edit |
| `groups.image_only` | Admin Group create/edit; one-click duplicate preserves it |
| `accounts.credentials.image_models`, `image_default_size` | OpenAI API Key account create/edit, **Image and video bridge** |
| Async image task and `async_image_synth` fields | Same account panel, including the full size/quality token matrix |
| Agnes video model/path/timing/host fields | Same account panel |
| `qq_email_warning_enabled` | Admin Settings -> Features |

The following fork configuration intentionally remains operational rather than browser-editable:

- OAuth/OIDC client registrations, issuer identity, client secrets, and signing-key rotation. These define the security boundary and are managed by migrations, reconciliation tooling, and deployment settings.
- Agiso delivery credentials (`app_secret`, `access_token`, seller identity, and API endpoint). These are service secrets loaded from deployment configuration.
- Imported agent identity private keys and task/runtime IDs. Their dedicated import flow owns their lifecycle; the generic account editor must not expose private key material.

If a new Sakrylle field is safe for an administrator to change at runtime, absence from the relevant create and edit forms is a regression. Document operational-only exceptions here with the reason.
