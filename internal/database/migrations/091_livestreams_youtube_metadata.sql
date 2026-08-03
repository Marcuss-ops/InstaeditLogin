-- Livestream module — YouTube broadcast metadata (wizard step 2).
--
-- Adds the per-broadcast YouTube snippet / status / contentDetails
-- fields collected by the creation wizard's "Configurazione YouTube"
-- step. Every column is operator-owned (PATCH-able); the livestream
-- worker maps them onto the liveBroadcast resource at prepare time:
--
--   category            liveBroadcast.snippet.categoryId (YouTube
--                       numeric id, e.g. "24" Entertainment; "" = none)
--   made_for_kids       liveBroadcast.status.madeForKids (+
--                       selfDeclaredMadeForKids)
--   language            liveBroadcast.snippet.defaultLanguage
--                       (ISO 639-1 code, e.g. "it"; "" = none)
--   thumbnail_media_id  media_assets.id of the uploaded cover
--                       (wizard "Carica immagine"; Media Library and
--                       Dark Editor sources land with later releases)
--   dvr_enabled         liveBroadcast.contentDetails.enableDvr
--   auto_start          liveBroadcast.contentDetails.enableAutoStart
--   auto_stop           liveBroadcast.contentDetails.enableAutoStop
--   latency_preference  liveBroadcast.contentDetails.latencyPreference
--                       (normal | low | ultraLow)
--
-- Idempotent: ADD COLUMN IF NOT EXISTS per column; greenfield and
-- already-migrated databases converge to the same schema.
ALTER TABLE livestreams
    ADD COLUMN IF NOT EXISTS category           TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS made_for_kids      BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS language           TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS thumbnail_media_id UUID    REFERENCES media_assets(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS dvr_enabled        BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS auto_start         BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS auto_stop          BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS latency_preference TEXT    NOT NULL DEFAULT 'normal'
                                                    CHECK (latency_preference IN ('normal', 'low', 'ultraLow'));
