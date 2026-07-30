import { ArrowRight, Calendar } from "lucide-react";
import { useBooking } from "../../components/booking/BookingProvider";

/* ----------------------------------------------------------------------------
 * Scale CTA — compact "Ready to scale your agency?" card.
 *
 * CTA now opens the booking modal (Calendly-style strategy-call flow).
 * -------------------------------------------------------------------------- */

export function ScaleCTA() {
  const { open: openBooking } = useBooking();

  return (
    <div className="mt-12 surface-glass border border-white/15 rounded-2xl p-8 relative overflow-hidden text-center animate-fade-up animation-delay-400">
      <div aria-hidden="true" className="absolute inset-0 cta-glow opacity-30 pointer-events-none" />
      <div className="relative">
        <h3 className="text-display-3 text-white mb-3">Ready to scale your agency?</h3>
        <p className="text-sm text-zinc-400 mb-6 max-w-[48ch] mx-auto">
          Unite all your clients on one platform. Reduce publishing time by 80%
          and offer a service your competitors can't match.
        </p>
        <button
          type="button"
          onClick={() => openBooking("growth")}
          className="group inline-flex items-center gap-2 px-8 py-3.5 rounded-xl bg-white text-black font-semibold text-sm hover:bg-white/90 hover:shadow-[0_0_40px_-8px_rgba(255,255,255,0.3)] transition-all"
        >
          <Calendar className="w-4 h-4" />
          Schedule My Free Call
          <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
        </button>
      </div>
    </div>
  );
}
