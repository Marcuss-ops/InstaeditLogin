import { Sparkles, Shield } from "lucide-react";
import { IconSchedule, IconAnalyze } from "./shared";

/* ----------------------------------------------------------------------------
 * Features
 * -------------------------------------------------------------------------- */

export function Features() {
  return (
    <section id="features" className="relative py-24 sm:py-32 bg-elevated overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora opacity-25 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">Built for automation</div>
          <h2 className="text-display-2 text-white">A channel engine that runs while you sleep.</h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            From content strategy to multi-platform publishing, every moving part is
            designed to turn a new channel into a monetized channel — fast.
          </p>
        </div>
        <div className="grid lg:grid-cols-3 gap-5">
          <div className="surface-card p-7 lg:p-8 relative overflow-hidden lg:col-span-2 lg:row-span-2 animate-fade-up hover:border-violet-400/30 transition-all duration-300">
            <div aria-hidden="true" className="absolute -top-32 -right-32 w-80 h-80 rounded-full bg-violet-500 blur-3xl opacity-50" />
            <div aria-hidden="true" className="absolute bottom-0 left-0 right-0 h-px bg-gradient-to-r from-transparent via-violet-400/50 to-transparent" />
            <div className="relative">
              <div className="inline-flex w-12 h-12 rounded-xl items-center justify-center ring-1 ring-violet-400/40 surface-glass text-violet-300 mb-5">
                <Sparkles className="w-6 h-6" />
              </div>
              <h3 className="text-display-3 text-white mb-3 max-w-[22ch]">YouTube-first, then everywhere.</h3>
              <p className="text-sm text-zinc-400 leading-relaxed max-w-[52ch]">
                We optimize for the platform that pays: YouTube. Then we repurpose every
                video to TikTok, Instagram, X, LinkedIn, Facebook and Threads — all from a
                single automated workflow.
              </p>
              <div className="mt-7 surface-glass rounded-xl border border-white/10 overflow-hidden">
                <div className="flex items-center gap-1.5 px-4 py-2.5 border-b border-white/5">
                  <span className="w-2.5 h-2.5 rounded-full bg-[#ff5f57]" />
                  <span className="w-2.5 h-2.5 rounded-full bg-[#febc2e]" />
                  <span className="w-2.5 h-2.5 rounded-full bg-[#28c840]" />
                  <span className="ml-3 text-[11px] text-zinc-500">Calendar · this week</span>
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
            <h3 className="text-display-3 text-white mb-2">Auto-publishing engine.</h3>
            <p className="text-sm text-zinc-400 leading-relaxed">
              Videos go live at the best times for views and monetization. No manual
              uploads, no timezone math — just a channel that grows 24/7.
            </p>
          </div>
          <div className="surface-card p-6 relative overflow-hidden animate-fade-up animation-delay-200 hover:border-pink-400/30 transition-all duration-300">
            <div className="inline-flex w-11 h-11 rounded-xl items-center justify-center ring-1 ring-pink-400/40 surface-glass text-pink-300 mb-4">
              <Shield className="w-5 h-5" />
            </div>
            <h3 className="text-display-3 text-white mb-2">Monetization guardrails.</h3>
            <p className="text-sm text-zinc-400 leading-relaxed">
              We track watch-time, subscriber velocity and revenue metrics so your channel
              hits the YouTube Partner Program requirements faster and safer.
            </p>
          </div>
          <div className="surface-card p-6 relative overflow-hidden lg:col-span-3 animate-fade-up animation-delay-300 hover:border-amber-400/30 transition-all duration-300">
            <div aria-hidden="true" className="absolute -bottom-24 -right-24 w-72 h-72 rounded-full bg-amber-500/30 blur-3xl pointer-events-none" />
            <div className="relative grid lg:grid-cols-2 gap-6 items-center">
              <div>
                <div className="inline-flex w-11 h-11 rounded-xl items-center justify-center ring-1 ring-amber-400/40 surface-glass text-amber-300 mb-4">
                  <IconAnalyze className="w-5 h-5" />
                </div>
                <h3 className="text-display-3 text-white mb-2">Revenue-first analytics.</h3>
                <p className="text-sm text-zinc-400 leading-relaxed max-w-[52ch]">
                  Track RPM, CPM and ad-revenue growth in one dashboard. We focus on the
                  metrics that actually pay you — and show you how to push them higher.
                </p>
              </div>
              <div className="surface-glass rounded-xl border border-white/10 p-5">
                <div className="flex items-end justify-between gap-2 h-24">
                  {[42, 64, 38, 78, 56, 92, 70, 88, 60, 96, 74, 84].map((h, i) => (
                    <div key={i} className="flex-1 rounded-t-sm bg-gradient-to-t from-violet-500/60 to-cyan-400/90" style={{ height: `${h}%` }} />
                  ))}
                </div>
                <div className="flex justify-between text-[10px] text-zinc-500 mt-2">
                  <span>Jan</span><span>Mar</span><span>May</span><span>Jul</span><span>Sep</span><span>Nov</span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
