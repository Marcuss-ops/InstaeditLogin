import {
  Lightbulb, Film, Cpu, CalendarClock, Globe, BarChart3, Languages, RefreshCw, ArrowRight, Palette
} from "lucide-react";
/* ----------------------------------------------------------------------------
 * AI Pipeline Visualization — "From idea to publication"
 * -------------------------------------------------------------------------- */

export function Pipeline() {
  const steps = [
    { icon: Lightbulb, label: "Idea", desc: "Tell us what niche or topic you want to dominate — we plan the strategy", color: "from-violet-500 to-purple-500" },
    { icon: Film, label: "Create", desc: "Record your video or let ChronoN generate it — no camera needed", color: "from-blue-500 to-cyan-500" },
    { icon: Cpu, label: "AI Polishes", desc: "Subtitles, thumbnails, captions — everything optimized for maximum views", color: "from-emerald-500 to-teal-500" },
    { icon: CalendarClock, label: "Schedule", desc: "Post at the exact times your audience is online — more views, more revenue", color: "from-amber-500 to-orange-500" },
    { icon: Globe, label: "Publish", desc: "One click → YouTube, TikTok, Instagram and more — maximum reach instantly", color: "from-pink-500 to-rose-500" },
    { icon: BarChart3, label: "Earn", desc: "Track views, subscribers, and revenue — know exactly what's making money", color: "from-indigo-500 to-violet-500" },
  ];

  return (
    <section id="pipeline" className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora opacity-20 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">How it works</div>
          <h2 className="text-display-2 text-white">
            From idea to{" "}
            <span className="text-gradient-animated">revenue.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            Create content once, publish everywhere, and start earning — all in
            minutes, not months. No editing skills needed. No team required.
          </p>
        </div>

        {/* Flow visualization — horizontal on lg, vertical on mobile */}
        <div className="relative">
          {/* Connecting line */}
          <div aria-hidden="true" className="hidden lg:block absolute top-[72px] left-0 right-0 h-0.5 bg-gradient-to-r from-violet-500/40 via-cyan-400/40 to-pink-500/40 pointer-events-none" />

          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-6 gap-4 lg:gap-3">
            {steps.map((s, i) => (
              <div key={s.label} className={`relative animate-fade-up ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300", "animation-delay-400", "animation-delay-500"][i]}`}>
                {/* Step number badge */}
                <div className="flex lg:flex-col items-center lg:items-center gap-4 lg:gap-3 p-4 lg:p-5 surface-card hover:border-violet-400/30 hover:shadow-[0_8px_32px_rgba(139,92,246,0.15)] transition-all duration-300 group">
                  <div className={`relative w-12 h-12 lg:w-14 lg:h-14 rounded-xl bg-gradient-to-br ${s.color} flex items-center justify-center shrink-0 group-hover:scale-110 transition-transform duration-300 shadow-lg`}>
                    <s.icon className="w-5 h-5 lg:w-6 h-6 text-white" />
                    {/* Pulse ring */}
                    <div className="absolute inset-0 rounded-xl ring-2 ring-white/20 animate-pulse-glow opacity-0 group-hover:opacity-100 transition-opacity" />
                  </div>
                  <div className="lg:text-center">
                    <div className="flex items-center gap-2 lg:justify-center">
                      <span className="text-[10px] font-bold text-zinc-500 tabular-nums">0{i + 1}</span>
                      <h3 className="text-sm font-bold text-white">{s.label}</h3>
                    </div>
                    <p className="text-[11px] text-zinc-400 mt-1 leading-relaxed lg:text-center">{s.desc}</p>
                  </div>
                </div>
                {/* Arrow between steps (mobile) */}
                {i < steps.length - 1 && (
                  <div aria-hidden="true" className="lg:hidden flex justify-center py-1">
                    <ArrowRight className="w-4 h-4 text-zinc-600 rotate-90" />
                  </div>
                )}
              </div>
            ))}
          </div>
        </div>

        {/* AI automation highlights */}
        <div className="mt-16 grid grid-cols-1 sm:grid-cols-3 gap-4 animate-fade-up animation-delay-600">
          {[
            { icon: Languages, title: "50+ languages", desc: "Reach audiences worldwide with automatic subtitle translation" },
            { icon: Palette, title: "AI Thumbnails", desc: "Eye-catching thumbnails generated for every platform" },
            { icon: RefreshCw, title: "Auto repurposing", desc: "Turn one long video into Shorts, Reels, and TikToks instantly" },
          ].map((h) => (
            <div key={h.title} className="surface-card-soft p-4 flex items-center gap-3 hover:border-white/20 transition-colors">
              <div className="w-10 h-10 rounded-lg bg-gradient-to-br from-violet-500/20 to-cyan-500/20 flex items-center justify-center text-violet-300 shrink-0">
                <h.icon className="w-5 h-5" />
              </div>
              <div>
                <div className="text-sm font-semibold text-white">{h.title}</div>
                <div className="text-[11px] text-zinc-500">{h.desc}</div>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}


