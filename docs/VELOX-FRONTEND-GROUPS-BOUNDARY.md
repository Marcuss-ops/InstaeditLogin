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

## Test di verifica del disaccoppiamento

- **Test A — Velox spento:** login, Groups, Channels, Group Detail, YouTube
  Videos, lingue, Drive, Posting funzionano; solo "Apri InstaEditor" fallisce.
- **Test B — DB Velox vuoto di gruppi:** InstaEdit mostra tutto normalmente.
- **Test C — rinomina gruppo in InstaEdit:** nessun aggiornamento in Velox.
- **Test D — flusso Modifica:** progetto → editor → salvataggio timeline in
  Velox; su InstaEdit resta il mapping via `external_project_id`.
