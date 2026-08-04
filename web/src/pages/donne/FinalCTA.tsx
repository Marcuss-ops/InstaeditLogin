import { ArrowRight, Calendar, ShieldCheck } from "lucide-react";
import { useBooking } from "../../components/booking/BookingProvider";
import { FINAL_CTA } from "./content";

/**
 * Sezione finale — ultima chiamata all'azione con prenotazione.
 */
export function FinalCTA() {
  const { open: openBooking } = useBooking();

  return (
    <section className="relative py-24 sm:py-32 overflow-hidden bg-[#F9F8F6]">
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="absolute w-[460px] h-[460px] top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 rounded-full bg-[#F4E4D3] blur-[130px] opacity-60" />
      </div>
      <div className="relative mx-auto max-w-4xl px-6 text-center animate-fade-up">
        <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full bg-white border border-[#E2D8E6] text-xs font-medium text-[#6B4E71] mb-6">
          <ShieldCheck className="w-3.5 h-3.5 text-[#6E9E68]" />
          {FINAL_CTA.badge}
        </div>
        <h2 className="text-display-1 text-[#4A3E56] text-balance">
          {FINAL_CTA.titleStart}{" "}
          <span className="text-gradient-warm">{FINAL_CTA.titleAccent}</span>
        </h2>
        <p className="text-body-lg text-[#6E6677] mt-6 max-w-[54ch] mx-auto">
          {FINAL_CTA.subtitle}
        </p>
        <div className="flex flex-col sm:flex-row items-center justify-center gap-4 mt-9">
          <button
            type="button"
            onClick={() => openBooking("general")}
            className="group inline-flex items-center gap-2 px-8 py-4 rounded-xl bg-gradient-to-r from-[#E07A5F] to-[#E28743] text-white font-semibold text-sm shadow-[0_10px_28px_-10px_rgba(224,122,95,0.8)] hover:shadow-[0_16px_38px_-10px_rgba(226,135,67,0.85)] hover:scale-[1.02] active:scale-100 transition-all"
          >
            <Calendar className="w-4 h-4" />
            {FINAL_CTA.cta}
            <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
          </button>
          <a
            href="#come-funziona"
            className="inline-flex items-center gap-2 px-6 py-4 rounded-xl bg-white border border-[#E2D8E6] text-sm font-medium text-[#6B4E71] hover:border-[#C9B9CE] hover:text-[#4A3E56] transition-all"
          >
            {FINAL_CTA.linkSecondary}
          </a>
        </div>
        <p className="text-sm text-[#7A7280] mt-6">{FINAL_CTA.smallPrint}</p>
      </div>
    </section>
  );
}
