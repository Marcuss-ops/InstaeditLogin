import {
  ArrowRight,
  ExternalLink,
  Image as ImageIcon,
  Loader2,
  Send,
} from "lucide-react";
import type { EditorSession } from "../../types/uploads";
import { getRomeTZ, isScheduleInPast, localToUTC } from "./youtubeStudioTime";
import { FormField } from "./YouTubeStudioFormElements";
import { cn } from "../../lib/utils";

export function SessionRow({
  session,
  isActive,
  isPublishing,
  isExpanded,
  thumbnailMediaId,
  scheduleAt,
  onToggle,
  onThumbnailChange,
  onScheduleAtChange,
  onAttach,
  onPublishNow,
  onSchedule,
}: {
  session: EditorSession;
  isActive: boolean;
  isPublishing: boolean;
  isExpanded: boolean;
  thumbnailMediaId: string;
  scheduleAt: string;
  onToggle: () => void;
  onThumbnailChange: (v: string) => void;
  onScheduleAtChange: (v: string) => void;
  onAttach: () => void;
  onPublishNow: () => void;
  onSchedule: () => void;
}) {
  const hasThumbnail = !!session.thumbnail_media_id;
  const isPublished = session.status === "published";
  const canAttach = !isActive && !isPublished && thumbnailMediaId.trim().length > 0;
  const scheduleInPast = isScheduleInPast(scheduleAt);
  const hasValidSchedule = scheduleAt.length > 0 && !scheduleInPast;
  const canSchedule = !isPublishing && hasValidSchedule;
  const tz = getRomeTZ();
  const isScheduling = hasValidSchedule;

  // Format the scheduled date for the button when scheduling.
  const scheduleDate = hasValidSchedule ? new Date(scheduleAt) : null;
  const scheduleLabel = scheduleDate
    ? scheduleDate.toLocaleString("it-IT", {
        dateStyle: "short",
        timeStyle: "short",
      })
    : "";

  return (
    <article
      className="rounded-xl border border-white/[0.08] bg-white/[0.02] p-4 space-y-3"
      data-testid="yt-studio-session-row"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-[13px] font-mono text-white truncate">
            {session.youtube_video_id || "(unknown video)"}
          </p>
          <p className="text-[11px] text-[#9aa0aa] mt-0.5">
            status:{" "}
            <span className="font-semibold text-white">{session.status}</span>
            {" · "}
            desired privacy:{" "}
            <span className="font-semibold text-white">
              {session.desired_privacy}
            </span>
            {session.publish_at && (
              <>
                {" · "}
                publish_at:{" "}
                <span className="font-mono">{session.publish_at}</span>
              </>
            )}
            {session.actual_privacy && (
              <>
                {" · "}
                actual privacy:{" "}
                <span className="font-semibold text-emerald-200">
                  {session.actual_privacy}
                </span>
              </>
            )}
          </p>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <a
            href={session.editor_url}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1 px-3 py-1.5 rounded-lg bg-white/[0.06] border border-white/[0.10] text-[12px] font-semibold text-white hover:bg-white/[0.10] transition-colors no-underline"
          >
            <ExternalLink size={12} aria-hidden="true" /> Velox
          </a>
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <span
          className={cn(
            "inline-flex items-center gap-1 px-2 py-1 rounded-md text-[11px] font-semibold border",
            hasThumbnail
              ? "border-emerald-500/30 bg-emerald-500/[0.10] text-emerald-300"
              : "border-amber-500/30 bg-amber-500/[0.10] text-amber-300",
          )}
        >
          <ImageIcon size={11} aria-hidden="true" />
          {hasThumbnail
            ? `thumbnail · ${session.thumbnail_media_id}`
            : "thumbnail: not set"}
        </span>
        {session.youtube_sync_status && (
          <span
            className={cn(
              "inline-flex items-center gap-1 px-2 py-1 rounded-md text-[11px] font-semibold border",
              session.youtube_sync_status === "confirmed"
                ? "border-emerald-500/30 bg-emerald-500/[0.10] text-emerald-300"
                : session.youtube_sync_status === "drift"
                  ? "border-amber-500/30 bg-amber-500/[0.10] text-amber-300"
                  : "border-blue-500/30 bg-blue-500/[0.10] text-blue-200",
            )}
          >
            YouTube: {session.youtube_sync_status}
          </span>
        )}
        {!isPublished && (
          <button
            type="button"
            onClick={onToggle}
            className="inline-flex items-center gap-1 px-3 py-1.5 rounded-lg bg-white/[0.06] border border-white/[0.10] text-[12px] font-semibold text-white hover:bg-white/[0.10] transition-colors"
            data-testid="yt-studio-attach-toggle"
          >
            {hasThumbnail ? "Replace thumbnail" : "Allega copertina"}
          </button>
        )}

        {/* Smart publish/schedule button */}
        {!isPublished && (isScheduling ? (
          <button
            type="button"
            onClick={onSchedule}
            disabled={!canSchedule}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-blue-500/[0.18] border border-blue-500/30 text-[12px] font-semibold text-blue-100 hover:bg-blue-500/[0.26] transition-colors disabled:opacity-50"
            data-testid="yt-studio-publish-now"
          >
            {isPublishing ? (
              <Loader2 size={12} className="animate-spin" aria-hidden="true" />
            ) : (
              <ArrowRight size={12} aria-hidden="true" />
            )}
            Programma per {scheduleLabel}
          </button>
        ) : (
          <button
            type="button"
            onClick={onPublishNow}
            disabled={isPublishing}
            className="inline-flex items-center gap-1 px-3 py-1.5 rounded-lg bg-emerald-500/[0.12] border border-emerald-500/30 text-[12px] font-semibold text-emerald-200 hover:bg-emerald-500/[0.18] transition-colors disabled:opacity-50"
            data-testid="yt-studio-publish-now"
          >
            {isPublishing ? (
              <Loader2 size={12} className="animate-spin" aria-hidden="true" />
            ) : (
              <Send size={12} aria-hidden="true" />
            )}
            Pubblica ora
          </button>
        ))}

        {!isPublished && (
          <button
            type="button"
            onClick={onToggle}
            className="inline-flex items-center gap-1 px-3 py-1.5 rounded-lg bg-white/[0.06] border border-white/[0.10] text-[12px] font-semibold text-white hover:bg-white/[0.10] transition-colors"
            data-testid="yt-studio-schedule-toggle"
          >
            {isScheduling ? "Modifica programmazione" : "Programma pubblicazione"}
          </button>
        )}
      </div>

      {isPublished && (
        <div className="flex flex-wrap items-center gap-3 text-[12px] text-emerald-200/80">
          <span>Pubblicato: verifica completata.</span>
          <a
            href={`https://www.youtube.com/watch?v=${encodeURIComponent(session.youtube_video_id)}`}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1 text-white hover:text-emerald-200 underline underline-offset-2"
            data-testid="yt-studio-published-link"
          >
            Apri video su YouTube <ExternalLink size={12} aria-hidden="true" />
          </a>
        </div>
      )}

      {isExpanded && !isPublished && (
        <div className="space-y-3 pt-2 border-t border-white/[0.06]">
          <FormField
            id={`yt-studio-thumb-${session.id}`}
            label={hasThumbnail ? "Sostituisci thumbnail_media_id" : "thumbnail_media_id"}
            helpText="Paste the media asset ID returned by /api/v1/media presign+complete."
          >
            <input
              id={`yt-studio-thumb-${session.id}`}
              type="text"
              placeholder="media-asset-id-123"
              value={thumbnailMediaId}
              onChange={(e) => onThumbnailChange(e.target.value)}
              className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[13px] text-white placeholder:text-white/20 focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all"
              spellCheck={false}
              autoComplete="off"
              data-testid="yt-studio-thumbnail-input"
            />
          </FormField>
          <div className="flex items-center justify-end">
            <button
              type="button"
              onClick={onAttach}
              disabled={!canAttach}
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-lg bg-white text-black text-[12px] font-semibold hover:bg-white/90 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              data-testid="yt-studio-attach-submit"
            >
              {isActive ? (
                <Loader2 size={12} className="animate-spin" aria-hidden="true" />
              ) : (
                <ImageIcon size={12} aria-hidden="true" />
              )}
              {hasThumbnail ? "Sostituisci copertina" : "Allega copertina"}
            </button>
          </div>

          <div className="border-t border-white/[0.06] pt-3 space-y-3">
            <p className="text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider">
              Schedule publication
            </p>
            {/* Timezone indicator */}
            <div className="flex items-center gap-2 px-3 py-2 rounded-lg bg-blue-500/[0.06] border border-blue-500/15">
              <span className="text-[11px] font-semibold text-blue-200/80">
                🕐 Fuso orario: {tz.label}
              </span>
            </div>
            <FormField
              id={`yt-studio-publish-at-${session.id}`}
              label="Data e ora di pubblicazione"
              helpText={
                hasValidSchedule
                  ? `UTC: ${localToUTC(scheduleAt)} — Il video resterà privato fino all'orario indicato, poi diventerà pubblico automaticamente.`
                  : "Il video resterà privato fino all'orario indicato, poi diventerà pubblico automaticamente."
              }
              error={
                scheduleInPast
                  ? "La data di pubblicazione deve essere nel futuro."
                  : null
              }
            >
              <input
                id={`yt-studio-publish-at-${session.id}`}
                type="datetime-local"
                value={scheduleAt}
                onChange={(e) => onScheduleAtChange(e.target.value)}
                className={cn(
                  "w-full px-3 py-2 bg-white/[0.04] border rounded-xl text-[13px] text-white placeholder:text-white/20 focus:outline-none focus:ring-1 focus:ring-white/10 transition-all",
                  scheduleInPast
                    ? "border-red-500/40 focus:border-red-500/60"
                    : "border-white/[0.08] focus:border-white/[0.20]",
                )}
                data-testid="yt-studio-schedule-input"
              />
            </FormField>
            {/* DST note */}
            <p className="text-[10px] text-[#9aa0aa]/60">
              L'ora legale viene gestita automaticamente. Inserisci l'orario
              come lo vedi sull'orologio in Italia — il sistema converte in UTC.
            </p>
            <div className="flex items-center justify-end">
              <button
                type="button"
                onClick={onSchedule}
                disabled={!canSchedule}
                className="inline-flex items-center gap-1.5 px-4 py-2 rounded-lg bg-blue-500/[0.18] border border-blue-500/30 text-[12px] font-semibold text-blue-100 hover:bg-blue-500/[0.26] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                data-testid="yt-studio-schedule-submit"
              >
                {isPublishing ? (
                  <Loader2 size={12} className="animate-spin" aria-hidden="true" />
                ) : (
                  <ArrowRight size={12} aria-hidden="true" />
                )}
                Programma
              </button>
            </div>
          </div>
        </div>
      )}
    </article>
  );
}
