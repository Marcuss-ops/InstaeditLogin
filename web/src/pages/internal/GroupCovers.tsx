import { useMemo, useState } from "react";
import { Image as ImageIcon, Loader2, Plus, RefreshCw } from "lucide-react";
import { EmptyState } from "../../components/feedback/EmptyState";
import { GroupCoverCard } from "./GroupCoverCard";
import { useGroupCovers } from "./useGroupCovers";
import { useGroupYouTubeVideos } from "./useGroupYouTubeVideos";
import { cn } from "../../lib/utils";

type CoverFilter = "all" | "draft" | "ready" | "archived";

const FILTERS: Array<{ key: CoverFilter; label: string }> = [
  { key: "all", label: "Tutte" },
  { key: "draft", label: "Bozze" },
  { key: "ready", label: "Pronte" },
  { key: "archived", label: "Archiviate" },
];

/**
 * Covers grid for one group (the Copertine hub body). Replaces the
 * video grid: the user picks a group and sees every cover project
 * created in it — current + archived history — with its rendered
 * preview, status, channel/video and a "Modifica in InstaEditor" CTA
 * that opens the editor in a new tab (the SPA never navigates away).
 */
export function GroupCovers({ groupId }: { groupId: number }) {
  const { state, refreshCovers, openCoverEditor, openingCoverId } = useGroupCovers(groupId);
  // Video manifest for the one-click create: quickCreateCover opens
  // InstaEditor directly on the group's most recent private video and
  // saves the new cover under a random name (no picker dialog).
  const { quickCreateCover, openingVideoID } = useGroupYouTubeVideos(groupId);
  const [filter, setFilter] = useState<CoverFilter>("all");

  const handleCreateCover = () => {
    void quickCreateCover().then((opened) => {
      if (opened) refreshCovers();
    });
  };

  const visibleCovers = useMemo(() => {
    if (state.kind !== "ready") return [];
    if (filter === "all") return state.covers;
    return state.covers.filter((cover) => cover.project_status === filter);
  }, [filter, state]);

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
        <div className="flex items-center gap-2">
          {/* Always-visible create CTA (Photoshop-style fixed button in the
              covers zone): quickCreateCover opens InstaEditor directly on
              the group's most recent private video with a random project
              name — works even when the grid is full. */}
          <button
            type="button"
            onClick={handleCreateCover}
            disabled={openingVideoID != null}
            aria-label="Crea copertina"
            data-testid="group-covers-create-header"
            className="group relative inline-flex h-9 w-9 items-center justify-center rounded-full border border-violet-500/30 bg-violet-500/[0.10] text-violet-200 shadow-lg transition-all duration-200 hover:scale-105 hover:border-violet-400/50 hover:bg-violet-500/[0.22] hover:text-violet-100 disabled:cursor-wait disabled:opacity-60"
          >
            {openingVideoID != null ? (
              <Loader2 size={17} className="animate-spin" aria-hidden="true" />
            ) : (
              <Plus size={17} aria-hidden="true" />
            )}
            <span className="pointer-events-none absolute left-1/2 top-full z-20 mt-2 -translate-x-1/2 whitespace-nowrap rounded-lg border border-white/[0.08] bg-[#0c0c12] px-2.5 py-1 text-[11px] font-semibold text-white opacity-0 shadow-lg transition-opacity duration-150 group-hover:opacity-100">
              Crea copertina
            </span>
          </button>
          <div className="flex items-center gap-1 rounded-xl border border-white/[0.08] bg-white/[0.03] p-1">
            {FILTERS.map((f) => (
              <button
                key={f.key}
                type="button"
                onClick={() => setFilter(f.key)}
                className={cn(
                  "rounded-lg px-2.5 py-1 text-[11px] font-semibold transition-colors",
                  filter === f.key
                    ? "bg-white text-black"
                    : "text-[#9aa0aa] hover:bg-white/[0.06] hover:text-white",
                )}
              >
                {f.label}
              </button>
            ))}
          </div>
          <button
            type="button"
            onClick={refreshCovers}
            className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.08] bg-white/[0.04] px-2.5 py-1.5 text-[11px] font-semibold text-[#cdd2da] transition-colors hover:bg-white/[0.08] hover:text-white"
            data-testid="group-covers-refresh"
          >
            <RefreshCw size={12} aria-hidden="true" />
            Aggiorna
          </button>
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

      {state.kind === "ready" && visibleCovers.length === 0 && (
        <EmptyState
          title={filter === "all" ? "Nessuna copertina in questo gruppo" : "Nessuna copertina in questo stato"}
          icon={<ImageIcon size={24} />}
          className="mx-auto max-w-sm bg-white/[0.02] py-10 border-white/[0.08]"
          cta={
            filter === "all" ? (
              <div className="group relative mx-auto mt-1 inline-block">
                <button
                  type="button"
                  onClick={handleCreateCover}
                  disabled={openingVideoID != null}
                  aria-label="Crea copertina"
                  data-testid="group-covers-create"
                  className="flex h-14 w-14 items-center justify-center rounded-full border border-violet-500/30 bg-violet-500/[0.10] text-violet-200 shadow-lg transition-all duration-200 hover:scale-105 hover:border-violet-400/50 hover:bg-violet-500/[0.22] hover:text-violet-100 disabled:cursor-wait disabled:opacity-60"
                >
                  {openingVideoID != null ? (
                    <Loader2 size={24} className="animate-spin" aria-hidden="true" />
                  ) : (
                    <Plus size={24} aria-hidden="true" />
                  )}
                </button>
                <span className="pointer-events-none absolute left-1/2 top-full mt-2.5 -translate-x-1/2 whitespace-nowrap rounded-lg border border-white/[0.08] bg-[#0c0c12] px-2.5 py-1 text-[11px] font-semibold text-white opacity-0 shadow-lg transition-opacity duration-150 group-hover:opacity-100">
                  Crea copertina
                </span>
              </div>
            ) : undefined
          }
        />
      )}

      {state.kind === "ready" && visibleCovers.length > 0 && (
        <div className="grid grid-cols-1 gap-4 min-[560px]:grid-cols-2 min-[1080px]:grid-cols-3 min-[1440px]:grid-cols-4">
          {visibleCovers.map((cover) => (
            <GroupCoverCard
              key={cover.project_id}
              cover={cover}
              previewUrl={cover.preview_media_id ? state.previewUrls[cover.preview_media_id] : undefined}
              opening={openingCoverId === cover.project_id}
              onOpenEditor={openCoverEditor}
            />
          ))}
        </div>
      )}

    </section>
  );
}
