# Google OAuth Console Setup — YouTube and Drive (Steps 0–8)

Part of the [Google OAuth Testing and Production Setup](OAUTH-PRODUCTION.md)
documentation set. This file holds the **console walkthrough**:
prerequisites, consent screen, brand verification, production publishing,
quota increase, rollout verification, and 200-channel distribution.

The canonical scope declarations (Step 3) live in the
[index document](OAUTH-PRODUCTION.md#step-3--declare-the-scopes-minimum-set)
because the `TestOAuthScopes_DocsMatchCanonical` canary lints that file's
scope table against `cmd/oauth-scope-canary/main.go::canonicalScopes`.

Related documents:

- [Limits we have to plan around](oauth-google-limits.md)
- [200-Channel rollout workflow](oauth-google-rollout.md)
- [Monitoring refresh-token TTL](oauth-google-monitoring.md)
- [Troubleshooting](oauth-google-troubleshooting.md)

## Google Cloud Console checks (operator-only)

These values cannot be verified from Git:

1. **Audience:** choose `Internal` only when every user belongs to the same
   Google Workspace organization. Use `External` for SaaS users outside one
   organization.
2. **Publishing status:** keep staging/Test clients in **Testing** and publish
   the verified production client as **Production**. Verify the badge in the
   current Google Auth Platform console; `APP_MODE` is only a local guardrail.
3. **Authorized domains and redirects:** verify the domain first, then add the
   exact environment-specific callback URL to the matching Web application
   client. Never copy a production redirect into a staging client by habit.
4. **Scopes:** compare the consent-screen declaration and a real token's
   granted scopes with the canonical YouTube set in
   [Step 3](OAUTH-PRODUCTION.md#step-3--declare-the-scopes-minimum-set). A
   token may have fewer scopes than requested; treat missing required scopes
   as a reconnect condition.
5. **Offline grant:** perform one fresh consent with the intended client and
   confirm the server stores both encrypted token fields. Do not paste tokens
   into tickets or command history.

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

> The canonical scope table, the `yt-analytics.readonly` anti-scope note,
> and the scopes justification live in the
> [index document](OAUTH-PRODUCTION.md#step-3--declare-the-scopes-minimum-set)
> (this table is linted by `TestOAuthScopes_DocsMatchCanonical`).

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
   * The justification text from
     [Step 3](OAUTH-PRODUCTION.md#step-3--declare-the-scopes-minimum-set)
     (paste verbatim).
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
   `scripts/verify-google-oauth-mode.sh` (YouTube grant) and
   `scripts/verify-drive-oauth-mode.sh` (Drive grant) against
   sample access tokens to confirm the current mode before any
   operator rollout. See the
   [TTL monitoring doc](oauth-google-monitoring.md) for the two
   regimes.

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
4. Run `scripts/verify-google-oauth-mode.sh` (YouTube grant) and
   `scripts/verify-drive-oauth-mode.sh` (Drive grant) against access
   tokens issued to the published client. Each script prints the
   `aud` (= client_id) and `expires_in` (the access-token's
   remaining TTL in seconds, normally ~3,600 for a 1-hour access
   token). The fact that the tokens were issued at all by the
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
a labelled row in the Quotas tab). See
[the 2026 quota model](oauth-google-limits.md#youtube-data-api-v3--video-uploads-bucket-2026-model)
for the bucket-unit math:

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
     [Step 3](OAUTH-PRODUCTION.md#step-3--declare-the-scopes-minimum-set) plus:
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
   * Both consent screens (YouTube and Drive) show the InstaEdit
     app name + logo (not "Unverified app").
   * Scopes list matches
     [Step 3](OAUTH-PRODUCTION.md#step-3--declare-the-scopes-minimum-set)
     exactly (no extras, no missing).
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

6. **Run `scripts/verify-google-oauth-mode.sh`** (YouTube grant)
   and **`scripts/verify-drive-oauth-mode.sh`** (Drive grant)
   against the same access tokens. Each script prints `aud` (= the
   production OAuth client_id) and `expires_in` (the access-token
   TTL). Sanity-check that `aud` matches `YOUTUBE_CLIENT_ID` or
   `GOOGLE_DRIVE_CLIENT_ID` respectively in `.env.production`.

## Step 8 — distribute the 200 channels across manager accounts

Per the **50–100 refresh tokens / `(Google Account, OAuth client)` pair** and
**100 channels / Google Account** limits — detailed in
[the limits doc](oauth-google-limits.md) — the 200 channels must be
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

> **Automating the split.** The offline CLI workflow that produces the
> per-manager CSVs is documented in
> [the rollout doc](oauth-google-rollout.md) — `scripts/distribute_channels_to_managers`.
