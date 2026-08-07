"""Recon for vertical-slice-e2e honesty report.

Prints the actual on-disk state in InstaeditLogin to drive a
PASS / FAIL / PENDING classification per the 11 PASS criteria
specified in the original spec.
"""
import subprocess
import re
import os
from pathlib import Path

ROOT = "/home/pierone/Projects/company"
IE = ROOT + "/InstaeditLogin"
WEB = IE + "/web/src"
API = IE + "/api/openapi.yaml"
GOSRC = IE + "/pkg/api"


def run(args, cwd=ROOT, timeout=60):
    return subprocess.run(
        args, cwd=cwd, capture_output=True, text=True, timeout=timeout
    )


def section(title):
    print()
    print(f"=== {title} ===")


def head(text, n=60):
    for i, ln in enumerate(text.splitlines()[:n], 1):
        print(f"  L{i}: {ln[:200]}")


# 1. ContentNew.tsx — the wizard origin
section("1. ContentNew.tsx (size + structure)")
p = Path(WEB) / "pages/internal/ContentNew.tsx"
print(f"  exists: {p.is_file()}; size: {p.stat().st_size if p.is_file() else 0} bytes")

# 2. features/publishing/* tree
section("2. features/publishing/ tree")
pub = Path(WEB) / "features/publishing"
if pub.is_dir():
    for x in sorted(pub.rglob("*")):
        if x.is_file():
            print(f"  {x.relative_to(IE)}: {x.stat().st_size} bytes")
else:
    print("  features/publishing/ DOES NOT EXIST")

# 3. publishing/wizard steps
section("3. wizard step components presence")
for step in ["VideoStep.tsx", "ChannelStep.tsx", "ConfirmationStep.tsx", "WizardStep.tsx"]:
    for hit in Path(WEB).rglob(step):
        print(f"  PRESENT: {hit.relative_to(IE)} ({hit.stat().st_size} bytes)")

# 4. /api/v1/posts invocation
section("4. POST /api/v1/posts call sites")
r = run(["grep", "-rnE", "authedFetch.*/api/v1/posts", WEB])
print(r.stdout if r.stdout else "  (none)")

# 5. presign / complete
section("5. presign / presigned / media complete hooks")
r = run(["grep", "-rnE", "presign|presigned|complete.*media|/media/complete", WEB])
print(r.stdout[:3000] if r.stdout else "  (none)")

# 6. /post_targets/ usage
section("6. /post_targets/ polling usage (frontend)")
r = run(["grep", "-rnE", "/api/v1/post_targets/|post_target_id|usePostTargetStatus", WEB])
print(r.stdout[:3000] if r.stdout else "  (none)")

# 7. /accounts/{id}/content
section("7. /accounts/{id}/content (channel content) usage (frontend)")
r = run(["grep", "-rnE", "/api/v1/accounts/.+/content|channelContentApi|channelContent", WEB])
print(r.stdout[:3000] if r.stdout else "  (none)")

# 8. curl probe of the running backend
section("8. curl probe http://localhost:8080")
endpoints = [
    "/api/v1/posts",
    "/api/v1/post_targets/1",
    "/api/v1/accounts/1/content",
    "/api/v1/accounts/1/content?limit=5&privacy=private",
    "/api/v1/posts/1/targets",
    "/api/v1/youtube/editor-sessions",
    "/",
]
for ep in endpoints:
    cmd = (
        'curl -sS -m 5 -o /tmp/curl_body '
        '-w "HTTP %{http_code} size=%{size_download} time=%{time_total}s" '
        f'http://localhost:8080{ep}'
    )
    r = subprocess.run(["bash", "-lc", cmd], timeout=15, capture_output=True, text=True)
    body = ""
    try:
        body = Path("/tmp/curl_body").read_text()[:300]
    except Exception:
        body = "(unreadable)"
    print(f"  --- GET {ep} ---")
    print(f"  {(r.stdout or r.stderr).strip()[:120]}")
    print(f"  body[:300]: {body!r}")

# 9. backend ::8080 process
section("9. processes binding :8080")
r = subprocess.run(
    ["bash", "-lc", "ps -ef | grep -v grep | head -120"],
    capture_output=True, text=True, timeout=10
)
out = r.stdout
keep = [
    ln
    for ln in out.splitlines()
    if (":8080" in ln) or ("instaedit" in ln.lower()) or ("/main" in ln) or ("cmd/main" in ln)
]
for ln in keep[:30]:
    print(f"  {ln[:200]}")

# 10. openapi.yaml: 4 sections + schemas
section("10. openapi.yaml endpoints")
if Path(API).is_file():
    text = Path(API).read_text()
    sections = text.split("\n  /")
    for tag in [
        "/api/v1/posts:",
        "/api/v1/post_targets:",
        "/api/v1/accounts/{accountId}/content:",
        "/api/v1/youtube/editor-sessions:",
        "/api/v1/media/",
        "/api/v1/media:",
    ]:
        if tag in text:
            idx = text.find(tag)
            print(f"  --- {tag} ---")
            head(text[idx : idx + 700], 30)
else:
    print("  openapi.yaml NOT FOUND")

# 11. tests touching the spec
section("11. spec-area tests")
found = []
for root, dirs, files in os.walk(WEB):
    for f in files:
        if f.endswith(("ContentPublish.test.tsx", "ContentPublish.test.ts",
                       "ConfirmationStep.test.tsx", "ContentNew.test.tsx",
                       "useCreatePost.test.ts", "usePostTargetStatus.test.ts")):
            found.append(os.path.join(root, f))
for f in sorted(set(found)):
    print(f"  {os.path.relpath(f, IE)} ({os.path.getsize(f)} bytes)")

# 12. Go server handlers
section("12. Go handlers (pkg/api)")
for pat in [
    "posts_handlers.go",
    "post_targets_handlers.go",
    "accounts_handlers.go",
    "accounts_read_handlers.go",
    "youtube_editor_handlers.go",
    "media_handlers.go",
]:
    for hit in sorted(Path(GOSRC).rglob(pat)):
        if hit.parent.name == GOSRC.split("/")[-1]:
            print(f"  {hit.relative_to(IE)} ({hit.stat().st_size} bytes)")

# 13. OAuth + Velox env tokens
section("13. OAuth/Velox env tokens (presence only, no value disclosure)")
for v in [
    "INSTAGOOGLE_REFRESH_TOKEN",
    "YOUTUBE_API_KEY",
    "VELOX_API_URL",
    "VELOX_API_KEY",
    "INSTAEDITOR_URL",
    "EDITOR_URL",
    "INSTAGOOGLE_CLIENT_ID",
]:
    val = os.environ.get(v, None)
    print(f"  {v}: {'SET' if val else 'unset'}")
