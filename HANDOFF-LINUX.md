# Handoff — Setup locale Linux per login funzionale

Stato corrente: commit + push eseguiti su `main` da Windows. Su Linux segui i passi qui sotto per portare il backend live, completare il flow OAuth Meta end-to-end, e verificare che il login funzioni nel browser.

> Tutto è partito da qui: il frontend ora riconosce il banner "Vercel stale deployment" e mostra i 3 pitfall classici di `VITE_API_BASE_URL`. Il backend ha validator fail-fast su tutti i secret. Manca solo tu — l'operatore — che compili i 3 campi secret di Meta/Database e premi "start".

---

## 1. Pull del codice

```bash
git clone git@github.com:Marcuss-ops/InstaeditLogin.git
# oppure, se hai già il repo:
cd InstaeditLogin
git checkout main
git pull
```

## 2. Tool runtime da verificare

| Tool | Versione minima | Check |
|------|-----------------|-------|
| Go | 1.26+ | `go version` |
| Node | 22+ (per il frontend) | `node --version` |
| npm | bundled with Node | `npm --version` |
| openssl | 3.x | `openssl version` |
| psql | 15+ (per le migrations) | `psql --version` |
| PostgreSQL server | 15+ | vedi sotto |

Se `psql` manca, puoi installare solo il client (bastano pochi MB) o usare lo stesso Postgres server per migration + backend.

## 3. PostgreSQL — scegline uno

### Opzione A — Postgres locale (più veloce, no registrazioni)

```bash
# Ubuntu/Debian
sudo apt install postgresql postgresql-contrib
sudo systemctl start postgresql

# macOS (Homebrew)
brew install postgresql@15
brew services start postgresql@15

# Crea db + user
sudo -u postgres psql <<'SQL'
CREATE USER instaedit WITH PASSWORD 'instaedit_dev_pwd';
CREATE DATABASE instaedit_login OWNER instaedit;
GRANT ALL PRIVILEGES ON DATABASE instaedit_login TO instaedit;
SQL
```

`DATABASE_URL` risultante: `postgresql://instaedit:instaedit_dev_pwd@localhost:5432/instaedit_login?sslmode=disable`

### Opzione B — Supabase free tier (zero-config, 500MB gratis)

1. https://supabase.com → Sign up → New project
2. Settings → Database → Connection string → **URI** mode
3. Copia la URL — è già in formato `postgresql://postgres:[PASSWORD]@aws-0-eu-west-1.pooler.supabase.com:6543/postgres`

### Opzione C — Neon free tier (serverless, scale-to-zero)

1. https://neon.tech → Sign up → Create project
2. Copia la connection string: `postgresql://username:password@ep-xxx.region.aws.neon.tech/neondb?sslmode=require`

## 4. Crea `.env` con i secret

```bash
cp .env.example .env
```

Poi modifica `.env` e riempi **3 campi obbligatori**:

| Campo | Valore |
|-------|--------|
| `DATABASE_URL` | (la URL del punto 3) |
| `META_APP_ID` | (vedi punto 5) |
| `META_APP_SECRET` | (vedi punto 5) |

I due secret locali (`JWT_SECRET` ed `ENCRYPTION_KEY`) sono già stati generati da Windows e scritti in `.env`. Se vuoi rigenerarli su Linux (consigliato — i secret Windows non sono nel repo):

```bash
# Genera nuovi secret
echo "JWT_SECRET=$(openssl rand -hex 32)" >> .env
echo "ENCRYPTION_KEY=$(openssl rand -base64 32)" >> .env
# Poi rimuovi le righe JWT_SECRET=... e ENCRYPTION_KEY=... esistenti
# dal .env, lasciando solo le nuove.
```

**Validator attivo** — se uno qualsiasi di questi è mancante/sbagliato, `go run cmd/server/main.go` rifiuta di partire con un messaggio specifico:
- `JWT_SECRET < 32 byte` → "got X; expected 32"
- `ENCRYPTION_KEY` non 32 byte decodificati → "got X; expected 32"
- `META_APP_SECRET < 32 char` → "got X; must be at least 32"
- `META_*` key-without-secret o viceversa per platform opzionali → "TIKTOK_CLIENT_SECRET is required"

## 5. Meta Developer Console (obbligatorio per login)

1. https://developers.facebook.com → **My Apps** → **Create App** (tipo "Other" → "Next")
2. **Settings → Basic**:
   - Copia **App ID** → mettilo in `META_APP_ID`
   - Copia **App Secret** (clicca "Show") → mettilo in `META_APP_SECRET`
3. **Use cases** → configura **"Authenticate and request data from users with Facebook Login"** (o simile per Instagram)
4. **Facebook Login for Business** (o **Facebook Login** → **Settings**) → **Valid OAuth Redirect URIs**:
   ```
   http://localhost:8080/api/v1/auth/meta/callback
   ```
   Salva.
5. Roles → aggiungi il tuo Facebook account come **Developer** o **Tester** (l'app è in Development mode di default — solo Testers possono fare login).
6. Per Instagram publishing (opzionale ma utile), collega un **Instagram Business account** all'app.

## 6. Avvia il backend

```bash
# Verifica migrations (se il db è appena creato, le migrations devono girare)
psql "$DATABASE_URL" -f db/migrations/001_init.sql
psql "$DATABASE_URL" -f db/migrations/002_add_refresh_token.sql

# Avvia
go run cmd/server/main.go
```

Output atteso:
```
verify-api-base-url [OK]  VITE_API_BASE_URL = ... (context: local)
msg="Router configured" auth_mode="strict (Bearer required)" strict_jwt_auth=true
msg="listening" addr=0.0.0.0:8080
```

## 7. Verifica backend (3 sanity check rapidi)

```bash
# (1) Health endpoint
curl -sS http://localhost:8080/api/v1/health | python -m json.tool
# Atteso: {"status":"ok","service":"InstaEdit","version":"2.0.0","platforms":["meta"]}

# (2) Meta OAuth login reindirizza a Facebook (302)
curl -sI http://localhost:8080/api/v1/auth/meta/login
# Atteso: HTTP/1.1 302 Found + Location: https://www.facebook.com/v18.0/dialog/oauth?...

# (3) Protected route rifiuta senza JWT (401)
curl -sI http://localhost:8080/api/v1/accounts
# Atteso: HTTP/1.1 401 Unauthorized
```

Tutti e 3 verdi = backend OK.

## 8. Avvia il frontend

```bash
cd web
echo "VITE_API_BASE_URL=http://localhost:8080" > .env
npm install
npm run dev
```

Output atteso:
```
verify-api-base-url [OK]  VITE_API_BASE_URL = http://localhost:8080 (context: local)
  VITE v8.1.1  ready in 380 ms
  ➜  Local:   http://localhost:5173/
```

Apri http://localhost:5173 nel browser.

## 9. Test login Meta end-to-end

1. http://localhost:5173 → click **"Login with Meta"**
2. Il browser naviga a `http://localhost:8080/api/v1/auth/meta/login` → 302 verso Facebook
3. Login su Facebook come Tester (l'utente aggiunto in punto 5)
4. Autorizza l'app
5. Facebook rimanda a `http://localhost:8080/api/v1/auth/meta/callback?code=...`
6. Backend scambia `code` per `access_token` → emette JWT → 302 verso `${FRONTEND_URL}/auth/callback?jwt=...`
7. Frontend cattura il JWT dalla query string, lo salva in localStorage, naviga a `/dashboard`

A questo punto sei loggato. Vai su `/status` per vedere:
- Probe banner **verde** (backend healthy)
- Latency
- Auth boundary: `✓ 401 (CORS + middleware healthy)`
- Stats: cache hit rate

## 10. Troubleshooting

### Backend non si avvia — "JWT_SECRET must be at least 32 bytes"
Il validator del backend è fail-fast. Controlla che `JWT_SECRET=` abbia esattamente 64 caratteri hex (32 byte). Rigenera con `openssl rand -hex 32`.

### Meta login fallisce — "App Not Setup"
Hai dimenticato di aggiungerti come Developer/Tester in Meta App → Roles. Oppure l'app è in Live mode ma non hai Marketing/Instagram permissions richieste.

### Meta login reindirizza a "Invalid Redirect URI"
Hai dimenticato di aggiungere `http://localhost:8080/api/v1/auth/meta/callback` in Facebook Login → Settings → Valid OAuth Redirect URIs. Vedi punto 5.

### Frontend 404 su `/favicon.ico`
Il prebuild hook rigenera `web/public/favicon.ico` ad ogni build. Se stai servendo direttamente da `vite dev`, il file è servito al volo. Se stai buildando, `npm run build` lo copia in `dist/favicon.ico`. Se ancora 404, verifica che `vite.config.ts` abbia il plugin `verifyApiBaseUrlPlugin` e che il `prebuild` script in `package.json` esista.

### /status page mostra banner rosso
Vai su `/status` e leggi la ragione (es. `vercel_stale_deploy` vs `unreachable` vs `timeout`). I 3 pitfall classici sono documentati in `README.md` sezione `## Deployment`.

## 11. (Opzionale) Setup produzione

Dopo che il flow locale funziona, per andare in produzione:
- **Backend**: deploy su Railway/Render/Fly con `DATABASE_URL` + tutti i secret come env vars sul servizio
- **Frontend**: deploy su Vercel (già configurato via `web/vercel.json`)
- **Vercel env**: `VITE_API_BASE_URL` deve puntare all'URL pubblica del backend
- **CORS**: nel backend `.env`, `CORS_ALLOWED_ORIGINS=https://instaedit.org,https://www.instaedit.org`
- **Meta redirect URI**: aggiungi `https://api.instaedit.org/api/v1/auth/meta/callback` alla console Meta

---

## File di riferimento

- `internal/config/config.go` — env validation fail-fast
- `internal/auth/jwt.go` — middleware strict/legacy modes
- `pkg/api/routes.go` — router + CORS config
- `web/src/lib/auth.ts` — `authedFetch()`, `probeBackend()`, JWT helpers
- `web/src/lib/probe-cache.ts` — cache 5min per /health + force-clear
- `web/src/lib/probe-display.ts` — banner copy per ogni `ProbeFailureReason`
- `web/src/pages/Status.tsx` — UI completa del probe + fallback banner
- `web/scripts/verify-api-base-url.ts` — build-time validator
- `web/scripts/generate-favicon-ico.mjs` — prebuild ICO generator
- `README.md` — sezione "Deployment" con i 3 pitfall

Buon login! 🚀
