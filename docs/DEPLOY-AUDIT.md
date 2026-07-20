# InstaEdit — Deployment Audit (HTTPS · DNS · redirect · hosting)

**Scope:** Block #2 of the broad site-quality plan — verify domain, HTTPS, DNS, redirect rules, and all deployment configuration surfaces: `fly.toml`, `web/vercel.json`, `ops/local/Caddyfile`, and the now-deprecated `ops/vps/Caddyfile` (moved to `ops/legacy/Caddyfile` in this block).

## Executive summary

- **HTTPS termination: ✅ covered on every public-facing surface.** Fly (`api.instaedit.org`) uses `force_https = true` + Fly-managed Let's Encrypt. Vercel (`app.instaedit.org` + apex `instaedit.org`) terminates TLS automatically. Local dev (`ops/local/Caddyfile`) uses mkcert + cloudflared.
- **DNS delegation: ✅ documented canonical records in `docs/DEPLOY.md` §1.5.** Apex `instaedit.org` A → Vercel Anycast `76.76.21.21`. `app.instaedit.org` CNAME → `cname.vercel-dns.com.` (Vercel edge). `api.instaedit.org` CNAME → `instaedit-login.fly.dev.` (Fly). CAA records restrict issuance to LE; SPF + DKIM + DMARC wired for Resend.
- **Apex 301 redirect to canonical**: **✅ fixed in commit `8271639` on `main`.** Previously only declared via the Vercel dashboard (not auditable in git); now declarative in `web/vercel.json` `redirects[]`. Also redirects `www.instaedit.org` → `app.instaedit.org` (the `www.` variant was previously undocumented).
- **Canonical SEO host: ✅ fixed in commit `8271639` on `main`.** `web/index.html` `og:url`, `twitter:url`, JSON-LD `url`, `image`, `author.url` switched from `https://instaedit.org/` (the apex that 301-redirects) to `https://app.instaedit.org/` (the landing surface). `sitemap.xml` was already on this canonical. `<link rel="canonical" href="https://app.instaedit.org/" />` added for browser-level canonicalization.
- **Legacy VPS Caddy: ✅ moved to `ops/legacy/Caddyfile` (commit `8271639`).** The file describes a `dev.instaedit.org` deployment that pre-dates the current Fly + Vercel architecture. `git mv` preserves history; `docker-compose.yml` inline reference updated to match.

## DNS + hosting topology (post-audit)

| Surface | Host | Hosted by | TLS | Configuration source |
| --- | --- | --- | --- | --- |
| Marketing SPA (apex) | `instaedit.org` | Vercel (A `76.76.21.21`) | Automatic LE (Vercel) | `web/vercel.json` `redirects[]` now enforces 301 → `app.instaedit.org` |
| Marketing SPA (app) | `app.instaedit.org` | Vercel (CNAME `cname.vercel-dns.com.`) | Automatic LE (Vercel) | `web/vercel.json` framework/rewrites |
| API backend | `api.instaedit.org` | Fly.io (CNAME `instaedit-login.fly.dev.`) | Automatic LE (Fly) | `fly.toml` `[[services]] api`, `force_https=true` |
| Legacy VPS (no longer on path) | `dev.instaedit.org` | Caddy + LE (manual, kept in repo as archaeology) | Manual LE | `ops/legacy/Caddyfile` (was `ops/vps/Caddyfile`) |
| Local dev tunnel | `https://:8443` (cloudflared) | mkcert + cloudflared on operator laptop | mkcert (local CA) | `ops/local/Caddyfile` |

OAuth callback URIs (registered in each provider's developer console) all terminate at the Fly backend:
- `https://api.instaedit.org/api/v1/auth/{instagram|facebook|threads|x|tiktok|youtube|linkedin}/callback`
- Sources of truth: `fly.toml` `[env]` (committed) + `docs/DEPLOY.md` §3 (canonical reference).

## HTTPS / TLS termination — finding-by-surface

### Fly.io (`api.instaedit.org`)
- `[[services]] processes = ["api"]` exposes `internal_port = 8080` on `port 80` (handlers `["http"]`, `force_https = true`) and `port 443` (handlers `["tls", "http"]`). ✅
- `release_command = "./migrate"` runs in an ephemeral machine with the SAME image + staged secrets; abort-on-failure prevents partial deploys (`docs/DEPLOY.md` §7.4).
- `min_machines_running = 1` + `auto_stop_machines = false` enforce always-on for both api + worker groups. ✅
- **HSTS not configured** in `fly.toml` directly — Fly delegates that to the edge proxy (none here). Real defect, deferred to dedicated hardening block.
- **Live status uncertainty**: `docs/DEPLOY.md` §8.1 records that as of 2026-07-14 the live `api.instaedit.org` was reporting `Server: Caddy` (i.e. not Fly) and may not match the latest deploy. Operator-side investigation required. Captured as open follow-up, not a config fix from this block.

### Vercel (`app.instaedit.org` + apex `instaedit.org`)
- `web/vercel.json` `framework: "vite"` — Vite plugin picks up Vite's SPA config. ✅
- `web/vercel.json` now declares apex `instaedit.org` + `www.instaedit.org → https://app.instaedit.org/$1` 301 redirect before the SPA `/(.*) → /index.html` rewrite (commit `8271639`). ✅
- **HSTS not declared** in `vercel.json` `headers[]` — real defect, deferred.
- Canonical SEO surface (in `web/index.html`): `og:url`, `twitter:url`, JSON-LD fields, `<link rel="canonical">` all reference `https://app.instaedit.org/` (commit `8271639`). ✅

### Legacy VPS (`dev.instaedit.org`) — `ops/legacy/Caddyfile`
- Was mounted under `/srv/instaedit/web/dist` and reverse-proxied `/api/*` + `/instaedit-dev/*` on the host. Not in the production path for months.
- **Moved to `ops/legacy/Caddyfile`** in commit `8271639` via `git mv` (history preserved). `docker-compose.yml` inline reference at the Velox bridge section now reads `ops/legacy/Caddyfile + ops/local/Caddyfile`.
- File content unchanged — kept as historical record of the pre-Fly/Vercel shape; do NOT use as a deploy reference.

### Local dev tunnel (`ops/local/Caddyfile`)
- Hostname-less `https://:8443` block with mkcert certs from `~/instaedit-certs/localhost.pem`. Behind cloudflared for off-laptop access. ✅
- Internal (`/internal/*`) is `abort`ed before any reverse-proxy pass — same hardening as the (former) VPS Caddyfile.
- TLS `auto_https off` (Caddy global section) — this is local-only, the mkcert cert is pinned in the site block.
- No production exposure path; audit-clean because not on the deployment surface.

## Redirect rules

| Source                          | Destination                      | Surface                       | Where it's declared                                      | Status |
| ------------------------------- | -------------------------------- | ----------------------------- | ------------------------------------------------------- | ------ |
| `instaedit.org/(.*)`            | `https://app.instaedit.org/$1`   | Vercel edge (apex)            | `web/vercel.json` `redirects[]` (commit `8271639`)       | ✅     |
| `www.instaedit.org/(.*)`        | `https://app.instaedit.org/$1`   | Vercel edge                   | `web/vercel.json` `redirects[]` (commit `8271639`)       | ✅ NEW |
| Fly port 80 (`api.instaedit.org`) | `https://api.instaedit.org`    | Fly `[[services]]`            | `fly.toml` `force_https = true`                          | ✅     |
| `/connections`                    | `/app/linking`                   | React Router                  | `web/src/App.tsx` `<Navigate … replace />`               | ✅     |
| `*` (unknown route)               | `/`                              | React Router                  | `web/src/App.tsx` `<Route path="*">`                      | ✅     |
| `/api/v1/auth/(provider)/login`   | Provider OAuth dialog             | Go backend                    | `internal/services/{provider}_oauth.go`                   | ✅     |

## Findings (categorized)

### A. Canonical host divergence (REAL defect, **fixed** in `8271639`)

`web/index.html` (5 sites):
1. `<meta property="og:url" content="https://instaedit.org/" />`
2. `<meta name="twitter:url" content="https://instaedit.org/" />`
3. `<meta name="twitter:image" content="https://instaedit.org/app-icon-1024.png" />`
4. JSON-LD `"url": "https://instaedit.org/"`
5. JSON-LD `"image": "https://instaedit.org/app-icon-1024.png"`
6. JSON-LD `"author": { "url": "https://instaedit.org/" }`

All six updated to `https://app.instaedit.org/…` plus a new `<link rel="canonical" href="https://app.instaedit.org/" />` for browser-level canonicalization. `web/public/sitemap.xml` was already correct (started aligned on `https://app.instaedit.org/`).

### B. Apex 301 in source control (REAL defect, **fixed** in `8271639`)

`web/vercel.json` previously had no `redirects[]` block — the apex → app redirect was configured only via the Vercel dashboard (not auditable in git, manual drift risk). Now declarative:
- `instaedit.org/(.*) → https://app.intaedit.org/$1` (permanent: true)
- `www.instaedit.org/(.*) → https://app.instaedit.org/$1` (permanent: true)

Both `has`-match on the `host` so they trigger only for the apex + `www.` hostnames, not for `app.instaedit.org` traffic (which falls through to the SPA rewrite).

### C. Legacy VPS Caddyfile on production path (REAL defect, **fixed** in `8271639`)

`ops/vps/Caddyfile` describes a `dev.instaedit.org` deployment built around `/srv/instaedit/web/dist` (VPS filesystem path) and a hand-rolled LE renewal. The current production topology is Fly (backend) + Vercel (frontend) — the VPS file is not on the path. File moved to `ops/legacy/Caddyfile` (Git history preserved) + `docker-compose.yml` inline comment updated to match. File body unchanged (kept as historical record).

### D. HSTS / security headers (REAL defect, **deferred**)

No `Strict-Transport-Security`, `Content-Security-Policy`, `X-Content-Type-Options`, `X-Frame-Options`, or `Referrer-Policy` declared on any of the four landing surfaces (Fly api, Vercel SPA, legacy VPS, local tunnel). Fly has no native HSTS knob, so the right surface is `web/vercel.json` `headers[]` for the SPA + Fly edge config for the API. Deferred to a dedicated "harden headers" block — needs SPA + API coordination that doesn't fit a 1-commit block.

### E. DNSSEC / CAA records (REAL defect, **registrar-side only**)

`docs/DEPLOY.md` §1.5 documents DNSSEC enablement + CAA records (`0 issue "letsencrypt.org"`, `0 iodef "mailto:security@instaedit.org"`). Declarative in docs only — registrar-side opt-in (Cloudflare: one-click; Namecheap: DS records). No code change from this audit; tracked as runbook action.

### F. `docs/DEPLOY.md` §8.1 active warning (NOT a config fix)

Doc itself records that as of 2026-07-14 the live `api.instaedit.org` was responding with `Server: Caddy` (not Fly), missing `fly-request-id`, and `platforms: ["threads"]` only — likely DNS re-pointed, stale Fly deploy, or another developer's isolated deploy. Operator-side investigation required per the §8.1.1 DNS correction checklist. Tracked as ongoing operational risk, not a config fix.

## Per-surface appendix

### `fly.toml`

- `app = "instaedit-login"`, `primary_region = "iad"`.
- `[build] dockerfile = "Dockerfile"`, `build_target = "production"` — pinned explicitly.
- `[deploy]` rolling, `min_machines_running = 1`, `release_command = "./migrate"`.
- `[env]` shared non-sensitive config: GIN_MODE release, S3 endpoints (Tigris), Email provider (Resend), OAuth callback URIs (public, registered in provider consoles). Note: the OAuth redirect URIs in `[env]` are duplicated in `docs/DEPLOY.md` §3 and in `scripts/required-fly-secrets.txt` — all three are canonical, must agree on values.
- `[processes.api.env] PORT = "8080"` and `[processes.worker.env] WORKER_HEALTH_PORT = "9090"` — Fly ports 80/443 map to api; worker private-network 9090.
- `[[services]]` api/worker/migrate (migrate commented, doc-only); `[[metrics]]` scrapes api process's `/api/v1/metrics` (worker metrics live in a separate process registry, not exposed here by design).
- `[[vm]] shape = shared-cpu-1x / 512mb / auto_stop_machines = false` — enforces the no-scale-to-zero contract.

### `web/vercel.json`

- `framework: "vite"` picks up Vite plugin conventions.
- `buildCommand: "npm run build"`, `installCommand: "npm ci"`, `outputDirectory: "dist"`.
- `redirects[]` (comments `8271639`): apex + `www.` → `app.instaedit.org/$1` (301, declarative).
- `rewrites[]`: `/(.*) → /index.html` (SPA fallback).

### `ops/local/Caddyfile`

- `{ admin off; auto_https off }` global — single-origin dev tunnel.
- Hostname-less `https://:8443` with mkcert certs (SAN `localhost`).
- Hardens `/internal/*` (abort), proxies `/api/*` → `127.0.0.1:8080`, proxies `/instaedit-dev/*` → `127.0.0.1:19000` (local MinIO), and reverse-proxies all other traffic to `127.0.0.1:5173` (Vite dev server).

### `ops/legacy/Caddyfile` (was `ops/vps/Caddyfile`)

- Single site block `dev.instaedit.org` with `encode gzip`, `handle /internal/* abort`, `/api/*` reverse-proxy to `127.0.0.1:8080`, `/instaedit-dev/*` reverse-proxy to MinIO `:19000`, SPA fallback served from `/srv/instaedit/web/dist`.
- Body unchanged from when it shipped at `ops/vps/`. Kept as historical record. Do not deploy from this file.

### `docker-compose.yml` + `docker-compose.local.yml`

- Compose-merge caveat documented (overlay redeclares `healthcheck`, `networks`, `internal: true`).
- Local-only MinIO host binding `127.0.0.1:19000:9000` for VMs where `:9000` is occupied.
- Single inline reference to `ops/legacy/Caddyfile` + `ops/local/Caddyfile` updated from `ops/vps/Caddyfile + ops/local/Caddyfile` in commit `8271639`.

## Open follow-ups

1. **HSTS + security headers block** — Vercel `headers[]` for SPA + Fly edge config for API. Live-preview on Vercel requires the same headers to avoid leaking header choices across PRs (per `docs/ARCHITECTURE.md` §Velox runtime policy).
2. **`docs/DEPLOY.md` §8.1 active investigation** — operator-side: `dig +short api.instaedit.org CNAME`, `flyctl status`, re-run §8 Gates A/B/C.
3. **DNSSEC enablement at registrar** — Cloudflare one-click, Namecheap DS records. CAA records are already in the runbook; need to verify they propagate.
4. **www.instaedit.org 301** is now in source — verify Vercel dashboard is configured to accept the `www.` host (or remove that record from the redirects list if not desired).
5. **`ops/legacy/Caddyfile` body update** — the two `docker run` / `cp` bootstrap commands at the top of the file still reference `ops/vps/Caddyfile` (self-references). Tracked for a follow-up micro-commit if any operator needs to re-bootstrap the legacy setup.

## Verdict

**HTTPS, DNS, redirect, and deployment configuration: ✅ verified, three real defects shipped as a single block in commit `8271639` on `main`.** Open follow-ups: HSTS hardening (deferred), DEPLOY.md §8.1 live verification (operator-side), and registrar-side DNSSEC (no code). No branches created.
