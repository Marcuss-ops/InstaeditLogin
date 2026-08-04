import { PlayCircle } from "lucide-react";
import { TESTIMONIALS } from "./content";

/**
 * Sezione "Le Loro Storie" — testimonianze video in formato Short.
 * Griglia di frame 9:16 su sfondo chiaro, con accento caldo.
 */
export function Testimonials() {
  return (
    <section id="testimonianze" className="relative py-24 sm:py-32 overflow-hidden bg-[#FFFFFF]">
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 text-center mx-auto animate-fade-up">
          <div className="text-eyebrow text-[#A4708C] mb-3">{TESTIMONIALS.eyebrow}</div>
          <h2 className="text-display-2 text-[#4A3E56]">
            {TESTIMONIALS.title}
            <span className="text-gradient-warm">{TESTIMONIALS.titleAccent}</span>
          </h2>
          <p className="text-body-lg text-[#6E6677] mt-5 max-w-[58ch] mx-auto">
            {TESTIMONIALS.subtitle}
          </p>
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-5 gap-5">
          {TESTIMONIALS.videos.map((id, i) => (
            <div
              key={id}
              className={`animate-fade-up ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300", "animation-delay-400", "animation-delay-500", "animation-delay-600", "animation-delay-100"][i % 8]}`}
            >
              <div className="relative mx-auto w-full max-w-[260px] bg-white rounded-[2rem] border border-[#E8D8DB] p-2 shadow-[0_18px_44px_-24px_rgba(122,95,110,0.45)] transition-all duration-300 hover:border-[#D8A0A5] hover:shadow-[0_24px_50px_-24px_rgba(180,100,105,0.5)]">
                <div className="relative aspect-[9/16] rounded-[1.6rem] overflow-hidden bg-[#F6F3F7]">
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
                <div className="flex items-center justify-center gap-1.5 py-2 text-[10px] text-[#7A7280]">
                  <PlayCircle className="w-3.5 h-3.5 text-[#C4696F]" />
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
