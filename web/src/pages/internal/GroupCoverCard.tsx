import { memo, useRef, useState } from "react";
import { Calendar, Check, Edit3, ExternalLink, Image as ImageIcon, Loader2, X } from "lucide-react";
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
  renaming,
  onOpenEditor,
  onOpenPreview,
  onRenameCover,
}: {
  cover: GroupCover;
  previewUrl?: string;
  opening: boolean;
  /** True while a rename PUT for this cover is in flight. */
  renaming?: boolean;
  onOpenEditor: (cover: GroupCover) => void;
  onOpenPreview: (cover: GroupCover) => void;
  onRenameCover: (cover: GroupCover, newTitle: string) => Promise<boolean> | boolean;
}) {
  const status = projectStatusLabel(cover.project_status);
  const editLabel = editStatusLabel(cover.edit_status);
  const preview = previewUrl || safeAssetUrl(cover.source_thumbnail_url);

  // Inline title editing: click the title to turn it into an input;
  // Enter/✓ commits (PUT partial { title } → draft_title), Escape/✕
  // cancels. The saved title lands in the DB and the hub card renders
  // it — same field InstaEditor's rename pill writes.
  const [editing, setEditing] = useState(false);
  const [draftValue, setDraftValue] = useState("");
  // Guard against double-commit: blur can fire both on the ✓ mousedown
  // (input loses focus) and on a post-cancel/unmount blur. Only the
  // FIRST commit after a fresh edit is allowed; a stale draft value is
  // dropped on cancel so an Escape + stray blur can never PUT.
  const committingRef = useRef(false);
  const cancelledRef = useRef(false);
  const displayTitle = cover.draft_title || cover.name || "Copertina senza titolo";

  const startEditing = () => {
    setDraftValue(displayTitle);
    committingRef.current = false;
    cancelledRef.current = false;
    setEditing(true);
  };

  const commitRename = async () => {
    if (cancelledRef.current) return;
    if (committingRef.current) return;
    committingRef.current = true;
    const value = draftValue.trim();
    setEditing(false);
    if (!value || value === displayTitle.trim()) return;
    await onRenameCover(cover, value);
  };

  const cancelEditing = () => {
    cancelledRef.current = true;
    setEditing(false);
  };

  return (
    <article
      className="group overflow-hidden rounded-2xl border border-white/[0.08] bg-white/[0.025] transition-colors hover:border-violet-400/30 hover:bg-white/[0.05]"
      data-testid="group-cover-card"
    >
      <div
        className="relative aspect-video w-full cursor-zoom-in overflow-hidden bg-black/40"
        onClick={() => onOpenPreview(cover)}
        onDoubleClick={(event) => { event.preventDefault(); onOpenEditor(cover); }}
        title="Clicca per ingrandire · doppio click per modificare in InstaEditor"
      >
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
        {editing ? (
          <div className="flex items-center gap-1.5">
            <input
              autoFocus
              value={draftValue}
              onChange={(event) => setDraftValue(event.target.value)}
              onBlur={commitRename}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault();
                  event.currentTarget.blur();
                } else if (event.key === "Escape") {
                  cancelEditing();
                }
              }}
              maxLength={100}
              aria-label="Titolo copertina"
              data-testid="cover-title-input"
              className="w-full min-w-0 flex-1 truncate rounded-md border border-violet-500/40 bg-black/40 px-2 py-1 text-[14px] font-semibold text-white outline-none transition-colors focus:border-violet-400/70"
            />
            {renaming ? (
              <Loader2 size={14} className="shrink-0 animate-spin text-violet-300" aria-hidden="true" />
            ) : (
              <>
                <button
                  type="button"
                  onClick={commitRename}
                  aria-label="Conferma titolo"
                  title="Salva titolo"
                  className="shrink-0 rounded-md border border-emerald-500/25 bg-emerald-500/[0.10] p-1 text-emerald-300 transition-colors hover:bg-emerald-500/[0.20]"
                >
                  <Check size={13} aria-hidden="true" />
                </button>
                <button
                  type="button"
                  onClick={cancelEditing}
                  aria-label="Annulla modifica titolo"
                  title="Annulla"
                  className="shrink-0 rounded-md border border-white/[0.10] bg-white/[0.04] p-1 text-[#9aa0aa] transition-colors hover:bg-white/[0.08] hover:text-white"
                >
                  <X size={13} aria-hidden="true" />
                </button>
              </>
            )}
          </div>
        ) : (
          <button
            type="button"
            onClick={startEditing}
            disabled={renaming}
            title="Clicca per rinominare la copertina"
            data-testid="cover-title-edit"
            className="group/title flex w-full items-center gap-1.5 truncate text-left text-[14px] font-semibold text-white transition-colors hover:text-violet-200 disabled:opacity-70"
          >
            <span className="truncate">{displayTitle}</span>
            {renaming ? (
              <Loader2 size={12} className="shrink-0 animate-spin text-violet-300" aria-hidden="true" />
            ) : (
              <Edit3
                size={11}
                className="shrink-0 text-white/25 opacity-0 transition-opacity group-hover/title:opacity-100"
                aria-hidden="true"
              />
            )}
          </button>
        )}
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
