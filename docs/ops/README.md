# Operations Docs

This directory holds Sakrylle API operational facts, gotchas, and "why" decisions. Keep `CLAUDE.md` as a short index and put long notes here.

## Index

- [infrastructure.md](infrastructure.md) — production topology, deploy commands, companion services, common SSH/Docker ops.
- [customizations.md](customizations.md) — Sakrylle fork behavior changes and currency policy.
- [identity-email.md](identity-email.md) — SMTP, OAuth/OIDC, Sakrylle Web SSO, notification templates.
- [channels-and-billing.md](channels-and-billing.md) — channel/group topology, pricing, model mapping, operational traps.
- [async-image-bridge.md](async-image-bridge.md) — async image task bridge, synthetic usage, pricing display rules.
- [admin-dns.md](admin-dns.md) — admin password rotation, Cloudflare DNS, China access notes.
- [upstream-sync.md](upstream-sync.md) — upstream merge/rebase, Wire regeneration, recurring conflict rules.

## Governance

- `docs/ops/` is the canonical in-repo place for operational facts.
- Product-facing docs for `doc.sakrylle.com` live outside this repository unless explicitly reintroduced.
- When changing OAuth/OIDC behavior, scopes, claims, client registration, configuration isolation, branding rules, or rollout state, update the relevant ops doc in the same change or state why no doc update was needed.
- Do not add secrets, private keys, token values, or production passwords to this directory.
