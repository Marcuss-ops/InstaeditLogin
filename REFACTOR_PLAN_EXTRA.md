# REFACTOR_PLAN_EXTRA.md — InstaeditLogin

> **Piano supplementare** per il workspace `InstaeditLogin/`.
> Complementare a `VeloxFrontend/REFACTOR_PLAN.md`.
> Stessa metodologia (commit atomici su `main`, NO branch, push immediato).

- **Repo**: `git@github.com:Marcuss-ops/InstaeditLogin`
- **Aree principali**:
  - `internal/` — Go backend core (services / repository / worker / config / bootstrap)
  - `pkg/api/` — API handlers (largest contributor dopo internal/)
  - `web/` — Next.js frontend
  - `scripts/`, `bin/`, `docs/`, `config/`, `migrations/`
- **Soglia**: ≤ 400 LOC per `.go` prod, ≤ 600 LOC per `*_test.go`
- **Scan**: `find InstaeditLogin -type f ... | sort -rn | head -30` (2026-07-28, esclusi binari)

## Top 20 file (ranked by LOC, esclusi binari)

| # | LOC | Path | Tipo | Ruolo |
|---|---:|---|---|---|
| 1 | **2616** | `pkg/api/youtube_editor_sessions_test.go` | test | YouTube editor session tests |
| 2 | 1433 | `internal/services/youtube_channel.go` | prod | YouTube channel API + OAuth + uploads |
| 3 | 1411 | `internal/worker/upload_worker.go` | prod | Upload worker |
| 4 | 1371 | `pkg/api/account_routes_test.go` | test | Account routes tests |
| 5 | 1365 | `pkg/api/admin_velox_destinations_test.go` | test | Admin destinations tests |
| 6 | 1203 | `internal/repository/post_repo.go` | prod | Posts repository |
| 7 | 1195 | `internal/services/tiktok_oauth_test.go` | test | TikTok OAuth tests |
| 8 | 1173 | `pkg/api/auth_routes_test.go` | test | Auth routes tests |
| 9 | 1147 | `pkg/api/router.go` | prod | API router (route registration) |
| 10 | 1142 | `pkg/api/drive_batch_import_test.go` | test | Drive batch import tests |
| 11 | 1126 | `internal/config/config.go` | prod | App config |
| 12 | 1116 | `internal/repository/post_repo_test.go` | test | Posts repo tests |
| 13 | 1092 | `internal/bootstrap/app.go` | prod | Bootstrap / DI wire-up |
| 14 | 1089 | `internal/repository/external_delivery_repo.go` | prod | Delivery persistence |
| 15 | 1067 | `tests/e2e/e2e_harness.go` | test | E2E harness helpers |
| 16 | 1039 | `web/src/pages/internal/DriveBatchImportDialog.tsx` | prod | Drive batch import dialog |
| 17 | 1025 | `internal/repository/upload_job_repo.go` | prod | Upload job persistence |
| 18 | 1014 | `internal/worker/drive_batch_crawler_test.go` | test | Drive batch crawler tests |

## Top production code (non-test) — focus del refactor

| # | LOC | Path | Ruolo | Rischio |
|---|---:|---|---|---|
| 1 | **1433** | `internal/services/youtube_channel.go` | YouTube channel API | 🔴 CRITICAL |
| 2 | 1411 | `internal/worker/upload_worker.go` | Upload worker | 🔴 CRITICAL |
| 3 | 1203 | `internal/repository/post_repo.go` | Posts repository | 🟡 HIGH |
| 4 | 1147 | `pkg/api/router.go` | API router | 🟡 HIGH |
| 5 | 1126 | `internal/config/config.go` | App config | 🟡 HIGH |
| 6 | 1092 | `internal/bootstrap/app.go` | DI wire-up | 🟡 HIGH |
| 7 | 1089 | `internal/repository/external_delivery_repo.go` | Delivery persistence | 🟡 HIGH |
| 8 | 1039 | `web/src/pages/internal/DriveBatchImportDialog.tsx` | UI dialog | 🟢 MEDIUM |
| 9 | 1025 | `internal/repository/upload_job_repo.go` | Upload jobs | 🟡 HIGH |

## Top test code (separate track)

| # | LOC | Path | Ruolo | Note |
|---|---:|---|---|---|
| 1 | **2616** | `pkg/api/youtube_editor_sessions_test.go` | YouTube editor session tests | ⚠️ 4-5x il limite — alto segnale |
| 2 | 1371 | `pkg/api/account_routes_test.go` | Account routes tests | |
| 3 | 1365 | `pkg/api/admin_velox_destinations_test.go` | Admin destinations tests | |
| 4 | 1195 | `internal/services/tiktok_oauth_test.go` | TikTok OAuth tests | |
| 5 | 1173 | `pkg/api/auth_routes_test.go` | Auth routes tests | |
| 6 | 1142 | `pkg/api/drive_batch_import_test.go` | Drive batch import tests | |
| 7 | 1116 | `internal/repository/post_repo_test.go` | Posts repo tests | |

## Estrazioni proposte (production code)

### `internal/services/youtube_channel.go` (1433 LOC) — 🔴 CRITICAL
Ruolo: YouTube channel API facade (Channel CRUD, OAuth, uploads entry).
Estrazioni (5 moduli, ~5 commit):
- `services/youtube_channel.go` (~250 LOC) — facade
- `services/youtube_channel_oauth.go` (~250 LOC)
- `services/youtube_channel_uploads.go` (~300 LOC)
- `services/youtube_channel_metadata.go` (~250 LOC)
- `services/youtube_channel_resolver.go` (~200 LOC)

### `internal/worker/upload_worker.go` (1411 LOC) — 🔴 CRITICAL
Ruolo: orchestratore upload (state machine, retry, multipart).
Estrazioni (5 moduli):
- `worker/upload_worker.go` (~250 LOC)
- `worker/upload_worker_state.go` (~250 LOC)
- `worker/upload_worker_retry.go` (~250 LOC)
- `worker/upload_worker_multipart.go` (~300 LOC)
- `worker/upload_worker_callbacks.go` (~250 LOC)

### `internal/repository/post_repo.go` (1203 LOC) — 🟡 HIGH
Estrazioni: 4 moduli paralleli (queries / writes / joins / facade ~250 LOC ciascuno).

### `pkg/api/router.go` (1147 LOC) — 🟡 HIGH
Ruolo: route registration monolitica.
Estrazioni:
- `pkg/api/router.go` (~150 LOC) — entrypoint
- `pkg/api/routes/auth.go` (~250 LOC) — auth routes
- `pkg/api/routes/account.go` (~250 LOC) — account routes
- `pkg/api/routes/youtube.go` (~250 LOC) — YouTube routes
- `pkg/api/routes/admin.go` (~250 LOC) — admin routes

### `internal/config/config.go` (1126 LOC) — 🟡 HIGH
Estrazioni: `config/{database,oauth,storage,workers}.go` (~150-250 LOC ciascuno) + facade.

### `internal/bootstrap/app.go` (1092 LOC) — 🟡 HIGH
Estrazioni: `bootstrap/wireup_{services,repos,workers}.go` + facade.

### `internal/repository/external_delivery_repo.go` (1089 LOC) — 🟡 HIGH
Split analogo a `post_repo.go`.

### `web/src/pages/internal/DriveBatchImportDialog.tsx` (1039 LOC) — 🟢 MEDIUM
Estrazioni: split in `<DriveBatchImportDialog>` (shell), `<DriveFilePicker>`, `<DriveImportProgress>`, `useDriveImport()`, lib per Drive API client.

### `internal/repository/upload_job_repo.go` (1025 LOC) — 🟡 HIGH
Split analogo a `post_repo.go`.

## Estrazioni test code (parallel track, soglia ≤ 600 LOC)

### `pkg/api/youtube_editor_sessions_test.go` (2616 LOC) — ⚠️ OVERSIZE 4x
Split per scenario:
- `pkg/api/youtube_editor_sessions_create_test.go` (~500 LOC)
- `pkg/api/youtube_editor_sessions_publish_test.go` (~500 LOC)
- `pkg/api/youtube_editor_sessions_thumbnails_test.go` (~400 LOC)
- `pkg/api/youtube_editor_sessions_drafts_test.go` (~400 LOC)
- `pkg/api/youtube_editor_sessions_helpers_test.go` (~500 LOC)
- + helpers in `pkg/api/youtube_editor_sessions_testutil.go` (shared)

### Test file > 1000 LOC (altri)
Stesso pattern:
- `account_routes_test.go` (1371) → split per endpoint
- `admin_velox_destinations_test.go` (1365) → split per area
- `tiktok_oauth_test.go` (1195) → split per flow OAuth
- `auth_routes_test.go` (1173) → split per handler
- `drive_batch_import_test.go` (1142) → split per scenario
- `post_repo_test.go` (1116) → split per operation

Stima: **~30 commit** solo per i test file > 1000 LOC.

## Rischi specifici
- **`upload_worker.go` + `youtube_channel.go`**: probabilmente i file con la più alta complessità ciclomatica; refactor a slice pattern è rischioso. Aggiungere test E2E prima dell'estrazione.
- **Repository split**: transazioni DB che attraversano più repo = rischio. Verificare con E2E test.
- **`pkg/api/router.go`**: ogni route module ha side-effect (registrazione); mantenere idempotenza dopo lo split.
- **Test file > 1000 LOC**: non sono "bug" ma sono sintomo di scarsa coesione. Lo split in tanti `_test.go` paralleli rischia di duplicare helper/spawn — estrai in un `<area>_testutil.go` separato.

## Suggested execution order

### Fase 1 — Test file > 1000 LOC (30 commit)
1. `pkg/api/youtube_editor_sessions_test.go` (5 commit + 1 helper file)
2. `pkg/api/account_routes_test.go` (3 commit)
3. `pkg/api/admin_velox_destinations_test.go` (3 commit)
4. `internal/services/tiktok_oauth_test.go` (3 commit)
5. `pkg/api/auth_routes_test.go` (3 commit)
6. `pkg/api/drive_batch_import_test.go` (3 commit)
7. `internal/repository/post_repo_test.go` (3 commit)

### Fase 2 — Library code (40 commit)
1. `internal/config/` (5 commit)
2. `internal/repository/post_repo` (4 commit)
3. `internal/repository/upload_job_repo` (4 commit)
4. `internal/repository/external_delivery_repo` (4 commit)
5. `internal/services/google_drive_oauth` (3 commit)
6. `internal/services/delivery_drive_destination` (3 commit)
7. `internal/services/youtube_upload` (3 commit)
8. `internal/services/youtube_channel` (5 commit)
9. `internal/bootstrap/app` (4 commit)
10. `internal/worker/upload_worker` (5 commit)

### Fase 3 — pkg/api router (4 commit)
1. `pkg/api/router.go` split in routes/{auth,account,youtube,admin}.go

### Fase 4 — Frontend (3 commit)
1. `web/.../DriveBatchImportDialog.tsx` split

Stima totale: **~77 commit** per InstaeditLogin.
