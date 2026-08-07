# InstaEditLogin v2

Microservizio multi-piattaforma di autenticazione OAuth 2.0 e publishing contenuti.
Supporta **7 piattaforme social** con gestione unificata di token e API.

## Piattaforme Supportate

| Piattaforma      | OAuth | Publish | Descrizione                               |
|------------------|-------|---------|-------------------------------------------|
| **Instagram**    | ✅    | ✅      | Foto/Reel su Instagram via Graph API      |
| **Facebook**     | ✅    | ✅      | Post su Facebook Pages via Graph API      |
| **Threads**      | ✅    | ✅      | Post testuali su Threads via Graph API    |
| **TikTok**       | ✅    | ✅      | Video publishing via TikTok API v2        |
| **Twitter / X**  | ✅ (OAuth 2.0 PKCE) | ✅ | Tweets testuali via X API v2              |
| **YouTube**      | ✅    | ✅      | Upload video via YouTube Data API v3      |
| **LinkedIn**     | ✅    | ✅      | Post testuali e articoli via LinkedIn Posts API |

Tutte le piattaforme sono **opzionali e indipendenti** (Taglio 2.4): si attivano
singolarmente, ognuna solo se le proprie credenziali sono configurate nel file ambiente esplicito (`.env.dev` in locale o il secret file VPS in produzione).
Il server parte anche con un solo provider attivo (es. solo YouTube) o con zero
provider (in questo caso `/api/v1/auth/{anything}` risponde 404).

## Stack Tecnologico

- **Linguaggio:** Go 1.26+
- **Database:** PostgreSQL
- **Sicurezza:** AES-256-GCM per token a riposo, JWT per sessioni
- **Pattern:** Small capability interfaces (OAuthProvider, AccountDiscoverer, ContentValidator, Publisher, AsyncPublisher) — Taglio 2a

## Avvio Rapido

### Prerequisiti

- Go 1.26+
- PostgreSQL 15+
- **Nessuna piattaforma social è obbligatoria** (Taglio 2.4): configura nel
  `.env.dev` solo le credenziali delle piattaforme che vuoi supportare. Le
  sette piattaforme (Meta, TikTok, X/Twitter, YouTube, LinkedIn, Google Drive,
  Velox) sono tutte indipendenti — vedi `## Piattaforme indipendenti` più sotto.

### Setup

```bash
# 1. Clona il repository
git clone https://github.com/Marcuss-ops/InstaeditLogin.git
cd InstaeditLogin

# 2. Configura le variabili d'ambiente
cp .env.dev.example .env.dev
# Modifica .env.dev con le tue credenziali reali
# Scommenta le piattaforme che vuoi attivare

# 3. Avvia la topologia canonica (migrate + API + worker)
make dev
```

Usa sempre un file ambiente esplicito: `.env.dev` in locale e
`/opt/instaedit/secrets/.env.production` sul VPS. Non creare o usare un root
`.env` per il percorso Compose, e non copiare secret reali nel repository.

### Worker di background

La topologia supportata usa processi separati: `cmd/migrate` applica le
migrazioni come job one-shot, `cmd/api` espone solo HTTP e `cmd/worker` esegue
i worker in background. `make dev` avvia questa stessa topologia tramite
Docker Compose e mantiene la parità con production.

La topologia supportata usa esclusivamente i tre entrypoint separati
`cmd/migrate`, `cmd/api` e `cmd/worker`; non esiste una modalità single-process
legacy. Per i controlli usa `make verify-entrypoint-topology`.

## Architettura

```
instaedit-login/
├── cmd/api/main.go             # HTTP entrypoint canonico
├── cmd/worker/main.go          # Background-worker entrypoint canonico
├── cmd/migrate/main.go         # Migration entrypoint one-shot
├── internal/
│   ├── auth/                   # JWT + API key middleware (Taglio 1.1)
│   ├── config/                 # Configurazione e file ambiente espliciti
│   ├── credentials/            # CredentialVault (encrypt + refresh + advisory lock)
│   ├── crypto/                 # AES-256-GCM encrypt/decrypt
│   ├── database/               # Connessione PostgreSQL e migrations
│   ├── models/                 # Modelli platform-agnostici
│   ├── providers/              # BuildRegistry — per-platform capability wiring
│   ├── repository/             # CRUD unificato (User, PlatformAccount, Token, Post, Workspace)
│   ├── worker/                 # Publish worker + async reconciler
│   └── services/
│       ├── provider.go         # Small capability interfaces + CapabilityRouter (Taglio 2a/2e)
│       ├── meta_oauth_base.go  # Meta OAuth base (shared by Facebook/Instagram/Threads)
│       ├── facebook_oauth.go   # Provider Facebook Pages
│       ├── instagram_oauth.go  # Provider Instagram Business (Reel + image)
│       ├── threads_oauth.go    # Provider Threads (async container)
│       ├── tiktok_oauth.go     # Provider TikTok (async 4-step state machine)
│       ├── twitter_oauth.go    # Provider X/Twitter (OAuth 2.0 PKCE only)
│       ├── youtube_oauth.go    # Provider YouTube (resumable upload)
│       ├── linkedin_oauth.go   # Provider LinkedIn (REST posts)
│       ├── http_client.go      # HTTP client shared by providers
│       ├── metrics_helper.go   # Publish + token refresh metrics wrappers
│       └── storage.go          # S3-compatible storage (presigned uploads)
└── pkg/api/                    # HTTP handlers + router (platform-agnostic)
```

## API Endpoints

| Metodo | Rotte                                  | Descrizione                        |
|--------|----------------------------------------|------------------------------------|
| GET    | `/api/v1/health`                       | Health check + piattaforme attive  |
| GET    | `/api/v1/auth/{provider}/login`        | Redirect OAuth (meta, tiktok, ...) |
| GET    | `/api/v1/auth/{provider}/callback`     | Callback OAuth                     |
| POST   | `/api/v1/posts/publish`                | Pubblica contenuto su piattaforma  |
| GET    | `/api/v1/accounts?user_id=X&platform=Y`| Lista account collegati            |

### Publish Request Body

```json
{
  "user_id": 1,
  "platform": "tiktok",
  "media_url": "https://example.com/video.mp4",
  "caption": "Check this out!",
  "content_type": "video",
  "title": "My Video"
}
```

## Piattaforme indipendenti (Taglio 2.4)

Le piattaforme social principali (Meta, TikTok, X/Twitter, YouTube,
LinkedIn) e i connettori aggiuntivi (Google Drive, Velox) si registrano
in modo **completamente indipendente** l'una dall'altra. Il server parte
con un qualsiasi sottoinsieme configurato, anche una sola.

**Regole** (valide per tutte le piattaforme supportate):

1. **Piattaforma disabilitata**: nessuna variabile d'ambiente settata
   per quella piattaforma (es. `YOUTUBE_CLIENT_ID` e `YOUTUBE_CLIENT_SECRET`
   entrambe vuote) → la piattaforma non viene registrata, il server parte
   senza di essa, `/api/v1/auth/youtube/login` risponde 404.
2. **Piattaforma abilitata**: credenziali complete presenti nel file ambiente esplicito
   → la piattaforma viene registrata all'avvio e i suoi endpoint
   OAuth/Publish sono attivi.
3. **Piattaforma half-configured** (es. `YOUTUBE_CLIENT_ID` settato ma
   `YOUTUBE_CLIENT_SECRET` vuoto) → l'avvio **fallisce** con un errore
   esplicito che dice quale env var manca. Meglio fallire al boot che
   scoprire il problema al primo click OAuth.

**Caso speciale Meta**: le credenziali `META_APP_ID` + `META_APP_SECRET`
sono condivise da tutti i provider Meta-family (Facebook, Instagram,
Threads). Se una di queste è half-configured (solo ID o solo secret)
l'avvio fallisce con errore esplicito. Se entrambe sono vuote MA
`FACEBOOK_REDIRECT_URI` è settato, la registrazione di Facebook viene
saltata con un warning (`Slog.Warn`) — la URL di login Facebook senza
`META_APP_ID` non potrebbe funzionare, quindi è meglio skippare
esplicitamente che registrare un servizio zoppo.

**Esempi di configurazione validi**:

| Variabili nel file ambiente esplicito                 | Piattaforme attive              |
|-----------------------------------------------------|----------------------------------|
| `META_APP_ID` + `META_APP_SECRET` + `FACEBOOK_REDIRECT_URI` | Facebook solo                |
| `YOUTUBE_CLIENT_ID` + `YOUTUBE_CLIENT_SECRET`       | YouTube solo                     |
| `LINKEDIN_CLIENT_ID` + `LINKEDIN_CLIENT_SECRET`     | LinkedIn solo                    |
| (nessuna env OAuth)                                 | Nessuna (server parte lo stesso) |
| Tutte e 5 le piattaforme configurate                | Tutte e 5                        |

## Generazione dei secret

Prima di avviare il server, genera i due secret locali (`JWT_SECRET` ed
`ENCRYPTION_KEY`) con valori conformi alle policy di `validate()`:

```bash
# JWT_SECRET: deve essere almeno 32 byte
#   (HS256 richiede una chiave ≥ output hash, RFC 7518 §3.2)
openssl rand -hex 32

# ENCRYPTION_KEY: deve decodificare esattamente a 32 byte (AES-256-GCM)
openssl rand -base64 32
```

I `*_CLIENT_SECRET` delle piattaforme opzionali (TikTok, Twitter, YouTube,
LinkedIn) e `META_APP_SECRET` (anch'esso opzionale, Taglio 2.4) vengono
rilasciati dalle rispettive console sviluppatore (vedi i link sopra
in `## Piattaforme Supportate`) e devono essere ≥32 caratteri in copia-incolla
— un valore più corto fa fallire l'avvio. Se invece l'env var è vuota, la
piattaforma corrispondente non viene registrata.

> ⚠️ **Conserva i secret in modo sicuro**: non committare `.env.dev` né il secret file production, non
> riusare lo stesso secret su due ambienti (dev/staging/prod), ruotalo
> immediatamente se viene esposto.

## Autenticazione JWT

L'API emette un JWT HS256 breve (15 minuti) al termine del flusso OAuth
(`/api/v1/auth/{provider}/callback`) e lo scrive in un cookie **HttpOnly**
(`session`), insieme a un cookie `refresh` opaco per il refresh token. La SPA
usa `credentials: "include"` su ogni richiesta autenticata; il JWT non viene
mai salvato in `localStorage`. Il flusso è:

1. Il browser completa il consenso OAuth sul provider esterno.
2. Il backend riceve il callback, crea la sessione ed imposta i cookie
   `session` (JWT, HttpOnly) e `refresh` (token opaco, HttpOnly).
3. La SPA è reindirizzata a `/app/linking`.
4. Il browser invia automaticamente il cookie `session` alle API protette.
5. Il middleware estrae il JWT dal cookie o dall'header `Authorization: Bearer`
   e verifica firma, issuer (`instaeditlogin`), audience (`api`) e metodo
   (`HS256`).

Per integrazioni server-to-server o test con curl si può usare l'header
`Authorization: Bearer <jwt>`; il cookie HttpOnly è il percorso normale
per il browser.

### Auth JWT (Taglio 1.1)

- Cookie `session` mancante o invalido → **401** `missing or invalid session`
- Header `Authorization` non in formato `Bearer` → **401** `invalid authorization header`
- JWT scaduto, firma invalida, issuer/audience errati o metodo diverso da HS256 → **401** `invalid or expired token`
- JWT valido → `user_id` inserito nel contesto della richiesta e l'handler gira

Il browser invia automaticamente il cookie `session`; per le integrazioni
server-to-server o per i test con curl si può usare
`Authorization: Bearer <jwt>`. La SPA usa `authedFetch()` in
`web/src/lib/auth.ts` con `credentials: "include"`.

All'avvio il server logga:
```
msg="Router configured" jwt_access_ttl_minutes=15 jwt_refresh_ttl_days=30
```

> 🚨 **NESSUNA MODALITÀ LEGACY** 🚨
>
> L'identità arriva esclusivamente dal JWT di sessione (Bearer header o cookie
> HttpOnly). Il body e la query string non vengono mai usati per ricavare
> l'identità. Un client senza Bearer valido riceve 401 in qualsiasi
> ambiente (`dev`, `staging`, `production`).

## Local Meta OAuth setup

Per provare Instagram/Facebook/Threads in locale, configura una Meta app in
Development mode e aggiungi l'account operatore come Developer o Tester:

1. In [Meta for Developers](https://developers.facebook.com), crea o apri l'app.
2. Copia App ID e App Secret in `META_APP_ID` e `META_APP_SECRET` del solo
   `.env.dev`.
3. Configura Facebook Login for Business (o Facebook Login) e registra
   **lo stesso valore di ciascuna `*_REDIRECT_URI` Meta presente in `.env.dev`**
   (`INSTAGRAM_REDIRECT_URI`, `FACEBOOK_REDIRECT_URI`,
   `THREADS_REDIRECT_URI`). Il template può contenere `localhost:8080` come
   default del processo Go, ma il percorso `make dev` pubblica l'API su `8081`:
   prima di usarlo, imposta tutti i callback su `http://localhost:8081/...`,
   oppure usa il proxy Caddy e imposta tutti i callback su
   `https://localhost:8443/...`. L'URL registrato su Meta e quello nel file
   ambiente devono essere identici e raggiungibili dal browser.
4. Collega, se necessario, un account Instagram Business all'app.
5. Dopo aver aggiornato il file ambiente, riavvia il backend e verifica il
   redirect OAuth con l'host/porta configurati, ad esempio:

```bash
curl -sI http://localhost:8081/api/v1/auth/instagram/login
```

Per produzione, il redirect deve usare l'host API HTTPS ed è documentato nel
runbook [`docs/DEPLOY.md`](docs/DEPLOY.md); non copiare secret in documenti,
log o commit.

## Deployment

La piattaforma production è ibrida e controllata direttamente:

- Vercel serve il frontend React su `https://app.instaedit.org`.
- Il server proprietario esegue API, worker, PostgreSQL e MinIO con Docker Compose.
- Caddy sul VPS espone `https://api.instaedit.org` verso l'API e blocca `/internal/*`.
- Fly.io, Railway, Render e Kubernetes non fanno parte del percorso supportato.

### Secret del backend self-hosted

Sul server crea `/opt/instaedit/secrets/.env.production`, proprietario root e
con permessi 600, partendo da [.env.production.example](.env.production.example).
Non committare mai il file reale e non inserirlo nei log o nei backup Git.

I valori OAuth production devono essere:

```env
FRONTEND_URL=https://app.instaedit.org
YOUTUBE_REDIRECT_URI=https://api.instaedit.org/api/v1/auth/youtube/callback
CORS_ALLOWED_ORIGINS=https://app.instaedit.org
COOKIE_DOMAIN=.instaedit.org
```

### Backend: avvio self-hosted

```bash
cd /opt/instaedit/InstaeditLogin
INSTAEDIT_ENV_FILE=/opt/instaedit/secrets/.env.production \
  docker compose \
    --env-file /opt/instaedit/secrets/.env.production \
    -f docker-compose.yml \
    -f docker-compose.production.yml \
    up -d --build
```

Compose attende PostgreSQL healthy, esegue `instaedit-migrate` e avvia API e
worker solo dopo il completamento delle migrazioni. L'API resta su
`127.0.0.1:8080`; database e MinIO non sono pubblicati sull'host.
Usa [ops/vps/Caddyfile](ops/vps/Caddyfile) come riferimento per
il reverse proxy API. Il frontend e le sue route web restano su Vercel.
Per la topologia, il cutover e le verifiche usa [`docs/DEPLOY.md`](docs/DEPLOY.md);
per DNS, TLS, monitoring e recovery usa [`docs/OPERATIONS.md`](docs/OPERATIONS.md).

### Frontend: Vercel

Il progetto Vercel usa la root del repository, il file `vercel.json` e il workflow
`.github/workflows/deploy.yml`; i comandi del file root puntano alla workspace `web/`.
Per una build locale equivalente:

```bash
VITE_API_BASE_URL=https://api.instaedit.org npm --prefix web ci
VITE_API_BASE_URL=https://api.instaedit.org npm --prefix web run build
```

Il bundle deve contenere `https://api.instaedit.org`, mai `dev.instaedit.org`,
`fly.dev`, `vercel.app` come API o `localhost`.

### Verifica dopo il deploy

```bash
curl -fsS https://api.instaedit.org/api/v1/health
curl -fsS https://api.instaedit.org/ready
INSTAEDIT_ENV_FILE=/opt/instaedit/secrets/.env.production \
  docker compose --env-file /opt/instaedit/secrets/.env.production \
    -f docker-compose.yml -f docker-compose.production.yml ps
```

Durante un nuovo collegamento Google l'URL deve contenere esclusivamente:
`redirect_uri=https://api.instaedit.org/api/v1/auth/youtube/callback`.

### Note storiche sui deploy non supportati

Le configurazioni e istruzioni storiche di provider alternativi sono archiviate
soltanto per audit; il percorso operativo corrente è Vercel + VPS ed è descritto
in [`docs/DEPLOY.md`](docs/DEPLOY.md) e [`docs/OPERATIONS.md`](docs/OPERATIONS.md).
## Sicurezza

- Token OAuth **mai** salvati in chiaro (AES-256-GCM)
- `ENCRYPTION_KEY` con **esattamente** 32 byte decodificati (validato allo
  startup; un messaggio d'errore mostra entrambi i numeri, es. "got 16;
  expected 32")
- `JWT_SECRET` con **almeno** 32 byte (RFC 7518 §3.2; validato allo startup)
- Tutti i `*_CLIENT_SECRET` (META_APP_SECRET + TIKTOK_CLIENT_SECRET +
  X_CLIENT_SECRET + YOUTUBE_CLIENT_SECRET + LINKEDIN_CLIENT_SECRET)
  con almeno **32 caratteri** quando l'env var è settata (validato allo
  startup; un valore vuoto = piattaforma disabilitata, Taglio 2.4)
- Auth JWT (Taglio 1.1): blocca ogni richiesta a `/api/v1/posts/publish`
  e `/api/v1/accounts` senza `Authorization: Bearer <jwt>` valido; nessun
  fallback a `user_id` body/query, nessun ID sintetico (default userID=1 rimosso)
- **X / Twitter OAuth 2.0 PKCE (Taglio 1.3)**: ogni publish usa esclusivamente il Bearer
  token utente OAuth 2.0 (PKCE) ottenuto via `/api/v1/auth/twitter/callback`.
- `.env.dev` e i file ambiente reali esclusi da git
- HTTPS richiesto in produzione
- Per i dettagli sui secret minimi vedi `## Generazione dei secret` in alto
