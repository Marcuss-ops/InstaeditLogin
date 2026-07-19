# Google OAuth Production Setup — YouTube

Step-by-step procedure for pushing the InstaEdit YouTube OAuth client
out of **Testing mode** (the default for newly created apps) into
**Production mode** (required for the 200-channel operator rollout).

This document is scoped to the **YouTube Data API v3** client. The same
shape applies to Meta / LinkedIn / TikTok clients — those flows are
covered by `docs/DEPLOY.md` and the `META_*` / `TIKTOK_*` sections of
`.env.production.example`.

## Why this matters (the Testing-mode trap)

In **Testing mode**:

* Refresh tokens **expire after 7 days** for any external (non-Google
  employee) test user. Every operator who connects a channel must
  re-authorize weekly. This silently breaks Drive imports, scheduled
  publishes, and the channel-binding check from P0#3 — all of which
  read the long-lived refresh token.
* The "Add users" tester list caps at **100 test users**. The 200
  channels the operator wants to roll out exceed this cap.
* Several sensitive scopes (`youtube.upload`, `youtube.readonly`,
  `yt-analytics.readonly`) require explicit Google verification
  before they can be requested by any user outside the test list.

Production mode fixes all three: refresh tokens last indefinitely
(until revoked by the user or rotated by us), the 100-user cap is
removed, and verified scopes can be requested by any Google account
that grants consent.

## Limits we have to plan around

### 50 refresh tokens per OAuth client + Google Account pair

Each combination of (OAuth client_id, Google Account) holds at most
**50 active refresh tokens** at any time. When the 51st grant
arrives, **Google silently invalidates the oldest token** without
notifying the app. (Some third-party write-ups cite a higher limit;
the conservative 50 figure comes from Google's official OAuth 2.0
documentation and gives the operator more headroom.)

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

The 50-token limit is documented at Google's official OAuth 2.0
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

### YouTube Data API v3 default quota

`videos.insert` has a **default daily quota of 10,000 units**, and
each call costs **1,600 units**. That works out to **6 uploads/day
per project** — well below the 200-channel requirement. The quota
must be increased before the rollout. Procedure below.

## Step 0 — prerequisites

Before opening the Google Cloud Console:

* Operator has a Google Workspace identity with billing enabled on the
  Cloud project.
* The OAuth client id + secret in `.env.production` matches the one
  used in development (rotation requires re-consent from every
  connected user).
* Domain ownership for the OAuth consent screen **Application home
  page** is verified (`https://app.instaedit.org/` is reachable and
  serves the SPA).
* Privacy policy is hosted at
  `https://app.instaedit.org/privacy.html` (already deployed per
  the `web/public/privacy.html` repo file).

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
| Application terms of service | `https://app.instaedit.org/tos.html`                                  |
| Developer contact email      | `dev@instaedit.org`                                                    |

## Step 3 — declare the sensitive scopes

Under **Scopes for Google APIs**, add:

| Scope                                                            | Why we need it                                                                                            |
| ---                                                              | ---                                                                                                       |
| `https://www.googleapis.com/auth/youtube.upload`                 | `videos.insert` (upload a video) — required for the entire publish path                                   |
| `https://www.googleapis.com/auth/youtube.readonly`              | `channels.list?mine=true` (P0#3 channel binding check), `videos.list` (Reconcile poll), account details    |
| `https://www.googleapis.com/auth/yt-analytics.readonly`         | Analytics import for the dashboard (post-launch P2)                                                       |
| `https://www.googleapis.com/auth/userinfo.email`                 | Identify the operator's Google Account during OAuth                                                        |
| `https://www.googleapis.com/auth/userinfo.profile`               | Display name + avatar for the dashboard                                                                   |
| `openid`                                                         | Standard OIDC identifier                                                                                  |

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
* **yt-analytics.readonly**: "Future-facing; the dashboard will
  surface per-video analytics (views, watch time, retention). Not
  exercised in production today; declared so the scope is already
  verified when we ship the analytics tab in P2."
* **userinfo.email / userinfo.profile / openid**: "Standard
  operator identity — display name + avatar in the dashboard,
  email for security notifications."

## Step 4 — submit for verification

1. Back on the **OAuth consent screen** page, click **Save and
   continue** until you reach the final **Summary** step.
2. Click **Submit for verification**. The form asks for:
   * The justification text from Step 3 (paste verbatim).
   * A demo video showing the operator flow end-to-end (record once,
     store on a private YouTube link or as an unlisted Google Drive
     file; reference the URL in the form).
   * Screenshots of the dashboard, the OAuth consent screen as the
   end-user sees it, and the upload success state.
3. Google does not publish a fixed SLA. The typical turnaround is
   **3–7 business days** for non-sensitive scopes, but sensitive
   scopes (youtube.upload, youtube.readonly, yt-analytics.readonly)
   routinely take several weeks. Plan for **4+ weeks of slack**;
   budget for longer if Google requests additional review artifacts.
4. While verification is pending, the app is **still in Testing
   mode**. You can keep iterating, but refresh tokens still expire
   after 7 days for non-tester users.

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

## Step 6 — request a YouTube Data API v3 quota increase

The default 10,000 units/day (≈ 6 videos.insert/day) is below the
200-channel operator requirement. Even at 1 video/channel/day,
you need 320,000 units/day. Submit a quota increase request:

1. Sidebar → **APIs & Services → Library**.
2. Search **YouTube Data API v3** → click → **Manage**.
3. Tab **Quotas** → click **Edit quota** (top-right).
4. Form asks for:
   * **New quota value**: `10000000` units/day (10M = ~6,250
     `videos.insert` calls; leaves headroom for testing + retries +
     analytics). The hard ceiling Google grants to verified apps
     varies — start at 10M, escalate if Google pushes back.
   * **Justification**: paste the same scopes justification from
     Step 3 plus:
       "InstaEdit is a multi-tenant SaaS used by content operators
       to publish to several YouTube channels from one dashboard.
       One operator manages up to 200 channels, each requiring
       at minimum one videos.insert per upload. 200 channels × 1
       upload/day × 1,600 units = 320,000 units/day. Requesting
       10,000,000 units/day to leave headroom for the full
       schedule + retries + analytics calls."
   * **Link to the verified app** (paste the OAuth consent screen
     URL).
   * **Demo video** (same one as Step 4).
5. SLA: Google officially states that quota requests can take
   **up to 10 business days** (often faster for verified apps).
   Until the increase is approved, the daily 6-video ceiling
   stands.

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
   show as connected (refresh token is still valid).
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

## Step 8 — distribute the 200 channels across manager accounts

Per the **50 refresh tokens / OAuth client + Google Account** and
**100 channels / Google Account** limits, the 200 channels must be
distributed across **4–5 manager Google Accounts**:

| Manager Google Account | Channel id range         | Channel count |
| ---                    | ---                      | ---           |
| `mgr-a@instaedit.org`  | `UCaaaaaa…` – `UCaaaaao` | ~50           |
| `mgr-b@instaedit.org`  | `UCbbbbbb…` – `UCbbbbbo` | ~50           |
| `mgr-c@instaedit.org`  | `UCcccccc…` – `UCccccco` | ~50           |
| `mgr-d@instaedit.org`  | `UCdddddd…` – `UCdddddo` | ~50           |

(Five accounts at 40 channels each is also valid if a fourth manager
slot is unavailable or a single account needs faster rotation.)

Each manager performs the OAuth flow once; the resulting refresh
tokens live on separate `platform_accounts.platform_user_id` rows
(the operator-side channel ID, e.g. `UC…`). The InstaEdit
**workspace_channels** table (P0#4 migration 044) tracks which
workspaces each manager's channels are attached to.

Distribute by **putting the operator's primary account in the pool**
so the operator still has ≤ 50 refresh tokens on their own account
even after a channel migration, and by **rotating secondary channels
across accounts** so that no single account gets all of its channels
revoked at once if an OAuth grant is revoked from
[Google's third-party apps page](https://myaccount.google.com/permissions).

## Operational checklist

The operator's deployment runbook should include these steps in
order:

1. ✅ OAuth app **verification** approved (Step 4).
2. ✅ OAuth app moved to **Production** (Step 5).
3. ✅ **Quota increase** approved (Step 6).
4. ✅ 7-day reconnect test passes on a fresh Google Account
   (Step 7).
5. ✅ Manager Google Accounts created + OAuth dance complete for
   each (Step 8).
6. ✅ Drive batch import tested on a non-tester manager account
   (cross-checks P0#1 single-channel binding + P0#3 pre-upload
   check on Production credentials).
7. ✅ Per-channel channel-binding dashboard widget shows
   `reauth_required` flips correctly when an operator revokes the
   InstaEdit grant from Google's
   [third-party apps page](https://myaccount.google.com/permissions).

Any single step failing here blocks the 200-channel rollout.
