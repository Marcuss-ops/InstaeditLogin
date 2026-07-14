import { DEMO_MODE } from "../lib/demo";

/**
 * Persistent amber strip pinned to the top of the viewport when
 * `VITE_DEMO_MODE=true`. Communicates to the user (and to anyone
 * looking at a screenshot) that the page is running against
 * mock data, not a live API. Survives route changes because it
 * lives in `App.tsx` above `<BrowserRouter>`.
 *
 * The banner is intentionally non-interactive — no close button
 * (the SPA always runs against mock data while DEMO_MODE is on,
 * and there is no "production mode" toggle in the UI; switching
 * requires a Vercel env-var change + redeploy).
 */
export function DemoBanner() {
  if (!DEMO_MODE) return null;
  return (
    <div
      role="status"
      aria-live="polite"
      data-testid="demo-banner"
      className="bg-amber-50 border-b border-amber-200 px-4 py-2 text-center text-amber-800 text-[12px] font-medium"
    >
      <span className="font-semibold tracking-wide uppercase mr-1.5">
        Demo mode
      </span>
      <span className="text-amber-700">
        Backend not connected — every page is showing mock data with a
        demo account. Deploy the Go API and remove{" "}
        <code className="font-mono text-[11px] bg-amber-100/70 border border-amber-200/70 rounded px-1 py-0.5">
          VITE_DEMO_MODE
        </code>{" "}
        from Vercel to go live.
      </span>
    </div>
  );
}
