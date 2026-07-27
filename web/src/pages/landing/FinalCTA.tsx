import { ArrowRight, Calendar, Send } from "lucide-react";
import { useBooking } from "../../components/booking/BookingProvider";
import { CONTACT_DISCORD_HANDLE, CONTACT_DISCORD_URL } from "../../components/editor/shared";

/* ----------------------------------------------------------------------------
 * Final CTA — scarcity + booking funnel.
 *
 * Primary CTA opens the booking modal. Discord is preserved as a
 * visible alt-channel pill so visitors who prefer chat can still
 * reach us — but the funnel goes through the strategy-call flow.
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
            <a
              href={CONTACT_DISCORD_URL}
              target="_blank"
              rel="noopener noreferrer"
              className="group inline-flex items-center gap-2 px-6 py-3.5 rounded-xl surface-glass border border-white/15 text-sm font-medium text-zinc-300 hover:border-white/30 hover:text-white transition-all"
            >
              <Send className="w-3.5 h-3.5 text-sky-300" />
              <span>
                Prefer to chat first?{" "}
                <span className="text-zinc-400 group-hover:text-zinc-200 transition-colors">
                  Discord {CONTACT_DISCORD_HANDLE}
                </span>
              </span>
            </a>
          </div>
        </div>
      </div>
    </section>
  );
}
