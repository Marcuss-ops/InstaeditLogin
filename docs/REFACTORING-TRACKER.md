# Refactoring Tracker

Snapshot of tracked source files above the **500-line threshold**, plus a
**450–500 watchlist** of files about to cross it, with their current size and
refactoring status. Kept in sync with the tooling:

- `scripts/loc-report.sh` — informational report: top-N largest files + files
  above threshold (default `-t 500`).
- `scripts/loc-check.sh` — CI gate: fails when a tracked file **grows** past
  the threshold vs. `origin/main` (`make loc-check`; strict full-tree via
  `make loc-check LOC_AGAINST=none`).

Scan: tracked `go` / `ts` / `tsx` files only (docs, SQL, JSON, YAML excluded).
59 files are currently above 500 lines; 44 more sit in the 450–500 watchlist.

_Snapshot: 2026-08-02 (third update today — 9 more files split below
threshold since the afternoon snapshot: 8 test files >800 lines split by
scenario + DriveBatchImportDialogViews.tsx. The >800 section is now empty:
no tracked source file is above 800 lines today. Count dropped 69 → 59)._

## Legend

| Status | Meaning |
|--------|---------|
| `da fare` | Still above 500 lines; candidate for a split (see the split-by-concern pattern used in the "Completati" section). |
| `fatto` | Refactored this session; now below the threshold (see Completati). |
| `#NNN` | Placeholder for the GitHub issue tracking the refactor (labeled `refactor`). Replace with the real issue number once the issue is created. |

## GitHub issue convention

Every `da fare` row maps 1:1 to a GitHub issue labeled `refactor`. The `Issue`
column currently holds the placeholder `#NNN`; no issues have been created yet.
When creating one for a file, use a title like
`refactor: split <file> below 500 lines` and apply the `refactor` label, then
replace the placeholder in this tracker with the real number (e.g. `#201`).

```bash
gh issue create \
  --label refactor \
  --title "refactor: split pkg/api/youtube_publish_pipeline_test.go (1157 lines)" \
  --body "See docs/REFACTORING-TRACKER.md — split by concern, keep test coverage, run go test, update this tracker."
```

---

## > 800 lines (0 files)

The 8 test files that previously sat here (nvidia_metadata_publish_e2e 928,
youtube_oauth_browser_e2e 926, internal_velox_get_delivery 923,
migrations_integration 901, internal_velox_validate 898,
channel_authorization 883, auth_routes_callback 865,
publish_worker_publish 834) were all split by scenario on 2026-08-02
(commit `5132462`). No tracked source file is above 800 lines today — the
`loc-check` strict offenders list is empty.

## 600–800 lines (25 files)

| File | Righe | Stato | Issue |
|------|------:|-------|-------|
| `internal/worker/publish_reconcile_integration_test.go` | 766 | da fare | #NNN |
| `internal/repository/upload_job_pool_test.go` | 765 | da fare | #NNN |
| `internal/worker/reconcile_worker_test.go` | 751 | da fare | #NNN |
| `internal/services/instagram_oauth_test.go` | 719 | da fare | #NNN |
| `internal/services/youtube_oauth_binding_test.go` | 712 | da fare | #NNN |
| `internal/deliveries/group_expand_test.go` | 711 | da fare | #NNN |
| `internal/services/provider_error_test.go` | 710 | da fare | #NNN |
| `pkg/api/posts_test.go` | 708 | da fare | #NNN |
| `pkg/api/internal_velox_resolve_target_test.go` | 708 | da fare | #NNN |
| `pkg/api/internal_velox_deliveries_test.go` | 707 | da fare | #NNN |
| `internal/repository/post_repo_test.go` | 705 | da fare | #NNN |
| `internal/auth/jwt_test.go` | 700 | da fare | #NNN |
| `pkg/api/internal_velox_thumbnail_session_test.go` | 694 | da fare | #NNN |
| `pkg/api/internal_velox_deliver_test.go` | 694 | da fare | #NNN |
| `pkg/api/accounts_performance_assembler_test.go` | 683 | da fare | #NNN |
| `tests/e2e/oauth_callback_binding_e2e_test.go` | 675 | da fare | #NNN |
| `internal/worker/publish_worker_publish_youtube_test.go` | 666 | da fare | #NNN |
| `internal/deliveries/state_test.go` | 646 | da fare | #NNN |
| `pkg/api/internal_velox_callback_dispatcher_test.go` | 641 | da fare | #NNN |
| `pkg/api/accounts_performance_handlers_test.go` | 639 | da fare | #NNN |
| `cmd/batch-import-drive-folder/main_test.go` | 629 | da fare | #NNN |
| `tests/e2e/validate_account_e2e_test.go` | 628 | da fare | #NNN |
| `internal/testutil/runtime/runtime_test.go` | 609 | da fare | #NNN |
| `internal/worker/mocks_test.go` | 608 | da fare | #NNN |
| `tests/e2e/e2e_harness_fakes.go` | 601 | da fare | #NNN |

## 500–600 lines (34 files)

| File | Righe | Stato | Issue |
|------|------:|-------|-------|
| `pkg/api/youtube_group_videos_phantom_test.go` | 597 | da fare | #NNN |
| `pkg/api/internal_velox_e2e_helpers_test.go` | 590 | da fare | #NNN |
| `pkg/api/account_routes_test.go` | 589 | da fare | #NNN |
| `cmd/yttest/main.go` | 586 | da fare | #NNN |
| `internal/models/external_delivery_test.go` | 578 | da fare | #NNN |
| `pkg/api/common_test_mocks_test.go` | 573 | da fare | #NNN |
| `internal/providers/registry_test.go` | 553 | da fare | #NNN |
| `internal/models/external_delivery.go` | 549 | da fare | #NNN |
| `internal/worker/process_ingest_verify_integration_test.go` | 547 | da fare | #NNN |
| `web/src/pages/internal/ScheduledByAccount.tsx` | 546 | da fare | #NNN |
| `tests/e2e/e2e_harness_helpers.go` | 542 | da fare | #NNN |
| `pkg/api/drive_batch_uploads_test.go` | 541 | da fare | #NNN |
| `internal/services/provider_error.go` | 540 | da fare | #NNN |
| `cmd/test-youtube-upload/main.go` | 540 | da fare | #NNN |
| `internal/repository/post_repo_retry.go` | 537 | da fare | #NNN |
| `pkg/api/auth_handlers.go` | 532 | da fare | #NNN |
| `internal/repository/group_repo.go` | 531 | da fare | #NNN |
| `internal/services/threads_oauth.go` | 528 | da fare | #NNN |
| `internal/config/config.go` | 528 | da fare | #NNN |
| `cmd/batch-import-drive-folder/main.go` | 527 | da fare | #NNN |
| `internal/services/facebook_oauth_test.go` | 526 | da fare | #NNN |
| `pkg/api/youtube_group_videos_list_test.go` | 517 | da fare | #NNN |
| `internal/services/youtube_oauth_validate_test.go` | 515 | da fare | #NNN |
| `internal/services/http_client_test.go` | 513 | da fare | #NNN |
| `web/src/pages/Programs.tsx` | 512 | da fare | #NNN |
| `web/src/features/publishing/wizard/ConfirmationStep.tsx` | 510 | da fare | #NNN |
| `pkg/api/youtube_editor_sessions_publish_inflight_test.go` | 509 | da fare | #NNN |
| `pkg/api/internal_velox_e2e_test.go` | 507 | da fare | #NNN |
| `internal/repository/post_repo_post.go` | 507 | da fare | #NNN |
| `web/src/pages/internal/Compose.tsx` | 506 | da fare | #NNN |
| `web/src/features/publishing/wizard/ChannelMetadataStep.tsx` | 506 | da fare | #NNN |
| `internal/database/multi_tenancy_test.go` | 504 | da fare | #NNN |
| `scripts/distribute_channels_to_managers/main_test.go` | 502 | da fare | #NNN |
| `internal/repository/delivery_session_repo.go` | 502 | da fare | #NNN |

## 450–500 lines (44 files) — watchlist (prossimi alla soglia)

Not yet above 500 but within 50 lines of the threshold. Not gated by
`loc-check` and not `da fare` yet — monitor them, since a single growth event
can push them over. No GitHub issues planned for these yet.

| File | Righe |
|------|------:|
| `pkg/api/oauth_session_redirect_test.go` | 499 |
| `internal/bootstrap/workers_wiring.go` | 499 |
| `internal/services/youtube_oauth_resume_test.go` | 498 |
| `internal/repository/import_batch_repo.go` | 497 |
| `internal/repository/outbox_repo_test.go` | 495 |
| `pkg/api/youtube_editor_sessions_update_test.go` | 493 |
| `internal/repository/youtube_video_edit_sessions.go` | 493 |
| `internal/analytics/period_resolver_test.go` | 493 |
| `internal/models/post.go` | 492 |
| `internal/services/tiktok_publish_test.go` | 491 |
| `pkg/api/csrf_test.go` | 489 |
| `pkg/api/drive_batch_common_test.go` | 488 |
| `internal/repository/external_delivery_repo_test.go` | 488 |
| `web/src/features/channels/hooks/useChannelContent.test.tsx` | 487 |
| `internal/outbox/dispatcher.go` | 487 |
| `internal/deliveries/state.go` | 486 |
| `pkg/api/accounts_performance_assembler.go` | 484 |
| `pkg/api/account_sync_oauth_test.go` | 484 |
| `internal/services/provider.go` | 483 |
| `internal/services/delivery_drive_destination_test.go` | 483 |
| `pkg/api/velox_types.go` | 481 |
| `pkg/api/drive_batch_v2_test.go` | 481 |
| `pkg/api/accounts_read_handlers.go` | 480 |
| `internal/credentials/vault_integration_test.go` | 479 |
| `internal/services/youtube_validate.go` | 478 |
| `pkg/api/youtube_editor_sessions_metadata_test.go` | 476 |
| `pkg/api/drive_import_handlers.go` | 475 |
| `pkg/api/router.go` | 473 |
| `pkg/api/drive_batch_v2_handlers.go` | 472 |
| `internal/repository/outbox_repo.go` | 472 |
| `web/src/pages/internal/AccountPerformance.tsx` | 471 |
| `internal/services/google_drive_oauth_test.go` | 471 |
| `internal/repository/post_repo_idempotency_test.go` | 469 |
| `internal/worker/reconcile_worker.go` | 467 |
| `web/src/pages/internal/Dashboard.tsx` | 464 |
| `internal/services/youtube_channel_content.go` | 463 |
| `pkg/api/youtube_editor_sessions_publish_test.go` | 461 |
| `internal/worker/drive_batch_crawler_test_helpers_test.go` | 461 |
| `pkg/api/workspace_channels_test.go` | 460 |
| `pkg/api/channel_analytics_service_test.go` | 456 |
| `tests/e2e/youtube_oauth_browser_e2e_test.go` | 454 |
| `pkg/api/media_test.go` | 454 |
| `internal/repository/post_repo_aggregate.go` | 453 |
| `internal/veloxclient/client_test.go` | 451 |

**Nota:** `web/src/pages/internal/GroupYouTubeVideos.tsx` e
`web/src/pages/internal/DriveBatchImportDialogViews.tsx` sono stati splitati
oggi (159 e 16 righe) e non compaiono più in nessuna tabella — vedi
Completati. `tests/e2e/youtube_oauth_browser_e2e_test.go` (454) è il nuovo
ingresso watchlist dopo lo split da 926. I file splitati oggi
(`internal/outbox/dispatcher.go` 487, `pkg/api/router.go` 473) restano in
watchlist — monitorarli per non farli risalire.
`internal/services/tiktok_publish_test.go` (491) resta sotto soglia —
monitorarlo per non farlo risalire.
`internal/worker/drive_batch_crawler_test_helpers_test.go` (461) è l'helper
file dello split del crawler, anch'esso sotto soglia ma in watchlist.

---

## Completati in questa sessione (stato: `fatto`)

Files split below the threshold during this session's refactoring campaign
(ongoing on `main`). All follow the split-by-concern pattern:
extract helpers/types into `*_helpers` / `*_types` files, then split tests per
feature/scenario.

| File | Prima | Dopo | Split in |
|------|------:|-----:|----------|
| `pkg/api/youtube_publish_pipeline_test.go` | 1157 | 139 | `youtube_publish_pipeline_test.go` (139) + `_shared_test.go` (132) + `_thumbnail_test.go` (264) + `_assetsize_test.go` (144) + `_localization_test.go` (166) + `_cas_test.go` (418) |
| `internal/services/tiktok_publish_test.go` | 937 | 491 | `tiktok_publish_test.go` + `tiktok_publish_mock_test.go` (197) + `tiktok_publish_pullfromfile_test.go` (280) |
| `internal/worker/drive_batch_crawler_test.go` | 1014 | 326 | `drive_batch_crawler_test.go` (326) + `drive_batch_crawler_test_helpers_test.go` (465) + `drive_batch_crawler_e2e_test.go` (260) |
| `pkg/api/drive_batch_import_test.go` | 1142 | 159 | `drive_batch_import_test.go` + `_errors_test.go` (139) + `_pagination_test.go` (246) + `_shareddrive_test.go` (151) + `_idempotency_test.go` (340) + `_e2e_test.go` (177) |
| `tests/e2e/pipeline_e2e_test.go` | 954 | 104 | per scenario + helper in `e2e_harness` |
| `internal/outbox/dispatcher_test.go` | 976 | 174 | `dispatcher_{dispatch,retry,errors,concurrency}_test.go` |
| `docs/OAUTH-PRODUCTION.md` | 979 | 321 | `oauth-google-{setup,limits,rollout,monitoring,troubleshooting}.md` |
| `docs/OPERATIONS.md` | 827 | 115 | `operations-{deploy,monitoring,runbook,email}.md` |
| `pkg/api/common_test.go` | 892 | 83 | `common_test_helpers_test.go` + `common_test_mocks_test.go` |
| `pkg/api/youtube_group_videos.go` | 767 | 304 | `youtube_group_videos_{types,helpers,cache,fetch}.go` + (second pass) `resolveGroupYouTubeAccounts` + `writeGroupVideosOK` → `_helpers.go` |
| `web/src/pages/internal/YouTubeStudio.tsx` | 709 | 149 | `useYouTubeStudio{Data,Actions,PrivateVideos}.ts` + sezioni |
| `web/src/components/booking/BookingProvider.tsx` | 694 | 8 | context + `BookingModal.tsx` + moduli dedicati |
| `web/src/pages/internal/ContentPublish.test.tsx` | 676 | — (eliminato) | `ContentPublish.{retryGating,retryFlow,states,crossTab}.test.tsx` + `testUtils.tsx` |
| `pkg/api/modules.go` | 649 | 40 | un file per modulo |
| `internal/auth/jwt.go` | 605 | 175 | `jwt_{issue,verify,middleware,random,connectlink}.go` |
| `pkg/api/uploads_handlers.go` | 604 | 106 | `uploads_{filters,counts,list,schedule}_handlers.go` |
| `pkg/api/admin_channels_handlers.go` | 592 | 139 | `admin_channels_{connectlink,fleet,import}.go` |
| `pkg/api/youtube_editor_sessions_by_project.go` | 551 | 220 | `youtube_editor_sessions_by_project_publish.go` (341) |
| `internal/services/youtube_oauth.go` | 575 | 389 | `youtube_types.go` (184) |
| `web/src/pages/internal/ContentPublish.tsx` | 622 | 213 | `contentPublishStatusVisual.ts` (131) + `useContentPublishRetry.ts` (67) + `ContentPublish{AggregateBanner,TargetRow,SuccessCard}.tsx` |
| `web/src/components/booking/BookingModal.tsx` | 600 | 279 | `bookingModalSteps.tsx` (192) + `bookingModalPrimitives.tsx` (140) |
| `pkg/api/posts_handlers.go` | 583 | 434 | `posts_create.go` (247) — 5 phase helpers di `handleCreatePost` |
| `pkg/api/internal_velox_callback_dispatcher.go` | 577 | 321 | `internal_velox_callback_dispatcher_{types,helpers}.go` |
| `internal/outbox/dispatcher.go` | 571 | 487 | `dispatcher_backoff.go` (66) + `dispatcher_mark.go` (70) |
| `pkg/api/youtube_editor_sessions.go` | 560 | 409 | `youtube_editor_sessions_{thumbnail,inflight}.go` |
| `pkg/api/accounts_write_handlers.go` | 544 | 90 | `accounts_validate.go` (332) + `accounts_sync.go` (152) |
| `pkg/api/groups_handlers.go` | 541 | 351 | `groups_accounts.go` (132) + `groups_settings.go` (89) |
| `internal/deliveries/group_expand.go` | 537 | 366 | `group_expand_status.go` (181) |
| `pkg/api/router.go` | 535 | 473 | 13 wrapper velox/integrations → `modules_velox.go` + `modules_integrations.go` |
| `internal/repository/platform_account_repo.go` | 534 | 135 | `platform_account_{attach,reauth,crud}.go` |
| `web/src/pages/internal/GroupYouTubeVideos.tsx` | 576 | 159 | `useGroupYouTubeVideos.ts` (246) + `groupYouTubeVideos{Types,Visual}.ts` + `GroupYouTubeVideo{Card,PreviewModal}.tsx` |
| `web/src/pages/internal/DriveBatchImportDialogViews.tsx` | 575 | 16 | barrel: `driveBatchImport{Form,Views,Primitives,Format}` |
| `pkg/api/nvidia_metadata_publish_e2e_test.go` | 928 | 383 | `_negative_test.go` (426) + `_helpers_test.go` (149) |
| `tests/e2e/youtube_oauth_browser_e2e_test.go` | 926 | 454 | `_fakes_test.go` (357) + `_seed_test.go` (145) |
| `pkg/api/internal_velox_get_delivery_test.go` | 923 | 226 | `_helpers_test.go` (381) + `_spec8_test.go` (336) |
| `internal/database/migrations_integration_test.go` | 901 | 444 | `_oauth_backfill_test.go` (254) + `_upload_jobs_test.go` (225) |
| `pkg/api/internal_velox_validate_test.go` | 898 | 249 | `_helpers_test.go` (208) + `_diag_test.go` (129) + `_ratelimit_test.go` (346) |
| `internal/services/channel_authorization_test.go` | 883 | 245 | `_helpers_test.go` (171) + `_status_test.go` (229) + `_atomic_test.go` (273) |
| `pkg/api/auth_routes_callback_test.go` | 865 | 279 | `_reauth_test.go` (268) + `_youtube_test.go` (344) |
| `internal/worker/publish_worker_publish_test.go` | 834 | 345 | `_claim_test.go` (386) + `_error_test.go` (125) |

**Nota `pkg/api/youtube_group_videos.go`:** lo split originale lo portò a 500
righe (sotto la soglia), poi è RISALITO a 570 (secondo pass di oggi: estrazione
di `resolveGroupYouTubeAccounts` + `writeGroupVideosOK` in `_helpers.go`) e ora
è a **304** — definitivamente `fatto`. I suoi file di test collegati
(`youtube_group_videos_phantom_test.go` 597, `_list_test.go` 517) restano sopra
— inclusi nelle tabelle sopra.

**Nota sezione >800:** tutti gli 8 file precedentemente sopra 800 sono stati
splitati per scenario il 2026-08-02 (commit `5132462`); la sezione è ora vuota
e il count complessivo è sceso da 69 a 59.

---

## Come aggiornare questo file

```bash
# Lista aggiornata dei file sopra 500 righe (ordinati per dimensione):
git ls-files | grep -E '\.(go|ts|tsx)$' | while IFS= read -r f; do \
  [ -f "$f" ] || continue; lines=$(wc -l < "$f"); \
  [ "$lines" -gt 500 ] && printf '%6d  %s\n' "$lines" "$f"; \
done | sort -rn

# Watchlist 450–500 (file prossimi alla soglia):
git ls-files | grep -E '\.(go|ts|tsx)$' | while IFS= read -r f; do \
  [ -f "$f" ] || continue; lines=$(wc -l < "$f"); \
  [ "$lines" -ge 450 ] && [ "$lines" -le 500 ] && printf '%6d  %s\n' "$lines" "$f"; \
done | sort -rn

# Oppure, con il report ufficiale:
./scripts/loc-report.sh -t 500 -n 20
```
