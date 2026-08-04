import { ArrowRight, Calendar, Bot } from "lucide-react";
import { useBooking } from "../../components/booking/BookingProvider";
import { HERO } from "./content";

/**
 * Hero della landing "DonneTube" — promessa al centro: i primi
 * $2.000/mese con video faceless, in pilota automatico. Tema chiaro,
 * accenti caldi (corallo/terracotta) e serif per il titolo.
 */
export function Hero() {
  const { open: openBooking } = useBooking();

  return (
    <section className="relative pt-32 pb-24 overflow-hidden bg-[#F9F8F6]">
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="absolute w-[520px] h-[520px] -top-40 -left-32 rounded-full bg-[#EAD6DE] blur-[120px] opacity-70" />
        <div className="absolute w-[460px] h-[460px] -bottom-48 -right-24 rounded-full bg-[#F4E4D3] blur-[120px] opacity-70" />
      </div>
      <div
        aria-hidden="true"
        className="absolute inset-0 opacity-[0.5] pointer-events-none"
        style={{
          backgroundImage:
            "radial-gradient(rgba(107,78,113,0.10) 1px, transparent 1px)",
          backgroundSize: "26px 26px",
        }}
      />

      <div className="relative mx-auto max-w-4xl px-6 text-center animate-fade-up">
        <div className="flex flex-wrap items-center justify-center gap-2 mb-7">
          <span className="donne-chip">
            <Bot className="w-3.5 h-3.5 text-[#C4696F]" />
            {HERO.badge}
          </span>
          <span className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full bg-[#FBF1E7] border border-[#EDDFCE] text-xs font-medium text-[#A06A38]">
            <span className="relative flex h-2 w-2">
              <span className="animate-pulse-glow absolute inline-flex h-full w-full rounded-full bg-[#E28743] opacity-75" />
              <span className="relative inline-flex rounded-full h-2 w-2 bg-[#E28743]" />
            </span>
            {HERO.badgeLimited}
          </span>
        </div>

        <h1 className="text-display-1 text-[#4A3E56] text-balance mx-auto">
          {HERO.titleStart}{" "}
          <span className="text-gradient-warm">{HERO.titleAccent}</span>
        </h1>

        <p className="text-body-lg text-[#5B5566] font-semibold mt-7">
          {HERO.subtitleTop}
        </p>

        <p className="text-body-lg text-[#6E6677] mt-5 max-w-[62ch] mx-auto">
          {HERO.subtitle}
        </p>

        <div className="flex flex-col sm:flex-row items-center justify-center gap-4 mt-8">
          <button
            type="button"
            onClick={() => openBooking("general")}
            className="group inline-flex items-center gap-2 px-7 py-3.5 rounded-xl bg-gradient-to-r from-[#E07A5F] to-[#E28743] text-white font-semibold text-sm shadow-[0_10px_28px_-10px_rgba(224,122,95,0.8)] hover:shadow-[0_16px_38px_-10px_rgba(226,135,67,0.85)] hover:scale-[1.02] active:scale-100 transition-all"
          >
            <Calendar className="w-4 h-4" />
            {HERO.ctaPrimary}
            <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
          </button>
          <a
            href="#risultati"
            className="inline-flex items-center gap-2 px-6 py-3.5 rounded-xl bg-white border border-[#E2D8E6] text-sm font-medium text-[#6B4E71] hover:border-[#C9B9CE] hover:text-[#4A3E56] transition-all"
          >
            {HERO.ctaSecondary}
          </a>
        </div>

        <div className="mt-10 flex flex-wrap items-center justify-center gap-3 text-sm text-[#7A7280]">
          {HERO.stats.map((stat) => (
            <div
              key={stat.label}
              className="donne-chip"
            >
              <span className="text-[#4A3E56] font-semibold">{stat.value}</span>
              <span>{stat.label}</span>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
