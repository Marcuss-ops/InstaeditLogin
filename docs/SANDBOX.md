# Sandbox vs Live environment

InstaEditLogin supporta **due ambienti paralleli** identificati dal prefisso della API key:

| Prefisso | Ambiente | Uso |
|---|---|---|
| `sk_test_…` | **Sandbox / test** | Sviluppo, CI, smoke test |
| `sk_live_…` | **Production / live** | Traffico reale verso account social reali |

I due ambienti **non condividono dati**: le API key test non possono
riferire risorse create da una chiave live, e viceversa. Anche il
database (Postgres) viene partizionato logicamente tramite la
colonna `environment` sulla tabella `api_keys` (migration 017);
i `platform_accounts` connessi da un flusso OAuth di una chiave `sk_test_…`
hanno effect sui **sandbox account** delle piattaforme social
(quando offerti) o su account reali con limiti di quota ridotti.

## Generazione della chiave

Entrambi i prefissi seguono lo stesso schema:

```
sk_test_aB3xY9K2nQp…   (52 caratteri base32 dopo il prefisso)
sk_live_aB3xY9K2nQp…   (52 caratteri base32 dopo il prefisso)
```

Il secret è generato da `crypto/rand` (256 bit di entropia) e
hashato con SHA-256 prima della persistenza (`api_keys.key_hash`).
Il plaintext è restituito **una sola volta** nella risposta a
`POST /api/v1/api-keys` — non è più recuperabile dopo.

### Esempio curl

```bash
# Crea una chiave di test
curl -X POST "${BASE_URL}/api/v1/api-keys" \
  -H "Authorization: Bearer ${EXISTING_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "CI smoke key",
    "environment": "test",
    "scopes": ["posts:write", "media:write"]
  }'
```

Risposta **`201 Created`**:

```json
{
  "id": "ak_01J8X7Y2N3K4M5P6Q7R8S9T0V1",
  "name": "CI smoke key",
  "environment": "test",
  "key_prefix": "sk_test_aB3xY9K2",
  "secret": "sk_test_aB3xY9K2nQpR4sT7vW8xY1zA3bC6dE9fG2hJ5kL8mN0pQ3rS6uV9",
  "scopes": ["posts:write", "media:write"],
  "created_at": "2026-07-12T10:00:00Z"
}
```

▸ **Copia `secret` immediatamente** — è mostrato solo adesso. Il
`key_prefix` (16 caratteri) resta visibile nel dashboard per
identificare la chiave nelle audit log.

## Differenze pratiche tra ambienti

| Aspetto | `sk_test_` | `sk_live_` |
|---|---|---|
| Rate limit (default) | 100 req/min | 1000 req/min |
| Costo per chiamata provider | Fittizio (não addebita) | Addebito reale quota del developer |
| Account connessi | Sandbox account* | Account social reali |
| Webhook delivery | Solo URL `https://` con self-signed (dev) | HTTPS obbligatorio, no replay di prova |
| Storage media | Bucket separato `media-test-*` | Bucket `media-prod-*` |
| Retention audit log | 7 giorni | 90 giorni |
| `Idempotency-Key` richiesto | No (best effort) | Sì su `POST /api/v1/posts` |

\* Per piattaforme che offrono sandbox account (TikTok Sandbox, Meta
Development App), il flusso OAuth usa la app di dev e l'utente
accede con credenziali fittizie. Per le altre (YouTube, LinkedIn),
i `sk_test_` operano contro account reali ma con quote ridotte e
flag esplicito `test=true` sui log del provider.

## Validazione del prefisso lato server

Il middleware in `internal/auth/apikey_middleware.go`:

1. Estrae il valore `Authorization: Bearer <…>`
2. Controlla il prefisso con `auth.IsApiKeyBearer`
3. Parsifica env/secret con `auth.ParseFullKey`
4. Hasha il secret e fa lookup su `api_keys.key_hash = $1`
5. Inietta `env`, `key_id`, `scopes` nel `context.Context`

Un payload malformato (es. `Bearer sk_…` con un solo prefisso
sbagliato come `sk_prod_`) ottiene `401 Unauthorized` con envelope
`{"error":{"code":"authentication_error", …}}` — non un 200
"silente" con scope ridotti.

## Checklist di go-live

Prima di scambiare una `sk_test_` con una `sk_live_`:

- [ ] Webhook endpoint risponde `200 OK` in <5s e serve HTTPS con cert valido
- [ ] `Idempotency-Key` inviato su tutti i `POST /api/v1/posts`
- [ ] Verifica firma `X-InstaEdit-Signature` sui webhook in ingresso
- [ ] Rate-limit client rispettoso (vedi [`SANDBOX.md#rate-limits`](#rate-limits))
- [ ] Monitoring su `5xx` (alert a >1% in 5min)
- [ ] Storage bucket `media-prod-*` raggiungibile e CORS configurato
- [ ] `environment: "live"` su tutte le nuove chiavi generate
- [ ] Audit log retention impostata a 90 giorni (DB-side)

## Rate-limits

| Endpoint | `sk_test_` | `sk_live_` |
|---|---|---|
| `POST /api/v1/posts` | 10 req/min | 60 req/min |
| `POST /api/v1/posts/{id}/publish` | 10 req/min | 60 req/min |
| `POST /api/v1/media/presign` | 30 req/min | 120 req/min |
| `GET /api/v1/accounts` | 60 req/min | 300 req/min |
| Webhook delivery (verso il tuo endpoint) | Best-effort | At-least-once con retry |

Quando si supera il rate limit, la risposta è:

```
HTTP/1.1 429 Too Many Requests
Retry-After: 42
X-RateLimit-Limit: 60
X-RateLimit-Remaining: 0

{
  "error": {
    "code": "rate_limited",
    "message": "Too many requests. Retry after 42s.",
    "retryable": true,
    "retry_after_seconds": 42
  }
}
```
