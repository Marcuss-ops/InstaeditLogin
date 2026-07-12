import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { AlertTriangle, ArrowRight, CheckCircle2, Loader2, Shield, Sparkles } from "lucide-react";
import { Nav } from "../components/Nav";
import { API_BASE_URL } from "../lib/api";
import { PROVIDERS } from "../lib/providers";
import { probeBackend, subscribeToVisibility, type ProbeResult } from "../lib/auth";
import { clearProbeCache, readProbeCache, writeProbeCache } from "../lib/probe-cache";
import { useLongPress } from "../lib/use-long-press";
import { bannerCopy, shortHost, statusLabel } from "../lib/probe-display";

type ProbeState =
  | { kind: "loading" }
  | { kind: "ready"; result: ProbeResult };

export function Login() {
  const [probe, setProbe] = useState<ProbeState>(() => {
    const cached = readProbeCache();
    return cached !== null
      ? { kind: "ready", result: cached.backend }
      : { kind: "loading" };
  });
  const lastHealthyRef = useRef<boolean>(readProbeCache()?.backend.ok === true);

  const runProbe = useCallback(async () => {
    setProbe({ kind: "loading" });
    const result = await probeBackend();
    lastHealthyRef.current = result.ok;
    if (result.ok) {
      writeProbeCache({ lastOkAt: Date.now(), backend: result });
    }
    setProbe({ kind: "ready", result });
  }, []);

  // Dev affordance: long-press the status pill to force re-probe
  const forceReprobe = useCallback(() => {
    clearProbeCache();
    void runProbe();
  }, [runProbe]);

  useEffect(() => {
    if (probe.kind === "loading") {
      void runProbe();
    }
    return subscribeToVisibility(() => {
      if (lastHealthyRef.current) return;
      void runProbe();
    });
  }, [runProbe, probe.kind]);

  const failureCopy =
    probe.kind === "ready" && !probe.result.ok
      ? bannerCopy(probe.result)
      : null;
  const degraded = failureCopy !== null;

  return (
    <div className="min-h-screen bg-[#030308] flex flex-col relative isolate">
      {/* Ambient orbs */}
      <div className="ambient-orbs" aria-hidden="true">
        <div className="orb orb-1"></div>
        <div className="orb orb-2"></div>
        <div className="orb orb-3"></div>
        <div className="orb orb-4"></div>
        <div className="orb orb-5"></div>
      </div>

      <Nav />

      <div className="flex-1 flex flex-col items-center justify-center px-6 py-16 relative z-10">
        {/* Header */}
        <div className="text-center mb-12 reveal max-w-[640px]">
          {/* Subtle badge */}
          <div className="inline-flex items-center gap-2 px-4 py-1.5 rounded-full bg-white/[0.06] border border-white/[0.12] text-[12px] text-[#9aa0aa] mb-6 backdrop-blur-sm">
            <Sparkles size={13} className="text-[#7B61FF]" />
            Connect your social accounts
          </div>

          <h1 className="text-[clamp(28px,5vw,48px)] font-extrabold tracking-[-0.02em] mb-4 leading-[1.05]">
            <span className="text-white">Sign in to </span>
            <span className="bg-gradient-to-r from-[#0A84FF] to-[#7B61FF] bg-clip-text text-transparent">
              InstaEdit
            </span>
          </h1>
          <p className="text-[#9aa0aa] text-[17px] leading-relaxed">
            Choose a platform to connect. Secure OAuth{degraded ? null : " — no passwords, no data shared without your consent"}.
          </p>
        </div>

        {/* Degraded banner */}
        {degraded && (
          <div
            role="alert"
            aria-live="polite"
            className="w-full max-w-[640px] mb-8 bg-red-500/10 border border-red-500/25 rounded-2xl p-5 backdrop-blur-sm reveal"
          >
            <div className="flex items-start gap-3">
              <AlertTriangle size={20} className="text-red-400 mt-0.5 shrink-0" />
              <div className="flex-1 min-w-0">
                {failureCopy && (
                  <>
                    <p className="text-red-300 font-bold text-[15px] mb-1">
                      {failureCopy.title}
                    </p>
                    <p className="text-red-200/80 text-[13px] leading-relaxed mb-3">
                      {failureCopy.body}
                    </p>
                    <p className="text-[#9aa0aa] text-[13px] leading-relaxed mb-3">
                      <strong className="font-semibold text-white">How to fix:</strong>{" "}
                      {failureCopy.hint}
                    </p>
                  </>
                )}
                <p className="text-[#6b7280] text-[11px] mt-2 font-mono break-all">
                  Probed URL: {probe.kind === "ready" ? probe.result.url : API_BASE_URL}
                </p>
                <div className="flex items-center gap-3 mt-4">
                  <button
                    type="button"
                    onClick={() => void runProbe()}
                    className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-red-600 text-white text-[13px] font-semibold hover:bg-red-500 transition-colors"
                  >
                    Retry probe
                  </button>
                  <Link
                    to="/status"
                    className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-white/[0.06] border border-red-500/30 text-red-300 text-[13px] font-semibold hover:bg-red-500/10 transition-colors no-underline"
                  >
                    Open system status →
                  </Link>
                </div>
              </div>
            </div>
          </div>
        )}

        {/* Provider cards — staggered animation */}
        <div
          className={`grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4 w-full max-w-[820px] ${
            degraded ? "opacity-50 pointer-events-none" : ""
          }`}
          aria-disabled={degraded}
        >
          {PROVIDERS.map((p, i) => (
            <a
              key={p.id}
              href={degraded ? undefined : `${API_BASE_URL}/api/v1/auth/${p.id}/login`}
              tabIndex={degraded ? -1 : 0}
              aria-disabled={degraded}
              className="group relative bg-white/[0.04] border border-white/[0.10] rounded-2xl p-5 no-underline text-white hover:bg-white/[0.07] hover:border-white/[0.22] hover:-translate-y-[3px] hover:shadow-[0_16px_48px_rgba(0,0,0,0.4)] transition-all duration-300 overflow-hidden"
              style={{ animation: `fadeUp 0.5s cubic-bezier(0.22, 1, 0.36, 1) ${i * 0.08}s both` }}
            >
              {/* Gradient bar on hover */}<div className={`absolute top-0 left-0 right-0 h-[2px] bg-gradient-to-r ${p.color} opacity-0 group-hover:opacity-100 transition-opacity duration-300 rounded-t-2xl`} />

              {/* Subtle glow on hover */}
              <div
                className={`absolute -inset-2 bg-gradient-to-br ${p.color} opacity-0 group-hover:opacity-[0.06] blur-2xl transition-opacity duration-500 pointer-events-none`}
              />

              <div className="relative flex items-start gap-4">
                {/* Icon */}
                <div
                  className={`w-11 h-11 rounded-xl bg-gradient-to-br ${p.iconBg} flex items-center justify-center text-white shrink-0 shadow-lg group-hover:scale-105 transition-transform duration-300`}
                  style={{ boxShadow: `0 0 20px ${p.glowColor}` }}
                >
                  {p.icon}
                </div>

                <div className="flex-1 min-w-0">
                  <div className="flex items-center justify-between">
                    <h3 className="font-bold text-[15px] mb-1 text-white group-hover:text-white transition-all duration-300">
                      {p.name}
                    </h3>
                    <ArrowRight
                      size={15}
                      className="text-white/20 group-hover:text-white/70 group-hover:translate-x-[3px] transition-all duration-300 shrink-0"
                    />
                  </div>
                  <p className="text-[12px] text-[#9aa0aa] leading-relaxed group-hover:text-[#b0b8c0] transition-colors duration-300">
                    {p.description}
                  </p>
                </div>
              </div>
            </a>
          ))}
        </div>

        {/* Degraded note */}
        {degraded && (
          <p className="mt-6 text-[13px] text-[#6b7280] max-w-[480px] text-center reveal">
            Provider buttons are temporarily disabled while the backend is unreachable. They will re-enable
            automatically when the connection comes back.
          </p>
        )}

        {/* Bottom: status pill + security note */}
        <div className="mt-12 flex flex-col items-center gap-4 reveal">
          <BackendStatusPill probe={probe} onRetry={runProbe} forceReprobe={forceReprobe} />
          <div className="flex items-center gap-2 text-[12px] text-[#6b7280]">
            <Shield size={13} className="text-[#0A84FF]" />
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
  const forceHint =
    "Right-click or long-press to force re-probe (bypass 5min cache TTL)";

  if (probe.kind === "loading") {
    return (
      <span
        {...longPress}
        className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full bg-white/[0.04] border border-white/[0.10] text-[#9aa0aa] text-[11px] font-medium select-none backdrop-blur-sm"
        aria-live="polite"
        title={forceHint}
      >
        <Loader2 size={11} className="animate-spin" />
        Checking connection…
      </span>
    );
  }
  if (probe.result.ok) {
    return (
      <span
        {...longPress}
        className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full bg-emerald-500/10 border border-emerald-500/25 text-emerald-400 text-[11px] font-medium select-none backdrop-blur-sm"
        title={`OAuth backend reachable at ${probe.result.url} — ${forceHint}`}
      >
        <CheckCircle2 size={11} />
        Connected to {shortHost(probe.result.url)}
      </span>
    );
  }
  return (
    <Link
      to="/status"
      {...longPress}
      className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full bg-red-500/10 border border-red-500/25 text-red-400 text-[11px] font-medium hover:bg-red-500/15 transition-colors no-underline select-none backdrop-blur-sm"
      title={`Backend at ${probe.result.url} is unreachable — ${forceHint}`}
      onClick={() => onRetry()}
    >
      <AlertTriangle size={11} />
      {statusLabel(probe.result)} →
    </Link>
  );
}
