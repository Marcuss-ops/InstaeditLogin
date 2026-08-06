import { Loader2, RefreshCw } from "lucide-react";
import { cn } from "../../lib/utils";
import { STATUS_VISUAL, RETRIABLE_STATUSES } from "./contentPublishStatusVisual";
import type { PostTarget } from "../../features/publishing/api/types";
import { ProviderBadge } from "../../components/brand/PlatformLogos";

/**
 * TargetRow — one card per target with a status badge, optional
 * error message, attempt count, inline retry error, and a
 * "Riprova pubblicazione" button when the target is retriable
 * (failed / retrying / waiting_provider).
 */
export function TargetRow({
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
  const v = STATUS_VISUAL[target.status] ?? STATUS_VISUAL.failed;
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
          <ProviderBadge
            platform="youtube"
            className="h-6 rounded-md border-0"
            compact
            logoClassName="h-5 w-5"
            showName
          />
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
