# InstaEditLogin v2

Microservizio multi-piattaforma di autenticazione OAuth 2.0 e publishing contenuti.
Supporta **5 piattaforme social** con gestione unificata di token e API.

## Piattaforme Supportate

| Piattaforma      | OAuth | Publish | Descrizione                               |
|------------------|-------|---------|-------------------------------------------|
| **Meta** (FB/IG)  | ✅    | ✅      | Foto/Reel su Instagram via Graph API      |
| **TikTok**        | ✅    | ✅      | Video publishing via TikTok API v2        |
| **Twitter / X**   | ✅    | ✅      | Tweets testuali via X API v2              |
| **YouTube**       | ✅    | ✅      | Upload video via YouTube Data API v3      |

Tutte le piattaforme oltre a Meta sono **opzionali**: si attivano solo se le relative credenziali sono configurate nel `.env`.

## Stack Tecnologico

- **Linguaggio:** Go 1.26+
- **Database:** PostgreSQL
- **Sicurezza:** AES-256-GCM per token a riposo, JWT per sessioni
- **Pattern:** Interface-based providers (OAuthProvider + ContentPublisher + TokenManager)

## Avvio Rapido

### Prerequisiti

- Go 1.26+
- PostgreSQL 15+
- Meta App ID e App Secret (obbligatori — da [developers.facebook.com](https://developers.facebook.com))
- Credenziali opzionali per TikTok, Twitter, YouTube

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

## Sicurezza

- Token OAuth **mai** salvati in chiaro (AES-256-GCM)
- `.env` escluso da git
- HTTPS richiesto in produzione
- Chiave di cifratura validata (32 byte base64)
