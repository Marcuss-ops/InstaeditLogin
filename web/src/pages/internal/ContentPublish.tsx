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
 *      partially_published / waiting_provider).
 *   3. A success card when every target is `published`: shows
 *      two CTAs (Visualizza nel canale + Apri su YouTube).
 *
 * Retry flow:
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
import { useEffect, useRef, useState } from "react";
import { Link, useParams, useNavigate } from "react-router-dom";
import {
  AlertCircle,
  AlertTriangle,
  ArchiveX,
  CheckCircle2,
  Clock,
  ExternalLink,
  FileText,
  Loader2,
  RefreshCw,
  Tv,
  XCircle,
} from "lucide-react";
import { cn } from "../../lib/utils";
import { ApiError, AuthError } from "../../lib/auth";
import { Skeleton } from "../../components/feedback/Skeleton";
import { ErrorState } from "../../components/feedback/ErrorState";
import { retryPostTarget } from "../../features/publishing/api/postTargetsApi";
import { usePostTargetStatus } from "../../features/publishing/hooks/usePostTargetStatus";
import type { PostStatus, PostTarget } from "../../features/publishing/api/types";
import { dispatchYouTubePublishChanged } from "../../features/channels/hooks/useYouTubePublishLiveUpdate";

// ─── Status badge mapping (single source of truth) ────────────────────

interface StatusVisual {
  label: string;
  /** Tailwind color tokens applied to bg/text/border. */
  bg: string;
  text: string;
  border: string;
  /**
   * Permissive on-purpose: `React.ElementType` accepts the full
   * `LucideIcon` runtime shape (which has a much wider prop surface
   * than `size`/`aria-hidden`/`className`). Locking it down further
   * would just regress to TS2769 — verify by attempting
   * `React.ComponentType<{size?: number; aria-hidden?: React.AriaAttributes["aria-hidden"]; className?: string}>`.
   */
  Icon: React.ElementType;
  /** Whether the row is "still in motion" → spinner instead of static icon. */
  inMotion: boolean;
}

const STATUS_VISUAL: Record<PostStatus, StatusVisual> = {
  queued: {
    label: "In coda",
    bg: "bg-slate-500/[0.08]",
    text: "text-slate-200",
    border: "border-slate-500/30",
    Icon: Clock,
    inMotion: false,
  },
  publishing: {
    label: "Pubblicazione su YouTube",
    bg: "bg-blue-500/[0.08]",
    text: "text-blue-200",
    border: "border-blue-500/30",
    Icon: Loader2,
    inMotion: true,
  },
  published: {
    label: "Pubblicato",
    bg: "bg-emerald-500/[0.08]",
    text: "text-emerald-200",
    border: "border-emerald-500/30",
    Icon: CheckCircle2,
    inMotion: false,
  },
  failed: {
    label: "Fallito",
    bg: "bg-red-500/[0.08]",
    text: "text-red-200",
    border: "border-red-500/30",
    Icon: XCircle,
    inMotion: false,
  },
  retrying: {
    label: "Nuovo tentativo in corso",
    bg: "bg-amber-500/[0.08]",
    text: "text-amber-200",
    border: "border-amber-500/30",
    Icon: RefreshCw,
    inMotion: true,
  },
  waiting_provider: {
    label: "In attesa del provider",
    bg: "bg-yellow-500/[0.08]",
    text: "text-yellow-200",
    border: "border-yellow-500/30",
    Icon: Clock,
    inMotion: false,
  },
  partially_published: {
    label: "Parzialmente pubblicato",
    bg: "bg-yellow-500/[0.08]",
    text: "text-yellow-200",
    border: "border-yellow-500/30",
    Icon: AlertTriangle,
    inMotion: false,
  },
  draft: {
    label: "Bozza",
    bg: "bg-slate-500/[0.08]",
    text: "text-slate-200",
    border: "border-slate-500/30",
    Icon: FileText,
    inMotion: false,
  },
  dlq: {
    label: "Spostato in DLQ",
    bg: "bg-zinc-500/[0.08]",
    text: "text-zinc-300",
    border: "border-zinc-500/30",
    Icon: ArchiveX,
    inMotion: false,
  },
};

/**
 * Targets in these states can be re-armed via the retry endpoint.
 * Strict `{ failed, retrying, waiting_provider }` per user spec.
 * `partially_published` is intentionally NOT retriable here: it
 * surfaces as its own badge in `STATUS_VISUAL` without a recovery
 * button — the user spec doesn't list it in the retryable states.
 *
 * `force: true` required by the server for `waiting_provider` only;
 * `failed` / `retrying` accept an unforced retry per openapi.yaml
 * § /post-targets/{id}/retry.
 */
const RETRIABLE_STATUSES = new Set<PostStatus>([
  "failed",
  "retrying",
  "waiting_provider",
]);

function forceFlagFor(_status: PostStatus): boolean {
  // Only `waiting_provider` is in the retriable set; the parameter
  // underscore marks intentional non-use. If the retriable set grows
  // to include other non-failed terminal states, extend this switch.
  return _status === "waiting_provider";
}

// ─── Component ────────────────────────────────────────────────────────

export function ContentPublish() {
  const { postId } = useParams();
  const navigate = useNavigate();
  const numericId = parsePostId(postId);

  const { targets, status, error, refetch } = usePostTargetStatus(numericId);

  // Per-failed-target retry state. We key by target.id so multiple
  // failed targets can each show their own retry button + spinner.
  const [retryingId, setRetryingId] = useState<number | null>(null);
  const [retryErrorById, setRetryErrorById] = useState<Record<number, string>>({});

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

  const handleRetry = async (target: PostTarget): Promise<void> => {
    setRetryingId(target.id);
    setRetryErrorById((prev) => {
      const next = { ...prev };
      delete next[target.id];
      return next;
    });
    try {
      await retryPostTarget(target.id, { force: forceFlagFor(target.status) });
      await refetch();
    } catch (err) {
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      setRetryErrorById((prev) => ({
        ...prev,
        [target.id]:
          err instanceof ApiError
            ? err.message
            : err instanceof Error
              ? err.message
              : "Riprova non riuscito.",
      }));
    } finally {
      setRetryingId(null);
    }
  };

  // ── Render: invalid postId param fast-path ──
  if (numericId == null) {
    return (
      <div className="px-4 md:px-8 py-8 max-w-3xl mx-auto">
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
              isRetrying={retryingId === t.id}
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

// ─── Sub-components ───────────────────────────────────────────────────

function AggregateBanner({
  targets,
  allPublished,
  anyFailed,
}: {
  targets: PostTarget[];
  allPublished: boolean;
  anyFailed: boolean;
}) {
  if (targets.length === 0) {
    return (
      <div
        className="mb-6 rounded-2xl border border-blue-500/30 bg-blue-500/[0.06] px-5 py-4 flex items-center gap-3"
        data-testid="aggregate-banner-polling"
      >
        <Loader2
          size={20}
          className="text-blue-200 animate-spin"
          aria-hidden="true"
        />
        <div className="flex-1 min-w-0">
          <div className="text-sm font-medium text-blue-100">
            In coda…
          </div>
          <p className="text-xs text-[#9aa0aa] mt-0.5">
            Stiamo recuperando lo stato dei target dal worker.
          </p>
        </div>
      </div>
    );
  }
  if (allPublished) {
    return null; // Success card takes over.
  }
  if (anyFailed) {
    return (
      <div
        className="mb-6 rounded-2xl border border-red-500/30 bg-red-500/[0.06] px-5 py-4 flex items-center gap-3"
        data-testid="aggregate-banner-failed"
      >
        <AlertCircle
          size={20}
          className="text-red-300 shrink-0"
          aria-hidden="true"
        />
        <div className="flex-1 min-w-0">
          <div className="text-sm font-medium text-red-100">
            Pubblicazione non riuscita
          </div>
          <p className="text-xs text-[#9aa0aa] mt-0.5">
            Una o più fasi hanno avuto esito negativo. Puoi riprovare
            la pubblicazione dal pannello qui sotto.
          </p>
        </div>
      </div>
    );
  }
  return (
    <div
      className="mb-6 rounded-2xl border border-blue-500/30 bg-blue-500/[0.06] px-5 py-4 flex items-center gap-3"
      data-testid="aggregate-banner-publishing"
    >
      <Loader2
        size={20}
        className="text-blue-200 animate-spin"
        aria-hidden="true"
      />
      <div className="flex-1 min-w-0">
        <div className="text-sm font-medium text-blue-100">
          In coda → Pubblicazione su YouTube
        </div>
        <p className="text-xs text-[#9aa0aa] mt-0.5">
          Il worker sta processando i target. La pagina si
          aggiorna automaticamente.
        </p>
      </div>
    </div>
  );
}

function TargetRow({
  target,
  isRetrying,
  retryError,
  onRetry,
}: {
  target: PostTarget;
  isRetrying: boolean;
  retryError: string | null;
  onRetry: () => void;
}) {
  const v = STATUS_VISUAL[target.status];
  const isRetriable = RETRIABLE_STATUSES.has(target.status);
  const Icon = v.Icon;
  return (
    <div
      className={cn(
        "rounded-xl border px-5 py-4 flex items-start gap-4",
        v.bg,
        v.border,
      )}
      data-testid={`target-row-${target.id}`}
    >
      {v.inMotion ? (
        <Loader2
          size={20}
          className={cn(v.text, "animate-spin shrink-0 mt-0.5")}
          aria-hidden="true"
        />
      ) : (
        <Icon
          size={20}
          className={cn(v.text, "shrink-0 mt-0.5")}
          aria-hidden="true"
        />
      )}
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2 flex-wrap">
          <span
            className={cn("text-sm font-semibold", v.text)}
            data-testid={`target-status-${target.id}`}
          >
            {v.label}
          </span>
          <span className="text-xs text-[#5c6473] font-mono">
            target_id={target.id}
          </span>
        </div>
        {target.error_message && (
          <p
            className="mt-1 text-xs text-[#cdd2da] break-words"
            data-testid={`target-error-${target.id}`}
          >
            {target.error_message}
          </p>
        )}
        {target.attempt_count != null && target.attempt_count > 0 && (
          <p className="mt-0.5 text-xs text-[#5c6473]">
            Tentativi: {target.attempt_count}
          </p>
        )}
        {retryError && (
          <p
            className="mt-1 text-xs text-red-300"
            role="alert"
            data-testid={`retry-error-${target.id}`}
          >
            {retryError}
          </p>
        )}
      </div>
      {isRetriable && (
        <button
          type="button"
          onClick={onRetry}
          disabled={isRetrying}
          className="inline-flex items-center gap-1.5 text-sm font-medium text-white bg-white/10 hover:bg-white/20 border border-white/15 rounded-lg px-3 py-1.5 disabled:opacity-50 disabled:cursor-not-allowed transition-colors shrink-0"
          data-testid={`retry-button-${target.id}`}
        >
          {isRetrying ? (
            <Loader2 size={14} className="animate-spin" aria-hidden="true" />
          ) : (
            <RefreshCw size={14} aria-hidden="true" />
          )}
          {isRetrying ? "Riprovando…" : "Riprova pubblicazione"}
        </button>
      )}
    </div>
  );
}

function SuccessCard({ targets }: { targets: PostTarget[] }) {
  return (
    <div
      className="rounded-2xl border border-emerald-500/30 bg-emerald-500/[0.06] p-6 md:p-8"
      data-testid="success-card"
    >
      <div className="flex items-center gap-3 mb-4">
        <CheckCircle2
          size={28}
          className="text-emerald-300 shrink-0"
          aria-hidden="true"
        />
        <div className="text-lg font-semibold text-emerald-100">
          Video caricato correttamente
        </div>
      </div>

      {/* Multi-target posts: one row per published target so each
          channel's video_id is visible. The aggregate headline stays
          at the top because the parent gate is
          `every(t.status === "published")`. */}
      <div className="space-y-3">
        {targets.map((t) => (
          <SuccessTargetRow key={t.id} target={t} />
        ))}
      </div>
    </div>
  );
}

function SuccessTargetRow({ target }: { target: PostTarget }) {
  const videoId = target.external_id ?? null;
  const fallbackUrl = videoId
    ? `https://www.youtube.com/watch?v=${encodeURIComponent(videoId)}`
    : null;
  const ytUrl = target.public_url ?? fallbackUrl;
  const channelUrl = videoId
    ? `/app/dashboard-channels/${target.platform_account_id}?video=${encodeURIComponent(videoId)}`
    : `/app/dashboard-channels/${target.platform_account_id}`;
  return (
    <div
      className="rounded-xl border border-emerald-500/20 bg-emerald-500/[0.04] px-4 py-3"
      data-testid={`success-row-${target.id}`}
    >
      <div className="text-xs text-[#9aa0aa] font-mono break-all mb-2">
        target_id={target.id} · video_id={videoId ?? "(sconosciuto)"}
      </div>
      <div className="flex flex-col sm:flex-row gap-2">
        <Link
          to={channelUrl}
          className="inline-flex items-center justify-center gap-2 rounded-lg bg-white text-[#030308] px-3 py-1.5 text-sm font-semibold hover:bg-[#e8ecf2] transition-colors"
          data-testid={`view-channel-button-${target.id}`}
        >
          <Tv size={14} aria-hidden="true" />
          Visualizza nel canale
        </Link>
        {ytUrl && (
          <a
            href={ytUrl}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center justify-center gap-2 rounded-lg border border-white/15 bg-white/[0.06] hover:bg-white/[0.12] text-white px-3 py-1.5 text-sm font-medium transition-colors"
            data-testid={`open-youtube-button-${target.id}`}
          >
            <ExternalLink size={14} aria-hidden="true" />
            Apri su YouTube
          </a>
        )}
      </div>
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

