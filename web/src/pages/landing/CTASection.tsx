import { Zap, Globe, ArrowRight, CheckCircle2, Target } from "lucide-react";

/* ----------------------------------------------------------------------------
 * CTA Section — 3 paths to automated YouTube income
 * -------------------------------------------------------------------------- */

export function CTASection() {
  const tiers = [
    {
      level: "Level 1",
      tagline: "Guided & fast",
      title: "Start & Earn",
      ringColor: "ring-emerald-400/40",
      iconColor: "text-emerald-300",
      bgGradient: "from-emerald-500/20 to-teal-500/20",
      hoverBorder: "hover:border-emerald-400/30",
      desc: "Go from zero to your first monthly paycheck in 30 days. You get a mentor, a ready-to-monetize channel, and a proven playbook — no camera, no editing, no tech skills.",
      features: [
        "Aged YouTube channel included — skip the algorithm's trust filter",
        "1-on-1 personal mentor on the highest-paying niches",
        "Zero editing — AI templates do all the work for you",
        "Step-by-step roadmap to your first $1,000/mo",
      ],
      cta: "Book Your Mentoring Session",
      ctaLink: "https://discord.com/users/1201477873719050332",
    },
    {
      level: "Level 2",
      tagline: "100% passive",
      title: "Done-For-You",
      ringColor: "ring-blue-400/40",
      iconColor: "text-blue-300",
      bgGradient: "from-blue-500/20 to-cyan-500/20",
      hoverBorder: "hover:border-blue-400/30",
      desc: "No camera, no voice, no time spent. We build, manage, and automate everything. Your channel generates revenue while you sleep.",
      features: [
        "Full-auto management across 7 platforms from day one",
        "5 channels + 10 AI videos included immediately",
        "Revenue from YouTube Shorts, TikTok, Reels & sponsorships",
        "Daily optimized content — you do nothing",
      ],
      cta: "Activate Full Automation",
      ctaLink: "https://discord.com/users/1201477873719050332",
      featured: true,
    },
    {
      level: "Level 3",
      tagline: "Scale & multiply",
      title: "Channel Portfolio",
      ringColor: "ring-violet-400/40",
      iconColor: "text-violet-300",
      bgGradient: "from-violet-500/20 to-purple-500/20",
      hoverBorder: "hover:border-violet-400/30",
      desc: "Turn one monetized channel into a full portfolio. Expand into multiple languages and niches with unlimited AI content and dedicated infrastructure.",
      features: [
        "Portfolio-wide automation and analytics dashboard",
        "Multi-language expansion for global reach",
        "Unlimited AI-generated videos across all channels",
        "Dedicated infrastructure and priority support",
      ],
      cta: "Scale Your Income",
      ctaLink: "https://discord.com/users/1201477873719050332",
    },
  ];

  return (
    <section id="programs" className="relative py-24 sm:py-32 bg-elevated overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora opacity-15 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 text-center mx-auto animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">Choose your path</div>
          <h2 className="text-display-2 text-white">
            Three ways to{" "}
            <span className="text-gradient">build passive income.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch] mx-auto">
            Whether you want to learn the system, have us run everything, or scale a full
            portfolio — the income is real and the path is clear.
          </p>
        </div>
        <div className="grid md:grid-cols-3 gap-6">
          {tiers.map((t, i) => (
            <div
              key={t.level}
              className={`surface-card p-7 relative overflow-hidden animate-fade-up ${t.hoverBorder} transition-all duration-300 group ${
                t.featured ? "ring-1 ring-blue-400/30 shadow-[0_8px_40px_rgba(59,130,246,0.15)]" : ""
              } ${["", "animation-delay-100", "animation-delay-200"][i]}`}
            >
              {t.featured && (
                <div className="absolute top-0 left-0 right-0 h-0.5 bg-gradient-to-r from-blue-500 to-cyan-400" />
              )}
              <div aria-hidden="true" className={`absolute -top-20 -right-20 w-48 h-48 rounded-full bg-radial ${t.bgGradient} opacity-60 blur-3xl pointer-events-none group-hover:opacity-100 transition-all duration-500`} />
              <div className="relative">
                <div className="flex items-center gap-3 mb-4">
                  <div className={`inline-flex w-10 h-10 rounded-xl items-center justify-center ring-1 ${t.ringColor} surface-glass ${t.iconColor}`}>
                    {i === 0 ? <Target className="w-5 h-5" /> : i === 1 ? <Zap className="w-5 h-5" /> : <Globe className="w-5 h-5" />}
                  </div>
                  <div>
                    <div className="text-[10px] font-bold text-zinc-500 uppercase tracking-wider">{t.level}</div>
                    <div className="text-xs text-zinc-400">{t.tagline}</div>
                  </div>
                </div>
                <h3 className="text-display-3 text-white mb-3">{t.title}</h3>
                <p className="text-sm text-zinc-400 leading-relaxed mb-5">{t.desc}</p>
                <ul className="space-y-3 mb-6">
                  {t.features.map((f) => (
                    <li key={f} className="flex items-start gap-2.5">
                      <CheckCircle2 className={`w-4 h-4 shrink-0 mt-0.5 ${t.iconColor}`} />
                      <span className="text-sm text-zinc-300">{f}</span>
                    </li>
                  ))}
                </ul>
                <a
                  href={t.ctaLink}
                  target="_blank"
                  rel="noopener noreferrer"
                  className={`inline-flex items-center gap-2 px-6 py-3 rounded-xl font-semibold text-sm transition-all group/btn ${
                    t.featured
                      ? "bg-white text-black hover:bg-white/90 hover:shadow-[0_0_40px_-8px_rgba(255,255,255,0.3)]"
                      : "surface-glass border border-white/15 text-zinc-200 hover:border-white/30 hover:text-white"
                  }`}
                >
                  {t.cta}
                  <ArrowRight className="w-4 h-4 group-hover/btn:translate-x-1 transition-transform" />
                </a>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
