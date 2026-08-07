# Audit Servizi & Fix S3 — InstaEditLogin

**Data:** 2026-08-07 06:55 UTC
**Ambiente:** dev locale (Docker Compose, `docker-compose.yml` base, senza overlay local)
**Branch:** `main` (`e99a043` — feat(web): SVG language flags, wider title detection, and deploy guide)
**Tipo di intervento:** audit read-only + correzione configurazione container (nessuna modifica al codice sorgente)

---

## 1. Perimetro e metodologia

Audit operativo dell'intero stack di sviluppo locale di InstaEditLogin:

- **Topologia verificata:** `db` (Postgres 15) + `migrate` (one-shot) + `api` (HTTP) + `worker` (background) + `minio` + `minio-init` (sidecar one-shot), secondo il contratto canonico di `docker-compose.yml`.
- **Frontend:** `web/` (React/Vite) — dev server e connessione SPA → API.
- **Strumenti usati:** `docker compose ps/inspect/logs`, `curl` su health/ready, `mc` (MinIO Client) per roundtrip S3 reale, test di raggiungibilità rete dai container, `docker compose config` per il render dell'interpolazione env.

Nessun file di codice è stato modificato. L'unico artefatto su filesystem è la rinomina di `.env` → `.env.old` (vedi §5).

---

## 2. Stato servizi (post-fix)

| Servizio | Immagine | Stato | Note |
|---|---|---|---|
| `db` | `postgres:15-alpine` | ✅ **healthy** | `127.0.0.1:5432`, PostgreSQL 15.18, 115 migrazioni applicate, 63 MB |
| `migrate` | `instaeditlogin-migrate` | ✅ **exited (0)** | one-shot ri-eseguito in modo idempotente durante la ricreazione |
| `api` | `instaeditlogin-api` | ✅ **healthy** | `127.0.0.1:8080`, `RestartCount=0`, 0 errori nei log |
| `worker` | `instaeditlogin-worker` | ✅ **up** | 12 worker di background registrati, `RestartCount=0`, 0 errori |
| `minio` | `minio/minio:latest` | ✅ **healthy** | roundtrip S3 reale verificato (write→read→delete) |
| `minio-init` | `minio/mc:latest` | ✅ **exited (0)** | bucket pronto (idempotente) |

**Health endpoint:**
```json
GET /api/v1/health  → {"limits":{"publish_horizon_days":30,"video_retention_buffer_days":7},
                       "platforms":["youtube"],"service":"InstaEditLogin","status":"ok","version":"2.0.0"}
GET /ready          → 200
```

**Rete:** `media-backend` (network interna `internal: true`) collega `api` + `worker` + `minio`; `default` collega tutti. Nessun container raggiunge `localhost:9000` (endpoint stale, vedi §3) ma tutti raggiungono `minio:9000` (DNS compose).

---

## 3. Problema trovato: configurazione S3 stale nei container

### Sintomo

I container `api` e `worker` in esecuzione erano stati creati ~11h prima dell'audit e giravano con env **stale**:

| Variabile | Valore nei container (stale) | Valore in `.env.dev` (atteso) |
|---|---|---|
| `S3_ENDPOINT` | `http://localhost:9000` | `https://dev.instaedit.org` |
| `S3_BUCKET` | `instaedit-dev-uploads` | `instaedit-media` |
| `S3_ACCESS_KEY` | `AKIADEVPLACEHOLDER` | `instaedit-local` |
| `S3_SECRET_KEY` | `devsecretplaceholder...` | `change-this-local-secret` |
| `APP_ENV` | `dev` (ok) | `dev` |

### Perché era un problema reale

1. **`localhost:9000` irraggiungibile dal container**: `docker exec instaedit-api wget http://localhost:9000/...` → **FAIL**. All'interno del container `localhost` è il container stesso; MinIO è raggiungibile solo via DNS compose `minio:9000` (→ **OK**).
2. **Bucket inesistente**: `instaedit-dev-uploads` non esiste in MinIO. Bucket reali: `instaedit-dev`, `instaedit-local`, `instaedit-media`. L'API/worker che scriveva su quel bucket avrebbe ricevuto `NoSuchBucket` a ogni PUT.
3. **Credenziali placeholder**: `AKIADEVPLACEHOLDER` non corrisponde alle credenziali root reali di MinIO (`MINIO_ROOT_USER`/`MINIO_ROOT_PASSWORD` da `.env.dev`) → anche l'autenticazione SigV4 sarebbe fallita.

**Conseguenza pratica:** il health check passava (non tocca S3) ma qualsiasi upload media, thumbnail, presigned URL o import Drive sarebbe fallito. Lo stack era "verde in superficie, rotto sotto".

---

## 4. Root cause: precedenza di interpolazione env di Docker Compose

Il render di `docker compose --env-file .env.dev config` mostrava che `api` e `worker` ricevevano `${S3_ENDPOINT}`/`${S3_BUCKET}` interpolati da valori stale, mentre `migrate` e `minio-init` (che li prendono via `env_file`) ricevevano già i valori corretti di `.env.dev`.

La precedenza di Docker Compose per l'interpolazione dei `${VAR}` in `environment:` è:

```
shell environment  >  --env-file  >  root .env  >  (env_file: per-service)
```

**Due fonti stale contribuivano:**

1. **Root `.env`** (gitignored, `APP_ENV=production`, S3 `localhost:9000`/`instaedit-dev-uploads`) — file legacy che ombreggiava `.env.dev`.
2. **Variabili esportate nella shell environment della sessione** (`env` mostrava `S3_ENDPOINT=http://localhost:9000`, `S3_BUCKET=instaedit-dev-uploads`, `APP_ENV=production`, `DATABASE_URL`, ecc.) — ereditate dalla sessione terminale/IDE, non da profili shell (`~/.bashrc`, `~/.profile`, `direnv` assenti).

### Prova sperimentale

```bash
# Con le var stale in ambiente → api/worker ricevono i valori sbagliati
$ INSTAEDIT_ENV_FILE=.env.dev docker compose --env-file .env.dev config | grep S3_ENDPOINT
api:       S3_ENDPOINT: http://localhost:9000     # ← stale
worker:    S3_ENDPOINT: http://localhost:9000     # ← stale

# Dopo unset delle variabili stale → tutto corretto
$ unset S3_ENDPOINT S3_BUCKET S3_ACCESS_KEY S3_SECRET_KEY S3_REGION APP_ENV APP_MODE DATABASE_URL
$ INSTAEDIT_ENV_FILE=.env.dev docker compose --env-file .env.dev config | grep S3_ENDPOINT
api:       S3_ENDPOINT: https://dev.instaedit.org   # ✅
migrate:   S3_ENDPOINT: https://dev.instaedit.org   # ✅
worker:    S3_ENDPOINT: https://dev.instaedit.org   # ✅
```

---

## 5. Fix applicato

### 5.1 Ricreazione `api` + `worker` con env corretto

Le variabili S3 sono state esportate in shell dai valori di `.env.dev` (per vincere la precedenza d'interpolazione) e i container sono stati ricreati:

```bash
export S3_ENDPOINT=$(grep -E '^S3_ENDPOINT=' .env.dev | head -1 | cut -d= -f2-)
export S3_BUCKET=$(grep -E '^S3_BUCKET=' .env.dev | head -1 | cut -d= -f2-)
export S3_ACCESS_KEY=$(grep -E '^S3_ACCESS_KEY=' .env.dev | head -1 | cut -d= -f2-)
export S3_SECRET_KEY=$(grep -E '^S3_SECRET_KEY=' .env.dev | head -1 | cut -d= -f2-)
INSTAEDIT_ENV_FILE=.env.dev docker compose --env-file .env.dev up -d --force-recreate api worker
```

**Nota:** `--force-recreate` ha ri-eseguito anche `migrate` e `minio-init` (dipendenze) — entrambi usciti con codice 0 in modo idempotente.

### 5.2 Rinomina del root `.env` stale

```bash
mv .env .env.old
```

Il file legacy (gitignored) non può più ombreggiare `.env.dev` nei prossimi `docker compose up`. `.env.old` resta consultabile come backup.

### 5.3 Verifica post-fix

| Check | Esito |
|---|---|
| Env container `api` | ✅ `S3_ENDPOINT=https://dev.instaedit.org`, `S3_BUCKET=instaedit-media`, `S3_ACCESS_KEY=instaedit-local` |
| Env container `worker` | ✅ identico ad `api` |
| Log startup `api` | ✅ `storage provider: S3-compatible configured ... bucket=instaedit-media` |
| Log startup `worker` | ✅ `12 background workers registered` |
| `/api/v1/health` + `/ready` | ✅ 200 |
| Errori nei log (5m) | ✅ 0 `ERROR`/`wire failed` |
| Roundtrip S3 (`mc cp/stat/rm`) | ✅ OK su `instaedit-media` |

---

## 6. Frontend (Vite dev server)

- Dev server avviato in sessione tmux persistente (`tui-test-1786085628910-usuu`): **http://localhost:5173**
- **Proxy confermato** in `web/vite.config.ts`: `/api` → `http://localhost:8080` (changeOrigin)
- `VITE_API_BASE_URL` vuoto → fallback `http://localhost:8080` in `web/src/lib/api.ts` (corretto per dev; warning atteso al boot)
- Verifiche browser (Chrome): landing e `/login` renderizzano, **0 errori console**, nessun banner "API unreachable", `GET /api/v1/health` via proxy → 200

---

## 7. Raccomandazioni e nodi aperti

1. **Unset delle variabili stale nella shell operatore** prima del prossimo `make dev` (o riavvio del terminale), altrimenti l'ombreggiatura ricompare:
   ```bash
   unset S3_ENDPOINT S3_BUCKET S3_ACCESS_KEY S3_SECRET_KEY S3_REGION APP_ENV APP_MODE DATABASE_URL
   ```
2. **`BOOKING_HASH_SECRET` non settato** → warning al boot (pepper random per-processo). Ok in dev; da settare in produzione per dedup stabile.
3. **MinIO senza porte host** nel `make dev` attuale (overlay `docker-compose.local.yml` non usato) → la Caddy locale (`ops/local/Caddyfile`, porta 19000) non può raggiungere MinIO; da chiarire il percorso S3 dev previsto (via `dev.instaedit.org` o overlay locale).
4. **Branch non mergiati**: `agent/fix-local-dev-runtime` ha 3 fix non su `main` (thumbnail URLs locali, token refresh YouTube, dev runtime).
5. **Refactoring**: il tracker è stato riallineato al tree corrente. Il refactoring procede per slice concrete su `main`, senza ticket inventati. Le slice già verificate includono configurazione, group repository, worker wiring, post handlers, YouTube OAuth, livestream commands e sampler/backoff.
6. **Security**: chiave Tigris leakata documentata in `TOMORROW.md` — da ruotare.
7. **Decisione deploy** (Path A/B/C da `TOMORROW.md`): Fly vs beta-Meta vs Hetzner self-hosted.

---

## 8. Riferimenti

- `docker-compose.yml` — topologia canonica (db → migrate → api+worker, minio, reti)
- `.env.dev` / `.env.dev.example` — configurazione dev attesa
- `docs/TOMORROW.md` — decisioni operative e security
- `docs/REFACTORING-TRACKER.md` — debito tecnico tracciato
- `docs/NPLUS1_PERFORMANCE.md`, `docs/NPLUS1_INDEX_AUDIT.md` — performance già validate
