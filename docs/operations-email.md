# Operations — Email provider runbook (`no-reply@instaedit.org`)

Part of the [Operations runbook](OPERATIONS.md) documentation set. This file
holds the **Resend-based transactional email sender** operational state:
canonical DNS records, DMARC progression, Gmail inbox test protocol, tracking
verification, and the `EMAIL_PROVIDER_KEY` capture plan.

Related documents:

- [Deploy edge (DNS + TLS/Caddy)](operations-deploy.md)
- [Monitoring baselines + go-live gate](operations-monitoring.md)
- [Recovery drills + storage + worker recovery](operations-runbook.md)

---

## 7. Email provider runbook (`no-reply@instaedit.org`)

Canonical reference for the Resend-based transactional email sender. Companion to `scripts/email/check-email-deliverability.sh` (read-only DNS verification). **NO app code commits in this section** — the backend does not yet wire Resend (see §7.5 for the deferred wiring plan).

[Section §7 verbatim from the previous runbook — Resend wiring is platform-agnostic: SPF apex TXT, DKIM CNAME, DMARC ramp, Gmail inbox test, tracking verification, EMAIL_PROVIDER_KEY capture protocol. References in §7.5 are sourced from `/opt/instaedit/secrets/.env.production` edits in the new VPS context.]

### 7.0 State assertion

After this runbook runs:

- [ ] SPF apex TXT at `instaedit.org`: `v=spf1 include:_spf.resend.com ~all` (warm-up `~all`)
- [ ] DKIM CNAME at `<selector>._domainkey.instaedit.org` → `<selector>.dkim.resend.com.` (selector from Resend dashboard)
- [ ] DMARC TXT at `_dmarc.instaedit.org`: `v=DMARC1; p=none; rua=mailto:security@instaedit.org; ruf=mailto:security@instaedit.org; pct=100` (warm-up `p=none`)
- [ ] Resend dashboard → Domains → `instaedit.org` shows green Verified badge
- [ ] Gmail inbox test passed (Authentication-Results: dkim=pass + spf=pass + dmarc=pass on a real Gmail address; email landed in INBOX not SPAM)
- [ ] `EMAIL_PROVIDER_KEY` captured in password manager `instaedit-login/email/EMAIL_PROVIDER_KEY` (scope = Sending Access ONLY). NOT yet pushed to `/opt/instaedit/secrets/.env.production` because the backend does not wire Resend yet.

### 7.1 DNS records (canonical Resend values, 2026)

Operator applies these records via the registrar dashboard (Cloudflare / Namecheap / Route 53). NO provisioning script exists — registrar APIs are heterogeneous and a misclick during provisioning could overwrite the SPF apex with a junk value, breaking all outbound mail. Verify with `./scripts/email/check-email-deliverability.sh` after applying.

| Host | Type | Value | TTL | Purpose |
|------|------|-------|-----|---------|
| `instaedit.org` (apex) | `TXT` | `v=spf1 include:_spf.resend.com ~all` | 3600 | Sender Policy Framework. The include host is `_spf.resend.com` (NOT bare `resend.com` — that was the pre-2024 convention; Resend moved to a `_spf.` sub-include in 2024 for separation of envelope-return SPF). `~all` (soft-fail) is canonical during the warm-up window because Gmail still accepts mail that fails SPF soft-fail; `-all` (hard-fail) would 5xx the first validation round of legitimate mail while the sender reputation is still ramping. |
| `<selector>._domainkey.instaedit.org` | `CNAME` | `<selector>.dkim.resend.com.` | 3600 | DKIM key rotation. The `<selector>` (typically `resend1`, `resend2`) is assigned by Resend when you add the domain. **Look at Resend dashboard → Domains → `instaedit.org` → Records** before pasting — the dashboard prints the actual selector. Make the CNAME target match exactly (`<selector>.dkim.resend.com.` with trailing dot); DNS resolvers normalise trailing dot but Resend's verifier expects the explicit form. |
| `_dmarc.instaedit.org` | `TXT` | `v=DMARC1; p=none; rua=mailto:security@instaedit.org; ruf=mailto:security@instaedit.org; pct=100` | 3600 | DMARC warm-up. `p=none` (no enforcement — just collects reports). Make sure `security@instaedit.org` mailbox exists BEFORE flipping `p=quarantine` (otherwise rua/ruf reports get rejected by your own receiver — a classic ops-blind-spot). |

### 7.2 DMARC progression schedule

The 2026 best-practice for brand-new sender domains enforces a slow ramp because Gmail's DMARC alignment curve is conservative:

| Phase | Days | DMARC policy | Exit condition (verified via Google Postmaster Tools + rua reports) |
|-------|------|--------------|--------------------------------------------------------------------|
| **1. Collect** | 0–28 | `p=none` | At least 2 weeks of rua reports show >99% SPF + DKIM alignment for legitimate mail; no spoofing detected on the apex. |
| **2. Soft-enforce** | 28–42 | `p=quarantine; pct=50` | Half of failing mail moves to SPAM; Postmaster Tools "Domain reputation" tab shows ≥ Medium. |
| **3. Quarantine** | 42–70 | `p=quarantine; pct=100` | 100% of spoofed mail moves to SPAM; no reports of legitimate mail in SPAM. |
| **4. Reject (target)** | 70+ | `p=reject` | Postmaster Tools shows High domain reputation for ≥ 1 consecutive month; FBL (Feedback Loop) loop hooked up. |

**Operator workflow**: register `instaedit.org` on https://postmaster.google.com/ (TMIX requires verifying the apex via a TXT or meta-tag) BEFORE flipping Phase 2 onward — Postmaster gives the per-day IP reputation that's the actual signal. The rua emails go to `security@instaedit.org`; set up an auto-filter + Slack notifier for them.

**Edge case — strict-from-day-one**: if a sibling high-volume SaaS sender already has ≥ 90 days of Gmail reputation on a related apex (rare), `p=reject` from day 1 is acceptable. Document the reasoning in this section.

### 7.3 Gmail inbox test protocol

This is the operator's first concrete verification — runs from the operator's laptop using their own Gmail address. The test MUST pass before inviting any non-operator user. This section is platform-agnostic: the SMTP / Resend sender path is independent of the runtime hosting platform.

**Step 1 — pre-flight**: run `./scripts/email/check-email-deliverability.sh` to confirm all 3 records resolve. Exit code must be 0.

**Step 2 — load the API key**: export `EMAIL_PROVIDER_KEY=<re_...>` from the password manager (`instaedit-login/email/EMAIL_PROVIDER_KEY`). NEVER paste into a shell history — use `read -s` instead.

```bash
read -rs EMAIL_PROVIDER_KEY
export EMAIL_PROVIDER_KEY
```

**Step 3 — trigger the canonical test send** (copy-paste; replace `your-test-address@gmail.com` with the operator's actual Gmail):

```bash
curl -X POST "https://api.resend.com/emails" \
  -H "Authorization: Bearer ${EMAIL_PROVIDER_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "from": "InstaEdit <no-reply@instaedit.org>",
    "to": ["your-test-address@gmail.com"],
    "subject": "Log in to InstaEdit",
    "html": "<p>Click the link below to securely log in:</p><p><a href=\"https://app.instaedit.org/verify?token=TEST_PLACEHOLDER\">Login to InstaEdit</a></p><p>Link expires in 15 minutes.</p><p>If you did not request this, ignore this email.</p>",
    "text": "Click to log in: https://app.instaedit.org/verify?token=TEST_PLACEHOLDER (link expires in 15 minutes).",
    "track_opens": false,
    "track_links": false,
    "headers": {
      "Feedback-ID": "instaedit:magic_link",
      "List-Unsubscribe-Post": "List-Unsubscribe=One-Click"
    },
    "tags": [
      {"name": "category", "value": "magic_link_test"}
    ]
  }'
```

Expected response: HTTP 200 + JSON `{"id":"<resend-message-id>"}`. Copy the message id — you'll check it in the dashboard in step 5.

> `track_opens: false` and `track_links: false` are NON-NEGOTIABLE for transactional magic-link emails. Open-pixel is personal data (IP + UA + timestamps) — GDPR/UK-GDPR/PIPEDA-comparable regimes require explicit consent. Link rewriting can strip magic-link token integrity if a third-party proxy logs / caches the rewrite.

**Step 4 — inspect the email in Gmail**:

1. Open `https://mail.google.com/` (operator's test address), look in INBOX.
2. Confirm the email landed in INBOX (not SPAM, not PROMOTIONS, not TRASH).
3. Open the message → kebab menu → **Show original**.
4. Inspect the `Authentication-Results:` header. MUST contain all three PASSES (any FAIL = see the table below):

```
Authentication-Results: mx.google.com;
        dkim=pass header.i=@instaedit.org header.d=instaedit.org;
        spf=pass smtp.mailfrom=instaedit.org;
        dmarc=pass header.from=instaedit.org action=none;
```

Failure-mode → DNS fix table:

| Header status | Root cause | Fix |
|---------------|------------|-----|
| `dkim=fail (signature body hash not verified)` | DKIM CNAME selector mismatch | Re-paste the DKIM CNAME from Resend dashboard (`<selector>._domainkey.instaedit.org` → `<selector>.dkim.resend.com.`). Verify the selector matches EXACTLY (dashboard prints `resend1` lowercase). |
| `dkim=neutral (no signature)` | DKIM record exists but TTL hasn't propagated to Gmail's resolver yet | Wait 60-300s (depends on TTL), re-send. |
| `spf=softfail` | SPF TXT uses bare `resend.com` instead of `_spf.resend.com`, or uses `-all` during warm-up | Re-paste SPF apex TXT with `include:_spf.resend.com` and `~all`. |
| `spf=neutral (no SPF record)` | TXT at apex missing entirely | Add `v=spf1 include:_spf.resend.com ~all` at apex. |
| `dmarc=fail (SPF or DKIM not aligned with From: domain)` | `instaedit.org` From: differs from `d=` tag in DKIM signature | Confirm Resend is signing with the `instaedit.org` apex (not a subdomain). If your From: is `no-reply@instaedit.org`, the DKIM must sign with `d=instaedit.org` for relaxed alignment — Resend does this by default for sender-domain verification. |
| `dmarc=fail (action=quarantine)` | DMARC is at `p=quarantine` AND SPF or DKIM failed AND < 50% alignment | Move back to `p=none` for 7 days, run more test volume, retry. |

**Step 5 — check Resend dashboard**: open Resend dashboard → Logs → find the message id from step 3 → confirm `email.delivered` event fired within 30s of send. If it's `email.bounced` or sit in `email.sent` without `delivered`, the issue is at the receiver (Gmail); check Gmail's response code in the raw event payload.

**Step 6 — verify tracking is OFF**: back in the email's raw source (`Show original`), confirm:

- The HTML `<a>` tag's `href` is literally `https://app.instaedit.org/verify?token=...`. If you see `href="https://track.resend.com/..."` (or any other Resend tracking host), the `track_links: false` was missing or the API version rejected it — the payload contract has been stable in Resend since 2024 so this would be an operator typo, not a Resend regression.
- The HTML body has no hidden `<img>` tracking pixel at the bottom of the body (an empty `<img src="...">` with no `alt` and `width=0 height=0`). If you see one, `track_opens: false` failed.

### 7.4 Tracking verification

Operational summary of the §7.3 step 6 protocol — what "tracking is off" actually means in 2026 Resend:

- **Open-tracking (pixel)**: a hidden `<img>` at the end of the HTML body that Resend uses to record opens (IP + UA + timestamp). For GDPR / UK-GDPR compliance you must NOT enable this for magic-link emails. Set `track_opens: false`.
- **Click-tracking (rewrite)**: Resend wraps every `<a href>` in a redirect through `track.resend.com` to record clicks. Disabling (`track_links: false`) is REQUIRED for magic-link emails because (a) the magic-link token is a security primitive — you don't want third-party proxy logs of who clicked what when, (b) some corp networks block Resend's tracking domains, which would 5xx an otherwise valid magic-link click.
- **Both options default ON in Resend**: you MUST `false` them on every transactional send. Future backend wiring (see §7.5) MUST set these defaults globally in the Send options for the magic-link + password-reset code paths, NOT per-call, so a refactor mistake doesn't silently flip them back.
- **Webhooks** (out of scope for beta): for production observability of `email.delivered` / `email.bounced` / `email.complained` events, wire a future `pkg/api/email_webhook.go` handler + sign with the HMAC `X-Resend-Signature` header. Defer to a follow-up task — the current beta does not need it because the Resend dashboard already shows all events live.

### 7.5 EMAIL_PROVIDER_KEY capture protocol

The provider key has different capture semantics than the rest of the `/opt/instaedit/secrets/.env.production` secrets:

1. **Capture NOW** from Resend dashboard → API Keys → Create API Key.
2. **Scope = `Sending Access` ONLY** (= just `POST /emails`). Do NOT select `Full Access` (= includes domain + webhook management) — minimise blast radius if the key ever leaks.
3. **Save in password manager** under the entry `instaedit-login/email/EMAIL_PROVIDER_KEY`. Format: starts with `re_` (≈ 40 chars).
4. **Do NOT add to `/opt/instaedit/secrets/.env.production` yet**. As of (post-commit 58742bf Resend unification), `internal/config/config.go` has no `EmailProvider*` fields; `pkg/api/magic_link.go::handleMagicLinkStart` returns the plaintext token in the response body (marked `// dev-only; production drops via Mailgun/SES`); and `pkg/api/auth_email.go::handleForgotPassword` has `// TODO(FASE 2.2): Send reset token via email` markers. The backend does NOT yet wire Resend — pushing the key into `.env.production` would be a secret that has zero readers, which is worse than no secret (rotation burden without value).
5. **When the backend wires Resend** (separate future task): add `EmailProvider`, `EmailFrom`, `EmailFromName`, `EmailProviderKey` fields to `Config`; wire `internal/services/email_sender.go` (a new file) to dispatch the magic-link / password-reset emails with `track_opens: false`, `track_links: false` defaults baked in. THEN push to `/opt/instaedit/secrets/.env.production` + redeploy via `docker compose up -d --force-recreate api worker`.

> Do NOT paste the key into shell history. `read -rs` + `export` is the safe pattern. Do NOT commit to `.env.production` until step 5 fires.

### 7.6 Recovery drills

| Symptom | Fire alarm | Runbook |
|---------|------------|---------|
| Browser console: no magic-link email arrives after `POST /api/v1/auth/magic-link/start` | (Dev-mode artifact) API body returns `magic_link_token: <plain>` — backend not wired yet, expected. To capture a real email: drop Resend `curl` from §7.3 into your shell. | Defer real email sending to backend wiring task (§7.5). The current check script + DMARC ramp are the only deliverability you're responsible for today. |
| Resend dashboard shows `domain not verified` (red badge) | Resend dashboard banner | Confirm `./scripts/email/check-email-deliverability.sh` passes (exit 0) for all 3 records; re-trigger verification from Resend dashboard after a TTL window (5 minutes for Cloudflare, up to 1 hour for Namecheap) |
| Gmail inbox test email lands in SPAM (rare for `p=none` warm-up but possible) | Operator's eye on the test send | Inspect raw source for `dkim=pass` but `dmarc=quarantine` or `dmarc=reject` — indicates DMARC is at a more aggressive policy than sender reputation supports. Drop to next-earlier phase in §7.2 for 7 days before retry. |
| `curl` returns `401 Unauthorized` even with the right key format | Operator typo | Resend keys are `re_` then a random base64 url-safe string; ANY prefix other than `re_` (or any trailing whitespace / newline from copy-paste) is invalid. Print the raw length: `${#EMAIL_PROVIDER_KEY}` ≠ 40 chars usually means a stray newline. |
| `dmarc=fail (domain not aligned)` From: header has a different domain than DKIM signature | Operator regression | Update the From: in the `curl` template to use exactly `instaedit.org` parent (not a subdomain like `mail.instaedit.org`). Verify Resend is signing with the registered sender apex (`instaedit.org`), not a related domain. |
| Tracking pixel appears despite `track_opens: false` | (Operator typo) `false` got typed as `False` or `0` | Resend's API is strict-lowercase JSON. `false` (boolean literal) is the only valid value; `"False"` (string) or `0` (integer) are silently IGNORED, falling back to the default (ON). |
| `security@instaedit.org` mailbox doesn't exist | Daily digest missing in Slack | Create the mailbox FIRST (Google Workspace / Fastmail / whatever you use) before flipping DMARC to `p=quarantine` (otherwise rua RUA reports get rejected). The deposit address for the rua/ruf policy is `security@instaedit.org`, NOT `postmaster@`, NOT `abuse@` (those are GROUP addresses, not personal, which complicates auto-routing). |
