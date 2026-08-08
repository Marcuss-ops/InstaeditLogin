import { memo } from "react";
import { Calendar, Edit3, ExternalLink, Image as ImageIcon, Loader2 } from "lucide-react";
import { cn } from "../../lib/utils";
import { LanguageFlag } from "../../components/brand/LanguageFlag";
import { safeAssetUrl } from "./groupYouTubeVideosVisual";
import type { GroupCover } from "./groupCoversTypes";

function projectStatusLabel(status: string): { label: string; tone: string } {
  switch (status) {
    case "ready":
      return { label: "Pronta", tone: "border-emerald-500/25 bg-emerald-500/[0.08] text-emerald-300" };
    case "archived":
      return { label: "Archiviata", tone: "border-white/[0.10] bg-white/[0.04] text-[#9aa0aa]" };
    case "draft":
    default:
      return { label: "Bozza", tone: "border-amber-500/25 bg-amber-500/[0.08] text-amber-300" };
  }
}

function editStatusLabel(status: string): string {
  switch (status) {
    case "published":
      return "Pubblicata";
    case "publishing":
      return "Pubblicazione in corso";
    case "failed":
      return "Pubblicazione fallita";
    default:
      return "In modifica";
  }
}

function formatCoverDate(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleDateString(undefined, { day: "2-digit", month: "short", year: "numeric" });
}

export const GroupCoverCard = memo(function GroupCoverCard({
  cover,
  previewUrl,
  opening,
  onOpenEditor,
}: {
  cover: GroupCover;
  previewUrl?: string;
  opening: boolean;
  onOpenEditor: (cover: GroupCover) => void;
}) {
  const status = projectStatusLabel(cover.project_status);
  const editLabel = editStatusLabel(cover.edit_status);
  const preview = previewUrl || safeAssetUrl(cover.source_thumbnail_url);

  return (
    <article
      className="group overflow-hidden rounded-2xl border border-white/[0.08] bg-white/[0.025] transition-colors hover:border-violet-400/30 hover:bg-white/[0.05]"
      data-testid="group-cover-card"
    >
      <div className="relative aspect-video w-full overflow-hidden bg-black/40">
        {preview ? (
          <img
            src={preview}
            alt=""
            loading="lazy"
            decoding="async"
            className="h-full w-full object-cover transition-transform duration-300 group-hover:scale-[1.02]"
          />
        ) : (
          <div className="flex h-full items-center justify-center">
            <ImageIcon size={26} className="text-white/20" aria-hidden="true" />
          </div>
        )}
        <span
          title={`Stato progetto: ${status.label}`}
          className={cn(
            "absolute left-2 top-2 inline-flex items-center rounded-md border px-1.5 py-0.5 text-[10px] font-bold backdrop-blur",
            status.tone,
          )}
        >
          {status.label}
        </span>
        {cover.language && (
          <span
            title={`Lingua: ${cover.language.toUpperCase()}`}
            className="absolute right-2 top-2 inline-flex h-6 items-center rounded-md border border-violet-500/20 bg-black/50 px-1.5 backdrop-blur"
          >
            <LanguageFlag code={cover.language} className="h-4 w-6" />
          </span>
        )}
      </div>

      <div className="p-3.5">
        <p
          className="truncate text-[14px] font-semibold text-white"
          title={cover.draft_title || cover.name}
        >
          {cover.draft_title || cover.name || "Copertina senza titolo"}
        </p>
        <p className="mt-1 truncate font-mono text-[11px] text-[#9aa0aa]" title={`${cover.channel_name || `Account #${cover.platform_account_id}`} · ${cover.youtube_video_id}`}>
          {cover.channel_name || `Account #${cover.platform_account_id}`}
          <span className="px-1 text-white/30">·</span>
          {cover.youtube_video_id}
        </p>

        <div className="mt-2.5 flex flex-wrap items-center gap-1.5">
          <span
            title="Stato della sessione editor"
            className={cn(
              "inline-flex items-center rounded-md border px-2 py-0.5 text-[10px] font-semibold",
              cover.edit_status === "published"
                ? "border-emerald-500/25 bg-emerald-500/[0.08] text-emerald-300"
                : cover.edit_status === "failed"
                  ? "border-red-500/25 bg-red-500/[0.08] text-red-300"
                  : "border-blue-500/25 bg-blue-500/[0.08] text-blue-300",
            )}
          >
            {editLabel}
          </span>
          <span className="inline-flex items-center gap-1 rounded-md border border-white/[0.08] bg-white/[0.03] px-2 py-0.5 text-[10px] text-[#9aa0aa]">
            <Calendar size={10} aria-hidden="true" />
            {formatCoverDate(cover.updated_at) || "—"}
          </span>
        </div>

        <div className="mt-3 flex items-center justify-between gap-2">
          <a
            href={`https://www.youtube.com/watch?v=${encodeURIComponent(cover.youtube_video_id)}`}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1 rounded-lg border border-white/[0.10] bg-white/[0.04] px-2.5 py-1.5 text-[11px] font-semibold text-[#cdd2da] transition-colors hover:bg-white/[0.08] hover:text-white"
            title="Apri il video su YouTube"
          >
            <ExternalLink size={12} aria-hidden="true" />
            Video
          </a>
          <button
            type="button"
            onClick={() => onOpenEditor(cover)}
            disabled={opening || !cover.velox_project_id || !cover.editor_url}
            className="inline-flex items-center gap-1.5 rounded-lg border border-violet-500/25 bg-violet-500/[0.10] px-3 py-1.5 text-[11px] font-bold text-violet-200 transition-colors hover:bg-violet-500/[0.20] hover:text-violet-100 disabled:cursor-not-allowed disabled:opacity-60"
            title={
              !cover.velox_project_id
                ? "Copertina non associata a un progetto editor"
                : !cover.editor_url
                  ? "Editor non configurato su questo server"
                  : "Apri la copertina in InstaEditor"
            }
          >
            {opening ? <Loader2 size={12} className="animate-spin" aria-hidden="true" /> : <Edit3 size={12} aria-hidden="true" />}
            {opening ? "Apertura…" : "Modifica in InstaEditor"}
          </button>
        </div>
      </div>
    </article>
  );
});
