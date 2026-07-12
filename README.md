# InstaEditLogin v2

Microservizio multi-piattaforma di autenticazione OAuth 2.0 e publishing contenuti.
Supporta **5 piattaforme social** con gestione unificata di token e API.

## Piattaforme Supportate

| Piattaforma      | OAuth | Publish | Descrizione                               |
|------------------|-------|---------|-------------------------------------------|
| **Meta** (FB/IG)  | ✅    | ✅      | Foto/Reel su Instagram via Graph API      |
| **TikTok**        | ✅    | ✅      | Video publishing via TikTok API v2        |
| **Twitter / X**   | ✅ (OAuth 2.0 PKCE) | ✅ | Tweets testuali via X API v2              |
| **YouTube**       | ✅    | ✅      | Upload video via YouTube Data API v3      |
| **LinkedIn**      | ✅    | ✅      | Post testuali e articoli via LinkedIn Posts API |

Tutte le piattaforme sono **opzionali e indipendenti** (Taglio 2.4): si attivano
singolarmente, ognuna solo se le proprie credenziali sono configurate nel `.env`.
Il server parte anche con un solo provider attivo (es. solo YouTube) o con zero
provider (in questo caso `/api/v1/auth/{anything}` risponde 404).

## Stack Tecnologico

- **Linguaggio:** Go 1.23+
- **Database:** PostgreSQL
- **Sicurezza:** AES-256-GCM per token a riposo, JWT per sessioni
- **Pattern:** Interface-based providers (OAuthProvider + ContentPublisher + TokenManager)

## Avvio Rapido

### Prerequisiti

- Go 1.23+
- PostgreSQL 15+
- **Nessuna piattaforma social è obbligatoria** (Taglio 2.4): configura nel
  `.env` solo le credenziali delle piattaforme che vuoi supportare. Le
  cinque piattaforme (Meta, TikTok, Twitter, YouTube, LinkedIn) sono
  tutte indipendenti — vedi `## Piattaforme indipendenti` più sotto.

### Setup

```bash
# 1. Clona il repository
git clone https://github.com/Marcuss-ops/InstaeditLogin.git
cd InstaeditLogin

# 2. Configura le variabili d'ambiente
cp .env.example .env
# Modifica .env con le tue credenziali reali
# Scommenta le piattaforme che vuoi attivare

# 3. Avvia il server
go run cmd/server/main.go
```

## Architettura

```
instaedit-login/
├── cmd/server/main.go          # Entry point con wiring multi-provider
├── internal/
│   ├── config/                 # Configurazioni e .env (multi-platform)
│   ├── database/               # Connessione PostgreSQL e migrations
│   ├── models/                 # Modelli platform-agnostici
│   ├── repository/             # CRUD unificato (User, PlatformAccount, Token)
│   ├── crypto/                 # AES-256-GCM encrypt/decrypt
│   └── services/
│       ├── provider.go         # OAuthProvider + ContentPublisher + TokenManager
│       ├── token_helper.go     # Token encryption/retrieval condiviso
│       ├── facebook_oauth.go   # Provider Meta (Facebook + Instagram)
│       ├── tiktok_oauth.go     # Provider TikTok
│       ├── twitter_oauth.go    # Provider Twitter/X
│       └── youtube_oauth.go    # Provider YouTube
└── pkg/api/routes.go           # Router platform-agnostico
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

Ogni piattaforma social (Meta, TikTok, Twitter, YouTube, LinkedIn) si
registra in modo **completamente indipendente** dalle altre. Il server
parte con un qualsiasi sottoinsieme di piattaforme configurate, anche
una sola.

**Regole** (valide per tutte e cinque le piattaforme):

1. **Piattaforma disabilitata**: nessuna variabile d'ambiente settata
   per quella piattaforma (es. `YOUTUBE_CLIENT_ID` e `YOUTUBE_CLIENT_SECRET`
   entrambe vuote) → la piattaforma non viene registrata, il server parte
   senza di essa, `/api/v1/auth/youtube/login` risponde 404.
2. **Piattaforma abilitata**: credenziali complete presenti nel `.env`
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

| `.env`                                              | Piattaforme attive              |
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

> ⚠️ **Conserva i secret in modo sicuro**: non committare `.env`, non
> riusare lo stesso secret su due ambienti (dev/staging/prod), ruotalo
> immediatamente se viene esposto.

## Autenticazione JWT & Rollout

L'API emette un JWT HS256 al termine del flusso OAuth (`/api/v1/auth/{provider}/callback`)
e lo restituisce:

- come **redirect** verso `${FRONTEND_URL}/auth/callback?jwt=...&provider=...&user_id=...&expires_at=...`
  per i browser (l'app React lo cattura e lo salva in `localStorage`), oppure
- come **JSON** `{ jwt_token, ... }` se `FRONTEND_URL` non è configurato (curl,
  Postman, integrazioni server-to-server).

Il middleware (`internal/auth.Middleware`) è **strict per costruzione** dopo il Taglio 1.1:
l'identità arriva solo dal JWT di sessione o da una API key (Taglio 1.2). Non esiste
alcuna modalità legacy o lenient.

### Auth (unica — Taglio 1.1)

- Authorization mancante → **401** `missing authorization header`
- Header non in formato `Bearer` → **401** `invalid authorization header`
- JWT scaduto o firma invalida → **401** `invalid or expired token`
- JWT valido → `user_id` inserito nel contesto della richiesta e l'handler gira

Il client deve allegare `Authorization: Bearer <jwt>` ad ogni chiamata a
`/api/v1/posts/publish` e `/api/v1/accounts`. La SPA lo fa automaticamente via
`authedFetch()` in `web/src/lib/auth.ts`.

All'avvio il server logga:
```
msg="Router configured" jwt_ttl_hours=168 auth=strict
```

> 🚨 **NESSUNA MODALITÀ LEGACY** 🚨
>
> Dopo il Taglio 1.1 il body e la query string non vengono più usati per ricavare
> l'identità: non c'è fallback a `user_id=1`, non c'è `STRICT_JWT_AUTH=false`, non c'è
> helper che restituisce un utente sintetico. Un client senza Bearer riceve 401 in
> qualsiasi ambiente (`dev`, `staging`, `production`).

## Deployment

Quando fai deploy del frontend React su Vercel (o piattaforma analoga),
`VITE_API_BASE_URL` deve puntare al backend Go **live** — non più a
localhost. I tre pitfall operativi che provocano il 404
`DEPLOYMENT_NOT_FOUND` al primo click OAuth li riassumo qui di seguito.

### 1. Puntare a un deployment Vercel defunto

L'antipattern più frequente: lasciare `VITE_API_BASE_URL` impostato su un
vecchio alias frontend Vercel (es. `https://vecchio-progetto.vercel.app`)
pensando che quello sia il backend. In realtà è un alias dello **stesso
frontend** ormai rimosso/cancellato/scaduto — Vercel risponde con la pagina
HTML standard `DEPLOYMENT_NOT_FOUND` invece di fare da proxy API.

**Sintomo**: `/status` col probe banner rosso che cita "Vercel stale
deployment" (motivo `vercel_stale_deploy`). Cliccando un bottone OAuth il
browser naviga alla URL ma riceve una pagina di errore invece del redirect
verso Meta/TikTok/etc.

**Fix**: `VITE_API_BASE_URL` deve essere l'URL diretto del backend
Go — Railway / Render / Fly.io / custom domain (es.
`https://api.example.com`). MAI un sottodominio `*.vercel.app`.

### 2. Dimenticare di redeploy dopo aver cambiato l'env

Le env var `VITE_*` vengono **baked dentro il bundle JS** al momento del
`vite build`. Cambiare `VITE_API_BASE_URL` (o qualsiasi altra `VITE_*`) nel
dashboard Vercel **NON** aggiorna il deployment corrente — serve un rebuild
esplicito.

**Sintomo**: la modifica env è stata salvata (la vedi nella tab
Environment Variables), ma la pagina `/login` mostra ancora il banner rosso
con la URL vecchia. Il probe torna green solo dopo il redeploy.

**Fix**: dopo ogni cambio di `VITE_API_BASE_URL` (o altra `VITE_*`),
Vercel → **Deployments** → menu `⋯` sul corrente → **Redeploy**. Spunta
anche **Clear Build Cache** se vuoi essere sicuro che il bundle JS
precedente non sia riusato da qualche cache CDN.

### 3. Confondere frontend origin con backend origin

Domanda frequente: "Vercel mi ha dato `https://instaedit-xyz.vercel.app`
come URL del frontend. Posso usare quello come `VITE_API_BASE_URL`?".
**No** — quel URL serve **il tuo stesso frontend** (asset statici), non
il backend. Vercel restituisce 404 per ogni `/api/v1/auth/.../*` perché
non c'è nessun run-time che risponde a quei path.

**Sintomo**: `VITE_API_BASE_URL` finisce in `https://*.vercel.app/api/v1/...`
e ogni probe restituisce 404 — sia `/health` che `/auth/{provider}/login`.

**Fix**: i due URL vivono su host **diversi**. Il frontend è Vercel (asset
statici, `vercel.json` → `dist/`), il backend è un servizio long-running
separato (Go + Postgres, deployato su Railway/Render/Fly.io/VPS). Esempio:
frontend su `https://instaedit.vercel.app`, backend su
`https://instaedit-api.fly.dev`. Metti il secondo in `VITE_API_BASE_URL`.

> 📖 La pagina `/status` linka a questa sezione dal banner rosso di
> degraded — se la probe fallisce con uno qualsiasi dei tre pitfall sopra,
> vieni direttamente qui dopo un click.

## Sicurezza

- Token OAuth **mai** salvati in chiaro (AES-256-GCM)
- `ENCRYPTION_KEY` con **esattamente** 32 byte decodificati (validato allo
  startup; un messaggio d'errore mostra entrambi i numeri, es. "got 16;
  expected 32")
- `JWT_SECRET` con **almeno** 32 byte (RFC 7518 §3.2; validato allo startup)
- Tutti i `*_CLIENT_SECRET` (META_APP_SECRET + TIKTOK_CLIENT_SECRET +
  TWITTER_CLIENT_SECRET + YOUTUBE_CLIENT_SECRET + LINKEDIN_CLIENT_SECRET)
  con almeno **32 caratteri** quando l'env var è settata (validato allo
  startup; un valore vuoto = piattaforma disabilitata, Taglio 2.4)
- Auth strict JWT (Taglio 1.1): blocca ogni richiesta a `/api/v1/posts/publish`
  e `/api/v1/accounts` senza `Authorization: Bearer <jwt>` valido; nessun
  fallback a `user_id` body/query, nessun ID sintetico (default userID=1 rimosso)
- **X / Twitter OAuth 2.0 PKCE only (Taglio 1.3)**: nessuna credenziale statica
  `TWITTER_API_KEY` / `TWITTER_API_KEY_SECRET` / `TWITTER_ACCESS_TOKEN` /
  `TWITTER_ACCESS_TOKEN_SECRET`; ogni publish usa esclusivamente il Bearer
  token utente OAuth 2.0 (PKCE) ottenuto via `/api/v1/auth/twitter/callback`.
  Le env var sono rimosse dal config e il fallback OAuth 1.0a in
  `internal/services/twitter_oauth.go::Publish` non esiste più.
- `.env` escluso da git
- HTTPS richiesto in produzione
- Per i dettagli sui secret minimi vedi `## Generazione dei secret` in alto
