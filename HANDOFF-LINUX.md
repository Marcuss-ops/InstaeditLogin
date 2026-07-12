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

## 5. Meta Developer Console (opzionale — solo se vuoi Instagram/Facebook/Threads)

1. https://developers.facebook.com → **My Apps** → **Create App** (tipo "Other" → "Next")
2. **Settings → Basic**:
   - Copia **App ID** → mettilo in `META_APP_ID`
   - Copia **App Secret** (clicca "Show") → mettilo in `META_APP_SECRET`
3. **Use cases** → configura **"Authenticate and request data from users with Facebook Login"** (o simile per Instagram)
4. **Facebook Login for Business** (o **Facebook Login** → **Settings**) → **Valid OAuth Redirect URIs**:
   ```
   http://localhost:8080/api/v1/auth/instagram/callback
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
msg="Router configured" jwt_ttl_hours=168
msg="listening" addr=0.0.0.0:8080
```

## 7. Verifica backend (3 sanity check rapidi)

```bash
# (1) Health endpoint
curl -sS http://localhost:8080/api/v1/health | python -m json.tool
# Atteso: {"status":"ok","service":"InstaEdit","version":"2.0.0","platforms":["instagram"]}

# (2) Meta OAuth login reindirizza a Facebook (302)
curl -sI http://localhost:8080/api/v1/auth/instagram/login
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
2. Il browser naviga a `http://localhost:8080/api/v1/auth/instagram/login` → 302 verso Facebook
3. Login su Facebook come Tester (l'utente aggiunto in punto 5)
4. Autorizza l'app
5. Facebook rimanda a `http://localhost:8080/api/v1/auth/instagram/callback?code=...`
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
Hai dimenticato di aggiungere `http://localhost:8080/api/v1/auth/instagram/callback` in Facebook Login → Settings → Valid OAuth Redirect URIs. Vedi punto 5.

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
- **Meta redirect URI**: aggiungi `https://api.instaedit.org/api/v1/auth/instagram/callback` alla console Meta

---

## 12. Dev vs Prod database isolation

**Regola d'oro**: una query di dev non deve MAI toccare righe di produzione. Le tre risorse da tenere separate:

| Risorsa | DEV (`APP_ENV=dev`) | PROD (`APP_ENV=production`) |
|---------|---------------------|----------------------------|
| Supabase project | `instaedit-dev` (free tier) | `instaedit-prod` (paid plan) |
| `DATABASE_URL` | `postgresql://postgres:[DEV-PW]@aws-0-eu-west-1.pooler.supabase.com:6543/postgres` | `postgresql://postgres:[PROD-PW]@aws-1-us-east-1.pooler.supabase.com:6543/postgres` |
| `SUPABASE_BUCKET` | `instaedit-dev-uploads` | `instaedit-prod-uploads` |
| `CORS_ALLOWED_ORIGINS` | `http://localhost:5173,http://localhost:4173` (no pubblici!) | `https://app.example.com,https://www.app.example.com` (no localhost!) |
| `JWT_SECRET` | un valore generato localmente | un valore separato, generato sul server di deploy |
| `ENCRYPTION_KEY` | un valore generato localmente | un valore separato (i token persistiti sono criptati con questa chiave!) |
| Meta OAuth app | una app Meta separata in Development mode | una app Meta separata in Live mode |

### Perché tre Supabase project e non uno con due branch?

Supabase **non supporta database branching** come Neon (richiede restauro da backup). Un dev che lancia `TRUNCATE posts CASCADE` su un db condiviso cancella anche i post di produzione. Due Supabase project separati sono l'unica opzione che protegge da questo.

### Come passare da dev a prod

**Opzione A — file `.env` per environment (consigliata per setup locale)**:
```bash
cp .env.example .env.dev
# modifica .env.dev con i valori dev (Supabase dev, localhost CORS, APP_ENV=dev)
cp .env.example .env.prod
# modifica .env.prod con i valori prod (Supabase prod, dominio pubblico CORS, APP_ENV=production)

# swap in base a cosa vuoi lanciare:
ln -sf .env.dev .env && go run cmd/server/main.go   # ora gira in dev
ln -sf .env.prod .env && go run cmd/server/main.go  # ora gira in prod
```

**Opzione B — env vars sul servizio di deploy**:
Su Railway / Render / Fly, configura due env group:
- `env:dev` con tutti i valori dev
- `env:prod` con tutti i valori prod

Il servizio di deploy carica il gruppo giusto in base al branch che promuovi (main = prod, ogni PR = preview con env:dev).

**Opzione C — secret manager**:
1Password, AWS Secrets Manager, GCP Secret Manager, HashiCorp Vault: ogni secret ha un ID tipo `instaedit-login/database-url/dev` e `instaedit-login/database-url/prod`. Il deploy script scarica il gruppo giusto in base a `APP_ENV`.

### Naming convention per i secret store (`_DEV_KEY` / `_PROD_KEY`)

Quando hai un secret che varia per environment (in particolare le chiavi service-role Supabase, le chiavi AWS, le chiavi di cifra), usa SEMPRE un suffisso nel nome del secret:

| Tipo | Secret ID nel manager | Valore |
|------|----------------------|--------|
| DB password | `instaedit-login/db-password/dev` | `[DEV-PASSWORD]` |
| DB password | `instaedit-login/db-password/prod` | `[PROD-PASSWORD]` |
| Supabase service-role key | `instaedit-login/supabase-service-key/dev` | `eyJ...DEV` |
| Supabase service-role key | `instaedit-login/supabase-service-key/prod` | `eyJ...PROD` |
| JWT secret | `instaedit-login/jwt-secret/dev` | `[32-byte dev]` |
| JWT secret | `instaedit-login/jwt-secret/prod` | `[32-byte prod, ≥64 byte]` |
| Encryption key | `instaedit-login/encryption-key/dev` | `[32-byte base64 dev]` |
| Encryption key | `instaedit-login/encryption-key/prod` | `[32-byte base64 prod]` |

Il suffisso `_DEV_KEY` / `_PROD_KEY` nel prompt dell'utente si riferisce a questa convenzione di naming (separare le due chiavi Supabase Service Key con un suffisso). Nel `.env` vero e proprio il backend legge un unico `SUPABASE_SERVICE_KEY`; il suffisso vive solo nel tuo secret store.

### Cosa succede se mischi gli ambienti

| Errore | Conseguenza |
|--------|-------------|
| Dev backend punta al DB prod | Un dev che fa testing vede post di utenti reali (privacy issue). L'opposto (prod DB password nel .env dev) cancella dati di prod. |
| Prod CORS include `http://localhost:5173` | Un attacker può hostare un sito su un dominio che 302-reindirizza al localhost dev per rubare JWT locali, ma più realisticamente: i dev possono accidentalmente vedere dati prod dal loro laptop. |
| Encryption key condivisa | Se la chiave dev è leaked, **tutti i token OAuth di prod sono decifrabili** (encryption_at_rest non è più una protezione). Genera sempre chiavi separate. |
| JWT secret condiviso | Un JWT firmato dev viene accettato anche dal backend prod → escalation orizzontale di privilegi. |
| CORS troppi origins | Più origini = più superficie d'attacco. Lista minima necessaria. |

### Verifica post-deploy

Dopo ogni deploy, esegui uno smoke test di isolamento:

```bash
# (1) Backend conferma APP_ENV in startup
curl -sS https://api.example.com/api/v1/health | python -m json.tool
# Atteso: il log precedente mostra "app_env":"production"

# (2) CORS blocca origine non in allowlist
curl -sI -H "Origin: https://evil.example.com" https://api.example.com/api/v1/health | grep -i access-control
# Atteso: NESSUNA riga `Access-Control-Allow-Origin` (rifiuto)

# (3) CORS accetta origine autorizzata
curl -sI -H "Origin: https://app.example.com" https://api.example.com/api/v1/health | grep -i access-control
# Atteso: `Access-Control-Allow-Origin: https://app.example.com`

# (4) Database NON è quello di dev
psql "$DATABASE_URL" -c "SELECT current_database();"
# Atteso: il db name contiene "prod" (NON "dev" / NON "instaedit_login" usato in locale)

# (5) Fail-fast non scatta (prova con APP_ENV=production)
APP_ENV=production DATABASE_URL=... JWT_SECRET=$(openssl rand -hex 32) go run cmd/server/main.go
# Atteso: il server parte (Taglio 1.1: nessun guard legacy)
```

### `.env.example` aggiornato

La sezione "APP_ENV", "Supabase Storage", e "CORS origins" del file `.env.example` in questo repo è stata aggiornata con esempi dev/prod side-by-side per supportare questa sezione. Leggi i commenti del file prima di copiare in `.env`.

---

## 13. Contract API (leggi prima di toccare gli handler `/workspaces` e `/posts`)

Tre dettagli del contract API che sono **bloccanti** — una modifica sbagliata rompe i test o la UI. Aggiunti di recente (vedi `pkg/api/posts.go` e `pkg/api/workspaces.go`).

### 13.1 422 vs 400 — quando usare quale

I handler `/workspaces` e `/posts` seguono una convenzione a due livelli:

- **400 Bad Request** = il body non è parseable come JSON, OPPURE un valore parseable è invalido (es. `status="bogus"` non corrisponde a nessun valore di `models.PostStatus`).
- **422 Unprocessable Entity** = il JSON è parseato OK, ma un campo semanticamente richiesto manca (es. `workspace_id=0`, `name=""`, `targets=[]`, oppure un target con `platform_account_id=0`).

Esempi concreti:

| Body | Risposta |
|------|----------|
| `POST /workspaces` con `{}` | 422 "name is required" |
| `POST /workspaces` con `"not json"` | 400 "invalid request body: ..." |
| `POST /posts` con `{"title":"x"}` (manca `workspace_id`) | 422 "workspace_id is required" |
| `POST /posts` con `{"workspace_id":1}` (manca `targets`) | 422 "at least one target is required" |
| `POST /posts` con `{"workspace_id":1,"targets":[{"platform_account_id":0}]}` | 422 "targets[0].platform_account_id is required" |
| `POST /posts` con `{"workspace_id":1,"status":"bogus"}` | 400 "status must be one of: draft, scheduled, publishing, published, failed" |

**Perché due livelli**: la SPA distingue "fix del tuo payload" (422 → mostra validation error sul form) da "fix della tua integrazione" (400 → logga un bug). Non collassare i due.

I test in `pkg/api/routes_test.go::TestHandleCreateWorkspace_MissingName_422`, `TestHandleCreatePost_MissingWorkspaceID_422`, `TestHandleCreatePost_NoTargets_422`, `TestHandleCreatePost_BadTargetID_422`, e il `TestPostsAPI_Create_BadStatus_400` in `pkg/api/posts_test.go` lockano il contract — qualsiasi regressione qui rompe `go test ./pkg/api/...`.

### 13.2 Auth JWT (Taglio 1.1)

L'identità arriva esclusivamente dal JWT di sessione (Bearer header o cookie
HttpOnly `session`). Non esiste alcun fallback a `user_id` dal body o dalla query.

L'helper unico è `requireUserID`:

```go
func requireUserID(w http.ResponseWriter, req *http.Request, r *Router) (int64, bool) {
    uid, ok := auth.UserIDFromContext(req.Context())
    if !ok || uid <= 0 {
        writeError(w, http.StatusUnauthorized, "missing user identity")
        return 0, false
    }
    return uid, true
}
```

Tutti gli endpoint protetti (workspaces, posts, publish, publish-all, accounts, storage)
chiamano `requireUserID` come prima riga. Nessun handler legge `user_id` dal body o dalla
query. I test ora iniettano un Bearer JWT via `issueTestJWT(t, 1)` invece di affidarsi al
fallback lenient (vedi `pkg/api/posts_test.go` e `pkg/api/workspaces_test.go`).

### 13.3 `createPostResponse` dual-shape (top-level + "post" key annidato)

`handleCreatePost` restituisce `201` con un body che soddisfa **due** decoder di test diversi (uno flat, uno nested):

```json
{
    "id": 100,
    "workspace_id": 1,
    "title": "hello",
    "caption": "world",
    "media_url": "",
    "scheduled_at": null,
    "status": "draft",
    "created_at": "2024-01-01T00:00:00Z",
    "post": {
        "id": 100, "workspace_id": 1, "title": "hello", "caption": "world",
        "media_url": "", "scheduled_at": null, "status": "draft",
        "created_at": "2024-01-01T00:00:00Z"
    },
    "targets": [
        {"id": 200, "post_id": 100, "platform_account_id": 10, "status": "scheduled"}
    ]
}
```

I campi flat (`id`, `workspace_id`, `status`, `scheduled_at`, ecc.) E la chiave annidata `"post"` puntano allo stesso `*models.Post`. Implementato da un `MarshalJSON` custom su `createPostResponse` in `pkg/api/posts.go` — la response è un `map[string]interface{}` costruito esplicitamente, NON uno struct con promotion embedded (che causerebbe un compile error Go "duplicate field Post" se provi anche a esporre una chiave "post").

**Perché due shape?**

- `pkg/api/routes_test.go::TestHandleCreatePost_Happy` decodifica in uno struct flat: `{ID, WorkspaceID, Status, ScheduledAt, Targets}`.
- `pkg/api/posts_test.go::TestPostsAPI_Create_Happy_ReturnsPostPlusTargets` decodifica in uno struct nested: `{Post, Targets}`.

Entrambi i test devono passare. Qualsiasi refactor che "semplifichi" la response (es. tornando a solo `{post, targets}`) rompe silenziosamente uno dei due decoder.

Se devi cambiare la response shape, aggiorna **entrambi** i test file + il docstring su `createPostResponse.MarshalJSON` in `pkg/api/posts.go` in lockstep. Il test del nome `TestHandleCreatePost_Happy` (flat) e `TestPostsAPI_Create_Happy_ReturnsPostPlusTargets` (nested) sono i lock-in.

---

## File di riferimento

- `internal/config/config.go` — env validation fail-fast + `EnforceProductionInvariants` (TODO: da estrarre)
- `internal/auth/jwt.go` — middleware strict-only (Taglio 1.1)
- `pkg/api/handlers.go` — `Router` struct, `NewRouter`, `Setup`, CORS + logging middleware, pre-existing routes (health, OAuth, publish, listAccounts, metrics)
- `pkg/api/workspaces.go` — `/api/v1/workspaces` CRUD handlers; 422 vs 400 contract (§13.1); lenient-auth fallback (§13.2)
- `pkg/api/posts.go` — `/api/v1/posts` CRUD handlers; 422 vs 400 contract (§13.1); lenient-auth fallback (§13.2); `createPostResponse` dual-shape (§13.3)
- `pkg/api/storage.go` — `/api/v1/storage/upload-url` handler
- `cmd/server/main.go` — auto-migrate on boot (rimosso fail-fast guard legacy in Taglio 1.1)
- `web/src/lib/auth.ts` — `authedFetch()`, `probeBackend()`, JWT helpers
- `web/src/lib/probe-cache.ts` — cache 5min per /health + force-clear
- `web/src/lib/probe-display.ts` — banner copy per ogni `ProbeFailureReason`; hint uses `window.location.origin`
- `web/src/pages/Status.tsx` — UI completa del probe + fallback banner
- `web/scripts/verify-api-base-url.ts` — build-time validator per `VITE_API_BASE_URL`
- `web/scripts/generate-favicon-ico.mjs` — prebuild ICO generator
- `README.md` — sezione "Deployment" con i 3 pitfall
- `.env.example` — template con sezioni dev/prod commentate (sezione 12 di HANDOFF)
- `HANDOFF-LINUX.md` §13 — contract API (422/400, lenient-auth, dual-shape)

Buon login! 🚀
