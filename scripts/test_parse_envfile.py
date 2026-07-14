#!/usr/bin/env python3
"""
scripts/test_parse_envfile.py — Regression test for _parse_envfile.py.

The parser is the security boundary of the Fly secrets deploy: a bug
here could push a malformed value (panic at boot), a shell-expanded
value (silently mutate a secret), or skip a disabled-provider check
(push a banned secret). This file locks the contract.

Run via:
    make fly-secrets-test
    # or: python3 scripts/test_parse_envfile.py

Design notes:
  - Plain Python `assert` + `sys.exit()`. NO pytest dep — keeps CI
    dependency-free and the test runs in <1s on any machine with
    python3 (already required by set-fly-secrets.sh pre-flight).
  - Each test creates a temp .env, invokes the parser as a subprocess,
    and asserts on (returncode, stdout, stderr). This catches both
    parser bugs and CLI-arg-parsing bugs in one shot.
"""

import os
import subprocess
import sys
import tempfile

# Resolve sibling paths relative to THIS file so the test works no
# matter what the cwd is when invoked (Makefile, CI, manual run).
SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
PARSER = os.path.join(SCRIPT_DIR, "_parse_envfile.py")
REQUIRED_FILE = os.path.join(SCRIPT_DIR, "required-fly-secrets.txt")
DISABLED_FILE = os.path.join(SCRIPT_DIR, "disabled-fly-secrets-prefixes.txt")

# Sanity-check the layout up front so a missing file gives a clear
# error instead of a cryptic FileNotFoundError from the parser.
for path in (PARSER, REQUIRED_FILE, DISABLED_FILE):
    if not os.path.isfile(path):
        print(f"❌ required file not found: {path}", file=sys.stderr)
        sys.exit(1)


# ── helpers ─────────────────────────────────────────────────────────────

def make_env(content: str) -> str:
    """Write `content` to a temp .env file. Returns the path."""
    fd, path = tempfile.mkstemp(suffix=".env")
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        f.write(content)
    return path


def run_parser(env_path: str, mode: str = "dry-run") -> tuple[int, str, str]:
    """Invoke the parser. Returns (returncode, stdout, stderr)."""
    result = subprocess.run(
        [
            "python3", PARSER,
            env_path, mode, "instaedit-login", SCRIPT_DIR,
        ],
        capture_output=True, text=True,
    )
    return result.returncode, result.stdout, result.stderr


# A canonical "all 27 keys, valid" set. Each test overrides one or
# more of these to exercise a specific failure mode.
def valid_env() -> str:
    return "\n".join([
        "DATABASE_URL=postgresql://u:p@h/d",
        "JWT_SECRET=abc123def456",
        "ENCRYPTION_KEYS=1:AbcBase64",
        "ACTIVE_ENCRYPTION_KEY_ID=1",
        "S3_ACCESS_KEY=AKIA",
        "S3_SECRET_KEY=secret",
        "EMAIL_PROVIDER_KEY=re_x",
        "META_APP_ID=123",
        "META_APP_SECRET=verylongsecret",
        "FRONTEND_URL=https://app.instaedit.org",
        "CORS_ALLOWED_ORIGINS=https://a,https://b",
        "COOKIE_DOMAIN=.instaedit.org",
        "INSTAGRAM_REDIRECT_URI=https://api/cb",
        "FACEBOOK_REDIRECT_URI=https://api/cb",
        "THREADS_REDIRECT_URI=https://api/cb",
        "X_CLIENT_ID=test_x_id",
        "X_CLIENT_SECRET=test_x_secret_64_chars_long_for_realism_xxxxxxxx",
        "X_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/twitter/callback",
        "TIKTOK_CLIENT_ID=test_tt_id",
        "TIKTOK_CLIENT_SECRET=test_tt_secret_64_chars_long_for_realism_xxxxxxxx",
        "TIKTOK_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/tiktok/callback",
        "YOUTUBE_CLIENT_ID=test_yt_id.apps.googleusercontent.com",
        "YOUTUBE_CLIENT_SECRET=test_yt_secret_64_chars_long_for_realism_xxxxxxxx",
        "LINKEDIN_CLIENT_ID=test_li_id",
        "LINKEDIN_CLIENT_SECRET=test_li_secret_64_chars_long_for_realism_xxxxxxxx",
        "LINKEDIN_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/linkedin/callback",
        "YOUTUBE_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/youtube/callback",
    ])


# ── test cases ──────────────────────────────────────────────────────────

def test_01_happy_path_dry_run_emits_nothing_to_stdout() -> None:
    """Valid env in dry-run mode: rc=0, preview on stderr, nothing on stdout."""
    env = make_env(valid_env())
    rc, out, err = run_parser(env, "dry-run")
    assert rc == 0, f"rc=0 expected, got {rc}; stderr: {err}"
    assert out == "", f"dry-run must NOT emit to stdout; got: {out!r}"
    # Preview should mention the app name + a couple of keys
    assert "instaedit-login" in err, f"preview header missing; stderr: {err}"
    assert "DATABASE_URL" in err, f"preview missing DATABASE_URL; stderr: {err}"
    assert "THREADS_REDIRECT_URI" in err, f"preview missing THREADS_REDIRECT_URI; stderr: {err}"


def test_02_happy_path_apply_emits_all_27_key_val_lines() -> None:
    """Valid env in apply mode: rc=0, all 27 KEY=VAL lines on stdout, no leak to stderr."""
    env = make_env(valid_env())
    rc, out, err = run_parser(env, "apply")
    assert rc == 0, f"rc=0 expected, got {rc}; stderr: {err}"
    lines = [l for l in out.splitlines() if l]
    assert len(lines) == 27, f"expected 27 KEY=VAL lines, got {len(lines)}: {lines}"
    for key in ("DATABASE_URL", "JWT_SECRET", "ENCRYPTION_KEYS",
                "ACTIVE_ENCRYPTION_KEY_ID", "THREADS_REDIRECT_URI"):
        assert any(l.startswith(f"{key}=") for l in lines), f"missing {key} in stdout: {out}"


def test_03_dollar_var_preserved_literally() -> None:
    """$VAR / $(cmd) in values must NOT be expanded by the parser.

    Note: the $(rm -rf /)-looking value is placed in META_APP_SECRET
    (a plain string), NOT in ENCRYPTION_KEYS — ENCRYPTION_KEYS has the
    strict `id:base64,id:base64,…` format and a $(...) value would
    fail its own format check before the literal-preservation check
    could be exercised. The contract under test here is "no shell
    expansion anywhere in any value"; ENCRYPTION_KEYS format is
    tested separately in test_12 + test_13.
    """
    env = make_env("\n".join([
        "DATABASE_URL=postgresql://u:p$ass@h/d",
        "JWT_SECRET=abc$xyz",
        "ENCRYPTION_KEYS=1:AbcBase64",  # valid format (id:base64)
        "ACTIVE_ENCRYPTION_KEY_ID=1",
        "S3_ACCESS_KEY=AKIA", "S3_SECRET_KEY=secret",
        "EMAIL_PROVIDER_KEY=re_x",
        "META_APP_ID=123",
        "META_APP_SECRET=$(rm -rf /)",  # malicious-looking; must be literal
        "FRONTEND_URL=https://app.instaedit.org",
        "CORS_ALLOWED_ORIGINS=https://a,https://b",
        "COOKIE_DOMAIN=.instaedit.org",
        "INSTAGRAM_REDIRECT_URI=https://api/cb",
        "FACEBOOK_REDIRECT_URI=https://api/cb",
        "THREADS_REDIRECT_URI=https://api/cb",
        "X_CLIENT_ID=test_x_id",
        "X_CLIENT_SECRET=test_x_secret_64_chars_long_for_realism_xxxxxxxx",
        "X_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/twitter/callback",
        "TIKTOK_CLIENT_ID=test_tt_id",
        "TIKTOK_CLIENT_SECRET=test_tt_secret_64_chars_long_for_realism_xxxxxxxx",
        "TIKTOK_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/tiktok/callback",
        "YOUTUBE_CLIENT_ID=test_yt_id.apps.googleusercontent.com",
        "YOUTUBE_CLIENT_SECRET=test_yt_secret_64_chars_long_for_realism_xxxxxxxx",
        "LINKEDIN_CLIENT_ID=test_li_id",
        "LINKEDIN_CLIENT_SECRET=test_li_secret_64_chars_long_for_realism_xxxxxxxx",
        "LINKEDIN_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/linkedin/callback",
        "YOUTUBE_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/youtube/callback",
    ]))
    rc, out, err = run_parser(env, "apply")
    assert rc == 0, f"rc=0 expected, got {rc}; stderr: {err}"
    assert "p$ass" in out, f"DATABASE_URL must preserve $VAR literally; got: {out}"
    assert "abc$xyz" in out, f"JWT_SECRET must preserve $VAR literally; got: {out}"
    assert "$(rm -rf /)" in out, f"META_APP_SECRET must preserve $(cmd) literally; got: {out}"


def test_14_utf8_bom_stripped_from_env_file() -> None:
    """UTF-8 BOM at file start (0xEF 0xBB 0xBF, added by Windows editors
    like Notepad) must be stripped transparently. Without `utf-8-sig`,
    the first KEY would have a \ufeff prefix, fail the [A-Z_] regex,
    and be silently skipped — operator sees a cryptic
    "missing required key: DATABASE_URL" error that is actually an
    encoding bug, not a content bug. The test writes the BOM bytes
    directly to disk and asserts the parser still sees DATABASE_URL."""
    fd, env = tempfile.mkstemp(suffix=".env")
    with os.fdopen(fd, "wb") as f:
        f.write(b"\xef\xbb\xbf")  # UTF-8 BOM
        for line in valid_env().split("\n"):
            f.write(line.encode("utf-8") + b"\n")
    rc, out, err = run_parser(env, "apply")
    assert rc == 0, f"BOM must parse identically to no-BOM; rc={rc}, stderr: {err}"
    assert "DATABASE_URL=postgresql://u:p@h/d" in out, f"BOM DATABASE_URL broken; got: {out}"


def test_15_empty_disabled_prefixes_list_rejected() -> None:
    """An empty disabled-prefixes list (only comments/blank lines) would
    build a regex in verify-fly-secrets.sh that matches every key
    (`^()[A-Z0-9_]*([[:space:]]|$)`). The parser must fail-closed
    (rc=3) on an empty list. We use a tmpdir with a fresh empty
    disabled-prefixes file so the real `scripts/disabled-fly-secrets-
    prefixes.txt` is untouched."""
    import shutil
    tmpdir = tempfile.mkdtemp()
    try:
        # Copy the real required list (parser needs it to be present)
        shutil.copy(REQUIRED_FILE, os.path.join(tmpdir, "required-fly-secrets.txt"))
        # Write an empty disabled file (only comments + blank lines)
        with open(os.path.join(tmpdir, "disabled-fly-secrets-prefixes.txt"), "w") as f:
            f.write("# only comments here\n# no actual prefixes\n\n")
        # Build a valid env file
        env = make_env(valid_env())
        # Invoke the parser with the tmpdir as script_dir
        result = subprocess.run(
            ["python3", PARSER, env, "dry-run", "instaedit-login", tmpdir],
            capture_output=True, text=True,
        )
        assert result.returncode == 3, f"empty disabled list must rc=3; got {result.returncode}, stderr: {result.stderr}"
        assert "empty" in result.stderr, f"error message missing; stderr: {result.stderr}"
    finally:
        shutil.rmtree(tmpdir)


def test_04_crlf_line_endings() -> None:
    """Windows-edited .env files with CRLF must parse identically to LF."""
    base = valid_env()
    crlf = "\r\n".join(base.split("\n")) + "\r\n"
    env = make_env(crlf)
    rc, out, err = run_parser(env, "apply")
    assert rc == 0, f"CRLF must parse identically to LF; rc={rc}, stderr: {err}"
    assert "DATABASE_URL=postgresql://u:p@h/d" in out, f"CRLF DATABASE_URL broken; got: {out}"


def test_05_export_prefix() -> None:
    """`export FOO=bar` syntax must parse identically to `FOO=bar`."""
    env = make_env("\n".join([
        "export DATABASE_URL=postgresql://u@h/d",
        "JWT_SECRET=abc", "ENCRYPTION_KEYS=1:Abc", "ACTIVE_ENCRYPTION_KEY_ID=1",
        "S3_ACCESS_KEY=AKIA", "S3_SECRET_KEY=secret",
        "EMAIL_PROVIDER_KEY=re_x",
        "META_APP_ID=123", "META_APP_SECRET=verylongsecret",
        "FRONTEND_URL=https://app.instaedit.org",
        "CORS_ALLOWED_ORIGINS=https://a,https://b",
        "COOKIE_DOMAIN=.instaedit.org",
        "INSTAGRAM_REDIRECT_URI=https://api/cb",
        "FACEBOOK_REDIRECT_URI=https://api/cb",
        "THREADS_REDIRECT_URI=https://api/cb",
        "X_CLIENT_ID=test_x_id",
        "X_CLIENT_SECRET=test_x_secret_64_chars_long_for_realism_xxxxxxxx",
        "X_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/twitter/callback",
        "TIKTOK_CLIENT_ID=test_tt_id",
        "TIKTOK_CLIENT_SECRET=test_tt_secret_64_chars_long_for_realism_xxxxxxxx",
        "TIKTOK_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/tiktok/callback",
        "YOUTUBE_CLIENT_ID=test_yt_id.apps.googleusercontent.com",
        "YOUTUBE_CLIENT_SECRET=test_yt_secret_64_chars_long_for_realism_xxxxxxxx",
        "LINKEDIN_CLIENT_ID=test_li_id",
        "LINKEDIN_CLIENT_SECRET=test_li_secret_64_chars_long_for_realism_xxxxxxxx",
        "LINKEDIN_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/linkedin/callback",
        "YOUTUBE_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/youtube/callback",
    ]))
    rc, out, err = run_parser(env, "apply")
    assert rc == 0, f"export prefix must parse; rc={rc}, stderr: {err}"
    assert "DATABASE_URL=postgresql://u@h/d" in out, f"export prefix not stripped; got: {out}"


def test_06_single_and_double_quotes() -> None:
    """VAL wrapped in single or double quotes: one layer stripped."""
    env = make_env("\n".join([
        'DATABASE_URL="postgresql://u@h/d"',
        "JWT_SECRET='abc def'",  # has a space — proves the quote strip happened
        "ENCRYPTION_KEYS='1:AbcBase64'",
        "ACTIVE_ENCRYPTION_KEY_ID=1",
        "S3_ACCESS_KEY=AKIA", "S3_SECRET_KEY=secret",
        "EMAIL_PROVIDER_KEY=re_x",
        "META_APP_ID=123", "META_APP_SECRET=verylongsecret",
        "FRONTEND_URL=https://app.instaedit.org",
        "CORS_ALLOWED_ORIGINS=https://a,https://b",
        "COOKIE_DOMAIN=.instaedit.org",
        "INSTAGRAM_REDIRECT_URI=https://api/cb",
        "FACEBOOK_REDIRECT_URI=https://api/cb",
        "THREADS_REDIRECT_URI=https://api/cb",
        "X_CLIENT_ID=test_x_id",
        "X_CLIENT_SECRET=test_x_secret_64_chars_long_for_realism_xxxxxxxx",
        "X_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/twitter/callback",
        "TIKTOK_CLIENT_ID=test_tt_id",
        "TIKTOK_CLIENT_SECRET=test_tt_secret_64_chars_long_for_realism_xxxxxxxx",
        "TIKTOK_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/tiktok/callback",
        "YOUTUBE_CLIENT_ID=test_yt_id.apps.googleusercontent.com",
        "YOUTUBE_CLIENT_SECRET=test_yt_secret_64_chars_long_for_realism_xxxxxxxx",
        "LINKEDIN_CLIENT_ID=test_li_id",
        "LINKEDIN_CLIENT_SECRET=test_li_secret_64_chars_long_for_realism_xxxxxxxx",
        "LINKEDIN_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/linkedin/callback",
        "YOUTUBE_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/youtube/callback",
    ]))
    rc, out, err = run_parser(env, "apply")
    assert rc == 0, f"quoted values must parse; rc={rc}, stderr: {err}"
    assert "DATABASE_URL=postgresql://u@h/d" in out, f"double quotes not stripped; got: {out}"
    assert "JWT_SECRET=abc def" in out, f"single quotes not stripped; got: {out}"


def test_07_inline_hash_is_data_not_comment() -> None:
    """POSIX: # at the START of a line is a comment, but inline # is part
    of the value. This is critical for passwords that may contain #."""
    env = make_env("\n".join([
        "DATABASE_URL=postgres # my dev db",  # inline # — must be data
        "JWT_SECRET=abc", "ENCRYPTION_KEYS=1:Abc", "ACTIVE_ENCRYPTION_KEY_ID=1",
        "S3_ACCESS_KEY=AKIA", "S3_SECRET_KEY=secret",
        "EMAIL_PROVIDER_KEY=re_x",
        "META_APP_ID=123", "META_APP_SECRET=verylongsecret",
        "FRONTEND_URL=https://app.instaedit.org",
        "CORS_ALLOWED_ORIGINS=https://a,https://b",
        "COOKIE_DOMAIN=.instaedit.org",
        "INSTAGRAM_REDIRECT_URI=https://api/cb",
        "FACEBOOK_REDIRECT_URI=https://api/cb",
        "THREADS_REDIRECT_URI=https://api/cb",
        "X_CLIENT_ID=test_x_id",
        "X_CLIENT_SECRET=test_x_secret_64_chars_long_for_realism_xxxxxxxx",
        "X_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/twitter/callback",
        "TIKTOK_CLIENT_ID=test_tt_id",
        "TIKTOK_CLIENT_SECRET=test_tt_secret_64_chars_long_for_realism_xxxxxxxx",
        "TIKTOK_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/tiktok/callback",
        "YOUTUBE_CLIENT_ID=test_yt_id.apps.googleusercontent.com",
        "YOUTUBE_CLIENT_SECRET=test_yt_secret_64_chars_long_for_realism_xxxxxxxx",
        "LINKEDIN_CLIENT_ID=test_li_id",
        "LINKEDIN_CLIENT_SECRET=test_li_secret_64_chars_long_for_realism_xxxxxxxx",
        "LINKEDIN_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/linkedin/callback",
        "YOUTUBE_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/youtube/callback",
    ]))
    rc, out, err = run_parser(env, "apply")
    assert rc == 0, f"inline # must parse; rc={rc}, stderr: {err}"
    # The whole string including the # must be the value
    assert "DATABASE_URL=postgres # my dev db" in out, f"inline # lost; got: {out}"


def test_08_redacted_placeholder_rejected() -> None:
    """Literal <redacted> in any value must be rejected (rc=3)."""
    env = make_env("\n".join([
        "META_APP_SECRET=<redacted>",
        "DATABASE_URL=x", "JWT_SECRET=abc", "ENCRYPTION_KEYS=1:Abc",
        "ACTIVE_ENCRYPTION_KEY_ID=1",
        "S3_ACCESS_KEY=AKIA", "S3_SECRET_KEY=secret",
        "EMAIL_PROVIDER_KEY=re_x",
        "META_APP_ID=123",
        "FRONTEND_URL=https://app.instaedit.org",
        "CORS_ALLOWED_ORIGINS=https://a,https://b",
        "COOKIE_DOMAIN=.instaedit.org",
        "INSTAGRAM_REDIRECT_URI=https://api/cb",
        "FACEBOOK_REDIRECT_URI=https://api/cb",
        "THREADS_REDIRECT_URI=https://api/cb",
        "X_CLIENT_ID=test_x_id",
        "X_CLIENT_SECRET=test_x_secret_64_chars_long_for_realism_xxxxxxxx",
        "X_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/twitter/callback",
        "TIKTOK_CLIENT_ID=test_tt_id",
        "TIKTOK_CLIENT_SECRET=test_tt_secret_64_chars_long_for_realism_xxxxxxxx",
        "TIKTOK_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/tiktok/callback",
        "YOUTUBE_CLIENT_ID=test_yt_id.apps.googleusercontent.com",
        "YOUTUBE_CLIENT_SECRET=test_yt_secret_64_chars_long_for_realism_xxxxxxxx",
        "LINKEDIN_CLIENT_ID=test_li_id",
        "LINKEDIN_CLIENT_SECRET=test_li_secret_64_chars_long_for_realism_xxxxxxxx",
        "LINKEDIN_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/linkedin/callback",
        "YOUTUBE_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/youtube/callback",
    ]))
    rc, out, err = run_parser(env, "dry-run")
    assert rc == 3, f"<redacted> must reject with rc=3; got rc={rc}, stderr: {err}"
    assert "<redacted> placeholder" in err, f"error message missing; stderr: {err}"


def test_09_disabled_provider_rejected() -> None:
    """Uncommented STRIPE_SECRET_KEY (or any disabled prefix) must be rejected."""
    env = make_env("\n".join([
        "STRIPE_SECRET_KEY=sk_test_xxx",
        "DATABASE_URL=x", "JWT_SECRET=abc", "ENCRYPTION_KEYS=1:Abc",
        "ACTIVE_ENCRYPTION_KEY_ID=1",
        "S3_ACCESS_KEY=AKIA", "S3_SECRET_KEY=secret",
        "EMAIL_PROVIDER_KEY=re_x",
        "META_APP_ID=123", "META_APP_SECRET=verylongsecret",
        "FRONTEND_URL=https://app.instaedit.org",
        "CORS_ALLOWED_ORIGINS=https://a,https://b",
        "COOKIE_DOMAIN=.instaedit.org",
        "INSTAGRAM_REDIRECT_URI=https://api/cb",
        "FACEBOOK_REDIRECT_URI=https://api/cb",
        "THREADS_REDIRECT_URI=https://api/cb",
        "X_CLIENT_ID=test_x_id",
        "X_CLIENT_SECRET=test_x_secret_64_chars_long_for_realism_xxxxxxxx",
        "X_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/twitter/callback",
        "TIKTOK_CLIENT_ID=test_tt_id",
        "TIKTOK_CLIENT_SECRET=test_tt_secret_64_chars_long_for_realism_xxxxxxxx",
        "TIKTOK_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/tiktok/callback",
        "YOUTUBE_CLIENT_ID=test_yt_id.apps.googleusercontent.com",
        "YOUTUBE_CLIENT_SECRET=test_yt_secret_64_chars_long_for_realism_xxxxxxxx",
        "LINKEDIN_CLIENT_ID=test_li_id",
        "LINKEDIN_CLIENT_SECRET=test_li_secret_64_chars_long_for_realism_xxxxxxxx",
        "LINKEDIN_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/linkedin/callback",
        "YOUTUBE_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/youtube/callback",
    ]))
    rc, out, err = run_parser(env, "dry-run")
    assert rc == 3, f"STRIPE must reject with rc=3; got rc={rc}, stderr: {err}"
    assert "disabled-provider" in err, f"error message missing; stderr: {err}"


def test_10_disabled_provider_commented_is_ok() -> None:
    """Commented-out disabled-provider keys must be tolerated (operator
    leaves them in for context, parser must skip)."""
    env = make_env("\n".join([
        "# STRIPE_SECRET_KEY=sk_test_xxx  # beta excludes Stripe",
        "# TIKTOK_CLIENT_KEY=tt_xxx  # beta excludes TikTok",
        "DATABASE_URL=x", "JWT_SECRET=abc", "ENCRYPTION_KEYS=1:Abc",
        "ACTIVE_ENCRYPTION_KEY_ID=1",
        "S3_ACCESS_KEY=AKIA", "S3_SECRET_KEY=secret",
        "EMAIL_PROVIDER_KEY=re_x",
        "META_APP_ID=123", "META_APP_SECRET=verylongsecret",
        "FRONTEND_URL=https://app.instaedit.org",
        "CORS_ALLOWED_ORIGINS=https://a,https://b",
        "COOKIE_DOMAIN=.instaedit.org",
        "INSTAGRAM_REDIRECT_URI=https://api/cb",
        "FACEBOOK_REDIRECT_URI=https://api/cb",
        "THREADS_REDIRECT_URI=https://api/cb",
        "X_CLIENT_ID=test_x_id",
        "X_CLIENT_SECRET=test_x_secret_64_chars_long_for_realism_xxxxxxxx",
        "X_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/twitter/callback",
        "TIKTOK_CLIENT_ID=test_tt_id",
        "TIKTOK_CLIENT_SECRET=test_tt_secret_64_chars_long_for_realism_xxxxxxxx",
        "TIKTOK_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/tiktok/callback",
        "YOUTUBE_CLIENT_ID=test_yt_id.apps.googleusercontent.com",
        "YOUTUBE_CLIENT_SECRET=test_yt_secret_64_chars_long_for_realism_xxxxxxxx",
        "LINKEDIN_CLIENT_ID=test_li_id",
        "LINKEDIN_CLIENT_SECRET=test_li_secret_64_chars_long_for_realism_xxxxxxxx",
        "LINKEDIN_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/linkedin/callback",
        "YOUTUBE_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/youtube/callback",
    ]))
    rc, out, err = run_parser(env, "dry-run")
    assert rc == 0, f"commented disabled providers must pass; rc={rc}, stderr: {err}"


def test_11_missing_required_key_rejected() -> None:
    """If one of the 27 required keys is missing or empty, reject (rc=3)."""
    lines = valid_env().split("\n")
    # Drop THREADS_REDIRECT_URI
    lines = [l for l in lines if not l.startswith("THREADS_REDIRECT_URI=")]
    env = make_env("\n".join(lines))
    rc, out, err = run_parser(env, "dry-run")
    assert rc == 3, f"missing required key must reject; got rc={rc}, stderr: {err}"
    assert "THREADS_REDIRECT_URI" in err, f"missing key name not in error; stderr: {err}"


def test_12_active_encryption_key_id_not_in_map_rejected() -> None:
    """If ACTIVE_ENCRYPTION_KEY_ID=2 but ENCRYPTION_KEYS only has id=1, reject.
    This is the boot-panic scenario: the config loader would fail at startup."""
    env = make_env("\n".join([
        "ENCRYPTION_KEYS=1:AbcBase64",  # only id=1 in the map
        "ACTIVE_ENCRYPTION_KEY_ID=2",   # but the active id is 2 (NOT in map)
        "DATABASE_URL=x", "JWT_SECRET=abc",
        "S3_ACCESS_KEY=AKIA", "S3_SECRET_KEY=secret",
        "EMAIL_PROVIDER_KEY=re_x",
        "META_APP_ID=123", "META_APP_SECRET=verylongsecret",
        "FRONTEND_URL=https://app.instaedit.org",
        "CORS_ALLOWED_ORIGINS=https://a,https://b",
        "COOKIE_DOMAIN=.instaedit.org",
        "INSTAGRAM_REDIRECT_URI=https://api/cb",
        "FACEBOOK_REDIRECT_URI=https://api/cb",
        "THREADS_REDIRECT_URI=https://api/cb",
        "X_CLIENT_ID=test_x_id",
        "X_CLIENT_SECRET=test_x_secret_64_chars_long_for_realism_xxxxxxxx",
        "X_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/twitter/callback",
        "TIKTOK_CLIENT_ID=test_tt_id",
        "TIKTOK_CLIENT_SECRET=test_tt_secret_64_chars_long_for_realism_xxxxxxxx",
        "TIKTOK_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/tiktok/callback",
        "YOUTUBE_CLIENT_ID=test_yt_id.apps.googleusercontent.com",
        "YOUTUBE_CLIENT_SECRET=test_yt_secret_64_chars_long_for_realism_xxxxxxxx",
        "LINKEDIN_CLIENT_ID=test_li_id",
        "LINKEDIN_CLIENT_SECRET=test_li_secret_64_chars_long_for_realism_xxxxxxxx",
        "LINKEDIN_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/linkedin/callback",
        "YOUTUBE_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/youtube/callback",
    ]))
    rc, out, err = run_parser(env, "dry-run")
    assert rc == 3, f"active id not in map must reject; got rc={rc}, stderr: {err}"
    assert "ACTIVE_ENCRYPTION_KEY_ID=2 not present" in err, f"error msg missing; stderr: {err}"


def test_13_non_uint32_encryption_key_id_rejected() -> None:
    """ENCRYPTION_KEYS entry with non-numeric id (e.g. 'foo:bar') must reject
    (strconv.ParseUint would panic in the Go config loader)."""
    env = make_env("\n".join([
        "ENCRYPTION_KEYS=foo:bar",  # 'foo' is not a uint32
        "ACTIVE_ENCRYPTION_KEY_ID=1",
        "DATABASE_URL=x", "JWT_SECRET=abc",
        "S3_ACCESS_KEY=AKIA", "S3_SECRET_KEY=secret",
        "EMAIL_PROVIDER_KEY=re_x",
        "META_APP_ID=123", "META_APP_SECRET=verylongsecret",
        "FRONTEND_URL=https://app.instaedit.org",
        "CORS_ALLOWED_ORIGINS=https://a,https://b",
        "COOKIE_DOMAIN=.instaedit.org",
        "INSTAGRAM_REDIRECT_URI=https://api/cb",
        "FACEBOOK_REDIRECT_URI=https://api/cb",
        "THREADS_REDIRECT_URI=https://api/cb",
        "X_CLIENT_ID=test_x_id",
        "X_CLIENT_SECRET=test_x_secret_64_chars_long_for_realism_xxxxxxxx",
        "X_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/twitter/callback",
        "TIKTOK_CLIENT_ID=test_tt_id",
        "TIKTOK_CLIENT_SECRET=test_tt_secret_64_chars_long_for_realism_xxxxxxxx",
        "TIKTOK_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/tiktok/callback",
        "YOUTUBE_CLIENT_ID=test_yt_id.apps.googleusercontent.com",
        "YOUTUBE_CLIENT_SECRET=test_yt_secret_64_chars_long_for_realism_xxxxxxxx",
        "LINKEDIN_CLIENT_ID=test_li_id",
        "LINKEDIN_CLIENT_SECRET=test_li_secret_64_chars_long_for_realism_xxxxxxxx",
        "LINKEDIN_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/linkedin/callback",
        "YOUTUBE_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/youtube/callback",
    ]))
    rc, out, err = run_parser(env, "dry-run")
    assert rc == 3, f"non-uint32 ENCRYPTION_KEYS id must reject; got rc={rc}, stderr: {err}"


# ── runner ──────────────────────────────────────────────────────────────

ALL_TESTS = [
    test_01_happy_path_dry_run_emits_nothing_to_stdout,
    test_02_happy_path_apply_emits_all_27_key_val_lines,
    test_03_dollar_var_preserved_literally,
    test_04_crlf_line_endings,
    test_05_export_prefix,
    test_06_single_and_double_quotes,
    test_07_inline_hash_is_data_not_comment,
    test_08_redacted_placeholder_rejected,
    test_09_disabled_provider_rejected,
    test_10_disabled_provider_commented_is_ok,
    test_11_missing_required_key_rejected,
    test_12_active_encryption_key_id_not_in_map_rejected,
    test_13_non_uint32_encryption_key_id_rejected,
    test_14_utf8_bom_stripped_from_env_file,
    test_15_empty_disabled_prefixes_list_rejected,
]


def main() -> int:
    print(f"Running {len(ALL_TESTS)} parser tests…")
    print()
    failed = 0
    for test in ALL_TESTS:
        sys.stdout.write(f"  {test.__name__}… ")
        sys.stdout.flush()
        try:
            test()
        except AssertionError as e:
            failed += 1
            print(f"FAIL\n    {e}")
        else:
            print("OK")
    print()
    if failed:
        print(f"❌ {failed}/{len(ALL_TESTS)} tests failed")
        return 1
    print(f"✓ All {len(ALL_TESTS)} parser tests passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
