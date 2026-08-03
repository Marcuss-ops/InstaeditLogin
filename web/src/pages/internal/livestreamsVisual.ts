import {
  LATENCY_OPTIONS,
  LIVESTREAM_LANGUAGES,
  YOUTUBE_CATEGORIES,
  type LivestreamRow,
  type LivestreamSummary,
  type LivestreamTab,
} from "./livestreamsTypes";

export type Tone = "success" | "warning" | "info" | "neutral" | "danger";

export const toneClasses: Record<Tone, string> = {
  success: "border-emerald-500/25 bg-emerald-500/[0.08] text-emerald-300",
  warning: "border-amber-500/25 bg-amber-500/[0.08] text-amber-300",
  info: "border-blue-500/25 bg-blue-500/[0.08] text-blue-300",
  neutral: "border-white/[0.12] bg-white/[0.04] text-[#cdd2da]",
  danger: "border-red-500/25 bg-red-500/[0.08] text-red-300",
};

/**
 * Tab → actual_state partition.
 *
 * Every state maps to exactly one tab (plus "Tutte"):
 *   - Live ora   → live, degraded, testing  (on air: public live,
 *                 live with problems, pre-live check)
 *   - Programmate → scheduled
 *   - Bozze      → draft, preparing, ready  (not yet on air)
 *   - Terminate  → completed, cancelled
 *   - Errori     → failed
 * Transient lifecycle states (starting, waiting_for_ingest,
 * reconnecting, stopping) surface only under "Tutte"; "In
 * riconnessione" also gets its own summary card so it can never be
 * missed while a stream is flapping.
 */
export const LIVESTREAM_TABS: Array<{ id: LivestreamTab; label: string }> = [
  { id: "all", label: "Tutte" },
  { id: "live", label: "Live ora" },
  { id: "scheduled", label: "Programmate" },
  { id: "drafts", label: "Bozze" },
  { id: "ended", label: "Terminate" },
  { id: "errors", label: "Errori" },
];

const LIVE_STATES = new Set(["live", "degraded", "testing"]);
const DRAFT_STATES = new Set(["draft", "preparing", "ready"]);
const ENDED_STATES = new Set(["completed", "cancelled"]);

export function isLiveNow(row: LivestreamRow): boolean {
  return LIVE_STATES.has(row.actual_state);
}

export function isScheduled(row: LivestreamRow): boolean {
  return row.actual_state === "scheduled";
}

export function isReconnecting(row: LivestreamRow): boolean {
  return row.actual_state === "reconnecting";
}

export function isFailed(row: LivestreamRow): boolean {
  return row.actual_state === "failed";
}

export function isDraft(row: LivestreamRow): boolean {
  return DRAFT_STATES.has(row.actual_state);
}

export function isEnded(row: LivestreamRow): boolean {
  return ENDED_STATES.has(row.actual_state);
}

export function matchesTab(row: LivestreamRow, tab: LivestreamTab): boolean {
  switch (tab) {
    case "all":
      return true;
    case "live":
      return isLiveNow(row);
    case "scheduled":
      return isScheduled(row);
    case "drafts":
      return isDraft(row);
    case "ended":
      return isEnded(row);
    case "errors":
      return isFailed(row);
  }
}

/**
 * Counts for the four summary cards. The "Live ora" count deliberately
 * includes degraded/testing (on-air streams), while the sidebar badge
 * (useActiveLiveCount) stays strict on `actual_state === "live"` — the
 * badge is a glanceable "how many are broadcasting" hint, this number
 * is an operator view of everything currently on air.
 */
export function summarize(items: LivestreamRow[]): LivestreamSummary {
  const summary: LivestreamSummary = { live: 0, scheduled: 0, reconnecting: 0, errors: 0 };
  for (const row of items) {
    if (isLiveNow(row)) summary.live += 1;
    if (isScheduled(row)) summary.scheduled += 1;
    if (isReconnecting(row)) summary.reconnecting += 1;
    if (isFailed(row)) summary.errors += 1;
  }
  return summary;
}

const STATE_LABELS: Record<string, string> = {
  draft: "Bozza",
  preparing: "Preparazione",
  ready: "Pronta",
  scheduled: "Programmata",
  starting: "Avvio",
  waiting_for_ingest: "In attesa di ingest",
  testing: "In test",
  live: "LIVE",
  degraded: "Degradata",
  reconnecting: "In riconnessione",
  stopping: "Stop in corso",
  completed: "Terminata",
  failed: "Errore",
  cancelled: "Annullata",
};

export function stateLabel(actualState: string): string {
  return STATE_LABELS[actualState] ?? actualState;
}

export function stateTone(actualState: string): Tone {
  switch (actualState) {
    case "live":
      return "success";
    case "degraded":
    case "failed":
      return "danger";
    case "reconnecting":
      return "warning";
    case "testing":
    case "starting":
    case "waiting_for_ingest":
      return "info";
    default:
      return "neutral";
  }
}

/**
 * Derived stream health. The raw YouTube ingest health (bitrate, FPS,
 * audio) lands with the livestream worker via livestream_runs — until
 * then the state machine is the best honest signal available.
 */
export function healthOf(row: LivestreamRow): { label: string; tone: Tone } {
  switch (row.actual_state) {
    case "live":
      return { label: "Ottima", tone: "success" };
    case "degraded":
      return { label: "Scarsa", tone: "danger" };
    case "testing":
      return { label: "In test", tone: "info" };
    case "reconnecting":
      return { label: "Riconnessione", tone: "warning" };
    case "starting":
    case "waiting_for_ingest":
      return { label: "Avvio…", tone: "info" };
    case "stopping":
      return { label: "Stop in corso", tone: "neutral" };
    case "completed":
      return { label: "Terminata", tone: "neutral" };
    case "cancelled":
      return { label: "Annullata", tone: "neutral" };
    case "failed":
      return { label: "Errore", tone: "danger" };
    default:
      return { label: "Non avviata", tone: "neutral" };
  }
}

export function playbackModeLabel(mode: string): string {
  switch (mode) {
    case "loop_continuous":
      return "Playlist in loop";
    case "play_once":
      return "Riproduci una volta";
    default:
      return mode || "—";
  }
}

export function privacyLabel(privacy: string): string {
  switch (privacy) {
    case "private":
      return "Privata";
    case "unlisted":
      return "Non in elenco";
    case "public":
      return "Pubblica";
    default:
      return privacy || "—";
  }
}

export function categoryLabel(id: string): string {
  if (!id) return "Nessuna categoria";
  const found = YOUTUBE_CATEGORIES.find((c) => c.id === id);
  return found ? found.label : id;
}

export function languageLabel(code: string): string {
  if (!code) return "Non impostata";
  const found = LIVESTREAM_LANGUAGES.find((l) => l.code === code);
  return found ? found.label : code;
}

export function latencyLabel(value: string): string {
  const found = LATENCY_OPTIONS.find((o) => o.value === value);
  return found ? found.label : value || "Normale";
}

export function formatScheduledAt(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  return date.toLocaleString(undefined, {
    day: "2-digit",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function scheduleLabel(row: LivestreamRow): string {
  switch (row.schedule_type) {
    case "manual":
      return "Avvio manuale";
    case "now":
      return "Avvio immediato";
    case "recurring":
      return "Ricorrente";
    case "scheduled":
      return row.scheduled_start_at
        ? `Programmata: ${formatScheduledAt(row.scheduled_start_at)}`
        : "Programmata";
    default:
      return row.schedule_type || "—";
  }
}

/**
 * Stream duration. Not exposed by the API yet — it lives on
 * livestream_runs, which the worker will populate once broadcasts can
 * actually start. Until then every card renders "—" instead of a
 * misleading value derived from config creation time.
 */
export function durationLabel(_row: LivestreamRow): string {
  return "—";
}

/**
 * Relative "ultima verifica" label for the creation wizard.
 * Null/absent values render "Mai verificato" — deliberately distinct
 * from a date so missing data is visible instead of silently claiming
 * a recent check.
 */
export function formatLastVerified(value?: string | null): string {
  if (!value) return "Mai verificato";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "Mai verificato";
  const elapsedMs = Date.now() - date.getTime();
  const minutes = Math.floor(elapsedMs / 60_000);
  if (minutes < 1) return "Ora";
  if (minutes < 60) return `${minutes} min fa`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} h fa`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days} gg fa`;
  return date.toLocaleDateString(undefined, { day: "2-digit", month: "short", year: "numeric" });
}
