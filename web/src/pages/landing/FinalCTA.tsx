import { ArrowRight, Calendar } from "lucide-react";
import { useBooking } from "../../components/booking/BookingProvider";

/* ----------------------------------------------------------------------------
 * Final CTA — scarcity + booking funnel.
 *
 * Primary CTA opens the booking modal. The funnel is single-track:
 * visitors go through the 3-question qualification flow inside the
 * modal, then into the live scheduler. Everything else (email, phone,
 * WhatsApp) lives on the editor contact card, NOT on the marketing page.
 * -------------------------------------------------------------------------- */

export function FinalCTA() {
  const { open: openBooking } = useBooking();

  return (
    <section id="contact" className="relative py-24 sm:py-32 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 cta-glow opacity-40 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6 text-center">
        <div className="max-w-3xl mx-auto animate-fade-up">
          <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full surface-glass border border-red-400/30 text-xs font-medium text-red-300 mb-6">
            <Calendar className="w-3.5 h-3.5" />
            <span>Limited spots — we accept only 10 new students this month to guarantee 1-on-1 support</span>
          </div>
          <h2 className="text-display-1 text-white mb-6">
            Ready to turn YouTube into your{" "}
            <span className="text-gradient">monthly paycheck?</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mb-8 max-w-[52ch] mx-auto">
            Book a free strategy call and we'll map out exactly how you'll
            reach your first $2,000/mo &mdash; even if you have zero
            experience and zero subscribers today.
          </p>
          <div className="flex flex-col sm:flex-row items-center justify-center gap-4">
            <button
              type="button"
              onClick={() => openBooking()}
              className="group inline-flex items-center gap-2 px-8 py-3.5 rounded-xl bg-white text-black font-semibold text-sm hover:bg-white/90 hover:shadow-[0_0_40px_-8px_rgba(255,255,255,0.3)] transition-all"
            >
              <Calendar className="w-4 h-4" />
              Schedule My Free Call
              <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
            </button>
          </div>
        </div>
      </div>
    </section>
  );
}
