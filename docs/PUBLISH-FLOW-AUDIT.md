# Audit del vertical slice "Publish Flow" (2026-07-30)

> Mappatura del codice già esistente, dei riusi concreti e dei **gap**
> ancora da chiudere per realizzare il flusso:
>
> `/content/new` → upload → POST /posts (private) → pagina canale →
> "Modifica copertina" → InstaEditor → nuova thumbnail + nuova
> privacy → verifica su pagina canale + YouTube Studio.

Questo documento è il punto di partenza per i commit successivi del
vertical slice. Va aggiornato quando un riuso cambia o quando un gap
viene chiuso.

---

## TL;DR — naming mismatch (decisione da prendere)

Il piano fa riferimento a path e componenti che nel codice reale
**non esistono con quei nomi**:

| Piano                           | Realtà nel codice                                  | Note |
| ------------------------------- | -------------------------------------------------- | ---- |
| `/content/new`                  | `/app/compose` (`InternalCompose`)                 | la logica upload + POST /posts è già qui |
| `/content/:postId/publish`      | **non esiste**                                     | `Posts.tsx` è solo una lista, non c'è pagina-stato |
| `/dashboard-channels/:paId`     | `/app/accounts/:accountId` (`AccountDetailsPage`)  | ha già tabs + Modifica copertina + Open on YouTube |
| `NewContentWizard`              | **non esiste** — sostituibile da `InternalCompose` | refactor possibile |
| `PublishView`                   | **non esiste** — da creare                         | sarà la pagina-stato async |
| `GroupsView`                    | `/app/groups` (`GroupsPage`)                       | NON contiene il video grid, è solo folder tree |
| `useYouTubePublishLiveUpdate`   | **non esiste**                                     | da creare (cross-tab) |
| `createYouTubeEditorSession`    | **inline in 3 file** (non estratto come client)    | AccountDetails.tsx, YouTubeStudio.tsx, Calendar.tsx |
| `groupYouTubeVideosQueryKey`    | **non esiste**                                     | la query key è in AccountDetails |

> Decisione da prendere all'inizio dell'implementazione:
>
> **A)** riusare `/app/compose` + `/app/accounts/:accountId` (minor
> cost, lavora sull'esistente, nessuna migrazione di route);
>
> **B)** rinominare/aggiungere i path nuovi `/content/*` e
> `/dashboard-channels/:id` come da piano (richiede redirect delle
> route esistenti e client-side redirect nel nav).
>
> L'audit qui documenta lo stato attuale e funziona per entrambe le
> scelte. Per i primi commit si assume **(A)** come default, riutilizzo
> dei path esistenti.

---

## Backend — endpoint già pronti (nessun lavoro necessario)

Tutti i contratti lato server sono **già implementati e testati**:

| Metodo / path                                     | Handler                                    | Note riuso |
| ------------------------------------------------- | ------------------------------------------ | ---------- |
| `POST /api/v1/media/presign`                      | `handlePresignMedia` (media_handlers.go)   | Restituisce `{asset_id, upload_url, upload_method, upload_headers, expires_at}`. Accetta SHA256 + publish_at opzionali. |
| `POST /api/v1/media/{id}/complete`                | `handleCompleteMedia` (media_handlers.go)  | HEAD sull'S3, transizione `pending → ready`, validazione size + content-type. |
| `POST /api/v1/posts`                              | `handleCreatePost` (posts_handlers.go)     | **Contratto Taglio 3.2**: `media: [{asset_id}]` (NON più `media_url`). Workspace ownership check. **Restituisce 201 Created**, non 202 (piano dice 202 — gestire come success). |
| `POST /api/v1/posts/{id}/publish`                 | `handlePublishPostID`                      | Transizione post + targets a "publishing". |
| `POST /api/v1/posts/{id}/retry`                   | `handleRetryPost`                          | Per re-armare un failed post intero. |
| `POST /api/v1/posts/{id}/cancel`                  | `handleCancelPost`                         | Queue → draft. |
| `POST /api/v1/posts/{id}/targets`                 | `handleAddTarget`                          | Aggiunge un target a post esistente. |
| `PATCH /api/v1/posts/{id}`                        | `handlePatchPost`                          | Edit title/caption/media/status. |
| `GET /api/v1/posts/{id}`                          | `handleGetPost`                            | Single post con isolation cross-tenant. |
| `GET /api/v1/posts/workspace/{wid}`               | `handleListByWorkspace`                     | Lista per workspace. |
| `GET /api/v1/posts`                               | `handleListPosts`                          | Lista globale utente, con `?workspace_id` opzionale. |
| `POST /api/v1/posts/{id}/schedule`                | `handleSchedulePost`                       | publish_at canonico, scheduled_at alias legacy. |
| `POST /api/v1/post-targets/{id}/retry`            | `handleRetryTarget`                        | Per re-armare un singolo target failed. **OK per "Riprova pubblicazione"**. |
| `GET /api/v1/posts/{id}/targets`                  | `handleGetPostTargets`                     | ⚠ **GAP**: implementazione attualmente restituisce `{targets: []}` perché `postStore.ListByPost` non è ancora wirato. Vedi "Gap" sotto. |
| `POST /api/v1/youtube/editor-sessions`            | `handleCreateYouTubeEditorSession` (youtube_editor_sessions.go) | Click-idempotente (FindOrCreate). Helper condiviso con worker. Restituisce `{session_id, velox_project_id, editor_url}`. |
| `GET /api/v1/youtube/editor-sessions/by-project/{vp_id}` | youtube_editor_sessions_by_project.go | Session lookup by velox_project_id. |
| `POST /api/v1/youtube/editor-sessions/by-project/{vp_id}/publish` | idem | Publish del InstaEditor. Response con `actual_privacy` e `youtube_sync_status` (P0#7). |
| `PATCH /api/v1/youtube/editor-sessions/by-project/{vp_id}` | handleUpdateYouTubeEditorSession | Upload thumbnail asset a session (post-InstaEditor). |
| `GET /api/v1/youtube/editor-sessions`            | listEditorSessionsHandler                  | Lista "code da modificare" (già usato in YouTubeStudio). |
| `POST /api/v1/youtube/editor-sessions/{id}/thumbnail` | youtube_editor_sessions.go             | Attach thumbnail asset a session. |
| `POST /api/v1/youtube/editor-sessions/{id}/publish` | publishEditorSessionHandler              | Publish by session_id (alternativa a by-project). |
| `GET /api/v1/accounts/{id}/content`               | `handleAccountContent` (accounts_read_handlers.go) | ✅ Filtro `?privacy=private|unlisted|public` validato server-side prima della query YouTube (vedi `account_routes_test.go:1068`). `?limit=` e `?cursor=` supportati. **Esatto contratto del piano.** |
| `GET /api/v1/accounts/{id}`                       | `handleGetAccount`                         | Detail completo canale (avatar, banner, metrics). |
| `POST /api/v1/accounts/{id}/sync`                 | già wirato                                 | Refresh da YouTube (usato da AccountDetails). |
| `POST /api/v1/accounts/{id}/validate`             | già wirato                                 | Test token. |
| `POST /api/v1/accounts/{id}/reconnect`            | già wirato                                 | OAuth reauth. |
| `DELETE /api/v1/accounts/{id}`                    | già wirato                                 | Disconnect. |

> **Idempotency-Key** è supportato nativamente da `handleCreatePost`
> (migration 021, livello 1). Cache `(workspace_id, key, payload_hash)`
> → 201 replay / 409 conflict. Generazione UUID lato client obbligatoria
> per evitare doppio post su doppio click.

### Modelli dominio rilevanti (già esistenti)

- `internal/models/post.go` — `Post`, `PostTarget`, `PostStatus`
  (draft/queued/publishing/published/failed/retrying/dlq).
- `internal/models/asset.go` — `MediaAsset` (UUID, SHA256, status,
  user_id).
- `internal/models/youtube_video_edit.go` — `YouTubeVideoEdit` con
  `actual_privacy` e `youtube_sync_status` (P0#7).
- `internal/models/account_details.go` — `AccountContentItem`,
  `AccountContentPage` (items + next_cursor).

### State machine (confermata in `pkg/metrics/collector.go`)

```
draft → queued → publishing → published
                  ↓
                  retrying
                  ↓
                  waiting_provider
                  ↓
                  failed
                  ↓
                  partially_published / dlq
```

Il target row in `post_targets` è il **source of truth** per lo stato
del singolo canale (vedi `pkg/api/migrations/018_publish_state_machine.sql`
e `035_worker_hardening.sql`). `post_targets.id` è quello che ci serve
per il polling.

---

## Frontend — riusi concreti (file + linee)

### Riusi ad alta confidenza

| File                                            | Pattern riuso                                  | Note |
| ----------------------------------------------- | ---------------------------------------------- | ---- |
| `web/src/pages/internal/Compose.tsx:160-260`    | **presign + PUT + complete** flow completo     | Codice pronto da estrarre in `mediaApi.ts`. I handle `presign failed`, size mismatch, mark failed sono già gestiti. |
| `web/src/pages/internal/Compose.tsx:280-330`    | **POST /api/v1/posts** payload builder        | Costruisce already `workspace_id`, `content.{title,caption,media:[{asset_id}]}`, `targets:[{platform_account_id}]}`, `status`. **NON** passa `Idempotency-Key` (gap). |
| `web/src/pages/internal/AccountDetails.tsx:111-145` | `ContentVideoCard` con thumbnail, duration, date, privacy, metrics, **"Open on YouTube"**, **"Modifica copertina"** | Esatto UI del piano. Seleziona solo privacy=private (hardcoded). |
| `web/src/pages/internal/AccountDetails.tsx:280-310` | **`handleEditThumbnail`** → POST `/youtube/editor-sessions` + `window.open(editor_url, '_blank')` | Logica completa identica a quanto descritto nel piano. Da estrarre in `youtube/editorSessionsApi.ts` per evitare duplicazione (oggi anche in YouTubeStudio.tsx:288 e Calendar.tsx:242). |
| `web/src/pages/internal/AccountDetails.tsx:240-275` | Loader `/accounts/{id}/content?limit=20` con privacy filter | Pattern di loadContent riusabile. Aggiunti tab "Tutti/Privati/Unlisted/Pubblici" e highlighting via `?video=`. |
| `web/src/lib/auth.ts:authedFetch`               | fetch con CSRF + 401 → AuthError + toast       | Usare per tutte le nuove chiamate. |
| `web/src/lib/api-client.ts:apiClient<T>`        | wrapper tipato con auto JSON + API_BASE_URL    | Alternativa typed-first a `authedFetch`. |
| `web/src/lib/providers.tsx:PROVIDERS`           | mappa provider → colore / nome / icona         | Riuso per badge canale nella `ChannelHeader`. |
| `web/src/components/feedback/{Skeleton,ErrorState,EmptyState}` | skeleton/empty/error UI          | Standardizzati per tutte le nuove pagine. |
| `web/src/pages/internal/AccountDetails.tsx:520-580` | header card con avatar canale, status OAuth, refresh, back | Pattern identico al `ChannelHeader` richiesto dal piano. |

### Riusi a media confidenza

- **`AccountDetailsPage` come scheletro di "pagina canale"**: ha già
  Overview/Videos/Connection tabs, header con avatar, Modifica
  copertina, Open on YouTube, Load more pagination. Per farlo matchare
  il piano serve solo: (a) spostare `?video=` evidence, (b) default
  tab = Videos (NON Overview), (c) sostituire hardcoded `privacy=private`
  con i chip filter Tutti/Privati/Unlisted/Pubblici, (d) cambiare
  back-link da `/app/linking` a `/app/groups` (per matchare "torna ai canali").
- **`Posts.tsx`** come scheletro fallback: contiene già lista post con
  status badge. Da estendere con click-through alla pagina-stato.

---

## Frontend — Placeholder e Gap (da costruire)

| Cosa                                               | Perché è un gap |
| -------------------------------------------------- | --------------- |
| `/content/:postId/publish` (o `/app/content/:postId/publish`) | Non esiste alcuna pagina-stato per singolo post. Da creare come state-machine UI con polling su `/posts/{id}/targets`. |
| Wizard a 3 step (`/content/new`)                   | Compose.tsx è monolite. Da rifattorizzare in 3 step espliciti con step indicator se si vuole matchare il piano. **Possibile short-cut**: mantenere Compose.tsx e aggiungere solo "Step header" + validazione privacy=private obbligatoria. Decidere con utente. |
| Lista canali YouTube                                | `Compose.tsx` carica TUTTI gli account, non filtrati per `platform=youtube`. Da aggiungere un endpoint nuovo `GET /api/v1/accounts?platform=youtube` oppure filtrare lato client. |
| `useCreatePost.ts` hook con Idempotency-Key        | Non esiste. Compose.tsx chiama `authedFetch` direttamente senza header `Idempotency-Key`. |
| `usePostTargetStatus.ts` hook poll                 | Non esiste. `handleGetPostTargets` server-side restituisce array vuoto — gap dual-sided. |
| `useChannelContent` hook + `channelContentApi.ts`    | Non esiste. AccountDetails usa fetch inline. |
| `useYouTubePublishLiveUpdate` (cross-tab)          | **Non esiste**. Solo `courserpierone/inbox-provider.tsx` usa `BroadcastChannel` (per mark-read messages). Niente cross-tab publish updates in InstaeditLogin. |
| `createYouTubeEditorSession` client estratto        | Inline in 3 file: AccountDetails, YouTubeStudio, Calendar. Duplicato. |
| Cache-buster sulla thumbnail                       | Non esiste. `AccountDetails.tsx` mostra `item.thumbnail_url` raw. |
| `?video=…` highlighting                            | Non implementato. AccountDetails riceve solo `accountId` da `useParams`. |
| `refetchInterval` durante processing               | Non esiste. La pagina si ricarica solo on-focus. |
| `refetchOnWindowFocus: true`                       | Non esiste (AccountDetails ricarica solo su click del button "Sync"). |

---

## Hook & API layer mancanti (struttura target)

```
web/src/features/publishing/
├── api/
│   ├── mediaApi.ts          ← estrapola handleFileChange da Compose.tsx
│   ├── postsApi.ts          ← POST /posts + Idempotency-Key + types
│   └── postTargetsApi.ts    ← GET /posts/{id}/targets (e nuovo GET /post_targets/{id}?)
└── hooks/
    ├── useCreatePost.ts     ← UUID per Idempotency-Key + mutation
    └── usePostTargetStatus.ts ← polling state-machine

web/src/features/channels/
├── api/
│   ├── channelContentApi.ts ← GET /accounts/{id}/content
│   ├── channelsApi.ts       ← GET /accounts?platform=youtube (da aggiungere al backend)
│   └── editorSessionsApi.ts ← POST /youtube/editor-sessions (estratto dai 3 call site)
├── hooks/
│   ├── useChannel.ts        ← GET /accounts/{id}
│   ├── useChannelContent.ts ← useChannelContent + privacy filter + cursor
│   └── useYouTubePublishLiveUpdate.ts ← 1 sola BroadcastChannel + invalidazione multi-query
│       └── invalida: ['channel-content', accountId, privacy] + groupYouTubeVideosQueryKey (se in futuro Groups ha video)
└── components/
    ├── ChannelHeader.tsx
    ├── ChannelVideoFilters.tsx
    ├── ChannelVideoCard.tsx  ← riusa pattern di ContentVideoCard
    └── PublishStatusPanel.tsx ← riusa pattern di Posts.tsx row
```

---

## Decisioni aperte

1. **Path nuovo `/content/*` oppure riuso `/app/compose` + `/app/accounts/:id`?**
   Aggiungere route nuove richiede redirect delle esistenti e
   aggiornamento di tutti i link interni (`Sidebar.tsx`, `Dashboard.tsx`,
   ecc.). Riusare `/app/*` evita redirect ma NON matcha i nomi del piano.

2. **`GET /api/v1/post_targets/{id}` esiste?** No, NON esiste. Solo
   `GET /api/v1/posts/{id}/targets` (e attualmente vuoto). Servono
   entrambe: aggiungere il singolo `GET /post_targets/{id}` E wire
   `listByPost` lato `postStore`.

3. **`POST /api/v1/posts` status code**: il piano dice 202. Il backend
   restituisce 201. Trattare 201 come success senza cambiare il
   contratto server esistente.

4. **Default tab in pagina canale**: piano dice "Tutti". La logica
   attuale hardcoded di AccountDetails è `private`. Da cambiare.

5. **Back-link in pagina canale**: piano dice "Torna ai canali"
   (probabilmente `/app/groups` o nuovo `/app/channels`). AccountDetails
   oggi va a `/app/linking`. Decidere se mantenere linking o reindirizzare
   a `groups`.

6. **`idempotency_key` per upload completi**: la Fase 2 prevede un
   UUID per `/media/presign`? Compose.tsx non lo fa. Per il vertical
   slice minimo, OK solo su `/posts`; documentare il gap per upload-side.

---

## Order of work proposto (ricalibrato sull'esistente)

Per minimizzare il numero di commit e appoggiarsi al riuso:

1. **Estrarre `createEditorSession` client condiviso** (3 call site).
   Single commit. Impatto zero funzionale.
2. **Aggiungere `GET /api/v1/post_targets/{id}` + wirare
   `handleGetPostTargets`** lato backend. Single commit. Sblocca il
   polling frontend.
3. **Aggiungere `GET /api/v1/accounts?platform=youtube`** (se non
   esiste già). Probabilmente già filterable lato client.
4. **Costruire `features/publishing/` API + hooks** (mediaApi,
   postsApi, postTargetsApi, useCreatePost con UUID Idempotency-Key,
   usePostTargetStatus). Single commit.
5. **Costruire `features/channels/` API + hooks** (channelContentApi,
   editorSessionsApi, useChannel, useChannelContent, useYouTubePublishLiveUpdate).
   Single commit.
6. **Decidere**: A) riusare `AccountDetails` e aggiungere tabs +
   filter chips + `?video=` highlight; OPPURE B) creare nuova pagina
   `/app/dashboard-channels/:id`.
7. **Creare `PublishStatusPanel`** pagina (route da decidere).
   Polling target, badge status, link "Visualizza canale" + "Apri YouTube".
8. **Cross-tab BroadcastChannel unificato**: 1 solo listener che
   invalida SIA channel content cache SIA groupYouTubeVideosQueryKey
   (al momento solo il primo).
9. **Test E2E verticale** (vedi criteri PASS del piano).

---

## File da NON toccare in questa fase

- `internal/worker/`, `pkg/metrics/`, `internal/repository/` (lato
  server i contratti sono già pronti).
- `web/src/pages/platforms/`, `web/src/pages/landing/`,
  `web/src/components/marketing/`, `web/src/components/editor/`,
  `web/src/pages/internal/Uploads.tsx`, `Posts.tsx` (eccetto per
  piccolo extension al link "Open status").
- `web/src/pages/internal/YouTubeStudio.tsx`: è il InstaEditor,
  mantiene la sua logica. Solo verificare che pubblicando si torni
  alla pagina canale con refresh.

---

## Riferimenti rapidi

- Piano originale: vedi sezione `## Cosa implementare oggi`
  nel prompt utente.
- Documentazione API: `InstaeditLogin/api/openapi.yaml` —
  `pkg/api/accounts_read_handlers.go:374` (filter privacy),
  `pkg/api/posts_handlers.go:200` (handleCreatePost con idempotency),
  `pkg/api/youtube_editor_sessions.go:230` (POST endpoint).
- State machine: `pkg/metrics/collector.go:81` (`knownTargetStatuses`).
- Cross-tab precedent: `courserpierone/src/components/layout/inbox-provider.tsx:78-185`
  (modello per `useYouTubePublishLiveUpdate`).
