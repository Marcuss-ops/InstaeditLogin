import { DollarSign, Clock, Users, BarChart3 } from "lucide-react";

/* ----------------------------------------------------------------------------
 * Results — income-focused stats + screenshot-style testimonials
 * -------------------------------------------------------------------------- */

export function ResultsSection() {
  const stats = [
    { v: "$2,150", l: "Avg. student income", desc: "per month, per channel", icon: DollarSign, color: "text-emerald-400" },
    { v: "14 days", l: "Avg. first payout", desc: "from channel start", icon: Clock, color: "text-blue-400" },
    { v: "50+", l: "Channels monetized", desc: "and generating revenue", icon: Users, color: "text-violet-400" },
    { v: "100%", l: "AI-automated", desc: "zero editing required", icon: BarChart3, color: "text-amber-400" },
  ];

  const testimonials = [
    {
      quote: "Day 20 I hit my first monetization thanks to the aged channel and my mentor's guidance. I never thought it could be this fast.",
      author: "Marcus T.",
      role: "Start & Earn student",
      badge: "First payout: Day 20",
      badgeColor: "text-emerald-400",
    },
    {
      quote: "I went from zero to $1,800/mo in 6 weeks. The AI does all the editing — I just approve the scripts. It's genuinely passive.",
      author: "Sarah L.",
      role: "Done-For-You member",
      badge: "Current income: $1,800/mo",
      badgeColor: "text-blue-400",
    },
    {
      quote: "The aged channel was the game-changer. My videos got indexed in hours instead of weeks. I hit 1,000 subs in 12 days.",
      author: "David K.",
      role: "Start & Earn student",
      badge: "1K subs in 12 days",
      badgeColor: "text-violet-400",
    },
    {
      quote: "I manage 4 channels now, all automated. Portfolio-level is where the real money starts. Each one pays for itself in week one.",
      author: "Ana R.",
      role: "Channel Portfolio member",
      badge: "4 channels live",
      badgeColor: "text-amber-400",
    },
  ];

  const channels = [
    { img: "/results/result-1.jpg", alt: "YouTube channel growth result" },
    { img: "/results/result-2.jpg", alt: "Content strategy result" },
    { img: "/results/result-3.jpg", alt: "Channel monetization result" },
    { img: "/results/result-4.jpg", alt: "Video performance result" },
    { img: "/results/result-5.jpg", alt: "Creator growth result" },
    { img: "/results/result-6.jpg", alt: "Multi-platform result" },
  ];

  return (
    <section id="results" className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora opacity-15 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 text-center mx-auto animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">Results</div>
          <h2 className="text-display-2 text-white">
            Real people.{" "}
            <span className="text-gradient">Real income.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch] mx-auto">
            Most creators spend months earning nothing. Our students hit their first
            payout in under two weeks and build a recurring monthly income on autopilot.
          </p>
        </div>

        {/* Stats */}
        <div className="grid grid-cols-2 lg:grid-cols-4 gap-5 mb-16">
          {stats.map((s, i) => (
            <div
              key={s.l}
              className={`surface-card p-6 text-center animate-fade-up hover:border-violet-400/30 transition-all duration-300 ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300"][i]}`}
            >
              <s.icon className={`w-6 h-6 mx-auto mb-3 ${s.color}`} />
              <div className="text-3xl sm:text-4xl font-extrabold text-white tabular-nums tracking-tight">{s.v}</div>
              <div className="text-sm font-medium text-zinc-300 mt-2">{s.l}</div>
              <div className="text-xs text-zinc-500 mt-1">{s.desc}</div>
            </div>
          ))}
        </div>

        {/* Screenshot-style testimonials */}
        <div className="grid md:grid-cols-2 gap-5 mb-16">
          {testimonials.map((t, i) => (
            <div
              key={t.author}
              className={`surface-card p-6 relative overflow-hidden animate-fade-up hover:border-violet-400/30 hover:shadow-[0_8px_32px_rgba(139,92,246,0.12)] transition-all duration-300 ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300"][i]}`}
            >
              <div aria-hidden="true" className="absolute top-0 left-0 right-0 h-0.5 bg-gradient-to-r from-violet-500/60 to-cyan-400/60" />
              <div className="flex items-center justify-between mb-4">
                <div className="flex items-center gap-3">
                  <div className="w-10 h-10 rounded-full bg-gradient-to-br from-violet-500 to-cyan-500 flex items-center justify-center text-white text-sm font-semibold">
                    {t.author.charAt(0)}
                  </div>
                  <div>
                    <div className="text-sm font-semibold text-white">{t.author}</div>
                    <div className="text-xs text-zinc-500">{t.role}</div>
                  </div>
                </div>
                <span className={`text-[10px] font-bold px-2.5 py-1 rounded-full surface-glass border border-white/10 ${t.badgeColor}`}>
                  {t.badge}
                </span>
              </div>
              <div className="surface-glass rounded-xl border border-white/10 p-4 mb-4">
                <div className="flex items-center gap-2 mb-3">
                  <div className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse-glow" />
                  <span className="text-[10px] text-zinc-500 font-medium">YouTube Studio · Earnings</span>
                </div>
                <div className="flex items-baseline gap-2">
                  <span className="text-2xl font-extrabold text-white tabular-nums">$2,150.00</span>
                  <span className="text-[10px] text-zinc-500">this month</span>
                </div>
                <div className="flex items-end gap-1 h-12 mt-3">
                  {[30, 45, 35, 55, 42, 68, 52, 75, 60, 82, 70, 88].map((h, j) => (
                    <div key={j} className="flex-1 rounded-t-sm bg-gradient-to-t from-violet-500/40 to-emerald-400/80" style={{ height: `${h}%` }} />
                  ))}
                </div>
              </div>
              <p className="text-sm text-zinc-300 leading-relaxed italic">&ldquo;{t.quote}&rdquo;</p>
            </div>
          ))}
        </div>

        {/* Channel results image gallery */}
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-5">
          {channels.map((ch, i) => (
            <div
              key={ch.img}
              className={`surface-card overflow-hidden animate-fade-up hover:border-violet-400/30 hover:shadow-[0_8px_32px_rgba(139,92,246,0.12)] transition-all duration-300 group ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300", "animation-delay-400", "animation-delay-500"][i]}`}
            >
              <div className="relative overflow-hidden">
                <img
                  src={ch.img}
                  alt={ch.alt}
                  className="w-full h-auto object-cover group-hover:scale-105 transition-transform duration-500"
                  loading="lazy"
                />
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
