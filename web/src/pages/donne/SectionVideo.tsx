/**
 * Video di sfondo per le sezioni DonneTube.
 *
 * Rende le sezioni più vive mostrando un filmato in loop dietro il testo.
 * I file sono self-hosted (web/public/videos), leggeri e serviti dallo
 * stesso dominio, quindi l'autoplay muto è affidabile ovunque.
 *
 * Un overlay crema regolabile mantiene l'identità del tema chiaro, e uno
 * scrim bianco sfumato nella parte alta protegge la leggibilità dei titoli.
 */
export function SectionVideo({
  src,
  overlay = 0.42,
}: {
  /** Percorso locale del video (MP4, es. /videos/harsh.mp4). */
  src: string;
  /** Opacità dell'overlay crema (0 = video pieno, 1 = sfondo uniforme). */
  overlay?: number;
}) {
  return (
    <div
      aria-hidden="true"
      className="absolute inset-0 overflow-hidden pointer-events-none"
    >
      <video
        className="absolute inset-0 w-full h-full object-cover"
        src={src}
        autoPlay
        muted
        loop
        playsInline
        preload="auto"
      />
      {/* Overlay crema per l'identità del tema chiaro (video visibile sotto) */}
      <div
        className="absolute inset-0"
        style={{ backgroundColor: "#F9F8F6", opacity: overlay }}
      />
      {/* Scrim bianco sfumato in alto: i titoli restano leggibili */}
      <div className="absolute inset-x-0 top-0 h-64 bg-gradient-to-b from-white/90 to-transparent" />
      {/* Scrim morbido in basso per la chiusura della sezione */}
      <div className="absolute inset-x-0 bottom-0 h-40 bg-gradient-to-t from-white/70 to-transparent" />
    </div>
  );
}
