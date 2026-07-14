import { AlertCircle, RefreshCw } from "lucide-react";
import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

/**
 * ErrorState \u2014 shared fallback when a screen can't load its data.
 *
 * Each page wires its own `onRetry` (typically the page's `loadAll` /
 * `loadAccounts` / mutation `refetch()`). The component is opinionated
 * about visual structure: icon + title + message + (optional) help
 * text + (optional) retry button. Appearance matches the inline
 * error blocks we used to author per-page.
 */
export type ErrorStateProps = {
  title?: string;
  message: string;
  helpText?: ReactNode;
  onRetry?: () => void;
  retryLabel?: string;
  className?: string;
};

export function ErrorState({
  title = "Couldn't load",
  message,
  helpText,
  onRetry,
  retryLabel = "Retry",
  className,
}: ErrorStateProps) {
  return (
    <div
      role="alert"
      className={cn(
        "bg-white border border-neutral-200 rounded-xl p-8 text-center",
        className,
      )}
      data-testid="error-state"
    >
      <AlertCircle size={28} className="mx-auto mb-3 text-red-400" aria-hidden="true" />
      <p className="text-red-500 font-semibold text-[15px] mb-1">{title}</p>
      <p className="text-[14px] text-neutral-500 mb-5 break-words">{message}</p>
      {helpText && (
        <p className="text-[12px] text-neutral-400 mb-5 break-words">{helpText}</p>
      )}
      {onRetry && (
        <button
          type="button"
          onClick={onRetry}
          className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-black text-white text-[14px] font-semibold hover:bg-neutral-800 transition-colors"
          data-testid="error-state-retry"
        >
          <RefreshCw size={14} />
          {retryLabel}
        </button>
      )}
    </div>
  );
}
