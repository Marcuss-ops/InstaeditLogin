# TikTok for Developers — App Review runbook

Step-by-step procedure for pushing the InstaEdit **TikTok** OAuth
client through TikTok's App Review (the gate that lets the Content
Posting API serve non-sandbox traffic). Companion doc to
`docs/OAUTH-PRODUCTION.md` (Google) — TikTok's review surface is
different (UI form-fill in the developer portal, NOT a verification
form like Google Brand Verification).

## TL;DR — what TikTok wants in the form

Every box must match the canonical production values before the
reviewer accepts the submission. **All values are also wired into
the codebase** — the only operator-side action is to paste them
into the TikTok Developer Portal UI.

| Form field                                         | Canonical value (paste-from)                                                                                   | Source-of-truth                                                                                              |
| ---                                                | ---                                                                                                              | ---                                                                                                          |
| **App name**                                       | `InstaEdit`                                                                                                    | web/public, web/src/pages/platforms/data/tiktok.tsx                                                          |
| **App icon**                                       | 1024×1024 PNG/JPG, ≤ 5 MB, no upscale                                                                          | web/src/components/landing/Nav.tsx + public/ favicon (must match)                                            |
| **Category**                                       | `Social Networking`                                                                                            | (TikTok UI default; matches the SAAS product)                                                               |
| **Description** (≤ 120 chars)                      | `InstaEdit is an AI-powered video creation platform that lets users generate, edit, and publish videos to TikTok` | web/src/pages/platforms/data/tiktok.tsx                                                                      |
| **Terms of Service URL**                           | `https://instaedit.org/terms`                                                                                  | web/public/tos.html                                                                                          |
| **Privacy Policy URL**                             | `https://instaedit.org/privacy`                                                                                | web/public/privacy.html                                                                                      |
| **Platforms**                                      | **Web** (only)                                                                                                  | TikTok UI checkbox                                                                                            |
| **Web/Desktop URL**                                | `https://instaedit.org/tiktok`                                                                                  | web/src/pages/platforms/PlatformPage.tsx → landing page for `/tiktok`                                       |
| **Products**                                       | `Login Kit` + `Content Posting API`                                                                            | web/src/pages/platforms/data/tiktok.tsx + internal/services/tiktok_oauth_oauth.go                            |
| **Scopes** (must list ALL of them)                 | `user.info.basic`, `video.publish`, `video.upload`                                                              | internal/services/tiktok_oauth_oauth.go::GetLoginURLWithOptions (line 31) — must match verbatim              |
| **Redirect URI** (under Login Kit + Content Posting API) | `https://api.instaedit.org/api/v1/auth/tiktok/callback`                                                   | Source-of-truth: configurazione ambiente VPS, TIKTOK_REDIRECT_URI, docs/TIKTOK-APP-REVIEW.md. Lives at `/opt/instaedit/secrets/.env.production` on the VPS as entry `TIKTOK_REDIRECT_URI`; dev fallback `http://localhost:8080/...` in `internal/config/config.go:510`.                          |
| **Web / Desktop URL** (Login Kit config)           | `https://instaedit.org/tiktok`                                                                                  | web/src/pages/platforms/PlatformPage.tsx                                                                      |
| **Required reviewer explanation** (≤ 1000 chars)   | Paste the block from `Reviewer explanation` below                                                              | solidal block; do not paraphrase                                                                              |
| **Demo video** (mp4/mov, ≤ 5 files, ≤ 50 MB each)  | See `Demo video recording recipe` below                                                                        | Filename suggestion: `instaedit-tiktok-app-review-demo-YYYY-MM-DD.mp4`                                      |

> ⚠️  **Why a TikTok tunnel URL (`*.trycloudflare.com`) is NOT
> acceptable here.** TikTok quick tunnels rotate their subdomain on
> every restart; a reviewer who tests the OAuth flow 2 hours after
> submission lands on a stale URL and the flow returns 404. The
> redirect URI is *pinned by the provider console on registration*
> (per docs/DEPLOY.md §3, production secrets and environment) — use the canonical instaedit
> one and it stays stable across reviewer retests.

## Canonical production values — single source of truth

### Redirect URI

```
https://api.instaedit.org/api/v1/auth/tiktok/callback
```

* Lives in `/opt/instaedit/secrets/.env.production` on the VPS as `TIKTOK_REDIRECT_URI`.
  Source-of-truth: configurazione ambiente VPS, TIKTOK_REDIRECT_URI, docs/TIKTOK-APP-REVIEW.md.
* `internal/config/config.go:510` reads `TIKTOK_REDIRECT_URI`
  from env (with a `http://localhost:8080/...` default for dev).
  Two call sites consume it:
  - `internal/services/tiktok_oauth_oauth.go:22` → OAuth start
  - `internal/services/tiktok_oauth_oauth.go:36` → token exchange

### Scopes

```
user.info.basic,video.publish,video.upload
```

* Verbatim — packed into the `scope=` query param by
  `internal/services/tiktok_oauth_oauth.go::GetLoginURLWithOptions`.
* `internal/services/tiktok_oauth_test.go::TestTikTok_GetLoginURL_IncludesVideoUploadScope`
  is the regression guard: drops of `video.upload` from the URL
  scope string fail this test.

### Web/Desktop URL

```
https://instaedit.org/tiktok
```

* Landing page lives at `web/src/pages/platforms/PlatformPage.tsx`
  (data backing it at `web/src/pages/platforms/data/tiktok.tsx`).
* Must be reachable to the reviewer over HTTPS — verify with
  `curl -fsSL https://instaedit.org/tiktok` from any host before
  submission.

### Postgres connection (`DATABASE_URL`)

```
DATABASE_URL=postgresql://instaedit:<password>@db:5432/instaedit_login?sslmode=disable
```

* `<password>` lives only in `/opt/instaedit/secrets/.env.production` on the
  VPS — never committed, never echoed in this doc. The local-dev
  override is the literal `instaedit:dev_password`, published by
  `docker-compose.yml` at lines 44, 61, 104, 142 — the four
  Compose-managed services each inherit this DSN via env injection;
  the matching `postgres` image boot-time seed lives in the compose
  stack (NOT in `internal/database/migrations/001_init.sql`, which
  only models the data tables).
* `db` is the compose-network alias for the `postgres` service inside
  the docker bridge; `?sslmode=disable` is the correct setting there —
  TLS is Caddy's job, not Postgres's. Full canonical 26-secret
  mapping: [`docs/DEPLOY.md` §3](./DEPLOY.md#3-production-secrets-and-environment).

## Reviewer explanation (≤ 1000 chars, paste verbatim)

```
REVISION — Addressed all reviewer feedback:

1. APP ICON — Replaced with icon matching website favicon and browser tab consistently.

2. PRIVACY POLICY — Created at /privacy; mentions "InstaEdit" by name; links to TikTok's Privacy Policy and Developer Terms; discloses Login Kit + Content Posting API usage.

3. TERMS OF SERVICE — Created at /terms; mentions "InstaEdit" by name; links to TikTok's Developer Terms and Privacy Policy; discloses TikTok API integration.

4. VISIBILITY — PP and ToS links are clearly visible in the website footer on every page — no menu or login required.

5. WEBSITE — Fully developed marketing site. Added SEO meta tags, Open Graph, Twitter Card, and JSON-LD structured data. Dedicated TikTok integration page at /tiktok.

6. DOMAIN — All URLs updated to instaedit.org.

PRODUCTS & SCOPES:
- Login Kit: Authenticates users via TikTok OAuth. Scope: user.info.basic (display profile info on dashboard).
- Content Posting API: Users upload and publish video
```

(Adjust the trailing section if the reviewer asks for clarifications on a
specific scene — leave the numbered REVIEW items as-is so the diff is
minimal from the previous submission.)

## Demo video recording recipe

TikTok's spec from the form: *"the demo video should clearly show the
user interface and user interactions"* and *"the demo video should
showcase the website or app where the features will actually be
integrated"*. A reviewer who sees blurry UI or a trycloudflare URL
will reject it on first pass — the most common rejection of 2026
batches was *"demo video images pixelated, kindly use high quality
image"*.

### Pre-flight (15 minutes before you hit record)

1. Open Chrome → go to `https://instaedit.org/tiktok` (the
   reviewer-facing URL; **never** record a `trycloudflare.com` URL
   here — the URL must match what the form lists).
2. Reset browser zoom to 100% (Cmd/Ctrl + 0). Recording at any other
   zoom level produces sub-pixel text that compresses into mush.
3. Open DevTools → toggle device toolbar (Cmd/Ctrl + Shift + M) →
   set width 1920, height 1080, DPR 1 (not Retina sim). The
   record-as-1920 capture gives bit-perfect 1080p when exported.
4. Full-screen the browser (F11 on desktop + no bookmarks bar).
5. Sign into the dashboard with the **sandbox** TikTok account the
   reviewer will use (the dev login that points at TikTok's sandbox
   content-posting environment, NOT your personal business account).

### Recording settings (use the OS-native recorder or OBS)

| Tool                | Target bitrate | Codec                | Notes                                                                                            |
| ---                 | ---            | ---                  | ---                                                                                              |
| macOS QuickTime     | (automatic)    | H.264 High Profile   | Use *File → New Screen Recording*; the default captures ~25 Mbps which is plenty for UI-only video. |
| Windows Game Bar    | "High" preset  | H.264                | `Win + G` → Settings → Capturing → "High quality video recording".                                |
| OBS Studio          | **15–20 Mbps** | H.264 / x264 CRF 18  | Output → Recording → Mode: Standard; bitrate 15 Mbps minimum, 20 Mbps for the dashboard frame.   |
| Linux SimpleScreenRecorder | ≥ 12 Mbps | H.264          | `ss-recorder -c h264 -b 12000000`                                                                |

> **Resist scaling the video down after capture.** Up-scaling 720p →
> 1080p produces exactly the pixelation the reviewer will reject.
> Record at the resolution you intend to ship.

### Scenes to capture (3 scenes, 20–40 s each)

| Scene | What you record                                                                                       | TikTok submission field it proves              |
| ---   | ---                                                                                                    | ---                                            |
| **A** | Dashboard → click "Connect TikTok" → redirect to TikTok OAuth consent screen → consent screen shows `user.info.basic` scope → callback lands on `/tiktok` page with username/avatar visible | **Login Kit + user.info.basic**                |
| **B** | Dashboard → upload a `.mp4` file → status row shows "processing" → row flips to "ready" / "uploaded"                                                              | **Content Posting API — video.upload (Upload-as-Draft)** |
| **C** | Dashboard → publish the previously-uploaded video as a post (Direct Post) → confirmation modal → resolve to `tiktok.com/@user/video/...` URL                            | **Content Posting API — video.publish (Direct Post)**      |

The three scenes together exercise every scope in the form and every
product line. A reviewer who sees all three in one file gets the
end-to-end view in 1–2 minutes.

### Export + ship

1. Export the recording as **MP4 / H.264**, **1920×1080** (or higher),
   **≥ 15 Mbps** bitrate. Container: `.mp4` (TikTok's accepted format
   list — `.mov` also accepted, but `.mp4` is the smaller upload for
   the same quality).
2. File size ≤ 50 MB (TikTok App Review limit per file).
3. Upload in the **App review → "Upload at least one demo video"**
   field of the TikTok Developer Portal form. **Replace** the
   previous attempt; do not add a 2nd video.
4. Click **Submit for review**.

## Pre-submit checklist

Run this list once before every re-submit:

- [ ] **App icon is 1024×1024** PNG/JPG, ≤ 5 MB. Open the file in any
  image viewer and check both dims; an icon shipped as 256×256 and
  upscaled will pixelate on TikTok's preview surface **and** on the
  reviewer dashboard. (Pixelation here sets off the same red flag as
  the demo video.)
- [ ] **App icon matches the in-app favicon** (web/public icon + the
  Nav logo). Mismatched icons ad-hoc trigger "website ≠ app" drift
  rejections.
- [ ] **App name = `InstaEdit`** (1 line, fits in 50 chars), with
  the exact casing in the form. A reviewer search sees case-sensitive
  matches.
- [ ] **Description length ≤ 120 chars**. The current text
  (`InstaEdit is an AI-powered video creation platform …`) is 111
  chars — fine. **AI-powered** is a claim the reviewer may quiz; if
  the publish flow itself doesn't exercise AI generation, soften
  the wording to avoid a "where is the AI?" rejection.
- [ ] **Privacy Policy URL returns 200**:
  `curl -fsS -o /dev/null -w '%{http_code}\n' https://instaedit.org/privacy`
- [ ] **Terms of Service URL returns 200**:
  `curl -fsS -o /dev/null -w '%{http_code}\n' https://instaedit.org/terms`
- [ ] **Web/Desktop URL returns 200 + body**:
  `curl -fsSL https://instaedit.org/tiktok | head -c 200`
- [ ] **Redirect URI on the form = `https://api.instaedit.org/api/v1/auth/tiktok/callback`**.
  **NOT** `*.trycloudflare.com` (tunnels rotate) and **NOT** `localhost`
  (the dev fallback in `internal/config/config.go:510`).
- [ ] **Scripts/verify-tiktok-app-review-config.sh exits 0** (the
  mirror of the secrets-mode check scripts; see below).

## scripts/verify-tiktok-app-review-config.sh

A short shell helper that mirrors `scripts/verify-google-oauth-mode.sh` —
it reads `TIKTOK_REDIRECT_URI` from the env / `.env` file and prints
whether the value matches the canonical production URL.

```bash
# Read from .env.production or any local .env* that exports TIKTOK_REDIRECT_URI
set -a; source .env.production; set +a
./scripts/verify-tiktok-app-review-config.sh
```

The script exits `0` when the redirect URI matches the canonical
value, exits `2` on any non-canonical value (e.g. a `trycloudflare.com`
URL), and exits `1` on pre-flight failure. The script does NOT make any
network calls; it runs entirely on the local filesystem.

## Reject → fix → re-submit (the loop)

TikTok App Review goes through cycles of **rejection → fix → re-submit**.
The most common rejection reasons are (in frequency of 2026 Q1 + Q2):

| Rejection feedback                                            | Fix                                                                                                  |
| ---                                                           | ---                                                                                                  |
| **"Demo video images pixelated, kindly use high quality image"** | Re-record at 1920×1080, bitrate ≥ 15 Mbps, browser zoom 100%, DPR 1 (see Demo video recording recipe). |
| **"Privacy policy URL inaccessible"**                         | `curl -fsSL https://instaedit.org/privacy` — verify 200. The apex/frontend route is Vercel-managed; inspect the Vercel domain, redirect, and deployment status first. Check Caddy only for API callbacks or other `api.instaedit.org` failures (see [docs/DEPLOY.md §4](./DEPLOY.md#4-caddy-on-the-vps) and `docs/OPERATIONS.md §3`). |
| **"App icon does not match the website logo"**                | Ship the SAME icon at 1024×1024 in both surfaces (no upscaling).                                     |
| **"OAuth flow did not complete"**                             | Make sure the redirect URI on the portal matches `api.instaedit.org` exactly — `trycloudflare.com` tunnels fail here.    |
| **"Scopes do not match the requested permissions"**          | Cross-check with `scripts/verify-tiktok-app-review-config.sh` plus the App Review form field.       |
| **"App is not in production"**                               | The InstaEdit app is in beta on TikTok's side today; this rejection may happen if reviewer clicks into the wrong app variant. |

Loop cap: TikTok accepts unlimited re-submits (no SLA — typical
turnaround 3–7 business days), so iterate freely. **Do NOT change the
Client Key / Client Secret** between resubmits — TikTok treats the
client identity as immutable per app, so a key change resets the
review window to scratch.
