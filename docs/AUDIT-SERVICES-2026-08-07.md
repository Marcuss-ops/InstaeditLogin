# Audit servizi e configurazione S3 — InstaEditLogin

**Data dell'audit:** 2026-08-07 06:55 UTC
**Ambito:** sviluppo locale Docker Compose
**Classificazione:** report operativo redatto; non contiene credenziali, token, valori di environment, identificativi di sessione o dati personali.

> Questo documento distingue le osservazioni effettuate durante l'audit storico dalle
> regole operative attuali. Per la topologia supportata fare riferimento a
> [`docs/DEPLOY.md`](DEPLOY.md), [`docs/ARCHITECTURE.md`](ARCHITECTURE.md) e
> [`docs/BINARIES.md`](BINARIES.md).

---

## 1. Perimetro e metodologia

L'audit ha esaminato il runtime locale e la configurazione dello stack:

- PostgreSQL;
- migrazione one-shot;
- API HTTP;
- worker;
- MinIO e inizializzazione del bucket;
- frontend Vite e proxy verso l'API;
- interpolazione delle variabili Docker Compose;
- raggiungibilità interna e round-trip S3.

Sono stati usati controlli read-only o verifiche operative non distruttive, tra cui
`docker compose ps`, `docker compose inspect`, health/readiness endpoint, rendering
Compose della configurazione, controlli di rete tra container e un round-trip S3 di
prova con rimozione dell'oggetto di test.

Nessun segreto viene riprodotto in questo report. I valori osservati durante l'audit
sono descritti soltanto per categoria: endpoint stale, bucket errato, credenziale
placeholder o variabile ereditata dalla shell.

---

## 2. Stato osservato durante l'audit

L'audit storico ha osservato il seguente grafo di servizi:

| Servizio | Ruolo | Esito osservato |
|---|---|---|
| `db` | PostgreSQL | healthy |
| `migrate` | migrazione one-shot | completato con exit code 0 |
| `api` | HTTP API | healthy |
| `worker` | processi di background | attivo |
| `minio` | storage S3-compatible | healthy; round-trip verificato |
| `minio-init` | inizializzazione idempotente del bucket | completato con exit code 0 |

Il report originale conteneva un conteggio di worker non più valido. La topologia
attuale registra **13 worker supervisionati** tramite `internal/worker.Registry` e
`internal/bootstrap/workers_wiring.go`. Il conteggio canonico è documentato in
`docs/ARCHITECTURE.md` e non deve essere duplicato manualmente in report storici.

Gli endpoint health/readiness hanno risposto correttamente durante l'audit. Questo
non prova da solo la disponibilità dello storage: il controllo di salute applicativo
non deve essere interpretato come un test completo di upload o presigned URL.

---

## 3. Problema S3 rilevato

### Sintomo

I container API e worker erano stati avviati con una configurazione S3 diversa da
quella attesa dal profilo dev. Le differenze riguardavano:

- endpoint non raggiungibile dall'interno del container;
- nome bucket non presente nello storage locale;
- credenziali placeholder o non corrispondenti al servizio MinIO attivo.

I valori effettivi sono stati omessi intenzionalmente da questo documento.

### Impatto

Il problema non era visibile dal solo health check. Avrebbe potuto causare errori su:

- upload di media;
- thumbnail e presigned URL;
- import da Google Drive;
- lettura o scrittura di oggetti su MinIO.

La causa operativa era quindi una configurazione runtime incoerente, non un problema
di autenticazione OAuth o di raggiungibilità PostgreSQL.

---

## 4. Root cause: precedenza delle variabili Compose

L'analisi ha confermato che le variabili usate nell'interpolazione di
`environment:` possono provenire da una sorgente diversa da quella usata da
`env_file:`. In generale, la precedenza applicata da Docker Compose è:

```text
shell environment > --env-file > root .env > valori dichiarati nel contesto
```

Il runtime osservato era stato influenzato da una combinazione di:

1. un vecchio file `.env` root ignorato da Git;
2. variabili stale esportate nella sessione della shell o dell'IDE;
3. avvio del Compose base senza l'overlay locale previsto dal percorso dev.

Per evitare ambiguità, il percorso locale canonico deve usare il target `make dev`,
che carica esplicitamente `.env.dev` e combina:

```bash
docker compose \
  --env-file .env.dev \
  -f docker-compose.yml \
  -f docker-compose.local.yml \
  up --build
```

Non creare un root `.env` generico per il percorso operativo locale e non affidarsi a
variabili esportate accidentalmente dalla shell.

---

## 5. Correzione operativa applicata durante l'audit

Durante l'audit sono stati ricreati i servizi interessati usando la configurazione
dev corretta e sono state ripetute le dipendenze idempotenti (`migrate` e
`minio-init`). Il report non conserva i comandi contenenti valori di environment o
segreti.

La verifica post-correzione ha confermato:

| Controllo | Esito |
|---|---|
| Configurazione S3 coerente tra API e worker | PASS |
| Endpoint interno MinIO raggiungibile dai container | PASS |
| Bucket applicativo inizializzato | PASS |
| Round-trip write/read/delete su oggetto di test | PASS |
| Health e readiness | PASS |
| Ricreazione idempotente di migrazione e bucket-init | PASS |

Il round-trip di prova non deve essere eseguito su dati di produzione senza una
procedura autorizzata; in produzione valgono i controlli non distruttivi del runbook.

---

## 6. Stato della topologia attuale

La topologia supportata oggi è:

```text
Vercel
  └── frontend React/Vite (web/)

VPS
  └── Caddy
      └── Docker Compose
          ├── migrate (one-shot)
          ├── api (cmd/api, HTTP only)
          ├── worker (cmd/worker, 13 worker supervisionati)
          ├── PostgreSQL
          └── MinIO
```

- Caddy è l'unico ingresso pubblico del backend.
- PostgreSQL e MinIO restano privati alla VPS e alla rete Compose.
- `cmd/migrate` completa prima dell'avvio operativo di API e worker.
- Il backend usa esclusivamente gli entrypoint separati `cmd/migrate`, `cmd/api` e `cmd/worker`.
- Le alternative di hosting backend e object storage non fanno parte del percorso
  operativo supportato; usare la topologia Vercel + VPS descritta nei documenti canonici.

---

## 7. Frontend locale

Durante l'audit il frontend Vite è stato verificato tramite il proxy dev verso l'API.
Il comportamento atteso è:

- `VITE_API_BASE_URL` vuoto in dev usa il fallback locale previsto dal frontend;
- le richieste `/api` vengono inoltrate all'API locale dal proxy Vite;
- il frontend non accede direttamente a PostgreSQL o MinIO;
- le verifiche browser devono controllare rendering, health via proxy e assenza di
  errori console, senza stampare environment o credenziali.

Gli identificativi delle sessioni terminali/browser usate nell'audit originale sono
stati rimossi perché non sono necessari per riprodurre la procedura.

---

## 8. Follow-up operativi

1. Usare sempre `.env.dev` con `docker-compose.local.yml` per lo sviluppo locale.
2. Prima di `make dev`, controllare che la shell non esporti variabili S3, database o
   ambiente che sovrascrivano il profilo esplicito.
3. Mantenere `S3_ENDPOINT`, bucket e credenziali applicative coerenti con il servizio
   MinIO del profilo in uso; non copiarne i valori in documentazione o log.
4. Impostare `BOOKING_HASH_SECRET` in produzione tramite il secret manager, senza
   inserirlo nel repository o nei report.
5. Gestire qualsiasi precedente esposizione di credenziali tramite rotazione del
   secret interessato e verifica dei log; questo report non contiene né conserva il
   valore esposto.
6. Per deploy, DNS, Caddy, MinIO e recovery seguire esclusivamente
   [`docs/DEPLOY.md`](DEPLOY.md) e [`docs/OPERATIONS.md`](OPERATIONS.md).
7. Per il conteggio e il lifecycle dei worker usare il codice e
   [`docs/ARCHITECTURE.md`](ARCHITECTURE.md), non copie storiche di questo audit.

---

## 9. Riferimenti canonici

- [`docker-compose.yml`](../docker-compose.yml) — grafo base dei servizi;
- [`docker-compose.local.yml`](../docker-compose.local.yml) — overlay dev locale;
- [`docker-compose.production.yml`](../docker-compose.production.yml) — hardening VPS;
- [`Dockerfile`](../Dockerfile) — target `migrate`, `api`, `worker` e wrapper legacy;
- [`docs/DEPLOY.md`](DEPLOY.md) — deployment Vercel + VPS + Compose + MinIO;
- [`docs/OPERATIONS.md`](OPERATIONS.md) — DNS, TLS, monitoring e recovery;
- [`docs/ARCHITECTURE.md`](ARCHITECTURE.md) — architettura applicativa e worker;
- [`docs/BINARIES.md`](BINARIES.md) — responsabilità e contratti degli entrypoint.

**Regola privacy:** audit e documentazione possono descrivere nomi di variabili,
ruoli, errori e procedure, ma non devono contenere valori di secret, token, password,
header Authorization, URL firmati, identificativi privati o output integrali dei
container.
