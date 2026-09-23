export type CalendarProgressRow = {
  key: string;
  label: string;
  status: string;
  progress?: number;
};

const asRecord = (value: unknown): Record<string, unknown> | undefined =>
  value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;

function firstText(value: Record<string, unknown>, keys: string[]): string {
  for (const key of keys) {
    const candidate = value[key];
    if (typeof candidate === "string" && candidate.trim()) return candidate.trim();
  }
  return "";
}

function rowFrom(label: string, value: unknown, key: string): CalendarProgressRow {
  const record = asRecord(value);
  if (!record) {
    return { key, label, status: typeof value === "string" ? value : "" };
  }
  const status = firstText(record, ["status", "state", "phase_status", "phase"])
    || (record.completed === true ? "completed" : record.running === true ? "running" : "");
  const rawProgress = record.progress ?? record.percent ?? record.progress_percent;
  const progress = typeof rawProgress === "number" && Number.isFinite(rawProgress)
    ? Math.max(0, Math.min(100, Math.round(rawProgress)))
    : undefined;
  const completed = record.completed_count ?? record.completed;
  const total = record.total_count ?? record.total;
  const countStatus = typeof completed === "number" && typeof total === "number" && total > 0
    ? `${completed}/${total}`
    : "";
  return { key, label, status: status || countStatus, progress };
}

function displayName(value: string): string {
  const normalized = value.trim().toLowerCase().replace(/[.\s-]+/g, "_");
  const phaseLabels: Record<string, string> = {
    scheduled: "SCHEDULED",
    dispatching: "QUEUED",
    queued: "QUEUED",
    script_generation: "SCRIPTING",
    script_generate: "SCRIPTING",
    script_plan: "SCRIPTING",
    media_search: "MEDIA SEARCH",
    asset_search: "MEDIA SEARCH",
    youtube_download: "MEDIA DOWNLOADING",
    media_download: "MEDIA DOWNLOADING",
    media_processing: "MEDIA PROCESSING",
    stock_search: "STOCK SEARCH",
    stock_download: "STOCK DOWNLOADING",
    voiceover_generation: "VOICEOVER",
    audio_generation: "VOICEOVER",
    overlay_prepare: "OVERLAY PREPARE",
    overlay_render: "RENDERING",
    clip_render: "RENDERING",
    video_render: "RENDERING",
    video_assembly: "ASSEMBLING",
    video_assemble: "ASSEMBLING",
    thumbnail_generation: "THUMBNAIL",
    content_ready: "CONTENT READY",
    publishing: "PUBLISHING",
    completed: "DONE",
    succeeded: "DONE",
    failed: "FAILED",
  };
  return phaseLabels[normalized]
    ?? value.replace(/[._-]+/g, " ").replace(/\b\w/g, (letter) => letter.toUpperCase());
}

export function getCalendarPhaseLabel(value?: string): string {
  return value ? displayName(value) : "";
}

function rowsFromEvents(value: unknown, source: string): CalendarProgressRow[] {
  if (!Array.isArray(value)) return [];
  return value.slice(-12).flatMap((entry, index) => {
    const record = asRecord(entry);
    if (!record) return [];
    const label = firstText(record, ["current_stage", "stage", "phase", "name", "event_type", "type", "message", "event"]);
    if (!label) return [];
    const row = rowFrom(displayName(label), record, `${source}-${index}`);
    if (!row.status) row.status = firstText(record, ["message", "event_type", "type"]);
    return [row];
  });
}

/** Read the compact stage/event shapes emitted by PipelineGen. */
export function getCalendarProgressRows(snapshot?: Record<string, unknown>): CalendarProgressRow[] {
  if (!snapshot) return [];

  const stages = asRecord(snapshot.stage_progress);
  if (stages && Object.keys(stages).length > 0) {
    return Object.entries(stages).slice(0, 12).map(([name, value]) =>
      rowFrom(displayName(name), value, `stage-${name}`),
    );
  }

  const timeline = rowsFromEvents(snapshot.timeline, "timeline");
  if (timeline.length > 0) return timeline;
  return rowsFromEvents(snapshot.events, "event");
}
