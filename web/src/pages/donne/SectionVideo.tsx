import { useEffect, useRef } from "react";

/**
 * Video di sfondo per le sezioni DonneTube.
 *
 * Rende le sezioni più vive mostrando un filmato in loop dietro il testo,
 * mantenendo leggibile la copia del tema chiaro grazie a un overlay crema
 * regolabile. Autoplay muto, senza controlli e senza audio.
 *
 * Il video viene caricato in modo pigro (IntersectionObserver): l'URL viene
 * assegnato solo quando la sezione si avvicina al viewport, così i ~80 MB
 * totali dei tre sfondi non gravano sul primo caricamento della pagina.
 */
export function SectionVideo({
  src,
  overlay = 0.76,
}: {
  /** URL diretto del video (MP4). */
  src: string;
  /** Opacità dell'overlay crema (0 = video pieno, 1 = sfondo uniforme). */
  overlay?: number;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const videoRef = useRef<HTMLVideoElement>(null);

  // Assegna l'URL e avvia la riproduzione solo quando la sezione è vicina.
  useEffect(() => {
    const container = containerRef.current;
    const video = videoRef.current;
    if (!container || !video) return;

    const observer = new IntersectionObserver(
      (entries) => {
        if (entries[0]?.isIntersecting) {
          if (!video.src) video.src = src;
          video.play().catch(() => {
            /* autoplay bloccato dal browser: il video resta fermo, nessun crash */
          });
        }
      },
      { rootMargin: "400px 0px" },
    );

    observer.observe(container);
    return () => observer.disconnect();
  }, [src]);

  return (
    <div
      ref={containerRef}
      aria-hidden="true"
      className="absolute inset-0 overflow-hidden pointer-events-none"
    >
      <video
        ref={videoRef}
        className="absolute inset-0 w-full h-full object-cover"
        muted
        loop
        playsInline
        preload="none"
      />
      <div
        className="absolute inset-0"
        style={{ backgroundColor: "#F9F8F6", opacity: overlay }}
      />
    </div>
  );
}
