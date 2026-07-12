import type { ReactNode } from "react";
import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import {
  AlertTriangle,
  ArrowLeft,
  CheckCircle2,
  Clock,
  Copy,
  ExternalLink,
  Loader2,
  Pause,
  Play,
  RefreshCw,
  ShieldCheck,
} from "lucide-react";
import { API_BASE_URL } from "../lib/api";
import {
  probeAuthBoundary,
  probeBackend,
  subscribeToVisibility,
  type AuthBoundaryResult,
  type ProbeResult,
} from "../lib/auth";
import { clearProbeCache, getProbeCacheStats, readProbeCache, writeProbeCache } from "../lib/probe-cache";
import { useLongPress } from "../lib/use-long-press";
import { bannerCopy, shortHost, statusLabel } from "../lib/probe-display";

type State =
  | { kind: "loading" }
  | {
      kind: "ready";
      health: ProbeResult;
      auth: AuthBoundaryResult;
      latencyMs: number;
      healthBody: Record<string, unknown> | null;
      checkedAt: number;
    };

const POLL_OK_MS = 30_000;
const POLL_DEGRADED_MS = 10_000;

function copyToClipboard(text: string): void {
  if (typeof navigator === "undefined" || !navigator.clipboard) {
    return;
  }
  void navigator.clipboard.writeText(text).catch(() => {
    /* ignore — best-effort UX */
  });
}

export function Status() {
  // Hydrate the initial state from the sessionStorage cache so /status
  // can render the green/red card, the stat grid, the auth-boundary
  // line, and the JSON body on first paint instead of showing a
  // "Probing…" spinner for ~1s. Polling still runs on its normal
  // cadence; the cache only skips the *initial* probe when fresh.
  const [state, setState] = useState<State>(() => {
    const cached = readProbeCache();
    if (cached !== null) {
      return {
        kind: "ready",
        health: cached.backend,
        // Fall back to a synthetic "ok" if the previous run was a
        // Login-only mount that didn't cache the auth-boundary result.
        // It will be overwritten by the next poll, so this only
        // affects first-paint fidelity for users who have not yet
        // visited /status after a backend outage.
        auth: cached.authBoundary ?? { ok: true, status: 401 },
        latencyMs: cached.latencyMs ?? 0,
        healthBody: cached.healthBody ?? null,
        // Hydrate the timestamp so "Last probe: Xs ago" reflects the
        // real age of the cached data, not the mount time.
        checkedAt: cached.lastOkAt,
      };
    }
    return { kind: "loading" };
  });
  const [paused, setPaused] = useState(false);
  const [now, setNow] = useState<number>(() => Date.now());
  // Seeded from the cache so a hydrated "ready/healthy" tab is treated
  // like one that just probed and succeeded. The polling tick uses
  // this to pick POLL_OK_MS vs POLL_DEGRADED_MS.
  const lastHealthyRef = useRef<boolean>(
    state.kind === "ready" && state.health.ok,
  );
  const scheduledRef = useRef<number | null>(null);
  // Guards the initial mount probe so re-runs of the polling effect
  // (e.g. pause→resume) keep their "always probe once" behavior.
  const isInitialRun = useRef(true);

  const runProbes = useCallback(async () => {
    if (paused) {
      return;
    }
    const healthStart = Date.now();
    const [health, auth] = await Promise.all([probeBackend(), probeAuthBoundary()]);
    const latencyMs = Date.now() - healthStart;

    let healthBody: Record<string, unknown> | null = null;
    if (health.ok) {
      try {
        const r = await fetch(`${API_BASE_URL}/api/v1/health`);
        if (r.ok) {
          healthBody = (await r.json()) as Record<string, unknown>;
        }
      } catch {
        /* non-critical, the green card already proves reachability */
      }
    }

    lastHealthyRef.current = health.ok;
    setState({
      kind: "ready",
      health,
      auth,
      latencyMs,
      healthBody,
      checkedAt: Date.now(),
    });
    // Mirror the result into the sessionStorage cache so the next
    // mount of /login (or /status) hydrates from it. Failures are
    // deliberately not cached — see probe-cache.ts invariants.
    if (health.ok) {
      writeProbeCache({
        lastOkAt: Date.now(),
        backend: health,
        authBoundary: auth,
        healthBody,
        latencyMs,
      });
    }
  }, [paused]);

  // Dev affordance: right-click or long-press the green/red card to
  // bypass the 5-minute probe cache TTL and force an immediate fresh
  // probe. The same handler is wired to both cards (and to the
  // /login pill) so the affordance feels consistent across pages.
  const forceReprobe = useCallback(() => {
    clearProbeCache();
    void runProbes();
  }, [runProbes]);
  const longPress = useLongPress(forceReprobe);
  const forceHint =
    "Right-click or long-press to force re-probe (bypass 5min cache TTL)";

  // Self-rescheduling poll chain — interval depends on the last known health.
  useEffect(() => {
    // First mount with a fresh cache: skip the initial probe so we
    // don't waste a /health call AND don't briefly flash "Probing…".
    // Subsequent re-runs of this effect (pause→resume, etc.) still
    // probe once, matching the previous behavior — the user pressed
    // Resume and expects fresh data.
    if (isInitialRun.current) {
      isInitialRun.current = false;
      if (readProbeCache() === null) {
        void runProbes();
      }
    } else {
      void runProbes();
    }

    const tick = () => {
      if (paused) {
        return;
      }
      const interval = lastHealthyRef.current ? POLL_OK_MS : POLL_DEGRADED_MS;
      scheduledRef.current = window.setTimeout(() => {
        void runProbes().finally(() => tick());
      }, interval);
    };

    tick();

    return () => {
      if (scheduledRef.current !== null) {
        clearTimeout(scheduledRef.current);
        scheduledRef.current = null;
      }
    };
  }, [runProbes, paused]);

  // Tick every second to drive the countdown display.
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);

  // Visibility self-heal: when returning to a degraded tab, re-probe so
  // recovery shows up immediately.
  useEffect(() => {
    return subscribeToVisibility(() => {
      if (!lastHealthyRef.current) {
        void runProbes();
      }
    });
  }, [runProbes]);

  // Hide this page from search engines — listing an admin surface is unhelpful.
  useEffect(() => {
    const meta = document.createElement("meta");
    meta.name = "robots";
    meta.content = "noindex,nofollow";
    document.head.appendChild(meta);
    return () => {
      meta.remove();
    };
  }, []);

  const checkedAt = state.kind === "ready" ? state.checkedAt : null;
  const ageSec = checkedAt === null ? 0 : Math.max(0, Math.round((now - checkedAt) / 1000));
  const intervalHint = lastHealthyRef.current ? POLL_OK_MS : POLL_DEGRADED_MS;
  const nextInSec = Math.max(0, Math.round(intervalHint / 1000 - ageSec));
  const degraded = state.kind === "ready" && !state.health.ok;
  const failureProbe =
    state.kind === "ready" && !state.health.ok
      ? (state.health as Extract<ProbeResult, { ok: false }>)
      : null;
  const degradedBanner = failureProbe ? bannerCopy(failureProbe) : null;
  const authState = state.kind === "ready" ? state.auth : null;

  // Read the in-memory cache counters on every render. They update
  // synchronously inside readProbeCache / clearProbeCache, so the
  // component re-renders (via state changes) always pick up the
  // freshest values. Hit rate is derived as hits / (hits + misses) —
  // force clears are excluded since they don't represent a cache miss.
  const cacheStats = getProbeCacheStats();
  const cacheTotal = cacheStats.hits + cacheStats.misses;
  const cacheHitRate =
    cacheTotal === 0
      ? null
      : Math.round((cacheStats.hits / cacheTotal) * 100);
  const cacheForceClearLabel = `${cacheStats.forceClears} force ${cacheStats.forceClears === 1 ? "clear" : "clears"}`;

  return (
    <div className="min-h-screen bg-neutral-50 flex flex-col">
      <div className="max-w-[860px] mx-auto px-6 w-full">
        {/* Top bar */}
        <div className="flex items-center justify-between py-6">
          <Link
            to="/"
            className="text-sm font-medium text-neutral-500 hover:text-black transition-colors no-underline inline-flex items-center gap-1"
          >
            <ArrowLeft size={14} /> Back to home
          </Link>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => setPaused((p) => !p)}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full bg-neutral-100 border border-neutral-200 text-neutral-700 text-[12px] font-medium hover:bg-neutral-200 transition-colors"
            >
              {paused ? <Play size={12} /> : <Pause size={12} />}
              {paused ? "Resume auto-refresh" : "Pause auto-refresh"}
            </button>
            <button
              type="button"
              onClick={() => void runProbes()}
              disabled={state.kind === "loading"}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full bg-black text-white text-[12px] font-semibold hover:bg-neutral-800 transition-colors disabled:opacity-50"
            >
              <RefreshCw size={12} />
              Refresh now
            </button>
          </div>
        </div>

        <div className="flex flex-col items-center justify-center pb-16">
          <h1 className="text-[clamp(28px,4vw,40px)] font-extrabold tracking-[-0.02em] mb-2 text-black text-center">
            System Status
          </h1>
          <p className="text-neutral-500 text-[16px] text-center max-w-[600px] mb-10">
            Real-time reachability probe of the OAuth backend. Auto-refreshes every {POLL_OK_MS / 1000}s when
            healthy, every {POLL_DEGRADED_MS / 1000}s when something is wrong.
          </p>

          {state.kind === "loading" ? (
            <div className="bg-white border border-neutral-200 rounded-xl p-12 w-full flex flex-col items-center gap-4">
              <Loader2 size={32} className="animate-spin text-neutral-400" />
              <p className="text-[14px] text-neutral-500">Probing backend…</p>
              <code className="text-[12px] text-neutral-400 font-mono break-all">{API_BASE_URL}</code>
            </div>
          ) : (
            <>
              {degraded && degradedBanner && (
                <div
                  role="alert"
                  aria-live="polite"
                  {...longPress}
                  className="w-full bg-red-50 border border-red-200 rounded-xl p-6 mb-6 select-none"
                  title={forceHint}
                >
                  <div className="flex items-start gap-3">
                    <AlertTriangle size={22} className="text-red-500 mt-0.5 shrink-0" />
                    <div className="flex-1 min-w-0">
                      <h2 className="text-red-700 font-bold text-[17px] mb-1">
                        {degradedBanner.title}
                      </h2>
                      <p className="text-red-700/90 text-[14px] leading-relaxed mb-2">
                        {degradedBanner.body}
                      </p>
                      <p className="text-neutral-700 text-[14px] leading-relaxed">
                        <strong className="font-semibold">How to fix:</strong> {degradedBanner.hint}
                      </p>
                      {/* Direct link to the README troubleshooting section so
                          the user can match their symptom to one of the 3
                          documented pitfalls (Vercel stale deploy / forgot
                          to redeploy / frontend-vs-backend origin confusion)
                          without leaving the app's failed OAuth flow. */}
                      <a
                        href="https://github.com/Marcuss-ops/InstaeditLogin/blob/main/README.md#deployment"
                        target="_blank"
                        rel="noopener noreferrer"
                        className="mt-3 inline-flex items-center gap-1.5 text-[13px] text-red-700 hover:text-red-900 underline underline-offset-2 font-medium transition-colors"
                      >
                        <ExternalLink size={12} />
                        See "Deployment" troubleshooting guide on GitHub
                      </a>
                      <p className="text-neutral-500 text-[12px] mt-3 font-mono break-all">
                        Probed URL: {state.health.url}
                      </p>
                      <button
                        type="button"
                        onClick={() => void runProbes()}
                        className="mt-4 inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-red-600 text-white text-[13px] font-semibold hover:bg-red-700 transition-colors"
                      >
                        <RefreshCw size={14} />
                        Retry now
                      </button>
                    </div>
                  </div>
                </div>
              )}

              {!degraded && (
                <div
                  {...longPress}
                  className="w-full bg-green-50 border border-green-200 rounded-xl p-6 mb-6 flex items-start gap-3 select-none"
                  title={forceHint}
                >
                  <CheckCircle2 size={28} className="text-green-600 mt-0.5 shrink-0" />
                  <div>
                    <h2 className="text-green-700 font-bold text-[17px] mb-1">
                      All systems operational
                    </h2>
                    <p className="text-green-700/90 text-[14px]">
                      Backend at <span className="font-mono font-semibold">{shortHost(state.health.url)}</span>{" "}
                      answered /api/v1/health in {state.latencyMs}ms.
                    </p>
                  </div>
                </div>
              )}

              {/* Stat grid */}
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 w-full">
                <StatCard
                  label="Backend URL (probed)"
                  value={<URLWithCopy url={state.health.url} />}
                />
                <StatCard
                  label="Last probe"
                  value={
                    state.health.ok
                      ? `${ageSec}s ago — healthy`
                      : `${ageSec}s ago — ${statusLabel(state.health)}`
                  }
                />
                <StatCard
                  label="Response latency"
                  value={state.health.ok ? `${state.latencyMs}ms` : "—"}
                />
                <StatCard
                  label="Auth boundary"
                  value={
                    authState === null
                      ? "—"
                      : authState.ok ? (
                          <span className="text-green-700 font-semibold">
                            ✓ 401 (CORS + middleware healthy)
                          </span>
                        ) : (
                          <span className="text-red-700 font-semibold">
                            degraded: {authState.reason} ({authState.status ?? "no response"})
                          </span>
                        )
                  }
                />
                {/* Cache efficiency spans the full row so the 5-card
                    grid stays visually balanced (2+2+1). The counters
                    are per-tab in-memory; reset on page reload. */}
                <div className="sm:col-span-2">
                  <StatCard
                    label="Probe cache efficiency (this tab)"
                    value={
                      <span className="flex flex-col gap-0.5">
                        <span>
                          <span className="text-green-700 font-semibold">
                            {cacheStats.hits}
                          </span>{" "}
                          hits ·{" "}
                          <span className="text-neutral-500">
                            {cacheStats.misses}
                          </span>{" "}
                          misses
                          {cacheHitRate === null
                            ? " (no reads yet)"
                            : ` (${cacheHitRate}% hit rate)`}
                        </span>
                        <span className="text-[12px] text-neutral-400 font-normal">
                          {cacheForceClearLabel} via right-click / long-press ·
                          cache saves a /health call per hit
                        </span>
                      </span>
                    }
                  />
                </div>
              </div>

              {/* JSON body */}
              {state.healthBody && (
                <div className="bg-white border border-neutral-200 rounded-xl p-5 w-full mt-6">
                  <h3 className="text-[13px] font-semibold text-neutral-700 mb-2 flex items-center gap-1.5">
                    <ShieldCheck size={14} className="text-neutral-500" />
                    Response from GET /api/v1/health
                  </h3>
                  <pre className="bg-neutral-900 text-green-300 font-mono text-[12px] leading-relaxed rounded-lg p-4 overflow-x-auto">
                    {JSON.stringify(state.healthBody, null, 2)}
                  </pre>
                </div>
              )}

              {/* Auto-refresh footer */}
              <div className="mt-8 text-[13px] text-neutral-400 flex items-center gap-2">
                <Clock size={14} />
                {paused ? (
                  <span>Auto-refresh paused. Click Refresh now to update.</span>
                ) : (
                  <span>Next automatic probe in {nextInSec}s.</span>
                )}
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  );
}

function StatCard({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="bg-white border border-neutral-200 rounded-xl px-5 py-4">
      <div className="text-[12px] font-medium text-neutral-500 mb-1.5 uppercase tracking-wider">
        {label}
      </div>
      <div className="text-[14px] text-black font-medium break-all">{value}</div>
    </div>
  );
}

function URLWithCopy({ url }: { url: string }) {
  return (
    <span className="flex items-center gap-2 font-mono">
      <span className="truncate">{url}</span>
      <button
        type="button"
        onClick={() => copyToClipboard(url)}
        aria-label="Copy backend URL"
        className="shrink-0 inline-flex items-center justify-center w-6 h-6 rounded bg-neutral-100 hover:bg-neutral-200 text-neutral-600 transition-colors"
      >
        <Copy size={12} />
      </button>
    </span>
  );
}
