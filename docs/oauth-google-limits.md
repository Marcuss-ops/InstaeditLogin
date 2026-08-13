# Google OAuth Limits — Capacity Planning

Part of the [Google OAuth Testing and Production Setup](OAUTH-PRODUCTION.md)
documentation set. This file holds the **capacity limits** the
200-channel rollout must plan around:

- 50–100 refresh tokens per `(OAuth client, Google Account)` pair
- 100 channels per Google Account
- `channels.list?mine=true` pagination + the 40–50 per-manager cap
- YouTube Data API v3 "Video Uploads" bucket (2026 quota model)

Related documents:

- [Console setup walkthrough](oauth-google-setup.md)
- [200-Channel rollout workflow](oauth-google-rollout.md)
- [Monitoring refresh-token TTL](oauth-google-monitoring.md)

## 50–100 refresh tokens per OAuth client + Google Account pair

Each combination of (OAuth client_id, Google Account) holds at most
**50–100 active refresh tokens** at any time. When the cap is hit,
**Google silently invalidates the oldest token** without notifying
the app. (Google's official OAuth 2.0 documentation cites 50; some
2024+ third-party write-ups cite 100; the conservative 50 figure
gives the operator more headroom.)

For the 200-channel rollout, this means:

* One Google Account can directly manage ≤ 50 channels through a
  single OAuth client without triggering silent eviction.
* For 200 channels, **distribute the channels across 4–5 manager
  Google Accounts** — for example 40–50 channels per manager — to
  leave headroom for re-auths and rotations. Targeting 50 exactly
  leaves no buffer for connection-state churn.
* Each manager account performs its own OAuth flow; the resulting
  refresh tokens are stored per `platform_accounts.platform_user_id`
  on the corresponding `youtube` row.

The token-count limit is documented at Google's official OAuth 2.0
guide
([developers.google.com/identity/protocols/oauth2 — Expiration](https://developers.google.com/identity/protocols/oauth2#expiration))
and cross-referenced at
[Google Support](https://support.google.com/youtube/answer/3046356);
it is enforced server-side.

## 100 channels per Google Account

A single Google Account can manage up to **100 YouTube channels**
(each with its own `UC…` channel id). Beyond that, the extra channels
cannot be transferred into the account. For the 200-channel
deployment, distribute channels across 4–5 manager accounts as
detailed in [Step 8](oauth-google-setup.md#step-8--distribute-the-200-channels-across-manager-accounts).

## `channels.list?mine=true` pagination + 40–50 channels per manager

A single Google Account can be granted access to up to **100 YouTube
channels**, all managed under the same OAuth grant. The pre-2024
InstaEdit code path calls `channels.list?mine=true&maxResults=50`
without following `nextPageToken`, so it can only see the first 50
channels of any manager. As soon as a manager exceeds 50 channels the
remaining ones are invisible to the channel-binding check and the
publisher will silently act on a wrong channel.

**Hard cap per manager: 40–50 channels.** Picking the floor (40 +
margin) keeps `channels.list` responses to a single page (no
`nextPageToken` chasing needed for the pre-upload binding check) and
keeps every manager comfortably under both Google's 100-channels-per-
Account cap and the 50–100 refresh-tokens-cap-per-`(Google Account,OAuth client)` cap. **Operators MUST NOT exceed 50 channels per
manager.** To exceed this hard cap, BOTH preconditions below MUST
be verified live on a test account first:

1. The YouTube service has been upgraded to follow `nextPageToken`,
   loop until the response returns an empty `nextPageToken`, and
   tolerate up to 200 channels in a single grant (the server-side
   `mine=true` maximum the API exposes today).
2. The operator has confirmed the manager's refresh-token count
   stays below the 50-100 silent-invalidation cap (see the limit
   above).

**The failure mode the cap prevents.** Going past 50 channels per
manager HARD-BLOCKS every channel beyond the 50th in that manager's
set. `channels.list?mine=true&maxResults=50` returns only the first
50, so any expected `UC…` past position 50 is INVISIBLE to
`ValidateChannelBinding` in `internal/services/youtube_oauth.go`. The
function returns the typed `ErrYouTubeChannelMismatch` sentinel; the
publish worker (around `internal/worker/publish_worker.go:434`) treats
that sentinel as terminal and calls `MarkReauthRequired` on the
platform_account — flipping `status='reauth_required'` and stamping
`reauth_required_at=NOW()`. The channel is then BRICKED: the
post_target is marked `'failed'`, the publish queue stops retrying,
and the operator must complete a full new OAuth dance against Google
(consent click → new refresh_token grant) to recover the channel.
There is no in-app bypass — no admin "flips the flag back" route,
no auto-retry that escapes the cap, no 5th-manager overflow lane.

Per the actual code path, the failure is therefore STRICTLY WORSE
than a wrong-target upload: a wrong-target upload would still show
up in the Step 7 `snippet.channelId` reconciliation check and the
PostGreSQL row would survive with the correct status. A
maxResults=50 truncation flips the platform_account to a state
where every publish for the affected channel is permanently
rejected until full re-consent. Operators MUST honor the 50-channel
cap exactly because every channel past it becomes unactionable
from the publisher. Until the `nextPageToken` pagination ships
(see Step 8 follow-up), the cap is a single-page response
guarantee — no exceptions, no over-the-cap routing on a 5th manager.

For the 200-channel rollout: **4–5 managers, ≤50 channels each,
single-page `channels.list` today**. Distribute by **rotating secondary
channels across managers** so no single manager gets all of its
channels revoked at once if an OAuth grant is later revoked from
[Google's third-party apps page](https://myaccount.google.com/permissions).

> **Aggiornamento 2026**: il sistema ora gestisce fino a **200 canali per grant** con paginazione attiva (vedi Step 3.3). La regola "max 40-50 canali per manager" indicata in Step 8 resta valida come **budget operativo consigliato** (UI saturation threshold), NON come hard limit tecnico. Operatori con >50 canali per manager devono: (a) verificare che il refresh-token count resti sotto il cap 50-100 per la coppia `(Google Account, OAuth client)`, e (b) confermare che il loro `channels.list?mine=true` con paginazione copra l'intero channel set del manager.

## YouTube Data API v3 — Video Uploads bucket (2026 model)

Since **1° giugno 2026**, YouTube charges `videos.insert` against its
own dedicated "Video Uploads" bucket instead of the older shared
"units" budget that mixed read + write calls under one number. The math
this doc used to print (`10,000 units/day default ÷ 1,600 units per call
= ~6 videos.insert/day (LEGACY pre-2026 / 1600 math; current 2026 model: 1 video = 1 bucket unit, default 100/day)`) is **obsolete as of 2026-06-01**.

* **Cost per call**: `1` bucket unit per `videos.insert`.
* **Default daily cap**: `100` `videos.insert` per Google Cloud project
  per day (≈ 1 upload every 15 minutes — fine for a single-channel dev
  app, way too tight for a 200-channel operator fleet).
* **Multiplier**: bucket units are spent 1-to-1 against `videos.insert`.
  Adding `N` bucket units to the daily cap buys exactly `N` more daily
  `videos.insert` calls. The legacy `units [×*] 1600` / `[/÷] 1600` arithmetic (pre-2026-06-01); current 2026 model: 1 video = 1 bucket unit
  you may see elsewhere in the Google docs does NOT apply to this
  bucket.
* **Scope (very important)**: this bucket is **per Google Cloud project**,
  NOT per manager and NOT per Google Account. InstaEdit uses ONE Google
  Cloud project, so all 4–5 manager accounts draw from the SAME daily
  budget. The 300–400 /day request below is the **total fleet budget**
  — not per-manager. An operator who interprets it as per-manager will
  plan around 1,200–2,000 /day instead of 300–400 /day and submit a
  quota request that Google will reject out of hand.

For the 200-channel daily target (200 calls/day steady-state + retries +
canary private uploads + test traffic + margin) request **at least 300
bucket units/day**; ideally **400** so the rollout keeps a 50–100% buffer.
The Google-published, cross-verified quota increase URL is
[Quota Calculator](https://developers.google.com/youtube/v3/determine_quota_cost).

How to submit the increase request: [Step 6 — quota increase](oauth-google-setup.md#step-6--request-a-youtube-data-api-v3-quota-increase).

## General bucket — budget 100 upload + 100 copertine/giorno (2026)

Oltre al bucket dedicato "Video Uploads", dal **1° giugno 2026** Google
ha un secondo bucket rilevante per InstaEdit: il bucket **"general"**
(altre API, default **10.000 unità/giorno** per Google Cloud project).
Il modello a 3 bucket è implementato in
`internal/services/youtube_quota_manager.go` (`YouTubeQuotaManager`) e
persistito per-giorno-Pacifico in `youtube_quota_daily` (migration
124); la tabella dei costi è la mappa `youtubeOperationSpecs`:

| Operazione          | Bucket          | Costo | Note                                   |
| ------------------- | --------------- | ----- | -------------------------------------- |
| `videos.insert`     | video_uploads   | 1     | cap 100/giorno, contatore dedicato     |
| `search.list`       | searches        | 1     | cap 100/giorno, bucket separato        |
| `videos.update`     | general         | 50    | pubblicazione/metadata                 |
| `thumbnails.set`    | general         | 50    | copertina personalizzata               |
| `videos.list`       | general         | 1     | verifica/ownership/reconciliation      |
| `channels.list`     | general         | 1     | binding canale / `mine=true`           |

### Il problema: 100 upload + 100 copertine saturano il bucket general

Con **100 video al giorno** e **copertina personalizzata su tutti** il
conto del bucket general è il seguente (prima della migration al
`publishAt` nativo, commit `88c2495d`):

```text
videos.update       100 × 50 =  5.000 unità
thumbnails.set      100 × 50 =  5.000 unità
-----------------------------------------
TOTALE general                = 10.000 unità  →  100% del cap giornaliero
```

Il bucket general è a **quota piena**, **zero margine** per:

* `videos.list` (reconciliation, verifica ownership prima della
  copertina — `GetYouTubeVideo` nei handler e nel reconciler);
* `channels.list` (binding check pre-upload, ~46 call sites nel
  codebase);
* **retry** transitori (`PublishThumbnail` e `SetThumbnail` ritentano
  internamente fino a 3 volte con `doWithRetry` — ogni retry brucia
  altre 50 unità);
* qualunque altra operazione di manutenzione/metadata.

`YouTubeQuotaManager` riserva le unità **prima** della chiamata API
(`ReserveOperation` → `FOR UPDATE` su `youtube_quota_daily`), quindi
quando il bucket general è a quota il pipeline si ferma **prima** di
spendere chiamate reali — ma con 100 copertine + 100 update il blocco
arriva già nel pomeriggio. **Con il vecchio flusso, 100/giorno non era
sicuro.**

### La mitigazione già in produzione: `status.publishAt` nativo

Dal commit `88c2495d` i video **programmati** (publish_at futuro +
privacy desiderata `public`) passano `status.publishAt` direttamente
nel `videos.insert` privato: **YouTube è l'orologio** — pubblica da
solo alla scadenza, anche se i nostri server sono giù — e **il
`videos.update` post-publish_at non viene più emesso** (il publish
worker salta la `videos.update` quando il flag `native_publish_at` è
presente sulla riga `youtube_target_publications`).

**Controllo e recovery, non orologio**: il publish worker non marca
più `published` alla cieca — prima verifica lo stato reale del video
via `videos.list` (1 unità dal bucket general, `settleNativePublish`
in `internal/worker/publish_worker_youtube.go`): se pubblico, stampa
il target senza `videos.update`; se ancora privato **entro la finestra
di grazia di 10 minuti** dal publish_at, ri-accoda il target con
backoff e riverifica al tick successivo (YouTube sta ancora
transitando — mai gareggiare con 50 unità); se ancora privato **oltre
i 10 minuti** (YouTube ha mancato la transizione programmata),
**recovery**: forza il pubblico con un singolo `videos.update`. Costo
aggiuntivo: 1 `videos.list` (1 unità) per video programmato — già
incluso nella voce `N_videos_list` della formula di budget sotto.

Il `ReconcileWorker` resta recovery-only: sistema solo le righe bloccate
in `publishing` interrogando lo stato della piattaforma; i video
programmati che si stabilizzano via `settleNativePublish` non entrano
mai in quella finestra.

Risultato per lo scenario **100 upload + 100 copertine, tutti
programmati**:

```text
video_uploads       100 × videos.insert (1)  = 100/100 chiamate  (al cap)
general             100 × thumbnails.set (50) =  5.000 unità
                    videos.update             =      0  ← risparmiate 5.000
----------------------------------------------
TOTALE general                              =  5.000 / 10.000  →  50% libero
```

**50% di headroom** sul bucket general: spazio per reconciliation
(`videos.list` ≈ 1 unità per video + `channels.list`), retry e margine
operativo. Il collo di bottiglia diventa il bucket `video_uploads`
(100 chiamate) — non il bucket general.

### Formula di budget da usare in pianificazione

```text
unità_general/giorno =
    50 × N_update_immediati      (video pubblicati senza publishAt nativo)
  + 50 × N_copertine             (thumbnails.set, una per video coperto)
  +  1 × N_videos_list           (reconciliation + verifica ownership)
  +  1 × N_channels_list         (binding check, min=1 per upload)
  + margine retry                (≈ 50–150 unità, 1–3 retry copertina/update)
```

Soglie operative consigliate (cap default 10.000):

* **≤ 100 copertine/giorno e tutti i video programmati**: 5.000–5.500
  unità → **sicuro**, nessuna richiesta quota necessaria.
* **≥ 50 update immediati + 100 copertine**: 7.500–8.000 unità →
  **richiedere aumento** del bucket general a 15.000–20.000 via
  [Quota Calculator](https://developers.google.com/youtube/v3/determine_quota_cost).
* **100 update immediati + 100 copertine**: 10.000+ → **sopra il cap**,
  blocco garantito; convertire al `publishAt` nativo oppure richiedere
  l'aumento prima del rollout.

---

## Piano copertine (thumbnails) — stato attuale e piano automatico

### Cosa esiste già oggi

1. **`SetThumbnail`** (`internal/services/youtube_channel_thumbnail.go`):
   chiamata `thumbnails.set` pura (PNG/JPEG ≤ 2 MB), 50 unità dal
   bucket general. Usata dal handler
   `POST /api/v1/groups/{group_id}/youtube/videos/{video_id}/thumbnail`
   (`pkg/api/youtube_group_videos_thumbnail.go`) — flusso
   **THUMBNAIL-ONLY**: non tocca privacy né snippet.
2. **`PublishThumbnail`** (stesso file): `thumbnails.set` + `videos.update`
   in un'unica sequenza con retry (3 tentativi) — usata dal publish
   degli editor-sessions (`pkg/api/youtube_editor_sessions_by_project_publish.go`).
   Costo combinato: **50 + 50 = 100 unità** dal bucket general.
3. **Stato machine deliveries** (`internal/deliveries/state.go`):
   `THUMBNAIL_PENDING → THUMBNAIL_UPLOADING → THUMBNAIL_APPLIED`
   (con `THUMBNAIL_FAILED` come exit terminale post-`PRIVATE_UPLOADED`,
   invariante privacy=private preservata). La transizione
   `THUMBNAIL_APPLIED → READY_TO_PUBLISH` è condizionata dal flag
   `require_thumbnail` del contratto
   (`docs/velox-instaedit-contract.md` §10, riga 124).
4. **Capability `SetThumbnail`** nel target catalog
   (`internal/deliveries/target_catalog.go`): YouTube espone oggi
   `UploadVideo=true, SetThumbnail=true, Publish=true, Schedule=true`;
   `CapabilitiesForTarget` è il punto centrale dove una piattaforma
   senza copertine (es. TikTok oggi) restituisce `SetThumbnail=false`.

### Cosa manca al piano automatico (da implementare)

Il **piano copertine** consiste nel far applicare la copertina
**automaticamente dal worker**, senza intervento umano, per ogni
video che ha una `thumbnail_media_id`/`cover_template_version_id`
risolta (snapshot già presenti in
`internal/worker/publish_snapshot_metadata.go`):

1. **Fase di consegna**: dopo `PRIVATE_UPLOADED`, se il target ha la
   capability `SetThumbnail` e la copertina è risolta, transizione
   `THUMBNAIL_PENDING → THUMBNAIL_UPLOADING → THUMBNAIL_APPLIED`
   eseguita dal worker con `SetThumbnail` (50 unità general).
2. **Orchestrazione quota**: ogni `thumbnails.set` deve passare da
   `YouTubeQuotaManager.ReserveOperation(YouTubeOpThumbnailsSet)`
   prima della chiamata, così il worker si ferma **prima** di
   superare le 10.000 unità invece di ricevere `quotaExceeded` a metà
   giornata.
3. **Niente `videos.update` per i programmati**: per i video con
   `publishAt` nativo la copertina è l'**unico** costo general (50
   unità); non ricombinare mai `PublishThumbnail` (che aggiunge 50
   unità di `videos.update`) nel percorso automatico dei video
   programmati — usare `SetThumbnail` puro.
4. **Retry e margine**: con `doWithRetry` (3 tentativi) ogni copertina
   può costare fino a 150 unità in caso di errori transitori; il
   budget di cui sopra include già questo margine.

### Riepilogo budget con il piano copertine completo

| Scenario (100 video/giorno)                | video_uploads | general units | Esito                |
| ------------------------------------------ | ------------- | ------------- | -------------------- |
| 100 programmati + 100 copertine            | 100/100       | ~5.000        | ✅ sicuro            |
| 100 immediati + 100 copertine (vecchio)    | 100/100       | ~10.000+      | ❌ quota exceeded    |
| 100 programmati, copertine solo dove serve | 100/100       | 100–5.000     | ✅ sicuro            |

Il `publishAt` nativo (già in main) è la chiave: trasforma lo scenario
critico in uno con il 50% di bucket general libero, e il piano
copertine può essere abilitato in sicurezza fino a ~100 copertine/
giorno senza richieste di aumento quota.
