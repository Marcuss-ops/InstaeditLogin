import { AlertTriangle } from "lucide-react";
import { Component, type ReactNode } from "react";

/**
 * ErrorBoundary \u2014 top-level catch-all for uncaught render/network errors.
 *
 *   \u2022 getDerivedStateFromError flips `hasError` so the next render
 *     returns the fallback page instead of the children.
 *   \u2022 componentDidCatch logs the error and the component stack to
 *     `console.error`. (Future: forward to Sentry once
 *     `SENTRY_DSN` is provisioned; the backend already mid-stacks Send.)
 *   \u2022 "Try again" resets the boundary state AND remounts the
 *     subtree via a `key` bump so any stale state in descendants is
 *     discarded. Mirrors what react-error-boundary does internally.
 *   \u2022 "Reload the page" hard-reloads via `window.location.reload()`.
 */
type Props = {
  children: ReactNode;
};

type State = {
  hasError: boolean;
  error: Error | null;
  resetKey: number;
};

export class ErrorBoundary extends Component<Props, State> {
  state: State = { hasError: false, error: null, resetKey: 0 };

  static getDerivedStateFromError(error: unknown): Partial<State> {
    return {
      hasError: true,
      error: error instanceof Error ? error : new Error(String(error)),
    };
  }

  componentDidCatch(error: Error, info: { componentStack?: string }): void {
    // eslint-disable-next-line no-console -- intentional diagnostic log;
    // Sentry will replace this once SENTRY_DSN is wired.
    console.error("ErrorBoundary caught:", error, info?.componentStack);
  }

  private handleReset = (): void => {
    this.setState((prev) => ({
      hasError: false,
      error: null,
      resetKey: prev.resetKey + 1,
    }));
  };

  private handleReload = (): void => {
    if (typeof window !== "undefined") window.location.reload();
  };

  render(): ReactNode {
    if (!this.state.hasError) {
      // Wrap children in a keyed div so a reset forces a remount.
      return (
        <div key={this.state.resetKey} data-testid="error-boundary-children">
          {this.props.children}
        </div>
      );
    }

    const message = this.state.error?.message ?? "An unexpected error occurred.";

    return (
      <div
        role="alert"
        className="min-h-screen bg-neutral-50 flex flex-col items-center justify-center p-6"
        data-testid="error-boundary-fallback"
      >
        <div className="w-16 h-16 rounded-2xl bg-red-100 border border-red-200 flex items-center justify-center mb-6 shadow-sm">
          <AlertTriangle size={28} className="text-red-500" aria-hidden="true" />
        </div>
        <h1 className="text-[clamp(24px,4vw,32px)] font-extrabold tracking-[-0.02em] text-black mb-2 text-center">
          Something went wrong
        </h1>
        <p className="text-neutral-500 text-[14px] text-center max-w-[420px] mb-4">
          An error occurred while loading this page. You can try again
          without reloading, or hard-reload the whole app.
        </p>
        <p
          className="text-neutral-400 font-mono text-[11px] text-center max-w-[420px] mb-6 break-all"
          data-testid="error-boundary-message"
        >
          {message}
        </p>
        <div className="flex flex-wrap items-center justify-center gap-2">
          <button
            type="button"
            onClick={this.handleReset}
            className="inline-flex items-center gap-2 px-4 py-2.5 rounded-xl bg-black text-white text-[14px] font-semibold hover:bg-neutral-800 transition-colors"
            data-testid="error-boundary-reset"
          >
            Try again
          </button>
          <button
            type="button"
            onClick={this.handleReload}
            className="inline-flex items-center gap-2 px-4 py-2.5 rounded-xl bg-neutral-100 hover:bg-neutral-200 text-[14px] font-semibold text-neutral-800 transition-colors"
          >
            Reload the page
          </button>
        </div>
      </div>
    );
  }
}
