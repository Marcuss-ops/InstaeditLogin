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
 * Results — income-focused stats + YouTube screenshot gallery.
 *
 * Social proof lives in two verified forms only: the student video
 * testimonials (Testimonials.tsx) and the YouTube Studio screenshot
 * gallery below. The gallery images are LIGHTBOX-CLICKABLE so visitors
 * can inspect them full-size (previous complaint: small unreadable
 * thumbnails). The lightbox is a single accessible dialog with
 * Esc/backdrop/prev-next close, body scroll lock, focus trap, and
 * ARIA labelling.
 * -------------------------------------------------------------------------- */

export function ResultsSection() {
  const stats = [
    { v: "$1,940", l: "Median student income", desc: "across all active channels", icon: DollarSign, color: "text-emerald-400" },
    { v: "14 days", l: "Avg. first payout", desc: "from channel start", icon: Clock, color: "text-blue-400" },
    { v: "50+", l: "Channels monetized", desc: "and generating revenue", icon: Users, color: "text-violet-400" },
    { v: "90%", l: "AI-executed pipeline", desc: "you approve, we run", icon: BarChart3, color: "text-amber-400" },
  ];

  const screenshots = [
    { img: "/results/result-1.jpg", alt: "YouTube channel growth result — first 90 days", caption: "Day-by-day growth on a finance channel" },
    { img: "/results/result-2.jpg", alt: "Content strategy result — RPM uplift", caption: "RPM uplift after PM setup" },
    { img: "/results/result-3.jpg", alt: "Channel monetization result — Partner Program", caption: "Partner Program activation window" },
    { img: "/results/result-4.jpg", alt: "Video performance result — 28-day revenue", caption: "28-day revenue per video" },
    { img: "/results/result-5.jpg", alt: "Creator growth result — subscribers", caption: "Subscriber curve crossing 1k" },
    { img: "/results/result-6.jpg", alt: "Multi-platform result — cross-post", caption: "Cross-post earnings across 7 platforms" },
  ];

  const [lightbox, setLightbox] = useState<{ imgIdx: number } | null>(null);

  const closeLightbox = useCallback(() => setLightbox(null), []);

  // Escape closes the lightbox; ←/→ step through the gallery when open.
  useEffect(() => {
    if (!lightbox) return;
    function onKey(e: KeyboardEvent) {
      if (!lightbox) return;
      if (e.key === "Escape") {
        e.preventDefault();
        closeLightbox();
        return;
      }
      if (e.key === "ArrowRight") {
        e.preventDefault();
        setLightbox({ imgIdx: (lightbox.imgIdx + 1) % screenshots.length });
      } else if (e.key === "ArrowLeft") {
        e.preventDefault();
        setLightbox({ imgIdx: (lightbox.imgIdx - 1 + screenshots.length) % screenshots.length });
      }
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [lightbox, screenshots.length, closeLightbox]);

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

        {/* YouTube Studio screenshot gallery — clickable into the lightbox. */}
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-5">
          {screenshots.map((ch, i) => (
            <button
              key={ch.img}
              type="button"
              onClick={() => setLightbox({ imgIdx: i })}
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

      {/* Lightbox overlay — shared accessible dialog for the gallery */}
      <LightboxOverlay
        lightbox={lightbox}
        onClose={closeLightbox}
        onPrev={() => {
          if (!lightbox) return;
          setLightbox({ imgIdx: (lightbox.imgIdx - 1 + screenshots.length) % screenshots.length });
        }}
        onNext={() => {
          if (!lightbox) return;
          setLightbox({ imgIdx: (lightbox.imgIdx + 1) % screenshots.length });
        }}
        screenshots={screenshots}
      />
    </section>
  );
}

/* ------------------------------------------------------------------------- */
/* LightboxOverlay — accessible dialog for zooming a gallery screenshot.     */
/* ------------------------------------------------------------------------- */

function LightboxOverlay({
  lightbox,
  onClose,
  onPrev,
  onNext,
  screenshots,
}: {
  lightbox: { imgIdx: number } | null;
  onClose: () => void;
  onPrev: () => void;
  onNext: () => void;
  screenshots: ReadonlyArray<{ img: string; alt: string; caption: string }>;
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

  const ch = screenshots[lightbox.imgIdx];

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label={`Image preview: ${ch.alt}`}
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
          <button
            type="button"
            onClick={onPrev}
            aria-label={`Previous image (${((lightbox.imgIdx - 1 + screenshots.length) % screenshots.length) + 1} of ${screenshots.length})`}
            className="inline-flex items-center justify-center w-10 h-10 rounded-full bg-white/[0.08] hover:bg-white/[0.16] border border-white/15 text-white transition-colors"
          >
            <ChevronLeft className="w-5 h-5" />
          </button>
          <button
            type="button"
            onClick={onNext}
            aria-label={`Next image (${((lightbox.imgIdx + 1) % screenshots.length) + 1} of ${screenshots.length})`}
            className="inline-flex items-center justify-center w-10 h-10 rounded-full bg-white/[0.08] hover:bg-white/[0.16] border border-white/15 text-white transition-colors"
          >
            <ChevronRight className="w-5 h-5" />
          </button>
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
          <figure>
            <img
              src={ch.img}
              alt={ch.alt}
              className="w-full h-auto max-h-[80vh] object-contain bg-[#0a0a12]"
            />
            <figcaption className="px-5 py-3 border-t border-white/10 text-sm text-zinc-300 bg-[#14141c]/60">
              <span className="text-zinc-400 mr-2">{(lightbox.imgIdx + 1)} / {screenshots.length}</span>
              {ch.caption}
            </figcaption>
          </figure>
        </div>
      </div>
    </div>
  );
}
