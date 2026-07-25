# 📋 Tomorrow — Cosa fare quando riapri

> File di onboarding operativo per la sessione di domani. Tutto è già
> committato in `main`. Tu devi solo: (1) deployare la demo su Vercel,
> (2) decidere se aspettare Fly o passare a un VPS, (3) ruotare la key
> Tigris che hai leakato in chat (sì, sempre, lo so).

---

## ⏱️ Quick wins (15 min totali)

### 1. [5 min] **Salva i 3 secret in 1Password**
Sono ancora in chiaro sul tuo filesystem. Catturali ADESSO prima di
perderli (chiudi il terminale = li perdi).

```bash
cat /tmp/tmp.f5JXPZCgvy
shred -u /tmp/tmp.f5JXPZCgvy
```

| Variabile | Dove salvarla |
|---|---|
| `JWT_SECRET=aa4706fd8c215aecd52a708ad046fe03c87f1e78895da93802ba4c1733ea8c7a` | 1Password → `instaedit-login/jwt-secret/production` |
| `ENCRYPTION_KEYS=1:JqNrXo7KlhfZTGt8lRtxnhe0MNekPZQCtgP2qDvnmyo=` | 1Password → `instaedit-login/encryption-key-1/production` |
| `ACTIVE_ENCRYPTION_KEY_ID=1` | 1Password → `instaedit-login/active-encryption-key-id/production` |
| `ADMIN_INVITE_TOKEN=d94c3b3784f67c7cde4479171935529f0fe14d0ac7000365a426fc36bd4ea9f0` | 1Password → `instaedit-login/admin-invite-token/production` |

### 2. [2 min] **Ruota la chiave Tigris leakata**
Sì, lo so, "non me ne frega" — ma la chat history è persistente e
chiunque la legga può scrivere/leggere/cancellare file nel tuo
bucket. 2 minuti e sei a posto.

1. https://console.storage.dev → **Access Keys**
2. Revoca `Testkey`
3. Crea nuova `instaedit-prod-2026-07-15`
4. Salva tid_/tsec_ in 1Password → `instaedit-login/s3-{access,secret}-key/production`
5. **MAI** incollarla in chat / issue / commit

### 3. [8 min] **Deploy della demo su Vercel** ⭐ PRIORITÀ
Questo è l'unica cosa utile che puoi fare oggi con Fly bloccato.

1. https://vercel.com → InstaeditLogin → **Settings → Environment Variables**
2. Aggiungi:
   - Key: `VITE_DEMO_MODE`     Value: `true`
   - Key: `VITE_API_BASE_URL`  Value: `https://api.instaedit.invalid`
     (qualcosa di sintatticamente valido, non importa che esista)
3. **Deployments** → ultimo commit → **⋯** → **Redeploy**
4. Aspetta 1-2 min (build verde = tutto ok)
5. Apri l'URL Vercel → dovresti vedere:
   - [ ] Banner **arancione "Demo mode"** in alto
   - [ ] Landing su `/accounts` (no login richiesto)
   - [ ] "Welcome, Demo User"
   - [ ] "No connected accounts" → bottone **Connect more accounts**
   - [ ] `/connections` → 7 provider cards, click → toast "Connect requires backend"
   - [ ] `/compose` → form visibile, submit → toast
   - [ ] `/posts` → "No posts yet"
   - [ ] `/settings/api` → tutti i tab funzionano (API keys, webhooks)

Se tutto si vede: **hai un SPA navigabile, screenshot-ready, da far
vedere a chiunque** (investitori, designer, amici, gatto). ✅

---

## 🎯 Decisione del giorno (10 min di pensiero, 0 comandi)

Scegli UNO dei tre path. **Non iniziare a lavorare sui due che non scegli.**

### Path A: "Aspetto che Fly si sblocchi"
Se pensi che Fly ti riaccetti il pagamento entro 1-2 settimane (carta
rifiutata spesso = errore temporaneo, non ban permanente).

→ Vedi sezione [Se Fly funziona](#se-fly-funziona) in fondo.

### Path B: "Droppo tutto tranne Meta + Vercel" (raccomandato per la beta privata)
L'idea: 14 secret, non 27. Solo Meta come provider. Lanci la beta
con Instagram/Facebook/Threads, il resto lo aggiungi dopo.

→ Vedi sezione [Path B — beta privata solo Meta](#path-b--beta-privata-solo-meta).

### Path C: "Basta cloud, mi prendo un Hetzner e ci metto tutto"
L'idea: €30/mese su Hetzner CCX13, Docker Compose con Go API + worker
+ Postgres + MinIO + Caddy. Zero vendor lock-in, dati tuoi.

→ Vedi sezione [Path C — self-hosted su Hetzner](#path-c--self-hosted-su-hetzner).

---

## 📌 Se Fly funziona (riprendi il piano originale)

```bash
# 1. Genera (se non l'hai già fatto) i 3 secret locali
openssl rand -hex 32   # JWT_SECRET
openssl rand -base64 32 # ENCRYPTION_KEYS=1:<questo>
openssl rand -hex 32   # ADMIN_INVITE_TOKEN

# 2. Crea l'app Fly
flyctl auth login
flyctl apps create instaedit-login
flyctl postgres create --name instaedit-production --region iad \
  --vm-size shared-cpu-1x --volume-size 1 --ha-replica-count 1
flyctl postgres attach instaedit-production --app instaedit-login

# 3. Crea .env.production (NON committare)
cp .env.example .env.production
# Riempi TUTTI i 27 secret, tra cui:
#   - JWT_SECRET, ENCRYPTION_KEYS, ACTIVE_ENCRYPTION_KEY_ID, ADMIN_INVITE_TOKEN
#   - S3_ACCESS_KEY, S3_SECRET_KEY (la NUOVA, post-rotazione)
#   - DATABASE_URL (POOLED, dall'output di flyctl postgres create)
#   - META_APP_ID, META_APP_SECRET (dalla Meta Dev Console)
#   - Le 7 redirect_uri (pubbliche, già in fly.toml)

# 4. Push + deploy
make fly-secrets-dry-run    # deve uscire 0
make fly-secrets            # stage su Fly
make fly-secrets-verify     # conferma 27/27
make fly-verify             # sanity-check fly.toml
make fly-deploy             # build + migrate + rollout

# 5. Verifica live
curl https://api.instaedit.org/api/v1/health   # → 200
curl https://api.instaedit.org/ready           # → 200, workers_ready: true

# 6. Vercel: togli VITE_DEMO_MODE, reimposta VITE_API_BASE_URL=https://api.instaedit.org
# → Redeploy
```

Tempo stimato: 1-2 ore se fila liscio.

---

## 🥇 Path B — beta privata solo Meta

L'obiettivo: 14 secret invece di 27, una sola integrazione OAuth, vai
in produzione in mezza giornata.

### Cosa droppi
- TikTok (richiede App Review 2-4 settimane)
- YouTube (richiede OAuth consent screen verification)
- LinkedIn (richiede product approval)
- X/Twitter (richiede App Review 1-2 settimane)
- Stripe (non monetizzi ancora)
- Resend (non wirato, non serve)
- Sentry (non wirato, usa i logs)

### Cosa tieni
- Vercel ✅
- Meta (Instagram + Facebook + Pages) ✅
- Postgres (Fly) ✅
- S3 / Tigris ✅
- JWT + ENCRYPTION_KEYS + ADMIN_INVITE_TOKEN ✅

### Step pratici

```bash
# 1. Rimuovi i 13 secret non più necessari dalla hosted-platform secrets manifest
#    (LinkedIn, TikTok, YouTube, X — 12 secret + EMAIL_PROVIDER_KEY = 13)
#    → portalo da 27 a 14

# 2. Crea l'app Meta business (https://developers.facebook.com/apps)
#    Aggiungi "Facebook Login for Business"
#    Redirect URIs:
#      https://api.instaedit.org/api/v1/auth/instagram/callback
#      https://api.instaedit.org/api/v1/auth/facebook/callback
#      https://api.instaedit.org/api/v1/auth/threads/callback
#    → copia META_APP_ID + META_APP_SECRET

# 3. Fly + .env.production + make fly-secrets + make fly-deploy

# 4. Crea il primo utente (da te, manualmente, via DB o endpoint protetto)

# 5. Invita 5-10 amici fidati → beta privata
```

---

## 🏠 Path C — self-hosted su Hetzner

L'obiettivo: €30/mese tutto compreso (VPS + dominio + backup), zero
dipendenze "in forse", dati tuoi.

### Cosa ti serve
1. Hetzner Cloud → Cloud Server CCX13 (4 vCPU / 16GB / 160GB NVMe) → €30/mese
2. Dominio (già hai `instaedit.org`)
3. 1 ora per il setup iniziale

### Cosa installi
- **Docker + Docker Compose**
- **Caddy** (reverse proxy + Let's Encrypt automatico)
- **Go binary** (build del `Dockerfile` esistente)
- **Postgres 16** (volume persistente, WAL archiving giornaliero su Hetzner Storage Box €3/mese)
- **MinIO** (S3-compatible, docker image ufficiale)
- **(opzionale) Postfix** per email

### Step pratici
1. Crea account Hetzner + carta valida (carta italiana funziona)
2. `hcloud server create --name instaedit --type ccx13 --image ubuntu-24.04 --location nbg1`
3. Punta `api.instaedit.org` e `app.instaedit.org` all'IP del server
4. SSH dentro, installa Docker
5. (Quando pronto) chiedimi: ti genero `docker-compose.yml` con tutto

---

## 🌅 Se hai tempo / vuoi fare altro

### Aggiungi seed data alla demo (per screenshot più belli)
I mock attualmente ritornano array vuoti. Se vuoi che la demo mostri
"1 Instagram connesso + 2 post di esempio" chiedimelo e aggiungo
qualche fixture in `web/src/lib/demo.ts`. Tempo: 5 min.

### Aggiungi il job "MinIO → Drive per cold storage"
Solo se hai scelto Path C. È un worker Go che periodicamente fa
`mc mirror minio/bucket drive:/InstaEdit-Archive`. 30 min di codice.

### Self-host email (Postfix)
Se vuoi inviare magic-link dalla tua VPS, configurazione Postfix +
DKIM + SPF + DMARC. Mezza giornata. Per ora skippa — l'email
transazionale non è wirata comunque.

### Self-host error tracking (GlitchTip)
Solo se diventi grande. Per ora `fly logs` basta.

---

## 📊 Stato del progetto ad oggi

| Cosa | Stato |
|---|---|
| Codice Go (API + worker) | ✅ pronto |
| Codice React (SPA) | ✅ pronto |
| Demo mode (frontend senza backend) | ✅ pronto (test 178/178 verdi) |
| Tigris bucket creato | ✅ ma key leakata |
| Fly payment | ❌ rifiutato |
| Meta app | ⏸️ non ancora creata |
| DNS api.instaedit.org | ⏸️ non ancora configurato |
| Beta privata | ⏸️ bloccata su Fly |
| Migrazione VPS | ⏸️ opzionale |

---

## 🆘 Se ti blocchi

Mandami l'output del comando + la riga esatta dell'errore. Ti rispondo
con il prossimo comando o il fix. No screenshot, no copia-incolla di
secret, no panico.

**Buona sessione domani. 🚀**

---

## 🔍 Tigris-vs-MinIO cutover audit (2026-07-25, post-Fly-destroy)

> **Verdict: ⚠️ NOT YET SAFE TO REMOVE TIGRIS.** Code-side ✅ GREEN;
> bucket-side + VPS runtime ⏸️ UNCLEAR (needs operator execution on VPS).

### Audit gate (4-step user spec from chat)

| # | Step | Verifier | Result |
|---|------|----------|--------|
| 1 | VPS-side `docker compose exec minio mc ls minio/instaedit-local` | operator SSH | ⏸️ UNCLEAR |
| 2 | Tigris bucket listing (`aws --endpoint https://t3.storage.dev s3 ls s3://instaedit-prod-media/ --recursive`) | operator + Tigris creds | ⏸️ UNCLEAR |
| 3 | Local grep: `t3.storage.dev` \| `tigris` \| `FLY_STORAGE` in code/compose/env/scripts | sandbox | ✅ GREEN |
| 4 | Bucket parity diff (1 vs 2) | post 1+2 | ⏸️ BLOCKED — depends on 1+2 |

### Code-side detail (executed from this sandbox)

**Portabile / nessuna ref hard-coded:**
- `internal/config/config.go:583` — `S3Endpoint: getEnv("S3_ENDPOINT", "")`
  completamente env-driven, nessun default Tigris.
- `internal/services/storage.go` —comment: `S3-compatible — requires S3_ENDPOINT + S3_BUCKET + S3_ACCESS_KEY + S3_SECRET_KEY` (neutro rispetto al provider).
- `docker-compose.yml` — solo servizio `minio`. Nessun servizio `tigris`.
- Tests (`internal/services/youtube_oauth_resume_test.go:381+`): puntano a `http://localhost:9000` (MinIO locale).
- `internal/worker/upload_worker.go` — nessun riferimento a `t3.storage.dev` o `FLY_STORAGE` trovato.

**Riferimenti Tigris rimasti (intenzionali):**
- `scripts/s3/provision-tigris.sh` — script di provisioning del bucket
  `instaedit-prod-media` (default endpoint `https://t3.storage.dev`).
- `scripts/ops/post_deploy_smoke.sh:10,40,206` — Phase 9.4 Tigris presigned-PUT
  smoke test (§B.5).
- `docs/DEPLOY.md:373,471-498,817` (§10) — playbook di ritiro Tigris (la
  this-section è la documentazione del piano di migrazione, NON codice vivo).
- `docs/OPERATIONS.md:192,206,514-520,767` — Tigris marcato come legacy +
  rollback window note.

### Operator pre-destroy checklist (sequenza verificabile)

```bash
# ─── 1. Snapshot VPS-side MinIO ──────────────────────────────────
ssh root@51.91.11.36
docker compose exec minio mc ls minio/instaedit-local --recursive \
  | tee /tmp/minio-listing.txt
docker compose exec minio mc ls --summarize minio/instaedit-local \
  | tee /tmp/minio-summary.txt

# ─── 2. Snapshot Tigris ─────────────────────────────────────────
# Da locale, con le Tigris key di 1Password (NON incollare in chat)
AWS_ACCESS_KEY_ID=$tig_key AWS_SECRET_ACCESS_KEY=$tig_sec \
  aws --endpoint https://t3.storage.dev s3 ls s3://instaedit-prod-media/ --recursive \
  | tee /tmp/tigris-listing.txt

# ─── 3. Bucket parity diff ──────────────────────────────────────
diff /tmp/minio-listing.txt /tmp/tigris-listing.txt
# Exit 0 = set uguali. Exit 1 = ci sono file solo da un lato.

# ─── 4. VPS .env: conferma S3_ENDPOINT punta a MinIO ────────────
grep -E '^S3_ENDPOINT' /opt/instaedit/.env.production
# Atteso: S3_ENDPOINT=http://minio:9000 (o https://minio.instaedit.local)
# Se punta ancora a https://t3.storage.dev → BONK prima di toccare Fly.

# ─── 5. Smoke test §B.5 contro MinIO ────────────────────────────
bash scripts/ops/post_deploy_smoke.sh
# Atteso: "/api/v1/media/presign: 200 + presigned URL + key=... (Tigris
# signed PUT OK)". Se il nome dello smoke dice ancora "Tigris" ma il
# flusso passa, OK; altrimenti rinomina §B.5 in "S3 signed PUT OK".
```

### Decisione finale

- Se TUTTI i 5 punti sopra sono verdi → `Tigris-safe-to-remove: ✅`
- Altrimenti → esegui prima `docs/DEPLOY.md` §10 (mc mirror Tigris → MinIO).

### Perché questo blocco è pre-destroy

`scripts/destroy-fly-app.sh` ha Tigris marcato come **OUT-OF-SCOPE**
intenzionalmente (riga 27, 69): tocca solo l'app `instaedit-login` su
Fly, non bucket esterni. Quindi **distruggere Fly NON rompe Tigris** —
la domanda "Tigris è ancora necessario?" è ortogonale al Fly cutover
ed è un'operazione di igiene storage separata.

### Riferimenti interni

- Manuale migrazione: `docs/DEPLOY.md` §10 (Tigris retirement)
- Audit log destroy: `scripts/destroy-fly-app.sh --apply` (NON tocca Tigris)
- Runbook smoke test: `scripts/ops/post_deploy_smoke.sh` §B.5

---

## 🩹 Code-side disambiguation: `internal/worker/upload_worker.go:910`

> **Verifica:** il match `tigris` nelle `*.go` cade dentro
> `classifyUploadError`, NON in un endpoint hard-coded.

La riga esatta:

```go
// internal/worker/upload_worker.go (~line 910)
case containsAny(s, "s3", "tigris", "minio", "presigned"):
    return "s3_error"
```

**Cosa fa realmente:** `containsAny` è un helper di substring-match
usato da `classifyUploadError` per classificare gli error runtime in
una tassonomia stabile (`drive_error`, `s3_error`, `youtube_error`,
`auth_error`, `timeout`, …). Le stringhe `"s3"`, `"tigris"`,
`"minio"`, `"presigned"` sono **needle** nell'err.Error() — NON
endpoint di storage.

**Implicazioni per il cutover:**

1. **Nessun data-plane coupling.** La classifier non legge da Tigris;
   mappa solo errori. Post-cutover (Tigris rimosso) la voce `tigris`
   è dead-but-harmless: nessun errore runtime la triggera più.
2. **Nessun refuso end-user.** Il match non trapela in UI né in
   payload response — solo nel campo `error_code` della riga in DB.
3. **Cleanup consigliato (post-cutover, NON blocker):** rimuovere
   `"tigris"` dagli needles di `containsAny`. È un refactor cosmetico,
   non un correctness fix. Tracked come followup dopo Destroy Fly.

**Conclusione:** code-side ✅ GREEN confermato. L'evidenza non
obbliga ad azione immediata; richiede solo un futuro cleanup cosmetico.

---

## 🛡️ Audit conditions mancanti (integrate dopo code-review)

Il primo cut della checklist operatoriale copriva solo la **bucket
parity**. Mancano 4 condizioni di **runtime/contract parity** che, se
non verdi, possono rompere il cutover dal lato utente senza rompersi
dal lato bucket. Aggiunte al gate:

### 6. CORS policy diff (Tigris vs MinIO)

```bash
# Tigris default (Storage dashboard → bucket → CORS):
#   - AllowOrigins: ["*"]
#   - AllowMethods: ["GET","PUT","POST","DELETE","HEAD"]
#   - AllowHeaders: ["*"]
#   - ExposeHeaders: ["ETag","Content-Length","Content-Type"]
#   - MaxAgeSeconds: 3600

# MinIO default su docker-compose: NESSUNA CORS rule.
# La SPA in https://app.instaedit.org (Vercel) non potrà fare
# presigned-URL PUT/GET se MinIO non espone la CORS rule all'origine
# Vercel.

# FIX sulla VPS (3 step; ATTENZIONE: heredoc-bare non funziona perché
# `docker compose exec` non vede il filesystem dell'host — serve docker cp
# OR stdin via `-T`):
cat > /tmp/cors-minio.json <<'EOF'
{
  "cors": {
    "0": {
      "allowedOrigins": ["https://app.instaedit.org","https://instaedit.org"],
      "allowedMethods": ["GET","PUT","POST","DELETE","HEAD"],
      "allowedHeaders": ["*"],
      "exposeHeaders": ["ETag","Content-Length","Content-Type"],
      "maxAgeSeconds": 3600
    }
  }
}
EOF
docker compose exec minio mc admin config import < /tmp/cors-minio.json
# `mc admin config import` legge da stdin, quindi l'host-side heredoc
# sopravvive il boundary host→container. Restart per ricaricare:
docker compose restart minio
```

### 7. Public-read bucket policy diff

```bash
# Se il bucket Tigris espone oggetti pubblici (avatar, thumbnail,
# post preview), MinIO deve avere la policy equivalente.

# Lista oggetti Tigris pubblici:
AWS_ACCESS_KEY_ID=$tig_key AWS_SECRET_ACCESS_KEY=$tig_sec \
  aws --endpoint https://t3.storage.dev s3api list-objects-v2 \
  --bucket instaedit-prod-media --query 'Contents[?contains(Key, `public/`)]'

# Replica su MinIO (VPS):
docker compose exec minio mc anonymous set download minio/instaedit-local
# (oppure solo prefix-specifici se serve granularità)
```

### 8. S3_REGION / S3_USE_SSL parity

```bash
# Tigris: HTTPS + region "auto" (o "us-east-1").
# MinIO compose: HTTP + region "us-east-1" (placeholder).
# Se il Go client ha `S3_USE_SSL` o `S3_REGION` env-driven, devono
# essere settati correttamente per il nuovo endpoint.

# Verifica runtime sulla VPS:
grep -E '^S3_(REGION|USE_SSL|ENDPOINT|PATH_STYLE)' /opt/instaedit/.env.production
# Atteso (la VPS gira dentro docker-compose: HTTP single-host):
#   S3_ENDPOINT=http://minio:9000
#   S3_REGION=us-east-1              # placeholder per MinIO (ignorato)
#   S3_USE_SSL=false                 # solo se la Caddy termina TLS
#   S3_PATH_STYLE=true               # CRITICAL per MinIO single-host

# S3_PATH_STYLE è letto da `internal/config/config.go::StorageConfig.S3PathStyle`
# (bool) e passato al client S3 Go. Per single-host MinIO (compose) DEVE
# essere `true`; altrimenti il client costruisce URL virtual-hosted
# (`minio:9000.instaedit-local`) e fallisce la PUT silenziosamente.
```

### 9. Object-locking / versioning / lifecycle replication

```bash
# Se Tigris bucket ha lifecycle rules (es. expire-after-30d),
# Versioning ON, o Object Lock, vanno replicate su MinIO.

# Ispezione Tigris:
AWS_ACCESS_KEY_ID=$tig_key AWS_SECRET_ACCESS_KEY=$tig_sec \
  aws --endpoint https://t3.storage.dev s3api get-bucket-lifecycle-configuration \
  --bucket instaedit-prod-media
AWS_ACCESS_KEY_ID=$tig_key AWS_SECRET_ACCESS_KEY=$tig_sec \
  aws --endpoint https://t3.storage.dev s3api get-bucket-versioning \
  --bucket instaedit-prod-media

# Replica su MinIO (VPS). STEP:
# (a) Cattura lifecycle da Tigris in JSON pulito (AWS CLI v2 only — v1
#     emette XML anche dietro --endpoint):
AWS_ACCESS_KEY_ID=$tig_key AWS_SECRET_ACCESS_KEY=$tig_sec \
  aws --endpoint https://t3.storage.dev s3api get-bucket-lifecycle-configuration \
  --bucket instaedit-prod-media > /tmp/lifecycle-tigris.json
python3 -c 'import json,sys; json.load(open("/tmp/lifecycle-tigris.json"))' \
  || { echo 'FAIL: AWS CLI v1 emette XML — usa v2 (aws --version >= 2.0)'; exit 1; }

# (b) Import lifecycle + attiva versioning su MinIO. `mc ilm import`
#     legge da stdin (stesso workaround host→container descritto in §6):
docker compose exec minio mc ilm import minio/instaedit-local < /tmp/lifecycle-tigris.json
docker compose exec minio mc version enable minio/instaedit-local
```

### 10. KMS / SSE-S3 encryption parity

```bash
# Tigris può avere default SSE-S3 encryption (o custom KMS keys) sul
# bucket. MinIO NON lo eredita — va configurato esplicitamente.

# Ispezione Tigris:
AWS_ACCESS_KEY_ID=$tig_key AWS_SECRET_ACCESS_KEY=$tig_sec \
  aws --endpoint https://t3.storage.dev s3api get-bucket-encryption \
  --bucket instaedit-prod-media
# Output tipico: {"ServerSideEncryptionConfiguration":{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"AES256"}}]}}

# Replica su MinIO (BOTH server-default + per-bucket):
# (a) Default a livello di server (env in docker-compose.yml):
#     MINIO_API_KMS_SECRET_KEY=<base64-32-byte>   # opzionale; usa solo se KMS custom
docker compose exec minio mc admin config set /tmp/enc.json <<'EOF'
{ "encrypt": { "0": { "sse": { "algorithm": "AES256" } } } }
EOF
# (ATTENZIONE: stesso heredoc-host/container gotcha del §6 — se redisca
# problemi, usa il pattern docker cp descritto sopra.)

# (b) Default sul bucket specifico:
docker compose exec minio mc encrypt set sse-s3 minio/instaedit-local

# Se Tigris usa SSE-KMS custom: serve ri-cryptare gli oggetti esistenti
# con `rclone sync ... --s3-sse aws:kms --s3-sse-kms-key-id …`. Esula
# dal cutover; flaggato come follow-up post-mirror.
```

### Decisione finale (aggiornata)

- Se TUTTI i 10 punti (1-5 originali + 6-10 sopra) sono verdi → `Tigris-safe-to-remove: ✅`
- Altrimenti → esegui prima `docs/DEPLOY.md` §10 (mc mirror Tigris → MinIO).
- Se il bucket ha SSE-KMS custom (non SSE-S3 default) → served KMS-mirror
  post-cutover; non blocker, ma tracked come P1 follow-up.

---

## ⚠️ Cascade-destroy warning: Tigris Fly-managed

> Rischio non-Fly-side ma REALE: bucket Fly-managed può essere rimosso
> automaticamente da `fly apps destroy` anche se i nostri script
> sono OUT-OF-SCOPE in spirit.

**Problema:** `scripts/s3/provision-tigris.sh:10` documenta una
variante **Fly-managed**:

```
# For Fly.io's managed Tigris (regional),
# export S3_ENDPOINT=https://fly.storage.tigris.dev
```

Se il bucket `instaedit-prod-media` è stato creato con `fly storage
create` o `flyctl storage attach` (anziché indipendentemente dalla
dashboard t3.storage.dev), ALLORA `fly apps destroy --app
instaedit-login` **potrebbe cascade-rimuovere il bucket Tigris**
tramite teardown dei volumi Fly-attached.

`scripts/destroy-fly-app.sh` NON chiama esplicitamente `fly storage
...` quindi è out-of-scope in **codice**. Ma il teardown Fly potrebbe
comunque rimuovere il bucket come side-effect.

**Pre-destroy disambiguation obbligatoria:**

```bash
# 1. Sul Fly dashboard → "Storage" per l'app instaedit-login.
#    Lista bucket attached.

# 2. Comando CLI (operator-only):
flyctl storage list --app instaedit-login
# Se l'output include "instaedit-prod-media" come Fly-attached:
#   → bucket è Fly-managed → RISK CASCADE-DESTROY
# Altrimenti:
#   → bucket è standalone → destroy-fly-app.sh è sicuro

# 3. Se RISK CASCADE-DESTROY confermato, PRIMA del destroy:
#    a) Backup bucket locale MinIO (step 1-4 della checklist sopra)
#    b) Versioning Tigris ON prima del destroy:
#       aws --endpoint https://t3.storage.dev s3api put-bucket-versioning \
#         --bucket instaedit-prod-media --versioning-configuration Status=Enabled
#    c) Solo DOPO backup-and-confirmed: proceed destroy.
```

**Perché questo blocco è pre-destroy:** questo controllo **NON** è
coperto da `destroy-fly-app.sh` (che è giustamente out-of-scope). È
una decisione operator-side che va presa PRIMA di lanciare
`destroy-fly-app.sh --apply`.

### Riferimenti interni

- Manuale migrazione: `docs/DEPLOY.md` §10 (Tigris retirement)
- Audit log destroy: `scripts/destroy-fly-app.sh --apply` (NON tocca Tigris)
- Runbook smoke test: `scripts/ops/post_deploy_smoke.sh` §B.5
