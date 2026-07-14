import { useEffect, useSyncExternalStore } from "react";
import { AlertTriangle, CheckCircle2, Info, X, XCircle } from "lucide-react";
import { toastBus } from "./toast-bus";
import type { ToastEntry, ToastKind } from "./types";

/**
 * Per-variant visual + ARIA config.
 *
 *   - `ariaRole`: "status" lets screen readers announce when the user
 *     is idle; "alert" interrupts and is used only for `error`. The
 *     region's `aria-live="polite"` is overridden for the alert
 *     variant via the inline `aria-live` on the alert toast itself,
 *     so ATs that respect the region will go assertive automatically.
 *   - Colors: distinct enough to read at a glance (green/red/blue/
 *     amber), and they match the existing toast palette used in
 *     bespoke per-page toasts (Login.tsx, Compose.tsx) so the visual
 *     language is consistent across the app.
 */
const VARIANTS: Record<
  ToastKind,
  {
    Icon: typeof CheckCircle2;
    ring: string;
    bg: string;
    text: string;
    iconColor: string;
    ariaRole: "status" | "alert";
  }
> = {
  success: {
    Icon: CheckCircle2,
    ring: "border-green-300",
    bg: "bg-green-50",
    text: "text-green-900",
    iconColor: "text-green-600",
    ariaRole: "status",
  },
  error: {
    Icon: XCircle,
    ring: "border-red-300",
    bg: "bg-red-50",
    text: "text-red-900",
    iconColor: "text-red-600",
    ariaRole: "alert",
  },
  info: {
    Icon: Info,
    ring: "border-blue-300",
    bg: "bg-blue-50",
    text: "text-blue-900",
    iconColor: "text-blue-600",
    ariaRole: "status",
  },
  warning: {
    Icon: AlertTriangle,
    ring: "border-amber-300",
    bg: "bg-amber-50",
    text: "text-amber-900",
    iconColor: "text-amber-600",
    ariaRole: "status",
  },
};

/**
 * ToastViewport — fixed-position top-right queue subscribed to the
 * bus via `useSyncExternalStore`. Renders nothing when the queue is
 * empty so it doesn't occupy DOM nodes.
 */
export function ToastViewport() {
  const toasts = useSyncExternalStore(
    toastBus.subscribe,
    toastBus.getSnapshot,
    toastBus.getSnapshot,
  );

  // Escape dismisses ALL toasts. Simpler than a "dismiss topmost"
  // heuristic and matches how users expect keyboard navigation to
  // clear a small notification stack.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape" && toastBus.getSnapshot().length > 0) {
        toastBus.dismissAll();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  if (toasts.length === 0) return null;

  // Why `pointer-events-none` on the outer region and
  // `pointer-events-auto` on each card: the region container has no
  // visible bounds AT a particular spot; only the cards within it do.
  // `pointer-events-none` lets clicks fall through the empty parts of
  // the viewport back to the page below; each card re-enables pointer
  // events so its dismiss button is clickable. This is the same
  // pattern used by react-aria's `<ToastRegion/>`. Don't refactor to
  // `pointer-events: auto` on the outer container — it would silently
  // eat clicks on whatever page content happens to sit under the
  // empty parts of the toast viewport.
  return (
    <div
      className="fixed top-4 right-4 z-50 flex flex-col gap-2 max-w-sm pointer-events-none"
      role="region"
      aria-label="Notifications"
      aria-live="polite"
      data-testid="toast-viewport"
    >
      {toasts.map((toast) => (
        <ToastCard key={toast.id} toast={toast} />
      ))}
    </div>
  );
}

function ToastCard({ toast }: { toast: ToastEntry }) {
  const variant = VARIANTS[toast.kind];
  const { Icon } = variant;
  // Error toasts put `aria-live="assertive"` inline so ATs that read
  // the toast element (not just the region) announce it as an
  // interruption regardless of whether they walked up to the region.
  return (
    <div
      role={variant.ariaRole}
      aria-live={variant.ariaRole === "alert" ? "assertive" : "polite"}
      data-testid={`toast-${toast.kind}`}
      data-toast-id={toast.id}
      className={`pointer-events-auto flex items-start gap-3 rounded-xl border ${variant.ring} ${variant.bg} px-4 py-3 shadow-[0_8px_24px_rgba(0,0,0,0.08)] text-[13px] ${variant.text}`}
    >
      <Icon
        size={18}
        className={`shrink-0 mt-0.5 ${variant.iconColor}`}
        aria-hidden="true"
      />
      <span className="flex-1 leading-[1.45]">{toast.message}</span>
      <button
        type="button"
        onClick={() => toastBus.dismiss(toast.id)}
        className="shrink-0 text-current opacity-60 hover:opacity-100 transition-opacity -mr-1 -mt-0.5"
        aria-label="Dismiss notification"
        data-testid={`toast-dismiss-${toast.kind}`}
      >
        <X size={14} aria-hidden="true" />
      </button>
    </div>
  );
}
