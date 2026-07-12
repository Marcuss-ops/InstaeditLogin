# Posts — `POST /api/v1/posts`

L'endpoint universale per creare un post multi-piattaforma.
Restituisce **`202 Accepted`** per ogni piattaforma target, con
`status=queued` per ogni `PostTarget` (l'esecuzione è asincrona
via worker).

> Vedi anche: [`ENDPOINTS.md#posts`](ENDPOINTS.md) e
> [`POSTS.md#status-machine`](#status-machine) per il lifecycle.

## Indice

- [Request shape](POSTS.md#request-shape)
- [Per-platform settings](POSTS.md#per-platform-settings)
- [Headers richiesti](POSTS.md#headers)
- [Risposta 202](POSTS.md#response)
- [Status machine](POSTS.md#status-machine)
- [Idempotency](POSTS.md#idempotency)

---

## Request shape

```bash
curl -X POST "${BASE_URL}/api/v1/posts" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Idempotency-Key: 7b7e3a1e-9f4d-4a3b-bbf2-4ad6e0a1c2d3" \
  -H "Content-Type: application/json" \
  -d '{
    "workspace_id": 1,
    "scheduled_at": "2026-07-12T15:00:00Z",
    "content": {
      "title":   "Reel estivo 2026",
      "caption": "Primo teaser della stagione",
      "media":   [{"asset_id": "ma_01J8X…"}]
    },
    "targets": [
      {
        "platform_account_id": 101,
        "settings": {
          "tiktok": {
            "privacy_level":    "public",
            "allow_comments":   true,
            "allow_duet":       true,
            "allow_stitch":     false,
            "auto_add_music":   false
          }
        }
      },
      {
        "platform_account_id": 102,
        "settings": {
          "youtube": {
            "title":           "Reel estivo 2026 — InstaEditLogin demo",
            "privacy_status":  "unlisted",
            "made_for_kids":   false,
            "category_id":     22
          }
        }
      },
      {
        "platform_account_id": 103,
        "settings": {
          "instagram": {
            "collaborators":   [],
            "location_id":     null,
            "alt_text":        "Video verticale con tramonto sul mare"
          }
        }
      }
    ]
  }'
```

▸ La shape è cambiata da v1 (che aveva `UserID + Platforms list[]`)
alla **v2 / universal payload**: un array `targets[]` con
`platform_account_id` + blocco `settings` per-piattaforma. La
v1 è deprecata ma il server accetta entrambi per retro-compat
(3 mesi).

---

## Per-platform settings

`settings` è un **discriminated union** per piattaforma — la chiave
è il nome della piattaforma (`tiktok`, `youtube`, `instagram`, …)
e il valore è lo shape specifico di quella piattaforma.

### TikTok

```jsonc
{
  "tiktok": {
    "privacy_level":   "public" | "friends" | "private",   // default "public"
    "allow_comments":  true | false,                       // default true
    "allow_duet":      true | false,                       // default true
    "allow_stitch":    true | false,                       // default true
    "auto_add_music":  true | false                        // default false
  }
}
```

### YouTube

```jsonc
{
  "youtube": {
    "title":           "string (max 100)",                 // obbligatorio
    "description":     "string (max 5000)",                // opzionale
    "privacy_status":  "public" | "unlisted" | "private",  // default "unlisted"
    "made_for_kids":   true | false,                       // legalmente richiesto
    "category_id":     22,                                 // People & Blogs
    "tags":            ["string", "..."]                   // opzionale
  }
}
```

⚠️ **`made_for_kids` è legalmente obbligatorio** ai sensi del
COPPA. Se omesso il server assume `false` e logga un WARNING.

### Instagram

```jsonc
{
  "instagram": {
    "alt_text":        "string (max 1000)",
    "location_id":     "string (Facebook page-scoped ID)",
    "collaborators":   ["@user_handle", ...],
    "cover_asset_id":  "ma_… (opzionale, per Reel cover)"
  }
}
```

### Facebook Pages

```jsonc
{
  "facebook": {
    "page_id":          "string (page-scoped ID)",
    "scheduled_publish": true,
    "backdated_time":   "ISO8601 (opzionale)",
    "call_to_action":   {
      "type": "LIKE_PAGE",
      "value": { "link": "https://…" }
    }
  }
}
```

### Threads

```jsonc
{
  "threads": {
    "reply_control":   "everyone" | "accounts_you_follow",
    "topic_tag":       "string",
    "location_id":     "string"
  }
}
```

### Twitter / X

```jsonc
{
  "twitter": {
    "reply_settings":  "everyone" | "mentioned_users" | "followers",
    "quote_tweet_id":  "string (numeric)",
    "media_alt_text":  { "<asset_id>": "string" }
  }
}
```

### LinkedIn

```jsonc
{
  "linkedin": {
    "visibility":      "PUBLIC" | "CONNECTIONS",
    "article_url":     "string (per share di articolo)",
    "media_category":  "IMAGE" | "VIDEO" | "NONE"
  }
}
```

---

## Headers

| Header | Obbligatorio | Note |
|---|---|---|
| `Authorization` | ✅ | `Bearer <sk_test_… | sk_live_… | jwt>` |
| `Content-Type` | ✅ | `application/json` |
| `Idempotency-Key` | ✅ in live, ⚠️ best-effort in test | UUID v4; vedi sotto |

---

## Response

```
HTTP/1.1 202 Accepted
Location: /api/v1/posts/{id}
X-Request-Id: req_01J8X…
```

```json
{
  "id":          "post_01HXY…",
  "workspace_id": 1,
  "status":      "queued",
  "scheduled_at": "2026-07-12T15:00:00Z",
  "idempotency_key": "7b7e3a1e-…",
  "targets": [
    {
      "id":                  "pt_01HXY…101",
      "platform_account_id": 101,
      "platform":            "tiktok",
      "status":              "queued",
      "scheduled_publish_at": "2026-07-12T15:00:00Z",
      "settings_echo":       { "tiktok": { … } }
    },
    {
      "id":                  "pt_01HXY…102",
      "platform_account_id": 102,
      "platform":            "youtube",
      "status":              "queued",
      "settings_echo":       { "youtube": { … } }
    },
    {
      "id":                  "pt_01HXY…103",
      "platform_account_id": 103,
      "platform":            "instagram",
      "status":              "queued",
      "settings_echo":       { "instagram": { … } }
    }
  ]
}
```

▸ Ogni target è indipendente — un target può diventare `published`
mentre un altro `failed`. Il Post padre diventa `partially_published`
quando almeno uno è terminal ma non tutti.

---

## Status machine

```
queued ─► publishing ─► published
                  └──► failed
                  └──► waiting_provider (TikTok async, see below)
                  └──► partially_published (≥1 published, ≥1 failed)
```

`waiting_provider` è uno stato **TikTok-only**: l'API TikTok
accetta il video ma lo stato finale (processato / rifiutato / in
review) arriva 30-90s dopo via polling. Il reconciler
worker (`internal/worker/publish_worker.go`) controlla lo stato
ogni 60s.

---

## Idempotency

Con `Idempotency-Key: <UUID>`:

- **Prima richiesta** → crea post + targets → 202 Accepted
- **Retry con stessa chiave** (entro 24h) → risponde **202** con
  lo stesso `id` (NON 200 OK, perché lo stato del post potrebbe
  essere cambiato nel frattempo)
- **Retry con chiave diversa ma stesso body** → crea un nuovo
  post (l'`Idempotency-Key` è la chiave di dedupe, non l'hash del body)

Risposta in caso di conflitto (stessa chiave, body diverso):

```
HTTP/1.1 409 Conflict
```

```json
{
  "error": {
    "code":    "idempotency_key_conflict",
    "message": "Idempotency-Key già usato con un body diverso."
  }
}
```

#### Esempio

```bash
KEY=$(uuidgen)
curl -X POST "${BASE_URL}/api/v1/posts" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Idempotency-Key: $KEY" \
  -H "Content-Type: application/json" \
  -d @post.json

# Retry (es.: network blip):
curl -X POST "${BASE_URL}/api/v1/posts" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Idempotency-Key: $KEY" \
  -H "Content-Type: application/json" \
  -d @post.json
# → stesso id, stesso payload targets
```

---

## Esempio end-to-end in 5 comandi

```bash
# 1. Crea chiave
KEY=$(curl -fsS -X POST "${BASE_URL}/api/v1/api-keys" \
  -H "Authorization: Bearer ${EXISTING_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"name":"smoke","environment":"test","scopes":["posts:write","media:write"]}' \
  | jq -r .secret)

# 2. Upload media
ASSET=$(curl -fsS -X POST "${BASE_URL}/api/v1/media/presign" \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"workspace_id":1,"mime_type":"video/mp4","size_bytes":15728640,"filename":"r.mp4"}' \
  | jq -r .upload_url)
curl -fsS -X PUT "$ASSET" -H "Content-Type: video/mp4" --upload-file r.mp4
ASSET_ID=$(curl -fsS -X POST "${BASE_URL}/api/v1/media/$(uuidgen)/complete" \
  -H "Authorization: Bearer $KEY" -d '{}' | jq -r .asset_id)

# 3. OAuth TikTok (apre il browser)
open "${BASE_URL}/api/v1/auth/tiktok/login?api_key=$KEY"

# 4. Crea post
IDEM=$(uuidgen)
curl -fsS -X POST "${BASE_URL}/api/v1/posts" \
  -H "Authorization: Bearer $KEY" \
  -H "Idempotency-Key: $IDEM" \
  -H "Content-Type: application/json" \
  -d "{
    \"workspace_id\": 1,
    \"scheduled_at\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",
    \"content\": {
      \"caption\": \"Smoke test\",
      \"media\": [{\"asset_id\": \"${ASSET_ID}\"}]
    },
    \"targets\": [{
      \"platform_account_id\": 101,
      \"settings\": {\"tiktok\": {\"privacy_level\": \"public\"}}
    }]
  }"

# 5. Attendi il webhook `post.target.published` (vedi WEBHOOKS.md)
```
