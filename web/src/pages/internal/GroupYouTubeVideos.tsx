import { useCallback, useEffect, useRef, useState } from "react";
import {
  AlertTriangle,
  CheckCircle2,
  ExternalLink,
  Loader2,
  RefreshCw,
  Video,
} from "lucide-react";
import { Link, useNavigate } from "react-router-dom";
import { ApiError, authedFetch, AuthError } from "../../lib/auth";
import { EmptyState } from "../../components/feedback/EmptyState";
import { cn } from "../../lib/utils";

interface GroupYouTubeVideo {
  youtube_video_id: string;
  title: string;
  thumbnail_url?: string;
  privacy_status?: string;
  processing_status?: string;
  platform_account_id: number;
  channel_name?: string;
  editor_status?: string;
  desired_privacy?: string;
  publish_at?: string;
  actual_privacy?: string;
  youtube_sync_status?: string;
  phantom?: boolean;
}

interface GroupYouTubeVideosSummary {
  total_videos: number;
  truncated: boolean;
  accounts: number;
  accounts_with_videos: number;
  failed_accounts: number;
  invalid_token_accounts?: number[];
}

interface GroupYouTubeVideosResponse {
  videos?: GroupYouTubeVideo[];
  summary?: Partial<GroupYouTubeVideosSummary>;
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
      summary: GroupYouTubeVideosSummary;
      hasMore: boolean;
      nextOffset: number | null;
      isLoadingMore: boolean;
    }
  | { kind: "error"; message: string; upstream: boolean };

const DEFAULT_PAGE_SIZE = 50;
function normalizeSummary(summary?: Partial<GroupYouTubeVideosSummary>): GroupYouTubeVideosSummary {
  return {
    total_videos: summary?.total_videos ?? 0,
    truncated: summary?.truncated ?? false,
    accounts: summary?.accounts ?? 0,
    accounts_with_videos: summary?.accounts_with_videos ?? 0,
    failed_accounts: summary?.failed_accounts ?? 0,
    invalid_token_accounts: summary?.invalid_token_accounts ?? [],
  };
}

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

  const loadVideos = useCallback(
    async (signal: AbortSignal, offset = 0, append = false): Promise<void> => {
      try {
        const params = new URLSearchParams({
          include_subgroups: "true",
          limit: String(DEFAULT_PAGE_SIZE),
          offset: String(offset),
        });
        const response = await authedFetch(
          `/api/v1/groups/${groupId}/youtube/videos?${params.toString()}`,
          { signal },
        );
        if (signal.aborted) return;
        const data = (await response.json()) as GroupYouTubeVideosResponse;
        const videos = data.videos ?? [];
        const summary = normalizeSummary(data.summary);
        setState((previous) => {
          const previousVideos = append && previous.kind === "ready" ? previous.videos : [];
          return {
            kind: "ready",
            videos: [...previousVideos, ...videos],
            warnings: data.warnings ?? (append && previous.kind === "ready" ? previous.warnings : []),
            summary,
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
    [groupId, navigate],
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
            Video YouTube
          </h3>
          <p className="text-[12px] text-[#9aa0aa] mt-1">
            Qui vedi se il video è stato pubblicato davvero o se è ancora in verifica.
          </p>
        </div>
        <button
          type="button"
          onClick={() => refreshVideos()}
          className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.08] bg-white/[0.04] px-2.5 py-1.5 text-[11px] font-semibold text-[#cdd2da] hover:bg-white/[0.08] hover:text-white transition-colors"
          data-testid="group-youtube-videos-refresh"
        >
          <RefreshCw size={12} aria-hidden="true" />
          Aggiorna stato
        </button>
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

      {state.kind === "ready" && (
        <div className="mb-2 grid grid-cols-2 gap-2 sm:grid-cols-4" data-testid="group-youtube-summary">
          <div className="rounded-lg border border-white/[0.08] bg-white/[0.03] px-3 py-2">
            <p className="text-[10px] uppercase tracking-wide text-[#9aa0aa]">Video totali</p>
            <p className="text-[16px] font-bold text-white">{state.summary.total_videos}</p>
            {state.summary.truncated && <p className="text-[10px] text-amber-200/80">Limite raggiunto</p>}
          </div>
          <div className="rounded-lg border border-white/[0.08] bg-white/[0.03] px-3 py-2">
            <p className="text-[10px] uppercase tracking-wide text-[#9aa0aa]">Canali</p>
            <p className="text-[16px] font-bold text-white">{state.summary.accounts}</p>
          </div>
          <div className="rounded-lg border border-white/[0.08] bg-white/[0.03] px-3 py-2">
            <p className="text-[10px] uppercase tracking-wide text-[#9aa0aa]">Con video</p>
            <p className="text-[16px] font-bold text-white">{state.summary.accounts_with_videos}</p>
          </div>
          <div className="rounded-lg border border-amber-500/15 bg-amber-500/[0.04] px-3 py-2">
            <p className="text-[10px] uppercase tracking-wide text-amber-200/70">Da verificare</p>
            <p className="text-[16px] font-bold text-amber-200">{state.summary.failed_accounts}</p>
          </div>
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
          title="Nessun video trovato nel gruppo"
          description="Il gruppo contiene account, ma YouTube non ha restituito video visualizzabili. Questo non conferma né esclude una pubblicazione: controlla la pagina del canale o il link della conferma di pubblicazione."
          icon={<Video size={28} />}
          className="p-6 bg-white/[0.02] border-white/[0.08]"
        />
      )}

      {state.kind === "ready" && state.videos.length > 0 && (
        <div className="space-y-2">
          {state.videos.map((video) => {
            const publication = publicationState(video);
            const watchUrl = `https://www.youtube.com/watch?v=${encodeURIComponent(video.youtube_video_id)}`;
            const channelUrl = `/app/dashboard-channels/${video.platform_account_id}?video=${encodeURIComponent(video.youtube_video_id)}`;
            return (
              <article
                key={`${video.platform_account_id}:${video.youtube_video_id}`}
                className="flex flex-col gap-3 rounded-xl border border-white/[0.08] bg-white/[0.03] p-3 sm:flex-row sm:items-center"
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
                    {video.publish_at && new Date(video.publish_at).getTime() > Date.now() && (
                      <span className="rounded-full border border-blue-500/20 bg-blue-500/[0.06] px-2 py-0.5 text-blue-200">
                        Diventa pubblico il {new Date(video.publish_at).toLocaleString("it-IT")}
                      </span>
                    )}
                    {video.phantom && <span className="text-[#9aa0aa]">stato letto dalla pubblicazione</span>}
                  </div>
                </div>
                <div className="flex shrink-0 flex-wrap items-center gap-2 sm:flex-col sm:items-end">
                  <a href={watchUrl} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 text-[11px] font-semibold text-blue-300 hover:text-blue-200 no-underline">
                    Apri su YouTube <ExternalLink size={12} aria-hidden="true" />
                  </a>
                  <Link to={channelUrl} className="text-[11px] font-semibold text-[#cdd2da] underline-offset-2 hover:text-white hover:underline">
                    Apri nel canale
                  </Link>
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
    </section>
  );
}
