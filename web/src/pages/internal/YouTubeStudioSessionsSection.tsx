import { CheckCircle2, Loader2, Video } from "lucide-react";
import { EmptyState } from "../../components/feedback";
import { SessionRow } from "./YouTubeStudioSessionRow";
import type { ActionState } from "./youtubeStudioTypes";
import type { EditorSession } from "../../types/uploads";

/**
 * YouTubeStudioSessionsSection renders the "Sessions awaiting your input"
 * panel: the refresh action, the scoped empty states and the SessionRow
 * list. Pure presentational — row actions are passed down as callbacks
 * from InternalYouTubeStudio.
 */
export function YouTubeStudioSessionsSection({
  sessions,
  noChannels,
  selectedWorkspaceId,
  action,
  activeSessionId,
  thumbnailMediaId,
  scheduleAt,
  onToggle,
  onThumbnailChange,
  onScheduleAtChange,
  onAttach,
  onPublishNow,
  onSchedule,
  refreshing,
  onRefresh,
}: {
  sessions: EditorSession[];
  noChannels: boolean;
  selectedWorkspaceId: number | "";
  action: ActionState;
  activeSessionId: string | null;
  thumbnailMediaId: string;
  scheduleAt: string;
  onToggle: (sessionId: string) => void;
  onThumbnailChange: (value: string) => void;
  onScheduleAtChange: (value: string) => void;
  onAttach: (sessionId: string) => void;
  onPublishNow: (sessionId: string) => void;
  onSchedule: (sessionId: string) => void;
  refreshing: boolean;
  onRefresh: () => void;
}) {
  return (
    <section className="bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-6 space-y-4 shadow-[0_8px_32px_rgba(0,0,0,0.4)]">
      <header className="flex items-center justify-between gap-3">
        <div>
          <h2 className="text-[16px] font-bold text-white flex items-center gap-2">
            <Video size={16} aria-hidden="true" />
            Sessions awaiting your input
          </h2>
          <p className="text-[13px] text-[#9aa0aa] mt-1">
            {sessions.length === 0
              ? "No active sessions."
              : `${sessions.length} session${sessions.length === 1 ? "" : "s"} ready to publish.`}
          </p>
        </div>
        <button
          type="button"
          onClick={onRefresh}
          disabled={refreshing || selectedWorkspaceId === ""}
          className="inline-flex items-center gap-1 text-[12px] font-semibold text-[#9aa0aa] hover:text-white transition-colors disabled:opacity-50"
          data-testid="yt-studio-refresh"
        >
          {refreshing ? (
            <Loader2 size={12} className="animate-spin" aria-hidden="true" />
          ) : null}
          Refresh
        </button>
      </header>

      {selectedWorkspaceId === "" && (
        <EmptyState
          title="Select a workspace first"
          description="The list of editor sessions is scoped per workspace."
          icon={<Video size={32} />}
          className="bg-white/[0.02] border-white/[0.06]"
        />
      )}

      {selectedWorkspaceId !== "" && noChannels && (
        <EmptyState
          title="No YouTube channels connected"
          description="Connect a YouTube channel in /app/linking to manage its videos."
          icon={<Video size={32} />}
          className="bg-white/[0.02] border-white/[0.06]"
        />
      )}

      {selectedWorkspaceId !== "" && !noChannels && sessions.length === 0 && (
        <EmptyState
          title="Nothing waiting"
          description="Once the Drive-folder importer finishes, editor sessions will appear here."
          icon={<CheckCircle2 size={32} />}
          className="bg-white/[0.02] border-white/[0.06]"
        />
      )}

      {selectedWorkspaceId !== "" && !noChannels && sessions.length > 0 && (
        <div className="space-y-3" data-testid="yt-studio-sessions">
          {sessions.map((session) => (
            <SessionRow
              key={session.id}
              session={session}
              isActive={
                action.kind === "attaching" && action.sessionId === session.id
              }
              isPublishing={
                action.kind === "publishing" && action.sessionId === session.id
              }
              isExpanded={activeSessionId === session.id}
              thumbnailMediaId={thumbnailMediaId}
              scheduleAt={scheduleAt}
              onToggle={() => onToggle(session.id)}
              onThumbnailChange={onThumbnailChange}
              onScheduleAtChange={onScheduleAtChange}
              onAttach={() => onAttach(session.id)}
              onPublishNow={() => onPublishNow(session.id)}
              onSchedule={() => onSchedule(session.id)}
            />
          ))}
        </div>
      )}
    </section>
  );
}
