import { ArrowRight } from "lucide-react";

/* ----------------------------------------------------------------------------
 * Final CTA before footer
 * -------------------------------------------------------------------------- */

export function FinalCTA() {
  return (
    <section id="contact" className="relative py-24 sm:py-32 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 cta-glow opacity-40 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6 text-center">
        <div className="max-w-3xl mx-auto animate-fade-up">
          <h2 className="text-display-1 text-white mb-6">
            Ready to turn YouTube into your{" "}
            <span className="text-gradient">next income stream?</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mb-8 max-w-[52ch] mx-auto">
            Book a free call and we will map out your English-language YouTube
            channel — from niche to first revenue — in under 3 weeks.
          </p>
          <div className="flex flex-col sm:flex-row items-center justify-center gap-4">
            <a
              href="https://discord.com/users/1201477873719050332"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 px-8 py-3.5 rounded-xl bg-white text-black font-semibold text-sm hover:bg-white/90 hover:shadow-[0_0_40px_-8px_rgba(255,255,255,0.3)] transition-all group"
            >
              Book your free call
              <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
            </a>
            <a
              href="https://discord.com/users/1201477873719050332"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 px-8 py-3.5 rounded-xl surface-glass border border-white/15 text-sm font-medium text-zinc-200 hover:border-violet-400/50 hover:text-white transition-all"
            >
              Ask a question on Discord
            </a>
          </div>
        </div>
      </div>
    </section>
  );
}
