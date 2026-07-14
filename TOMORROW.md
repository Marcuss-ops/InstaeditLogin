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
# 1. Rimuovi i 13 secret non più necessari da required-fly-secrets.txt
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
