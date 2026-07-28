import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import {
  ArrowLeft,
  ArrowRight,
  CheckCircle2,
  ExternalLink,
  Image as ImageIcon,
  Loader2,
  Pencil,
  Send,
  Video,
} from "lucide-react";
import { AuthError, authedFetch } from "../../lib/auth";
import { useToast } from "../../components/toast";
import { EmptyState, ErrorState, Skeleton } from "../../components/feedback";
import { cn } from "../../lib/utils";
import type {
  EditorSession,
  PlatformAccount,
  Workspace,
} from "../../types/uploads";

type LoadState =
  | { kind: "loading" }
  | {
      kind: "ready";
      workspaces: Workspace[];
      youtubeChannels: PlatformAccount[];
      sessions: EditorSession[];
    }
  | { kind: "error"; message: string };

type ActionState =
  | { kind: "idle" }
  | { kind: "creating" }
  | { kind: "attaching"; sessionId: string }
  | { kind: "publishing"; sessionId: string };

type ContentItem = {
  external_id: string;
  title?: string;
  description?: string;
  thumbnail_url?: string;
  public_url?: string;
  privacy?: string;
  status?: string;
  published_at?: string;
  duration?: string;
};

// The list endpoint requires workspace_id; account_id is optional and
// narrows results to a single channel. Keep these in sync with the
// handler in pkg/api/youtube_editor_sessions.go (handleListYouTubeEditorSessions).
function buildSessionsQuery(workspaceId: number | "", accountId: number | "") {
  const params = new URLSearchParams();
  if (workspaceId !== "") params.set("workspace_id", String(workspaceId));
  if (accountId !== "") params.set("account_id", String(accountId));
  return params.toString();
}

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
      const qs = buildSessionsQuery(workspaceId, accountId);
      const resp = await authedFetch(
        `/api/v1/youtube/editor-sessions?${qs}`,
        { signal },
      );
      const data = (await resp.json()) as { sessions: EditorSession[] };
      return data.sessions ?? [];
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
      const resp = await authedFetch("/api/v1/youtube/editor-sessions", {
        method: "POST",
        body: JSON.stringify({
          workspace_id: selectedWorkspaceId,
          platform_account_id: selectedChannelId,
          youtube_video_id: videoId,
        }),
      });
      const data = (await resp.json()) as {
        session_id: string;
        velox_project_id: string;
        editor_url: string;
      };
      toast.success("Editor session created — opening Velox…");
      setManualVideoId("");
      // Reset to idle immediately so the form re-enables for the next
      // submission. The opened tab is the user's confirmation; we don't
      // gate further form interaction on it.
      setAction({ kind: "idle" });
      window.open(data.editor_url, "_blank", "noopener,noreferrer");
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
        await authedFetch(
          `/api/v1/youtube/editor-sessions/${sessionId}/thumbnail`,
          {
            method: "POST",
            body: JSON.stringify({ thumbnail_media_id: mediaId }),
          },
        );
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
        await authedFetch(
          `/api/v1/youtube/editor-sessions/${sessionId}/publish`,
          {
            method: "POST",
            body: JSON.stringify({
              privacy_status: "public",
            }),
          },
        );
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
      setAction({ kind: "publishing", sessionId });
      try {
        await authedFetch(
          `/api/v1/youtube/editor-sessions/${sessionId}/publish`,
          {
            method: "POST",
            body: JSON.stringify({
              privacy_status: "private",
              publish_at: publishAtDate.toISOString(),
            }),
          },
        );
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

function SessionRow({
  session,
  isActive,
  isPublishing,
  isExpanded,
  thumbnailMediaId,
  scheduleAt,
  onToggle,
  onThumbnailChange,
  onScheduleAtChange,
  onAttach,
  onPublishNow,
  onSchedule,
}: {
  session: EditorSession;
  isActive: boolean;
  isPublishing: boolean;
  isExpanded: boolean;
  thumbnailMediaId: string;
  scheduleAt: string;
  onToggle: () => void;
  onThumbnailChange: (v: string) => void;
  onScheduleAtChange: (v: string) => void;
  onAttach: () => void;
  onPublishNow: () => void;
  onSchedule: () => void;
}) {
  const hasThumbnail = !!session.thumbnail_media_id;
  const canAttach = !isActive && thumbnailMediaId.trim().length > 0;
  const canSchedule = !isPublishing && scheduleAt.length > 0;

  return (
    <article
      className="rounded-xl border border-white/[0.08] bg-white/[0.02] p-4 space-y-3"
      data-testid="yt-studio-session-row"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-[13px] font-mono text-white truncate">
            {session.youtube_video_id || "(unknown video)"}
          </p>
          <p className="text-[11px] text-[#9aa0aa] mt-0.5">
            status:{" "}
            <span className="font-semibold text-white">{session.status}</span>
            {" · "}
            desired privacy:{" "}
            <span className="font-semibold text-white">
              {session.desired_privacy}
            </span>
            {session.publish_at && (
              <>
                {" · "}
                publish_at:{" "}
                <span className="font-mono">{session.publish_at}</span>
              </>
            )}
          </p>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <a
            href={session.editor_url}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1 px-3 py-1.5 rounded-lg bg-white/[0.06] border border-white/[0.10] text-[12px] font-semibold text-white hover:bg-white/[0.10] transition-colors no-underline"
          >
            <ExternalLink size={12} aria-hidden="true" /> Velox
          </a>
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <span
          className={cn(
            "inline-flex items-center gap-1 px-2 py-1 rounded-md text-[11px] font-semibold border",
            hasThumbnail
              ? "border-emerald-500/30 bg-emerald-500/[0.10] text-emerald-300"
              : "border-amber-500/30 bg-amber-500/[0.10] text-amber-300",
          )}
        >
          <ImageIcon size={11} aria-hidden="true" />
          {hasThumbnail
            ? `thumbnail · ${session.thumbnail_media_id}`
            : "thumbnail: not set"}
        </span>
        <button
          type="button"
          onClick={onToggle}
          className="inline-flex items-center gap-1 px-3 py-1.5 rounded-lg bg-white/[0.06] border border-white/[0.10] text-[12px] font-semibold text-white hover:bg-white/[0.10] transition-colors"
          data-testid="yt-studio-attach-toggle"
        >
          {hasThumbnail ? "Replace thumbnail" : "Allega copertina"}
        </button>
        <button
          type="button"
          onClick={onPublishNow}
          disabled={isPublishing}
          className="inline-flex items-center gap-1 px-3 py-1.5 rounded-lg bg-emerald-500/[0.12] border border-emerald-500/30 text-[12px] font-semibold text-emerald-200 hover:bg-emerald-500/[0.18] transition-colors disabled:opacity-50"
          data-testid="yt-studio-publish-now"
        >
          {isPublishing ? (
            <Loader2 size={12} className="animate-spin" aria-hidden="true" />
          ) : (
            <Send size={12} aria-hidden="true" />
          )}
          Pubblica ora
        </button>
        <button
          type="button"
          onClick={onToggle}
          className="inline-flex items-center gap-1 px-3 py-1.5 rounded-lg bg-white/[0.06] border border-white/[0.10] text-[12px] font-semibold text-white hover:bg-white/[0.10] transition-colors"
          data-testid="yt-studio-schedule-toggle"
        >
          Programma pubblicazione
        </button>
      </div>

      {isExpanded && (
        <div className="space-y-3 pt-2 border-t border-white/[0.06]">
          <FormField
            id={`yt-studio-thumb-${session.id}`}
            label={hasThumbnail ? "Sostituisci thumbnail_media_id" : "thumbnail_media_id"}
            helpText="Paste the media asset ID returned by /api/v1/media presign+complete."
          >
            <input
              id={`yt-studio-thumb-${session.id}`}
              type="text"
              placeholder="media-asset-id-123"
              value={thumbnailMediaId}
              onChange={(e) => onThumbnailChange(e.target.value)}
              className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[13px] text-white placeholder:text-white/20 focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all"
              spellCheck={false}
              autoComplete="off"
              data-testid="yt-studio-thumbnail-input"
            />
          </FormField>
          <div className="flex items-center justify-end">
            <button
              type="button"
              onClick={onAttach}
              disabled={!canAttach}
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-lg bg-white text-black text-[12px] font-semibold hover:bg-white/90 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              data-testid="yt-studio-attach-submit"
            >
              {isActive ? (
                <Loader2 size={12} className="animate-spin" aria-hidden="true" />
              ) : (
                <ImageIcon size={12} aria-hidden="true" />
              )}
              {hasThumbnail ? "Sostituisci copertina" : "Allega copertina"}
            </button>
          </div>

          <div className="border-t border-white/[0.06] pt-3 space-y-3">
            <p className="text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider">
              Schedule publication
            </p>
            <FormField
              id={`yt-studio-publish-at-${session.id}`}
              label="Publish at"
              helpText="Video will be set to private and go public at this time."
            >
              <input
                id={`yt-studio-publish-at-${session.id}`}
                type="datetime-local"
                value={scheduleAt}
                onChange={(e) => onScheduleAtChange(e.target.value)}
                className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[13px] text-white focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all"
                data-testid="yt-studio-schedule-input"
              />
            </FormField>
            <div className="flex items-center justify-end">
              <button
                type="button"
                onClick={onSchedule}
                disabled={!canSchedule}
                className="inline-flex items-center gap-1.5 px-4 py-2 rounded-lg bg-blue-500/[0.18] border border-blue-500/30 text-[12px] font-semibold text-blue-100 hover:bg-blue-500/[0.26] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                data-testid="yt-studio-schedule-submit"
              >
                {isPublishing ? (
                  <Loader2 size={12} className="animate-spin" aria-hidden="true" />
                ) : (
                  <ArrowRight size={12} aria-hidden="true" />
                )}
                Programma
              </button>
            </div>
          </div>
        </div>
      )}
    </article>
  );
}

function StudioShell({ children }: { children: React.ReactNode }) {
  return (
    <div className="min-h-full p-8 bg-[#030308] text-[#e8e8ef]">
      <div className="max-w-3xl mx-auto space-y-6">
        <header className="mb-2">
          <p className="text-[12px] font-semibold uppercase tracking-[0.16em] text-[#9aa0aa] mb-2">
            / app / youtube / studio
          </p>
          <div className="flex items-center justify-between gap-4">
            <div>
              <h1 className="text-[28px] font-extrabold tracking-[-0.02em] text-white flex items-center gap-3">
                <span className="inline-flex w-10 h-10 rounded-xl bg-gradient-to-br from-red-500 via-pink-500 to-violet-500 items-center justify-center text-white shadow-[0_4px_16px_rgba(239,68,68,0.30)]">
                  <Video size={20} aria-hidden="true" />
                </span>
                YouTube Studio
              </h1>
              <p className="text-[15px] text-[#9aa0aa] mt-2 max-w-xl">
                Edit thumbnails of videos already on the channel and publish
                them when they're ready.
              </p>
            </div>
            <Link
              to="/app/uploads"
              className="hidden sm:inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors no-underline"
            >
              <ArrowLeft size={14} aria-hidden="true" /> Back to Imports
            </Link>
          </div>
        </header>
        {children}
      </div>
    </div>
  );
}

function FormField({
  id,
  label,
  helpText,
  error,
  children,
}: {
  id: string;
  label: string;
  helpText?: string;
  error?: string | null;
  children: React.ReactNode;
}) {
  return (
    <div>
      <label
        htmlFor={id}
        className="block text-[13px] font-semibold text-[#9aa0aa] mb-1.5"
      >
        {label}
      </label>
      {children}
      {helpText && !error && (
        <p className="mt-1.5 text-[12px] text-[#9aa0aa]/80">{helpText}</p>
      )}
      {error && (
        <p className="mt-1.5 text-[12px] text-red-400" role="status">
          {error}
        </p>
      )}
    </div>
  );
}

function FormSelect({
  id,
  label,
  value,
  onChange,
  placeholder,
  disabled,
  options,
}: {
  id: string;
  label: string;
  value: number | "";
  onChange: (v: number | "") => void;
  placeholder: string;
  disabled?: boolean;
  options: Array<{ value: number; label: string }>;
}) {
  return (
    <div>
      <label
        htmlFor={id}
        className="block text-[13px] font-semibold text-[#9aa0aa] mb-1.5"
      >
        {label}
      </label>
      <select
        id={id}
        value={value === "" ? "" : String(value)}
        disabled={disabled}
        onChange={(e) =>
          onChange(e.target.value === "" ? "" : Number(e.target.value))
        }
        className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all disabled:opacity-50"
      >
        <option value="" disabled className="bg-[#1f1f2e]">
          {placeholder}
        </option>
        {options.map((o) => (
          <option key={o.value} value={o.value} className="bg-[#1f1f2e]">
            {o.label}
          </option>
        ))}
      </select>
    </div>
  );
}