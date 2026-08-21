import { memo, useRef, useState } from "react";
import { Calendar, Check, Edit3, Image as ImageIcon, Loader2, X } from "lucide-react";
import { cn } from "../../lib/utils";
import { LanguageFlag } from "../../components/brand/LanguageFlag";
import { categoryLabelForId, privacyBadgeForStatus, toneClasses } from "./groupYouTubeVideosVisual";
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

function lifecycleStatusLabel(status?: string): { label: string; tone: string } {
  switch (status) {
    case "published": return { label: "Pubblicata", tone: "border-emerald-500/25 bg-emerald-500/[0.08] text-emerald-300" };
    case "applied": return { label: "Applicata", tone: "border-sky-500/25 bg-sky-500/[0.08] text-sky-300" };
    case "ready": return { label: "Pronta", tone: "border-violet-500/25 bg-violet-500/[0.08] text-violet-300" };
    case "error": return { label: "Errore", tone: "border-red-500/25 bg-red-500/[0.08] text-red-300" };
    default: return { label: "Bozza", tone: "border-amber-500/25 bg-amber-500/[0.08] text-amber-300" };
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
  onRenameCover,
}: {
  cover: GroupCover;
  previewUrl?: string;
  opening: boolean;
  /** True while a rename PUT for this cover is in flight. */
  renaming?: boolean;
  onOpenEditor: (cover: GroupCover) => void;
  onRenameCover: (cover: GroupCover, newTitle: string) => Promise<boolean> | boolean;
}) {
  const status = projectStatusLabel(cover.project_status);
  const editLabel = editStatusLabel(cover.edit_status);
  const lifecycle = lifecycleStatusLabel(cover.lifecycle_status);
  const privacy = privacyBadgeForStatus(cover.privacy_status);
  const category = categoryLabelForId(cover.category_id);
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
        <span
          title={`Stato progetto: ${status.label}`}
          className={cn(
            "absolute left-3 top-3 inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-[10px] font-bold backdrop-blur-md",
            status.tone,
          )}
        >
          <span aria-hidden="true" className="h-1.5 w-1.5 rounded-full bg-current" />
          {status.label}
        </span>
        {cover.language && (
          <span
            title={`Lingua: ${cover.language.toUpperCase()}`}
            className="absolute right-3 top-3 inline-flex h-7 items-center rounded-lg border border-white/[0.12] bg-black/45 px-1.5 backdrop-blur-md"
          >
            <LanguageFlag code={cover.language} className="h-4 w-6" />
          </span>
        )}
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

        <div className="mt-3 flex flex-wrap items-center gap-1.5">
          <span title="Stato lifecycle della copertina" className={cn("inline-flex items-center rounded-full border px-2.5 py-1 text-[10px] font-semibold", lifecycle.tone)}>{lifecycle.label}</span>
          <span
            title={`Visibilità video: ${privacy.label}`}
            className={cn(
              "inline-flex items-center gap-1 rounded-full border px-2.5 py-1 text-[10px] font-semibold",
              toneClasses[privacy.tone],
            )}
          >
            <span aria-hidden="true">{privacy.emoji}</span>
            {privacy.label}
          </span>
          {category && (
            <span
              title={`Categoria: ${category}`}
              className="inline-flex items-center rounded-full border border-white/[0.08] bg-white/[0.03] px-2.5 py-1 text-[10px] text-[#9aa0aa]"
            >
              {category}
            </span>
          )}
          <span
            title="Stato della sessione editor"
            className={cn(
              "inline-flex items-center rounded-full border px-2.5 py-1 text-[10px] font-semibold",
              cover.edit_status === "published"
                ? "border-emerald-500/25 bg-emerald-500/[0.08] text-emerald-300"
                : cover.edit_status === "failed"
                  ? "border-red-500/25 bg-red-500/[0.08] text-red-300"
                  : "border-blue-500/25 bg-blue-500/[0.08] text-blue-300",
            )}
          >
            {editLabel}
          </span>
          <span className="inline-flex items-center gap-1 rounded-full border border-white/[0.08] bg-white/[0.03] px-2.5 py-1 text-[10px] text-[#9aa0aa]">
            <Calendar size={10} aria-hidden="true" />
            {formatCoverDate(cover.updated_at) || "—"}
          </span>
        </div>

      </div>
    </article>
  );
});
