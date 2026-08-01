import { ExternalLink, Loader2, Pencil } from "lucide-react";
import { FormField, FormSelect } from "./YouTubeStudioFormElements";
import type { PlatformAccount, Workspace } from "./youtubeStudioTypes";

/**
 * YouTubeStudioCreateForm is the "Edit a video already on the channel"
 * panel: workspace/channel filters, a manual video-ID input and the
 * create action. Pure presentational — all state lives in the hooks
 * used by InternalYouTubeStudio.
 */
export function YouTubeStudioCreateForm({
  workspaces,
  youtubeChannels,
  selectedWorkspaceId,
  onWorkspaceChange,
  selectedChannelId,
  onChannelChange,
  manualVideoId,
  onManualVideoIdChange,
  isCreating,
  canCreate,
  onCreate,
}: {
  workspaces: Workspace[];
  youtubeChannels: PlatformAccount[];
  selectedWorkspaceId: number | "";
  onWorkspaceChange: (value: number | "") => void;
  selectedChannelId: number | "";
  onChannelChange: (value: number | "") => void;
  manualVideoId: string;
  onManualVideoIdChange: (value: string) => void;
  isCreating: boolean;
  canCreate: boolean;
  onCreate: () => void;
}) {
  return (
    <section className="bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-6 space-y-5 shadow-[0_8px_32px_rgba(0,0,0,0.4)]">
      <header>
        <h2 className="text-[16px] font-bold text-white flex items-center gap-2">
          <Pencil size={16} aria-hidden="true" />
          Edit a video already on the channel
        </h2>
        <p className="text-[13px] text-[#9aa0aa] mt-1">
          Paste a private video's ID to open the Velox thumbnail editor.
        </p>
      </header>

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <FormSelect
          id="yt-studio-workspace"
          label="Workspace"
          value={selectedWorkspaceId}
          onChange={onWorkspaceChange}
          placeholder="Select a workspace…"
          disabled={isCreating}
          options={workspaces.map((w) => ({ value: w.id, label: w.name }))}
        />
        <FormSelect
          id="yt-studio-channel"
          label="YouTube channel"
          value={selectedChannelId}
          onChange={onChannelChange}
          placeholder="Select a channel…"
          disabled={isCreating}
          options={youtubeChannels.map((c) => ({
            value: c.id,
            label: `@${c.username}`,
          }))}
        />
      </div>

      <FormField
        id="yt-studio-video-id"
        label="YouTube Video ID"
        helpText="The 11-char ID after v= in any YouTube URL, e.g. dQw4w9WgXcQ."
      >
        <input
          id="yt-studio-video-id"
          type="text"
          placeholder="dQw4w9WgXcQ"
          value={manualVideoId}
          disabled={isCreating}
          onChange={(e) => onManualVideoIdChange(e.target.value)}
          className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white placeholder:text-white/20 focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all disabled:opacity-50"
          spellCheck={false}
          autoComplete="off"
          data-testid="yt-studio-video-id-input"
        />
      </FormField>

      <div className="flex items-center justify-end gap-3 pt-2">
        <button
          type="button"
          onClick={onCreate}
          disabled={!canCreate}
          data-testid="yt-studio-create"
          className="inline-flex items-center gap-2 px-5 py-2.5 rounded-xl bg-white text-black text-[14px] font-semibold hover:bg-white/90 transition-all disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {isCreating ? (
            <Loader2 size={16} className="animate-spin" aria-hidden="true" />
          ) : (
            <ExternalLink size={16} aria-hidden="true" />
          )}
          {isCreating ? "Opening editor…" : "Modifica copertina"}
        </button>
      </div>
    </section>
  );
}
