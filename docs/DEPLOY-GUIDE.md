# Guida pratica — Deploy e aggiornamento (InstaEdit)

> **Scopo:** aggiornare correttamente il prodotto in produzione, backend e
> frontend, con i comandi **verificati** su questa infrastruttura.
> Questa guida integra e semplifica `docs/DEPLOY.md` (runbook canonico) e
> `docs/DEPLOY_FRONTEND.md`; per DNS/cert/monitoraggio/recovery vedi
> `docs/OPERATIONS.md`.

## Architettura in 30 secondi

```
app.instaedit.org  ──►  Vercel (solo frontend web/, bundle da web/dist)
api.instaedit.org  ──►  VPS 51.91.11.36  (Caddy → 127.0.0.1:8080)
                            └─ docker compose: db · migrate · api · worker · minio
```

⚠️ **Questo computer È il VPS di produzione** (hostname `vps-334f342f`,
IP `51.91.11.36`, Caddy + `instaedit-velox-proxy.service` attivi). Lo stack
Docker che vedi qui è il backend che serve `api.instaedit.org`. Attenzione
quando lanci comandi Docker: stai operando su produzione.

## Aggiornare il BACKEND (VPS)

```bash
cd /home/pierone/Projects/company/InstaeditLogin
git pull                          # aggiorna il codice
INSTAEDIT_ENV_FILE=.env.dev docker compose --env-file .env.dev up -d --build
```

- **env file:** lo stack usa `.env.dev` come `env_file` (è la configurazione
  reale di questa macchina: la sua `ENCRYPTION_KEY` decifra tutti i token
  OAuth salvati — verificato 62/62). **Non avviare lo stack con altri env**
  (`.env`, `.env.staging`, …): le chiavi differiscono e i token
  diventerebbero illeggibili.
- `docker compose up -d --build` ricostruisce `api`, `worker`, `migrate`
  (il job `migrate` applica le migrazioni pendenti prima che api/worker
  partano). Il DB non viene toccato dai rebuild.

### Verifiche dopo l'aggiornamento backend

```bash
docker ps                                  # api healthy, worker up, db healthy
curl -fsS https://api.instaedit.org/api/v1/health   # 200
curl -s  https://api.instaedit.org/ready            # {status, database, migrations}
docker exec instaedit-db psql -U instaedit -d instaedit_login -tAc \
  "SELECT filename FROM schema_migrations ORDER BY applied_at DESC LIMIT 5"
docker logs instaedit-api --since 10m 2>&1 | grep -icE 'error|panic'   # 0 atteso
```

`/ready` può rispondere 503 nei primi secondi dopo il reboot: è il warm-up
dei worker, non un errore. Riprova dopo ~1 minuto.

## Deploy del FRONTEND (Vercel)

### 1. Flusso automatico (consigliato)

1. Verifica in locale:
   ```bash
   cd web && npm ci && npm run lint && npm run build
   ```
2. Committa e pusha su `main`:
   ```bash
   cd .. && git add web/src && git commit -m "feat(web): ..." && git push origin main
   ```
3. Il flusso è `push su main → integration-fast → deploy.yml (solo se la CI
   è verde) → Vercel --prod → app.instaedit.org`.
4. Controlla l'esito:
   ```bash
   gh run list --repo Marcuss-ops/InstaeditLogin --workflow deploy.yml --limit 5
   gh run watch RUN_ID --repo Marcuss-ops/InstaeditLogin --exit-status
   ```

### 2. Deploy manuale (override della CI)

Se `integration-fast` fallisce per un test non correlato, esegui il manual
dispatch già previsto dal workflow:

```bash
gh workflow run deploy.yml --repo Marcuss-ops/InstaeditLogin --ref main
```

### 3. Deploy d'emergenza da questo computer (CI bloccata / CDN stantio)

Il dashboard Vercel può mostrare `Ready` mentre il CDN continua a servire un
bundle vecchio (bug noto dell'integrazione git-app, documentato in
`deploy.yml`). In quel caso usa la CLI Vercel con build pre-assemblata:

```bash
# Requisiti: VERCEL_TOKEN esportato nella shell.

cd /home/pierone/Projects/company/InstaeditLogin

# 1) Build con l'API base di produzione (mai localhost!)
VITE_API_BASE_URL=https://api.instaedit.org npm --prefix web run build

# 2) Assembla l'output prebuilt (project instaedit-login-267l)
mkdir -p .vercel/output/static && rm -rf .vercel/output/static/*
cp -r web/dist/. .vercel/output/static/
# .vercel/project.json deve contenere:
#   {"projectId":"prj_YqO5G5jSkFpMrgTQqPShOIaGQhar","orgId":"ekM71HTZxivI7VJK8n2kWiZJ"}

# 3) config.json già presente in .vercel/output/ (redirect + proxy /api + SPA)

# 4) Deploy in produzione
npx --yes vercel@latest deploy --prebuilt --prod
```

Il deploy deve rispondere `Ready in ~7s`. **Dopo un deploy d'emergenza,
committa il codice**: produzione e repo devono restare allineati.

### Verifica dopo ogni deploy frontend

```bash
curl -fsSIL https://app.instaedit.org/            # HTTP/2 200, server: Vercel
curl -fsSL https://app.instaedit.org/ | rg -o 'assets/[^"]+\.js'   # hash nuovo?
# confronta con web/dist/assets/ — l'hash deve coincidere
curl -fsSL https://app.instaedit.org/assets/Groups-*.js | grep -c 'Analizza lingue' # >0
```

Poi **hard refresh due volte** (Ctrl+Shift+R) nel browser: la cache locale
può tenere il bundle precedente.

## Operazioni comuni

| Operazione | Comando |
| --- | --- |
| Aggiornare backend | `git pull && INSTAEDIT_ENV_FILE=.env.dev docker compose --env-file .env.dev up -d --build` |
| Deploy frontend normale | push su `main` (vedi §1) |
| Deploy frontend d'emergenza | vedi §3 (CLI `vercel deploy --prebuilt --prod`) |
| Rilevare/assegnare lingue dai titoli | `go run -tags=channellang ./scripts/apply_channel_languages.go --database-url "$DSN"` (dry-run) e `--apply` per scrivere |
| Log API | `docker logs instaedit-api --tail 100` |
| Stato worker | `curl -s https://api.instaedit.org/ready` |

## Regole d'oro (errori già visti)

1. **Mai cambiare `ENCRYPTION_KEY`** senza procedura di rotazione: i token
   OAuth salvati non si decifrano più (incidente del 2/8). Se devi verificare
   che la chiave corrente sia giusta, usa un probe con `internal/crypto`
   contro i blob della tabella `tokens`.
2. **Mai ricostruire lo stack con un env diverso da `.env.dev`** su questa
   macchina: cambia `ENCRYPTION_KEY`, `JWT_SECRET` e le credenziali S3 in
   produzione.
3. **CI `integration-fast` cancellata = deploy saltato.** I push rapidi
   cancellano la run in corso (`cancel-in-progress`); se il deploy non parte,
   usa il manual dispatch o il deploy d'emergenza.
4. **Il dashboard Vercel può mentire**: verifica sempre l'hash del bundle
   servito dal CDN, non lo stato della deployment.
5. **DB:** i rebuild non toccano il volume `instaeditlogin_instaedit_pg_data`.
   Per il backup/restore vedi `docs/operations-runbook.md`.

## Rollback

- **Frontend:** ridistribuisci il commit precedente (push di un revert, o
  manual dispatch, o §3 con il vecchio `web/dist` ricostruito da quel commit).
- **Backend:** `git checkout <sha-precedente> && INSTAEDIT_ENV_FILE=.env.dev
  docker compose --env-file .env.dev up -d --build`. Le migrazioni sono
  idempotenti; per il ripristino dati segui `docs/operations-runbook.md`.

## Riferimenti

- `docs/DEPLOY.md` — runbook canonico deploy (topologia, cutover)
- `docs/DEPLOY_FRONTEND.md` — dettagli workflow Vercel/CI
- `docs/OPERATIONS.md` — hub operativo (DNS, cert, monitoring, recovery)
- `docs/LOCAL-DEVELOPMENT.md` — stack locale
