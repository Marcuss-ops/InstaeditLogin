# Media upload via presigned URL

L'upload del media (immagine o video) è un flusso a **tre step**:

1. **Presign** — `POST /api/v1/media/presign` ti restituisce un URL
   firmato S3-compatible e un `asset_id`.
2. **Upload** — `PUT` diretto del file sull'URL firmato (il client
   carica da browser o da CLI).
3. **Complete** — `POST /api/v1/media/{asset_id}/complete` notifica
   il server che l'upload è terminato e sblocca il media per i post.

Questo pattern evita di inoltrare il blob attraverso il server
InstaEditLogin (che resta snello) e usa direttamente lo storage
S3-compatible.

## Step 1 — Presign

```bash
curl -X POST "${BASE_URL}/api/v1/media/presign" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "workspace_id": 1,
    "mime_type":     "video/mp4",
    "size_bytes":    15728640,
    "filename":      "reel-2026-07-12.mp4"
  }'
```

Risposta **`200 OK`**:

```json
{
  "asset_id":      "ma_01HXY…",
  "upload_url":    "https://media-prod.s3.example.com/upload/…?X-Amz-Signature=…",
  "upload_method": "PUT",
  "upload_headers": {
    "Content-Type": "video/mp4"
  },
  "expires_at":    "2026-07-12T10:15:00Z",
  "max_bytes":     15728640
}
```

▸ **`upload_url` è valido per 15 minuti** dalla firma. Trascorso
quello scade `403 Forbidden` dal bucket. Rigenera con un nuovo
presign.

### Vincoli per tipo

| MIME type | Max bytes | Note |
|---|---|---|
| `image/jpeg` | 8 MB | Instagram, Facebook, Threads, LinkedIn |
| `image/png` | 8 MB | Come sopra |
| `video/mp4` | 1 GB | Tutte le piattaforme video |
| `video/quicktime` | 1 GB | TikTok (consigliato transcodificare in mp4) |

## Step 2 — Upload a S3

```bash
curl -X PUT "${UPLOAD_URL}" \
  -H "Content-Type: video/mp4" \
  --upload-file reel-2026-07-12.mp4
```

Risposta attesa **`200 OK`** dal bucket (S3 risponde con corpo vuoto + ETag).

### Da browser (form diretto)

```html
<form method="POST" action="${UPLOAD_URL}" enctype="multipart/form-data">
  <input type="file" name="file" accept="video/mp4">
  <button type="submit">Upload</button>
</form>
```

⚠️ Per il CORS bucket è già configurato lato server per accettare
`PUT` da qualunque origine registrata nel workspace.

## Step 3 — Complete

```bash
curl -X POST "${BASE_URL}/api/v1/media/${ASSET_ID}/complete" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "width":  1080,
    "height": 1920,
    "duration_seconds": 17.5
  }'
```

Risposta **`200 OK`**:

```json
{
  "asset_id":      "ma_01HXY…",
  "status":        "ready",
  "public_url":    "https://media-prod.cdn.example.com/ma_01HXY….mp4",
  "mime_type":     "video/mp4",
  "size_bytes":    15728640,
  "width":         1080,
  "height":        1920,
  "duration_seconds": 17.5
}
```

▸ A questo punto `public_url` è pronto per essere usato in
`POST /api/v1/posts.targets[].media` (vedi [`POSTS.md`](POSTS.md)).

## Asset lifecycle

```
presigned (15min) → uploaded (in attesa di /complete) → ready
                                            │
                                            └→ expired (se /complete non chiamato in 24h)
```

Il worker di cleanup gira ogni 6h e rimuove asset rimasti
`uploaded` oltre 24h per liberare spazio bucket.

## Errori comuni

| HTTP | Code | Quando |
|---|---|---|
| `400` | `validation_error` | MIME non supportato o `size_bytes > max_bytes` |
| `401` | `authentication_error` | API key mancante o revocata |
| `403` | `media_invalid` | Workspace_id non appartiene al chiamante |
| `413` | `media_invalid` | Upload reale supera `max_bytes` |
| `422` | `validation_error` | Step 3 chiamato su asset in stato != `uploaded` |
| `502` | `provider_unavailable` | Bucket S3 irraggiungibile |

## Da CLI (esempio completo)

```bash
#!/bin/bash
set -euo pipefail

PRESIGN=$(curl -fsS -X POST "${BASE_URL}/api/v1/media/presign" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d "{\"workspace_id\":1,\"mime_type\":\"video/mp4\",\"size_bytes\":$(stat -c%s \"$1\"),\"filename\":\"$(basename "$1")\"}")

ASSET_ID=$(jq -r .asset_id <<<"$PRESIGN")
UPLOAD_URL=$(jq -r .upload_url <<<"$PRESIGN")
CONTENT_TYPE=$(jq -r .upload_headers.\"Content-Type\" <<<"$PRESIGN")

curl -fsS -X PUT "$UPLOAD_URL" \
  -H "Content-Type: $CONTENT_TYPE" \
  --upload-file "$1"

curl -fsS -X POST "${BASE_URL}/api/v1/media/${ASSET_ID}/complete" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{}'

echo "✓ Uploaded $1 → asset $ASSET_ID"
```
