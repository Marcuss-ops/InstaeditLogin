# Operations — Deploy edge (DNS + TLS/Caddy)

Part of the [Operations runbook](OPERATIONS.md) documentation set. This file
holds the **edge-routing** operational state: DNS records for `instaedit.org`
and the TLS certificate lifecycle on the VPS (Caddy + Let's Encrypt).

Related documents:

- [Monitoring baselines + go-live gate](operations-monitoring.md)
- [Recovery drills + storage + worker recovery](operations-runbook.md)
- [Email provider runbook (Resend)](operations-email.md)

---

## 1. DNS records (`instaedit.org`)

For the canonical topology and DNS table see `docs/DEPLOY.md` §1. This section covers
the **why** behind each record + the failure modes that trigger a
reissue.

### 1.1 Authority + delegation

| Apex registrar | Domain controller | Notes |
|----------------|-------------------|-------|
| Cloudflare (preferred) | NS `anna.ns.cloudflare.com`, `bob.ns.cloudflare.com`, … | Keep `api.instaedit.org` DNS-only so Caddy can complete the LE HTTP-01 challenge against the VPS. Manage `app.`, apex, and `www.` exactly as shown by the Vercel project; their TLS terminates at Vercel. |
| Namecheap (fallback) | domain basicDNS | Point only `api.instaedit.org` at `51.91.11.36`; configure apex, `app.`, and `www.` with the Vercel-managed records supplied by the project. |
| Route 53 (fallback) | provider-managed records | Use the Vercel project targets for apex, `app.`, and `www.`; use the VPS address for `api.instaedit.org`. Do not replace a Vercel-managed record with a VPS A record. |

### 1.2 Failure recovery — Caddy / Let's Encrypt HTTP-01

**Symptoms:** `curl -sI https://api.instaedit.org/api/v1/health` returns `server:` other than `Caddy`, OR returns a Caddy error page mentioning `acme`, OR the cert is older than the expected auto-renew window (60 days).

**Root cause:** LE HTTP-01 challenge could not reach the VPS on port 80 + path `/`.well-known/acme-challenge/...`.

Triage checklist (from the operator laptop):

```bash
# 1. Confirm the backend DNS resolves to the VPS
dig +short api.instaedit.org A  # expect: 51.91.11.36

# 2. Confirm host-managed Caddy is up + listening
ssh instaedit@$VPS_IP 'sudo systemctl is-active --quiet caddy'
#   expect: exit 0; Caddy is host-managed and owns ports 80/443

# 3. Confirm Caddy can serve the LE challenge path from the public IP
ssh instaedit@$VPS_IP 'sudo journalctl -u caddy --since "1 hour ago" --no-pager | grep -iE "acme|certificate|renew"'

# 4. From external internet (operator laptop), confirm a known 200 path
curl -fsS https://api.instaedit.org/api/v1/health | jq
#   expect: {"status":"ok","service":"InstaEditLogin",...}
```

**Common fixes** (all commands run via `ssh instaedit@$VPS_IP` unless noted):

- The previous (wrong) A record was cached downstream → lower TTL to 60s globally, wait one old-TTL window before retrying. Caddy renews nightly; the next renewal cycle catches the corrected target.
- Cloudflare proxy was turned on for `api.instaedit.org` → set that backend record to DNS-only (grey cloud). Do not change the Vercel-managed frontend records to point at the VPS.
- Firewall on the VPS blocks TCP/80 or TCP/443 → `sudo ufw allow 80/tcp && sudo ufw allow 443/tcp && sudo ufw reload`. Confirm with `sudo ufw status`.
- Caddy's certificate storage is full or corrupt → inspect `sudo journalctl -u caddy`, preserve the existing certificate data, and follow the host-level Caddy recovery procedure before considering re-issuance.
- **Storm recovery:** LE has a hard limit of 5 failed validations per account per hostname per hour. Wait at least 60 minutes between retries if the failure count is the limiter.

Workaround if the VPS is unreachable beyond quick repair: temporarily
point only the `api.instaedit.org` record at a known-good Caddy origin (e.g.
an emergency standby host). Leave the Vercel-managed apex, `app.`, and `www.`
records unchanged; Caddy will renew against the new API target on the next
cycle.

### 1.3 Cert renewal — proactive (was: "Vercel TXT validation")

**Symptoms:** nothing — Caddy renews silently ~30 days before expiry.
We watch the cert state via `sudo journalctl -u caddy --since 168h --no-pager | grep -iE "renew|certificate|expir"`.

Triage (operator-on-call cadence: weekly 5-minute check):

```bash
ssh instaedit@$VPS_IP 'sudo journalctl -u caddy --since 168h --no-pager | grep -iE "renew|certificate|expir"'
#   expect: "certificate obtained successfully" OR "renewing certificate"
#   failure: no renewal lines in the last 7 days → Caddy rejected renewal
```

Common causes:

- The VPS IP changed and the `api.instaedit.org` record is stale → update only the backend record, validate it externally, and let Caddy re-discover the API certificate. Frontend DNS remains Vercel-managed.
- The API hostname was removed from the Caddyfile → compare against `git log main -- ops/vps/Caddyfile` and restore only the reviewed `api.instaedit.org` route. Frontend hostnames are configured in Vercel, not Caddy.
- The VPS port 80 (LE HTTP-01) was blocked mid-renewal → see §1.2 step "firewall".

### 1.4 Apex CNAME-flattening breaks

CNAME at apex is illegal per RFC. ALIAS / ANAME / CNAME-flattening is
registrar-specific and fragile. We deliberately use:

- Apex, `app.`, and `www.` → the Vercel-managed records supplied by the project (Vercel redirects apex/www to `app.`).
- `api.instaedit.org` → the VPS address `51.91.11.36` (or the current `$VPS_IP`).
- Do not add an apex A/AAAA record pointing the frontend at the VPS unless the Vercel project explicitly requires it.

If you ever need to migrate registrars (Namecheap → Cloudflare), copy the
Vercel-managed frontend records from the project dashboard and the API record
to the new provider. Do not introduce apex A/ALIAS-flattening records pointing
the frontend at the VPS.

---

## 2. TLS certificate lifecycle

Vercel terminates TLS for the apex, `app.`, and `www.` frontend hosts. Caddy
on the VPS obtains and renews the LE certificate for `api.instaedit.org`
(and the compatibility-only `dev.instaedit.org` block when it is enabled).
Renewal windows are 30 days before expiry; Caddy auto-renews every ~60 days.
Failure modes:

| Symptom | Fire alarm | Runbook |
|---------|------------|---------|
| `curl -sI https://api.instaedit.org/api/v1/health` returns `Server:` other than `Caddy` | Sentry `tls.origin` capture OR uptime monitor | Re-check §1.2 — DNS + firewall + the host-managed Caddy service and journal |
| Browser shows `NET::ERR_CERT_AUTHORITY_INVALID` for `app.` or `api.` | Sentry capture + manual verification | For `app.`/apex/`www.`, inspect the Vercel domain and certificate status. For `api.`, inspect `sudo journalctl -u caddy | grep -i issuer`, Caddy validation, and DNS CAA records. |
| Browser shows `NET::ERR_CERT_DATE_INVALID` | Uptime monitor ping fails | Check upstream — REGRESSION-class bug, file incident |
| Caddy logs show `failed to obtain certificate: acme: error: ... rateLimited` | Sentry capture within an hour of the failure | LE rate-limit hit. See §1.2 storm-recovery hint. |

### 2.1 Reload Caddy (after editing `ops/vps/Caddyfile`)

**Caddy is NOT part of the Docker Compose stack.** It is a host-managed
systemd service. The tracked `ops/vps/Caddyfile` is the source of truth on the
operator laptop; edit it there, sync it to the VPS, then validate and reload
with `sudo caddy validate` and `sudo systemctl reload caddy`.

**Systemd service** (install Caddy from the distribution or official Caddy
APT repository; manage it through `caddy.service`):

```bash
# 1. Edit the tracked source, commit it, and fast-forward the VPS checkout.
$EDITOR ops/vps/Caddyfile
git add ops/vps/Caddyfile && git commit -m 'caddy: <change>' && git push origin main
ssh instaedit@$VPS_IP \
  'cd /opt/instaedit/InstaeditLogin && git pull --ff-only origin main'

# 2. Copy the reviewed Caddyfile into /etc/caddy/Caddyfile on the VPS
ssh instaedit@$VPS_IP 'sudo install -m 0644 /opt/instaedit/InstaeditLogin/ops/vps/Caddyfile /etc/caddy/Caddyfile'

# 3. Validate before reload
ssh instaedit@$VPS_IP 'sudo caddy validate --config /etc/caddy/Caddyfile'

# 4. Reload via systemd
ssh instaedit@$VPS_IP 'sudo systemctl reload caddy'

# 5. Verify
ssh instaedit@$VPS_IP 'sudo systemctl status caddy'
#  expect: Active: active (running)
```

**Cross-reference**: `ops/vps/Caddyfile` is the source of truth. Do not edit
`/etc/caddy/Caddyfile` on the VPS directly; always edit the tracked source,
commit it, fast-forward the VPS checkout, validate, and reload through systemd.
The containerd `ctr` command is not part of this deployment.
