import { useCallback, useState } from "react";
import { ErrorState, Skeleton } from "../../components/feedback";
import { YouTubePublishCard } from "./YouTubePublishCard";
import { StudioShell } from "./YouTubeStudioShell";
import { useYouTubeStudioData } from "./useYouTubeStudioData";
import { useYouTubeStudioPrivateVideos } from "./useYouTubeStudioPrivateVideos";
import { useYouTubeStudioActions } from "./useYouTubeStudioActions";
import { YouTubeStudioCreateForm } from "./YouTubeStudioCreateForm";
import { YouTubeStudioPrivateVideosSection } from "./YouTubeStudioPrivateVideosSection";
import { YouTubeStudioSessionsSection } from "./YouTubeStudioSessionsSection";

export function InternalYouTubeStudio() {
  const {
    loadState,
    selectedWorkspaceId,
    setSelectedWorkspaceId,
    selectedChannelId,
    setSelectedChannelId,
    refreshing,
    load,
    handleRefresh,
    patchSession,
  } = useYouTubeStudioData();

  const [privateVideosEnabled, setPrivateVideosEnabled] = useState(false);
  const { privateVideos, loadingVideos, copyrightByVideoId, recordCopyrightCheck, applyThumbnailFile, thumbnailVideoID } = useYouTubeStudioPrivateVideos(
    selectedChannelId,
    privateVideosEnabled,
  );

  const {
    manualVideoId,
    setManualVideoId,
    activeSessionId,
    setActiveSessionId,
    thumbnailMediaId,
    setThumbnailMediaId,
    scheduleAt,
    setScheduleAt,
    action,
    publishResult,
    setPublishResult,
    handleCreateSession,
    handleAttachThumbnail,
    handlePublishNow,
    handleSchedule,
    canCreate,
  } = useYouTubeStudioActions({
    selectedWorkspaceId,
    selectedChannelId,
    refresh: handleRefresh,
    patchSession,
    onCopyrightResult: recordCopyrightCheck,
  });

  const handleSelectVideo = useCallback(
    (videoId: string) => {
      setManualVideoId(videoId);
      window.scrollTo({ top: 0, behavior: "smooth" });
    },
    [setManualVideoId],
  );

  if (loadState.kind === "loading") {
    return (
      <StudioShell>
        <div className="space-y-4" data-testid="yt-studio-loading">
          <Skeleton variant="card" height={56} />
          <Skeleton variant="card" height={56} />
          <Skeleton variant="card" height={56} />
          <Skeleton variant="card" height={120} />
          <Skeleton variant="card" height={240} />
        </div>
      </StudioShell>
    );
  }

  if (loadState.kind === "error") {
    return (
      <StudioShell>
        <ErrorState
          title="Couldn't load YouTube Studio"
          message={loadState.message}
          helpText="Sign in again or reload the page to retry."
          onRetry={() => void load()}
          className="bg-[#1f1f2e] border-white/[0.12]"
        />
      </StudioShell>
    );
  }

  const { workspaces, youtubeChannels, sessions } = loadState;
  const noChannels = youtubeChannels.length === 0;
  const publishSession = publishResult
    ? sessions.find((session) => session.id === publishResult.sessionId)
    : undefined;
  const publishPreview = publishResult
    ? privateVideos.find((video) => video.external_id === publishResult.result.video_id)
    : undefined;

  return (
    <StudioShell>
      <YouTubeStudioCreateForm
        workspaces={workspaces}
        youtubeChannels={youtubeChannels}
        selectedWorkspaceId={selectedWorkspaceId}
        onWorkspaceChange={setSelectedWorkspaceId}
        selectedChannelId={selectedChannelId}
        onChannelChange={setSelectedChannelId}
        manualVideoId={manualVideoId}
        onManualVideoIdChange={setManualVideoId}
        isCreating={action.kind === "creating"}
        canCreate={canCreate}
        onCreate={() => void handleCreateSession()}
      />

      <YouTubeStudioPrivateVideosSection
        selectedChannelId={selectedChannelId}
        privateVideos={privateVideos}
        loadingVideos={loadingVideos}
        copyrightByVideoId={copyrightByVideoId}
        manualVideoId={manualVideoId}
        onSelectVideo={handleSelectVideo}
        privateVideosEnabled={privateVideosEnabled}
        onLoad={() => setPrivateVideosEnabled(true)}
        onThumbnailFile={(video, file) => void applyThumbnailFile(video, file)}
        thumbnailVideoID={thumbnailVideoID}
      />

      {publishResult && (
        <YouTubePublishCard
          result={publishResult.result}
          session={publishSession}
          preview={publishPreview}
          checking={publishResult.checking}
          onDismiss={() => setPublishResult(null)}
        />
      )}

      <YouTubeStudioSessionsSection
        sessions={sessions}
        noChannels={noChannels}
        selectedWorkspaceId={selectedWorkspaceId}
        action={action}
        activeSessionId={activeSessionId}
        thumbnailMediaId={thumbnailMediaId}
        scheduleAt={scheduleAt}
        onToggle={(sessionId) =>
          setActiveSessionId((prev) => (prev === sessionId ? null : sessionId))
        }
        onThumbnailChange={setThumbnailMediaId}
        onScheduleAtChange={setScheduleAt}
        onAttach={(sessionId) => void handleAttachThumbnail(sessionId)}
        onPublishNow={(sessionId) => void handlePublishNow(sessionId)}
        onSchedule={(sessionId) => void handleSchedule(sessionId)}
        refreshing={refreshing}
        onRefresh={() => void handleRefresh()}
      />
    </StudioShell>
  );
}
