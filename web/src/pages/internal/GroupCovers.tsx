import { useState } from "react";
import { ArrowUpRight, Image as ImageIcon, Loader2, Plus, Sparkles, WandSparkles, X } from "lucide-react";
import { GroupCoverCard } from "./GroupCoverCard";
import { useGroupCovers } from "./useGroupCovers";
import { useGroupYouTubeVideos } from "./useGroupYouTubeVideos";
import { GroupVideoManager } from "./GroupVideoManager";
import type { GroupCover, GroupDraft } from "./groupCoversTypes";
import { GroupCoverPreviewModal } from "./GroupCoverPreviewModal";
import { ThumbnailDropTarget } from "./ThumbnailDropTarget";

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
  const { state, drafts, refreshCovers, createStandaloneDraft, openCoverEditor, openingCoverId, renameCover, renamingCoverId, saveCoverDraft, savingCoverId } = useGroupCovers(groupId);
  // ONE canonical video-list hook for the whole page: the one-click
  // quick-create AND the Video/Cover manager below share the same list
  // state instead of firing two parallel fetches.
  const videosController = useGroupYouTubeVideos(groupId, true, groupName);
  const { quickCreateCover, openingVideoID } = videosController;
  const [previewCover, setPreviewCover] = useState<GroupCover | null>(null);
  const [previewTitle, setPreviewTitle] = useState("");
  const [previewDescription, setPreviewDescription] = useState("");
  const [draftTarget, setDraftTarget] = useState<GroupDraft | GroupCover | null>(null);

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
      || (cover.thumbnail_media_id ? state.previewUrls[cover.thumbnail_media_id] : undefined)
      || cover.source_thumbnail_url;
  };

  const draftMediaId = (draft: GroupDraft | GroupCover): string | undefined =>
    "thumbnail_media_id" in draft ? (draft.thumbnail_media_id || draft.preview_media_id || undefined) : (draft.preview_media_id || undefined);

  const applyDraft = async (videoId: string, accountId: number) => {
    if (!draftTarget) return;
    const mediaId = draftMediaId(draftTarget);
    if (!mediaId) {
      window.alert("Questa bozza non ha ancora un'immagine esportata. Aprila nell'editor e salvala prima di applicarla.");
      return;
    }
    if (!window.confirm("Impostare questa bozza come copertina del video? L'immagine verrà pubblicata su YouTube.")) return;
    await videosController.applyThumbnailMedia(videoId, accountId, mediaId);
    setDraftTarget(null);
    refreshCovers();
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
          <span className="text-[12px] font-semibold text-[#cbd0d8]">{state.kind === "ready" ? state.covers.length + drafts.length : "—"}</span>
          <span className="text-[11px] text-[#777e8b]">{state.kind === "ready" && state.covers.length + drafts.length === 1 ? "copertina" : "copertine"}</span>
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
        <>
        <div className="mb-5 rounded-2xl border border-white/[0.08] bg-white/[0.025] p-4" data-testid="group-draft-gallery">
          <div className="mb-3 flex items-center justify-between gap-3">
            <div><h3 className="text-[14px] font-bold text-white">Galleria bozze del gruppo</h3><p className="mt-1 text-[11px] text-[#858c99]">Crea una copertina anche senza scegliere subito un video.</p></div>
            <button type="button" onClick={() => { const name = window.prompt("Nome della nuova bozza", "Thumbnail Codex"); if (name !== null) void createStandaloneDraft(name); }} className="inline-flex items-center gap-1.5 rounded-lg border border-violet-400/25 bg-violet-500/[0.12] px-3 py-2 text-[11px] font-bold text-violet-100 hover:bg-violet-500/[0.22]"><WandSparkles size={13} /> Nuova bozza</button>
          </div>
          {drafts.length === 0 && state.covers.length === 0 ? <p className="rounded-xl border border-dashed border-white/[0.1] px-4 py-5 text-center text-[12px] text-[#858c99]">Nessuna bozza ancora. Creane una per iniziare.</p> : <div className="flex gap-3 overflow-x-auto pb-1">
            {[...drafts, ...state.covers.filter((cover) => cover.project_status === "draft")].map((draft) => {
              const mediaId = draftMediaId(draft);
              const preview = "source_thumbnail_url" in draft ? coverAssetUrl(draft) : undefined;
              const draftKey = "id" in draft ? draft.id : draft.project_id;
              return <button type="button" key={draftKey} onClick={() => setDraftTarget(draft)} className="group min-w-[190px] overflow-hidden rounded-xl border border-white/[0.08] bg-black/20 text-left hover:border-violet-400/40">
                <div className="aspect-video bg-white/[0.05]">{preview ? <img src={preview} alt="" className="h-full w-full object-cover" /> : <div className="flex h-full items-center justify-center text-violet-200/60"><ImageIcon size={24} /></div>}</div>
                <div className="p-3"><p className="truncate text-[12px] font-semibold text-white">{draft.name}</p><p className="mt-1 text-[10px] text-[#858c99]">{mediaId ? "Pronta da applicare" : "Bozza editor"}</p></div>
              </button>;
            })}
          </div>}
        </div>
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
            <ThumbnailDropTarget key={cover.project_id} onFile={(file) => {
              if (!window.confirm("Sostituire la copertina di questo video? L'immagine verrà caricata e pubblicata su YouTube.")) return;
              void videosController.applyThumbnailFile(cover.youtube_video_id, cover.platform_account_id, file);
            }} busy={videosController.thumbnailVideoID === cover.youtube_video_id} className="group">
              <GroupCoverCard
                cover={cover}
                previewUrl={coverAssetUrl(cover)}
                opening={openingCoverId === cover.project_id}
                renaming={renamingCoverId === cover.project_id}
                onOpenEditor={handleOpenCoverEditor}
                onOpenPreview={handleOpenPreview}
                onRenameCover={renameCover}
              />
            </ThumbnailDropTarget>
          ))}
        </div>
        </>
      )}

      {draftTarget && state.kind === "ready" ? <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4" role="dialog" aria-modal="true">
        <div className="w-full max-w-lg rounded-2xl border border-white/[0.1] bg-[#11141c] p-5 shadow-2xl"><div className="mb-4 flex items-center justify-between"><div><h3 className="text-[16px] font-bold text-white">Applica bozza</h3><p className="mt-1 text-[11px] text-[#858c99]">Scegli il video di destinazione.</p></div><button type="button" onClick={() => setDraftTarget(null)} className="rounded-lg p-2 text-[#9aa0aa] hover:bg-white/[0.08]"><X size={16} /></button></div><div className="max-h-[55vh] space-y-2 overflow-y-auto">{videosController.state.kind === "ready" ? videosController.state.videos.map((video) => <button type="button" key={video.youtube_video_id} onClick={() => void applyDraft(video.youtube_video_id, video.platform_account_id)} className="flex w-full items-center justify-between rounded-xl border border-white/[0.08] bg-white/[0.03] p-3 text-left hover:border-violet-400/40"><span className="min-w-0"><span className="block truncate text-[12px] font-semibold text-white">{video.title}</span><span className="mt-1 block font-mono text-[10px] text-[#858c99]">{video.youtube_video_id}</span></span><span className="ml-3 shrink-0 text-[11px] font-bold text-violet-200">Imposta</span></button>) : <p className="text-[12px] text-[#858c99]">Caricamento video…</p>}</div></div>
      </div> : null}

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
