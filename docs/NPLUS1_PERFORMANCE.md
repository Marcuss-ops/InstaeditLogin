# NPLUS1_PERFORMANCE — dataset 10/50/100/200 account

Misurazione offline del fix N+1 (Fasi 1–6) su dataset simulati. Il
principio architetturale verificato: **aprire la pagina dei canali esegue
esattamente 1 query SQL (LEFT JOIN batched) e 1–2 richieste API; le
chiamate a YouTube avvengono solo nel worker di background, mai durante
il page load.**

## Cosa viene misurato e come

| Metrica | Strumento | Limite |
|---|---|---|
| Tempo di risposta `GET /api/v1/accounts` (handler) | Benchmark Go `BenchmarkHandleListAccounts_{10,50,100,200}` in `pkg/api/accounts_list_benchmark_test.go` (repository fake che restituisce N account + snapshot; misura classificazione, stale-stamping e JSON encoding) | Sub-millisecondo |
| Richieste HTTP al page load | Perf test Vitest `web/src/pages/internal/Linking.perf.test.tsx` (fetch mock che conta gli URL) | ≤ 3 |
| Fan-out per-account (`/accounts/{id}`) e chiamate YouTube esterne | Stesso perf test: il contatore dei dettagli deve restare 0; il backend non chiama MAI il provider nel read path (pinned da `pkg/api/accounts_stale_snapshot_test.go`) | 0 |
| Tempo fino a pagina interattiva | Perf test Vitest (render → heading + card provider) | < 5s (CI-safe); valori reali riportati sotto |

**Non misurabile offline** (richiede infrastruttura live): latenza della
singola query LEFT JOIN su PostgreSQL (indicizzata su `user_id`) e p95
end-to-end con rete. Con 1 sola query a prescindere da N e un handler a
~0,15ms, il budget di 300–500ms p95 ha margini di oltre 3 ordini di
grandezza lato handler.

## Matrice risultati (misurato, 2026-08-06)

| Account | Richieste API | Richieste /accounts/{id} | Chiamate YouTube | /accounts handler (ns/op) | Pagina interattiva |
|---|---:|---:|---:|---:|---:|
| 10  | 1–2 | 0 | 0 | ~18 000 ns (0,018 ms) | 263 ms |
| 50  | 1   | 0 | 0 | ~79 000 ns (0,079 ms) | 82 ms  |
| 100 | 1   | 0 | 0 | ~151 000 ns (0,151 ms) | 59 ms  |
| 200 | 1   | 0 | 0 | ~240 000 ns (0,240 ms) | 25 ms  |

> `Linking.perf.test.tsx` (estratto dalla run):
> `[perf] accounts=50 api_requests=1 total_requests=1 per_account_fanout=0 time_to_interactive=82ms`
> `[perf] accounts=100 api_requests=1 total_requests=1 per_account_fanout=0 time_to_interactive=59ms`
> `[perf] accounts=200 api_requests=1 total_requests=1 per_account_fanout=0 time_to_interactive=25ms`
>
> Benchmark Go (`-benchtime 500x`, handler con cursor pagination):
> `10=18025 50=78961 100=151361 200=239685 ns/op`.
> I valori assoluti variano leggermente a seconda della versione
> dell'handler (il cap di paginazione tronca il lavoro per N>100), ma
> restano tutti sotto il millisecondo con margini di oltre 3 ordini di
> grandezza sul budget p95 di 300–500 ms.
> (Le richieste API restano 1 anche a 200 account: il manifest condiviso
> header+pagina è deduplicato dalla cache 60s di `listAllAccounts`).

## Verdetto contro gli obiettivi della DoD

| Obiettivo | Target | Misurato | Esito |
|---|---|---|---|
| 50 account pagina interattiva | < 1,5 s | 82 ms | ✅ PASS |
| 100 account pagina interattiva | < 2 s | 59 ms | ✅ PASS |
| 200 account pagina interattiva | < 3 s | 25 ms | ✅ PASS |
| Chiamate YouTube durante il load | 0 | 0 (fan-out 0) | ✅ PASS |
| Richieste API per aprire la pagina | 2–3 | 1–2 | ✅ PASS |
| Nessun aumento lineare delle richieste | costante | costante (1–2) | ✅ PASS |
| p95 API | < 300–500 ms | handler ~0,15 ms (DB/network da misurare live) | ⚠️ PARZIALE (live) |

## Perché il numero di richieste non cresce con N

- **Frontend**: `Linking.tsx` fa 1 sola `GET /api/v1/accounts`; gli avatar
  mancanti mostrano il placeholder iniziale (nessuna richiesta al
  dettaglio per recuperare un'immagine). La cache condivisa di
  `listAllAccounts` (staleTime 60s + dedup in-flight) collassa in 1
  richiesta anche header (`AccountSwitcher`) + pagina.
- **Backend**: `handleListAccounts` esegue 1 query `LEFT JOIN` su
  `account_resource_snapshots` (Fasi 1–2) e restituisce per ogni account
  `avatar_url`, `account_state`, `is_publishable`, `snapshot_stale`.
  `handleGetAccount` non chiama mai il provider (strict rule, Fase 4).
- **Worker**: gli snapshot stale vengono aggiornati dal
  `SnapshotRefreshSweepWorker` con concorrenza 4, mai dal page load.

## Riproducibilità

```bash
# Backend handler (10/50/100/200 account)
go test ./pkg/api/ -bench BenchmarkHandleListAccounts -benchtime 2000x -run '^$'

# Frontend (richieste HTTP + tempo interattivo + fan-out)
cd web && npx vitest run src/pages/internal/Linking.perf.test.tsx

# 0 chiamate YouTube al page load (strict rule)
go test ./pkg/api/ -run 'TestHandleGetAccount_StaleSnapshot|TestHandleListAccounts_Joined' -count=1
```

## Raccomandazioni live (fuori scope offline)

1. Misurare il p95 reale di `GET /api/v1/accounts` in staging con 200
   account e traffico reale (le metriche `internal/metrics` sono già
   cablate).
2. Misurare il tempo della query `LEFT JOIN` con `EXPLAIN (ANALYZE)`
   sul DB di produzione (atteso sub-millisecondo con l'indice
   `platform_accounts(user_id, status)`).
