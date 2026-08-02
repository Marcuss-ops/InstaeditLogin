/**
 * ContentPublish status visuals — single source of truth for the
 * per-status badge mapping and the retryable-state set.
 *
 * Split out of ContentPublish.tsx (pattern: YouTubeStudio → separate
 * types/mapping module). The page renders `TargetRow` per target using
 * STATUS_VISUAL; `ContentPublish` gates the retry button via
 * RETRIABLE_STATUSES.
 */
import type * as React from "react";
import {
  AlertTriangle,
  ArchiveX,
  CheckCircle2,
  Clock,
  FileText,
  Loader2,
  RefreshCw,
  XCircle,
} from "lucide-react";
import type { PostStatus } from "../../features/publishing/api/types";

export interface StatusVisual {
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

export const STATUS_VISUAL: Record<PostStatus, StatusVisual> = {
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
export const RETRIABLE_STATUSES = new Set<PostStatus>([
  "failed",
  "retrying",
  "waiting_provider",
]);
