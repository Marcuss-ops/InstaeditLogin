# Diagnosi N+1 — pagina Linking / GET /api/v1/accounts

Data: 2026-08-06 — Fase 1 del piano di correzione fan-out.

## Verdetto

**Confermato: pattern N+1.** Aprire la pagina Linking (`/app/linking`) con 46
canali YouTube produce **fino a 47 richieste API** (1 lista + 46 dettagli) e,
quando gli snapshot sono vecchi, **fino a 46 chiamate YouTube simultanee**
scatenate dal solo caricamento della pagina.

## 1. Fan-out lato frontend

File: `web/src/pages/internal/Linking.tsx`

- Riga **81** — chiamata lista: `GET /api/v1/accounts`
- Riga **92** — fan-out: `const enriched = await Promise.all(accounts.map(...))`
- Riga **96** — richiesta per canale: `GET /api/v1/accounts/${account.id}`
- Riga **93** — il filtro: `if (account.platform !== "youtube" || account.avatar_url) return account;`

La pagina interroga il dettaglio di **ogni account YouTube che non ha già un
`avatar_url` nella lista** (e la lista non lo fornisce quasi mai — vedi §2).
Tutte le richieste partono in parallelo, senza limite di concorrenza.

### Altri fan-out secondari (stesso pattern, minor priority)

- `web/src/pages/internal/Dashboard.tsx` righe 136-139 — `Promise.all` su
  `/accounts/{id}/content?limit=50&privacy=private` per ogni account.
- `web/src/pages/internal/usePrivateVideos.ts` riga 38 — `Promise.all` sugli
  account → `/accounts/{id}/content`.
- `web/src/pages/internal/GroupsDetailPanels.tsx` riga 259 — `Promise.all` su
  `/accounts/{id}` nel contesto dei gruppi.

## 2. Lato backend — perché ogni richiesta dettaglio è pesante

File: `pkg/api/accounts_read_handlers.go`

- `handleListAccounts` (riga ~142): una sola query
  `ListPlatformAccountsByUser`. L'avatar arriva da
  `avatarURLFromMetadata(account)` cioè **solo da `metadata.avatar_url`**
  (`pkg/api/accounts_performance_assembler.go` righe 423-428), **mai dallo
  snapshot** → per YouTube è quasi sempre vuoto → innesca il fan-out.
- `handleGetAccount` (riga ~189): per ogni richiesta:
  - riga **232**: `const snapshotMaxAge = 10 * time.Minute`
  - riga **243**: `IsSnapshotStale(account.ID, ...)` → 1 lettura DB
  - riga **248-253**: se stale → 1-3 letture dal Vault (token)
  - riga **255**: `detailsProvider.GetAccountDetails(...)` → **1 chiamata YouTube**
  - riga **291**: `UpsertSnapshot(...)` → 1 scrittura DB
  - righe **297-302**: `UpsertDaily(...)` + `storeYouTubeEarnings(...)` →
    eventuale **seconda chiamata YouTube Analytics** se il token ha lo scope monetario

Quindi una singola apertura pagina con snapshot vecchi equivale a:
`46 richieste HTTP` + `~46 chiamate YouTube` + `~184 operazioni DB` + `46+ letture Vault`.

## 3. Nessun refresher in background nel worker

`internal/worker/` **non contiene alcun goroutine di refresh snapshot**:
l'elenco dei file (publish_worker, upload_worker, reconcile_worker,
token_refresh_sweep, sessions_cleanup, webhook_worker, drive_batch_crawler,
ecc.) non include un refresher di `account_resource_snapshots`. Il grep su
`snapshot|GetAccountDetails|AccountDetails` nei worker non trova un
refresher dedicato. Gli unici refresh snapshot avvengono **on-demand dentro
l'handler HTTP** (`handleGetAccount` e `handleSyncAccount` in
`pkg/api/accounts_sync.go`), quindi il carico ricade sul page load.

## 4. Quantificazione con i dati reali (46 canali)

| Scenario snapshot | Richieste HTTP | Chiamate YouTube | Operazioni DB |
| --- | ---: | ---: | ---: |
| Fresh (< 10 min) | 47 | 0 | ~93 (46 letture + 1 lista) |
| Vecchi / assenti | 47 | fino a 46 + 46 Analytics | ~185 |

## 5. Duplicazioni di lista (refetch multipli)

Più componenti chiamano indipendentemente `GET /api/v1/accounts`:
`AccountSwitcher.tsx` riga 37, `useGroupsData.ts` riga 47, `useYouTubeStudioData.ts`
riga 56, `Compose.tsx` riga 162, `Dashboard.tsx` riga 112, `Linking.tsx` riga 81.
Nessuna cache condivisa né deduplicazione.

## 6. Azioni pianificate (fasi successive)

1. **Backend** — `GET /api/v1/accounts` aggregato: batch query con
   `LEFT JOIN account_resource_snapshots`, avatar/state/snapshot_stale nel
   payload della lista (niente fan-out).
2. **Frontend** — rimuovere il `Promise.all` in `Linking.tsx`; la lista deve
   bastare da sola; avatar mancanti → placeholder.
3. **Regola** — nessuna chiamata YouTube durante il page load; snapshot stale
   servito dalla cache + `refresh_pending`.
4. **Worker** — refresher snapshot in background con concorrenza limitata
   (3-5) + azioni esplicite "Aggiorna tutti / questo canale".
5. **Cache condivisa** per `GET /api/v1/accounts` (staleTime, no refetch on
   focus, dedup).
6. **Indici DB** verificati con `EXPLAIN ANALYZE` prima di aggiungere nulla.
7. **Test prestazionali** 10/50/100/200 account + verifica Definition of Done.
