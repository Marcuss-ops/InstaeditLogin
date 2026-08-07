# Audit InstaEditor autonomo

**Data:** 2026-08-03  
**Repository:** `InstaeditLogin`  
**Branch verificato:** `main`  
**Commit di partenza:** `2c7fb0b` (`test(db): cover thumbnail restore migration contract`)  
**Tipo di intervento:** audit documentale read-only; nessuna modifica funzionale.

> **Nota di rebranding (2026):** il nome commerciale della superficie descritta in questo audit è oggi **InstaEditor**. Il filename storico `AUDIT_DARK_EDITOR.md` e gli identificatori tecnici o di compatibilità (per esempio path ed env legacy) sono mantenuti intenzionalmente.

## 1. Perimetro e risultato sintetico

L’audit ha coperto:

- frontend React/Vite e punti di ingresso al InstaEditor;
- API HTTP, middleware e registrazione delle route;
- modelli, repository e wiring dei servizi;
- migrazioni e vincoli PostgreSQL;
- Media Library, upload e object storage;
- autosave esistente;
- renderer/job pipeline;
- test unitari, di integrazione ed E2E.

### Risultato principale

Il repository contiene già una **base backend significativa per il modello autonomo** `ThumbnailProjectModule`, ma il InstaEditor utilizzabile dall’utente resta ancora principalmente il flusso YouTube-specifico basato su `youtube_video_edits`.

In particolare:

- il database ha già progetti, revisioni, asset references, export e assignments;
- modelli Go, repository per progetti/revisioni e API CRUD/snapshot/restore sono presenti;
- il progetto autonomo non richiede account YouTube, video o OAuth nel proprio schema e nei propri endpoint di progetto;
- il frontend non espone ancora una libreria Copertine né un editor autonomo collegato a questi endpoint;
- non risultano implementati il renderer canonico thumbnail, la persistenza applicativa degli export e gli endpoint operativi di render/assignment;
- l’autosave esistente salva metadati YouTube (`draft_*`), non lo snapshot completo del canvas;
- il flusso `CreateEditorSession` rimane vincolato a workspace, account YouTube, collegamento canale, token OAuth e video remoto.

## 2. Stato Git e vincoli osservati

Il controllo iniziale ha rilevato:

```text
main...origin/main
 M web/src/index.css
 M web/src/pages/landing/Features.tsx
```

Questi due file erano già modificati e **non sono stati toccati** dall’audit. Il report è stato creato come file separato.

Remote verificato:

```text
origin git@github.com:Marcuss-ops/InstaeditLogin
```

Regola operativa adottata per questo lavoro:

- solo `main`;
- nessun branch secondario;
- nessuna modifica funzionale durante l’audit;
- il commit deve contenere esclusivamente questo report;
- i file già modificati dall’utente devono rimanere fuori dal commit.

## 3. Mappa del dominio attuale

### 3.1 Flusso YouTube-specifico esistente

| Area | File principali | Evidenza |
|---|---|---|
| Modello | `internal/models/youtube_video_edit.go` | `YouTubeVideoEdit` contiene `workspace_id`, `platform_account_id`, `youtube_video_id`, `velox_project_id`, `thumbnail_media_id` e i campi `Draft*`. |
| Schema | `internal/database/migrations/065_youtube_video_edits.sql` | `youtube_video_edits` richiede `workspace_id`, `platform_account_id` e `youtube_video_id`. |
| Creazione sessione | `pkg/api/youtube_editor_sessions.go` | `CreateEditorSession` verifica workspace, account YouTube, link workspace-channel, token e servizio YouTube. |
| Validazione remota | `pkg/api/youtube_editor_sessions.go` | viene chiamato `GetYouTubeVideo`; il video deve appartenere al canale, essere processed e non public. |
| Frontend API | `web/src/features/youtube/api/editorSessionsApi.ts` | il POST richiede `workspace_id`, `platform_account_id`, `youtube_video_id`; la risposta apre `editor_url`. |
| Entry point UI | `web/src/pages/internal/DashboardChannels.tsx`, `AccountDetails.tsx`, `useYouTubeStudioActions.ts` | il pulsante “Modifica copertina” nasce da un video/canale e crea una sessione YouTube. |
| Autosave | `pkg/api/youtube_editor_sessions_draft.go`, `internal/repository/youtube_video_edit_sessions.go` | salva titolo, descrizione, tag, lingue, traduzioni, privacy e `draft_*`; non il canvas grafico. |

**Conclusione:** questo percorso continua a rappresentare una sessione di editing legata a un video YouTube. Non deve essere reso nullable per assorbire il nuovo caso autonomo.

### 3.2 Modulo autonomo già introdotto

| Area | File principali | Evidenza |
|---|---|---|
| Modello | `internal/models/thumbnail_project.go` | `ThumbnailProject`, `ThumbnailProjectRevision`, `ThumbnailProjectAsset`, `ThumbnailExport`, `ThumbnailAssignment`. |
| Migrazione base | `internal/database/migrations/094_thumbnail_projects.sql` | cinque tabelle con FK, check, indici e vincoli di isolamento. |
| Migrazione restore | `internal/database/migrations/097_thumbnail_project_restore.sql` | adegua il vincolo hash per consentire restore come nuova revisione. |
| Repository | `internal/repository/thumbnail_project_repo.go` | create/list/find/update CAS, snapshot, hash SHA-256, revisioni, restore, archive/delete soft. |
| API types | `pkg/api/thumbnail_projects_types.go` | interfaccia indipendente `ThumbnailProjectStore`, request/response per progetto, snapshot e restore. |
| Handler | `pkg/api/thumbnail_projects_handlers.go` | create, list, get, update, snapshot, list/get revision, restore, archive e delete. |
| Route module | `pkg/api/modules_thumbnail_projects.go` | route protette `/api/v1/thumbnail-projects...`. |
| Wiring | `internal/bootstrap/database_storage_wiring.go`, `internal/bootstrap/app.go`, `internal/bootstrap/router_wiring.go`, `pkg/api/router_options.go` | repository e store sono cablati nell’applicazione. |

## 4. Database e persistenza

### 4.1 Tabelle già presenti

`internal/database/migrations/094_thumbnail_projects.sql` definisce:

- `thumbnail_projects`: aggregate del progetto grafico, workspace-scoped;
- `thumbnail_project_revisions`: snapshot immutabili con `schema_version`, `snapshot_json`, `snapshot_sha256` e `renderer_version`;
- `thumbnail_project_assets`: riferimenti a `media_assets` con ruoli `background`, `foreground`, `logo`, `overlay`, `reference`, `font`;
- `thumbnail_exports`: metadati del file renderizzato, media id, dimensioni, hash, renderer e status;
- `thumbnail_assignments`: destinazioni YouTube opzionali per un export.

### 4.2 Vincoli positivi rilevati

Sono già presenti vincoli importanti per il modello autonomo:

- `thumbnail_projects` non contiene account YouTube, video, token o OAuth;
- workspace e creator sono obbligatori;
- dimensioni canvas limitate e positive;
- `version >= 1` per concorrenza ottimistica;
- snapshot JSON obbligatoriamente object e hash di 32 byte;
- unicità di `(project_id, revision_number)`;
- FK composite per impedire puntatori cross-project;
- FK composite per impedire assignment verso un export di un altro progetto;
- FK workspace/account e discriminator `platform = 'youtube'`;
- vincolo unico `(export_id, platform_account_id, youtube_video_id)`;
- soft delete del progetto invece di distruzione immediata della cronologia.

### 4.3 Gap database/applicativo

La struttura dati è più avanti dell’uso applicativo:

- `thumbnail_project_assets` ha schema e modello, ma non risultano repository/API operativi per aggiungere, rimuovere o elencare gli asset del progetto;
- `thumbnail_exports` ha schema e modello, ma non risultano metodi repository per creare, aggiornare o leggere export renderizzati;
- `thumbnail_assignments` ha schema e modello, ma non risultano handler/repository per creare, leggere, aggiornare o applicare assignment;
- i riferimenti `preview_media_id` e `latest_export_id` sono presenti a livello DB/modello, ma non è stato trovato un flusso completo che li aggiorni tramite render reale;
- i vincoli database coprono la coerenza relazionale, ma non certificano da soli l’esistenza del file nell’object storage o la corrispondenza hash/pixel tra preview ed export.

## 5. API attuali

### 5.1 Endpoint autonomi già presenti

Il modulo registra route protette:

```text
POST   /api/v1/thumbnail-projects
GET    /api/v1/thumbnail-projects
GET    /api/v1/thumbnail-projects/{id}
PATCH  /api/v1/thumbnail-projects/{id}
PUT    /api/v1/thumbnail-projects/{id}/snapshot
GET    /api/v1/thumbnail-projects/{id}/revisions
GET    /api/v1/thumbnail-projects/{id}/revisions/{revision_id}
POST   /api/v1/thumbnail-projects/{id}/restore/{revision_id}
POST   /api/v1/thumbnail-projects/{id}/archive
DELETE /api/v1/thumbnail-projects/{id}
```

Caratteristiche osservate:

- accesso workspace verificato dall’handler;
- progetto creato con solo workspace, nome e dimensioni canvas;
- snapshot con `base_version` o `If-Match`;
- conflitto mappato a `409` e codice `PROJECT_VERSION_CONFLICT`;
- restore crea una nuova revisione;
- DELETE è lifecycle-safe/soft delete.

### 5.2 Endpoint autonomi assenti o non conclusi

Non risultano route equivalenti a:

```text
POST /api/v1/thumbnail-projects/{id}/render
GET  /api/v1/thumbnail-exports/{export_id}
POST /api/v1/thumbnail-exports/{export_id}/assignments
```

Non risulta quindi completato il percorso API:

```text
snapshot persistito → renderer canonico → media asset ready → thumbnail_export → assignment
```

### 5.3 API YouTube ancora dominante

Le API frontend/server per aprire il InstaEditor sono ancora centrate su:

```text
POST /api/v1/youtube/editor-sessions
GET  /api/v1/youtube/editor-sessions...
POST /api/v1/youtube/editor-sessions/{id}/thumbnail
PUT  /api/v1/youtube/editor-sessions/by-project/{velox_project_id}/draft
POST /api/v1/youtube/editor-sessions/{id}/publish
```

Queste API servono la sessione YouTube e il publish, non il progetto grafico autonomo.

## 6. Frontend

### 6.1 Cosa esiste

File rilevanti:

- `web/src/features/youtube/api/editorSessionsApi.ts` — client canonical per sessioni YouTube e apertura `editor_url`;
- `web/src/features/youtube/hooks/useCreateYouTubeEditorSession.ts` — mutation hook del POST YouTube;
- `web/src/pages/internal/DashboardChannels.tsx` — entry point da video di canale;
- `web/src/pages/internal/AccountDetails.tsx` — entry point da account/video;
- `web/src/pages/internal/YouTubeStudioSessionRow.tsx` e `YouTubePublishCard.tsx` — visualizzazione sessione/editor URL;
- `web/src/pages/internal/useMediaLibrary.ts` — picker di asset pronti per il wizard livestream;
- `web/src/features/publishing/api/mediaApi.ts` e `useUploadMedia.ts` — upload presign → PUT → complete;
- `web/src/pages/Editor.tsx` e `web/src/components/editor/*` — pagina marketing “One raw idea. Every platform.”, non il canvas persistente del InstaEditor.

### 6.2 Assenze frontend rilevate

Non risultano nel frontend applicativo:

- route/pagina per libreria `Copertine`;
- lista filtrabile `Tutte / Bozze / Pronte / Collegate / Archiviate`;
- CTA “Crea nuova copertina” senza prerequisiti YouTube;
- form iniziale per nome, formato, dimensione e sfondo;
- client API TypeScript per `/api/v1/thumbnail-projects`;
- stato React del canvas grafico persistibile;
- debounce autosave dello snapshot;
- `flushPendingAutosave()` prima di chiusura, preview, export o assignment;
- UI “Salvataggio… / Salvato alle… / Modifiche non salvate / Errore di salvataggio” per il canvas;
- gestione UI del `409 PROJECT_VERSION_CONFLICT` con “Ricarica versione recente” e “Salva come copia”;
- UI preview/export autonomo;
- selettore di assignment Gruppo → Canale → Video → Lingua → Preview → Conferma;
- recupero autonomo da progetto/revisione/asset dopo reload o cambio browser.

### 6.3 Autosave: distinzione necessaria

L’autosave esistente in `youtube_video_edits` è utile per i dati editoriali YouTube, ma non soddisfa il requisito canvas:

```text
Titolo / descrizione / tag / lingue / traduzioni / privacy
≠
Snapshot grafico con canvas / oggetti / coordinate / livelli / trasformazioni / asset
```

Il fatto che esista un endpoint `/draft` non certifica il salvataggio della copertina grafica.

## 7. Media Library e storage

### 7.1 Componenti già disponibili

Il flusso media esistente è maturo per asset uploadati:

```text
POST /api/v1/media/presign
→ PUT diretto su storage S3-compatible/MinIO
→ POST /api/v1/media/{id}/complete
→ media_assets.status = ready
```

File principali:

- `internal/database/migrations/006_media_assets.sql`;
- `internal/models/asset.go`;
- `internal/repository/asset_repo.go`;
- `pkg/api/media_handlers.go`;
- `pkg/api/media_library_handlers.go`;
- `pkg/api/modules_media.go`;
- `web/src/features/publishing/api/mediaApi.ts`;
- `web/src/features/publishing/hooks/useUploadMedia.ts`;
- `web/src/pages/internal/useMediaLibrary.ts`.

`GET /api/v1/media` restituisce asset ready dell’utente, metadata ffprobe e URL presigned di preview. Il server conserva `media_assets` come fonte di verità e usa URL freschi per lo storage.

### 7.2 Gap rispetto al progetto grafico

Non è ancora dimostrato il collegamento completo tra il canvas e la Media Library:

- il client della Media Library è usato da wizard/live flow, non da un autonomo thumbnail editor;
- non risulta un’API per registrare `thumbnail_project_assets` quando un’immagine viene inserita nel canvas;
- non risulta un resolver che, in apertura progetto, carichi tutte le immagini/font referenziate nello snapshot tramite `media_id`;
- non risulta un controllo applicativo completo per impedire asset cross-workspace durante il salvataggio del progetto;
- non risulta un flusso che garantisca asset/font disponibili dopo cache clear, nuovo browser o riavvio servizi;
- la persistenza del file renderizzato è prevista nello schema, ma non è ancora collegata a un renderer thumbnail e a `media_assets` ready.

## 8. Renderer e pipeline

### 8.1 Pipeline presenti ma diverse

Il repository contiene pipeline Velox/render per job generici e delivery, tra cui:

- `internal/veloxjobs/*`;
- `internal/veloxcontract/*`;
- `pkg/api/velox/*`;
- `internal/worker/*` per ingest, probe, publish e callback;
- test E2E del percorso Drive → storage → publish → Velox callback.

Questi componenti non equivalgono a un renderer canonico per snapshot thumbnail.

### 8.2 Renderer thumbnail autonomo non trovato

Non risultano componenti applicativi con responsabilità completa di:

1. leggere `thumbnail_project_revisions.snapshot_json`;
2. applicare il renderer version corretto;
3. produrre PNG/JPEG deterministico;
4. caricare il file nello storage tramite Media Library;
5. verificare dimensioni, file size e SHA-256;
6. creare/aggiornare `thumbnail_exports`;
7. collegare preview ed export alla revisione esatta.

Il modello `ThumbnailExport` e la tabella esistono, ma il comportamento operativo di render/export non è ancora presente nel percorso API/worker individuato.

## 9. Test esistenti

### 9.1 Copertura positiva già presente

Backend:

- `internal/database/migrations_thumbnail_projects_test.go` — presenza tabelle, defaults, indici, FK, cross-project e cross-workspace constraints;
- `internal/repository/thumbnail_project_repo_test.go` — create/update/status e query repository con sqlmock;
- `internal/repository/thumbnail_project_snapshot_test.go` — snapshot, hash, conflict e restore;
- `internal/repository/thumbnail_project_integration_test.go` — comportamento PostgreSQL reale per revisioni/versioni;
- `pkg/api/thumbnail_projects_handlers_test.go` — create/update/snapshot/list revision/restore/delete e conflitto `If-Match`.

YouTube/media:

- numerosi test su editor session, attach thumbnail, publish, CAS, metadata draft e Media Library;
- `pkg/api/media_test.go` e `pkg/api/media_library_test.go` — presign/complete/list e URL preview;
- test frontend per `editorSessionsApi`, upload media e hook upload.

### 9.2 Test mancanti rispetto alla certificazione richiesta

Non risultano test completi per:

- creazione, apertura e modifica di un progetto autonomo dal frontend;
- autosave canvas con debounce e flush prima di Export;
- preview ed export derivati dallo snapshot persistito;
- uguaglianza `snapshot_sha256`, `renderer_version`, dimensioni e bytes preview/export;
- asset/font ripristinati da server in un browser nuovo;
- restart completo API/worker/MinIO/PostgreSQL con progetto ancora apribile;
- cancellazione oggetto seguita da save/reload senza oggetto fantasma;
- trasformazioni `x`, `y`, `rotation`, `scaleX`, `scaleY` conservate nel render;
- conflict reale tra due tab tramite `409` e scelta utente;
- assignment successivo senza modifica del progetto originale;
- cross-workspace rejection per asset, project, export e assignment tramite API completa;
- backup/restore operativo di progetto, revisioni, export e asset;
- query di controllo asset orfani in una pipeline reale.

Gli E2E presenti coprono soprattutto pipeline YouTube/Velox/OAuth, non la Definition of Done del InstaEditor autonomo.

## 10. Matrice di conformità iniziale

| Requisito | Stato audit | Evidenza / nota |
|---|---:|---|
| Progetto senza canale | **Parziale** | DB/repository/API create lo supportano; manca UI e percorso E2E. |
| Schema separato da `youtube_video_edits` | **PASS** | `094_thumbnail_projects.sql` separa il dominio. |
| Salvataggio canvas completo | **Parziale** | snapshot API/repository presenti; nessun editor frontend collegato. |
| Revisioni immutabili | **Parziale** | persistenza e restore presenti; manca certificazione UX/E2E completa. |
| Hash SHA-256 snapshot | **PASS backend** | canonicalizzazione + hash nel repository. |
| Version/ETag/409 | **PASS backend parziale** | CAS e mapping handler presenti; manca UI multi-tab. |
| Autosave canvas con indicatore reale | **FAIL** | esiste solo autosave metadata YouTube. |
| Flush prima delle operazioni importanti | **FAIL** | non trovato nel frontend autonomo. |
| Preview canonica | **FAIL** | manca renderer/API preview autonomi. |
| Export persistente e verificabile | **FAIL** | schema presente, pipeline operativa assente. |
| Asset riferiti tramite media id | **Parziale** | schema/modello presenti; API e integrazione canvas assenti. |
| Asset/font persistenti | **Parziale** | storage media esistente; nessuna apertura autonoma verificata. |
| Libreria Copertine | **FAIL** | nessuna pagina/client/UI autonomi individuati. |
| Assignment successivo YouTube | **FAIL** | schema presente, API/repository/apply flow non individuati. |
| Progetto originale invariato dopo assignment | **Non verificato** | manca endpoint e test assignment. |
| Cross-workspace bloccato | **Parziale** | vincoli DB e workspace guard per progetti presenti; asset/export API mancanti. |
| Backup e restore | **Non verificato** | presenti test di migration/revision restore, non backup operativo. |
| Nessuna dipendenza OAuth per save | **PASS backend progetto** | route progetto usa workspace auth, non `CreateEditorSession`; UX non esposta. |
| Certificazione dopo restart servizi | **Non verificato** | manca scenario E2E autonomo. |

## 11. Rischi tecnici principali

1. **Confusione di aggregate:** estendere `youtube_video_edits` per coprire progetti liberi reintrodurrebbe nello stesso record editing, publish e destinazione.
2. **Falso “salvato”:** il draft metadata YouTube può sembrare autosave della copertina, ma non conserva il canvas.
3. **Preview stale:** senza flush server-side prima del render, preview/export possono derivare da una revisione precedente.
4. **Hash non sufficiente:** hash dello snapshot e hash del file sono controlli diversi; entrambi devono essere persistiti e verificati.
5. **Asset locali non autorevoli:** localStorage/IndexedDB non possono sostituire `media_assets` e snapshot server.
6. **Export senza renderer canonico:** un secondo canvas browser-only potrebbe divergere dal file finale.
7. **Assignment prematuro:** collegare direttamente il progetto a YouTube anziché un export rende ambiguo quale revisione/file è stato applicato.
8. **Wiring incompleto:** tabelle e modelli già presenti possono dare l’impressione di feature completata anche quando mancano handler, worker e UI.

## 12. Inventario dei file di riferimento

### Backend autonomo

```text
internal/database/migrations/094_thumbnail_projects.sql
internal/database/migrations/097_thumbnail_project_restore.sql
internal/database/migrations_thumbnail_projects_test.go
internal/models/thumbnail_project.go
internal/repository/thumbnail_project_repo.go
internal/repository/thumbnail_project_repo_test.go
internal/repository/thumbnail_project_snapshot_test.go
internal/repository/thumbnail_project_integration_test.go
pkg/api/modules_thumbnail_projects.go
pkg/api/thumbnail_projects_handlers.go
pkg/api/thumbnail_projects_handlers_test.go
pkg/api/thumbnail_projects_types.go
pkg/api/routes.go
pkg/api/router_options.go
internal/bootstrap/app.go
internal/bootstrap/database_storage_wiring.go
internal/bootstrap/router_wiring.go
```

### YouTube editor/session

```text
internal/database/migrations/065_youtube_video_edits.sql
internal/models/youtube_video_edit.go
internal/repository/youtube_video_edit_repo.go
internal/repository/youtube_video_edit_sessions.go
pkg/api/youtube_editor_sessions.go
pkg/api/youtube_editor_sessions_types.go
pkg/api/youtube_editor_sessions_draft.go
pkg/api/youtube_editor_sessions_thumbnail.go
pkg/api/youtube_editor_sessions_by_project.go
web/src/features/youtube/api/editorSessionsApi.ts
web/src/features/youtube/hooks/useCreateYouTubeEditorSession.ts
web/src/pages/internal/DashboardChannels.tsx
web/src/pages/internal/AccountDetails.tsx
```

### Media/storage

```text
internal/database/migrations/006_media_assets.sql
internal/models/asset.go
internal/repository/asset_repo.go
pkg/api/media_handlers.go
pkg/api/media_library_handlers.go
pkg/api/modules_media.go
web/src/features/publishing/api/mediaApi.ts
web/src/features/publishing/hooks/useUploadMedia.ts
web/src/pages/internal/useMediaLibrary.ts
internal/services/storage.go
internal/services/media_resolver.go
```

## 13. Conclusione dell’audit

Il repository è in una fase intermedia coerente con una separazione architetturale già iniziata:

> **Il modello autonomo esiste nel backend, ma il prodotto InstaEditor autonomo non è ancora certificabile end-to-end.**

La parte già implementata va preservata come modulo separato. Il flusso YouTube-specifico deve continuare a usare `youtube_video_edits` per sessione/publish, mentre il nuovo editor deve usare `thumbnail_projects` e le sue revisioni. La presenza di tabelle e repository non deve essere considerata Definition of Done finché frontend, renderer, export, assignment e scenari di persistenza/restart/browser non sono coperti da test pratici.
