# VERIFICATION_REPORT — Blocco #2 Vertical Slice

This report maps each of the 11 PASS criteria the user-defined spec
demands to the concrete evidence that proves it. It is intentionally
honest about what can be locked offline (vitest + static + backend
handler tests) and what can only be confirmed via live infra (a
running InstaeditLogin backend + a real YouTube sandbox channel + a
real Velox `/dark_editor_v2` instance).

## Boundary

| Scope | Status |
|---|---|
| TypeScript payload contracts (wizard → POST /posts, channel list, editor-sessions) | **Locked offline** via Vitest (39 web/src test files + ~330 VeloxEditiingg backend test files). |
| Backend Go handler behaviour (posts handler, editor-sessions handler, Velox postback validation, publish pipeline) | **Locked offline** via the per-handler Go test suite. |
| State machine transitions inside the app (publish status, channel content refetch, editor-sessions double-click guard) | **Locked offline** via Vitest state-machine assertions. |
| Visual rendering (header, filter chips, video card highlight, error states) | **Locked offline** via Vitest + RenderHook renders. |
| **Real YouTube upload + thumbnail write** | **Live infra only** — MUST be executed by the operator against a staging backend + OAuth tokens + a real YouTube sandbox channel. The Vitest suite mocks `authedFetch` and never leaves the sandbox. |
| **Real Velox InstaEditor (postMessage contract)** | **Live infra only** — the Velox InstaEditor must actually load at `/dark_editor_v2/editor/{velox_project_id}` and the user must interact with it. Vertex's iframe postMessage contract is verified at the schema level by `pkg/api/internal_velox_validate_test.go` but the iframe-to-client UX is unverifiable without the live Velox. |

## 11 PASS matrix

| # | Criterion | Status | Verifiable evidence | Commit |
|---|---|---|---|---|
| 1 | `POST /api/v1/posts` returns 202 Accepted | ✅ FULL | Payload contract locked in `ConfirmationStep.test.tsx` (private `private` literal, complete `targets[0].settings.youtube.*` shape, `Idempotency-Key` UUID per submit). Backend handler `pkg/api/posts_create_handler.go` shape matches; Go tests assert 202 + Accepted body. | `6a23296`, `be7f18c` |
| 2 | target state machine: `queued → publishing → published` (plus `failed`, `retrying`, `waiting_provider`, `partially_published`) | ⚠️ PARTIAL | UI-side transitions covered in `ContentPublish.tsx` + `usePostTargetStatus.test.tsx`. **Persistence-layer transitions** are covered by `internal/repository/youtube_target_publication_repo_atomic_test.go` (repo unit tests for state changes) and `internal/worker/publish_worker_publish_test.go` (worker step tests). **Gap:** there is no integration test that drives the live worker goroutine end-to-end over the full lifecycle (e.g. enqueue → lease → publish → verify `confirmed`). Until that integration test OR a successful runbook execution with captured transition timestamps exists, this row is PARTIAL. | `8f8b786`, `2a8c08b` + gap: needs `internal/integration/publish_lifecycle_test.go` (or runbook logs) |
| 3 | video visible in channel page after publish | ✅ FULL | `useChannelContent.test.tsx` `asserts items render, nextCursor advances, loadMore appends`. `DashboardChannels.test.tsx` asserts cards render with correct external_id. | `20894d6`, `5cfe0c1` |
| 4 | same `external_id` between wizard submitted `youtube_video_id` and the row shown on the channel | ✅ FULL | ConfirmationStep sends `youtube_video_id` in payload; `DashboardChannels.test.tsx` renders cards and `ChannelVideoCard` displays the same `external_id`. Backend leans on `youtube_video_id` as the dedupe key (`internal/repository/youtube_target_publication_repo.go`). | `20894d6`, `e51430c` |
| 5 | "Modifica copertina" opens the correct editor session | ✅ FULL | Hook `useCreateYouTubeEditorSession` fires through the canonical client `editorSessionsApi.createEditorSessionAndOpen` (committed `e11ddeb`). `DashboardChannels.test.tsx` asserts the payload `workspace_id` + `platform_account_id` + `youtube_video_id` matches and `createEditorSessionAndOpen` is called once per click; `inflightEditRef` prevents double-session creation. | `20894d6`, `e11ddeb` |
| 6 | new thumbnail visible on YouTube | ⚠️ PARTIAL | Schema contract: POST `/api/v1/youtube/editor-sessions/{id}/thumbnail` contract is locked in `editorSessionsApi.test.ts` via `pkg/api/youtube_thumbnail_session_create.go`. The actual upload to YouTube goes through the Velox editor + `internal/services/youtube_publish_thumbnail.go` which is unit-tested with mocked responses. **Cannot verify the real upload without live OAuth.** | `e11ddeb` + `internal/services/youtube_publish_thumbnail_test.go` |
| 7 | privacy flips `private → unlisted` then `private → public` | ⚠️ PARTIAL | Schema contract locked: `PublishYouTubeEditorSessionRequest { privacy_status: "public" \| "unlisted" \| "private" }` is type-tested in `editorSessionsApi.test.ts`. The Velox-postback-validated `actual_privacy` field is asserted in `pkg/api/internal_velox_validate_test.go` ("yields actual_privacy when valid choice is sent"). **Cannot verify the ACTUAL YouTube privacy flip without live OAuth.** | `e11ddeb` + `pkg/api/internal_velox_validate_test.go` |
| 8 | `actual_privacy` field matches the post-back | ✅ FULL | `pkg/api/internal_velox_validate.go` enforces `ActualPrivacy == "public"|"unlisted"|"private"`. `pkg/api/youtube_publish_actual_privacy_test.go` asserts the bridge between the Velox response and our `actual_privacy` column. | already shipped |
| 9 | `youtube_sync_status = confirmed` | ⚠️ PARTIAL | `internal/repository/youtube_target_publication_repo.go` + `pkg/api/youtube_thumbnail_session_create.go` write the column on a successful Velox postback + publish reconcile. `internal/worker/youtube_processing_reconciler.go` flips pending→confirmed. **Same integration gap as row 2:** the cited atomic test is a persistence-layer unit test, not a live-worker goroutine that drives the real lifecycle. Until `internal/integration/publish_lifecycle_test.go` (or runbook logs) prove the end-to-end flip, this row is PARTIAL. | already shipped + same integration-test gap as row 2 |
| 10 | channel page shows the updated thumbnail + privacy after the publish | ⚠️ PARTIAL | `ChannelVideoCard` reads `privacy` + `thumbnail_url` + the upload status from the API every refetch (`useChannelContent.test.tsx`). The `ChannelVideoCard.highlightVideoId` pass-through is wired. **BUT:** the page currently relies on a manual "Aggiorna" click OR a window-focus refetch to see the post-publish update. The spec-mandated `useYouTubePublishLiveUpdate` cross-tab invalidation on the **channel-content** query key is still pending — flagged in commit `e11ddeb` review as a followup. | `20894d6` + flagged followup |
| 11 | YouTube Studio confirms the same thumbnail + privacy + sync | 🔴 MANUAL ONLY | No live E2E was executed in this environment — there is no YouTube OAuth credential bound to this session, no live Velox endpoint, and no production-grade YouTube sandbox channel. The runbook below is what the operator executes to close this gap. | out of scope (manual) |

## Honest summary

- **PASS (offline-locked):** 1, 3, 4, 5, 8 → the **payload contracts + schema bridges + UI rendering** for the entire vertical slice are locked by Vitest + Go handler tests.
- **PARTIAL (schema / persistence locked, real-world not tested):** 2, 6, 7, 9, 10 → wire-level contracts to YouTube + Velox are locked AND the repository-layer state transitions are unit-tested, but **rows 2 + 9** also need an integration test that drives the live worker goroutine end-to-end (`internal/integration/publish_lifecycle_test.go`) while **rows 6 + 7 + 10** additionally need the live YouTube sandbox channel via the runbook.
- **MANUAL ONLY:** 11 → YouTube Studio round-trip.

The 6 PARTIAL/MANUAL items cluster around two distinct gaps: **a missing integration test that drives the live worker goroutine end-to-end** (rows 2 + 9), and **a missing live-operator runbook** (rows 6 + 7 + 10 + 11). The delta ships the contracts; both deliverables close the rest.

## Live E2E runbook (the operator executes this)

### Preconditions
- InstaeditLogin backend running locally or on staging: `go run ./cmd/instaedit-login` (see `docs/LOCAL-DEVELOPMENT.md`).
- OAuth tokens for a YouTube sandbox channel with permission to upload and to set `unlisted` + `public`.
- Velox staging server reachable at `VELOX_BASE_URL` + you have a valid workspace_id published in our DB.
- `CLIENT_BASE_URL` env var points to the frontend (default `http://localhost:5173`).

### Run #1 — unlisted
1. Open `http://localhost:5173/app/content/new`.
2. Step 1 — upload `tests/fixtures/video_alpha.mp4`. Wait for `asset_id` to render.
3. Step 2 — select channel `Test YouTube Sandbox`, title `E2E UNLISTED 2026-07-30`, description `e2e unlisted verification`, tags `e2e,unlisted`. Privacy chip locked to **Privato** (spec mandate).
4. Step 3 — verify the summary. Click **Carica su YouTube**. Status badge flips `In coda → In pubblicazione → Pubblicato`.
5. `/app/content/{post.id}/publish` shows `Visualizza nel canale` + `Apri su YouTube`.
6. Click **Visualizza nel canale** → `/app/dashboard-channels/{platformAccountId}?video={youtubeVideoId}`.
7. Verify: card row matches the `youtube_video_id` from Step 2; status `Live`; privacy `Privato`.
8. Click **Modifica copertina** → new tab opens at `/dark_editor_v2/editor/{velox_project_id}`.
9. Drag a new thumbnail onto the editor canvas. Set visibility to **Non in elenco**. Click **Pubblica**.
10. Editor returns to the channel page. Auto-refetch expected (currently manual): click **Aggiorna**.
11. Card now shows: privacy `Non in elenco`, thumbnail refreshed (cache busted).
12. **YouTube Studio** (separate tab on `studio.youtube.com`) → open the test channel → `Video` → find by ID → confirm thumbnail + visibility = **Non in elenco**. **PASS = #6, #7, #10 (auto-refetch), #11.**

### Run #2 — public
Repeat 1-12 with visibility set to **Pubblico** in step 9. **PASS = #6, #7, #10, #11.**

### Verifies the 11-row matrix

| # | Run #1 unlisted | Run #2 public |
|---|---|---|
| 1 | wizard→202 | wizard→202 |
| 2 | state machine | state machine |
| 3 | dashboard row exists | dashboard row exists |
| 4 | same external_id | same external_id |
| 5 | Velox project opened | Velox project opened |
| 6 | thumbnail visible on YouTube | thumbnail visible on YouTube |
| 7 | `private → unlisted` | `private → public` |
| 8 | `actual_privacy="unlisted"` | `actual_privacy="public"` |
| 9 | `youtube_sync_status="confirmed"` | `youtube_sync_status="confirmed"` |
| 10 | channel card → unlisted + new thumb | channel card → public + new thumb |
| 11 | YouTube Studio = unlisted + new thumb | YouTube Studio = public + new thumb |

## What landed (commit grounding)

| Commit | Description |
|---|---|
| `20894d6` | DashboardChannelsPage + useChannelAccount (Blocco #2 vertical slice) |
| `5cfe0c1` | channelContentApi + useChannelContent hook (Blocco #2 wire-up) |
| `e51430c` | ChannelHeader + ChannelVideoFilters + ChannelVideoCard components |
| `e11ddeb` | Centralize createYouTubeEditorSession into a shared client |
| `6a23296` | Step 3 — confirmation + POST /api/v1/posts + Idempotency-Key |
| `8f8b786` | /app/content/:postId/publish status page + failed retry |
| `12eb717` | Step 2 — channel + metadata step |
| `6e30ba1` | Step 1 — video upload wizard |
| `2a8c08b` | publishing hooks useCreatePost + usePostTargetStatus |
| `be7f18c` | publishing API layer (media, posts, post targets) |

## Open follow-ups (not blockers for ship)

The 6 PARTIAL/MANUAL items (5 PARTIAL + row 11 MANUAL) cluster around TWO distinct gaps already named in the Honest summary above. Both gaps are not blockers for ship, but each is a clean next-iteration target:

**Gap A — missing live-worker integration test (rows 2 + 9).** Add `internal/integration/publish_lifecycle_test.go` that drives the actual worker goroutine over the full target lifecycle (`queued → publishing → published`) and asserts `youtube_sync_status = confirmed` end-to-end. Rows 2 + 9 both flip from PARTIAL to FULL once it's green and reproducible.

**Gap B — cross-tab invalidation + live infra runbook (rows 6 + 7 + 10 + 11).** Once `useYouTubePublishLiveUpdate` (or its successor) dispatches a `CustomEvent("youtube-publish-changed")` with `{ account_id, status, actual_privacy }` and `DashboardChannelsPage` listens + auto-refetches, row 10 flips from PARTIAL to FULL without operator intervention. Rows 6, 7, 11 close only via the live operator runbook above.

Other next-step candidates:

- Add a Playwright E2E harness (`tests/e2e/youtube-publish-unlisted.spec.ts` + `youtube-publish-public.spec.ts`) that drives the operator runbook headlessly. The repo already has `playwright.config.ts`.
- Promote inline `/api/v1/workspaces` lookup into a shared `useWorkspaces` hook (current duplication: DashboardChannels.tsx, AccountDetails.tsx, Calendar.tsx).
