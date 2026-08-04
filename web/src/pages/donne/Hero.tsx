import { ArrowRight, Calendar, Bot } from "lucide-react";
import { useBooking } from "../../components/booking/BookingProvider";
import { HERO } from "./content";

/**
 * Hero della landing "DonneTube" — promessa al centro: i primi
 * $2.000/mese con video faceless, in pilota automatico. Il bottone
 * primario apre il booking (call strategica), quello secondario porta
 * alla gallery dei risultati.
 */
export function Hero() {
  const { open: openBooking } = useBooking();

  return (
    <section className="relative pt-32 pb-24 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora pointer-events-none" />
      <div aria-hidden="true" className="absolute inset-0 grid-bg pointer-events-none opacity-60" />
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-pink-500 w-[460px] h-[460px] -top-32 -left-24 animate-drift-slow opacity-70" />
        <div className="glow-orb bg-rose-400 w-[420px] h-[420px] -bottom-40 -right-24 animate-drift-rev opacity-60" />
      </div>

      <div className="relative mx-auto max-w-4xl px-6 text-center animate-fade-up">
        <div className="flex flex-wrap items-center justify-center gap-2 mb-7">
          <span className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full surface-glass border border-emerald-400/30 text-xs font-medium text-emerald-200">
            <Bot className="w-3.5 h-3.5" />
            <span>{HERO.badge}</span>
          </span>
          <span className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full surface-glass border border-red-400/30 text-xs font-medium text-red-300">
            <span className="relative flex h-2 w-2">
              <span className="animate-pulse-glow absolute inline-flex h-full w-full rounded-full bg-red-400 opacity-75" />
              <span className="relative inline-flex rounded-full h-2 w-2 bg-red-400" />
            </span>
            <span>{HERO.badgeLimited}</span>
          </span>
        </div>

        <h1 className="text-display-1 text-white text-balance mx-auto">
          {HERO.titleStart}{" "}
          <span className="text-gradient-animated">{HERO.titleAccent}</span>
        </h1>

        <p className="text-body-lg text-white/95 font-semibold mt-7">{HERO.subtitleTop}</p>

        <p className="text-body-lg text-zinc-300/90 mt-5 max-w-[62ch] mx-auto">
          {HERO.subtitle}
        </p>

        <div className="flex flex-col sm:flex-row items-center justify-center gap-4 mt-8">
          <button
            type="button"
            onClick={() => openBooking("general")}
            className="group inline-flex items-center gap-2 px-7 py-3.5 rounded-xl bg-gradient-to-r from-rose-500 to-orange-500 text-white font-semibold text-sm shadow-[0_4px_24px_-8px_rgba(251,146,60,0.55)] hover:shadow-[0_0_50px_-8px_rgba(251,146,60,0.55)] hover:scale-[1.02] active:scale-100 transition-all"
          >
            <Calendar className="w-4 h-4" />
            {HERO.ctaPrimary}
            <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
          </button>
          <a
            href="#risultati"
            className="inline-flex items-center gap-2 px-6 py-3.5 rounded-xl surface-glass border border-white/15 text-sm font-medium text-zinc-200 hover:border-white/30 hover:text-white transition-all"
          >
            {HERO.ctaSecondary}
          </a>
        </div>

        <div className="mt-10 flex flex-wrap items-center justify-center gap-4 text-sm text-zinc-400">
          {HERO.stats.map((stat) => (
            <div
              key={stat.label}
              className="flex items-center gap-2 surface-glass border border-white/10 px-3 py-1.5 rounded-full"
            >
              <span className="text-zinc-200 font-medium">{stat.value}</span>
              <span>{stat.label}</span>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
