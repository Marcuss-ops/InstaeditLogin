# N+1 fix — Index audit (Fase 7)

Verifica database della query batch `GET /api/v1/accounts` (LEFT JOIN
`platform_accounts` + `account_resource_snapshots`) con
`EXPLAIN (ANALYZE, BUFFERS)` su **PostgreSQL 15.18** (container locale
`instaedit-db`), dati reali (46 account, user_id=5) + dataset sintetico
a scala in transazione **ROLLBACK** (nessuna modifica persistente).

## Query analizzata (repository half dell'aggregato)

```sql
SELECT pa.id, pa.user_id, pa.platform, ..., ars.platform_account_id, ...
  FROM platform_accounts pa
  LEFT JOIN account_resource_snapshots ars ON ars.platform_account_id = pa.id
 WHERE pa.user_id = $1
   AND ($2::timestamptz IS NULL OR (pa.created_at, pa.id) < ($2, $3))
   AND ($4::bool OR pa.status NOT IN ('disconnected','revoked','deleted','cancelled','canceled'))
   AND ($5::text = '' OR pa.platform = $5)
 ORDER BY pa.created_at DESC, pa.id DESC LIMIT $6
```

## 1. Piano a scala reale (46 account, user 5)

```text
Limit  →  Sort (created_at DESC, id DESC)  →  Hash Right Join
  →  Seq Scan account_resource_snapshots (2 righe, PK lookup)
  →  Hash → Seq Scan platform_accounts pa
       Filter: (user_id = 5) AND status NOT IN (...)
Execution Time: 0.219–0.286 ms
```

**Giudizio: PASS.** A questa scala il Seq Scan è la scelta ottimale del
planner (la tabella è ~1 pagina); il LEFT JOIN è già servito dalla **PK**
di `account_resource_snapshots(platform_account_id)` — nessun indice
extra necessario per l'UNIONE.

## 2. Piano a scala (120k account / 12k utenti, filtro selettivo)

Dataset: 12.000 utenti temporanei + 120.000 account (stati misti:
active/deleted/disconnected), transazione rollback. Query per un utente
con ~10 account:

| Configurazione | Accesso a `platform_accounts` | Righe esaminate | Execution |
|---|---|---|---|
| **A** solo `idx_platform_accounts_user_id` (esistente) | Bitmap Index Scan su `user_id` + re-filter status in heap | 30 → 10 scartate | 0.206 ms |
| **B** + composito `(user_id, status)` | Bitmap Index Scan composito, status filtrato **nell'indice** | 10 | 0.170 ms |
| **C** + anche `(user_id, status, created_at DESC, id DESC)` | identico a B (la sort su ≤101 righe resta top-N, irrilevante) | 10 | 0.144 ms |

**Conclusione:** il composito `(user_id, status)` elimina il re-filter
dello status a livello heap quando il filtro utente è selettivo (il caso
multi-tenant reale). L'opzione C non aggiunge valore misurabile per la
sort (top-N heapsort su ≤101 righe) → **non aggiunta per evitare
indici alla cieca**.

## 3. Audit indici DoD

| Indice richiesto dalla DoD | Stato reale | Azione |
|---|---|---|
| `platform_accounts (user_id, status)` | **MANCANTE** (solo `user_id` e `status` separati, 001/005) | ✅ **migration 103** |
| `account_resource_snapshots (platform_account_id)` | ✅ già presente (PRIMARY KEY, migration 042) | nessuna |
| `group_accounts (group_id, platform_account_id)` | ✅ già presente (PK `(group_id, account_id)`, migration 041) | nessuna |
| `workspace_channels (workspace_id, platform_account_id)` | ✅ già presente (PK, migration 044) | nessuna |

## 4. Migration aggiunta

`internal/database/migrations/103_platform_accounts_user_status_index.sql`

```sql
CREATE INDEX IF NOT EXISTS idx_platform_accounts_user_status
    ON platform_accounts (user_id, status);
```

- Idempotente (contratto runner `migrations.go::RunMigrations`).
- Applicata e ri-verificata sul DB locale (EXPLAIN reale post-indice).
- Test integration `migrations_index_audit_test.go` (`-tags=integration`):
  presenza pre/post 103, PK degli altri 3 target, idempotenza del re-run,
  definizione esatta `(user_id, status)`.

## Numeri chiave

| Metrica | Prima | Dopo |
|---|---|---|
| 46 account (reali) | 0.286 ms (Seq Scan) | 0.219 ms (Seq Scan — piano invariato, varianza di run; l'indice non cambia il piano a questa scala) |
| 120k righe, utente selettivo | 0.206 ms (30 righe indice) | 0.170 ms (10 righe indice) |
| Chiamate SQL per page load | 1 (batch, invariato) | 1 |
| Chiamate YouTube al load | 0 | 0 |

## Definition of Done Fase 7

```text
EXPLAIN (ANALYZE, BUFFERS) eseguito su query batch reale     PASS
Indice platform_accounts (user_id, status) presente          PASS (migration 103)
account_resource_snapshots (platform_account_id) presente    PASS (PK)
group_accounts (group_id, platform_account_id) presente      PASS (PK)
workspace_channels (workspace_id, platform_account_id)       PASS (PK)
Nessun indice aggiunto alla cieca                            PASS (C scartata con evidenza)
Migration idempotente + test integration                     PASS
```
