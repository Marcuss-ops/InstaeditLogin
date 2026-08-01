# Refactoring Tracker

Snapshot of tracked source files above the **500-line threshold**, with their
current size and refactoring status. Kept in sync with the tooling:

- `scripts/loc-report.sh` — informational report: top-N largest files + files
  above threshold (default `-t 500`).
- `scripts/loc-check.sh` — CI gate: fails when a tracked file **grows** past
  the threshold vs. `origin/main` (`make loc-check`; strict full-tree via
  `make loc-check LOC_AGAINST=none`).

Scan: tracked `go` / `ts` / `tsx` files only (docs, SQL, JSON, YAML excluded).
86 files are currently above 500 lines.

_Snapshot: 2026-08-01._

## Legend

| Status | Meaning |
|--------|---------|
| `da fare` | Still above 500 lines; candidate for a split (see the split-by-concern pattern used in the "Completati" section). |
| `fatto` | Refactored this session; now below the threshold (see Completati). |

---

## > 800 lines (10 files) — current `loc-check` strict offenders

These are the files that make `make loc-check LOC_AGAINST=none` exit 1 today.

| File | Righe | Stato |
|------|------:|-------|
| `pkg/api/youtube_publish_pipeline_test.go` | 1157 | da fare |
| `internal/worker/drive_batch_crawler_test.go` | 1014 | da fare |
| `pkg/api/nvidia_metadata_publish_e2e_test.go` | 928 | da fare |
| `tests/e2e/youtube_oauth_browser_e2e_test.go` | 926 | da fare |
| `pkg/api/internal_velox_get_delivery_test.go` | 923 | da fare |
| `internal/database/migrations_integration_test.go` | 901 | da fare |
| `pkg/api/internal_velox_validate_test.go` | 898 | da fare |
| `internal/services/channel_authorization_test.go` | 883 | da fare |
| `pkg/api/auth_routes_callback_test.go` | 865 | da fare |
| `internal/worker/publish_worker_publish_test.go` | 834 | da fare |

## 600–800 lines (24 files)

| File | Righe | Stato |
|------|------:|-------|
| `internal/worker/publish_reconcile_integration_test.go` | 766 | da fare |
| `internal/repository/upload_job_pool_test.go` | 765 | da fare |
| `internal/worker/reconcile_worker_test.go` | 751 | da fare |
| `internal/services/instagram_oauth_test.go` | 719 | da fare |
| `internal/services/youtube_oauth_binding_test.go` | 712 | da fare |
| `internal/deliveries/group_expand_test.go` | 711 | da fare |
| `internal/services/provider_error_test.go` | 710 | da fare |
| `pkg/api/posts_test.go` | 708 | da fare |
| `pkg/api/internal_velox_resolve_target_test.go` | 708 | da fare |
| `pkg/api/internal_velox_deliveries_test.go` | 707 | da fare |
| `internal/repository/post_repo_test.go` | 705 | da fare |
| `internal/auth/jwt_test.go` | 700 | da fare |
| `pkg/api/internal_velox_thumbnail_session_test.go` | 694 | da fare |
| `pkg/api/internal_velox_deliver_test.go` | 694 | da fare |
| `pkg/api/accounts_performance_assembler_test.go` | 683 | da fare |
| `tests/e2e/oauth_callback_binding_e2e_test.go` | 675 | da fare |
| `internal/worker/publish_worker_publish_youtube_test.go` | 666 | da fare |
| `internal/deliveries/state_test.go` | 646 | da fare |
| `pkg/api/internal_velox_callback_dispatcher_test.go` | 641 | da fare |
| `pkg/api/accounts_performance_handlers_test.go` | 639 | da fare |
| `cmd/batch-import-drive-folder/main_test.go` | 629 | da fare |
| `tests/e2e/validate_account_e2e_test.go` | 628 | da fare |
| `web/src/pages/internal/ContentPublish.tsx` | 622 | da fare |
| `internal/testutil/runtime/runtime_test.go` | 609 | da fare |

## 500–600 lines (51 files)

| File | Righe | Stato |
|------|------:|-------|
| `internal/worker/mocks_test.go` | 608 | da fare |
| `internal/auth/jwt.go` | 605 | da fare |
| `pkg/api/uploads_handlers.go` | 604 | da fare |
| `tests/e2e/e2e_harness_fakes.go` | 601 | da fare |
| `web/src/components/booking/BookingModal.tsx` | 600 | da fare |
| `pkg/api/youtube_group_videos_phantom_test.go` | 597 | da fare |
| `pkg/api/admin_channels_handlers.go` | 592 | da fare |
| `pkg/api/internal_velox_e2e_helpers_test.go` | 590 | da fare |
| `pkg/api/account_routes_test.go` | 589 | da fare |
| `pkg/api/posts_handlers.go` | 583 | da fare |
| `internal/models/external_delivery_test.go` | 578 | da fare |
| `pkg/api/internal_velox_callback_dispatcher.go` | 577 | da fare |
| `web/src/pages/internal/DriveBatchImportDialogViews.tsx` | 575 | da fare |
| `internal/services/youtube_oauth.go` | 575 | da fare |
| `pkg/api/common_test_mocks_test.go` | 573 | da fare |
| `internal/outbox/dispatcher.go` | 571 | da fare |
| `internal/providers/registry_test.go` | 553 | da fare |
| `pkg/api/youtube_editor_sessions_by_project.go` | 551 | da fare |
| `pkg/api/youtube_editor_sessions.go` | 551 | da fare |
| `internal/models/external_delivery.go` | 549 | da fare |
| `internal/worker/process_ingest_verify_integration_test.go` | 547 | da fare |
| `web/src/pages/internal/ScheduledByAccount.tsx` | 546 | da fare |
| `pkg/api/accounts_write_handlers.go` | 544 | da fare |
| `tests/e2e/e2e_harness_helpers.go` | 542 | da fare |
| `pkg/api/groups_handlers.go` | 541 | da fare |
| `pkg/api/drive_batch_uploads_test.go` | 541 | da fare |
| `internal/services/provider_error.go` | 540 | da fare |
| `cmd/test-youtube-upload/main.go` | 540 | da fare |
| `internal/deliveries/group_expand.go` | 537 | da fare |
| `web/src/pages/internal/GroupYouTubeVideos.tsx` | 536 | da fare |
| `pkg/api/uploads_batch_handlers.go` | 536 | da fare |
| `pkg/api/router.go` | 535 | da fare |
| `internal/repository/platform_account_repo.go` | 534 | da fare |
| `internal/repository/group_repo.go` | 531 | da fare |
| `internal/services/threads_oauth.go` | 528 | da fare |
| `internal/config/config.go` | 528 | da fare |
| `cmd/batch-import-drive-folder/main.go` | 527 | da fare |
| `internal/services/facebook_oauth_test.go` | 526 | da fare |
| `pkg/api/youtube_group_videos_list_test.go` | 517 | da fare |
| `internal/services/youtube_oauth_validate_test.go` | 515 | da fare |
| `internal/services/http_client_test.go` | 513 | da fare |
| `web/src/pages/Programs.tsx` | 512 | da fare |
| `web/src/features/publishing/wizard/ConfirmationStep.tsx` | 510 | da fare |
| `pkg/api/youtube_editor_sessions_publish_inflight_test.go` | 509 | da fare |
| `pkg/api/internal_velox_e2e_test.go` | 507 | da fare |
| `internal/repository/post_repo_post.go` | 507 | da fare |
| `web/src/pages/internal/Compose.tsx` | 506 | da fare |
| `web/src/features/publishing/wizard/ChannelMetadataStep.tsx` | 506 | da fare |
| `internal/database/multi_tenancy_test.go` | 504 | da fare |
| `scripts/distribute_channels_to_managers/main_test.go` | 502 | da fare |
| `internal/repository/delivery_session_repo.go` | 502 | da fare |

---

## Completati in questa sessione (stato: `fatto`)

Files split below the threshold during this session's refactoring campaign
(commit range `802d31f` → `afee37f`). All follow the split-by-concern pattern:
extract helpers/types into `*_helpers` / `*_types` files, then split tests per
feature/scenario.

| File | Prima | Dopo | Split in |
|------|------:|-----:|----------|
| `internal/services/tiktok_publish_test.go` | 937 | 491 | `tiktok_publish_test.go` + `tiktok_publish_mock_test.go` (197) + `tiktok_publish_pullfromfile_test.go` (280) |
| `pkg/api/drive_batch_import_test.go` | 1142 | 159 | `drive_batch_import_test.go` + `_errors_test.go` (139) + `_pagination_test.go` (246) + `_shareddrive_test.go` (151) + `_idempotency_test.go` (340) + `_e2e_test.go` (177) |
| `tests/e2e/pipeline_e2e_test.go` | 954 | 104 | per scenario + helper in `e2e_harness` |
| `internal/outbox/dispatcher_test.go` | 976 | 174 | `dispatcher_{dispatch,retry,errors,concurrency}_test.go` |
| `docs/OAUTH-PRODUCTION.md` | 979 | 321 | `oauth-google-{setup,limits,rollout,monitoring,troubleshooting}.md` |
| `docs/OPERATIONS.md` | 827 | 115 | `operations-{deploy,monitoring,runbook,email}.md` |
| `pkg/api/common_test.go` | 892 | 83 | `common_test_helpers_test.go` + `common_test_mocks_test.go` |
| `pkg/api/youtube_group_videos.go` | 767 | 500 | `youtube_group_videos_{types,helpers,cache,fetch}.go` |
| `web/src/pages/internal/YouTubeStudio.tsx` | 709 | 149 | `useYouTubeStudio{Data,Actions,PrivateVideos}.ts` + sezioni |
| `web/src/components/booking/BookingProvider.tsx` | 694 | 8 | context + `BookingModal.tsx` + moduli dedicati |
| `web/src/pages/internal/ContentPublish.test.tsx` | 676 | — (eliminato) | `ContentPublish.{retryGating,retryFlow,states,crossTab}.test.tsx` + `testUtils.tsx` |
| `pkg/api/modules.go` | 649 | 40 | un file per modulo |

**Nota `pkg/api/youtube_group_videos.go`:** dopo lo split è esattamente a 500
righe (sotto la soglia `>500`), ma il suo set di file collegati
(`youtube_group_videos_phantom_test.go` 597, `_list_test.go` 517) resta sopra —
inclusi nelle tabelle sopra.

---

## Come aggiornare questo file

```bash
# Lista aggiornata dei file sopra 500 righe (ordinati per dimensione):
git ls-files | grep -E '\.(go|ts|tsx)$' | while IFS= read -r f; do \
  [ -f "$f" ] || continue; lines=$(wc -l < "$f"); \
  [ "$lines" -gt 500 ] && printf '%6d  %s\n' "$lines" "$f"; \
done | sort -rn

# Oppure, con il report ufficiale:
./scripts/loc-report.sh -t 500 -n 20
```
