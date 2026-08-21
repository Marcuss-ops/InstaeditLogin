import { memo, useRef, useState } from "react";
import { Calendar, Check, Edit3, Image as ImageIcon, Loader2, Maximize2, X } from "lucide-react";
import type { GroupCover } from "./groupCoversTypes";

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
  const preview = previewUrl;

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
      className="group flex h-full flex-col overflow-hidden rounded-2xl border border-white/[0.09] bg-[#0d0f15]/90 shadow-[0_14px_34px_rgba(0,0,0,0.12)] transition-all duration-200 hover:-translate-y-1 hover:border-violet-300/25 hover:shadow-[0_20px_44px_rgba(0,0,0,0.2)]"
      data-testid="group-cover-card"
    >
      <div
        className="relative aspect-video w-full cursor-pointer overflow-hidden bg-[#07080c]"
        onClick={() => onOpenEditor(cover)}
        title="Clicca per modificare in InstaEditor"
      >
        {preview ? (
          <img
            src={preview}
            alt=""
            loading="lazy"
            decoding="async"
            className="h-full w-full object-cover transition-transform duration-500 group-hover:scale-[1.04]"
          />
        ) : (
          <div className="flex h-full items-center justify-center bg-[radial-gradient(circle_at_50%_35%,rgba(139,92,246,0.16),transparent_45%)]">
            <div className="text-center"><span className="mx-auto flex h-11 w-11 items-center justify-center rounded-2xl border border-white/[0.08] bg-white/[0.04]"><ImageIcon size={20} className="text-white/30" aria-hidden="true" /></span><span className="mt-3 block px-3 text-[11px] text-[#7f8591]">Copertina non ancora esportata</span></div>
          </div>
        )}
        <div aria-hidden="true" className="pointer-events-none absolute inset-x-0 bottom-0 h-20 bg-gradient-to-t from-black/65 to-transparent opacity-70" />
        {opening && <span className="absolute inset-0 flex items-center justify-center bg-black/45 text-[11px] font-bold text-white">Apertura…</span>}
        <button
          type="button"
          onClick={(event) => { event.stopPropagation(); onOpenPreview(cover); }}
          aria-label="Ingrandisci anteprima copertina"
          title="Ingrandisci anteprima"
          className="absolute bottom-3 right-3 z-10 inline-flex h-8 items-center gap-1.5 rounded-lg border border-white/[0.16] bg-black/60 px-2.5 text-[10px] font-bold text-white backdrop-blur-md transition-colors hover:bg-violet-500/80"
        >
          <Maximize2 size={13} aria-hidden="true" />
          Zoom
        </button>
      </div>

      <div className="flex flex-1 flex-col p-4">
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
        <p className="mt-1 truncate text-[11px] text-[#8a919d]" title={`${cover.channel_name || `Account #${cover.platform_account_id}`} · ${cover.youtube_video_id}`}>
          {cover.channel_name || `Account #${cover.platform_account_id}`}
          <span className="px-1 text-white/30">·</span>
          <span className="font-mono text-[10px] text-white/35">{cover.youtube_video_id}</span>
        </p>

        <div className="mt-3 text-[10px] text-white/40"><Calendar size={10} className="mr-1 inline" aria-hidden="true" />Aggiornata {formatCoverDate(cover.updated_at) || "—"}</div>

      </div>
    </article>
  );
});
