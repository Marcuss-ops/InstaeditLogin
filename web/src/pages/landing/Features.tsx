import {
  Bot,
  DollarSign,
} from "lucide-react";
import { IconSchedule } from "../../components/landing/icons";

/* ----------------------------------------------------------------------------
 * Features — semi-automated pipeline, maximum income (gain-focused)
 * -------------------------------------------------------------------------- */

export function Features() {
  return (
    <section id="features" className="relative py-24 sm:py-32 bg-elevated overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora opacity-25 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">How it works</div>
          <h2 className="text-display-2 text-white">Semi-automated. Maximum income.</h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            No camera. No editing software. No experience. Our 90% AI-executed system turns a single
            idea into daily content across 7 platforms &mdash; engineered to generate revenue.
          </p>
        </div>
        <div className="grid lg:grid-cols-3 gap-5">
          <div className="surface-card p-7 lg:p-8 relative overflow-hidden lg:col-span-2 lg:row-span-2 animate-fade-up hover:border-violet-400/30 transition-all duration-300">
            <div aria-hidden="true" className="absolute -top-32 -right-32 w-80 h-80 rounded-full bg-violet-500 blur-3xl opacity-50" />
            <div aria-hidden="true" className="absolute bottom-0 left-0 right-0 h-px bg-gradient-to-r from-transparent via-violet-400/50 to-transparent" />
            <div className="relative">
              <div className="inline-flex w-12 h-12 rounded-xl items-center justify-center ring-1 ring-violet-400/40 surface-glass text-violet-300 mb-5">
                <Bot className="w-6 h-6" />
              </div>
              <h3 className="text-display-3 text-white mb-3 max-w-[22ch]">AI creates. You earn.</h3>
              <p className="text-sm text-zinc-400 leading-relaxed max-w-[52ch]">
                Type one sentence and ChronoN AI generates a professional,
                monetization-ready video. No camera, no microphone, no editing software.
                The AI handles everything from script to final render.
              </p>
              <div className="mt-7 surface-glass rounded-xl border border-white/10 overflow-hidden">
                <div className="flex items-center gap-1.5 px-4 py-2.5 border-b border-white/5">
                  <span className="w-2.5 h-2.5 rounded-full bg-[#ff5f57]" />
                  <span className="w-2.5 h-2.5 rounded-full bg-[#febc2e]" />
                  <span className="w-2.5 h-2.5 rounded-full bg-[#28c840]" />
                  <span className="ml-3 text-[11px] text-zinc-500">Autopilot · this week</span>
                </div>
                <div className="grid grid-cols-7 gap-1.5 p-3 text-center text-[10px] text-zinc-500">
                  {["M", "T", "W", "T", "F", "S", "S"].map((d, idx) => (
                    <div key={`${d}${idx}`} className="rounded-md border border-white/5 bg-black/20 py-2.5">
                      <div className="text-eyebrow text-zinc-600 mb-1.5">{d}</div>
                      <div className="space-y-1">
                        {[1, 2].slice(0, idx % 2 === 0 ? 2 : 1).map((i) => (
                          <div key={i} className={`h-1.5 rounded-full mx-1 ${i === 1 ? "bg-violet-400/70" : "bg-cyan-400/70"}`} />
                        ))}
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </div>
          </div>
          <div className="surface-card p-6 relative overflow-hidden animate-fade-up animation-delay-100 hover:border-cyan-400/30 transition-all duration-300">
            <div className="inline-flex w-11 h-11 rounded-xl items-center justify-center ring-1 ring-cyan-400/40 surface-glass text-cyan-300 mb-4">
              <IconSchedule className="w-5 h-5" />
            </div>
            <h3 className="text-display-3 text-white mb-2">7 platforms, 1 video.</h3>
            <p className="text-sm text-zinc-400 leading-relaxed">
              One AI-generated video is automatically converted and published to
              YouTube, TikTok, Instagram Reels, Facebook, X and more &mdash;
              multiplying your reach by 7x.
            </p>
          </div>
          <div className="surface-card p-6 relative overflow-hidden animate-fade-up animation-delay-200 hover:border-pink-400/30 transition-all duration-300">
            <div className="inline-flex w-11 h-11 rounded-xl items-center justify-center ring-1 ring-pink-400/40 surface-glass text-pink-300 mb-4">
              <DollarSign className="w-5 h-5" />
            </div>
            <h3 className="text-display-3 text-white mb-2">Revenue from day one.</h3>
            <p className="text-sm text-zinc-400 leading-relaxed">
              Our Fast-Track Partner Monetization setup gets your content through
              the Partner Program review window in days, not months — so you
              start earning ad revenue, sponsorships and affiliate income sooner.
            </p>
          </div>

        </div>
      </div>
    </section>
  );
}
