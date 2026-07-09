import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { AlertTriangle, ArrowRight, CheckCircle2, Loader2, Shield } from "lucide-react";
import { API_BASE_URL } from "../lib/supabase";
import { PROVIDERS } from "../lib/providers";
import { probeBackend, subscribeToVisibility, type ProbeResult } from "../lib/auth";
import { clearProbeCache, readProbeCache, writeProbeCache } from "../lib/probe-cache";
import { useLongPress } from "../lib/use-long-press";
import { bannerCopy, shortHost, statusLabel } from "../lib/probe-display";

type ProbeState =
  | { kind: "loading" }
  | { kind: "ready"; result: ProbeResult };

export function Login() {
  // Hydrate the initial state from the sessionStorage cache (TTL 5min,
  // see web/src/lib/probe-cache.ts) so a user bouncing between / and
  // /login doesn't see a "Checking…" flash on every mount, and we
  // avoid hitting /api/v1/health for cache-fresh tabs.
  const [probe, setProbe] = useState<ProbeState>(() => {
    const cached = readProbeCache();
    return cached !== null
      ? { kind: "ready", result: cached.backend }
      : { kind: "loading" };
  });
  // Tracks the last known health so we can avoid spamming /api/v1/health
  // on every tab-switch when the backend is already confirmed healthy.
  // Seeded from the cache so a hydrated "ready/healthy" tab is treated
  // like one that just probed and succeeded — the tab-focus re-probe
  // (subscribeToVisibility) correctly stays quiet until the user
  // explicitly retries or the cache is older than TTL.
  const lastHealthyRef = useRef<boolean>(readProbeCache()?.backend.ok === true);

  const runProbe = useCallback(async () => {
    setProbe({ kind: "loading" });
    const result = await probeBackend();
    lastHealthyRef.current = result.ok;
    // Only cache successful probes — a failure must never wipe the
    // last known-good snapshot (see probe-cache.ts invariants).
    if (result.ok) {
      writeProbeCache({ lastOkAt: Date.now(), backend: result });
    }
    setProbe({ kind: "ready", result });
  }, []);

  // Dev affordance: right-click or long-press the status pill to bypass
  // the 5-minute probe cache TTL and force an immediate fresh probe.
  // Clears the sessionStorage entry first so the new result, whatever
  // it is, becomes the new "last known good".
  const forceReprobe = useCallback(() => {
    clearProbeCache();
    void runProbe();
  }, [runProbe]);

  useEffect(() => {
    // Skip the initial /health call when the cache hydrated us. The
    // effect re-runs once probe completes (probe.kind: loading→ready)
    // and just re-wires the visibility listener — no extra probe.
    if (probe.kind === "loading") {
      void runProbe();
    }
    // Self-healing: re-probe after a tab regains focus, but only when the
    // last known result was degraded. This avoids hitting /health on every
    // alt-tab while a healthy tab is also fine.
    return subscribeToVisibility(() => {
      if (lastHealthyRef.current) {
        return;
      }
      void runProbe();
    });
  }, [runProbe, probe.kind]);

  // Derive failureCopy next to degraded rather than referring to it, so
  // TypeScript can narrow `probe.result` to its failure variant in the
  // SAME expression (bannerCopy's parameter type is `Extract<ProbeResult,
  // { ok: false }>` and would not accept the union). Behaviorally
  // identical: failureCopy is non-null exactly when `degraded` is true.
  const failureCopy =
    probe.kind === "ready" && !probe.result.ok
      ? bannerCopy(probe.result)
      : null;
  const degraded = failureCopy !== null;

  return (
    <div className="min-h-screen bg-neutral-50 flex flex-col">
      <div className="max-w-[1100px] mx-auto px-6 w-full">
        {/* Top bar */}
        <div className="flex items-center justify-between py-6">
          <Link
            to="/"
            className="text-sm font-medium text-neutral-500 hover:text-black transition-colors no-underline"
          >
            ← Back to home
          </Link>
          <BackendStatusPill probe={probe} onRetry={runProbe} forceReprobe={forceReprobe} />
        </div>

        <div className="flex flex-col items-center justify-center py-12">
          <h1 className="text-[clamp(28px,4vw,40px)] font-extrabold tracking-[-0.02em] mb-3 text-black text-center">
            Sign in to InstaEdit
          </h1>
          <p className="text-neutral-500 text-[17px] mb-8 text-center max-w-[480px]">
            Choose a platform to connect. Secure OAuth{degraded ? null : " — no passwords, no data shared without your consent"}.
          </p>

          {degraded && (
            <div
              role="alert"
              aria-live="polite"
              className="w-full max-w-[640px] mb-6 bg-red-50 border border-red-200 rounded-xl p-5"
            >
              <div className="flex items-start gap-3">
                <AlertTriangle size={20} className="text-red-500 mt-0.5 shrink-0" />
                <div className="flex-1 min-w-0">
                  {failureCopy && (
                    <>
                      <p className="text-red-700 font-bold text-[15px] mb-1">
                        {failureCopy.title}
                      </p>
                      <p className="text-red-700/90 text-[13px] leading-relaxed mb-3">
                        {failureCopy.body}
                      </p>
                      <p className="text-neutral-700 text-[13px] leading-relaxed mb-3">
                        <strong className="font-semibold">How to fix:</strong>{" "}
                        {failureCopy.hint}
                      </p>
                    </>
                  )}
                  <p className="text-neutral-500 text-[12px] mt-3 font-mono break-all">
                    Probed URL: {probe.kind === "ready" ? probe.result.url : API_BASE_URL}
                  </p>
                  <div className="flex items-center gap-3 mt-4">
                    <button
                      type="button"
                      onClick={() => void runProbe()}
                      className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-red-600 text-white text-[13px] font-semibold hover:bg-red-700 transition-colors"
                    >
                      Retry probe
                    </button>
                    <Link
                      to="/status"
                      className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-white border border-red-300 text-red-700 text-[13px] font-semibold hover:bg-red-50 transition-colors no-underline"
                    >
                      Open system status →
                    </Link>
                  </div>
                </div>
              </div>
            </div>
          )}

          <div
            className={`grid grid-cols-1 sm:grid-cols-2 gap-4 w-full max-w-[640px] ${
              degraded ? "opacity-60 pointer-events-none" : ""
            }`}
            aria-disabled={degraded}
          >
            {PROVIDERS.map((p) => (
              <a
                key={p.id}
                href={degraded ? undefined : `${API_BASE_URL}/api/v1/auth/${p.id}/login`}
                tabIndex={degraded ? -1 : 0}
                aria-disabled={degraded}
                className="group relative bg-white border border-neutral-200 rounded-xl p-5 no-underline text-black hover:border-neutral-400 hover:shadow-[0_8px_24px_rgba(0,0,0,0.06)] hover:-translate-y-[2px] transition-all overflow-hidden"
              >
                {/* Gradient bar on hover */}
                <div className={`absolute top-0 left-0 right-0 h-1 bg-gradient-to-r ${p.color} opacity-0 group-hover:opacity-100 transition-opacity rounded-t-xl`} />

                <div className="flex items-start gap-4">
                  <div className={`w-12 h-12 rounded-xl bg-gradient-to-br ${p.color} flex items-center justify-center text-white shrink-0`}>
                    {p.icon}
                  </div>
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center justify-between">
                      <h3 className="font-bold text-[15px] mb-1 text-black">{p.name}</h3>
                      <ArrowRight size={16} className="text-neutral-300 group-hover:text-black group-hover:translate-x-[2px] transition-all" />
                    </div>
                    <p className="text-[13px] text-neutral-500 leading-relaxed">{p.description}</p>
                  </div>
                </div>
              </a>
            ))}
          </div>

          {degraded && (
            <p className="mt-6 text-[13px] text-neutral-500 max-w-[480px] text-center">
              Provider buttons are temporarily disabled while the backend is unreachable. They will re-enable
              automatically when the connection comes back.
            </p>
          )}

          {/* Security note */}
          <div className="mt-10 flex items-center gap-2 text-[13px] text-neutral-400">
            <Shield size={14} className="text-[#0A84FF]" />
            <span>Official OAuth • No passwords saved • Revoke access at any time</span>
          </div>
        </div>
      </div>
    </div>
  );
}

function BackendStatusPill({
  probe,
  onRetry,
  forceReprobe,
}: {
  probe: ProbeState;
  onRetry: () => void;
  forceReprobe: () => void;
}) {
  const longPress = useLongPress(forceReprobe);
  // Tooltip explaining the dev affordance. Drawn into the title attr
  // on every state so hovering always surfaces the hint.
  const forceHint =
    "Right-click or long-press to force re-probe (bypass 5min cache TTL)";

  if (probe.kind === "loading") {
    return (
      <span
        {...longPress}
        className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full bg-neutral-100 border border-neutral-200 text-neutral-600 text-[12px] font-medium select-none"
        aria-live="polite"
        title={forceHint}
      >
        <Loader2 size={12} className="animate-spin" />
        Checking connection…
      </span>
    );
  }
  if (probe.result.ok) {
    return (
      <span
        {...longPress}
        className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full bg-green-50 border border-green-200 text-green-700 text-[12px] font-medium select-none"
        title={`OAuth backend reachable at ${probe.result.url} — ${forceHint}`}
      >
        <CheckCircle2 size={12} />
        Connected to {shortHost(probe.result.url)}
      </span>
    );
  }
  // Degraded: pill becomes a clickable escape-hatch to /status so a user
  // stuck on /login can immediately see the full diagnostic surface.
  // Right-click / long-press on the same pill forces a re-probe without
  // navigating (the link's onClick still runs after long-press, which
  // is fine — user gets a re-probe AND a navigation to /status).
  return (
    <Link
      to="/status"
      {...longPress}
      className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full bg-red-50 border border-red-200 text-red-700 text-[12px] font-medium hover:bg-red-100 transition-colors no-underline select-none"
      title={`Backend at ${probe.result.url} is unreachable — ${forceHint}`}
      onClick={(e) => {
        // Allow the link to navigate even though we want to also re-probe
        // on return. (No preventDefault — let React Router handle it.)
        e;
        onRetry();
      }}
    >
      <AlertTriangle size={12} />
      {statusLabel(probe.result)} →
    </Link>
  );
}
