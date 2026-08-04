import { Calendar, ArrowRight } from "lucide-react";
import { useBooking } from "../../components/booking/BookingProvider";
import { FINAL_CTA } from "./content";

/**
 * Chiusura — ultima call to action prima del footer.
 */
export function FinalCTA() {
  const { open: openBooking } = useBooking();

  return (
    <section className="relative py-24 sm:py-32 overflow-hidden">
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="surface-glass border border-white/15 rounded-2xl p-8 lg:p-12 relative overflow-hidden text-center animate-fade-up">
          <div aria-hidden="true" className="absolute inset-0 cta-glow opacity-30 pointer-events-none" />
          <div className="relative">
            <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full surface-glass border border-red-400/30 text-xs font-medium text-red-300 mb-5">
              <Calendar className="w-3.5 h-3.5" />
              <span>{FINAL_CTA.badge}</span>
            </div>
            <h2 className="text-display-2 text-white mb-4">{FINAL_CTA.title}</h2>
            <p className="text-body-lg text-zinc-400 max-w-[55ch] mx-auto mb-8">
              {FINAL_CTA.subtitle}
            </p>
            <div className="flex flex-col sm:flex-row items-center justify-center gap-4">
              <button
                type="button"
                onClick={() => openBooking("general")}
                className="group inline-flex items-center gap-2 px-8 py-3.5 rounded-xl bg-gradient-to-r from-rose-500 to-orange-500 text-white font-semibold text-sm shadow-[0_4px_24px_-8px_rgba(251,146,60,0.55)] hover:shadow-[0_0_50px_-8px_rgba(251,146,60,0.55)] hover:scale-[1.02] active:scale-100 transition-all"
              >
                <Calendar className="w-4 h-4" />
                {FINAL_CTA.cta}
                <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
              </button>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
