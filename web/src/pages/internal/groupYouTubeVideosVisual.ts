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
