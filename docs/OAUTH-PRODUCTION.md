# Google OAuth Testing and Production Setup — YouTube and Drive

Step-by-step procedure for configuring InstaEdit's separate Google OAuth
clients in **Testing** and **Production**. The repository can validate the
runtime mode and request parameters, but only the Google Cloud Console can
confirm the live audience, publishing status, verification state, and the
client IDs registered for each environment.

Use a separate Google OAuth client/project for staging and production. Never
reuse a production client secret in staging, and never commit either secret.
The production client must be moved to **Production** only after the required
brand/scope verification is complete; a staging client should remain in
**Testing** while it is used for controlled test accounts.

This document is scoped to the **YouTube Data API v3** and **Google
Drive API v3** clients. InstaEdit uses **two separate OAuth grants**:
one for YouTube and one for Google Drive. The operator's flow is
"import a folder from Drive → publish to YouTube", but each provider
is authorized independently through its own consent screen and its own
set of scopes. The same shape applies to Meta /
LinkedIn / TikTok clients — those flows are covered by `docs/DEPLOY.md`
and the `META_*` / `TIKTOK_*` sections of `.env.production.example`.

## Documentation map

This file is the **index** for the Google OAuth documentation set. The
detailed procedures live in the linked documents below; this page keeps
the TL;DR checklist, the canonical scope table (Step 3 — linted by the
`TestOAuthScopes_DocsMatchCanonical` canary), and the operational
checklist.

| Document | Contents |
| --- | --- |
| [oauth-google-setup.md](oauth-google-setup.md) | Console walkthrough: prerequisites, consent screen, brand verification (Step 4), production publish (Step 5), quota increase (Step 6), rollout verification (Step 7), channel distribution (Step 8) |
| [oauth-google-limits.md](oauth-google-limits.md) | Capacity limits: 50–100 refresh tokens per pair, 100 channels per account, `channels.list` pagination cap, 2026 Video Uploads quota model |
| [oauth-google-monitoring.md](oauth-google-monitoring.md) | Refresh-token TTL monitoring, verify-mode scripts, `APP_MODE` + clock-injection test coverage |
| [oauth-google-rollout.md](oauth-google-rollout.md) | Operator Workflow for 200-Channel Rollout: the `distribute_channels_to_managers` CLI runbook |
| [oauth-google-troubleshooting.md](oauth-google-troubleshooting.md) | Testing-mode trap, failure-mode quick reference |

## TL;DR — the Testing→Production checklist

Every box must be checked before any operator outside the Google test-user
list can use the app for more than 7 days at a time.

1. [ ] **Domain verified** in Google Search Console for
   `instaedit.org` (TXT or CNAME record).
2. [ ] **OAuth consent screen** filled (app name, support email, app
   domain, authorized domain, home page, privacy policy, ToS,
   developer contact).
3. [ ] **YouTube OAuth grant scopes** declared exactly as requested by
   `internal/services/youtube_oauth.go` (see the
   [canonical scope table](#step-3--declare-the-scopes-minimum-set)
   below):
   - `youtube.upload` (videos.insert)
   - `youtube.readonly` (channels.list for P0#3 binding check +
      processing-status poll)
   - `youtube.force-ssl` (thumbnail and metadata updates)
   - `userinfo.email`, `userinfo.profile`, `openid` (operator identity)

   Do **not** add `yt-analytics.readonly` or
   `yt-analytics-monetary.readonly`: the current publish flow does not use
   those scopes.
4. [ ] **Google Drive OAuth grant scopes** declared:
   - `drive.readonly` (Drive folder import; restricted scope —
     folder-level listing required for the production batch-crawler)
   - `userinfo.email`, `userinfo.profile`, `openid` (operator identity)
5. [ ] **Sensitive scope justification** filled in the verification
   form (see "Scopes justification" below).
6. [ ] **Brand verification** approved by Google (typically 4+ weeks
   for sensitive scopes).
7. [ ] **Consent screens published** (one-way switch from Testing to
   Production; see Step 5 in the
   [setup walkthrough](oauth-google-setup.md#step-5--move-from-needs-verification-to-production)).
8. [ ] **Refresh-token TTL monitoring** wired up so the 7-day Testing
   trap and the user-revocation case both produce alerts (see
   [oauth-google-monitoring.md](oauth-google-monitoring.md)).
9. [ ] **7-day reconnect test** passes on a fresh non-tester Google
   Account (refresh token still valid after a week).
10. [ ] **Quota increase** approved by Google (recommended **300–400
    videos.insert/day** in the dedicated "Video Uploads" bucket; see
    [the 2026 quota model](oauth-google-limits.md#youtube-data-api-v3--video-uploads-bucket-2026-model)).
11. [ ] **Manager Google Accounts** created + OAuth dance complete for
    each (4–5 accounts × ≤ 50 channels each, see
    [Step 8](oauth-google-setup.md#step-8--distribute-the-200-channels-across-manager-accounts)
    and the [rollout workflow](oauth-google-rollout.md)).

## Step 3 — declare the scopes (minimum set)

> This is the **canonical scope table**: `cmd/oauth-scope-canary/main.go`
> mirrors it and `TestOAuthScopes_DocsMatchCanonical` lints this file at
> test-time, so a docs-edit that drops one of these scopes fails CI.

Under **Scopes for Google APIs**, add only what the app exercises
in production. The principle of least privilege matters here:
**restricted** scopes (`drive.readonly`) require a deeper, more
expensive Google security audit. InstaEdit uses **two independent
OAuth grants** — one for YouTube and one for Google Drive — so
declare the scopes for each grant separately. The Drive grant is
required because the batch-crawler walks arbitrary folders at
install time and needs `drive.readonly` — `drive.file` would let the
user open individual files via the Google Picker API but cannot
enumerate folder contents, which the production batch-import flow
requires.

| Grant | Scope                                                            | Sensitivity    | Why we need it                                                                                            |
| --- | ---                                                              | ---            | ---                                                                                                       |
| YouTube | `https://www.googleapis.com/auth/youtube.upload`                 | Sensitive      | `videos.insert` (upload a video) — required for the entire publish path                                   |
| YouTube | `https://www.googleapis.com/auth/youtube.readonly`              | Sensitive      | `channels.list?mine=true` (P0#3 channel binding check), `videos.list` (processing-status poll)             |
| YouTube | `https://www.googleapis.com/auth/youtube.force-ssl`             | Sensitive      | Required to set video thumbnails via `thumbnails.set` — ensures the thumbnail upload is sent over HTTPS    |
| Drive | `https://www.googleapis.com/auth/drive.readonly`                | Restricted     | Drive folder import — folder-level listing for the batch crawler (the production batch-import flow walks arbitrary folder contents at install time) |
| Identity | `https://www.googleapis.com/auth/userinfo.email`                 | Non-sensitive  | Identify the operator's Google Account during OAuth                                                       |
| Identity | `https://www.googleapis.com/auth/userinfo.profile`               | Non-sensitive  | Display name + avatar for the dashboard                                                                   |
| Identity | `openid`                                                         | Non-sensitive  | Standard OIDC identifier                                                                                  |

> **Why `drive.readonly` and not `drive` or `drive.file`?** The
> full `drive` scope is **restricted**: it triggers a deeper Google
> security audit (often 3+ months, with mandatory third-party
> penetration testing) and exposes every file in the operator's
> Drive. `drive.file` grants access **only** to files the operator
> explicitly picks through the Google Picker API, which is the right
> tool for "user picks 3 videos" flows but cannot enumerate folder
> contents — so it cannot satisfy the InstaEdit batch-crawler, which
> walks every video in a chosen folder. `drive.readonly` is the
> smallest scope that lets the crawler list folder contents and
> download the files inside; approval is harder than `drive.file` but
> easier than `drive`, and the read-only nature of the access keeps
> the audit scope narrow. See
> [Google Drive API auth scopes](https://developers.google.com/workspace/drive/api/guides/api-specific-auth)
> for the full taxonomy.

> **Why is `yt-analytics.readonly` NOT in the minimum set?** It's
> sensitive AND unused: the production publish pipeline relies on
> `youtube.upload` + `youtube.readonly` only. `videos.insert` accepts
> `youtube.upload` directly — it does NOT need
> `yt-analytics.readonly` to publish analytics-rich content. The
> scope stays out of the consent screen entirely and **must NEVER be
> requested in production**.
>
> **Code-side guard.** `internal/services/youtube_oauth.go` builds
> the authorization URL; the scope string there MUST NOT include
> `yt-analytics.readonly`. Adding it back would (a) trigger a new
> brand-verification round at Google (every added sensitive scope is
> re-reviewed), and (b) deliver zero functional gain because
> `videos.insert` already accepts `youtube.upload` per the
> [YouTube Data API videos.insert reference](https://developers.google.com/youtube/v3/docs/videos/insert).
> Verify with
> `grep -n 'yt-analytics\.readonly' internal/services/youtube_oauth.go`
> before any OAuth-tune commit; any re-introduction is treated by
> this doc as a **blocking change** that must revert.

### Scopes justification (paste into the verification form)

Each OAuth grant is verified independently. Use the relevant
justification block below when submitting the corresponding grant
for verification.

> **Note on identity scopes.** Both grants request `userinfo.email`,
> `userinfo.profile`, and `openid` because each OAuth flow needs to
> identify the Google Account that is consenting. This duplication is
> expected; each grant is a separate token and cannot read identity
> information from the other grant.

#### YouTube grant

* **youtube.upload**: "InstaEdit is a content publishing tool.
  Operators connect their YouTube channels once, then schedule
  video uploads (or trigger them via Drive folder imports). The
  app uploads the video bytes to the operator's channel using the
  resumable upload protocol. No human user of the app watches or
  browses YouTube content through InstaEdit."
* **youtube.readonly**: "Used solely to (a) verify on every upload
  that the OAuth grant is still bound to the operator's chosen
  channel — defending against the wrong-channel silent-upload
  failure mode Google explicitly warns about — and (b) poll
  processing status after upload so the dashboard can show
  'published' once YouTube finishes processing the video."
* **userinfo.email / userinfo.profile / openid**: "Standard
  operator identity — display name + avatar in the dashboard,
  email for security notifications."

#### Drive grant

* **drive.readonly**: "Used solely to list the contents of, and
  download video files from, the operator-chosen Google Drive
  folder that boots the batch-crawler. Read-only — InstaEdit never
  creates, modifies, or deletes files in the operator's Drive. The
  downloaded bytes are then uploaded to the operator's connected
  YouTube channel(s) per the publish schedule they configured. The
  choice of `drive.readonly` over `drive.file` is because the
  crawler needs to enumerate folder contents, which `drive.file`
  does not allow."
* **userinfo.email / userinfo.profile / openid**: "Standard
  operator identity — display name + avatar in the dashboard,
  email for security notifications."

## Repository/runtime configuration contract

The backend reads these environment variables at startup:

| Environment | `APP_ENV` | `APP_MODE` | YouTube callback URL |
| --- | --- | --- | --- |
| Local development | `dev` | `dev` (local client only) | `http://localhost:8080/api/v1/auth/youtube/callback` |
| Staging / Google Testing | `staging` | `testing` | `https://<staging-api-host>/api/v1/auth/youtube/callback` |
| Production | `production` | `production` | `https://api.instaedit.org/api/v1/auth/youtube/callback` |

`APP_MODE` accepts only `dev`, `testing`, or `production`; unknown values
fail startup. `APP_MODE=testing` is a deployment declaration that the
configured Google consent screen is still in Testing, so operators should
expect Google's seven-day refresh-token behavior. It does not change Google
Console state by itself. `APP_MODE=production` must only be used with a
client whose Google publishing status is actually Production.

For every environment, `YOUTUBE_REDIRECT_URI` must match character-for-
character in all three places: the environment, the authorization URL, and
the Google Cloud Console web-client's authorized redirect URI. Do not use a
staging callback with a production client or vice versa; protocol, host,
port, path, and trailing slash must agree.

The authorization URL intentionally always includes `access_type=offline`
and `include_granted_scopes=true`. It adds `prompt=consent` only for an
explicit reconnect/consent flow; normal login does not force consent. This
avoids unnecessary refresh-token issuance/rotation while still allowing a
first connection or recovery flow to obtain a refresh token. OAuth client
IDs and secrets are server-side only; store them in the deployment secret
manager or protected environment file, not in Git, Docker images, the
frontend, URLs, or logs.

## Step anchors (linked from code comments)

The sections below moved to dedicated documents; the anchors remain here so
references in code comments and scripts (e.g. "docs/OAUTH-PRODUCTION.md
Step 7") keep resolving.

### Step 0 — prerequisites / Step 1 / Step 2

Prerequisites (billing, `.env.production` client match), app-domain
verification (Step 0.1), required URLs (Step 0.2), opening the console
(Step 1), and filling the consent screen (Step 2) → see
[oauth-google-setup.md](oauth-google-setup.md#step-0--prerequisites).

### Step 4 — submit for verification (the brand verification step)

Timeline, form artifacts, and the demo video → see
[oauth-google-setup.md](oauth-google-setup.md#step-4--submit-for-verification-the-brand-verification-step).

### Step 5 — move from "Needs verification" to "Production"

The one-way publish switch → see
[oauth-google-setup.md](oauth-google-setup.md#step-5--move-from-needs-verification-to-production).

### Step 6 — request a YouTube Data API v3 quota increase

The Video Uploads bucket request walkthrough → see
[oauth-google-setup.md](oauth-google-setup.md#step-6--request-a-youtube-data-api-v3-quota-increase)
and the [quota model](oauth-google-limits.md#youtube-data-api-v3--video-uploads-bucket-2026-model).

### Step 7 — verify the rollout works end-to-end

Disconnect/reconnect, 7-day wait, upload trigger, `tokeninfo` checks → see
[oauth-google-setup.md](oauth-google-setup.md#step-7--verify-the-rollout-works-end-to-end).

### Step 8 — distribute the 200 channels across manager accounts

Manager layout, refresh-token start counts, 5th-manager rotation reserve → see
[oauth-google-setup.md](oauth-google-setup.md#step-8--distribute-the-200-channels-across-manager-accounts).

### Limits we have to plan around

50–100 refresh tokens per pair, 100 channels per account, `channels.list`
pagination cap, and the 2026 Video Uploads bucket model → see
[oauth-google-limits.md](oauth-google-limits.md).

### Operator Workflow for 200-Channel Rollout

The `scripts/distribute_channels_to_managers` CLI runbook (when to use,
flags, summary format, operator checklist, failure modes) → see
[oauth-google-rollout.md](oauth-google-rollout.md).

### Monitoring refresh-token TTL

The two TTL regimes, alert signals, verify-mode scripts, and the
`APP_MODE` + clock-injection test coverage → see
[oauth-google-monitoring.md](oauth-google-monitoring.md).

### Why this matters (the Testing-mode trap)

The 7-day refresh-token trap and its failure modes → see
[oauth-google-troubleshooting.md](oauth-google-troubleshooting.md).

## Operational checklist

The operator's deployment runbook should include these steps in
order:

1. ✅ Domain verified in Search Console (Step 0.1 of the
   [setup walkthrough](oauth-google-setup.md#step-01--verify-the-app-domain-search-console)).
2. ✅ OAuth app **brand verification** approved (Step 4 of the
   [setup walkthrough](oauth-google-setup.md#step-4--submit-for-verification-the-brand-verification-step)).
3. ✅ OAuth app moved to **Production** (Step 5 of the
   [setup walkthrough](oauth-google-setup.md#step-5--move-from-needs-verification-to-production)).
4. ✅ **Quota increase** approved (Step 6 of the
   [setup walkthrough](oauth-google-setup.md#step-6--request-a-youtube-data-api-v3-quota-increase)).
5. ✅ 7-day reconnect test passes on a fresh Google Account
   (Step 7 of the [setup walkthrough](oauth-google-setup.md#step-7--verify-the-rollout-works-end-to-end)).
6. ✅ Refresh-token TTL monitoring alerts wired up
   ([oauth-google-monitoring.md](oauth-google-monitoring.md)).
7. ✅ Manager Google Accounts created + OAuth dance complete for
   each (Step 8 of the [setup walkthrough](oauth-google-setup.md#step-8--distribute-the-200-channels-across-manager-accounts)).
8. ✅ Drive batch import tested on a non-tester manager account
   (cross-checks P0#1 single-channel binding + P0#3 pre-upload
   check on Production credentials).
9. ✅ Per-channel channel-binding dashboard widget shows
   `reauth_required` flips correctly when an operator revokes the
   InstaEdit grant from Google's
   [third-party apps page](https://myaccount.google.com/permissions).
10. ✅ `scripts/verify-google-oauth-mode.sh` exits 0 against a
    freshly-issued Production YouTube access token, and
    `scripts/verify-drive-oauth-mode.sh` exits 0 against a
    freshly-issued Production Drive access token.

Any single step failing here blocks the 200-channel rollout.
