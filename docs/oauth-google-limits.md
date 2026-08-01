# Google OAuth Limits — Capacity Planning

Part of the [Google OAuth Testing and Production Setup](OAUTH-PRODUCTION.md)
documentation set. This file holds the **capacity limits** the
200-channel rollout must plan around:

- 50–100 refresh tokens per `(OAuth client, Google Account)` pair
- 100 channels per Google Account
- `channels.list?mine=true` pagination + the 40–50 per-manager cap
- YouTube Data API v3 "Video Uploads" bucket (2026 quota model)

Related documents:

- [Console setup walkthrough](oauth-google-setup.md)
- [200-Channel rollout workflow](oauth-google-rollout.md)
- [Monitoring refresh-token TTL](oauth-google-monitoring.md)

## 50–100 refresh tokens per OAuth client + Google Account pair

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

## 100 channels per Google Account

A single Google Account can manage up to **100 YouTube channels**
(each with its own `UC…` channel id). Beyond that, the extra channels
cannot be transferred into the account. For the 200-channel
deployment, distribute channels across 4–5 manager accounts as
detailed in [Step 8](oauth-google-setup.md#step-8--distribute-the-200-channels-across-manager-accounts).

## `channels.list?mine=true` pagination + 40–50 channels per manager

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

> **Aggiornamento 2026**: il sistema ora gestisce fino a **200 canali per grant** con paginazione attiva (vedi Step 3.3). La regola "max 40-50 canali per manager" indicata in Step 8 resta valida come **budget operativo consigliato** (UI saturation threshold), NON come hard limit tecnico. Operatori con >50 canali per manager devono: (a) verificare che il refresh-token count resti sotto il cap 50-100 per la coppia `(Google Account, OAuth client)`, e (b) confermare che il loro `channels.list?mine=true` con paginazione copra l'intero channel set del manager.

## YouTube Data API v3 — Video Uploads bucket (2026 model)

Since **1° giugno 2026**, YouTube charges `videos.insert` against its
own dedicated "Video Uploads" bucket instead of the older shared
"units" budget that mixed read + write calls under one number. The math
this doc used to print (`10,000 units/day default ÷ 1,600 units per call
= ~6 videos.insert/day (LEGACY pre-2026 / 1600 math; current 2026 model: 1 video = 1 bucket unit, default 100/day)`) is **obsolete as of 2026-06-01**.

* **Cost per call**: `1` bucket unit per `videos.insert`.
* **Default daily cap**: `100` `videos.insert` per Google Cloud project
  per day (≈ 1 upload every 15 minutes — fine for a single-channel dev
  app, way too tight for a 200-channel operator fleet).
* **Multiplier**: bucket units are spent 1-to-1 against `videos.insert`.
  Adding `N` bucket units to the daily cap buys exactly `N` more daily
  `videos.insert` calls. The legacy `units [×*] 1600` / `[/÷] 1600` arithmetic (pre-2026-06-01); current 2026 model: 1 video = 1 bucket unit
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

How to submit the increase request: [Step 6 — quota increase](oauth-google-setup.md#step-6--request-a-youtube-data-api-v3-quota-increase).
