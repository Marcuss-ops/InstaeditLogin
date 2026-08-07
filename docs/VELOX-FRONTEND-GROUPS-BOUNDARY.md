# Confine architetturale — nessun Groups/Channels operativo nel frontend Velox

**Data:** 2026-08-07
**Branch:** `main`
**Tipo:** decisione architetturale + vincolo negativo (vietato reintrodurre)

## Stato

Il frontend Velox (SPA Next.js, repo separato `VeloxFrontend`, deployata come
InstaEditor al base path `/dark_editor_v2`) deve smettere di essere un luogo in
cui esistono entità operative Groups/Channels.

**Questo repository (`InstaeditLogin`) NON contiene il sorgente del frontend
Velox.** La rimozione delle pagine Groups / Channels / Select group / Manage
channels che usano il DB Velox è un lavoro da eseguire nel repo
`VeloxFrontend`, non qui.

## Vincolo negativo (regola)

Vietato in questo repository:

- qualsiasi `SyncGroupsToVelox()`, `SyncChannelsFromVelox()`,
  `MirrorGroupMemberships()` o altra sincronizzazione Groups InstaEdit ↔ Velox;
- incorporare o incapsulare UI operativa Velox in `web/src` (iframe della SPA
  Velox, componenti copiati, proxy `dark_editor_v2/*` che esporrebbe pagine
  Groups/Channels);
- nuovi riferimenti runtime a gruppi/canali operativi lato Velox;
- rendere il DB Velox una fonte per la UI Groups/Channels di InstaEdit.

Il contesto dell'editor (project_id, channel_id, channel_name, language) arriva
dal progetto aperto e viene passato al launcher come context, non come entità
persistente da amministrare indipendentemente. L'unico collegamento forte è
`InstaEdit project_id ↔ velox_project_id`.

## Azione richiesta nel repo VeloxFrontend

1. Rimuovere le pagine/path operativi che utilizzano il DB Velox:
   - Groups
   - Channels
   - Select group
   - Manage channels
2. **NIENTE** sincronizzazione InstaEdit → Velox in sostituzione.
3. Il contesto deve arrivare dal progetto aperto.

## Verificato in questo repository

- Le schermate Groups/Channels lato InstaEdit
  (`web/src/pages/internal/Groups.tsx`, `GroupsDetailPanels.tsx`,
  `useGroupsData.ts`, ...) usano esclusivamente API InstaEdit
  (`/api/v1/groups`, `/api/v1/groups/aggregate`, `/api/v1/accounts/...`):
  comportamento corretto, da mantenere.
- `Covers.tsx` (handoff editor) non incorpora iframe Velox: solo redirect a
  `/dark_editor_v2/editor/{project_id}`.
- Nessun iframe o proxy di UI Groups/Channels Velox presente in questo repo.

## Verifica Azione 2 (2026-08-07) — nessuna richiesta Velox dai flussi Groups/Channels

Audit completo: **zero chiamate a `VELOX_CONTROL_URL` dai flussi operativi**
(Groups, Channels, Group detail, YouTube videos, selezione canale, lingua
canale).

Evidenze:

- `VELOX_CONTROL_URL` compare solo server-side, in `internal/config`,
  `internal/veloxclient` (client BFF), `internal/bootstrap/router_wiring.go`
  e `docker-compose.yml`. Il browser non lo legge mai.
- L'unico codice Go che raggiunge Velox è `internal/veloxclient`, i cui
  metodi coprono SOLO jobs/workers/assets/editor bridge (vedi
  `internal/veloxcontract/contract.go` — nessun metodo Groups/Channels).
- I handler `/api/v1/groups/*`, `/api/v1/groups/{id}/youtube/videos`,
  `/api/v1/accounts/*` leggono esclusivamente `groupStore` /
  `workspaceStore` / `userRepo` (InstaEdit DB).
- Il frontend (`web/src`) non contiene riferimenti a `/api/v1/velox/*` o
  `/integrations/velox/*`.

Guardie di regressione (fail-closed):

- `web/src/lib/arch/veloxBoundary.test.ts` — scansione statica di
  `web/src`: i pattern `VELOX_CONTROL_URL`, `VELOX_CONTROL_JWT_SECRET`,
  `VELOX_API_TOKEN`, `/api/v1/velox/`, `integrations/velox` devono restare
  assenti dal bundle browser.
- `internal/veloxcontract/scope_guard_test.go` — l'interfaccia `Client` e
  la tassonomia scope non possono crescere metodi/scope di catalogo
  (groups/channels/accounts/videos).

## Test di verifica del disaccoppiamento

- **Test A — Velox spento:** login, Groups, Channels, Group Detail, YouTube
  Videos, lingue, Drive, Posting funzionano; solo "Apri InstaEditor" fallisce.
- **Test B — DB Velox vuoto di gruppi:** InstaEdit mostra tutto normalmente.
- **Test C — rinomina gruppo in InstaEdit:** nessun aggiornamento in Velox.
- **Test D — flusso Modifica:** progetto → editor → salvataggio timeline in
  Velox; su InstaEdit resta il mapping via `external_project_id`.
