import { PlayCircle } from "lucide-react";
import { TESTIMONIALS } from "./content";

/**
 * Sezione "Le Loro Storie" — testimonianze video in formato Short.
 * Griglia di frame 9:16 (stesso trattamento del founder story della
 * landing principale), con palette calda femminile.
 */
export function Testimonials() {
  return (
    <section id="testimonianze" className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-orange-500 w-[380px] h-[380px] -top-20 -right-32 animate-drift-slow opacity-40" />
        <div className="glow-orb bg-rose-500 w-[340px] h-[340px] -bottom-32 -left-24 animate-drift-rev opacity-30" />
      </div>
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 text-center mx-auto animate-fade-up">
          <div className="text-eyebrow text-rose-300/90 mb-3">{TESTIMONIALS.eyebrow}</div>
          <h2 className="text-display-2 text-white">
            {TESTIMONIALS.title}
            <span className="text-gradient-warm">{TESTIMONIALS.titleAccent}</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch] mx-auto">
            {TESTIMONIALS.subtitle}
          </p>
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-5">
          {TESTIMONIALS.videos.map((id, i) => (
            <div
              key={id}
              className={`animate-fade-up ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300", "animation-delay-400", "animation-delay-500", "animation-delay-600", "animation-delay-100"][i % 8]}`}
            >
              <div className="relative mx-auto w-full max-w-[260px] surface-glass rounded-[2rem] border border-white/15 p-2 shadow-[0_30px_100px_-40px_rgba(244,63,94,0.45)] transition-all duration-300 hover:border-rose-400/40 hover:shadow-[0_30px_100px_-30px_rgba(244,63,94,0.5)]">
                <div className="relative aspect-[9/16] rounded-[1.6rem] overflow-hidden bg-black">
                  <iframe
                    src={`https://www.youtube.com/embed/${id}?playsinline=1`}
                    title={`Video testimonianza ${i + 1}`}
                    className="absolute inset-0 w-full h-full"
                    loading="lazy"
                    referrerPolicy="strict-origin-when-cross-origin"
                    allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share"
                    allowFullScreen
                  />
                </div>
                <div className="flex items-center justify-center gap-1.5 py-2 text-[10px] text-zinc-400">
                  <PlayCircle className="w-3.5 h-3.5 text-rose-300" />
                  <span>Storia di una studentessa</span>
                </div>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
