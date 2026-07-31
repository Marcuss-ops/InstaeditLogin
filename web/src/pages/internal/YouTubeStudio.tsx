import { useCallback, useEffect, useRef, useState } from "react";
import { AuthError, authedFetch } from "../../lib/auth";
import {
  CheckCircle2,
  ExternalLink,
  Loader2,
  Pencil,
  Video,
} from "lucide-react";
import {
  createYouTubeEditorSession,
  listYouTubeEditorSessions,
  attachYouTubeEditorSessionThumbnail,
  publishYouTubeEditorSession,
  openEditorInNewTab,
} from "../../features/youtube/api/editorSessionsApi";
import { useToast } from "../../components/toast";
import { EmptyState, ErrorState, Skeleton } from "../../components/feedback";
import { cn } from "../../lib/utils";
import { FormField, FormSelect } from "./YouTubeStudioFormElements";
import { SessionRow } from "./YouTubeStudioSessionRow";
import { StudioShell } from "./YouTubeStudioShell";
import { isScheduleInPast, localToUTC } from "./youtubeStudioTime";
import type { ActionState, ContentItem, LoadState } from "./youtubeStudioTypes";
import type { EditorSession, PlatformAccount, Workspace } from "../../types/uploads";

export function InternalYouTubeStudio() {
  const toast = useToast();
  const abortRef = useRef<AbortController | null>(null);

  const [loadState, setLoadState] = useState<LoadState>({ kind: "loading" });
  const [selectedWorkspaceId, setSelectedWorkspaceId] = useState<number | "">(
    "",
  );
  const [selectedChannelId, setSelectedChannelId] = useState<number | "">("");
  const [manualVideoId, setManualVideoId] = useState("");
  const [activeSessionId, setActiveSessionId] = useState<string | null>(null);
  const [thumbnailMediaId, setThumbnailMediaId] = useState("");
  const [scheduleAt, setScheduleAt] = useState("");
  const [action, setAction] = useState<ActionState>({ kind: "idle" });
  const [refreshing, setRefreshing] = useState(false);
  const [privateVideos, setPrivateVideos] = useState<ContentItem[]>([]);
  const [loadingVideos, setLoadingVideos] = useState(false);
  const privateVideosAbortRef = useRef<AbortController | null>(null);

  const fetchPrivateVideos = useCallback(
    async (accountId: number, signal?: AbortSignal) => {
      setLoadingVideos(true);
      try {
        const resp = await authedFetch(
          `/api/v1/accounts/${accountId}/content?limit=50&privacy=private`,
          { signal },
        );
        const data = (await resp.json()) as { items: ContentItem[] };
        if (!signal?.aborted) setPrivateVideos(data.items ?? []);
      } catch {
        if (!signal?.aborted) setPrivateVideos([]);
      } finally {
        if (!signal?.aborted) setLoadingVideos(false);
      }
    },
    [],
  );

  const fetchSessions = useCallback(
    async (
      workspaceId: number | "",
      accountId: number | "",
      signal?: AbortSignal,
    ): Promise<EditorSession[]> => {
      // The list helper composes the workspace_id + account_id query
      // string into /api/v1/youtube/editor-sessions. We narrow
      // `number | ""` to `number | undefined` for the helper's
      // optional-key shape (it does the `if (x !== undefined)` check
      // internally so we don't need to pre-filter empty strings).
      return listYouTubeEditorSessions({
        workspace_id: workspaceId === "" ? undefined : workspaceId,
        account_id: accountId === "" ? undefined : accountId,
        signal,
      });
    },
    [],
  );

  const load = useCallback(async () => {
    setLoadState({ kind: "loading" });
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    try {
      const [wsR, acctsR] = await Promise.all([
        authedFetch("/api/v1/workspaces", { signal: ctrl.signal }),
        authedFetch("/api/v1/accounts", { signal: ctrl.signal }),
      ]);
      if (ctrl.signal.aborted) return;

      const ws =
        ((await wsR.json()) as { workspaces: Workspace[] }).workspaces ?? [];
      const accts =
        ((await acctsR.json()) as { accounts: PlatformAccount[] }).accounts ??
        [];
      const youtubeChannels = accts.filter((a) => a.platform === "youtube");

      const resolvedWorkspaceId =
        ws.length === 1 ? ws[0].id : "";
      const resolvedChannelId =
        youtubeChannels.length === 1 ? youtubeChannels[0].id : "";

      // First list fetch uses the auto-resolved single-workspace/single-channel
      // (if any). If both are empty the user must pick before we can list;
      // we still set ready so the filters render.
      const sessions = resolvedWorkspaceId
        ? await fetchSessions(resolvedWorkspaceId, resolvedChannelId, ctrl.signal)
        : [];

      if (ctrl.signal.aborted) return;
      setLoadState({
        kind: "ready",
        workspaces: ws,
        youtubeChannels,
        sessions,
      });
      setSelectedWorkspaceId(resolvedWorkspaceId);
      setSelectedChannelId(resolvedChannelId);
    } catch (err) {
      if (ctrl.signal.aborted) return;
      if (err instanceof AuthError) {
        toast.error("Session expired — please sign in again.");
        return;
      }
      setLoadState({
        kind: "error",
        message:
          err instanceof Error
            ? err.message
            : "Unable to load YouTube editor sessions.",
      });
    }
  }, [fetchSessions, toast]);

  useEffect(() => {
    void load();
    return () => abortRef.current?.abort();
  }, [load]);

  // Re-fetch sessions whenever the workspace or channel filter changes.
  // The list endpoint filters server-side, so we don't need client-side
  // filtering on the response payload. Skip during the initial `loading`
  // fetch (load() owns that) and abort in-flight requests to avoid races.
  useEffect(() => {
    if (loadState.kind !== "ready") return;
    if (selectedWorkspaceId === "") {
      setLoadState((prev) =>
        prev.kind === "ready" ? { ...prev, sessions: [] } : prev,
      );
      return;
    }
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    void (async () => {
      try {
        const sessions = await fetchSessions(
          selectedWorkspaceId,
          selectedChannelId,
          ctrl.signal,
        );
        if (ctrl.signal.aborted) return;
        setLoadState((prev) =>
          prev.kind === "ready" ? { ...prev, sessions } : prev,
        );
      } catch {
        // Non-fatal: keep the previous list visible.
      }
    })();
  }, [fetchSessions, loadState.kind, selectedChannelId, selectedWorkspaceId]);

  useEffect(() => {
    privateVideosAbortRef.current?.abort();
    setPrivateVideos([]);
    if (selectedChannelId === "") return;
    const ctrl = new AbortController();
    privateVideosAbortRef.current = ctrl;
    void fetchPrivateVideos(selectedChannelId, ctrl.signal);
    return () => ctrl.abort();
  }, [fetchPrivateVideos, selectedChannelId]);

  const handleRefresh = useCallback(async () => {
    if (refreshing || selectedWorkspaceId === "") return;
    setRefreshing(true);
    try {
      const sessions = await fetchSessions(
        selectedWorkspaceId,
        selectedChannelId,
      );
      setLoadState((prev) =>
        prev.kind === "ready" ? { ...prev, sessions } : prev,
      );
    } catch {
      // toast surfaced by authedFetch
    } finally {
      setRefreshing(false);
    }
  }, [fetchSessions, refreshing, selectedChannelId, selectedWorkspaceId]);

  const handleCreateSession = useCallback(async () => {
    if (
      selectedWorkspaceId === "" ||
      selectedChannelId === "" ||
      !manualVideoId.trim()
    ) {
      return;
    }
    const videoId = manualVideoId.trim();
    setAction({ kind: "creating" });
    try {
      // Narrow before the call. `canCreate` already guards against
      // `selectedWorkspaceId === ""`, but tsc types the field as
      // `number | ""` (whole form) so we narrow explicitly instead
      // of casting via `as number` (which was a pre-existing latent
      // bug: JSON.stringify would have injected `""` if a stale
      // state ever slipped past canCreate).
      if (
        typeof selectedWorkspaceId !== "number" ||
        typeof selectedChannelId !== "number"
      ) {
        setAction({ kind: "idle" });
        return;
      }
      const session = await createYouTubeEditorSession({
        workspace_id: selectedWorkspaceId,
        platform_account_id: selectedChannelId,
        youtube_video_id: videoId,
      });
      toast.success("Editor session created — opening Velox…");
      setManualVideoId("");
      // Reset to idle immediately so the form re-enables for the next
      // submission. The opened tab is the user's confirmation; we don't
      // gate further form interaction on it.
      setAction({ kind: "idle" });
      openEditorInNewTab(session.editor_url);
      void handleRefresh();
    } catch (err) {
      if (err instanceof AuthError) return;
      setAction({ kind: "idle" });
      // authedFetch already toasts on non-OK responses; keep the form
      // mounted so the user can retry.
    }
  }, [
    handleRefresh,
    manualVideoId,
    selectedChannelId,
    selectedWorkspaceId,
    toast,
  ]);

  const handleAttachThumbnail = useCallback(
    async (sessionId: string) => {
      const mediaId = thumbnailMediaId.trim();
      if (!mediaId) return;
      setAction({ kind: "attaching", sessionId });
      try {
        await attachYouTubeEditorSessionThumbnail(sessionId, {
          thumbnail_media_id: mediaId,
        });
        toast.success("Thumbnail attached.");
        setThumbnailMediaId("");
        setActiveSessionId(null);
        void handleRefresh();
      } catch {
        // toast surfaced by authedFetch
      } finally {
        setAction({ kind: "idle" });
      }
    },
    [handleRefresh, thumbnailMediaId, toast],
  );

  const handlePublishNow = useCallback(
    async (sessionId: string) => {
      setAction({ kind: "publishing", sessionId });
      try {
        await publishYouTubeEditorSession(sessionId, {
          privacy_status: "public",
        });
        toast.success("Video published.");
        void handleRefresh();
      } catch {
        // toast surfaced by authedFetch
      } finally {
        setAction({ kind: "idle" });
      }
    },
    [handleRefresh, toast],
  );

  const handleSchedule = useCallback(
    async (sessionId: string) => {
      if (!scheduleAt) return;
      const publishAtDate = new Date(scheduleAt);
      if (isNaN(publishAtDate.getTime())) {
        toast.error("Pick a valid publish date.");
        return;
      }
      if (isScheduleInPast(scheduleAt)) {
        toast.error("La data di pubblicazione deve essere nel futuro.");
        return;
      }
      const utcISO = localToUTC(scheduleAt);
      setAction({ kind: "publishing", sessionId });
      try {
        await publishYouTubeEditorSession(sessionId, {
          privacy_status: "private",
          publish_at: utcISO,
        });
        toast.success("Publication scheduled.");
        setScheduleAt("");
        setActiveSessionId(null);
        void handleRefresh();
      } catch {
        // toast surfaced by authedFetch
      } finally {
        setAction({ kind: "idle" });
      }
    },
    [handleRefresh, scheduleAt, toast],
  );

  const canCreate =
    action.kind !== "creating" &&
    selectedWorkspaceId !== "" &&
    selectedChannelId !== "" &&
    manualVideoId.trim().length > 0;

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

  return (
    <StudioShell>
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
            onChange={setSelectedWorkspaceId}
            placeholder="Select a workspace…"
            disabled={action.kind === "creating"}
            options={workspaces.map((w) => ({ value: w.id, label: w.name }))}
          />
          <FormSelect
            id="yt-studio-channel"
            label="YouTube channel"
            value={selectedChannelId}
            onChange={setSelectedChannelId}
            placeholder="Select a channel…"
            disabled={action.kind === "creating"}
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
            disabled={action.kind === "creating"}
            onChange={(e) => setManualVideoId(e.target.value)}
            className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white placeholder:text-white/20 focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all disabled:opacity-50"
            spellCheck={false}
            autoComplete="off"
            data-testid="yt-studio-video-id-input"
          />
        </FormField>

        <div className="flex items-center justify-end gap-3 pt-2">
          <button
            type="button"
            onClick={() => void handleCreateSession()}
            disabled={!canCreate}
            data-testid="yt-studio-create"
            className="inline-flex items-center gap-2 px-5 py-2.5 rounded-xl bg-white text-black text-[14px] font-semibold hover:bg-white/90 transition-all disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {action.kind === "creating" ? (
              <Loader2 size={16} className="animate-spin" aria-hidden="true" />
            ) : (
              <ExternalLink size={16} aria-hidden="true" />
            )}
            {action.kind === "creating"
              ? "Opening editor…"
              : "Modifica copertina"}
          </button>
        </div>
      </section>

      {selectedChannelId !== "" && (
        <section className="bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-6 space-y-4 shadow-[0_8px_32px_rgba(0,0,0,0.4)]">
          <header>
            <h2 className="text-[16px] font-bold text-white flex items-center gap-2">
              <Video size={16} aria-hidden="true" />
              Video privati sul canale
            </h2>
            <p className="text-[13px] text-[#9aa0aa] mt-1">
              Clicca un video per iniziare a modificare la copertina.
            </p>
          </header>

          {loadingVideos && (
            <div className="flex items-center gap-2 text-[13px] text-[#9aa0aa]">
              <Loader2 size={14} className="animate-spin" /> Caricamento video…
            </div>
          )}

          {!loadingVideos && privateVideos.length === 0 && (
            <EmptyState
              title="Nessun video privato trovato"
              description="Carica un video privato su YouTube e ricarica la pagina."
              icon={<Video size={32} />}
              className="bg-white/[0.02] border-white/[0.06]"
            />
          )}

          {!loadingVideos && privateVideos.length > 0 && (
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
              {privateVideos.map((v) => (
                <button
                  key={v.external_id}
                  type="button"
                  onClick={() => {
                    setManualVideoId(v.external_id);
                    window.scrollTo({ top: 0, behavior: "smooth" });
                  }}
                  className={cn(
                    "flex gap-3 p-3 rounded-xl border text-left transition-all no-underline group",
                    manualVideoId === v.external_id
                      ? "border-blue-500/50 bg-blue-500/[0.08]"
                      : "border-white/[0.08] bg-white/[0.03] hover:bg-white/[0.06] hover:border-white/[0.15]",
                  )}
                >
                  <div className="w-28 h-16 rounded-lg bg-white/[0.08] overflow-hidden shrink-0">
                    {v.thumbnail_url ? (
                      <img
                        src={v.thumbnail_url}
                        alt={v.title ?? ""}
                        className="w-full h-full object-cover"
                        loading="lazy"
                      />
                    ) : (
                      <div className="w-full h-full flex items-center justify-center">
                        <Video size={16} className="text-white/20" />
                      </div>
                    )}
                  </div>
                  <div className="min-w-0 flex-1">
                    <p className="text-[12px] font-semibold text-white truncate">
                      {v.title || v.external_id}
                    </p>
                    <p className="text-[11px] text-[#9aa0aa] font-mono mt-0.5">
                      {v.external_id}
                    </p>
                    {v.published_at && (
                      <p className="text-[10px] text-[#9aa0aa] mt-0.5">
                        {new Date(v.published_at).toLocaleDateString("it-IT")}
                      </p>
                    )}
                  </div>
                </button>
              ))}
            </div>
          )}
        </section>
      )}

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
            onClick={() => void handleRefresh()}
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
                onToggle={() =>
                  setActiveSessionId((prev) =>
                    prev === session.id ? null : session.id,
                  )
                }
                onThumbnailChange={setThumbnailMediaId}
                onScheduleAtChange={setScheduleAt}
                onAttach={() => void handleAttachThumbnail(session.id)}
                onPublishNow={() => void handlePublishNow(session.id)}
                onSchedule={() => void handleSchedule(session.id)}
              />
            ))}
          </div>
        )}
      </section>
    </StudioShell>
  );
}
