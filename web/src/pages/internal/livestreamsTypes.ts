/**
 * Types for the Live streaming control center
 * (GET /api/v1/livestreams?workspace_id=N).
 *
 * Mirrors the backend `livestreamResponse` DTO
 * (pkg/api/livestreams_types.go). The YouTube resource references
 * (broadcast/stream ids) and the stream name/key are deliberately not
 * exposed by the API, so they never appear here either.
 */
export type LivestreamRow = {
  id: string;
  workspace_id: number;
  platform_account_id: number;
  /** Display name of the bound YouTube channel; empty when unknown. */
  channel_name?: string;
  title: string;
  description: string;
  privacy_status: string; // private | unlisted | public
  playback_mode: string; // loop_continuous | play_once
  schedule_type: string; // manual | now | scheduled | recurring
  scheduled_start_at?: string | null;
  desired_state: string;
  actual_state: string;
  resolution: string; // 720p30 | 1080p30
  frame_rate: number;
  auto_restart: boolean;
  /** YouTube numeric category id (empty = none). */
  category: string;
  made_for_kids: boolean;
  /** ISO 639-1 / BCP-47 code (empty = none). */
  language: string;
  /** media_assets.id of the uploaded cover (wizard Carica immagine). */
  thumbnail_media_id?: string | null;
  dvr_enabled: boolean;
  auto_start: boolean;
  auto_stop: boolean;
  /** normal | low | ultraLow */
  latency_preference: string;
  created_at: string;
  updated_at: string;
};

export type LivestreamsResponse = {
  items: LivestreamRow[];
  next_cursor?: string;
  has_more?: boolean;
};

/**
 * Per-channel preflight row from GET /api/v1/livestreams/channels
 * (creation-wizard step 1). LiveEnabled means the persisted grant
 * carries a YouTube live scope (youtube / youtube.force-ssl) — the
 * necessary condition for the Live Streaming API.
 */
export type LivestreamChannel = {
  platform_account_id: number;
  username: string;
  platform_user_id: string;
  account_state: "valid" | "reconnect_required" | "suspended" | "deleted" | string;
  oauth_ready: boolean;
  live_enabled: boolean;
  last_verified_at?: string | null;
  active_lives: number;
};

export type LivestreamChannelsResponse = {
  channels: LivestreamChannel[];
};

export type LivestreamTab =
  | "all"
  | "live"
  | "scheduled"
  | "drafts"
  | "ended"
  | "errors";

export type LivestreamSummary = {
  live: number;
  scheduled: number;
  reconnecting: number;
  errors: number;
};

/**
 * One row of GET /api/v1/media — the Media Library entry the live
 * wizard's step 3 renders. Probe fields stay null until the upload
 * worker probes the asset (ffprobe); `live_compatibility` is derived
 * server-side so the frontend never re-implements the profile check.
 */
export type MediaLibraryItem = {
  id: string;
  filename: string;
  content_type: string;
  size_bytes: number;
  created_at: string;
  /** Short-lived presigned GET URL (15 min) for `<video>` preview. */
  preview_url?: string;
  duration_seconds?: number | null;
  width?: number | null;
  height?: number | null;
  fps?: number | null;
  has_audio?: boolean | null;
  video_codec?: string;
  audio_codec?: string;
  probed_at?: string | null;
  live_compatibility: "ready" | "needs_normalization" | "unknown";
};

export type MediaLibraryResponse = {
  items: MediaLibraryItem[];
  next_cursor?: string;
  has_more?: boolean;
};

/**
 * Step-2 wizard form state (Configurazione YouTube). Lifted to the
 * page so back → forward preserves the entries; steps 3–5 (contenuti,
 * riproduzione, riepilogo) consume it when they land.
 */
export type LivestreamStep2Form = {
  title: string;
  description: string;
  privacy: "private" | "unlisted" | "public";
  category: string; // YouTube numeric id, "" = none
  madeForKids: boolean;
  language: string; // ISO 639-1 code, "" = none
  thumbnailMediaId: string | null;
  dvr: boolean;
  autoStart: boolean;
  autoStop: boolean;
  latency: "normal" | "low" | "ultraLow";
};

export const EMPTY_STEP2_FORM: LivestreamStep2Form = {
  title: "",
  description: "",
  privacy: "unlisted",
  category: "",
  madeForKids: false,
  language: "",
  thumbnailMediaId: null,
  dvr: false,
  autoStart: false,
  autoStop: false,
  latency: "normal",
};

/**
 * Known YouTube video category ids (videoCategories.list, default
 * global region). Static list for the wizard select — YouTube remains
 * the authority at broadcast creation time.
 */
export const YOUTUBE_CATEGORIES: Array<{ id: string; label: string }> = [
  { id: "1", label: "Film e animazione" },
  { id: "2", label: "Auto e veicoli" },
  { id: "10", label: "Musica" },
  { id: "15", label: "Animali domestici" },
  { id: "17", label: "Sport" },
  { id: "18", label: "Cortometraggi" },
  { id: "19", label: "Viaggi e eventi" },
  { id: "20", label: "Gaming" },
  { id: "21", label: "Video blog" },
  { id: "22", label: "Persone e blog" },
  { id: "23", label: "Commedia" },
  { id: "24", label: "Intrattenimento" },
  { id: "25", label: "Notizie e politica" },
  { id: "26", label: "Istruzione e tutorial" },
  { id: "27", label: "Educazione" },
  { id: "28", label: "Scienza e tecnologia" },
  { id: "29", label: "Non profit e attivismo" },
  { id: "42", label: "Shorts" },
];

/**
 * ISO 639-1 languages offered by the wizard (matches the app's
 * language-flag set plus the most common YouTube live languages).
 */
export const LIVESTREAM_LANGUAGES: Array<{ code: string; label: string }> = [
  { code: "it", label: "Italiano" },
  { code: "en", label: "Inglese" },
  { code: "es", label: "Spagnolo" },
  { code: "fr", label: "Francese" },
  { code: "de", label: "Tedesco" },
  { code: "pl", label: "Polacco" },
  { code: "ru", label: "Russo" },
  { code: "id", label: "Indonesiano" },
  { code: "tr", label: "Turco" },
  { code: "hi", label: "Hindi" },
];

/** liveBroadcast.contentDetails.latencyPreference options. */
export const LATENCY_OPTIONS: Array<{
  value: "normal" | "low" | "ultraLow";
  label: string;
  description: string;
}> = [
  { value: "normal", label: "Normale", description: "Latenza standard, qualità massima" },
  { value: "low", label: "Bassa", description: "~10s di ritardo, per interazioni live" },
  { value: "ultraLow", label: "Ultra bassa", description: "~5s di ritardo, per gaming e sport" },
];
