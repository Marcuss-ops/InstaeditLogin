#!/usr/bin/env python3
"""
scripts/_parse_envfile.py — Strict .env parser + validator for the
`instaedit-login` Fly secrets pipeline.

WHY THIS FILE EXISTS (not a heredoc in the bash script):
The previous version of `set-fly-secrets.sh` embedded the parser in a
heredoc, which made it untestable. This file is the security boundary
of the deploy: a bug here could push a malformed value to Fly, panic
the app at boot, or silently mutate a secret via shell expansion. The
companion `scripts/test_parse_envfile.py` locks the contract.

INTERFACE:
    python3 _parse_envfile.py <env_file> <mode> <app_name> <script_dir>

    env_file:    path to the .env file to parse
    mode:        "apply" (emit KEY=VAL on stdout for flyctl pipe) or
                 "dry-run" (no stdout; only stderr preview)
    app_name:    Fly app name (for the preview header)
    script_dir:  directory holding required-fly-secrets.txt and
                 disabled-fly-secrets-prefixes.txt (so this file is
                 self-contained — the bash wrapper passes its own
                 `$(dirname "$0")` so PYTHONPATH / cwd don't matter)

OUTPUT CONTRACT:
    stderr:  redacted preview table (always, in both modes)
    stdout:  KEY=VAL lines, ONE PER LINE, only in --apply mode
             (the bash wrapper pipes this to `flyctl secrets set -`)

EXIT CODES:
    0  OK
    1  pre-flight failure (file not found, required txt files missing)
    3  validation failure (missing key, <redacted>, disabled provider,
                          bad ENCRYPTION_KEYS format, active id not
                          in key map, non-uint32 id, etc.)
"""

import os
import re
import sys


def load_key_list(path: str) -> list[str]:
    """Load a one-per-line key list file. Skips # comments + blank lines.

    Uses utf-8-sig (NOT utf-8) for consistency with parse_envfile — a
    leading BOM would otherwise survive in the first key and the regex
    would skip it silently. The .txt files are repo-controlled so a
    BOM is unlikely, but the defensive fix is one character.
    """
    keys: list[str] = []
    with open(path, "r", encoding="utf-8-sig") as f:
        for raw in f:
            stripped = raw.strip()
            if not stripped or stripped.startswith("#"):
                continue
            keys.append(stripped)
    return keys


def parse_envfile(path: str) -> dict[str, str]:
    """Strict .env parser. NO shell expansion (literal $VAR / backticks).

    Handles:
      - Blank lines and # comments (full-line only; inline # is data)
      - Optional `export ` prefix
      - KEY = uppercase alphanumeric + underscore
      - VAL = everything after the first `=`
      - VAL may be wrapped in single or double quotes (one layer stripped)
      - CRLF line endings (Windows-edited .env files)
      - UTF-8 BOM at file start (Windows editors add 0xEF 0xBB 0xBF);
        encoding="utf-8-sig" strips it transparently, otherwise the
        first KEY would have a \ufeff prefix and the regex would skip
        it silently, causing a "missing required key" error that
        looks like a content bug but is actually an encoding bug.
    """
    parsed: dict[str, str] = {}
    with open(path, "r", encoding="utf-8-sig") as f:
        for raw in f:
            line = raw.rstrip("\r\n")
            stripped = line.strip()
            if not stripped or stripped.startswith("#"):
                continue
            if stripped.startswith("export "):
                stripped = stripped[len("export "):].lstrip()
            m = re.match(r"^([A-Z_][A-Z0-9_]*)=(.*)$", stripped)
            if not m:
                continue
            key, val = m.group(1), m.group(2)
            # Strip ONE layer of surrounding quotes (single or double).
            if len(val) >= 2 and (
                (val[0] == '"' and val[-1] == '"') or
                (val[0] == "'" and val[-1] == "'")
            ):
                val = val[1:-1]
            parsed[key] = val
    return parsed


def validate(
    parsed: dict[str, str],
    required_keys: list[str],
    disabled_prefixes: tuple[str, ...],
) -> list[str]:
    """Return a list of error messages (empty == valid)."""
    errors: list[str] = []

    # (1) All required keys present, non-empty, no <redacted> placeholder.
    for key in required_keys:
        val = parsed.get(key, "")
        if not val:
            errors.append(f"missing or empty: {key}")
        elif "<redacted>" in val:
            errors.append(f"<redacted> placeholder in: {key}")

    # (2) No disabled-provider key prefix in the parsed key NAMES.
    for key in parsed:
        if any(key.startswith(p) for p in disabled_prefixes):
            errors.append(f"disabled-provider key: {key}")

    # (3) ACTIVE_ENCRYPTION_KEY_ID is a uint32 digit string.
    active_id_str = parsed.get("ACTIVE_ENCRYPTION_KEY_ID", "")
    if active_id_str:
        if not active_id_str.isdigit():
            errors.append(
                f"ACTIVE_ENCRYPTION_KEY_ID must be a uint32 digit string; got: {active_id_str!r}"
            )
        elif not (0 <= int(active_id_str) <= 0xFFFFFFFF):
            errors.append(
                f"ACTIVE_ENCRYPTION_KEY_ID out of uint32 range: {active_id_str!r}"
            )

    # (4) ENCRYPTION_KEYS is CSV (id:base64,id:base64,...) with uint32 ids.
    # Per internal/crypto/encrypt.go + internal/config/config.go, the
    # config loader uses strconv.ParseUint(idStr, 10, 32), so ids must
    # be uint32 digit strings. Each entry is `id:base64key` separated
    # by commas. The base64 payload is the AES-256-GCM key (32 bytes
    # decoded) — we don't validate the base64 here because the Go
    # bootstrap will catch it with a clearer error message at boot.
    key_map: dict[int, str] = {}
    keys_csv = parsed.get("ENCRYPTION_KEYS", "")
    if keys_csv:
        for entry in keys_csv.split(","):
            entry = entry.strip()
            if not entry:
                continue
            if ":" not in entry:
                errors.append(f"ENCRYPTION_KEYS entry missing ':' separator: {entry!r}")
                continue
            id_str, key_b64 = entry.split(":", 1)
            if not id_str.isdigit() or not (0 <= int(id_str) <= 0xFFFFFFFF):
                errors.append(f"ENCRYPTION_KEYS entry has non-uint32 id: {id_str!r}")
                continue
            key_map[int(id_str)] = key_b64

    # ACTIVE_ENCRYPTION_KEY_ID must be present in the parsed key map.
    if active_id_str.isdigit() and key_map and int(active_id_str) not in key_map:
        errors.append(
            f"ACTIVE_ENCRYPTION_KEY_ID={active_id_str} not present in ENCRYPTION_KEYS "
            f"(ids found: {sorted(key_map.keys())})"
        )

    return errors


def emit_preview(parsed: dict[str, str], required_keys: list[str], app_name: str) -> None:
    """Print the redacted preview table to stderr. First 3 + last 3 + length."""
    print(
        f"── Secrets to push to {app_name} ───────────────────────────────────",
        file=sys.stderr,
    )
    print(
        "  (preview: first 3 + last 3 chars + length; never the full value)",
        file=sys.stderr,
    )
    print("", file=sys.stderr)
    for key in required_keys:
        val = parsed[key]
        if len(val) <= 8:
            redacted = "***"
        else:
            redacted = f"{val[:3]}***{val[-3:]}"
        print(
            f"  {key:<30} = {redacted:<30} (len={len(val)})",
            file=sys.stderr,
        )
    print("", file=sys.stderr)


def main() -> int:
    if len(sys.argv) != 5:
        print(
            "Usage: _parse_envfile.py <env_file> <mode> <app_name> <script_dir>",
            file=sys.stderr,
        )
        return 1

    env_file = sys.argv[1]
    mode = sys.argv[2]
    app_name = sys.argv[3]
    script_dir = sys.argv[4]

    if mode not in ("apply", "dry-run"):
        print(f"❌ mode must be 'apply' or 'dry-run'; got: {mode!r}", file=sys.stderr)
        return 2
    if not os.path.isfile(env_file):
        print(f"❌ env file not found: {env_file}", file=sys.stderr)
        return 1

    required_file = os.path.join(script_dir, "required-fly-secrets.txt")
    disabled_file = os.path.join(script_dir, "disabled-fly-secrets-prefixes.txt")
    if not os.path.isfile(required_file):
        print(f"❌ required-keys list not found: {required_file}", file=sys.stderr)
        return 1
    if not os.path.isfile(disabled_file):
        print(f"❌ disabled-prefixes list not found: {disabled_file}", file=sys.stderr)
        return 1

    try:
        required_keys = load_key_list(required_file)
        disabled_prefixes = tuple(load_key_list(disabled_file))
    except OSError as e:
        print(f"❌ failed to read key lists: {e}", file=sys.stderr)
        return 1

    # Fail-closed on empty lists. An empty required list would let
    # ANY .env through (no validation runs); an empty disabled list
    # would build a regex in verify-fly-secrets.sh that matches
    # every key (`^()[A-Z0-9_]*([[:space:]]|$)`). The lists are
    # repo-controlled (single source of truth), so an empty result
    # means someone deleted every entry — we reject before doing
    # any push.
    if not required_keys:
        print(f"❌ required-keys list is empty (fail-closed): {required_file}", file=sys.stderr)
        return 3
    if not disabled_prefixes:
        print(f"❌ disabled-prefixes list is empty (fail-closed — would match every key in verify): {disabled_file}", file=sys.stderr)
        return 3

    try:
        parsed = parse_envfile(env_file)
    except OSError as e:
        print(f"❌ failed to read env file: {e}", file=sys.stderr)
        return 1

    errors = validate(parsed, required_keys, disabled_prefixes)
    if errors:
        print("❌ Validation failed:", file=sys.stderr)
        for e in errors:
            print(f"   {e}", file=sys.stderr)
        return 3

    # All required keys must be in `parsed` (validation guarantees this)
    # before we emit anything.
    emit_preview(parsed, required_keys, app_name)

    if mode == "apply":
        # Emit KEY=VAL to stdout (one per line) for the flyctl pipe.
        # No quoting — flyctl expects literal KEY=VAL, and these values
        # are URL/hex/base64 strings (no whitespace, no shell specials).
        for key in required_keys:
            print(f"{key}={parsed[key]}", flush=True)

    return 0


if __name__ == "__main__":
    sys.exit(main())
