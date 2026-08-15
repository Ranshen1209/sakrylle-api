---
name: exporting-user-emails
description: Use when asked to export, dump, or collect Sakrylle user email addresses for a broadcast/announcement/mass email, or when building a recipient list from the production users table.
---

# Exporting Sakrylle User Emails

## Overview

User emails live in the `users.email` column of the production Postgres DB (`sub2api` database, `sub2api-postgres` container on `ssh-tokyo`). There is no admin export button — you reach the data over SSH → `docker exec` → `psql`.

Core principle: produce a **paste-ready, comma-separated** list (one `psql` query with `string_agg`), and always surface the **BCC + batching + strip-junk** caveats before the user sends.

## Access Path

```
ssh ssh-tokyo → docker exec sub2api-postgres → psql -U sub2api -d sub2api
```

## Quick Reference

| Goal | Command |
|---|---|
| Count users / with email | `SELECT count(*), count(*) FILTER (WHERE email IS NOT NULL AND email<>'') FROM users;` |
| Paste-ready list | `string_agg(email, ', ' ORDER BY email)` (see below) |
| Newline-per-line | `string_agg(email, E'\n' ORDER BY email)` |

## Implementation

Paste-ready comma-separated export (`-t` tuples-only, `-A` unaligned strips padding):

```bash
ssh ssh-tokyo "docker exec sub2api-postgres psql -U sub2api -d sub2api -t -A -c \"SELECT string_agg(email, ', ' ORDER BY email) FROM users WHERE email IS NOT NULL AND email <> '';\""
```

Write to a local file the user can copy from (note: macOS folder is `~/Downloads`, not `~/Download`):

```bash
# capture output, then Write tool → /Users/<user>/Downloads/user_mail.txt
```

## Sending Caveats — ALWAYS surface these

The user wants to mass-email. Before they send, flag:

1. **Use BCC, never To/CC** — otherwise every recipient sees all ~160+ addresses. Privacy leak.
2. **Batch it** — Resend (the configured SMTP, see `docs/ops/identity-email.md`) and most providers cap recipients per message, and one message with 160 addresses trips spam filters. Suggest ~30–50 per batch, or Resend's Broadcast/batch API.
3. **Strip junk before sending** — the list contains `admin@sakrylle.com` (own admin account) and disposable/throwaway domains (e.g. `web5h.com`, `ibymail.com`, `sanfranmail.com`). Offer a cleaned version.

## Common Mistakes

- Default `psql` output has column headers + alignment padding → not paste-ready. Use `-t -A`.
- `~/Download` (singular) does not exist on macOS; it's `~/Downloads`. Verify with `ls -d ~/Downloads`.
- Emails are case-preserved (`871600482@QQ.com`) — don't lowercase blindly; some providers treat local-part as case-sensitive.
- Don't echo the full list into chat AND a file unnecessarily — for a copy task, the file is what they asked for.
