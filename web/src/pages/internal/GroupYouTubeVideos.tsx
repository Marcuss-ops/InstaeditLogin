import { useCallback, useEffect, useRef, useState } from "react";
import {
  AlertTriangle,
  CheckCircle2,
  Edit3,
  ExternalLink,
  Loader2,
  RefreshCw,
  Video,
} from "lucide-react";
import { useNavigate } from "react-router-dom";
import { ApiError, authedFetch, AuthError } from "../../lib/auth";
import { EmptyState } from "../../components/feedback/EmptyState";
import { useToast } from "../../components/toast";
import {
  createEditorSessionAndOpen,
  createYouTubeEditorSession,
  generateYouTubeMetadata,
  openEditorInNewTab,
  type CreateYouTubeEditorSessionResponse,
  type GeneratedYouTubeMetadata,
} from "../../features/youtube/api/editorSessionsApi";
import { cn } from "../../lib/utils";

interface GroupYouTubeVideo {
  youtube_video_id: string;
  title: string;
  thumbnail_url?: string;
  privacy_status?: string;
  processing_status?: string;
  platform_account_id: number;
  channel_name?: string;
  language?: string;
  editor_status?: string;
  desired_privacy?: string;
  publish_at?: string;
  actual_privacy?: string;
  youtube_sync_status?: string;
  phantom?: boolean;
}

type VideoPreview = {
  video: GroupYouTubeVideo;
  metadata: GeneratedYouTubeMetadata | null;
  session: CreateYouTubeEditorSessionResponse | null;
  loading: boolean;
  error: string | null;
};

interface GroupYouTubeVideosResponse {
  videos?: GroupYouTubeVideo[];
  warnings?: string[];
  has_more?: boolean;
  next_offset?: number;
}

type LoadState =
  | { kind: "loading" }
  | {
      kind: "ready";
      videos: GroupYouTubeVideo[];
      warnings: string[];
      hasMore: boolean;
      nextOffset: number | null;
      isLoadingMore: boolean;
    }
  | { kind: "error"; message: string; upstream: boolean };

const DEFAULT_PAGE_SIZE = 50;
const RECENCY_OPTIONS = [7, 14, 28, 90] as const;

function privacyLabel(value?: string): string {
  switch (value) {
    case "public":
      return "Pubblico";
    case "unlisted":
      return "Non in elenco";
    case "private":
      return "Privato";
    default:
      return "Non rilevato";
  }
}

function publicationState(video: GroupYouTubeVideo): {
  label: string;
  tone: "success" | "warning" | "info" | "neutral";
} {
  const scheduledAt = video.publish_at ? new Date(video.publish_at) : null;
  const isFutureSchedule = scheduledAt != null && scheduledAt.getTime() > Date.now();
  if (video.youtube_sync_status === "pending") {
    return { label: "Pubblicazione inviata · verifica in corso", tone: "info" };
  }
  if (video.youtube_sync_status === "failed") {
    return { label: "Pubblicazione non confermata", tone: "warning" };
  }
  if (video.youtube_sync_status === "drift") {
    return { label: "Pubblicato · privacy da verificare", tone: "warning" };
  }
  if (isFutureSchedule && video.actual_privacy === "private") {
    return {
      label: "Programmazione completata · resta privato fino all'orario scelto",
      tone: "info",
    };
  }
  if (video.editor_status === "published" && video.youtube_sync_status !== "confirmed") {
    return { label: "Pubblicazione registrata · verifica YouTube", tone: "info" };
  }
  if (video.youtube_sync_status === "confirmed" && video.editor_status === "published") {
    return { label: "Pubblicato su YouTube", tone: "success" };
  }
  if (
    video.youtube_sync_status === "confirmed" &&
    (video.actual_privacy === "public" || video.privacy_status === "public")
  ) {
    return { label: "Pubblico su YouTube", tone: "success" };
  }
  if (
    video.youtube_sync_status !== "confirmed" &&
    (video.actual_privacy === "public" || video.privacy_status === "public")
  ) {
    return { label: "Visibilità rilevata · sincronizzazione non confermata", tone: "info" };
  }
  if (video.actual_privacy === "private" || video.privacy_status === "private") {
    return {
      label:
        video.desired_privacy === "private"
          ? "Privato su YouTube"
          : "Privato · possibile programmazione",
      tone: "neutral",
    };
  }
  if (["publishing", "processing", "queued"].includes(video.processing_status ?? "")) {
    return { label: "Elaborazione in corso", tone: "info" };
  }
  return { label: "Non ancora pubblicato", tone: "neutral" };
}

const toneClasses = {
  success: "border-emerald-500/25 bg-emerald-500/[0.08] text-emerald-300",
  warning: "border-amber-500/25 bg-amber-500/[0.08] text-amber-300",
  info: "border-blue-500/25 bg-blue-500/[0.08] text-blue-300",
  neutral: "border-white/[0.12] bg-white/[0.04] text-[#cdd2da]",
} as const;

export function GroupYouTubeVideos({ groupId }: { groupId: number }) {
  const navigate = useNavigate();
  const abortRef = useRef<AbortController | null>(null);
  const pollingAttemptsRef = useRef(0);
  const [state, setState] = useState<LoadState>({ kind: "loading" });
  const [recencyDays, setRecencyDays] = useState<number>(90);
  const [openingVideoID, setOpeningVideoID] = useState<string | null>(null);
  const [preview, setPreview] = useState<VideoPreview | null>(null);
  const toast = useToast();

  const openThumbnailEditor = useCallback(async (video: GroupYouTubeVideo) => {
    if (openingVideoID) return;
    setOpeningVideoID(video.youtube_video_id);
    try {
      // The group is the source of truth for the channel/workspace binding.
      // Using the first global workspace can point the editor at a valid but
      // unrelated workspace and produces "account not linked to workspace".
      const groupResponse = await authedFetch(`/api/v1/groups/${groupId}`);
      const groupData = (await groupResponse.json()) as { workspace_id?: number };
      const workspaceID = groupData.workspace_id;
      if (!workspaceID) throw new Error("Il gruppo non ha un workspace valido.");
      await createEditorSessionAndOpen({
        workspace_id: workspaceID,
        platform_account_id: video.platform_account_id,
        youtube_video_id: video.youtube_video_id,
        ...(video.thumbnail_url ? { source_thumbnail_url: video.thumbnail_url } : {}),
      });
      toast.success("Dark Editor aperto: il video resta privato finché non scegli di pubblicarlo.");
    } catch (error) {
      if (error instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      toast.error(error instanceof Error ? error.message : "Impossibile aprire il Dark Editor.");
    } finally {
      setOpeningVideoID(null);
    }
  }, [groupId, navigate, openingVideoID, toast]);

  const openVideoPreview = useCallback(async (video: GroupYouTubeVideo) => {
    setPreview({ video, metadata: null, session: null, loading: true, error: null });
    try {
      const groupResponse = await authedFetch(`/api/v1/groups/${groupId}`);
      const groupData = (await groupResponse.json()) as { workspace_id?: number };
      if (!groupData.workspace_id) throw new Error("Il gruppo non ha un workspace valido.");
      const session = await createYouTubeEditorSession({
        workspace_id: groupData.workspace_id,
        platform_account_id: video.platform_account_id,
        youtube_video_id: video.youtube_video_id,
        // Keep the preview/session source identical to the thumbnail
        // rendered in this list. The editor-session endpoint also
        // canonicalises this against YouTube and repairs stale rows.
        ...(video.thumbnail_url ? { source_thumbnail_url: video.thumbnail_url } : {}),
      });
      const language = video.language?.trim() || "en";
      const prompt = [
        "Genera metadata YouTube per una preview editoriale.",
        `Titolo originale: ${video.title || "senza titolo"}`,
        `Canale: ${video.channel_name || "YouTube"}`,
        `Video ID: ${video.youtube_video_id}`,
        `Lingua target principale: ${language}`,
        "Mantieni il significato del titolo e restituisci traduzioni naturali e concise.",
      ].join("\n");
      const metadata = await generateYouTubeMetadata(session.velox_project_id, prompt);
      setPreview((current) => current ? { ...current, session, metadata, loading: false } : current);
    } catch (error) {
      if (error instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      setPreview((current) => current ? {
        ...current,
        loading: false,
        error: error instanceof Error ? error.message : "Impossibile generare la preview NVIDIA.",
      } : current);
    }
  }, [groupId, navigate]);

  const loadVideos = useCallback(
    async (signal: AbortSignal, offset = 0, append = false): Promise<void> => {
      try {
        const params = new URLSearchParams({
          include_subgroups: "true",
          limit: String(DEFAULT_PAGE_SIZE),
          offset: String(offset),
          days: String(recencyDays),
        });
        const response = await authedFetch(
          `/api/v1/groups/${groupId}/youtube/videos?${params.toString()}`,
          { signal },
        );
        if (signal.aborted) return;
        const data = (await response.json()) as GroupYouTubeVideosResponse;
        const videos = (data.videos ?? []).filter((video) => {
          const privacy = String(video.actual_privacy ?? video.privacy_status ?? "").toLowerCase();
          return privacy === "private" && video.phantom !== true;
        });
        setState((previous) => {
          const previousVideos = append && previous.kind === "ready" ? previous.videos : [];
          return {
            kind: "ready",
            videos: [...previousVideos, ...videos],
            warnings: data.warnings ?? (append && previous.kind === "ready" ? previous.warnings : []),
            hasMore: data.has_more === true,
            nextOffset: data.has_more === true && data.next_offset != null ? data.next_offset : null,
            isLoadingMore: false,
          };
        });
      } catch (error) {
        if (signal.aborted) return;
        if (error instanceof AuthError) {
          navigate("/login", { replace: true });
          return;
        }
        const upstream = error instanceof ApiError && error.status === 502;
        setState({
          kind: "error",
          upstream,
          message: upstream
            ? "YouTube non risponde temporaneamente. Riprova tra poco."
            : error instanceof Error
              ? error.message
              : "Impossibile caricare i video YouTube.",
        });
      }
    },
    [groupId, navigate, recencyDays],
  );

  const refreshVideos = useCallback(
    (resetPolling = true): void => {
      if (resetPolling) pollingAttemptsRef.current = 0;
      abortRef.current?.abort();
      const controller = new AbortController();
      abortRef.current = controller;
      if (resetPolling) {
        setState({ kind: "loading" });
      }
      void loadVideos(controller.signal);
    },
    [loadVideos],
  );

  const loadMoreVideos = useCallback((): void => {
    if (state.kind !== "ready" || !state.hasMore || state.nextOffset == null || state.isLoadingMore) {
      return;
    }
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    setState((previous) =>
      previous.kind === "ready" ? { ...previous, isLoadingMore: true } : previous,
    );
    void loadVideos(controller.signal, state.nextOffset, true);
  }, [loadVideos, state]);

  useEffect(() => {
    refreshVideos();
    return () => abortRef.current?.abort();
  }, [refreshVideos]);

  const hasPendingVideos =
    state.kind === "ready" &&
    state.videos.some((video) => video.youtube_sync_status === "pending");

  useEffect(() => {
    if (!hasPendingVideos) return;
    if (pollingAttemptsRef.current >= 12) return;
    const interval = window.setInterval(() => {
      if (pollingAttemptsRef.current >= 12) {
        window.clearInterval(interval);
        return;
      }
      pollingAttemptsRef.current += 1;
      refreshVideos(false);
    }, 10_000);
    return () => window.clearInterval(interval);
  }, [hasPendingVideos, refreshVideos]);

  return (
    <section className="mb-6" data-testid="group-youtube-videos">
      <div className="flex items-center justify-between gap-3 mb-2">
        <div>
          <h3 className="text-[11px] font-bold uppercase tracking-wider text-[#9aa0aa]">
            Video privati da pubblicare
          </h3>
          <p className="text-[12px] text-[#9aa0aa] mt-1">
            Solo video privati recenti dei canali presenti nel gruppo.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <label className="text-[10px] text-[#9aa0aa]" htmlFor="group-video-recency">Periodo</label>
          <select
            id="group-video-recency"
            value={recencyDays}
            onChange={(event) => {
              setRecencyDays(Number(event.target.value));
            }}
            className="rounded-lg border border-white/[0.08] bg-white/[0.04] px-2 py-1.5 text-[11px] font-semibold text-[#cdd2da]"
            data-testid="group-youtube-videos-recency"
          >
            {RECENCY_OPTIONS.map((days) => <option key={days} value={days}>{days} giorni</option>)}
          </select>
          <button
            type="button"
            onClick={() => refreshVideos()}
            className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.08] bg-white/[0.04] px-2.5 py-1.5 text-[11px] font-semibold text-[#cdd2da] hover:bg-white/[0.08] hover:text-white transition-colors"
            data-testid="group-youtube-videos-refresh"
          >
            <RefreshCw size={12} aria-hidden="true" />
            Aggiorna
          </button>
        </div>
      </div>

      {state.kind === "loading" && (
        <div className="flex items-center gap-2 rounded-xl border border-white/[0.08] bg-white/[0.03] px-4 py-5 text-[12px] text-[#9aa0aa]">
          <Loader2 size={15} className="animate-spin" aria-hidden="true" />
          Caricamento stato video…
        </div>
      )}

      {state.kind === "error" && (
        <div
          className="rounded-xl border border-amber-500/25 bg-amber-500/[0.06] px-4 py-4 text-[12px] text-amber-200"
          role="alert"
          data-testid={state.upstream ? "group-youtube-upstream-error" : undefined}
        >
          {state.message}
        </div>
      )}

      {state.kind === "ready" && state.warnings.length > 0 && (
        <div className="mb-2 flex items-start gap-2 rounded-lg border border-amber-500/20 bg-amber-500/[0.05] px-3 py-2 text-[11px] text-amber-200" role="status">
          <AlertTriangle size={14} className="mt-0.5 shrink-0" aria-hidden="true" />
          <span>Alcuni canali non sono stati verificati: {state.warnings.join(" · ")}</span>
        </div>
      )}

      {state.kind === "ready" && state.videos.length === 0 && (
        <EmptyState
          title="Nessun video privato recente"
          description="Non ci sono video privati nei giorni selezionati. Prova ad ampliare il periodo a 90 giorni; i video pubblici e non in elenco non vengono mostrati."
          icon={<Video size={28} />}
          className="p-6 bg-white/[0.02] border-white/[0.08]"
        />
      )}

      {state.kind === "ready" && state.videos.length > 0 && (
        <div className="space-y-2">
          {state.videos.map((video) => {
            const publication = publicationState(video);
            const watchUrl = `https://www.youtube.com/watch?v=${encodeURIComponent(video.youtube_video_id)}`;
            return (
              <article
                key={`${video.platform_account_id}:${video.youtube_video_id}`}
                className="flex cursor-pointer flex-col gap-3 rounded-xl border border-white/[0.08] bg-white/[0.03] p-3 transition-colors hover:border-violet-400/30 hover:bg-white/[0.05] sm:flex-row sm:items-center"
                onClick={() => void openVideoPreview(video)}
                onKeyDown={(event) => {
                  if (event.key === "Enter" || event.key === " ") {
                    event.preventDefault();
                    void openVideoPreview(video);
                  }
                }}
                role="button"
                tabIndex={0}
                data-testid="group-youtube-video"
              >
                <div className="h-16 w-28 shrink-0 overflow-hidden rounded-lg bg-white/[0.08]">
                  {video.thumbnail_url ? (
                    <img src={video.thumbnail_url} alt="" className="h-full w-full object-cover" />
                  ) : (
                    <div className="flex h-full items-center justify-center text-white/20">
                      <Video size={20} aria-hidden="true" />
                    </div>
                  )}
                </div>
                <div className="min-w-0 flex-1">
                  <p className="truncate text-[13px] font-semibold text-white">
                    {video.title || "Video senza titolo"}
                  </p>
                  <p className="mt-0.5 truncate font-mono text-[10px] text-[#9aa0aa]">
                    {video.channel_name || `Account #${video.platform_account_id}`} · {video.youtube_video_id}
                  </p>
                  <div className="mt-2 flex flex-wrap items-center gap-2 text-[10px]">
                    <span className={cn("inline-flex items-center gap-1 rounded-full border px-2 py-0.5 font-semibold", toneClasses[publication.tone])}>
                      {publication.tone === "success" && <CheckCircle2 size={11} aria-hidden="true" />}
                      {publication.label}
                    </span>
                    <span className="rounded-full border border-white/[0.10] bg-white/[0.04] px-2 py-0.5 text-[#cdd2da]">
                      Privacy: {privacyLabel(video.actual_privacy ?? video.privacy_status)}
                    </span>
                    {video.language?.trim() ? (
                      <span className="rounded-full border border-violet-500/20 bg-violet-500/[0.06] px-2 py-0.5 text-violet-200">
                        Lingua: {video.language.trim().toUpperCase()}
                      </span>
                    ) : null}
                    {video.publish_at && new Date(video.publish_at).getTime() > Date.now() && (
                      <span className="rounded-full border border-blue-500/20 bg-blue-500/[0.06] px-2 py-0.5 text-blue-200">
                        Diventa pubblico il {new Date(video.publish_at).toLocaleString("it-IT")}
                      </span>
                    )}
                    {video.phantom && <span className="text-[#9aa0aa]">stato letto dalla pubblicazione</span>}
                  </div>
                </div>
                <div className="flex shrink-0 flex-wrap items-center gap-2 sm:flex-col sm:items-end">
                  <a href={watchUrl} target="_blank" rel="noopener noreferrer" onClick={(event) => event.stopPropagation()} className="inline-flex items-center gap-1 text-[11px] font-semibold text-blue-300 hover:text-blue-200 no-underline">
                    Apri su YouTube <ExternalLink size={12} aria-hidden="true" />
                  </a>
                  <button
                    type="button"
                    onClick={(event) => { event.stopPropagation(); void openThumbnailEditor(video); }}
                    disabled={openingVideoID !== null}
                    className="inline-flex items-center gap-1 text-[11px] font-semibold text-violet-300 underline-offset-2 hover:text-violet-200 hover:underline disabled:cursor-wait disabled:opacity-60"
                  >
                    <Edit3 size={12} aria-hidden="true" />
                    {openingVideoID === video.youtube_video_id ? "Apertura…" : "Modifica copertina"}
                  </button>
                </div>
              </article>
            );
          })}
          {state.hasMore && (
            <button
              type="button"
              onClick={loadMoreVideos}
              disabled={state.isLoadingMore}
              className="mt-2 inline-flex w-full items-center justify-center gap-2 rounded-xl border border-white/[0.10] bg-white/[0.04] px-4 py-2.5 text-[12px] font-semibold text-[#cdd2da] hover:bg-white/[0.08] hover:text-white disabled:opacity-50"
              data-testid="group-youtube-videos-load-more"
            >
              {state.isLoadingMore ? <Loader2 size={14} className="animate-spin" /> : null}
              {state.isLoadingMore ? "Caricamento…" : "Carica altri video"}
            </button>
          )}
        </div>
      )}

      {preview && (() => {
        const language = preview.video.language?.trim().toLowerCase() || "en";
        const localized = preview.metadata?.translations?.[language];
        const title = localized?.title || preview.metadata?.title || preview.video.title || "Video senza titolo";
        const description = localized?.description || preview.metadata?.description || "";
        return (
          <div
            className="fixed inset-0 z-50 flex items-center justify-center bg-black/75 p-4 backdrop-blur-sm"
            role="presentation"
            onMouseDown={(event) => { if (event.target === event.currentTarget) setPreview(null); }}
          >
            <div role="dialog" aria-modal="true" aria-labelledby="youtube-video-preview-title" className="max-h-[92vh] w-full max-w-3xl overflow-y-auto rounded-2xl border border-white/[0.12] bg-[#11131a] p-5 shadow-2xl">
              <div className="mb-4 flex items-start justify-between gap-4">
                <div>
                  <p className="text-[10px] font-bold uppercase tracking-widest text-violet-300">Preview localizzata · NVIDIA AI</p>
                  <h4 id="youtube-video-preview-title" className="mt-1 text-lg font-bold text-white">{preview.video.channel_name || "Video YouTube"}</h4>
                  <p className="mt-1 text-[11px] text-[#9aa0aa]">Lingua target: {language.toUpperCase()} · {preview.video.youtube_video_id}</p>
                </div>
                <button type="button" onClick={() => setPreview(null)} className="rounded-lg px-2 py-1 text-xl text-[#9aa0aa] hover:bg-white/[0.08] hover:text-white" aria-label="Chiudi preview">×</button>
              </div>

              <div className="grid gap-5 md:grid-cols-[1.15fr_1fr]">
                <div>
                  <div className="relative aspect-video overflow-hidden rounded-xl border border-white/[0.10] bg-black">
                    {preview.video.thumbnail_url ? <img src={preview.video.thumbnail_url} alt="Thumbnail attuale" className="h-full w-full object-cover" /> : null}
                    <div className="absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/90 via-black/55 to-transparent px-4 pb-4 pt-12">
                      <p className="line-clamp-2 text-center text-lg font-black leading-tight text-white drop-shadow-[0_2px_3px_rgba(0,0,0,0.9)]">{preview.loading ? "Generazione preview…" : title}</p>
                    </div>
                  </div>
                  <p className="mt-2 text-[10px] text-[#7f8591]">Preview grafica: la thumbnail originale resta invariata finché non pubblichi.</p>
                </div>

                <div className="space-y-4">
                  {preview.loading ? <div className="rounded-xl border border-white/[0.08] bg-white/[0.03] p-4 text-sm text-[#cdd2da]">NVIDIA sta preparando titolo, descrizione e traduzione…</div> : null}
                  {preview.error ? <div className="rounded-xl border border-amber-500/25 bg-amber-500/[0.06] p-4 text-sm text-amber-200">{preview.error}</div> : null}
                  {!preview.loading && !preview.error ? <>
                    <div>
                      <p className="mb-1 text-[10px] font-bold uppercase tracking-wider text-[#9aa0aa]">Titolo tradotto</p>
                      <p className="rounded-xl border border-white/[0.08] bg-white/[0.03] p-3 text-sm font-semibold text-white">{title}</p>
                    </div>
                    <div>
                      <p className="mb-1 text-[10px] font-bold uppercase tracking-wider text-[#9aa0aa]">Descrizione tradotta</p>
                      <p className="max-h-48 overflow-y-auto whitespace-pre-wrap rounded-xl border border-white/[0.08] bg-white/[0.03] p-3 text-xs leading-relaxed text-[#cdd2da]">{description || "Nessuna descrizione generata."}</p>
                    </div>
                  </> : null}
                </div>
              </div>

              <div className="mt-5 flex flex-wrap justify-end gap-2 border-t border-white/[0.08] pt-4">
                <button type="button" onClick={() => setPreview(null)} className="rounded-lg border border-white/[0.10] px-3 py-2 text-xs font-semibold text-[#cdd2da] hover:bg-white/[0.08]">Chiudi</button>
                {preview.session ? <button type="button" onClick={() => openEditorInNewTab(preview.session!.editor_url)} className="rounded-lg bg-violet-500 px-3 py-2 text-xs font-bold text-white hover:bg-violet-400">Apri editor copertina</button> : null}
              </div>
            </div>
          </div>
        );
      })()}
    </section>
  );
}
