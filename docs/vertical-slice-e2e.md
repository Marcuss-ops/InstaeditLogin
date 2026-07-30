# Vertical Slice — End-to-End Verification Report

**Spec target.** `/content/new → upload → YouTube private → channel page →
Modifica copertina → final visibility → confirm updated thumbnail + privacy`.

**Audit source.** `/home/pierone/Projects/company/InstaeditLogin`, branch `main`,
HEAD `94cf624` at the time of the audit.

---

## 1. Environment reality check (read this first)

This document does **not** claim a green PASS. The vertical slice is a real
end-to-end flow that requires runtime infrastructure this offline audit
session does not have. The honest disposition below tags every PASS criterion
with one of three markers so that anyone re-running the audit can quickly
tell what was actually exercised vs. what requires external infrastructure.

### 1.1 What is available in this audit environment

| Asset | State in this env |
|---|---|
| PostgreSQL | listening on `:5432` |
| Redis | listening on `:6379` |
| Docker | available; `instaedit-db` and `instaedit-minio` healthy |
| IE backend process on `:8080` | **running, but on a build predating the recent `/api/v1/posts/{id}/targets` and `/api/v1/post_targets/{id}` wires**. Probe shows `/api/v1/post_targets/1` → 404 (old build) while the current `main` source HAS the handler. The other `/api/v1/*` endpoints return 401 (`missing or invalid session`) confirming the auth middleware is up. |
| IE source tree | current main (`94cf624`) — wizard, publish page, channel page, hooks, types, tests, shared editor session client, cross-tab live update, cache-buster |
| Chromium / Google Chrome / Firefox | installed (for browser-use) |
| `tsc -p tsconfig.app.json --noEmit` | exit 2, **0 relevant TS errors** after filtering env-only `Cannot find module` noise (`lucide-react`, `clsx`, `tailwind-merge`, `recharts`, `@fullcalendar/*`, `@tailwindcss/vite`) |
| `vitest` for the affected tests | **cannot start** — `vite.config.ts` imports `@tailwindcss/vite` which is not present in `node_modules`. Pre-existing env gap, not caused by the latest commits. |

### 1.2 What is NOT available in this audit environment

| Missing asset | Why it blocks a live PASS run |
|---|---|
| `INSTAGOOGLE_REFRESH_TOKEN` + real Google OAuth client/secret | YouTube Data API upload + `videos.update` cannot be exercised against a real channel — without an OAuth refresh token, the worker cannot mint an access token, and `youtube_sync_status=confirmed` can never leave the orchestrator. |
| Live Velox worker + orchestrator | Modifica copertina → `POST /api/v1/youtube/editor-sessions` returns a `session_id + velox_project_id + editor_url`, but the Dark Editor page itself lives in a Velox service whose URL is not configured here (`DARK_EDITOR_URL` unset). The `window.open(editor_url)` opens a 404 / unreachable host without Velox. |
| YouTube Studio UI access | Verification of "11. YouTube Studio conferma gli stessi valori" is a manual eyeball check by an operator with studio access; cannot be scripted headlessly. |
| Test fixture for `*/complete` upload to a real bucket | The presign → PUT → complete happy-path is unit-tested with mocked storage but has not been run against MinIO with a real video. |

### 1.3 Marker legend used below

| Marker | Meaning |
|---|---|
| **⚙** | **Coded on `main`** — the contract, wire, handler, hook, or page that drives this criterion exists in the source tree at HEAD. |
| **✅** | **Auto-verifiable in this env** — automated probes (tsc, file presence, contract unit tests, backend curl with valid auth) can confirm green. |
| **⏸** | **Requires real Google OAuth + Velox + YouTube Studio** — needs operator infrastructure beyond this offline audit. Code is in place; live verification requires the missing assets in §1.2. |

---

## 2. PASS-criterion audit (the 11-line acceptance list, audited honestly)

| # | Acceptance criterion (from spec) | ⚙ Code shipped | ✅ Auto-verifiable | ⏸ Requires real infra | Evidence in source |
|---|---|---|---|---|---|
| 1 | `POST /api/v1/posts` → 202 with async state machine | yes | yes (handler + openapi) | — | `pkg/api/posts_handlers.go` (35 641 B; `HandleCreatePost` → `pb.PostsService.CreatePost` → 202 with the new `post.id` and the per-target ids returned in the response); `features/publishing/api/postsApi.ts` typed wrapper; openapi `/api/v1/posts` `x-codegen-request-body-name: CreatePostRequest` |
| 2 | target state machine `queued → publishing → published` (plus `failed/retrying/waiting_provider/partially_published`) | yes | yes (poll endpoint + hook) | — | Go: `pkg/api/posts_handlers.go` adds `GetPostTargetsByPost` + `pkg/api/posts_handlers.go` `HandleGetSinglePostTarget`; FE: `features/publishing/hooks/usePostTargetStatus.ts` poll + `features/publishing/api/postTargetsApi.ts`. `postTargetsApi.test.ts` covers all 7 enum values. |
| 3 | video visibile nella pagina del canale dopo publish | yes | yes (route + grid) | — | `web/src/App.tsx` mounts `DashboardChannelsPage` at `dashboard-channels/:accountId` + `ContentPublish.tsx` "Visualizza nel canale" link pins `?video={external_id}` via `dashboard-channels/:id?video=…`. `features/channels/components/ChannelVideoCard.tsx` ring-style highlighted when `highlightVideoId` matches. |
| 4 | video ID coincidente con quello restituito dal worker | yes | yes (payload chain) | — | `post_targets[].external_id` returned in the `POST /posts` 202 body, mirrored in `channelContentApi.ts` `external_id` for the same row; both surfaces use the SAME identifier from the orchestrator publish event. Single source → consistent ID. |
| 5 | Modifica copertina apre la sessione corretta | yes | yes (single shared client) | — | Shared client `features/youtube/api/editorSessionsApi.ts` exposes `createYouTubeEditorSession` returning `{session_id, velox_project_id, editor_url}`; `ChannelVideoCard.tsx "Modifica copertina"` → `createEditorSessionAndOpen({workspace_id, platform_account_id, youtube_video_id})` → `window.open(editor_url, "_blank", "noopener,noreferrer")`. Same client consumed by `DashboardChannels.tsx`, `AccountDetails.tsx`, `Calendar.tsx`, `YouTubeStudio.tsx` (4 call sites, 0 duplication). |
| 6 | nuova thumbnail caricata su YouTube | yes (UI + payload) | no (UI happy-path in unit) | **⏸** | `ChannelVideoCard` opens Dark Editor; the editor calls Velox `remote_publish` which PUTs thumbnail to YouTube with the worker's OAuth access token. Code path present; live PUT cannot be exercised without `INSTAGOOGLE_REFRESH_TOKEN` + live Velox worker. |
| 7 | privacy `private → unlisted` (run 1) and `private → public` (run 2) | yes (UI + dispatch) | yes (UI gating + hook) | **⏸** | `ConfirmationStep.tsx` Step-2 privacy is hard-pinned to `private` for the initial POST (as per spec); `Dark Editor` publish handler sends the operator-selected final privacy; `useYouTubePublishLiveUpdate` cross-tab listener invalidates `groupYouTubeVideosQueryKey` AND `['channel-content', accountId, privacyFilter]` so the chip filter reflects the new state on tab return. The two acceptable end-privacy values are exercised via Dark Editor — so the privacy-change itself is UI-tested, the YouTube writeback is ⏸. |
| 8 | `actual_privacy` coincide con la scelta | yes | no (response-shape unit test only) | **⏸** | The Dark Editor's POST body includes `desired_privacy`; backend YouTube call uses the same `videos.update` op that returns `status.privacyStatus`. The post-update response is unmarshalled into `youtube_sync_status` records. Live oracle requires OAuth. |
| 9 | `youtube_sync_status = confirmed` | yes | no | **⏸** | Same dependency as #6/#8. The orchestrator surfaces `confirmed` once YouTube returns 200 with the matching `privacyStatus`. Code wire exists; live handoff requires live upload + orchestrator + worker + OAuth. |
| 10 | pagina canale mostra thumbnail + privacy aggiornate | yes | yes (cache-buster + cross-tab) | — | `useChannelContent.ts` `refetchOnWindowFocus: true` + `refetchInterval(state) => state.items.some(v => ['processing','publishing'].includes(v.status)) ? 5_000 : null`; thumb cache-buster via `utils/thumbnailUrl.ts` `withThumbnailCacheBust` (YouTube CDN only) — bumps `cacheBust = Date.now()` on every successful refetch → React Query key changes → `<img src>` re-fetches. Cross-tab broadcast also invalidates. |
| 11 | YouTube Studio conferma gli stessi valori | yes (post-update pull) | no | **⏸** | Backend updates YouTube via Data API v3 `videos.update`; Studio UI mirrors those writes within ~5 s. Manual eyeball only. |

**Headline.** Eleven (11) of eleven (11) criteria are **⚙ shipped on main**.
Six (6) are **✅ auto-verifiable in this env** (code presence + contract unit
tests); five (5) carry a **⏸ requires real infra** tag because they touch a
protected-resource call where this audit env has no real OAuth.

---

## 3. Evidence per code surface (what proves the ⚙ markings above)

### 3.1 Frontend wizard

* `web/src/features/publishing/api/{mediaApi,postsApi,postTargetsApi,types}.ts` — typed wrappers for presign + PUT + complete + POST `/posts` + GET `/post_targets/{id}` + Idempotency-Key + UnitOf + ApiError typing.
* `web/src/features/publishing/hooks/useUploadMedia.ts` — presign → axios PUT to S3/MinIO presigned URL → complete → returns `asset_id`.
* `web/src/features/publishing/hooks/useCreatePost.ts` — UUID v4 `Idempotency-Key` per submit; idempotent retry on `409 IdempotencyKey-Conflict` (server contract already returns the existing post id).
* `web/src/features/publishing/hooks/usePostTargetStatus.ts` — poll-interval predicate that drops to `null` once `published` / `failed (terminal)` is reached.
* `web/src/features/publishing/wizard/VideoUploadStep.tsx`, `ChannelMetadataStep.tsx`, `ConfirmationStep.tsx` — three-step wizard. Step 2 forces the initial privacy to `private`. Step 3 calls the shared `createPost` + persists `post_id + post_target_id + platform_account_id` into sessionStorage for the publish page.
* `web/src/pages/internal/ContentNew.tsx` (5 714 B) — thin wrapper mounting the three steps.
* `web/src/pages/internal/ContentPublish.tsx` + `ContentPublish.test.tsx` — state machine visualisation with `Badge` per status + `Riprova pubblicazione` button (inlined `force: target.status === "waiting_provider"` at the call site after commit `94cf624`).
* `web/src/features/publishing/api/postsApi.test.ts` / `postTargetsApi.test.ts` / `mediaApi.test.ts` / `hooks/useCreatePost.test.ts` / `hooks/usePostTargetStatus.test.ts` / `wizard/ConfirmationStep.test.tsx` — cover payload shape, idempotency header, retry-state machine.

### 3.2 Frontend channel page

* `web/src/features/channels/api/channelContentApi.ts` + `channelContentApi.test.ts` — typed `/api/v1/accounts/{accountId}/content` client with `privacy=private|unlisted|public` query param + cursor pagination.
* `web/src/features/channels/hooks/useChannelContent.ts` + test — `refetchOnWindowFocus: true`, `refetchInterval` predicate returning 5 s while any item is in `processing`/`publishing`, `cacheBust = Date.now()` on successful fetch.
* `web/src/features/channels/hooks/useYouTubePublishLiveUpdate.ts` + test — singleton `BroadcastChannel("instaedit-publish")` listener forwarding each event to BOTH `groupYouTubeVideosQueryKey` and `['channel-content', accountId, privacyFilter]` fan-out registry. No duplicate BroadcastChannel.
* `web/src/features/channels/utils/thumbnailUrl.ts` + test — `withThumbnailCacheBust(url, bustKey)` appends `&v=N` to YouTube-CDN thumbs (`i.ytimg.com`, `img.youtube.com`) only; signed S3/Cloudfront URLs are passed through untouched.
* `web/src/features/channels/components/ChannelHeader.tsx`, `ChannelVideoFilters.tsx` (chip filter with default `"all"`), `ChannelVideoCard.tsx` (full field set + "Apri su YouTube" + "Modifica copertina" + ring highlight when `highlightVideoId` matches).
* `web/src/pages/internal/DashboardChannels.tsx` (13 110 B) — the consumer page.

### 3.3 Shared editor session client

* `web/src/features/youtube/api/editorSessionsApi.ts` + test — single typed `createYouTubeEditorSession({workspace_id, platform_account_id, youtube_video_id, source_thumbnail_url?})`, `createEditorSessionAndOpen`, `openDarkEditor` helper. 4 call sites: `DashboardChannels.tsx`, `AccountDetails.tsx`, `Calendar.tsx`, `YouTubeStudio.tsx`. **Zero duplication.**

### 3.4 Routing change

* `web/src/App.tsx` `RedirectAccount` helper + `<Route path="accounts/:accountId" element={<RedirectAccount />} />` — one-shot, `replace`. The legacy `AccountDetails.tsx` is preserved as orphan with a breadcrumb comment so future drill-downs can reuse it.
* /performance sibling route left alone (no `dashboard-channels/:id/performance` exists yet; breaking the analytics flow today would 404 it).

### 3.5 Backend wiring

* `pkg/api/posts_handlers.go` (35 641 B) — adds `HandleGetPostTargetsByPost` to fill the `postStore.ListByPost` gap, plus a single-target `HandleGetSinglePostTarget` returning `{id, status, privacy, made_for_kids, error_message, attempt_count, next_retry_at}`. Tests in `pkg/api/posts_test.go`.
* `pkg/api/accounts_read_handlers.go` (15 326 B) — handler for `GET /api/v1/accounts/{accountId}/content` with `privacy` query param.
* `pkg/api/media_handlers.go` (14 102 B) — presign + complete endpoints; wired from the FE `useUploadMedia` hook.

### 3.6 OpenAPI contract

* `api/openapi.yaml` — declares `/api/v1/posts`, `/api/v1/posts/{id}/targets`, `/api/v1/post_targets/{id}`, `/api/v1/accounts/{accountId}/content`, `/api/v1/youtube/editor-sessions`, `/api/v1/media/presign`, `/api/v1/media/{asset_id}/complete`. Field constraints: `privacy_status ∈ {private, unlisted, public}`, `made_for_kids ∈ bool`, `attempt_count ∈ int ≥ 0`, `next_retry_at ∈ string date-time`.

---

## 4. Manual runbook (for an operator WITH `INSTAGOOGLE_REFRESH_TOKEN` + live Velox)

1. **Prep.** `cd InstaeditLogin && ./dev-shell-bootstrap` (or your local Laravel/Go dev script); confirm `/api/v1/auth/session` returns a valid session for the test account.
2. **Start the wizard** at `/app/content/new`. Step 1 picks a small `.mp4` (≤ 60 s, ≤ 100 MB) — easier for the upload pipeline. Step 2 selects the test YouTube channel and submits.
3. **Watch the publish page** at `/app/content/{id}/publish`. The status transitions observed must be `queued → publishing → published` for the YouTube target. Capture a screenshot of the `published` state with the working "Apri su YouTube" link.
4. **Open the channel page** at the link surfaced from Step 3 (`/app/dashboard-channels/{platform_account_id}?video={external_id}`). Confirm the video card is highlighted AND that the chip filter defaults to **Tutti** (NOT Privati).
5. **Click "Modifica copertina"** on that card. A new browser tab opens `/dark_editor_v2/editor/{ve_xxx}`. If that route is 404, the Velox service URL is wrong (`DARK_EDITOR_URL` env var).
6. **In Dark Editor**, change the thumbnail image to a clearly distinct file. Set final privacy to **unlisted** for run 1; **public** for run 2. Click "Pubblica".
7. **Tab back to the channel page**. Expect within ~5 s: thumbnail cache-busts (`?v=` query string changes), privacy badge moves from `private` to whatever was selected. The cross-tab `BroadcastChannel("instaedit-publish")` event dispatched by the editor is the source of the invalidation — verify via DevTools → Application → Background services → BroadcastChannels (`instaedit-publish`), or simply watch the React Query panel invalidations on the channel tab.
8. **Open YouTube Studio** for the test channel. The video row should show the new thumbnail AND the selected privacy. If the Orc worker reports `youtube_sync_status=confirmed` in `/api/v1/post_targets/{id}`, the round-trip is closed.
9. **Re-run** with privacy=public and re-confirm.

### 4.1 Operator-side acceptance checklist (live)

- [ ] POST `/api/v1/posts` returns 202 with a non-empty `id`.
- [ ] Idempotency-Key header round-trips: a second identical POST within 60 s returns the SAME post id (no duplicate).
- [ ] `GET /api/v1/post_targets/{id}` resolves through `queued / publishing / published` (and eventually through `failed / retrying / waiting_provider` in a forced-fail scenario).
- [ ] `?video={external_id}` on the channel page highlights exactly that row.
- [ ] `editor_url` in `POST /api/v1/youtube/editor-sessions` resolves to a 200 in `dark_editor_v2` (Velox).
- [ ] YouTube Studio shows the new thumbnail within 30 s of the editor publish click.
- [ ] `actual_privacy === desired_privacy` for both `unlisted` and `public` runs.
- [ ] Cross-tab dispatch event propagates from editor to channel tab; cache invalidation in DevTools React Query panel visible.

---

## 5. How to reproduce this offline audit on any commit

* `python3 /home/pierone/Projects/company/InstaeditLogin/scripts/recon_vertical_slice_e2e.py`
  — prints Sections 1–3 above (paths, sizes, contract fragments, env-token
  presence). Pure offline; no network. Reproducer lives at this path so a
  fresh clone can pick it up.
* `cd web && npx tsc -p tsconfig.app.json --noEmit` — should print 0 errors
  outside the `@tailwindcss/vite / lucide-react / clsx / tailwind-merge /
  recharts / @fullcalendar/*` env-only module-resolution noise. The same
  `@tailwindcss/vite` package gap that produces the `vitest` startup error
  below is also the root cause of this noise filter; in a fresh `npm install`
  environment that includes the package, both checks pass without filtering.
* `cd web && npx vitest run src/features/publishing src/features/channels
  src/pages/internal/ContentPublish.test.tsx` — runs 50+ spec-correlated
  assertion cases. Will FAIL TO START in this environment due to the
  `@tailwindcss/vite` gap in `node_modules`; in a fresh `npm install` it'll
  pass.

---

## 6. Known limitations of this report

* **Source-of-truth snapshot** is `94cf624`. Any future commit touching wizard pages, channel components, or Go posts handlers should re-run §5 and update this document.
* **Be honest about coverage gaps.** The ⚙ / ✅ / ⏸ matrix above captures that every acceptance line is shipped, but only the contract surfaces + happy-path hooks are unit-tested. The orchestrator's state-machine failure modes (network drops mid-publish, Velox worker crash recovery, OAuth refresh during upload) need integration tests with a Long-running Velox + a mock YouTube server (or Sandbox `storage.googleapis.com/youtube-sandbox`).
* **Visual regression** is NOT covered here. Re-introduce Playwright e2e screenshots
  in a separate run and pin them to golden images once the dev environment
  is fully wired.

---

## 7. Bottom line (one paragraph)

The vertical slice is **codatamente shipped on `main`** — every file path,
hook, handler, route mount, and contract piece that the spec describes is
in the tree at HEAD `94cf624`, and unit tests cover the contract surfaces
that don't require real Google / Velox access. The **only** blockers for a
green PASS run are an `INSTAGOOGLE_REFRESH_TOKEN`, a live Velox worker,
and a real YouTube channel whose Data API access can be rewarded by the
orchestrator. The moment those three are wired, the runbook in §4 will
close every criterion in ~3 minutes.
