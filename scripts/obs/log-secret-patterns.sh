#!/usr/bin/env bash
# Shared privacy-contract patterns. Source this file; never print matched lines.

PATTERNS=(
  "(?i)(jwt[_-]?secret|access[_-]?token|refresh[_-]?token).{0,16}[a-f0-9]{20,}"
  "re_[a-zA-Z0-9]{20,}"
  "AKIA[0-9A-Z]{16,}"
  "://[a-z]+:[^@/]{6,}@"
  "(?i)password[[:space:]]*=[[:space:]]*[^[:space:]]{6,}"
  "[?&]csrf_token=[a-f0-9]{32,}"
  "[?&]token=[A-Za-z0-9_-]{20,}"
  "ya29\.[A-Za-z0-9._-]{8,}"
  "1//[A-Za-z0-9._-]{8,}"
  "(?i)\bBearer[[:space:]]+[A-Za-z0-9._~+/=-]{8,}"
)

PATTERN_NAMES=(
  "JWT / Access / Refresh Tokens"
  "Resend API Keys (re_* prefix)"
  "AWS Access Keys (AKIA prefix)"
  "Postgres / DB URI passwords"
  "Literal password assignments"
  "CSRF token query params"
  "Magic-link token query params"
  "Google OAuth access tokens (ya29.)"
  "Google OAuth refresh tokens (1//)"
  "Bearer credentials"
)
