# Autenticazione & API Key

InstaEditLogin usa **due modalità di autenticazione** per la stessa
API: una per utenti browser (JWT), una per server / CLI (API key).

Sono intercambiabili in qualunque endpoint protetto da
`r.protected()` (`pkg/api/handlers.go`) — il middleware
`apikey_middleware.go` rileva il prefisso `sk_test_` / `sk_live_`
e instrada verso la catena API-key, altrimenti verso JWT.

## Indice

- [API key (`sk_test_` / `sk_live_`)](AUTH.md#api-key)
- [JWT (utente OAuth)](AUTH.md#jwt)
- [Hosted OAuth per piattaforma](AUTH.md#hosted-oauth)
- [Header `Authorization`](AUTH.md#header-authorization)

---

## API key

### `POST /api/v1/api-keys` — crea

```bash
curl -X POST "${BASE_URL}/api/v1/api-keys" \
  -H "Authorization: Bearer ${EXISTING_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Production server",
    "environment": "live",
    "scopes": ["posts:write", "media:write", "accounts:read"]
  }'
```

▸ **`secret` viene mostrato UNA SOLA VOLTA**. Salvalo in un
secrets manager (AWS Secrets Manager, GCP Secret Manager, Vault).
Non è recuperabile successivamente.

Risposta **`201 Created`**:

```json
{
  "id": "ak_01HXY…",
  "name": "Production server",
  "environment": "live",
  "key_prefix": "sk_live_aB3xY9K2",
  "secret": "sk_live_aB3xY9K2nQp…",     ← copia ADESSO
  "scopes": ["posts:write","media:write","accounts:read"],
  "created_at": "2026-07-12T10:00:00Z"
}
```

### `GET /api/v1/api-keys` — lista

```bash
curl -X GET "${BASE_URL}/api/v1/api-keys" \
  -H "Authorization: Bearer ${API_KEY}"
```

Risposta **`200 OK`**:

```json
{
  "keys": [
    {
      "id": "ak_01HXY…",
      "name": "Production server",
      "environment": "live",
      "key_prefix": "sk_live_aB3xY9K2",   ← visibile, non è il secret
      "scopes": ["posts:write"],
      "last_used_at": "2026-07-12T09:58:00Z",
      "created_at": "2026-07-01T10:00:00Z"
    }
  ]
}
```

### `DELETE /api/v1/api-keys/{id}` — revoca

```bash
curl -X DELETE "${BASE_URL}/api/v1/api-keys/ak_01HXY…" \
  -H "Authorization: Bearer ${API_KEY}"
```

Risposta **`204 No Content`**. La successiva richiesta con quella
chiave ottiene `401 authentication_error`.

---

## JWT

Il JWT è per **utenti umani** che hanno completato il flusso
**hosted OAuth** (es.: un publisher UI che vuole agire come l'utente).
JWT è un HS256 firmato con `JWT_SECRET` e ha una durata di 1 ora.

```bash
# Ottenuto dal redirect dopo callback OAuth:
#   ${FRONTEND_URL}/auth/callback?jwt=eyJ…&provider=tiktok&user_id=42&expires_at=2026-07-12T11:00:00Z

curl -X GET "${BASE_URL}/api/v1/auth/me" \
  -H "Authorization: Bearer eyJ…"
```

Risposta **`200 OK`**:

```json
{
  "user_id": 42,
  "provider": "tiktok",
  "workspaces": [1, 2],
  "expires_at": "2026-07-12T11:00:00Z"
}
```

Per il refresh: rifai il flusso hosted OAuth (`GET /api/v1/auth/{provider}/login`).

---

## Hosted OAuth

Per ogni piattaforma supportata, il flusso è:

1. `GET /api/v1/auth/{provider}/login` → redirect 302 a `accounts.google.com` (o equivalente)
2. Utente concede gli scopes
3. Provider redirige a `GET /api/v1/auth/{provider}/callback?code=…&state=…`
4. Server scambia `code` per `access_token` + `refresh_token`
5. Server crea o aggiorna la riga `platform_accounts` (idempotente)
6. Server redirige a `${FRONTEND_URL}/auth/callback?jwt=…&provider=…&user_id=…&expires_at=…`

### Avvio del flusso (curl)

```bash
# Per il flow con API key (server-to-server onboarding):
curl -X GET "${BASE_URL}/api/v1/auth/tiktok/login?api_key=${API_KEY}" \
  -L
# → 302 → https://www.tiktok.com/v2/auth/authorize/?client_id=…&state=…
```

### Avvio del flow con utente browser (HTML link)

```html
<a href="${BASE_URL}/api/v1/auth/instagram/login?redirect=/dashboard/accounts">
  Connetti Instagram
</a>
```

### Provider supportati

| Provider | OAuth flow | Scopes minimi |
|---|---|---|
| `instagram` | Graph API OAuth 2.0 | `instagram_basic`, `instagram_content_publish` |
| `facebook` | Graph API OAuth 2.0 | `pages_manage_posts`, `pages_read_engagement` |
| `threads` | Graph API OAuth 2.0 | `threads_basic`, `threads_content_publish` |
| `tiktok` | TikTok Login v2 | `user.info.basic`, `video.publish` |
| `twitter` | OAuth 2.0 PKCE | `tweet.read`, `tweet.write`, `users.read` |
| `youtube` | Google OAuth 2.0 | `https://www.googleapis.com/auth/youtube.upload` |
| `linkedin` | LinkedIn OAuth 2.0 | `w_member_social`, `r_liteprofile` |

---

## Header `Authorization`

```
Authorization: Bearer <sk_test_… | sk_live_… | jwt>
```

Casi di errore:

| Header | Risposta |
|---|---|
| (assente) | `401 authentication_error` |
| `Bearer sk_…` con prefisso malformato | `401 authentication_error` |
| `Bearer sk_test_…` con secret non in DB | `401 authentication_error` |
| `Bearer sk_test_…` con secret in DB ma `revoked=true` | `401 authentication_error` |
| `Bearer eyJ…` JWT scaduto | `401 reauthentication_required` |

Tutte le risposte 401 contengono l'envelope canonico (vedi
`docs/OPENAPI.md`):

```json
{
  "error": {
    "code": "authentication_error",
    "message": "Invalid API key.",
    "request_id": "req_01J8X…"
  }
}
```
