#!/usr/bin/env bash
# scripts/email/check-email-deliverability.sh
#
# Read-only verification of the email sender domain's deliverability
# posture for `no-reply@instaedit.org` (Resend).
#
# Verifies:
#   1. SPF apex TXT
#   2. DKIM CNAME (selector is per Resend dashboard — pass as $1)
#   3. DMARC _dmarc TXT (initial posture = p=none during warm-up)
#
# Usage:
#   ./scripts/email/check-email-deliverability.sh          # default selector 'resend1'
#   ./scripts/email/check-email-deliverability.sh resend2  # custom DKIM selector
#
# Exit codes:
#   0  all records resolve to the expected Resend canonical values
#   1  one or more records FAIL — see printout for remediation pointers
#   2  dig not installed or DNS resolution error
#
# IMPORTANT: this script is READ-ONLY. It never touches DNS records.
# The reason NO provisioning script exists is DNS registrar APIs are
# heterogeneous (Cloudflare vs Namecheap vs Route 53 have completely
# different APIs and authentication models). A misclick on a
# provisioning script could overwrite the SPF apex with a different
# value, breaking ALL outbound mail until manually fixed. Operators
# should apply DNS changes manually via their registrar dashboard,
# then run this script to verify the propagation.

set -euo pipefail

DOMAIN="${DOMAIN:-instaedit.org}"
SELECTOR="${1:-resend1}"  # default — confirm with Resend dashboard (Domains → Records)

command -v dig >/dev/null 2>&1 || {
  echo "ERR: dig not installed — install with 'brew install bind' or 'apt install dnsutils'."
  exit 2
}

PASS=0
FAIL=0
FAIL_LIST=""

# ─── 1) SPF ───
echo "──── 1. SPF apex TXT ($DOMAIN) ────"
SPF="$(dig +short "$DOMAIN" TXT | tr -d '"' | tr -d '\n')"
EXPECTED_HEADER="v=spf1 include:_spf.resend.com"
if echo "$SPF" | grep -qF "$EXPECTED_HEADER"; then
  echo "✓ PASS: '$SPF'"
  PASS=$((PASS+1))
else
  echo "✗ FAIL: got '$SPF'"
  echo "         expected to contain '$EXPECTED_HEADER'"
  FAIL=$((FAIL+1))
  FAIL_LIST="${FAIL_LIST}\n  - SPF missing '_spf.resend.com' include"
fi

# ─── 2) DKIM ───
DKIM_HOST="${SELECTOR}._domainkey.${DOMAIN}"
echo "──── 2. DKIM CNAME ($DKIM_HOST) ────"
DKIM_TARGET="$(dig +short "$DKIM_HOST" CNAME)"
EXPECTED_DKIM="${SELECTOR}.dkim.resend.com."
# dig may return the value with or without a trailing dot — normalise.
NORMALISED_TARGET="${DKIM_TARGET%.}/${DKIM_TARGET:-empty}"
NORMALISED_EXPECTED="${EXPECTED_DKIM%.}/${EXPECTED_DKIM:-empty}"

if [ "$DKIM_TARGET" = "$EXPECTED_DKIM" ] || [ "$DKIM_TARGET" = "${EXPECTED_DKIM%.}" ]; then
  echo "✓ PASS: '$DKIM_TARGET' → '$EXPECTED_DKIM'"
  PASS=$((PASS+1))
else
  echo "✗ FAIL: got '$DKIM_TARGET'"
  echo "         expected to CNAME-resolve to '$EXPECTED_DKIM'"
  echo "         → confirm the '<selector>._domainkey.<apex>' CNAME in your registrar"
  echo "         → Resend assigns the selector per domain — look at Resend dashboard"
  echo "           → Domains → instaedit.org → Records; pass the actual selector as \$1."
  FAIL=$((FAIL+1))
  FAIL_LIST="${FAIL_LIST}\n  - DKIM CNAME mismatch (check Resend dashboard for selector + target)"
fi

# ─── 3) DMARC ───
echo "──── 3. DMARC TXT (_dmarc.$DOMAIN) ────"
DMARC="$(dig +short "_dmarc.${DOMAIN}" TXT | tr -d '"')"
if echo "$DMARC" | grep -qE "^v=DMARC1;"; then
  echo "✓ PASS: '$DMARC'"
  PASS=$((PASS+1))
  # Phase cue: warn if already at p=reject during warm-up
  if echo "$DMARC" | grep -q 'p=reject'; then
    echo ""
    echo "         ⚠ currently p=reject. The 2026 best-practice ramp for brand-new"
    echo "            domains is p=none (collect reports, no enforcement) → p=quarantine"
    echo "            (after 1-2 weeks clean) → p=reject (target enforcement)."
    echo "            Docs/OPERATIONS.md §7.2 has the full schedule."
    echo ""
    echo "            IF you intentionally enforce p=reject from day one (rare"
    echo "            choice — only acceptable when (a) Google Postmaster Tools"
    echo "            already shows volume / reputation ≥ 90 day average for a"
    echo "            similar sender, or (b) the operator accepts elevated"
    echo "            first-week spam risk), this warning is informational only."
  fi
else
  echo "✗ FAIL: got '$DMARC'"
  echo "         expected to start with 'v=DMARC1;'"
  FAIL=$((FAIL+1))
  FAIL_LIST="${FAIL_LIST}\n  - DMARC TXT missing or malformed"
fi

echo ""
echo "═══════════════════════════════════════════════════"
echo " SUMMARY: $PASS pass / $FAIL fail"
echo "═══════════════════════════════════════════════════"
if [ "$FAIL" -ne 0 ]; then
  echo ""
  echo "FAILURES:"
  printf "%b\n" "$FAIL_LIST"
  echo ""
  echo "Operator remediation playbook → docs/OPERATIONS.md §7"
  echo "  §7.1  DNS records (canonical Resend values to paste)"
  echo "  §7.2  DMARC progression schedule"
  echo "  §7.3  Gmail inbox test protocol + raw-header interpretation"
  echo ""
  exit 1
fi

echo ""
echo "✓ All DNS records consistent with Resend canonical values for $DOMAIN."
echo ""
echo "Operator next steps (NOT scriptable — registrar + Resend dashboard):"
echo ""
echo "  1. Open Resend dashboard → Domains → 'instaedit.org'."
echo "     Confirm the green 'Verified' badge (means SPF + DKIM validated"
echo "     against the records above)."
echo ""
echo "  2. Open Resend dashboard → API Keys → 'Create API Key'."
echo "     Restrict to 'Sending Access' ONLY (NOT 'Full Access' — minimises"
echo "     blast radius if the key ever leaks). Save under the password"
echo "     manager entry 'instaedit-login/email/EMAIL_PROVIDER_KEY'."
echo "     Do NOT yet add to .env.production — the backend does not wire"
echo "     Resend yet (see docs/OPERATIONS.md §7.5 for the wiring plan)."
echo ""
echo "  3. Run the Gmail inbox test → docs/OPERATIONS.md §7.3."
echo "     The test uses your own Gmail address + the canonical curl template"
echo "     in §7.3. Confirm the Authentication-Results header shows:"
echo "       dkim=pass header.d=instaedit.org"
echo "       spf=pass smtp.mailfrom=instaedit.org"
echo "       dmarc=pass header.from=instaedit.org"
echo "     AND the email lands in INBOX (not SPAM / not PROMOTIONS)."
echo ""
echo "  4. Inspect the raw email source in Gmail → 'Show original':"
echo "       - HTML body must contain the literal href https://app.instaedit.org/..."
echo "         (NOT https://track.resend.com/... — that would mean track_links leaked)"
echo "       - No hidden <img> tracking pixel at the end of the HTML body"
echo "         (no pixel = track_opens:false was honoured)"
echo ""
exit 0
