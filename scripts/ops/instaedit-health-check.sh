#!/usr/bin/env bash
# Health check for the production InstaEdit API (api.instaedit.org).
#
# Root cron entry (every 5 minutes):
#   */5 * * * * /home/pierone/Projects/company/InstaeditLogin/scripts/ops/instaedit-health-check.sh
#
# LOG-ONLY by design (operator preference, 2026-08-18): no external
# notification channel. On failure it appends a diagnostic line to
# /var/log/instaedit-health.log and exits 1 (visible to cron); on
# success it is silent and exits 0. Each failure line carries the HTTP
# code, total probe time and a cause hint, so a 502 (Caddy upstream
# down: nothing on 127.0.0.1:8080) is distinguishable at a glance from
# a 000 (Caddy/DNS/network unreachable — host down).
#
# Override the target for testing (never touches production):
#   INSTAEDIT_HEALTH_URL=https://httpstat.us/502 \
#     /home/pierone/Projects/company/InstaeditLogin/scripts/ops/instaedit-health-check.sh
#   tail -3 /var/log/instaedit-health.log
#
# Exit codes: 0 = healthy, 1 = unhealthy.

set -u

LOG=${INSTAEDIT_HEALTH_LOG:-/var/log/instaedit-health.log}
URL=${INSTAEDIT_HEALTH_URL:-https://api.instaedit.org/api/v1/health}
TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

# Probe once, capturing the HTTP code and the total wall time (a
# slow-but-200 response is a leading indicator worth seeing in the log
# history when an outage follows). curl already prints "000" via -w
# when the connection fails, so no "|| echo 000" fallback here — it
# would double-append and break the case matching below.
start="$(date +%s%N)"
code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "$URL" 2>/dev/null || true)"
end="$(date +%s%N)"
ms="$(( (end - start) / 1000000 ))"
[ -n "$code" ] || code=000

case "$code" in
  2*) exit 0 ;;
esac

# Cause hint per code — keeps the log line actionable without digging
# into Caddy/container state at 3am.
case "$code" in
  000) hint="Caddy/DNS/network unreachable — host down, firewall or DNS broken" ;;
  502) hint="Caddy upstream down — no listener on 127.0.0.1:8080 (docker ps; systemctl status instaedit-compose.service)" ;;
  503) hint="API up but unavailable (rate limit, startup, fail-closed metrics)" ;;
  5*)  hint="API answered with a server-side error" ;;
  4*)  hint="API answered but route/auth problem (wrong path or proxy)" ;;
  *)   hint="unexpected response" ;;
esac

printf '%s DOWN http=%s time=%sms url=%s hint=%s\n' "$TS" "$code" "$ms" "$URL" "$hint" >>"$LOG"
exit 1
