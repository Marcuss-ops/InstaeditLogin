import { ArrowRight, Zap, Clock, Bot } from "lucide-react";
import { DashboardMockup } from "./shared";

/* ----------------------------------------------------------------------------
 * Hero — gain-focused: immediate income, easy, guided
 * -------------------------------------------------------------------------- */

export function Hero() {
  return (
    <section className="relative pt-32 pb-20 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora pointer-events-none" />
      <div aria-hidden="true" className="absolute inset-0 grid-bg pointer-events-none opacity-60" />
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-violet-500 w-[460px] h-[460px] -top-32 -left-24 animate-drift-slow opacity-70" />
        <div className="glow-orb bg-cyan-400 w-[420px] h-[420px] -bottom-40 -right-24 animate-drift-rev opacity-60" />
      </div>

      <div className="relative mx-auto max-w-7xl px-6 grid lg:grid-cols-12 gap-10 items-center">
        <div className="lg:col-span-7 xl:col-span-6 animate-fade-up">
          <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full surface-glass border border-white/15 text-xs font-medium text-zinc-200 mb-7">
            <span className="relative flex h-2 w-2">
              <span className="animate-pulse-glow absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
              <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-400" />
            </span>
            <span>Turnkey system &mdash; zero experience needed</span>
          </div>

          <h1 className="text-display-1 text-white">
            Your First $2,000/Mo From Video{" "}
            <span className="text-gradient-animated">On Autopilot &mdash; No Experience Needed.</span>
          </h1>

          <p className="text-body-lg text-zinc-300/90 mt-7 max-w-[62ch]">
            Stop wasting months figuring out the algorithm. We give you an
            already-monetized YouTube channel, AI that creates the videos for you,
            and 1-on-1 coaching to get you to your first payout &mdash; fast.
          </p>

          <div className="flex flex-col sm:flex-row items-start sm:items-center gap-4 mt-8">
            <a
              href="https://discord.com/users/1201477873719050332"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 px-7 py-3.5 rounded-xl bg-white text-black font-semibold text-sm hover:bg-white/90 hover:shadow-[0_0_40px_-8px_rgba(255,255,255,0.3)] transition-all group"
            >
              Get Your Monetized Channel
              <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
            </a>
            <a
              href="#proof"
              className="inline-flex items-center gap-2 px-6 py-3.5 rounded-xl surface-glass border border-white/15 text-sm font-medium text-zinc-200 hover:border-white/30 hover:text-white transition-all"
            >
              See Real Results
            </a>
          </div>

          <div className="mt-10 flex flex-wrap items-center gap-4 text-sm text-zinc-400">
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
              <span className="text-zinc-200 font-medium">100%</span>
              <span>AI-automated</span>
            </div>
          </div>
        </div>

        <div className="lg:col-span-5 xl:col-span-6 relative">
          <DashboardMockup />
        </div>
      </div>
    </section>
  );
}
