import { useState } from "react";
import { Loader2, Plus } from "lucide-react";
import { GroupCoverCard } from "./GroupCoverCard";
import { useGroupCovers } from "./useGroupCovers";
import { useGroupYouTubeVideos } from "./useGroupYouTubeVideos";
import type { GroupCover } from "./groupCoversTypes";
import { GroupCoverPreviewModal } from "./GroupCoverPreviewModal";

/**
 * Covers grid for one group (the Copertine hub body). Replaces the
 * video grid: the user picks a group and sees every cover project
 * created in it — current + archived history — with its rendered
 * preview, status, channel/video and a "Modifica in InstaEditor" CTA
 * that opens the editor in a new tab (the SPA never navigates away).
 */
export function GroupCovers({ groupId, groupName }: { groupId: number; groupName?: string }) {
  const { state, refreshCovers, openCoverEditor, openingCoverId, renameCover, renamingCoverId, saveCoverDraft, savingCoverId } = useGroupCovers(groupId);
  // Video manifest for the one-click create: quickCreateCover opens
  // InstaEditor directly on the group's most recent private video and
  // saves the new cover under a random name (no picker dialog). The name
  // embeds the group name so the cover reads as belonging to this group.
  const { quickCreateCover, openingVideoID } = useGroupYouTubeVideos(groupId, true, groupName);
  const [previewCover, setPreviewCover] = useState<GroupCover | null>(null);
  const [previewTitle, setPreviewTitle] = useState("");
  const [previewDescription, setPreviewDescription] = useState("");

  // Both editor openings grab the destination tab SYNCHRONOUSLY inside
  // the click gesture: once the async session/draft/launch round-trips
  // complete, the already-open tab is navigated instead of issuing a
  // window.open that popup blockers would silently swallow. Noopener is
  // intentionally omitted here so the Window reference survives for the
  // later navigation (the editor is a first-party app on its own origin).
  const handleCreateCover = () => {
    const tab = window.open("about:blank", "_blank");
    void quickCreateCover(tab).then((opened) => {
      if (!opened) tab?.close();
      if (opened) refreshCovers();
    });
  };

  const handleOpenCoverEditor = (cover: GroupCover) => {
    const tab = window.open("about:blank", "_blank");
    void openCoverEditor(cover, tab);
  };

  const handleOpenPreview = (cover: GroupCover) => {
    setPreviewCover(cover);
    setPreviewTitle(cover.draft_title || cover.name || "");
    setPreviewDescription(cover.draft_description || "");
  };

  const coverAssetUrl = (cover: GroupCover): string | undefined => {
    if (state.kind !== "ready") return undefined;
    return (cover.preview_media_id ? state.previewUrls[cover.preview_media_id] : undefined)
      || (cover.thumbnail_media_id ? state.previewUrls[cover.thumbnail_media_id] : undefined);
  };

  return (
    <section className="mb-6" data-testid="group-covers">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
        <h3 className="text-[13px] font-bold text-white">
          Copertine del gruppo
          {state.kind === "ready" && (
            <span className="ml-2 rounded-md border border-white/[0.08] bg-white/[0.04] px-1.5 py-0.5 text-[10px] font-semibold text-[#9aa0aa]">
              {state.covers.length}
            </span>
          )}
        </h3>
      </div>

      {state.kind === "loading" && (
        <div className="flex items-center gap-2 rounded-xl border border-white/[0.08] bg-white/[0.03] px-4 py-5 text-[12px] text-[#9aa0aa]">
          <Loader2 size={15} className="animate-spin" aria-hidden="true" />
          Caricamento copertine…
        </div>
      )}

      {state.kind === "error" && (
        <div
          className="rounded-xl border border-amber-500/25 bg-amber-500/[0.06] px-4 py-4 text-[12px] text-amber-200"
          role="alert"
        >
          {state.message}
        </div>
      )}

      {state.kind === "ready" && (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          {/* Photoshop-style first tile: it is an action, not a persisted
              cover. It stays at the start of the grid so the next cover is
              always created from the same predictable + button. */}
          <button
            type="button"
            onClick={handleCreateCover}
            disabled={openingVideoID != null}
            aria-label="Crea copertina"
            data-testid="group-covers-create-card"
            className="group flex aspect-video min-h-[220px] flex-col items-center justify-center rounded-2xl border border-dashed border-violet-400/35 bg-violet-500/[0.06] p-6 text-center text-violet-100 shadow-[0_12px_32px_rgba(124,58,237,0.08)] transition-all duration-200 hover:-translate-y-0.5 hover:border-violet-300/70 hover:bg-violet-500/[0.13] disabled:cursor-wait disabled:opacity-60"
          >
            <span className="flex h-14 w-14 items-center justify-center rounded-full border border-violet-300/35 bg-violet-400/[0.13] shadow-lg transition-transform duration-200 group-hover:scale-110">
              {openingVideoID != null ? <Loader2 size={25} className="animate-spin" aria-hidden="true" /> : <Plus size={28} strokeWidth={2.2} aria-hidden="true" />}
            </span>
            <span className="mt-4 text-[14px] font-bold">Crea copertina</span>
            <span className="mt-1 text-[11px] text-violet-200/60">Clicca per creare una nuova copertina</span>
          </button>
          {state.covers.map((cover) => (
            <GroupCoverCard
              key={cover.project_id}
              cover={cover}
              previewUrl={coverAssetUrl(cover)}
              opening={openingCoverId === cover.project_id}
              renaming={renamingCoverId === cover.project_id}
              onOpenEditor={handleOpenCoverEditor}
              onOpenPreview={handleOpenPreview}
              onRenameCover={renameCover}
            />
          ))}
        </div>
      )}

      {previewCover && state.kind === "ready" ? (
        <GroupCoverPreviewModal
          cover={previewCover}
          previewUrl={coverAssetUrl(previewCover)}
          saving={savingCoverId === previewCover.project_id}
          opening={openingCoverId === previewCover.project_id}
          title={previewTitle}
          description={previewDescription}
          onTitleChange={setPreviewTitle}
          onDescriptionChange={setPreviewDescription}
          onClose={() => setPreviewCover(null)}
          onSave={() => void saveCoverDraft(previewCover, previewTitle, previewDescription)}
          onOpenEditor={() => {
            setPreviewCover(null);
            handleOpenCoverEditor(previewCover);
          }}
        />
      ) : null}

    </section>
  );
}
