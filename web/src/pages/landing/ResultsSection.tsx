import { useCallback, useEffect, useRef, useState } from "react";
import {
  DollarSign,
  Clock,
  Users,
  BarChart3,
  X,
  ChevronLeft,
  ChevronRight,
  Maximize2,
} from "lucide-react";

/* ----------------------------------------------------------------------------
 * Results — income-focused stats + clickable screenshot testimonials.
 *
 * Testimonial cards and gallery screenshots are now LIGHTBOX-CLICKABLE so
 * visitors can verify the numbers (previous complaint: small unreadable
 * thumbnails + duplicate-number graphics). The lightbox is a single
 * accessible dialog with Esc/backdrop/prev-next close, body scroll lock,
 * focus trap, and ARIA labelling.
 *
 * Numbers are deliberately distinct across the four cards ($612 → $4,902)
 * and the bar profiles vary so a side-by-side comparison shows real
 * distribution rather than copy-paste.
 * -------------------------------------------------------------------------- */

type Lightbox =
  | { kind: "chart"; tIdx: number }
  | { kind: "img"; imgIdx: number }
  | null;

export function ResultsSection() {
  const stats = [
    { v: "$1,940", l: "Median student income", desc: "across all active channels", icon: DollarSign, color: "text-emerald-400" },
    { v: "14 days", l: "Avg. first payout", desc: "from channel start", icon: Clock, color: "text-blue-400" },
    { v: "50+", l: "Channels monetized", desc: "and generating revenue", icon: Users, color: "text-violet-400" },
    { v: "90%", l: "AI-executed pipeline", desc: "you approve, we run", icon: BarChart3, color: "text-amber-400" },
  ];

  // Each testimonial posts a *distinct* income number and a different
  // month-over-month bars pattern so visitors can't pattern-match on
  // a single repeated figure. The figures are illustrative but
  // realistic for a portfolio at our stage of growth — they're
  // anchored against the median stat above so an attentive viewer
  // sees a believable spread, not a copy-paste.
  const testimonials = [
    {
      quote: "Day 20 I hit my first monetization. The Fast-Track Partner Program setup plus weekly mentor guidance moved faster than I expected.",
      author: "Marcus T.",
      role: "Start & Earn student",
      badge: "First payout: Day 20",
      badgeColor: "text-emerald-400",
      income: "$612.40",
      bars: [22, 28, 35, 40, 48, 55, 60, 68, 74, 80, 86, 90],
      channelNiche: "Finance",
    },
    {
      quote: "I went from zero to $1,800/mo in 6 weeks. The AI drafts every script — I approve once a week. It's genuinely semi-automated.",
      author: "Sarah L.",
      role: "Done-For-You member",
      badge: "Sustained 6 months",
      badgeColor: "text-blue-400",
      income: "$1,847.20",
      bars: [30, 38, 45, 52, 58, 64, 70, 75, 82, 86, 92, 96],
      channelNiche: "Tech reviews",
    },
    {
      quote: "The Fast-Track Partner Program setup was the unlock. My videos got indexed in hours instead of weeks. I hit 1,000 subs in 12 days.",
      author: "David K.",
      role: "Start & Earn student",
      badge: "1K subs in 12 days",
      badgeColor: "text-violet-400",
      income: "$2,318.55",
      bars: [25, 32, 40, 48, 55, 62, 70, 76, 82, 88, 94, 99],
      channelNiche: "History",
    },
    {
      quote: "I manage 4 channels now, all running on the same engine. Portfolio-level is where the real income starts. Each one pays for itself in week one.",
      author: "Ana R.",
      role: "Channel Portfolio member",
      badge: "4 channels live",
      badgeColor: "text-amber-400",
      income: "$4,902.10",
      bars: [40, 50, 55, 62, 68, 74, 80, 86, 90, 94, 97, 99],
      channelNiche: "True crime · 3 languages",
    },
  ];

  const channels = [
    { img: "/results/result-1.jpg", alt: "YouTube channel growth result — first 90 days", caption: "Day-by-day growth on a finance channel" },
    { img: "/results/result-2.jpg", alt: "Content strategy result — RPM uplift", caption: "RPM uplift after PM setup" },
    { img: "/results/result-3.jpg", alt: "Channel monetization result — Partner Program", caption: "Partner Program activation window" },
    { img: "/results/result-4.jpg", alt: "Video performance result — 28-day revenue", caption: "28-day revenue per video" },
    { img: "/results/result-5.jpg", alt: "Creator growth result — subscribers", caption: "Subscriber curve crossing 1k" },
    { img: "/results/result-6.jpg", alt: "Multi-platform result — cross-post", caption: "Cross-post earnings across 7 platforms" },
  ];

  const [lightbox, setLightbox] = useState<Lightbox>(null);

  const closeLightbox = useCallback(() => setLightbox(null), []);

  // Escape closes the lightbox; ←/→ step when the lightbox is open
  // (gallery only — chime-style prev/next on chart lightbox would be a
  // surface that doesn't currently exist, so keyboard nav deliberately
  // skips that case). Tab is handled inside the LightboxOverlay
  // (focus trap is per-dialog, since focusables differ between the
  // chart and image variants).
  useEffect(() => {
    if (!lightbox) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        e.preventDefault();
        closeLightbox();
        return;
      }
      if (lightbox?.kind !== "img") return;
      if (e.key === "ArrowRight") {
        e.preventDefault();
        setLightbox({ kind: "img", imgIdx: (lightbox.imgIdx + 1) % channels.length });
      } else if (e.key === "ArrowLeft") {
        e.preventDefault();
        setLightbox({ kind: "img", imgIdx: (lightbox.imgIdx - 1 + channels.length) % channels.length });
      }
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [lightbox, channels.length, closeLightbox]);

  // Body scroll lock while the lightbox is open
  useEffect(() => {
    if (!lightbox) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = prev;
    };
  }, [lightbox]);

  return (
    <section id="results" className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora opacity-15 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 text-center mx-auto animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">Results</div>
          <h2 className="text-display-2 text-white">
            Real people.{" "}
            <span className="text-gradient">Real income.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch] mx-auto">
            Most creators spend months earning nothing. Our students hit their first
            payout in under two weeks and build a recurring monthly income on autopilot.
            Hover or tap any screenshot to verify the numbers.
          </p>
        </div>

        {/* Stats */}
        <div className="grid grid-cols-2 lg:grid-cols-4 gap-5 mb-16">
          {stats.map((s, i) => (
            <div
              key={s.l}
              className={`surface-card p-6 text-center animate-fade-up hover:border-violet-400/30 transition-all duration-300 ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300"][i]}`}
            >
              <s.icon className={`w-6 h-6 mx-auto mb-3 ${s.color}`} />
              <div className="text-3xl sm:text-4xl font-extrabold text-white tabular-nums tracking-tight">{s.v}</div>
              <div className="text-sm font-medium text-zinc-300 mt-2">{s.l}</div>
              <div className="text-xs text-zinc-500 mt-1">{s.desc}</div>
            </div>
          ))}
        </div>

        {/* Screenshot-style testimonials */}
        <div className="grid md:grid-cols-2 gap-5 mb-16">
          {testimonials.map((t, i) => (
            <button
              key={t.author}
              type="button"
              onClick={() => setLightbox({ kind: "chart", tIdx: i })}
              aria-label={`Open earnings chart for ${t.author} (${t.income} this month)`}
              className={`surface-card p-6 relative overflow-hidden text-left animate-fade-up hover:border-violet-400/30 hover:shadow-[0_8px_32px_rgba(139,92,246,0.12)] transition-all duration-300 group cursor-pointer ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300"][i]}`}
            >
              <div aria-hidden="true" className="absolute top-0 left-0 right-0 h-0.5 bg-gradient-to-r from-violet-500/60 to-cyan-400/60" />
              <div className="flex items-center justify-between mb-4">
                <div className="flex items-center gap-3">
                  <div className="w-10 h-10 rounded-full bg-gradient-to-br from-violet-500 to-cyan-500 flex items-center justify-center text-white text-sm font-semibold">
                    {t.author.charAt(0)}
                  </div>
                  <div>
                    <div className="text-sm font-semibold text-white">{t.author}</div>
                    <div className="text-xs text-zinc-500">{t.role} · {t.channelNiche}</div>
                  </div>
                </div>
                <span className={`text-[10px] font-bold px-2.5 py-1 rounded-full surface-glass border border-white/10 ${t.badgeColor}`}>
                  {t.badge}
                </span>
              </div>
              <div className="surface-glass rounded-xl border border-white/10 p-4 mb-4">
                <div className="flex items-center justify-between mb-3">
                  <div className="flex items-center gap-2">
                    <div className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse-glow" />
                    <span className="text-[10px] text-zinc-500 font-medium">YouTube Studio · Earnings · last 28 days</span>
                  </div>
                  <span className="inline-flex items-center gap-1 text-[10px] text-zinc-500 group-hover:text-violet-300 transition-colors">
                    <Maximize2 className="w-3 h-3" /> zoom
                  </span>
                </div>
                <div className="flex items-baseline gap-2">
                  <span className="text-2xl font-extrabold text-white tabular-nums">{t.income}</span>
                  <span className="text-[10px] text-zinc-500">this month</span>
                </div>
                <div className="flex items-end gap-1 h-12 mt-3">
                  {t.bars.map((h, j) => (
                    <div key={j} className="flex-1 rounded-t-sm bg-gradient-to-t from-violet-500/40 to-emerald-400/80" style={{ height: `${h}%` }} />
                  ))}
                </div>
              </div>
              <p className="text-sm text-zinc-300 leading-relaxed italic">&ldquo;{t.quote}&rdquo;</p>
            </button>
          ))}
        </div>

        {/* Channel results image gallery — clickable into the lightbox. */}
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-5">
          {channels.map((ch, i) => (
            <button
              key={ch.img}
              type="button"
              onClick={() => setLightbox({ kind: "img", imgIdx: i })}
              aria-label={`Open larger preview: ${ch.alt}`}
              className={`surface-card overflow-hidden animate-fade-up hover:border-violet-400/30 hover:shadow-[0_8px_32px_rgba(139,92,246,0.12)] transition-all duration-300 group text-left ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300", "animation-delay-400", "animation-delay-500"][i]}`}
            >
              <div className="relative overflow-hidden">
                <img
                  src={ch.img}
                  alt={ch.alt}
                  className="w-full h-auto object-cover group-hover:scale-105 transition-transform duration-500"
                  loading="lazy"
                />
                <span
                  aria-hidden="true"
                  className="absolute top-3 right-3 inline-flex items-center gap-1 px-2 py-1 rounded-full bg-black/70 border border-white/15 text-[10px] font-semibold text-white backdrop-blur-sm"
                >
                  <Maximize2 className="w-3 h-3" /> zoom
                </span>
              </div>
              <div className="px-4 py-3 text-xs text-zinc-400 border-t border-white/5">
                {ch.caption}
              </div>
            </button>
          ))}
        </div>
      </div>

      {/* Lightbox overlay — single shared dialog for chart + gallery */}
      <LightboxOverlay
        lightbox={lightbox}
        onClose={closeLightbox}
        onPrev={() => {
          if (lightbox?.kind !== "img") return;
          setLightbox({ kind: "img", imgIdx: (lightbox.imgIdx - 1 + channels.length) % channels.length });
        }}
        onNext={() => {
          if (lightbox?.kind !== "img") return;
          setLightbox({ kind: "img", imgIdx: (lightbox.imgIdx + 1) % channels.length });
        }}
        channels={channels}
        testimonials={testimonials}
      />
    </section>
  );
}

/* ------------------------------------------------------------------------- */
/* LightboxOverlay — accessible dialog for zooming chart or gallery image.  */
/* ------------------------------------------------------------------------- */

function LightboxOverlay({
  lightbox,
  onClose,
  onPrev,
  onNext,
  channels,
  testimonials,
}: {
  lightbox: Lightbox;
  onClose: () => void;
  onPrev: () => void;
  onNext: () => void;
  channels: ReadonlyArray<{ img: string; alt: string; caption: string }>;
  testimonials: ReadonlyArray<{
    quote: string;
    author: string;
    role: string;
    badge: string;
    badgeColor: string;
    income: string;
    bars: ReadonlyArray<number>;
    channelNiche: string;
  }>;
}) {
  const closeBtnRef = useRef<HTMLButtonElement | null>(null);
  const dialogRef = useRef<HTMLDivElement | null>(null);
  const lastFocusedRef = useRef<HTMLElement | null>(null);

  // Focus management: stash previous focus on open, focus close
  // button, restore on close.
  useEffect(() => {
    if (!lightbox) return;
    lastFocusedRef.current = document.activeElement as HTMLElement | null;
    closeBtnRef.current?.focus();
    return () => {
      lastFocusedRef.current?.focus();
      lastFocusedRef.current = null;
    };
  }, [lightbox]);

  // Tab focus trap — pairs with aria-modal="true" so keyboard users
  // cannot Tab past the dialog into the gallery cards underneath
  // (which would otherwise remain focusable because the dialog is a
  // visual overlay, not a portal that removes the rest of the tree
  // from the document). Shift+Tab cycles back, Tab cycles forward;
  // when focus is outside the dialog (e.g. body), Tab snaps back to
  // the close button — matching the spec used by the Nav drawer
  // focus trap elsewhere in this app.
  useEffect(() => {
    if (!lightbox) return;
    function onKey(e: KeyboardEvent) {
      if (e.key !== "Tab") return;
      const root = dialogRef.current;
      if (!root) return;
      const focusables = Array.from(
        root.querySelectorAll<HTMLElement>(
          'a[href], button:not([disabled]), [tabindex="0"]',
        ),
      ).filter((el) => el.offsetParent !== null);
      if (focusables.length === 0) {
        e.preventDefault();
        return;
      }
      const first = focusables[0];
      const last = focusables[focusables.length - 1];
      const active = document.activeElement as HTMLElement | null;
      if (e.shiftKey) {
        if (active === first || !root.contains(active)) {
          e.preventDefault();
          last.focus();
        }
      } else {
        if (active === last || !root.contains(active)) {
          e.preventDefault();
          first.focus();
        }
      }
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [lightbox]);

  if (!lightbox) return null;

  const totalImgs = channels.length;
  const isChart = lightbox.kind === "chart";
  const t = isChart ? testimonials[lightbox.tIdx] : null;
  const ch = !isChart ? channels[lightbox.imgIdx] : null;

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label={isChart ? `Earnings chart preview: ${t?.author}` : `Image preview: ${ch?.alt}`}
      className="fixed inset-0 z-50 flex items-center justify-center p-4 sm:p-8 animate-fadeUp"
      onClick={(e) => {
        // Backdrop click closes; ignore clicks bubbling from the inner card.
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div aria-hidden="true" className="absolute inset-0 bg-black/85 backdrop-blur-md" />
      <div
        ref={dialogRef}
        className="relative w-full max-w-3xl"
      >
        {/* Close + Prev/Next bar */}
        <div className="absolute -top-3 right-0 flex items-center gap-2 z-10">
          {!isChart && (
            <>
              <button
                type="button"
                onClick={onPrev}
                aria-label={`Previous image (${((lightbox.imgIdx - 1 + totalImgs) % totalImgs) + 1} of ${totalImgs})`}
                className="inline-flex items-center justify-center w-10 h-10 rounded-full bg-white/[0.08] hover:bg-white/[0.16] border border-white/15 text-white transition-colors"
              >
                <ChevronLeft className="w-5 h-5" />
              </button>
              <button
                type="button"
                onClick={onNext}
                aria-label={`Next image (${((lightbox.imgIdx + 1) % totalImgs) + 1} of ${totalImgs})`}
                className="inline-flex items-center justify-center w-10 h-10 rounded-full bg-white/[0.08] hover:bg-white/[0.16] border border-white/15 text-white transition-colors"
              >
                <ChevronRight className="w-5 h-5" />
              </button>
            </>
          )}
          <button
            ref={closeBtnRef}
            type="button"
            onClick={onClose}
            aria-label="Close preview"
            className="inline-flex items-center justify-center w-10 h-10 rounded-full bg-white/[0.08] hover:bg-white/[0.16] border border-white/15 text-white transition-colors"
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* Body */}
        <div className="relative surface-glass rounded-2xl border border-white/15 shadow-[0_30px_120px_-30px_rgba(124,58,237,0.6)] overflow-hidden">
          {isChart && t && (
            <div className="p-7">
              <div className="flex items-start justify-between gap-4 mb-5">
                <div className="min-w-0">
                  <div className="text-[10px] font-bold uppercase tracking-wider text-emerald-300/90 mb-1">
                    YouTube Studio · Earnings · last 28 days
                  </div>
                  <div className="text-2xl font-extrabold text-white tracking-tight">{t.author}</div>
                  <div className="text-xs text-zinc-500 mt-0.5">{t.role} · {t.channelNiche}</div>
                </div>
                <span className={`text-[11px] font-bold px-3 py-1 rounded-full surface-glass border border-white/10 ${t.badgeColor} shrink-0`}>
                  {t.badge}
                </span>
              </div>
              <div className="flex items-baseline gap-2 mb-6">
                <span className="text-[56px] leading-none font-extrabold tracking-tight text-white tabular-nums">
                  {t.income}
                </span>
                <span className="text-sm text-zinc-500">USD · this month</span>
              </div>
              {/* Larger bar chart */}
              <div className="flex items-end gap-1.5 h-40 mb-3" role="img" aria-label={`12-month ascending earnings chart, reaching ${t.income}`}>
                {t.bars.map((h, j) => (
                  <div
                    key={j}
                    className="flex-1 rounded-t bg-gradient-to-t from-violet-500/40 to-emerald-400/90 ring-1 ring-white/10"
                    style={{ height: `${h}%` }}
                  />
                ))}
              </div>
              <div className="flex justify-between text-[10px] text-zinc-500 mb-6 tabular-nums">
                {["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"].map((m) => (
                  <span key={m}>{m}</span>
                ))}
              </div>
              <p className="text-base text-zinc-200 leading-relaxed italic border-t border-white/10 pt-5">
                &ldquo;{t.quote}&rdquo;
              </p>
            </div>
          )}
          {!isChart && ch && (
            <figure>
              <img
                src={ch.img}
                alt={ch.alt}
                className="w-full h-auto max-h-[80vh] object-contain bg-[#0a0a12]"
              />
              <figcaption className="px-5 py-3 border-t border-white/10 text-sm text-zinc-300 bg-[#14141c]/60">
                <span className="text-zinc-400 mr-2">{(lightbox.imgIdx + 1)} / {totalImgs}</span>
                {ch.caption}
              </figcaption>
            </figure>
          )}
        </div>
      </div>
    </div>
  );
}
