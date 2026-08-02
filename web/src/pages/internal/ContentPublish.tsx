/**
 * ContentPublish — `/app/content/:postId/publish` status-asincrono.
 *
 * Mounts `usePostTargetStatus(postId)` (setInterval-driven poll of
 * /api/v1/posts/{postId}/targets every 3000ms) and surfaces the
 * per-target state machine. Renders:
 *
 *   1. A primary progress banner — describes the aggregate flow
 *      ("In coda → Pubblicazione su YouTube → Pubblicato").
 *   2. One card per target with a status badge, optional
 *      error message, and a "Riprova pubblicazione" button when
 *      the target is in a retriable state (failed /
 *      retrying / waiting_provider).
 *   3. A success card when every target is `published`: shows
 *      two CTAs (Visualizza nel canale + Apri su YouTube).
 *
 * Retry flow (owned by useContentPublishRetry):
 *   - Trigger: POST /api/v1/post-targets/{id}/retry (`retryPostTarget`)
 *   - On success: refetch() so the polling loop picks up the new
 *     state, clear retry error
 *   - On ApiError: surface the server message inline so the user
 *     knows the retry didn't take
 *   - On AuthError: navigate to /login (silent — authedFetch
 *     already swallowed the toast)
 *   - Force flag: passed automatically when status is
 *     partially_published or waiting_provider (server requires
 *     `?force=true` for non-failed states per openapi.yaml)
 *
 * State machine UI mapping (Italian labels per the wizard copy):
 *   queued           → "In coda"
 *   publishing       → "Pubblicazione su YouTube"
 *   published        → "Pubblicato" (final state)
 *   failed           → "Fallito" (retriable)
 *   retrying         → "Nuovo tentativo in corso" (retriable)
 *   waiting_provider → "In attesa del provider" (retriable w/ force)
 *   draft            → "Bozza"
 *   partially_published → "Parzialmente pubblicato" (retriable w/ force)
 *   dlq              → "Spostato in DLQ" (not retriable)
 *
 * Visibility on the published-success card:
 *   - "Visualizza nel canale" → /dashboard-channels/{accountId}?video={ytId}
 *     (spec URL; a redirect route /app/dashboard-channels/:accountId
 *     forwards to /app/accounts/:accountId so the link is live today).
 *   - "Apri su YouTube" → target.public_url when set, else the
 *     canonical watch URL constructed from external_id.
 */
import { useCallback, useEffect, useRef } from "react";
import { useParams } from "react-router-dom";
import { Tv } from "lucide-react";
import { Skeleton } from "../../components/feedback/Skeleton";
import { ErrorState } from "../../components/feedback/ErrorState";
import { usePostTargetStatus } from "../../features/publishing/hooks/usePostTargetStatus";
import { dispatchYouTubePublishChanged } from "../../features/channels/hooks/useYouTubePublishLiveUpdate";
import { RETRIABLE_STATUSES } from "./contentPublishStatusVisual";
import { useContentPublishRetry } from "./useContentPublishRetry";
import { AggregateBanner } from "./ContentPublishAggregateBanner";
import { TargetRow } from "./ContentPublishTargetRow";
import { SuccessCard } from "./ContentPublishSuccessCard";

// ─── Component ────────────────────────────────────────────────────────

export function ContentPublish() {
  const { postId } = useParams();
  const numericId = parsePostId(postId);

  const { targets, status, error, refetch } = usePostTargetStatus(numericId);

  // Stable refetch wrapper so the memoized handleRetry keeps a
  // stable identity across renders (avoids re-rendering every
  // TargetRow on each poll tick).
  const retried = useCallback(async () => {
    await refetch();
  }, [refetch]);
  const { retryingIds, retryErrorById, handleRetry } = useContentPublishRetry(retried);

  // Cross-tab DISPATCH on terminal+published. Fires ONCE per post
  // lifetime — the dispatchFiredRef flips on the first edge so a
  // subsequent refetch that re-confirms allPublished doesn't re-
  // broadcast (which would cause every subscriber to do a duplicate
  // refetch). For multi-target posts we emit one event per
  // published target keyed by exact account_id.
  const dispatchFiredRef = useRef(false);
  useEffect(() => {
    if (dispatchFiredRef.current) return;
    if (numericId == null) return;
    if (targets.length === 0) return;
    if (!targets.every((t) => t.status === "published")) return;
    const publishedTargets = targets.filter(
      (t) =>
        t.status === "published" &&
        typeof t.platform_account_id === "number" &&
        t.platform_account_id > 0,
    );
    if (publishedTargets.length === 0) return;
    for (const t of publishedTargets) {
      dispatchYouTubePublishChanged({
        type: "youtube-publish-changed",
        account_id: t.platform_account_id!,
        status: t.status,
      });
    }
    dispatchFiredRef.current = true;
  }, [numericId, targets]);

  // ── Render: invalid postId param fast-path ──
  if (numericId == null) {
    return (
      <div
        className="px-4 md:px-8 py-8 max-w-3xl mx-auto"
        data-testid="content-publish-error"
      >
        <ErrorState
          title="postId non valido"
          message={`L'URL contiene un postId che non è un numero intero valido: ${postId ?? "(vuoto)"}`}
        />
      </div>
    );
  }

  // ── Render: loading OR error with empty snapshot ──
  const showLoading = status === "loading" && targets.length === 0;
  const showErrorCard =
    status === "error" && targets.length === 0;

  if (showLoading) {
    return (
      <div className="px-4 md:px-8 py-8 max-w-3xl mx-auto" data-testid="content-publish-loading">
        <h1 className="text-2xl font-semibold text-white mb-6 flex items-center gap-2">
          <Tv aria-hidden="true" /> Stato pubblicazione
        </h1>
        <div className="space-y-3">
          <Skeleton variant="card" height={64} />
          <Skeleton variant="card" height={64} />
        </div>
      </div>
    );
  }

  if (showErrorCard && error) {
    return (
      <div className="px-4 md:px-8 py-8 max-w-3xl mx-auto" data-testid="content-publish-error">
        <h1 className="text-2xl font-semibold text-white mb-6 flex items-center gap-2">
          <Tv aria-hidden="true" /> Stato pubblicazione
        </h1>
        <ErrorState
          title="Impossibile leggere lo stato"
          message={error}
          onRetry={() => void refetch()}
          retryLabel="Riprova"
        />
      </div>
    );
  }

  // ── Render: normal flow with targets[] ──
  const allPublished =
    targets.length > 0 && targets.every((t) => t.status === "published");
  const anyFailed = targets.some((t) =>
    RETRIABLE_STATUSES.has(t.status),
  );

  return (
    <div
      className="px-4 md:px-8 py-8 max-w-3xl mx-auto"
      data-testid="content-publish-page"
    >
      <h1 className="text-2xl md:text-3xl font-bold text-white mb-1 flex items-center gap-2">
        <Tv aria-hidden="true" /> Stato pubblicazione
      </h1>
      <p className="text-sm text-[#9aa0aa] mb-8">
        Monitoriamo lo stato del target ogni pochi secondi. Le
        notifiche toast ti avvisano anche se navighi su un'altra
        pagina.
      </p>

      {/* Aggregate progress banner */}
      <AggregateBanner
        targets={targets}
        allPublished={allPublished}
        anyFailed={anyFailed}
      />

      {/* Per-target rows */}
      {targets.length > 0 && (
        <div className="space-y-3 mb-6">
          {targets.map((t) => (
            <TargetRow
              key={t.id}
              target={t}
              isRetrying={retryingIds.has(t.id)}
              retryError={retryErrorById[t.id] ?? null}
              onRetry={() => void handleRetry(t)}
            />
          ))}
        </div>
      )}

      {/* Success card — shown ONLY when every target is published */}
      {allPublished && (
        <SuccessCard targets={targets} />
      )}
    </div>
  );
}

// ─── Helpers ──────────────────────────────────────────────────────────

function parsePostId(raw: string | undefined): number | null {
  if (raw == null) return null;
  const id = Number.parseInt(raw, 10);
  if (!Number.isFinite(id) || id <= 0) return null;
  return id;
}
