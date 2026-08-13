import { useState } from "react";
import { ArrowUpRight, Image as ImageIcon, Loader2, Plus, Sparkles } from "lucide-react";
import { GroupCoverCard } from "./GroupCoverCard";
import { useGroupCovers } from "./useGroupCovers";
import { useGroupYouTubeVideos } from "./useGroupYouTubeVideos";
import { GroupVideoManager } from "./GroupVideoManager";
import type { GroupCover } from "./groupCoversTypes";
import { GroupCoverPreviewModal } from "./GroupCoverPreviewModal";

/**
 * Video/Cover Manager for one group (the Copertine hub body). The
 * covers zone lists every cover project created in the group — current
 * + archived history — with its rendered preview, status, channel/video
 * and a "Modifica in InstaEditor" CTA that opens the editor in a new
 * tab (the SPA never navigates away). Below it, GroupVideoManager
 * offers the full video management UI (search, visibility tabs with
 * counts, category filter, VideoGrid with "Modifica copertina" and
 * "Dettagli" actions). Both zones share ONE canonical video-list hook
 * instance so the group list is fetched once.
 */
export function GroupCovers({ groupId, groupName }: { groupId: number; groupName?: string }) {
  const { state, refreshCovers, openCoverEditor, openingCoverId, renameCover, renamingCoverId, saveCoverDraft, savingCoverId } = useGroupCovers(groupId);
  // ONE canonical video-list hook for the whole page: the one-click
  // quick-create AND the Video/Cover manager below share the same list
  // state instead of firing two parallel fetches.
  const videosController = useGroupYouTubeVideos(groupId, true, groupName);
  const { quickCreateCover, openingVideoID } = videosController;
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
    <>
    <section className="mb-6" data-testid="group-covers">
      <div className="mb-5 flex flex-col gap-4 rounded-2xl border border-white/[0.09] bg-[#0d0f15]/90 p-5 shadow-[0_18px_50px_rgba(0,0,0,0.16)] sm:flex-row sm:items-center sm:justify-between sm:p-6">
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-[10px] font-bold uppercase tracking-[0.18em] text-violet-300/75">
            <Sparkles size={13} aria-hidden="true" />
            Spazio selezionato
          </div>
          <h2 className="mt-2 truncate text-[22px] font-bold tracking-[-0.025em] text-white sm:text-[25px]">{groupName || "Le tue copertine"}</h2>
          <p className="mt-1 text-[12px] text-[#858c99]">Progetti attivi e storico del gruppo, sempre nello stesso posto.</p>
        </div>
        <div className="flex shrink-0 items-center gap-2 rounded-xl border border-white/[0.08] bg-white/[0.035] px-3 py-2">
          <ImageIcon size={15} className="text-violet-300" aria-hidden="true" />
          <span className="text-[12px] font-semibold text-[#cbd0d8]">{state.kind === "ready" ? state.covers.length : "—"}</span>
          <span className="text-[11px] text-[#777e8b]">{state.kind === "ready" && state.covers.length === 1 ? "copertina" : "copertine"}</span>
        </div>
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
        <div className="grid grid-cols-1 gap-5 md:grid-cols-2 xl:grid-cols-3">
          <button
            type="button"
            onClick={handleCreateCover}
            disabled={openingVideoID != null}
            aria-label="Crea copertina"
            data-testid="group-covers-create-card"
            className="group relative flex min-h-[318px] flex-col overflow-hidden rounded-2xl border border-violet-300/20 bg-gradient-to-br from-violet-500/[0.18] via-violet-500/[0.06] to-sky-400/[0.06] p-6 text-left text-violet-100 shadow-[0_18px_45px_rgba(124,58,237,0.12)] transition-all duration-200 hover:-translate-y-1 hover:border-violet-300/45 hover:shadow-[0_22px_55px_rgba(124,58,237,0.2)] disabled:cursor-wait disabled:opacity-60"
          >
            <span aria-hidden="true" className="pointer-events-none absolute -right-12 -top-16 h-44 w-44 rounded-full bg-violet-300/15 blur-3xl transition-transform duration-500 group-hover:scale-125" />
            <span className="relative flex h-12 w-12 items-center justify-center rounded-2xl border border-violet-200/25 bg-violet-200/10 text-violet-100 shadow-[0_10px_25px_rgba(124,58,237,0.2)] transition-transform duration-200 group-hover:scale-105">
              {openingVideoID != null ? <Loader2 size={23} className="animate-spin" aria-hidden="true" /> : <Plus size={25} strokeWidth={2.2} aria-hidden="true" />}
            </span>
            <span className="relative mt-auto">
              <span className="block text-[19px] font-bold tracking-[-0.02em]">Crea copertina</span>
              <span className="mt-2 block max-w-[220px] text-[12px] leading-5 text-violet-100/65">Clicca per creare una nuova copertina partendo da un video del gruppo.</span>
            </span>
            <span className="relative mt-5 inline-flex items-center gap-1.5 text-[11px] font-bold text-violet-200/90">
              Inizia ora <ArrowUpRight size={14} aria-hidden="true" />
            </span>
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

    <div className="border-t border-white/[0.06] pt-6">
      <GroupVideoManager controller={videosController} />
    </div>
    </>
  );
}
