import { ArrowRight, Calendar, Clock, Zap, Bot } from "lucide-react";
import { useBooking } from "../../components/booking/BookingProvider";

/* ----------------------------------------------------------------------------
 * Hero — gain-focused: immediate income, easy, guided. Title is centered
 * to anchor first-3-sec attention on the income promise; supporting copy,
 * CTAs, and the trust-stat row sit beneath it on a single centered column.
 *
 * Primary CTA is the booking modal (Calendly-style scheduling flow
 * with a 3-question qualification form). The funnel moves visitors
 * through qualification before any human contact.
 *
 * Layout: centered hero copy keeps the first viewport focused on the
 * promise, while revenue proof appears in the ResultsSection below.
 * -------------------------------------------------------------------------- */

export function Hero() {
  const { open: openBooking } = useBooking();

  return (
    <section className="relative pt-32 pb-24 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora pointer-events-none" />
      <div aria-hidden="true" className="absolute inset-0 grid-bg pointer-events-none opacity-60" />
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-violet-500 w-[460px] h-[460px] -top-32 -left-24 animate-drift-slow opacity-70" />
        <div className="glow-orb bg-cyan-400 w-[420px] h-[420px] -bottom-40 -right-24 animate-drift-rev opacity-60" />
      </div>

      <div className="relative mx-auto max-w-4xl px-6 text-center animate-fade-up">
        <div className="flex flex-wrap items-center justify-center gap-2 mb-7">
          <span className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full surface-glass border border-emerald-400/30 text-xs font-medium text-emerald-200">
            <span className="relative flex h-2 w-2">
              <span className="animate-pulse-glow absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
              <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-400" />
            </span>
            <span>Semi-automated pipeline &mdash; 90% AI-executed</span>
          </span>
          <span className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-full surface-glass border border-red-400/30 text-xs font-medium text-red-300">
            <Clock className="w-3 h-3" />
            <span>Limited &mdash; 10 new clients this month</span>
          </span>
        </div>

        <h1 className="text-display-1 text-white text-balance mx-auto">
          Your First $2,000/Mo From Video{" "}
          <span className="text-gradient-animated">On Autopilot. No Camera. No Editing.</span>
        </h1>

        <p className="text-body-lg text-zinc-300/90 mt-7 max-w-[62ch] mx-auto">
          Stop wasting months figuring out the algorithm. Our AI produces
          the videos, our partner-program setup gets you monetized fast,
          and 1-on-1 coaching takes you to your first payout &mdash; predictably.
        </p>

        <div className="flex flex-col sm:flex-row items-center justify-center gap-4 mt-8">
          <button
            type="button"
            onClick={() => openBooking()}
            className="group inline-flex items-center gap-2 px-7 py-3.5 rounded-xl bg-gradient-to-r from-orange-500 via-red-500 to-pink-500 text-white font-semibold text-sm shadow-[0_4px_24px_-8px_rgba(239,68,68,0.6)] hover:shadow-[0_0_50px_-8px_rgba(239,68,68,0.55)] hover:scale-[1.02] active:scale-100 transition-all [text-shadow:0_1px_2px_rgba(0,0,0,0.45),0_0_12px_rgba(0,0,0,0.25)]"
          >
            <Calendar className="w-4 h-4" />
            Schedule Your Free Strategy Call
            <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
          </button>
          <a
            href="#proof"
            className="inline-flex items-center gap-2 px-6 py-3.5 rounded-xl surface-glass border border-white/15 text-sm font-medium text-zinc-200 hover:border-white/30 hover:text-white transition-all"
          >
            See Real Results
          </a>
        </div>

        <div className="mt-10 flex flex-wrap items-center justify-center gap-4 text-sm text-zinc-400">
          <div className="flex items-center gap-2 surface-glass border border-white/10 px-3 py-1.5 rounded-full">
            <Zap className="w-4 h-4 text-emerald-400" />
            <span className="text-zinc-200 font-medium">$2,150/mo</span>
            <span>avg. student income</span>
          </div>
          <div className="flex items-center gap-2 surface-glass border border-white/10 px-3 py-1.5 rounded-full">
            <Clock className="w-4 h-4 text-blue-400" />
            <span className="text-zinc-200 font-medium">14 days</span>
            <span>avg. first payout</span>
          </div>
          <div className="flex items-center gap-2 surface-glass border border-white/10 px-3 py-1.5 rounded-full">
            <Bot className="w-4 h-4 text-violet-400" />
            <span className="text-zinc-200 font-medium">90%</span>
            <span>AI-automated execution</span>
          </div>
        </div>
      </div>
    </section>
  );
}
