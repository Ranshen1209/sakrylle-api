# Infrastructure And Deploy

## Repository And Production

- **Upstream**: [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api)
- **Fork**: [Ranshen1209/sub2api](https://github.com/Ranshen1209/sub2api), branch `theme/monet-purple`
- **Production node**: `sakrylle-la` (`154.44.8.202`), Ubuntu 24.04.4 LTS, SSH user `admin`
- **Daily SSH entry**: `ssh ssh-sakrylle`, resolving to `ssh-sakrylle.sakrylle.com:443`
- **Compose file**: `/opt/stack/docker-compose.yml`

Los Angeles is the only production node. The retired Hong Kong and Tokyo
servers are neither production nor rollback targets.

Public port 443 is shared by an Nginx stream listener using
`ssl_preread_protocol`:

```text
Internet :443
  -> Nginx stream
     -> SSH: host.docker.internal:22
     -> TLS: 127.0.0.1:8443
            -> Nginx HTTP virtual hosts
            -> application containers
```

Port 80 is handled by Nginx for redirects and any explicitly configured HTTP
traffic. There is no active `sslh` service or rollback profile.

### Port 443 change safety

The normal SSH alias itself depends on the port 443 stream path. Before changing
the stream configuration, firewall, Docker networking around Nginx, or `sshd`:

1. Establish a separate direct session with `ssh -p 22 admin@154.44.8.202`.
2. Keep that direct session open and confirm it can run commands.
3. Test the proposed Nginx configuration before reload.
4. Verify both TLS and `ssh ssh-sakrylle` from a second terminal before closing
   the direct session.

Never perform a 443-edge change while relying only on SSH-over-443.

## Compose Stack

The Sub2API production core is:

- `sub2api`
- `sub2api-postgres`
- `sub2api-redis`
- `relay-pulse`

Nginx and other companion applications share the stack. Query the deployed
file instead of relying on a copied service inventory:

```bash
ssh ssh-sakrylle 'cd /opt/stack && docker compose config --services'
```

The production Compose file pins deployed images by digest (`image@sha256:...`).
CI still publishes the Sakrylle image and its human-friendly `:purple` tag, but
that floating tag is not the production version lock. A deployment must update
the Compose digest to the reviewed CI artifact before recreating `sub2api`.
Record the old digest for rollback; do not convert production back to tag-only
deployment.

Configs live under `/opt/stack/`, including `sub2api/.env`,
`nginx/conf.d/*.conf`, `relay-pulse/config/`, `secrets/cloudflare.ini`, and
`certs/live/sakrylle.com/`. Treat every `.env` and file under `secrets/` as
sensitive: do not print them into terminals, logs, issues, or commits.

Admin bootstrap account: `admin@sub2api.local`; its bootstrap password comes
from `.env` as `ADMIN_PASSWORD`. Restarts do not reseed an existing admin user.

## Build And Deploy

A push to `theme/monet-purple` triggers GitHub Actions and publishes the image.
Production deployment is a separate, deliberate operation:

1. Confirm CI succeeded and identify the published image digest.
2. Back up the current Compose file and database.
3. Update only the `sub2api` image digest in `/opt/stack/docker-compose.yml`.
4. Validate with `docker compose config --quiet`.
5. Recreate `sub2api`, then check container and HTTP health.

Representative verification commands:

```bash
ssh ssh-sakrylle 'cd /opt/stack && docker compose config --quiet'
ssh ssh-sakrylle 'cd /opt/stack && docker compose up -d sub2api'
ssh ssh-sakrylle 'cd /opt/stack && docker compose ps sub2api sub2api-postgres sub2api-redis relay-pulse'
ssh ssh-sakrylle 'curl -ksS -o /dev/null -w "%{http_code}\n" https://api.sakrylle.com/health'
```

Local preview needs `JWT_SECRET` of at least 32 bytes,
`TOTP_ENCRYPTION_KEY` of exactly 32 bytes, and `POSTGRES_PASSWORD`. The default
local URL is `http://localhost:18080`.

## Production Ingresses

All current production A records below resolve to `154.44.8.202` and are DNS
only. See [admin-dns.md](admin-dns.md) for DNS and certificate operations.

| Domain | Purpose | Notes |
| --- | --- | --- |
| `platform.sakrylle.com` | Unified full app, API, and OAuth/OIDC ingress | Preferred unified entry. Existing canonical frontend and issuer settings remain unchanged for compatibility. |
| `ai1.sakrylle.com` | App alias | Current `frontend_url` remains here. |
| `api.sakrylle.com` | API-only ingress | Allows gateway/API paths and health checks; other paths return 404. |
| `oidc1.sakrylle.com` | OAuth/OIDC issuer | Strict clients depend on this issuer. |
| `sub.sakrylle.com` | Legacy app alias | Kept online, but not recommended for new links or health checks. |
| `automatic-delivery.sakrylle.com` | Agiso delivery bridge | Webhook and health surface only. |
| `doc.sakrylle.com` | VitePress docs | Served from the external private docs project. |
| `status.sakrylle.com` | `relay-pulse` monitor | Config hot-reloads. |
| `chat.sakrylle.com` | Sakrylle Web | Open WebUI companion service. |

The apex and `www` redirect to the application. API-specific Nginx route details
belong in the deployed `nginx/conf.d/` files; validate those files on the server
before treating a route list as current.

Locked docs policies for `doc.sakrylle.com`: refund is 3 days, SLA is
best-effort, no age limit, reselling needs written permission,
`codex-auto-review` is documented as `gpt-5.4`, and group names use
`Claude-Kiro`, `GPT-Pro`, and `GPT-Plus`.

## Common Ops

### Status and health

```bash
ssh ssh-sakrylle 'cd /opt/stack && docker compose ps sub2api sub2api-postgres sub2api-redis relay-pulse'
ssh ssh-sakrylle 'docker exec nginx nginx -t'
ssh ssh-sakrylle 'curl -ksS -o /dev/null -w "%{http_code}\n" https://api.sakrylle.com/health'
```

### Logs

```bash
ssh ssh-sakrylle 'cd /opt/stack && docker compose logs --tail=100 sub2api'
ssh ssh-sakrylle 'cd /opt/stack && docker compose logs --tail=100 nginx relay-pulse'
```

Do not paste logs containing authorization headers, cookies, connection strings,
or user data into the repository.

### Backup

Create backups on the production node; do not transfer large production
archives through the local workstation:

```bash
ssh ssh-sakrylle 'install -d -m 700 /opt/stack/backups && docker exec sub2api-postgres pg_dump -U sub2api -d sub2api > /opt/stack/backups/sub2api-db-$(date +%F-%H%M%S).sql'
ssh ssh-sakrylle 'cp -a /opt/stack/docker-compose.yml /opt/stack/backups/docker-compose-$(date +%F-%H%M%S).yml'
```

Verify the backup exists and is non-empty without printing its contents.

### Restart

Use restart for configuration or database-only changes that require cache reload:

```bash
ssh ssh-sakrylle 'cd /opt/stack && docker compose restart sub2api'
```

Use `docker compose up -d sub2api` after changing the pinned image digest. Always
run the status and health checks after either operation.

### User concurrency policy

Production user concurrency is 30, including `default_concurrency` and
auth-source defaults. This limits simultaneous gateway requests per user, not
the number of users. Multiple API keys or parallel Responses streams consume
multiple slots.

The gateway generates `429 rate_limit_error` with
`Concurrency limit exceeded for user` when its Redis user-slot wait times out;
this is not an upstream provider limit. Inspect `concurrency:user:<user_id>` and
`concurrency:wait:<user_id>` in Redis. A slot is normally reclaimed on request
completion or by the 30-minute TTL. Remove one only after confirming its request
is no longer active.
