import { ArrowRight, Clock } from "lucide-react";

/* ----------------------------------------------------------------------------
 * Final CTA — urgency + income promise
 * -------------------------------------------------------------------------- */

export function FinalCTA() {
  return (
    <section id="contact" className="relative py-24 sm:py-32 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 cta-glow opacity-40 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6 text-center">
        <div className="max-w-3xl mx-auto animate-fade-up">
          <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full surface-glass border border-red-400/30 text-xs font-medium text-red-300 mb-6">
            <Clock className="w-3.5 h-3.5" />
            <span>Limited spots — we accept only 10 new students this month to guarantee 1-on-1 support</span>
          </div>
          <h2 className="text-display-1 text-white mb-6">
            Ready to turn YouTube into your{" "}
            <span className="text-gradient">monthly paycheck?</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mb-8 max-w-[52ch] mx-auto">
            Book a free call and we'll map out exactly how you'll reach your first
            $2,000/mo &mdash; even if you have zero experience and zero subscribers today.
          </p>
          <div className="flex flex-col sm:flex-row items-center justify-center gap-4">
            <a
              href="https://discord.com/users/1201477873719050332"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 px-8 py-3.5 rounded-xl bg-white text-black font-semibold text-sm hover:bg-white/90 hover:shadow-[0_0_40px_-8px_rgba(255,255,255,0.3)] transition-all group"
            >
              Start Earning Now
              <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
            </a>
            <a
              href="https://discord.com/users/1201477873719050332"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 px-8 py-3.5 rounded-xl surface-glass border border-white/15 text-sm font-medium text-zinc-200 hover:border-violet-400/50 hover:text-white transition-all"
            >
              Ask a Question on Discord
            </a>
          </div>
        </div>
      </div>
    </section>
  );
}
