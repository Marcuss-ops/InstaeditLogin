
/**
 * DashboardChannelsPage — single-channel page at /app/dashboard-channels/:accountId.
 *
 * Composes the channel header, filters, content list, and editor
 * entrypoint into the single-account channel page:
 *
 *   • <ChannelHeader>       — avatar/banner/handle/status/refresh/back
 *   • <ChannelVideoFilters> — Tutti/Privati/Non in elenco/Pubblici
 *   • <ChannelVideoCard>    — thumbnail + chips + Apri su YouTube +
 *                              Modifica copertina + ?video= highlight
 *   • useChannelAccount     — GET /accounts/{id} ⇒ state machine
 *   • useChannelContent     — GET /accounts/{id}/content(privacy…)
 *                              ⇒ state machine + loadMore
 *   • createEditorSessionAndOpen — canonical "Modifica copertina"
 *                                 entrypoint (opens Velox in a
 *                                 new tab)
 *
 * State:
 *   • `privacy` and `?video=` are URL-driven via useSearchParams
 *     (matches ChannelsPerformance / Calendar / ScheduledByAccount /
 *     Groups precedent — share-able URLs, back/forward works,
 *     refresh persists).
 *   • Initial privacy = "all" per spec (NOT "private" — starting on
 *     Privati makes a video disappear after a privacy bump to
 *     public, looking like a bug).
 *   • `?video=` is preserved across chip switches. If the highlight
 *     row is filtered out, the card just lacks the ring/badges
 *     until the user picks a chip that includes it.
 *
 * Concerns:
 *   • NO fetch anywhere in this page — both listers are hooks.
 *   • "Modifica copertina" uses an inline /api/v1/workspaces fetch
 *     (same precedent as AccountDetailsPage). A future iteration
 *     can consolidate this into a useWorkspaces hook; for the
 *     skeleton it's fine.
 *   • AuthError re-thrown by both hooks is caught by the
 *     ProtectedRoute wrapper at the route level — the page itself
 *     does NOT redirect on auth errors.
 *   • Analytics are intentionally rendered on the dedicated
 *     performance page rather than this content-focused surface.
 */

import { useCallback, useRef, useState } from "react";
import { useParams, useNavigate, useSearchParams } from "react-router-dom";
import { Loader2 } from "lucide-react";
import { authedFetch, AuthError } from "../../lib/auth";
import { useToast } from "../../components/toast";
import { ErrorState } from "../../components/feedback";
import { EmptyState } from "../../components/feedback/EmptyState";
import { Skeleton } from "../../components/feedback/Skeleton";
import { ChannelHeader } from "../../features/channels/components/ChannelHeader";
import { ChannelVideoFilters } from "../../features/channels/components/ChannelVideoFilters";
import { ChannelVideoCard } from "../../features/channels/components/ChannelVideoCard";
import { useChannelAccount } from "../../features/channels/hooks/useChannelAccount";
import { useChannelContent } from "../../features/channels/hooks/useChannelContent";
import { useChannelContentLiveUpdate } from "../../features/channels/hooks/useYouTubePublishLiveUpdate";
import type { ChannelVideo, PrivacyFilter } from "../../features/channels/types";
import { createEditorSessionAndOpen } from "../../features/youtube/api/editorSessionsApi";

const DEFAULT_LIMIT = 20;

const VALID_PRIVACY: ReadonlySet<PrivacyFilter> = new Set([
  "all",
  "private",
  "unlisted",
  "public",
]);

function parsePrivacyParam(raw: string | null): PrivacyFilter {
  // Unknown / missing ⇒ "all". The chip row accepts only the 4
  // canonical values so we never silently render an unknown state.
  if (raw && VALID_PRIVACY.has(raw as PrivacyFilter)) {
    return raw as PrivacyFilter;
  }
  return "all";
}

export function DashboardChannelsPage() {
  const { accountId: accountIdRaw } = useParams<{ accountId: string }>();
  const accountIdNum = accountIdRaw ? Number(accountIdRaw) : NaN;
  const accountId =
    Number.isFinite(accountIdNum) && accountIdNum > 0 ? accountIdNum : null;

  const navigate = useNavigate();
  const toast = useToast();
  const [searchParams, setSearchParams] = useSearchParams();
  const privacy = parsePrivacyParam(searchParams.get("privacy"));
  const highlightVideoId = searchParams.get("video") ?? undefined;

  const [refreshing, setRefreshing] = useState(false);
  const inflightEditRef = useRef(false);

  const accountState = useChannelAccount({ accountId });
  const contentState = useChannelContent({
    accountId,
    privacy,
    limit: DEFAULT_LIMIT,
    // Refresh on window focus events so closing the Velox popup
    // + refocusing the channel tab auto-invalidates whatever
    // changed server-side (the BC listener may not have echoed
    // because BroadcastChannel does NOT fire on the sender's tab).
    refetchOnWindowFocus: true,
    // Poll every 5s ONLY while any video is processing or
    // publishing — the steady-state case is a no-op. The
    // predicate is re-evaluated on every state transition so a
    // 'processing' video that drops off the list (rare; usually
    // the API bumps it to live / failed) immediately stops
    // polling without a manual unmount cycle.
    refetchInterval: (state) =>
      state.kind === "ready" &&
      state.items.some(
        (v) => v.status === "processing" || v.status === "publishing",
      )
        ? 5_000
        : null,
  });

  // Cross-tab invalidation: when ANY tab publishes a YouTube
  // change for this account, refetch BOTH the header (OAuth
  // status, avatar, etc.) AND the video list (new thumbnail,
  // new privacy, removed/added row). useCallback deps stay on
  // the .refetch fns (stable across renders thanks to empty
  // deps inside each hook) so the registry add/remove in
  // useChannelContentLiveUpdate does NOT churn on every render.
  const accountRefetch = accountState.refetch;
  const contentRefetch = contentState.refetch;
  const handlePublishChanged = useCallback(() => {
    void Promise.all([accountRefetch(), contentRefetch()]);
  }, [accountRefetch, contentRefetch]);
  useChannelContentLiveUpdate(accountId, handlePublishChanged);

  const handleRefreshBoth = useCallback(async (): Promise<void> => {
    setRefreshing(true);
    try {
      await Promise.all([
        accountState.refetch(),
        contentState.refetch(),
      ]);
    } finally {
      setRefreshing(false);
    }
  }, [accountState, contentState]);

  // Chip clicks use replace:true so users hitting Back from the
  // channel page go to the previous page (e.g. /app/linking) rather
  // than scrubbing through every filter they tried.
  const handlePrivacyChange = useCallback(
    (next: PrivacyFilter) => {
      setSearchParams(
        (prev) => {
          const sp = new URLSearchParams(prev);
          if (next === "all") {
            sp.delete("privacy");
          } else {
            sp.set("privacy", next);
          }
          return sp;
        },
        { replace: true },
      );
    },
    [setSearchParams],
  );

  const handleEditThumbnail = useCallback(
    async (video: ChannelVideo): Promise<void> => {
      if (accountId == null) return;
      if (inflightEditRef.current) {
        // The editor-sessions POST creates a brand-new session row
        // server-side each time. A double-click before the window
        // opens would produce a stale-open and an orphaned session.
        // Skip and let the first click finish.
        return;
      }
      inflightEditRef.current = true;
      try {
        const wsResp = await authedFetch("/api/v1/workspaces");
        const { workspaces } = (await wsResp.json()) as {
          workspaces: { id: number }[];
        };
        if (!workspaces.length) {
          toast.error(
            "Nessun workspace trovato. Creane uno prima di modificare la copertina.",
          );
          return;
        }
        await createEditorSessionAndOpen({
          workspace_id: workspaces[0]!.id,
          platform_account_id: accountId,
          youtube_video_id: video.external_id,
        });
        toast.success("Sessione editor creata — apertura Velox…");
      } catch (err) {
        if (err instanceof AuthError) {
          // Router handles the redirect at the AuthError boundary.
          return;
        }
        toast.error(
          err instanceof Error
            ? err.message
            : "Impossibile aprire l'editor per questo video.",
        );
      } finally {
        inflightEditRef.current = false;
      }
    },
    [accountId, toast],
  );

  // Invalid / missing accountId: bail out with an error card so the
  // page doesn't pretend to render a non-existent channel.
  if (accountId == null) {
    return (
      <div className="min-h-full p-8 bg-[#030308] text-[#e8e8ef]">
        <div className="max-w-5xl mx-auto">
          <ErrorState
            title="ID canale non valido"
            message="L'URL non contiene un accountId numerico valido."
            className="bg-[#1f1f2e] border-white/[0.12]"
          />
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-full p-8 bg-[#030308] text-[#e8e8ef]">
      <div className="max-w-5xl mx-auto">
        <ChannelHeader
          account={
            accountState.state.kind === "ready"
              ? accountState.state.account
              : undefined
          }
          refreshing={refreshing}
          onRefresh={() => void handleRefreshBoth()}
          onBack={() => navigate("/app/linking")}
        />

        {/* Filters — initial = "all" per spec */}
        <ChannelVideoFilters
          value={privacy}
          onChange={handlePrivacyChange}
          disabled={
            contentState.state.kind === "loading" ||
            contentState.state.kind === "error"
          }
        />

        <ContentGrid
          state={contentState.state}
          cacheBust={contentState.cacheBust}
          highlightVideoId={highlightVideoId}
          onEditThumbnail={handleEditThumbnail}
          onLoadMore={() => contentState.loadMore()}
          onRetry={() => contentState.refetch()}
        />
      </div>
    </div>
  );
}

interface ContentGridProps {
  state: ReturnType<typeof useChannelContent>["state"];
  /**
   * Cache-bust key from `useChannelContent().cacheBust`. Bumped
   * by the hook on every successful fetch (refresh / append /
   * loadMore). Forwarded into the card so YouTube CDN thumbnail
   * URLs invalidate on every successful server response.
   */
  cacheBust: number;
  highlightVideoId?: string;
  onEditThumbnail: (video: ChannelVideo) => Promise<void>;
  onLoadMore: () => Promise<void>;
  onRetry: () => Promise<void>;
}

function ContentGrid({
  state,
  cacheBust,
  highlightVideoId,
  onEditThumbnail,
  onLoadMore,
  onRetry,
}: ContentGridProps) {
  if (state.kind === "loading") {
    return (
      <div className="space-y-3" data-testid="content-grid-loading">
        {Array.from({ length: 5 }).map((_, i) => (
          <Skeleton key={i} variant="card" height={96} />
        ))}
      </div>
    );
  }

  if (state.kind === "error") {
    return (
      <ErrorState
        title="Impossibile caricare i video del canale"
        message={state.message}
        onRetry={onRetry}
        className="bg-[#1f1f2e] border-white/[0.12]"
      />
    );
  }

  if (state.items.length === 0) {
    return (
      <EmptyState
        title="Nessun video trovato"
        description="Il canale non ha video corrispondenti al filtro corrente."
        className="bg-[#1f1f2e] border-white/[0.08]"
      />
    );
  }

  return (
    <div className="space-y-3" data-testid="content-grid">
      {state.items.map((video) => (
        <ChannelVideoCard
          key={video.external_id}
          video={video}
          highlightVideoId={highlightVideoId}
          cacheBust={cacheBust}
          onEditThumbnail={(v) => void onEditThumbnail(v)}
        />
      ))}

      {state.nextCursor && !state.isLoadingMore && !state.loadMoreError && (
        <button
          type="button"
          onClick={() => void onLoadMore()}
          className="w-full py-3 text-[13px] font-semibold text-[#9aa0aa] hover:text-white transition-colors"
          data-testid="load-more"
        >
          Mostra altri
        </button>
      )}

      {state.isLoadingMore && (
        <div
          className="flex items-center justify-center gap-2 py-3 text-[13px] text-[#9aa0aa]"
          data-testid="load-more-spinner"
        >
          <Loader2 size={14} className="animate-spin" aria-hidden="true" />
          Caricamento…
        </div>
      )}

      {state.loadMoreError && state.nextCursor && (
        <div className="flex flex-col items-center gap-2 py-3">
          <p className="text-[13px] text-red-400">{state.loadMoreError}</p>
          <button
            type="button"
            onClick={() => void onLoadMore()}
            className="text-[13px] font-semibold text-[#9aa0aa] hover:text-white transition-colors"
            data-testid="load-more-retry"
          >
            Riprova
          </button>
        </div>
      )}
    </div>
  );
}
