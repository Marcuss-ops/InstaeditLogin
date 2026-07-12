# InstaEditLogin — Public Quickstart

Microservizio di **OAuth 2.0 + multi-platform publishing** in Go.
Un'unica API per pubblicare contenuti su **Instagram, Facebook, Threads, TikTok, X/Twitter, YouTube, LinkedIn** con gestione unificata di token, rate-limit, webhook.

> **5 minuti al primo post pubblicato.** Questa guida attraversa i sei step minimi del quickstart:
>
> 1. [Autenticazione & creazione API key](AUTH.md)
> 2. [Sandbox vs Live (`sk_test_` / `sk_live_`)](SANDBOX.md)
> 3. [Connessione account via hosted OAuth](AUTH.md#hosted-oauth)
> 4. [Upload media via presigned URL](MEDIA.md)
> 5. [Creazione post con `targets[]`](POSTS.md)
> 6. [Ricezione eventi via webhook](WEBHOOKS.md)

Ogni sezione fornisce l'esempio `curl` esatto pronto da copiare. Per il reference completo: [`docs/ENDPOINTS.md`](ENDPOINTS.md), [`docs/OPENAPI.md`](OPENAPI.md), [`docs/PROVIDER_MATRIX.md`](PROVIDER_MATRIX.md).

## Indice delle guide

| Doc | Cosa contiene |
|---|---|
| [`AUTH.md`](AUTH.md) | Autenticazione (JWT via OAuth / API key `sk_test_`/`sk_live_`), creazione/revoca API key, hosted OAuth flow per piattaforma |
| [`SANDBOX.md`](SANDBOX.md) | Semantica test vs live, rate limit differenziali, checklist di go-live |
| [`MEDIA.md`](MEDIA.md) | Upload presigned a 3 step (presign → PUT → complete), limiti per tipo |
| [`POSTS.md`](POSTS.md) | `POST /api/v1/posts` con `targets[]` + settings per-piattaforma, risposta 202 Accepted |
| [`WEBHOOKS.md`](WEBHOOKS.md) | Delivery, firma HMAC-SHA256, retry, replay attack protection |
| [`ENDPOINTS.md`](ENDPOINTS.md) | Reference completo di ogni endpoint |
| [`OPENAPI.md`](OPENAPI.md) | Schema OpenAPI-style delle request/response |
| [`ARCHITECTURE.md`](ARCHITECTURE.md) | Architettura interna (Postgres, Capability interfaces, worker) |
| [`MIGRATIONS.md`](MIGRATIONS.md) | Schema DB versionato per migration |
| [`PROVIDER_MATRIX.md`](PROVIDER_MATRIX.md) | Cosa ogni piattaforma supporta (scopes, media types, async publish) |

## Requisiti

- Server InstaEditLogin raggiungibile (self-hosted o hosted)
- API key valida (`sk_test_…` o `sk_live_…`) ottenuta dal dashboard
- `curl` + `jq` (questi esempi usano `jq` per il parsing JSON)

## Convenzioni di questo documento

- Tutti gli esempi usano `${BASE_URL}` — normalmente `https://api.instaeditlogin.example/v1` in live o `http://localhost:8080/api/v1` in locale.
- `${API_KEY}` è il plaintext di una chiave (mostrato UNA SOLA VOLTA alla creazione — salvalo subito).
- Le risposte sono mostrate come body JSON. L'`HTTP status` è indicato sopra ogni risposta.
- Le righe `▸` mostrano i campi più importanti; il resto è omesso con `…` per brevità.

## TL;DR — il flusso più corto

```bash
# 1) Crea una API key di test
curl -X POST "${BASE_URL}/api/v1/api-keys" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"name":"smoke","environment":"test","scopes":["posts:write"]}'

# 2) Connetti un account TikTok (hosted OAuth; apre il browser)
open "${BASE_URL}/api/v1/auth/tiktok/login?api_key=${API_KEY}"

# 3) Upload media
curl -X POST "${BASE_URL}/api/v1/media/presign" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"workspace_id":1,"mime_type":"video/mp4","size_bytes":15728640}'

# 4) Crea post con target singolo
curl -X POST "${BASE_URL}/api/v1/posts" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Idempotency-Key: $(uuidgen)" \
  -H "Content-Type: application/json" \
  -d @post.json   # vedi POSTS.md per lo shape

# 5) Ricevi evento via webhook (configurai l'URL al passo 2)
# → X-InstaEdit-Signature: t=…,v1=…
```

Continua con [`AUTH.md`](AUTH.md) per il dettaglio di ogni step.
