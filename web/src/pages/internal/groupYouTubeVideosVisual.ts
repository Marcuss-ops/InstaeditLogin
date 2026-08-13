import { YOUTUBE_CATEGORIES } from "../../features/youtube/api/categoriesApi";
import type { GroupYouTubeVideo, VideoAvailability } from "./groupYouTubeVideosTypes";

// Derive the availability projection from the raw wire fields. The API
// does not emit an availability object yet, so the hook stamps one per
// row; this fallback keeps consumers robust against unnormalized rows
// (fixtures, tests, cached responses).
export function videoAvailability(video: GroupYouTubeVideo): VideoAvailability {
  if (video.availability) return video.availability;
  if (video.phantom === true) {
    return {
      status: "deleted_or_missing",
      reason: "Il video non è più presente tra i video modificabili del canale.",
    };
  }
  if (video.youtube_sync_status === "drift") {
    return {
      status: "privacy_changed",
      reason: "La privacy rilevata su YouTube diverge da quella richiesta.",
    };
  }
  if (video.youtube_sync_status === "failed") {
    return {
      status: "unavailable",
      reason: "La pubblicazione non è stata confermata da YouTube.",
    };
  }
  return { status: "available" };
}

/**
 * Human label for a video's YouTube category: the row's own
 * `category_title` first, then the canonical snapshot by `category_id`
 * (e.g. "24" → "Intrattenimento"), then the raw id as last resort.
 * The backend does not emit category fields yet, so `undefined` is a
 * valid answer — consumers render it as "Categoria non impostata".
 */
export function categoryLabel(video: GroupYouTubeVideo): string | undefined {
  if (video.category_title) return video.category_title;
  if (video.category_id) {
    return (
      YOUTUBE_CATEGORIES.find((category) => category.id === video.category_id)?.label
      ?? video.category_id
    );
  }
  return undefined;
}

/** Distinct category options derived from a video list, sorted by label. */
export function categoryOptions(
  videos: GroupYouTubeVideo[],
): Array<{ key: string; label: string }> {
  const seen = new Map<string, string>();
  for (const video of videos) {
    const key = video.category_id ?? video.category_title;
    if (!key) continue;
    const label = categoryLabel(video) ?? key;
    if (!seen.has(key)) seen.set(key, label);
  }
  return Array.from(seen, ([key, label]) => ({ key, label })).sort((a, b) =>
    a.label.localeCompare(b.label, "it"),
  );
}

export function safeAssetUrl(value?: string): string | undefined {
  const candidate = value?.trim();
  if (!candidate) return undefined;
  try {
    const url = new URL(candidate, window.location.origin);
    if (!["http:", "https:", "blob:", "data:"].includes(url.protocol)) return undefined;
    return candidate;
  } catch {
    return undefined;
  }
}

export function publicationState(video: GroupYouTubeVideo): {
  label: string;
  tone: "success" | "warning" | "info" | "neutral";
} {
  const scheduledAt = video.publish_at ? new Date(video.publish_at) : null;
  const isFutureSchedule = scheduledAt != null && scheduledAt.getTime() > Date.now();
  if (video.youtube_sync_status === "pending") {
    return { label: "Pubblicazione inviata · verifica in corso", tone: "info" };
  }
  if (video.youtube_sync_status === "failed") {
    return { label: "Pubblicazione non confermata", tone: "warning" };
  }
  if (video.youtube_sync_status === "drift") {
    return { label: "Pubblicato · privacy da verificare", tone: "warning" };
  }
  if (isFutureSchedule && video.actual_privacy === "private") {
    return {
      label: "Programmazione completata · resta privato fino all'orario scelto",
      tone: "info",
    };
  }
  if (video.editor_status === "published" && video.youtube_sync_status !== "confirmed") {
    return { label: "Pubblicazione registrata · verifica YouTube", tone: "info" };
  }
  if (video.youtube_sync_status === "confirmed" && video.editor_status === "published") {
    return { label: "Pubblicato su YouTube", tone: "success" };
  }
  if (
    video.youtube_sync_status === "confirmed" &&
    (video.actual_privacy === "public" || video.privacy_status === "public")
  ) {
    return { label: "Pubblico su YouTube", tone: "success" };
  }
  if (
    video.youtube_sync_status !== "confirmed" &&
    (video.actual_privacy === "public" || video.privacy_status === "public")
  ) {
    return { label: "Visibilità rilevata · sincronizzazione non confermata", tone: "info" };
  }
  if (video.actual_privacy === "private" || video.privacy_status === "private") {
    return {
      label:
        video.desired_privacy === "private"
          ? "Privato su YouTube"
          : "Privato · possibile programmazione",
      tone: "neutral",
    };
  }
  if (["publishing", "processing", "queued"].includes(video.processing_status ?? "")) {
    return { label: "Elaborazione in corso", tone: "info" };
  }
  return { label: "Non ancora pubblicato", tone: "neutral" };
}

/**
 * Current YouTube visibility of the video (actual read-back first, then
 * the listing status). Independent from the publish lifecycle: a video
 * can be "Privato su YouTube" (lifecycle) AND still be visible only to
 * the channel (privacy). Unknown/missing values render neutrally.
 */
export function privacyBadge(video: GroupYouTubeVideo): {
  label: string;
  emoji: string;
  tone: keyof typeof toneClasses;
} {
  const privacy = String(video.actual_privacy ?? video.privacy_status ?? "").toLowerCase();
  switch (privacy) {
    case "public":
      return { label: "Pubblico", emoji: "🌍", tone: "success" };
    case "unlisted":
      return { label: "Non in elenco", emoji: "🔗", tone: "info" };
    case "private":
      return { label: "Privato", emoji: "🔒", tone: "neutral" };
    default:
      return { label: "Sconosciuta", emoji: "❔", tone: "neutral" };
  }
}

export function formatPublishAt(value?: string): string | null {
  if (!value) return null;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return null;
  return date.toLocaleString(undefined, {
    day: "2-digit",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export const toneClasses = {
  success: "border-emerald-500/25 bg-emerald-500/[0.08] text-emerald-300",
  warning: "border-amber-500/25 bg-amber-500/[0.08] text-amber-300",
  info: "border-blue-500/25 bg-blue-500/[0.08] text-blue-300",
  neutral: "border-white/[0.12] bg-white/[0.04] text-[#cdd2da]",
} as const;
