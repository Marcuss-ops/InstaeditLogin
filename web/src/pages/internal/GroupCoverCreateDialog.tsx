import { useEffect, useRef } from "react";
import { Loader2, Plus, Video, X } from "lucide-react";
import { EmptyState } from "../../components/feedback/EmptyState";
import { useGroupYouTubeVideos } from "./useGroupYouTubeVideos";
import { safeAssetUrl } from "./groupYouTubeVideosVisual";

/**
 * "Crea copertina" picker for the Copertine hub.
 *
 * A cover is always bound to a video (the editor session is keyed on
 * workspace + account + youtube_video), so this dialog lists the
 * group's private videos and reuses the canonical Groups → Modifica
 * flow (useGroupYouTubeVideos.openThumbnailEditor): picking a video
 * resolves/reuses the InstaEditor project and opens the editor in a
 * NEW TAB — the SPA never navigates away. onCreated fires after the
 * open attempt so the hub can close the dialog and refresh the grid
 * (the freshly created cover then appears in the group).
 */
export function GroupCoverCreateDialog({
  groupId,
  onClose,
  onCreated,
}: {
  groupId: number;
  onClose: () => void;
  onCreated: () => void;
}) {
  const { state, openingVideoID, openThumbnailEditor } = useGroupYouTubeVideos(groupId);
  const panelRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    panelRef.current?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onClose]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-sm"
      role="dialog"
      aria-modal="true"
      aria-label="Crea copertina"
      data-testid="group-cover-create-dialog"
      onClick={onClose}
    >
      <div
        ref={panelRef}
        tabIndex={-1}
        className="flex max-h-[80vh] w-full max-w-lg flex-col overflow-hidden rounded-2xl border border-white/[0.10] bg-[#12121a] shadow-2xl outline-none"
        onClick={(event) => event.stopPropagation()}
      >
        <div className="flex items-start justify-between gap-4 border-b border-white/[0.06] px-5 py-4">
          <div>
            <h3 className="text-[15px] font-bold text-white">Crea copertina</h3>
            <p className="mt-0.5 text-[12px] text-[#9aa0aa]">
              Scegli un video privato del gruppo: la copertina si apre in InstaEditor.
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label="Chiudi"
            className="rounded-lg p-1.5 text-[#9aa0aa] transition-colors hover:bg-white/[0.06] hover:text-white"
          >
            <X size={16} aria-hidden="true" />
          </button>
        </div>

        <div className="min-h-[200px] flex-1 overflow-y-auto p-3">
          {state.kind === "loading" && (
            <div className="flex items-center justify-center gap-2 py-10 text-[12px] text-[#9aa0aa]">
              <Loader2 size={16} className="animate-spin" aria-hidden="true" />
              Caricamento video…
            </div>
          )}

          {state.kind === "error" && (
            <EmptyState
              title="Impossibile caricare i video"
              description={state.message}
              icon={<Video size={20} />}
              className="mx-auto max-w-xs border-white/[0.08] bg-white/[0.02] py-6"
            />
          )}

          {state.kind === "ready" && state.videos.length === 0 && (
            <EmptyState
              title="Nessun video privato nel gruppo"
              description="Carica un video privato nel gruppo per potergli disegnare una copertina."
              icon={<Video size={20} />}
              className="mx-auto max-w-xs border-white/[0.08] bg-white/[0.02] py-6"
            />
          )}

          {state.kind === "ready" && state.videos.length > 0 && (
            <ul className="flex flex-col gap-1.5">
              {state.videos.map((video) => {
                const opening = openingVideoID === video.youtube_video_id;
                return (
                  <li key={`${video.platform_account_id}:${video.youtube_video_id}`}>
                    <button
                      type="button"
                      disabled={opening}
                      onClick={() => {
                        void openThumbnailEditor(video).then((opened) => {
                          if (opened) onCreated();
                        });
                      }}
                      className="flex w-full items-center gap-3 rounded-xl border border-white/[0.06] bg-white/[0.02] p-2.5 text-left transition-colors hover:border-violet-400/30 hover:bg-white/[0.05] disabled:cursor-wait disabled:opacity-60"
                      data-testid="group-cover-create-video"
                    >
                      <div className="relative h-12 w-20 shrink-0 overflow-hidden rounded-lg bg-black/40">
                        {safeAssetUrl(video.thumbnail_url) ? (
                          <img
                            src={safeAssetUrl(video.thumbnail_url)}
                            alt=""
                            loading="lazy"
                            className="h-full w-full object-cover"
                          />
                        ) : (
                          <div className="flex h-full items-center justify-center">
                            <Video size={16} className="text-white/25" aria-hidden="true" />
                          </div>
                        )}
                      </div>
                      <div className="min-w-0 flex-1">
                        <p className="truncate text-[13px] font-semibold text-white">
                          {video.title || "Video senza titolo"}
                        </p>
                        <p className="mt-0.5 truncate font-mono text-[11px] text-[#9aa0aa]">
                          {video.channel_name ? `${video.channel_name} · ` : ""}
                          {video.youtube_video_id}
                        </p>
                      </div>
                      {opening ? (
                        <Loader2 size={16} className="shrink-0 animate-spin text-violet-300" aria-hidden="true" />
                      ) : (
                        <Plus size={16} className="shrink-0 text-violet-300" aria-hidden="true" />
                      )}
                    </button>
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      </div>
    </div>
  );
}
