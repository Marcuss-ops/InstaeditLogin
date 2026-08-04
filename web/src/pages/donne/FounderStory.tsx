import { MessageCircleHeart, Sparkles } from "lucide-react";
import { FOUNDER } from "./content";

/**
 * Sezione "Dalla Fondatrice" — la storia di come è nato DonneTube.
 * Il messaggio della fondatrice è seguito dal video racconto orizzontale.
 */
export function FounderStory() {
  return (
    <section id="fondatrice" className="relative py-24 sm:py-32 overflow-hidden bg-[#F9F8F6]">
      <div className="relative mx-auto max-w-4xl px-6">
        <div className="animate-fade-up text-center">
          <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full bg-white border border-[#E2D8E6] text-xs font-medium text-[#6B4E71] mb-6">
            <Sparkles className="w-3.5 h-3.5 text-[#C4696F]" />
            {FOUNDER.eyebrow}
          </div>
          <h2 className="text-display-2 text-[#4A3E56] mb-8">
            {FOUNDER.titleStart}{" "}
            <span className="text-gradient-warm">{FOUNDER.titleAccent}</span>
          </h2>
        </div>

        <div className="grid lg:grid-cols-[1fr_auto] gap-10 items-start animate-fade-up">
          <div className="max-w-[60ch] mx-auto">
            {FOUNDER.paragraphs.map((p, i) => (
              <p
                key={i}
                className={`text-body-lg ${i === 0 ? "text-[#4A3E56]" : "text-[#6E6677]"} mb-5 last:mb-0`}
              >
                {p}
              </p>
            ))}
          </div>
          <div className="grid grid-cols-3 gap-4 lg:flex lg:flex-col lg:gap-4">
            {FOUNDER.stats.map((s) => (
              <div key={s.l} className="donne-card p-4 text-center lg:w-36">
                <div className="text-xl font-bold text-[#4A3E56] tabular-nums">{s.v}</div>
                <div className="text-[11px] uppercase tracking-wider text-[#7A7280] mt-1">
                  {s.l}
                </div>
              </div>
            ))}
          </div>
        </div>

        <div className="mt-14 animate-fade-up">
          <div className="max-w-3xl mx-auto bg-white rounded-2xl border border-[#E8D8DB] p-2.5 shadow-[0_18px_44px_-24px_rgba(122,95,110,0.45)] transition-all duration-300 hover:border-[#D8A0A5] hover:shadow-[0_24px_50px_-24px_rgba(180,100,105,0.5)]">
            <div className="relative aspect-video rounded-xl overflow-hidden bg-[#F6F3F7]">
              <iframe
                src={`https://www.youtube.com/embed/${FOUNDER.videoId}?playsinline=1`}
                title="Video racconto: come è nato DonneTube"
                className="absolute inset-0 w-full h-full"
                loading="lazy"
                referrerPolicy="strict-origin-when-cross-origin"
                allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share"
                allowFullScreen
              />
            </div>
            <div className="flex items-center justify-center gap-1.5 py-2.5 text-xs text-[#7A7280]">
              <MessageCircleHeart className="w-4 h-4 text-[#C4696F]" />
              <span>La storia di come è nato DonneTube</span>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
