# InstaEditLogin

Microservizio di autenticazione e gestione token per Instagram/Facebook (Meta).
Gestisce il flusso OAuth 2.0, la cifratura dei token sensibili (AES-256),
e l'inoltro di contenuti verso le API Meta.

## Stack Tecnologico

- **Linguaggio:** Go 1.26+
- **Database:** PostgreSQL
- **Sicurezza:** AES-256-GCM per token a riposo, JWT per sessioni

## Avvio Rapido

### Prerequisiti

- Go 1.26+
- PostgreSQL 15+
- Meta App ID e App Secret (da [developers.facebook.com](https://developers.facebook.com))

### Setup

```bash
# 1. Clona il repository
git clone https://github.com/Marcuss-ops/InstaeditLogin.git
cd InstaeditLogin

# 2. Configura le variabili d'ambiente
cp .env.example .env
# Modifica .env con le tue credenziali reali

# 3. Avvia il server
go run cmd/server/main.go
```

## Architettura

```
instaedit-login/
├── cmd/server/main.go        # Entry point
├── internal/
│   ├── config/               # Configurazioni e .env
│   ├── database/             # Connessione PostgreSQL
│   ├── models/               # Modelli dati (User, Token)
│   ├── repository/           # CRUD e query SQL
│   ├── crypto/               # AES-256 encrypt/decrypt
│   └── services/             # Logica business (OAuth, Meta API)
└── pkg/api/                  # Router e handlers HTTP
```

## API Endpoints

| Metodo | Rotte                            | Descrizione                    |
|--------|----------------------------------|--------------------------------|
| GET    | `/api/v1/health`                 | Health check                   |
| GET    | `/api/v1/auth/login`             | Redirect OAuth Meta            |
| GET    | `/api/v1/auth/callback`          | Callback OAuth Meta            |
| POST   | `/api/v1/posts/publish`          | Pubblica contenuto su IG/FB    |

## Sicurezza

- I token Meta NON vengono mai salvati in chiaro nel database
- AES-256-GCM con chiave derivata da `ENCRYPTION_KEY`
- `.env` escluso da git (vedi `.gitignore`)
- HTTPS richiesto in produzione
