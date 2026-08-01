# Google OAuth — Troubleshooting

Part of the [Google OAuth Testing and Production Setup](OAUTH-PRODUCTION.md)
documentation set. This file collects the failure modes and the
"why" behind them:

- The **Testing-mode trap** (7-day refresh-token expiry)
- Where each failure surfaces and how to react

Related documents:

- [Console setup walkthrough](oauth-google-setup.md)
- [Limits we have to plan around](oauth-google-limits.md)
- [Monitoring refresh-token TTL](oauth-google-monitoring.md)
- [200-Channel rollout workflow](oauth-google-rollout.md)

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
  InstaEdit publish pipeline — see
  [Step 3](OAUTH-PRODUCTION.md#step-3--declare-the-scopes-minimum-set).)
* The Drive folder-batch crawler uses the **restricted**
  `drive.readonly` scope — the importer walks arbitrary folders, so
  `drive.file` (per-file access only, opened via the Google Picker
  API) cannot satisfy the flow. `drive.readonly` requires a Google
  security review before the app can publish it externally; that
  review is precisely what the [setup walkthrough](oauth-google-setup.md)
  drives you through ("Step 4 — submit for verification").

Production mode fixes all of the above: refresh tokens normally remain
usable until revoked or otherwise invalidated by Google (for example, by
inactivity or token limits; see the official expiration guidance), the
Testing-user restriction no longer applies, and verified scopes can be
requested by consenting Google accounts.

## Failure-mode quick reference

| Symptom | Root cause | Action |
| --- | --- | --- |
| Channel flips to `reauth_required` within 7 days of consent | App still in **Testing** mode; refresh token expired (7-day trap) | Complete the production publish (setup doc Step 5) and re-connect the channel |
| `videos.insert` returns HTTP 400 `invalid_grant` | Refresh token already invalid (revoked, evicted, or expired) | Reconnect the channel through the dashboard OAuth flow; the new token overwrites the dead one (see [monitoring](oauth-google-monitoring.md#what-to-monitor)) |
| Publish fails with `ErrYouTubeChannelMismatch` sentinel → status `'failed'`, channel BRICKED | `channels.list?mine=true&maxResults=50` truncation: channel is past position 50 in the manager's set | Respect the 40–50 channels/manager cap or ship `nextPageToken` pagination (see [limits](oauth-google-limits.md#channelslistminetrue-pagination--4050-channels-per-manager)) |
| Token silently invalidated with no error | Hit the 50–100 refresh-token cap per `(OAuth client, Google Account)` pair; Google evicts the oldest silently | Keep ≤50 tokens per pair; distribute channels across 4–5 managers |
| `aud` in `tokeninfo` ≠ expected `YOUTUBE_CLIENT_ID` / `GOOGLE_DRIVE_CLIENT_ID` | Token issued by a different client (staging secret leaked into prod, or wrong env) | Rotate the client secret; re-consent every connected user |
| `azp != aud` on a web-server-flow token | Suspicious issuer — possible token confusion | Investigate; rotate the client secret |
| Quota request rejected out of hand | Requested >400/day or interpreted the bucket as per-manager | Re-read the [2026 quota model](oauth-google-limits.md#youtube-data-api-v3--video-uploads-bucket-2026-model): the bucket is per Google Cloud project (fleet-wide), 1 unit = 1 `videos.insert` |
| `invalid_grant` at exactly T+7d under `APP_MODE=testing` | The 7-day boundary is intentional in testing | Flip `APP_MODE=production`; the closure only branches on testing (see [monitoring](oauth-google-monitoring.md#appmode-flag--real-clock-injection-for-ttl-coverage)) |

## Operator checklist summary

The full ordered runbook lives in the
[operational checklist](OAUTH-PRODUCTION.md#operational-checklist) of
the index document. Any single step failing there blocks the
200-channel rollout.
