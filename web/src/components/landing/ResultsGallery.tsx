import { useCallback, useEffect, useRef, useState } from "react";
import {
  X,
  ChevronLeft,
  ChevronRight,
  Maximize2,
} from "lucide-react";

/* ----------------------------------------------------------------------------
 * ResultsGallery — shared YouTube Studio screenshot gallery with a
 * LIGHTBOX-CLICKABLE grid (previous complaint: small unreadable thumbnails).
 *
 * Used by BOTH the main InstaEdit landing (pages/landing/ResultsSection.tsx)
 * and the independent HerChannel AI landing (pages/donne/Results.tsx), so the
 * result screenshots and their accessibility affordances live in one place.
 *
 * The lightbox is a single accessible dialog with Esc/backdrop/prev-next
 * close, body scroll lock, focus trap, and ARIA labelling.
 * -------------------------------------------------------------------------- */

export type Screenshot = {
  img: string;
  alt: string;
  caption: string;
};

export function ResultsGallery({
  screenshots,
  accent = "violet",
}: {
  screenshots: ReadonlyArray<Screenshot>;
  accent?: "violet" | "pink";
}) {
  const [lightbox, setLightbox] = useState<{ imgIdx: number } | null>(null);

  const closeLightbox = useCallback(() => setLightbox(null), []);
  const hoverClass =
    accent === "pink"
      ? "hover:border-pink-400/30 hover:shadow-[0_8px_32px_rgba(244,63,94,0.12)]"
      : "hover:border-violet-400/30 hover:shadow-[0_8px_32px_rgba(139,92,246,0.12)]";

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
    <>
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-5">
        {screenshots.map((ch, i) => (
          <button
            key={ch.img}
            type="button"
            onClick={() => setLightbox({ imgIdx: i })}
            aria-label={`Open larger preview: ${ch.alt}`}
            className={`surface-card overflow-hidden animate-fade-up transition-all duration-300 group text-left ${hoverClass} ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300", "animation-delay-400", "animation-delay-500"][i]}`}
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
    </>
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
  screenshots: ReadonlyArray<Screenshot>;
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
  // cannot Tab past the dialog into the gallery cards underneath.
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
