import { useEffect, useRef, useState } from "react";
import {
  Bot,
  Globe,
  DollarSign,
  Zap,
  Languages,
  TrendingUp,
  Mic,
  CheckCircle2,
} from "lucide-react";
import { IconSchedule } from "../../components/landing/icons";

/* ----------------------------------------------------------------------------
 * Features — semi-automated pipeline, maximum income (gain-focused)
 * -------------------------------------------------------------------------- */

/* Multi-language growth chart (hand-drawn SVG).
 *
 * pathLength={1} normalises every polyline so `.line-draw` /
 * `.line-draw-play` (stroke-dasharray: 1) can animate a clean
 * left-to-right draw-on. The play class is only added once the card
 * scrolls into view (useInView below), so the animation is actually
 * seen instead of firing at mount below the fold.
 *
 * delta = growth % computed from the baseline (y=162 = bottom) using
 * the same 6-point series the line plots — shown next to each
 * endpoint flag so the "scale to multiple languages" claim is
 * visually provable, not just a headline.
 */
const LANG_SERIES = [
  {
    key: "en",
    label: "English",
    flag: "🇺🇸",
    color: "#38bdf8",
    autoDubbed: false,
    delta: "+57%",
    points: [
      [25, 107.9], [105, 100.4], [185, 93.9], [265, 89.3], [345, 83.7], [425, 78.1],
    ],
  },
  {
    key: "es",
    label: "Spanish",
    flag: "🇪🇸",
    color: "#a78bfa",
    autoDubbed: true,
    delta: "+173%",
    points: [
      [25, 117.1], [105, 103.2], [185, 87.4], [265, 70.7], [345, 56.8], [425, 42.9],
    ],
  },
  {
    key: "de",
    label: "German",
    flag: "🇩🇪",
    color: "#34d399",
    autoDubbed: true,
    delta: "+250%",
    points: [
      [25, 121.8], [105, 106], [185, 89.3], [265, 68.9], [345, 47.5], [425, 26.1],
    ],
  },
] as const;

const MONTHS = ["Jan", "Mar", "May", "Jul", "Sep", "Nov"];

/* Catmull-Rom → cubic Bézier so the growth lines read as smooth
 * analytics curves (YouTube Studio / Stripe style) instead of angular
 * polylines. */
function smoothPath(points: readonly (readonly [number, number])[]): string {
  if (points.length < 2) return "";
  let d = `M${points[0][0]} ${points[0][1]}`;
  for (let i = 0; i < points.length - 1; i++) {
    const p0 = points[Math.max(0, i - 1)];
    const p1 = points[i];
    const p2 = points[i + 1];
    const p3 = points[Math.min(points.length - 1, i + 2)];
    const c1x = p1[0] + (p2[0] - p0[0]) / 6;
    const c1y = p1[1] + (p2[1] - p0[1]) / 6;
    const c2x = p2[0] - (p3[0] - p1[0]) / 6;
    const c2y = p2[1] - (p3[1] - p1[1]) / 6;
    d += ` C${c1x} ${c1y}, ${c2x} ${c2y}, ${p2[0]} ${p2[1]}`;
  }
  return d;
}

/* Fires once when the target scrolls into view; no-op without
 * IntersectionObserver (falls back to "always in view"). */
function useInView<T extends HTMLElement>(threshold = 0.2) {
  const ref = useRef<T>(null);
  const [inView, setInView] = useState(
    () => typeof IntersectionObserver === "undefined",
  );
  useEffect(() => {
    const el = ref.current;
    if (!el || typeof IntersectionObserver === "undefined") return;
    const obs = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) {
          setInView(true);
          obs.disconnect();
        }
      },
      { threshold },
    );
    obs.observe(el);
    return () => obs.disconnect();
  }, [threshold]);
  return { ref, inView };
}

export function Features() {
  const { ref: chartRef, inView: chartInView } = useInView<HTMLDivElement>(0.2);

  /* Interactive language filter — clicking a pill dims that language's
   * line so the multi-region story can be read one series at a time.
   * "All dimmed" falls back to all-visible so the chart never goes empty. */
  const [hidden, setHidden] = useState<ReadonlySet<string>>(new Set());
  const toggleLang = (key: string) => {
    setHidden((prev) => {
      const next = new Set(prev);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
  };
  const isDimmed = (key: string) =>
    hidden.size === LANG_SERIES.length ? false : hidden.has(key);

  return (
    <section id="features" className="relative py-24 sm:py-32 bg-elevated overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora opacity-25 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">How it works</div>
          <h2 className="text-display-2 text-white">Semi-automated. Maximum income.</h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            No camera. No editing software. No experience. Our 90% AI-executed system turns a single
            idea into daily content across 7 platforms &mdash; engineered to generate revenue.
          </p>
        </div>
        <div className="grid lg:grid-cols-3 gap-5">
          <div className="surface-card p-7 lg:p-8 relative overflow-hidden lg:col-span-2 lg:row-span-2 animate-fade-up hover:border-violet-400/30 transition-all duration-300">
            <div aria-hidden="true" className="absolute -top-32 -right-32 w-80 h-80 rounded-full bg-violet-500 blur-3xl opacity-50" />
            <div aria-hidden="true" className="absolute bottom-0 left-0 right-0 h-px bg-gradient-to-r from-transparent via-violet-400/50 to-transparent" />
            <div className="relative">
              <div className="inline-flex w-12 h-12 rounded-xl items-center justify-center ring-1 ring-violet-400/40 surface-glass text-violet-300 mb-5">
                <Bot className="w-6 h-6" />
              </div>
              <h3 className="text-display-3 text-white mb-3 max-w-[22ch]">AI creates. You earn.</h3>
              <p className="text-sm text-zinc-400 leading-relaxed max-w-[52ch]">
                Type one sentence and ChronoN AI generates a professional,
                monetization-ready video. No camera, no microphone, no editing software.
                The AI handles everything from script to final render.
              </p>
              <div className="mt-7 surface-glass rounded-xl border border-white/10 overflow-hidden">
                <div className="flex items-center gap-1.5 px-4 py-2.5 border-b border-white/5">
                  <span className="w-2.5 h-2.5 rounded-full bg-[#ff5f57]" />
                  <span className="w-2.5 h-2.5 rounded-full bg-[#febc2e]" />
                  <span className="w-2.5 h-2.5 rounded-full bg-[#28c840]" />
                  <span className="ml-3 text-[11px] text-zinc-500">Autopilot · this week</span>
                </div>
                <div className="grid grid-cols-7 gap-1.5 p-3 text-center text-[10px] text-zinc-500">
                  {["M", "T", "W", "T", "F", "S", "S"].map((d, idx) => (
                    <div key={`${d}${idx}`} className="rounded-md border border-white/5 bg-black/20 py-2.5">
                      <div className="text-eyebrow text-zinc-600 mb-1.5">{d}</div>
                      <div className="space-y-1">
                        {[1, 2].slice(0, idx % 2 === 0 ? 2 : 1).map((i) => (
                          <div key={i} className={`h-1.5 rounded-full mx-1 ${i === 1 ? "bg-violet-400/70" : "bg-cyan-400/70"}`} />
                        ))}
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </div>
          </div>
          <div className="surface-card p-6 relative overflow-hidden animate-fade-up animation-delay-100 hover:border-cyan-400/30 transition-all duration-300">
            <div className="inline-flex w-11 h-11 rounded-xl items-center justify-center ring-1 ring-cyan-400/40 surface-glass text-cyan-300 mb-4">
              <IconSchedule className="w-5 h-5" />
            </div>
            <h3 className="text-display-3 text-white mb-2">7 platforms, 1 video.</h3>
            <p className="text-sm text-zinc-400 leading-relaxed">
              One AI-generated video is automatically converted and published to
              YouTube, TikTok, Instagram Reels, Facebook, X and more &mdash;
              multiplying your reach by 7x.
            </p>
          </div>
          <div className="surface-card p-6 relative overflow-hidden animate-fade-up animation-delay-200 hover:border-pink-400/30 transition-all duration-300">
            <div className="inline-flex w-11 h-11 rounded-xl items-center justify-center ring-1 ring-pink-400/40 surface-glass text-pink-300 mb-4">
              <DollarSign className="w-5 h-5" />
            </div>
            <h3 className="text-display-3 text-white mb-2">Revenue from day one.</h3>
            <p className="text-sm text-zinc-400 leading-relaxed">
              Our Fast-Track Partner Monetization setup gets your content through
              the Partner Program review window in days, not months — so you
              start earning ad revenue, sponsorships and affiliate income sooner.
            </p>
          </div>

          {/* ---------------- Scale to multiple languages ---------------- */}
          <div className="surface-card p-7 lg:p-8 relative overflow-hidden lg:col-span-3 animate-fade-up animation-delay-300 hover:border-amber-400/40 transition-all duration-300 bg-[radial-gradient(circle_at_top_right,rgba(234,179,8,0.16),transparent_45%)] shadow-[0_20px_50px_rgba(0,0,0,0.45)] hover:shadow-[0_24px_70px_-12px_rgba(251,146,60,0.28)]">
            <div aria-hidden="true" className="absolute -bottom-24 -right-24 w-72 h-72 rounded-full bg-amber-500/30 blur-3xl pointer-events-none" />
            <div aria-hidden="true" className="absolute -top-28 -right-28 w-64 h-64 rounded-full bg-amber-400/15 blur-3xl pointer-events-none" />
            <div className="relative grid lg:grid-cols-2 gap-8 lg:gap-10 items-center">
              <div>
                <div className="inline-flex w-11 h-11 rounded-xl items-center justify-center ring-1 ring-amber-400/50 surface-glass text-amber-300 mb-4 bg-gradient-to-br from-amber-400/25 to-orange-500/10 shadow-[0_0_28px_rgba(251,191,36,0.45)]">
                  <Globe className="w-5 h-5" />
                </div>
                <h3 className="text-display-3 text-white mb-3">
                  Scale to{" "}
                  <span className="text-gradient-gold text-glow-gold">
                    multiple languages.
                  </span>
                </h3>
                <p className="text-sm text-zinc-300 leading-relaxed max-w-[52ch]">
                  Expand your channel portfolio to Spanish, Portuguese, French,
                  German and more &mdash; all powered by AI translation and
                  localization. Reach global audiences without learning a new
                  language, and watch Tier-1 views compound across every region.
                </p>
                <ul className="mt-5 space-y-2.5">
                  <li className="flex items-center gap-2.5 text-sm text-zinc-200 group transition-colors hover:text-white">
                    <span className="inline-flex w-5 h-5 items-center justify-center rounded-md bg-amber-400/10 ring-1 ring-amber-400/40 text-amber-300 shrink-0">
                      <Zap className="w-3 h-3" />
                    </span>
                    1-Click AI Translation &amp; Voice Cloning
                  </li>
                  <li className="flex items-center gap-2.5 text-sm text-zinc-200 group transition-colors hover:text-white">
                    <span className="inline-flex w-5 h-5 items-center justify-center rounded-md bg-emerald-400/10 ring-1 ring-emerald-400/40 text-emerald-300 shrink-0">
                      <Languages className="w-3 h-3" />
                    </span>
                    Reach US, EU &amp; LATAM markets instantly
                  </li>
                  <li className="flex items-center gap-2.5 text-sm text-zinc-200 group transition-colors hover:text-white">
                    <span className="inline-flex w-5 h-5 items-center justify-center rounded-md bg-violet-400/10 ring-1 ring-violet-400/40 text-violet-300 shrink-0">
                      <Mic className="w-3 h-3" />
                    </span>
                    Voice-cloned dubbing keeps your tone &amp; energy
                  </li>
                </ul>
              </div>

              {/* Growth chart + live-pipeline feel */}
              <div ref={chartRef} className="relative surface-glass rounded-xl border border-white/10 p-5 pt-6 shadow-[inset_0_1px_0_rgba(255,255,255,0.05)]">
                {/* Floating stat badge — +340% Views via AI Translation */}
                <div className="absolute -top-3.5 right-4 z-10 inline-flex items-center gap-1.5 rounded-full bg-gradient-to-r from-amber-400 to-orange-500 px-3 py-1 text-[11px] font-bold text-[#201204] shadow-[0_0_24px_rgba(251,146,60,0.55)] animate-float-y">
                  <TrendingUp className="w-3.5 h-3.5" />
                  +340% Views via AI Translation
                </div>

                {/* Interactive language pills — click to focus a market */}
                <div className="mb-4 flex items-center justify-between gap-3">
                  <span className="text-[10px] font-semibold uppercase tracking-[0.16em] text-zinc-500">
                    Global views / month
                  </span>
                  <span className="inline-flex items-center gap-1.5 text-[11px] text-emerald-300">
                    <span className="h-1.5 w-1.5 rounded-full bg-emerald-400 shadow-[0_0_8px_rgba(52,211,153,0.8)]" />
                    Live growth
                  </span>
                </div>
                <div
                  className="flex flex-wrap items-center gap-2 mb-4"
                  role="group"
                  aria-label="Filter the growth chart by language"
                >
                  {LANG_SERIES.map((s) => {
                    const off = isDimmed(s.key);
                    return (
                      <button
                        key={s.key}
                        type="button"
                        onClick={() => toggleLang(s.key)}
                        aria-pressed={!off}
                        title={off ? `Show ${s.label} again` : `Hide ${s.label}`}
                        className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-[11px] font-medium border transition-all duration-300 cursor-pointer ${
                          off
                            ? "border-white/10 bg-white/[0.03] text-zinc-500"
                            : "text-zinc-100 bg-white/[0.06]"
                        }`}
                        style={
                          off
                            ? undefined
                            : { borderColor: `${s.color}66`, boxShadow: `0 0 16px -4px ${s.color}80` }
                        }
                      >
                        <span className="w-2 h-2 rounded-full" style={{ backgroundColor: s.color }} />
                        {s.flag} {s.label}
                        {s.autoDubbed && <span className="text-zinc-400">· auto-dub</span>}
                      </button>
                    );
                  })}
                </div>

                <svg
                  viewBox="0 0 460 175"
                  className="w-full h-auto"
                  role="img"
                  aria-label="Views growth across English, Spanish and German channels"
                >
                  <defs>
                    {LANG_SERIES.map((s) => (
                      <linearGradient key={s.key} id={`lngArea-${s.key}`} x1="0" y1="0" x2="0" y2="1">
                        <stop offset="0%" stopColor={s.color} stopOpacity="0.3" />
                        <stop offset="100%" stopColor={s.color} stopOpacity="0" />
                      </linearGradient>
                    ))}
                    <filter id="dotGlow" x="-80%" y="-80%" width="260%" height="260%">
                      <feGaussianBlur stdDeviation="3" result="blur" />
                      <feMerge>
                        <feMergeNode in="blur" />
                        <feMergeNode in="SourceGraphic" />
                      </feMerge>
                    </filter>
                  </defs>

                  {/* gridlines */}
                  {[45, 85, 125].map((y) => (
                    <line key={y} x1="25" y1={y} x2="425" y2={y} stroke="rgba(255,255,255,0.09)" strokeDasharray="3 5" />
                  ))}
                  {/* baseline */}
                  <line x1="25" y1="162" x2="425" y2="162" stroke="rgba(255,255,255,0.14)" />

                  {/* area fills — element opacity dims with the pills; the
                      gradient stopOpacity is the full-strength ceiling */}
                  {LANG_SERIES.map((s) => (
                    <path
                      key={`${s.key}-area`}
                      d={`${smoothPath(s.points)} L425 162 L25 162 Z`}
                      fill={`url(#lngArea-${s.key})`}
                      opacity={isDimmed(s.key) ? 0.15 : 1}
                      className="transition-opacity duration-500"
                    />
                  ))}

                  {/* lines — draw-on when the card scrolls into view */}
                  {LANG_SERIES.map((s, idx) => (
                    <path
                      key={s.key}
                      d={smoothPath(s.points)}
                      fill="none"
                      stroke={s.color}
                      strokeWidth="3"
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      pathLength={1}
                      opacity={isDimmed(s.key) ? 0.15 : 1}
                      style={{
                        animationDelay: `${0.3 + idx * 0.15}s`,
                        filter: `drop-shadow(0 0 5px ${s.color})`,
                      }}
                      className={`line-draw ${chartInView ? "line-draw-play" : ""} transition-opacity duration-500`}
                    />
                  ))}

                  {/* endpoint dots with glow + per-language delta labels */}
                  {LANG_SERIES.map((s) => {
                    const [x, y] = s.points[s.points.length - 1];
                    const off = isDimmed(s.key);
                    return (
                      <g key={s.key} opacity={off ? 0.2 : 1} className="transition-opacity duration-500">
                        <circle cx={x} cy={y} r="8" fill={s.color} opacity="0.25" filter="url(#dotGlow)" />
                        <circle cx={x} cy={y} r="3.5" fill={s.color} />
                        <text
                          x={x - 8}
                          y={y - 10}
                          fontSize="11"
                          fontWeight="700"
                          textAnchor="end"
                          fill={s.color}
                          stroke="#0d0e15"
                          strokeWidth={4}
                          paintOrder="stroke"
                        >
                          {s.flag} {s.delta}
                        </text>
                      </g>
                    );
                  })}
                </svg>

                {/* month labels — positioned at the exact SVG x coordinates (x/460 = %) */}
                <div className="relative mt-3 h-4 text-[10px] text-zinc-400 font-medium">
                  {MONTHS.map((m, i) => (
                    <span
                      key={m}
                      className="absolute -translate-x-1/2"
                      style={{ left: `${LANG_SERIES[0].points[i][0] / 4.6}%` }}
                    >
                      {m}
                    </span>
                  ))}
                </div>

                {/* Auto-dub pipeline status — the "software in action" proof */}
                <div className="mt-4 flex items-center gap-3 rounded-lg border border-white/10 bg-white/[0.04] px-3 py-2.5">
                  <Bot className="w-4 h-4 text-amber-300 shrink-0" />
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center justify-between gap-2 text-[11px]">
                      <span className="text-zinc-300 font-medium truncate">
                        Auto-dubbing Spanish + German tracks
                      </span>
                      <span className="inline-flex items-center gap-1 text-emerald-300 font-semibold tabular-nums shrink-0">
                        <CheckCircle2 className="w-3.5 h-3.5" />
                        Done in 4.2s
                      </span>
                    </div>
                    <div className="mt-1.5 h-1 rounded-full bg-white/10 overflow-hidden">
                      <div
                        className={`h-full rounded-full bg-gradient-to-r from-amber-400 to-orange-500 progress-fill ${
                          chartInView ? "progress-fill-play" : ""
                        }`}
                      />
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
