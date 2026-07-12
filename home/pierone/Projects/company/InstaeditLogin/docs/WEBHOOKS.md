# Webhooks

InstaEditLogin invia **eventi asincroni** al tuo endpoint quando
qualcosa di rilevante succede (post pubblicato, fallito, account
disconnesso, ecc.). Delivery **at-least-once** con firma HMAC-SHA256
per autenticità.

## Indice

- [Setup](WEBHOOKS.md#setup)
- [Headers](WEBHOOKS.md#headers)
- [Eventi](WEBHOOKS.md#eventi)
- [Verifica firma](WEBHOOKS.md#verifica-firma)
- [Retry & replay](WEBHOOKS.md#retry-replay)
- [Sandbox (test mode)](WEBHOOKS.md#sandbox)

---

## Setup

Il tuo endpoint deve:

1. Servire **HTTPS** con cert valido (Let's Encrypt OK)
2. Rispondere **200 OK** in **<5 secondi** (timeout server = 4.5s)
3. Verificare la firma `X-InstaEdit-Signature` (vedi sotto)
4. Essere idempotente sull'`X-InstaEdit-Event-Id` (usarlo come
   chiave di dedupe lato tuo)

### Registrazione dal dashboard

```bash
curl -X POST "${BASE_URL}/api/v1/webhook-endpoints" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "url":    "https://my-app.example.com/hooks/instaedit",
    "events": [
      "post.target.published",
      "post.target.failed",
      "account.disconnected",
      "reauthentication_required"
    ],
    "secret": "whsec_my_secret_64_chars…"
  }'
```

▸ **`secret` è la chiave di firma usata dal server** per firmare
i payload diretti a te. Conservala segreta (sarà visibile nel
dashboard in chiaro solo al momento della creazione; rotazione
genera un nuovo secret, vecchi eventi restano verificabili
finché non li cancelli).

Risposta **`201 Created`**:

```json
{
  "id":     "wh_01HXY…",
  "url":    "https://my-app.example.com/hooks/instaedit",
  "events": ["post.target.published", …],
  "secret": "whsec_my_secret_64_chars…",      ← copia ADESSO
  "active": true,
  "created_at": "2026-07-12T10:00:00Z"
}
```

---

## Headers

| Header | Tipo | Note |
|---|---|---|
| `Content-Type` | `application/json` | Sempre |
| `X-InstaEdit-Event-Id` | `string` (ULID) | Unico per evento — usa per dedupe |
| `X-InstaEdit-Event-Type` | `string` | es.: `post.target.published` |
| `X-InstaEdit-Signature` | `string` | `t=<unix_ts>,v1=<hex_hmac>` |
| `X-InstaEdit-Request-Id` | `string` | Per tracing lato server |
| `User-Agent` | `InstaEditLogin-Webhook/1.0` | |

---

## Eventi

### `post.target.published`

```json
{
  "event": "post.target.published",
  "delivered_at": "2026-07-12T15:00:42Z",
  "data": {
    "post_id":             "post_01HXY…",
    "post_target_id":      "pt_01HXY…101",
    "platform":            "tiktok",
    "platform_account_id": 101,
    "platform_post_id":    "tiktok_video_7890123456789",
    "platform_post_url":   "https://www.tiktok.com/@user/video/7890123456789",
    "published_at":        "2026-07-12T15:00:41Z",
    "workspace_id":        1
  }
}
```

### `post.target.failed`

```json
{
  "event": "post.target.failed",
  "delivered_at": "2026-07-12T15:00:42Z",
  "data": {
    "post_id":             "post_01HXY…",
    "post_target_id":      "pt_01HXY…101",
    "platform":            "instagram",
    "platform_account_id": 102,
    "error_code":          "media_invalid",
    "error_message":       "Video aspect ratio not supported (got 1:1, want 9:16).",
    "retryable":           false,
    "workspace_id":        1
  }
}
```

### `account.disconnected`

```json
{
  "event": "account.disconnected",
  "delivered_at": "2026-07-12T15:00:42Z",
  "data": {
    "platform_account_id": 102,
    "platform":            "instagram",
    "reason":              "user_revoked",
    "workspace_id":        1
  }
}
```

### `reauthentication_required`

```json
{
  "event": "reauthentication_required",
  "delivered_at": "2026-07-12T15:00:42Z",
  "data": {
    "platform_account_id": 102,
    "platform":            "tiktok",
    "reason":              "refresh_token_expired",
    "reauth_url":          "${BASE_URL}/api/v1/auth/tiktok/login?api_key=${API_KEY}",
    "workspace_id":        1
  }
}
```

### `post.scheduled` / `post.target.queued`

```json
{
  "event": "post.target.queued",
  "delivered_at": "2026-07-12T14:30:00Z",
  "data": {
    "post_id":             "post_01HXY…",
    "post_target_id":      "pt_01HXY…101",
    "platform":            "youtube",
    "scheduled_at":        "2026-07-13T09:00:00Z",
    "workspace_id":        1
  }
}
```

---

## Verifica firma

Header `X-InstaEdit-Signature`:

```
t=1752345612,v1=4f3a2b1c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a
```

**Algoritmo** (Node.js / pseudo-codice):

```js
function verifySignature(rawBody, headerValue, secret, toleranceSec = 300) {
  const parts = Object.fromEntries(
    headerValue.split(',').map(p => p.split('='))
  );
  const ts   = parseInt(parts.t, 10);
  const v1   = parts.v1;
  const signed = `${ts}.${rawBody}`;   // NB: rawBody = stringa ESATTA ricevuta, prima di JSON.parse
  const expected = crypto
    .createHmac('sha256', secret)
    .update(signed, 'utf8')
    .digest('hex');
  // constant-time compare
  if (!crypto.timingSafeEqual(Buffer.from(v1), Buffer.from(expected))) return false;
  // replay protection
  const now = Math.floor(Date.now() / 1000);
  if (Math.abs(now - ts) > toleranceSec) return false;
  return true;
}
```

**Go reference implementation** (vedi `internal/webhooker/verify.go` quando merge):

```go
func Verify(rawBody []byte, header, secret string) error {
    parts := strings.Split(header, ",")
    var ts int64
    var v1 string
    for _, p := range parts {
        k, v, _ := strings.Cut(p, "=")
        switch k {
        case "t":
            ts, _ = strconv.ParseInt(v, 10, 64)
        case "v1":
            v1 = v
        }
    }
    if math.Abs(float64(time.Now().Unix()-ts)) > 300 {
        return errors.New("timestamp out of tolerance")
    }
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write([]byte(fmt.Sprintf("%d.", ts)))
    mac.Write(rawBody)
    expected := hex.EncodeToString(mac.Sum(nil))
    if !hmac.Equal([]byte(v1), []byte(expected)) {
        return errors.New("signature mismatch")
    }
    return nil
}
```

#### Common pitfalls

- **Firma il raw body PRIMA di JSON.parse** — la serializzazione
  può cambiare (spazi, key order) e invalidare l'HMAC.
- **Salva `X-InstaEdit-Event-Id`** per dedupe: con at-least-once
  delivery puoi ricevere lo stesso evento più volte.
- **Rispondi 200 in fretta** (< 5s): se la tua logica è lenta,
  fai ACK immediato e processa in background (queue interna).
- **HTTPS obbligatorio** in live mode; in sandbox puoi usare
  self-signed su `localhost`.

---

## Retry & replay

| Tentativo | Delay | Note |
|---|---|---|
| 1 | immediato | First delivery |
| 2 | +30s | Se 5xx o timeout |
| 3 | +5min | Se ancora 5xx o timeout |
| 4 | +30min | Se ancora |
| 5 | +2h | Ultimo tentativo |

Totale massimo: ~3h. Dopo 5 tentativi falliti l'evento è
spostato in **dead-letter** (visibile in dashboard con action
"Replay").

```
GET /api/v1/webhook-deliveries?status=dead_letter

POST /api/v1/webhook-deliveries/{id}/replay
```

Replay ributta l'evento in coda con lo stesso `X-InstaEdit-Event-Id`
(la dedupe lato client previene il double-processing).

---

## Sandbox

In modalità `sk_test_`:

- Webhook URL può essere `http://localhost:…` (no HTTPS richiesto)
- `secret` di default è `whsec_test_default` (impostabile in setup)
- C'è un "ping" automatico al setup (`event: webhook.test`)
- Retry esponenziale ridotto (max 3 tentativi su 30min)

In modalità `sk_live_`:

- Solo HTTPS
- `secret` random 64-char (obbligatorio cambio dal default)
- Eventi firmati anche su retry (la firma cambia, ri-verifica)
- Dead-letter retention 30 giorni
