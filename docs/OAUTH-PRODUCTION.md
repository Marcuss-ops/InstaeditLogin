# Google OAuth Production Setup — YouTube + Drive

Step-by-step procedure for pushing the InstaEdit YouTube + Google Drive
OAuth client out of **Testing mode** (the default for newly created apps)
into **Production mode** (required for the 200-channel operator rollout
and for Drive folder-batch imports).

This document is scoped to the **YouTube Data API v3** + **Google
Drive API v3** client (the InstaEdit app combines them under one
consent screen because the operator's flow is "import a folder from
Drive → publish to YouTube"). The same shape applies to Meta /
LinkedIn / TikTok clients — those flows are covered by `docs/DEPLOY.md`
and the `META_*` / `TIKTOK_*` sections of `.env.production.example`.

## TL;DR — the Testing→Production checklist

Every box must be checked before any operator outside the Google test-user
list can use the app for more than 7 days at a time.

1. [ ] **Domain verified** in Google Search Console for
   `instaedit.org` (TXT or CNAME record).
2. [ ] **OAuth consent screen** filled (app name, support email, app
   domain, authorized domain, home page, privacy policy, ToS,
   developer contact).
3. [ ] **Minimum scopes** declared:
   - `youtube.upload` (videos.insert)
   - `youtube.readonly` (channels.list for P0#3 binding check +
     processing-status poll)
   - `drive.readonly` (Drive folder import; restricted scope — folder-level listing required for the production batch-crawler)
   - `userinfo.email` + `userinfo.profile` + `openid` (operator identity)
4. [ ] **Sensitive scope justification** filled in the verification
   form (see "Scopes justification" below).
5. [ ] **Brand verification** approved by Google (typically 4+ weeks
   for sensitive scopes).
6. [ ] **Consent screen published** (one-way switch from Testing to
   Production; see Step 5).
7. [ ] **Refresh-token TTL monitoring** wired up so the 7-day Testing
   trap and the user-revocation case both produce alerts (see
   "Monitoring refresh-token TTL" below).
8. [ ] **7-day reconnect test** passes on a fresh non-tester Google
   Account (refresh token still valid after a week).
9. [ ] **Quota increase** approved by Google (recommended **300–400
   videos.insert/day** in the dedicated "Video Uploads" bucket; default
   today is 100/day at 1 bucket unit per call — bucket units are spent
   1-to-1 with daily upload capacity, so the legacy `units ÷ 1600` math no
   longer applies anywhere in this pipeline).
10. [ ] **Manager Google Accounts** created + OAuth dance complete for
    each (4–5 accounts × ≤ 50 channels each, see "Distribute the 200
    channels").

## Why this matters (the Testing-mode trap)

In **Testing mode**:

* Refresh tokens **expire after 7 days** for any external (non-Google
  employee) test user. Every operator who connects a channel must
  re-authorize weekly. This silently breaks Drive imports, scheduled
  publishes, and the channel-binding check from P0#3 — all of which
  read the long-lived refresh token.
* The "Add users" tester list caps at **100 test users**. The 200
  channels the operator wants to roll out exceed this cap.
* Sensitive scopes actually requested by the app — `youtube.upload`
  and `youtube.readonly` — require explicit Google verification
  before they can be requested by any user outside the test list.
  (`yt-analytics.readonly` is intentionally NEVER requested by the
  InstaEdit publish pipeline — see Step 3 below.)
* The Drive folder-batch crawler uses the **restricted**
  `drive.readonly` scope — the importer walks arbitrary folders, so
  `drive.file` (per-file access only, opened via the Google Picker
  API) cannot satisfy the flow. `drive.readonly` requires a Google
  security review before the app can publish it externally; that
  review is precisely what this document drives you through (see
  "Step 4 — submit for verification").

Production mode fixes all of the above: refresh tokens last indefinitely
(until revoked by the user, by us, or by 6 months of inactivity —
see "Monitoring refresh-token TTL" below), the 100-user cap is
removed, and verified scopes can be requested by any Google account
that grants consent.

## Limits we have to plan around

### 50–100 refresh tokens per OAuth client + Google Account pair

Each combination of (OAuth client_id, Google Account) holds at most
**50–100 active refresh tokens** at any time. When the cap is hit,
**Google silently invalidates the oldest token** without notifying
the app. (Google's official OAuth 2.0 documentation cites 50; some
2024+ third-party write-ups cite 100; the conservative 50 figure
gives the operator more headroom.)

For the 200-channel rollout, this means:

* One Google Account can directly manage ≤ 50 channels through a
  single OAuth client without triggering silent eviction.
* For 200 channels, **distribute the channels across 4–5 manager
  Google Accounts** — for example 40–50 channels per manager — to
  leave headroom for re-auths and rotations. Targeting 50 exactly
  leaves no buffer for connection-state churn.
* Each manager account performs its own OAuth flow; the resulting
  refresh tokens are stored per `platform_accounts.platform_user_id`
  on the corresponding `youtube` row.

The token-count limit is documented at Google's official OAuth 2.0
guide
([developers.google.com/identity/protocols/oauth2 — Expiration](https://developers.google.com/identity/protocols/oauth2#expiration))
and cross-referenced at
[Google Support](https://support.google.com/youtube/answer/3046356);
it is enforced server-side.

### 100 channels per Google Account

A single Google Account can manage up to **100 YouTube channels**
(each with its own `UC…` channel id). Beyond that, the extra channels
cannot be transferred into the account. For the 200-channel
deployment, distribute channels across 4–5 manager accounts as
detailed in Step 8 below.

### `channels.list?mine=true` pagination + 40–50 channels per manager

A single Google Account can be granted access to up to **100 YouTube
channels**, all managed under the same OAuth grant. The pre-2024
InstaEdit code path calls `channels.list?mine=true&maxResults=50`
without following `nextPageToken`, so it can only see the first 50
channels of any manager. As soon as a manager exceeds 50 channels the
remaining ones are invisible to the channel-binding check and the
publisher will silently act on a wrong channel.

**Hard cap per manager: 40–50 channels.** Picking the floor (40 +
margin) keeps `channels.list` responses to a single page (no
`nextPageToken` chasing needed for the pre-upload binding check) and
keeps every manager comfortably under both Google's 100-channels-per-
Account cap and the 50–100 refresh-tokens-cap-per-`(Google Account,OAuth client)` cap. **Operators MUST NOT exceed 50 channels per
manager.** To exceed this hard cap, BOTH preconditions below MUST
be verified live on a test account first:

1. The YouTube service has been upgraded to follow `nextPageToken`,
   loop until the response returns an empty `nextPageToken`, and
   tolerate up to 200 channels in a single grant (the server-side
   `mine=true` maximum the API exposes today).
2. The operator has confirmed the manager's refresh-token count
   stays below the 50-100 silent-invalidation cap (see the limit
   above).

**The failure mode the cap prevents.** Going past 50 channels per
manager HARD-BLOCKS every channel beyond the 50th in that manager's
set. `channels.list?mine=true&maxResults=50` returns only the first
50, so any expected `UC…` past position 50 is INVISIBLE to
`ValidateChannelBinding` in `internal/services/youtube_oauth.go`. The
function returns the typed `ErrYouTubeChannelMismatch` sentinel; the
publish worker (around `internal/worker/publish_worker.go:434`) treats
that sentinel as terminal and calls `MarkReauthRequired` on the
platform_account — flipping `status='reauth_required'` and stamping
`reauth_required_at=NOW()`. The channel is then BRICKED: the
post_target is marked `'failed'`, the publish queue stops retrying,
and the operator must complete a full new OAuth dance against Google
(consent click → new refresh_token grant) to recover the channel.
There is no in-app bypass — no admin "flips the flag back" route,
no auto-retry that escapes the cap, no 5th-manager overflow lane.

Per the actual code path, the failure is therefore STRICTLY WORSE
than a wrong-target upload: a wrong-target upload would still show
up in the Step 7 `snippet.channelId` reconciliation check and the
PostGreSQL row would survive with the correct status. A
maxResults=50 truncation flips the platform_account to a state
where every publish for the affected channel is permanently
rejected until full re-consent. Operators MUST honor the 50-channel
cap exactly because every channel past it becomes unactionable
from the publisher. Until the `nextPageToken` pagination ships
(see Step 8 follow-up), the cap is a single-page response
guarantee — no exceptions, no over-the-cap routing on a 5th manager.

For the 200-channel rollout: **4–5 managers, ≤50 channels each,
single-page `channels.list` today**. Distribute by **rotating secondary
channels across managers** so no single manager gets all of its
channels revoked at once if an OAuth grant is later revoked from
[Google's third-party apps page](https://myaccount.google.com/permissions).

### YouTube Data API v3 — Video Uploads bucket (2026 model)

Since **1° giugno 2026**, YouTube charges `videos.insert` against its
own dedicated "Video Uploads" bucket instead of the older shared
"units" budget that mixed read + write calls under one number. The math
this doc used to print (`10,000 units/day default ÷ 1,600 units per call
= 6 videos.insert/day`) is **obsolete as of 2026-06-01**.

* **Cost per call**: `1` bucket unit per `videos.insert`.
* **Default daily cap**: `100` `videos.insert` per Google Cloud project
  per day (≈ 1 upload every 15 minutes — fine for a single-channel dev
  app, way too tight for a 200-channel operator fleet).
* **Multiplier**: bucket units are spent 1-to-1 against `videos.insert`.
  Adding `N` bucket units to the daily cap buys exactly `N` more daily
  `videos.insert` calls. The legacy `units × 1600` / `÷ 1600` arithmetic
  you may see elsewhere in the Google docs does NOT apply to this
  bucket.
* **Scope (very important)**: this bucket is **per Google Cloud project**,
  NOT per manager and NOT per Google Account. InstaEdit uses ONE Google
  Cloud project, so all 4–5 manager accounts draw from the SAME daily
  budget. The 300–400 /day request below is the **total fleet budget**
  — not per-manager. An operator who interprets it as per-manager will
  plan around 1,200–2,000 /day instead of 300–400 /day and submit a
  quota request that Google will reject out of hand.

For the 200-channel daily target (200 calls/day steady-state + retries +
canary private uploads + test traffic + margin) request **at least 300
bucket units/day**; ideally **400** so the rollout keeps a 50–100% buffer.
The Google-published, cross-verified quota increase URL is
[Quota Calculator](https://developers.google.com/youtube/v3/determine_quota_cost).

## Step 0 — prerequisites

Before opening the Google Cloud Console:

* Operator has a Google Workspace identity with billing enabled on the
  Cloud project.
* The OAuth client id + secret in `.env.production` matches the one
  used in development (rotation requires re-consent from every
  connected user).

### Step 0.1 — verify the app domain (Search Console)

Google requires that the **top private domain** of every URL
referenced in the consent screen be verified. For InstaEdit:

1. Open [search.google.com/search-console](https://search.google.com/search-console).
2. Add property → **URL prefix** → `https://app.instaedit.org/`.
3. Verify via **DNS TXT record** (recommended for non-Google-hosted
   properties) — the Search Console UI shows the exact record name +
   value to add to the `instaedit.org` DNS zone.
4. Repeat for the privacy policy host (`app.instaedit.org`) and the
   ToS host. They share the same top private domain so a single
   verification covers all three.
5. Confirm the **Verified** badge appears next to the property in
   Search Console before continuing. The OAuth consent screen will
   reject unverified domains at publish time.

### Step 0.2 — host the required URLs

* **Privacy policy** at `https://app.instaedit.org/privacy.html`
  (already deployed per the `web/public/privacy.html` repo file).
* **Terms of service** at `https://app.instaedit.org/tos.html`
  (already deployed per `web/public/tos.html`).
* **Application home page** at `https://app.instaedit.org/`
  reachable + serves the SPA.

All three must return HTTP 200 + a non-empty body. Google's crawler
visits them during verification.

## Step 1 — open the Google Cloud Console

1. Go to
   [console.cloud.google.com](https://console.cloud.google.com/)
   and select the **InstaEdit** project.
2. Sidebar → **APIs & Services → OAuth consent screen**.
3. Confirm the **User type** is set to **External** (it cannot be
   Internal unless the project belongs to a Google Workspace org and
   every user is in that org; we want External for SaaS onboarding).

## Step 2 — fill the OAuth consent screen

| Field                         | Value                                                                  |
| ---                           | ---                                                                    |
| App name                      | `InstaEdit`                                                            |
| User support email           | `support@instaedit.org`                                                |
| App logo                      | 256×256 PNG, served at `https://app.instaedit.org/logo.png`             |
| App domain                    | `app.instaedit.org`                                                    |
| Authorized domains           | `instaedit.org`                                                        |
| Application home page        | `https://app.instaedit.org/`                                           |
| Application privacy policy   | `https://app.instaedit.org/privacy.html`                               |
| Application terms of service | `https://app.instaedit.org/tos.html`                                   |
| Developer contact email      | `dev@instaedit.org`                                                    |
| Brand status                 | **Ready to publish** (the required pre-condition for verification)    |

## Step 3 — declare the scopes (minimum set)

Under **Scopes for Google APIs**, add only what the app exercises
in production. The principle of least privilege matters here:
**restricted** scopes (`drive.readonly`) require a deeper, more
expensive Google security audit. The cost is justified because the
Drive batch-crawler walks arbitrary folders at install time and
needs `drive.readonly` — `drive.file` would let the user open
individual files via the Google Picker API but cannot enumerate
folder contents, which the production batch-import flow requires.

| Scope                                                            | Sensitivity    | Why we need it                                                                                            |
| ---                                                              | ---            | ---                                                                                                       |
| `https://www.googleapis.com/auth/youtube.upload`                 | Sensitive      | `videos.insert` (upload a video) — required for the entire publish path                                   |
| `https://www.googleapis.com/auth/youtube.readonly`              | Sensitive      | `channels.list?mine=true` (P0#3 channel binding check), `videos.list` (processing-status poll)             |
| `https://www.googleapis.com/auth/drive.readonly`                | Restricted     | Drive folder import — folder-level listing for the batch crawler (the production batch-import flow walks arbitrary folder contents at install time) |
| `https://www.googleapis.com/auth/userinfo.email`                 | Non-sensitive  | Identify the operator's Google Account during OAuth                                                       |
| `https://www.googleapis.com/auth/userinfo.profile`               | Non-sensitive  | Display name + avatar for the dashboard                                                                   |
| `openid`                                                         | Non-sensitive  | Standard OIDC identifier                                                                                  |

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

The YouTube Data API verification form asks "why does your app need
this scope?". Recommended copy:

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

## Step 4 — submit for verification (the brand verification step)

> The current (2025) Google terminology is **brand verification** —
> it used to be called "OAuth verification" or "scope verification"
> and the forms have moved around, but the procedure is the same.
> Google's guide:
> [App Verification to use Google Authorization APIs (Brand Verification)](https://developers.google.com/identity/protocols/oauth2/production-readiness/brand-verification).

1. Back on the **OAuth consent screen** page, click **Save and
   continue** until you reach the final **Summary** step.
2. Confirm the **brand status** shows "**Ready to publish**" —
   this is the pre-condition for sensitive-scope verification. If
   it does not, fill in any missing app-store links / homepage /
   privacy policy first.
3. Click **Submit for verification**. The form asks for:
   * The justification text from Step 3 (paste verbatim).
   * A demo video showing the operator flow end-to-end (record once,
     store on a private YouTube link or as an unlisted Google Drive
     file; reference the URL in the form).
   * Screenshots of the dashboard, the OAuth consent screen as the
   end-user sees it, and the upload success state.
4. Google does not publish a fixed SLA. The typical turnaround is
   **3–7 business days** for non-sensitive scopes, but sensitive
   scopes (youtube.upload, youtube.readonly) routinely take **4+
   weeks**. Plan for **4+ weeks of slack**; budget for longer if
   Google requests additional review artifacts.
5. While verification is pending, the app is **still in Testing
   mode**. You can keep iterating, but refresh tokens still expire
   after 7 days for non-tester users. Run
   `scripts/verify-google-oauth-mode.sh` (added in this commit)
   against a sample access token to confirm the current mode before
   any operator rollout.

## Step 5 — move from "Needs verification" to "Production"

Once Google approves the verification:

1. Back on **OAuth consent screen** → **Publishing status** → click
   **Publish app**.
2. A modal asks to confirm: "Publishing moves the app to
   Production. Sensitive scopes become available to all users."
   Click **Confirm**.
3. The status badge flips to **In production**. This is a
   **one-way switch** — once published, you cannot move back to
   Testing mode without creating a new OAuth client.
4. Run `scripts/verify-google-oauth-mode.sh` against an access
   token issued to the published client. The script prints the
   `aud` (= client_id) and `expires_in` (the access-token's
   remaining TTL in seconds, normally ~3,600 for a 1-hour access
   token). The fact that the token was issued at all by the
   published client is a strong signal Production mode is live;
   pairing this with the refresh-token TTL monitor below catches
   the rare "verification approved but not yet published" window.

## Step 6 — request a YouTube Data API v3 quota increase

The default **100 videos.insert/day** in the dedicated Video Uploads
bucket is below the 200-channel operator requirement. Even at
1 video/channel/day you need 200 calls; the operational target — 200
channels daily + retries + private canary uploads + test traffic +
margin — calls for **300–400 videos.insert/day** in the bucket.
Submit a quota-increase request on the **Video Uploads bucket row**
(not on a generic "units" field — Google now exposes the bucket as
a labelled row in the Quotas tab):

1. Sidebar → **APIs & Services → Library**.
2. Search **YouTube Data API v3** → click → **Manage**.
3. Tab **Quotas** → on the **Video Uploads bucket** row, click
   **Edit quota** (top-right). The bucket may appear as a labelled
   row named "Video Uploads" or under whatever row Google currently
   uses for the dedicated `videos.insert` quota — pick the row whose
   unit of measure is "videos.insert per day", NEVER the project's
   overall unit-based quota (the old shape still exists in the Quotas
   tab for OTHER read endpoints but does NOT control `videos.insert`
   any more).
4. Form asks for:
   * **New quota value**: `400` bucket units/day (= 400
     `videos.insert` calls per day; 2× buffer over the steady-state
     200-channel, 1-per-day target). If Google pushes back on 400,
     drop to `300` — that still leaves a 50% buffer above the
     200-call steady state.
   * **Justification**: paste the same scopes justification from
     Step 3 plus:
       "InstaEdit is a multi-tenant SaaS used by content operators
       to publish to several YouTube channels from one dashboard.
       One operator manages up to 200 channels, each requiring
       at minimum one videos.insert per upload. 200 channels × 1
       upload/day = 200 bucket-unit calls/day. Requesting 400
       bucket units/day (= 400 videos.insert/day) to leave headroom
       for the full schedule + retries + private canary uploads +
       occasional backfills. Bucket units are 1-to-1 with
       videos.insert under the 2026 quota model."
   * **Link to the verified app** (paste the OAuth consent screen
     URL).
   * **Demo video** (same one as Step 4).
5. SLA: Google officially states that quota requests can take
   **up to 10 business days** (often faster for verified apps).
   Until the increase is approved, the default 100-videos.insert/day
   cap stands.

## Step 7 — verify the rollout works end-to-end

After all three approvals are in (Verification, Production publish,
Quota increase):

1. **Disconnect** an existing channel from the dashboard (so the
   refresh token is invalidated).
2. **Reconnect** through the normal OAuth flow as a fresh
   non-tester Google Account. Confirm:
   * Consent screen shows the InstaEdit app name + logo (not
     "Unverified app").
   * Scopes list matches Step 3 exactly (no extras, no missing).
   * Refresh token is persisted on the platform_accounts row.
3. **Wait 7 days**. Re-check the dashboard — the channel must still
   show as connected (refresh token is still valid). This is the
   smoke test for "Production mode refresh tokens don't expire
   after 7 days". If the channel flipped to `reauth_required`
   within the 7-day window, the app is **still in Testing mode**
   and Step 5 was not actually completed.
4. **Trigger an upload** through the worker. Confirm the upload
   succeeds against the new quota (the existing P0#3 channel
   binding check should pass on the first try).
5. **Hit the API** directly to confirm the new quota is live:

   ```bash
   curl -sS \
     "https://www.googleapis.com/youtube/v3/videos?part=id&mine=true" \
     -H "Authorization: Bearer ${OAUTH_ACCESS_TOKEN}" | jq .
   # → expect HTTP 200 with the operator's videos, no quotaExceeded error
   ```

6. **Run `scripts/verify-google-oauth-mode.sh`** against the same
   access token. It will print `aud` (= the production OAuth
   client_id) and `expires_in` (the access-token TTL). Sanity-check
   that `aud` matches `YOUTUBE_CLIENT_ID` in `.env.production`.

## Step 8 — distribute the 200 channels across manager accounts

Per the **50–100 refresh tokens / `(Google Account, OAuth client)` pair** and
**100 channels / Google Account** limits, the 200 channels must be
distributed across **4–5 manager Google Accounts**, each operating as a
self-contained OAuth dance with the manager's own identity:

| Manager Google Account | Channel id range         | Channel count |
| ---                    | ---                      | ---           |
| `mgr-a@instaedit.org`  | `UCaaaaaa…` – `UCaaaaao` | ~50           |
| `mgr-b@instaedit.org`  | `UCbbbbbb…` – `UCbbbbbo` | ~50           |
| `mgr-c@instaedit.org`  | `UCcccccc…` – `UCccccco` | ~50           |
| `mgr-d@instaedit.org`  | `UCdddddd…` – `UCdddddo` | ~50           |

(See the rotation-reserve footnote below for how the 5th manager slot
is used; the team's productive total stays at ≤ 200 channels regardless
of how the 4–5 lanes are populated.)

Each manager performs the **full, separate OAuth dance with their own
Google identity** (their own consent screen click, their own
`code → refresh_token` exchange, their own token vault entry). Each
manager's paired `(Google Account, OAuth client)` therefore starts at
zero refresh tokens — never inheriting tokens from a different
manager's account — so the **50-token silent-invalidation cap per
pair is enforced from install time**, not retro-fit later. The
resulting refresh tokens live on separate
`platform_accounts.platform_user_id` rows (the operator-side channel
ID, e.g. `UC…`). The InstaEdit **workspace_channels** table
(P0#4 migration 044) tracks which workspaces each manager's channels
are attached to.

Hard counts per manager at install time:

| Manager Google Account | Refresh tokens (start) | Channels bound |
| ---                    | ---                    | ---            |
| `mgr-a@instaedit.org`  | 0                      | ≤ 50           |
| `mgr-b@instaedit.org`  | 0                      | ≤ 50           |
| `mgr-c@instaedit.org`  | 0                      | ≤ 50           |
| `mgr-d@instaedit.org`  | 0                      | ≤ 50           |
| `mgr-e@instaedit.org`  | 0                      | ≤ 50 (rotation reserve — see footnote) |

> **Footnote — 5th manager.** The 5th manager is a **rotation
> reserve**, NOT a 5th productive slot. Total active channel fleet
> stays **≤ 200 channels** at all times (the operator's 200-channel
> scope). The 5th slot exists so that if any single manager's grant
> is revoked from Google's
> [third-party apps page](https://myaccount.google.com/permissions),
> the affected channels can be re-bound under the 5th manager's
> identity without planning around 50+ channels per manager on a
> single account. Operators MUST NOT add a 201st productive channel
> just because the 5th slot is empty.

Adding a new channel under a manager already at 50 active channels
is a **blocking** action — it forces a new manager rotation, which
would silently invalidate the next channel on the existing manager's
refresh-token budget per
[Google's OAuth 2.0 Expiration doc](https://developers.google.com/identity/protocols/oauth2#expiration).

Distribute by **putting the operator's primary account in the pool**
so the operator still has ≤ 50 refresh tokens on their own account
even after a channel migration, and by **rotating secondary channels
across accounts** so that no single account gets all of its channels
revoked at once if an OAuth grant is revoked from
[Google's third-party apps page](https://myaccount.google.com/permissions).

## Monitoring refresh-token TTL

This is the part most operators skip — until Production mode silently
appears to work, then breaks six months later when an unused channel
gets garbage-collected.

### The two TTL regimes

| Mode               | Refresh-token behaviour                                                                                              |
| ---                | ---                                                                                                                  |
| **Testing**        | Expires **7 days** after consent for every non-test user. The dashboard must re-prompt weekly.                       |
| **Production**     | **Indefinite** — until (a) the user revokes the grant via [myaccount.google.com/permissions](https://myaccount.google.com/permissions), (b) the user changes their Google Account password (which may invalidate grants that touch Gmail scopes — InstaEdit does not request Gmail scopes today, so this is not currently a risk), or (c) the refresh token is unused for ~6 months (Google may garbage-collect; conservative number, not formally documented). |

### What to monitor

1. **`oauth_connections.reauth_required_at IS NOT NULL` (HIGH alert)**
   Any row where this column flips from NULL → a timestamp means
   the next `vault.Renew` for that connection failed. This is the
   primary "this channel needs re-authorization" signal. The
   dashboard surfaces it; ops needs a paging alert.
2. **`oauth_connections.last_validated_at` older than 14 days (MEDIUM alert)**
   Even when refresh tokens are indefinite, the **vault's lazy
   re-encrypt path** (Blocco #2.2) and the **channel-binding check
   in `youtube_oauth.go::Publish`** should each touch the connection
   at least once a fortnight. A 14-day-stale `last_validated_at`
   is a strong "this channel is dormant and may have been
   garbage-collected" signal.
3. **`oauth_connections.expires_at IS NULL` for Production connections (INFO)**
   In Testing mode, `expires_at` is set to `now + 7 days` when the
   grant happens. In Production mode, the column stays NULL because
   the token has no fixed expiry. Spot-check this column via the
   `oauth_health` admin dashboard widget: any row with
   `app_mode = 'production'` (set by the publish verifier) but a
   non-NULL `expires_at` is a leftover from a Testing-mode grant and
   should be flagged for rotation.
4. **HTTP 400 `invalid_grant` from `videos.insert` (HIGH alert)**
   This is the **terminal** failure mode — the refresh token is
   already invalid. The vault must (a) flip `reauth_required_at`
   to NOW(), (b) emit the `youtube_publish_channel_mismatch_total`
   counter (P0#2, sibling of the channel-mismatch metric), and
   (c) surface a banner in the operator dashboard with a
   "Reconnect this channel" CTA. The fix is operator-driven: they
   click the CTA, get redirected through the OAuth flow, and the
   new refresh token overwrites the dead one.

### How to verify the current mode quickly

The `scripts/verify-google-oauth-mode.sh` helper calls
`GET https://www.googleapis.com/oauth2/v3/tokeninfo?access_token=...`
and prints:

* `aud` — the OAuth client_id the token was issued to. If this
  matches `YOUTUBE_CLIENT_ID` in `.env.production`, the token is
  signed by the production client.
* `expires_in` — the access-token's remaining TTL in seconds.
  Roughly 3,600 (1 hour) at issuance, decreasing. This does **not**
  reflect the refresh token's TTL (which is held server-side by
  Google), but a working `tokeninfo` response confirms the token
  has not yet expired and the client is in Google's good graces.
* `scope` — the space-delimited list of scopes the token was
  granted. Cross-check against Step 3.
* `azp` — the authorized party (the client that requested the
  token). For web-server-flow InstaEdit tokens, `azp == aud`. A
  mismatch is suspicious and worth investigating.

Use it as a quick "is the published app actually serving tokens?"
check after every consent-screen republish.

```bash
./scripts/verify-google-oauth-mode.sh "$OAUTH_ACCESS_TOKEN"
```

## Operational checklist

The operator's deployment runbook should include these steps in
order:

1. ✅ Domain verified in Search Console (Step 0.1).
2. ✅ OAuth app **brand verification** approved (Step 4).
3. ✅ OAuth app moved to **Production** (Step 5).
4. ✅ **Quota increase** approved (Step 6).
5. ✅ 7-day reconnect test passes on a fresh Google Account
   (Step 7).
6. ✅ Refresh-token TTL monitoring alerts wired up
   ("Monitoring refresh-token TTL").
7. ✅ Manager Google Accounts created + OAuth dance complete for
   each (Step 8).
8. ✅ Drive batch import tested on a non-tester manager account
   (cross-checks P0#1 single-channel binding + P0#3 pre-upload
   check on Production credentials).
9. ✅ Per-channel channel-binding dashboard widget shows
   `reauth_required` flips correctly when an operator revokes the
   InstaEdit grant from Google's
   [third-party apps page](https://myaccount.google.com/permissions).
10. ✅ `scripts/verify-google-oauth-mode.sh` exits 0 against a
    freshly-issued Production access token.

Any single step failing here blocks the 200-channel rollout.