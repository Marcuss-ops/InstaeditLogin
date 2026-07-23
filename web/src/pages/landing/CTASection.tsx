import { Zap, Globe, ArrowRight, CheckCircle2, Target } from "lucide-react";

/* ----------------------------------------------------------------------------
 * CTA Section — 3 Paths to YouTube income
 * -------------------------------------------------------------------------- */

export function CTASection() {
  const tiers = [
    {
      level: "Level 1",
      tagline: "Learn the system",
      title: "YouTube Mentorship",
      ringColor: "ring-emerald-400/40",
      iconColor: "text-emerald-300",
      bgGradient: "from-emerald-500/20 to-teal-500/20",
      hoverBorder: "hover:border-emerald-400/30",
      desc: "Learn how to build, automate and monetize an English-language YouTube channel from scratch. Get 1-on-1 mentoring and an aged channel so you skip the trust-filter phase.",
      features: [
        "1-on-1 mentorship on niche, strategy and monetization",
        "Aged English-language YouTube channel included",
        "Automation playbook for content, SEO and publishing",
        "Roadmap to first online income in under 3 weeks",
      ],
      cta: "Start learning",
      ctaLink: "https://discord.com/users/1201477873719050332",
    },
    {
      level: "Level 2",
      tagline: "We run it for you",
      title: "Done-For-You Channel",
      ringColor: "ring-blue-400/40",
      iconColor: "text-blue-300",
      bgGradient: "from-blue-500/20 to-cyan-500/20",
      hoverBorder: "hover:border-blue-400/30",
      desc: "You bring the niche and the budget; we build, manage and automate your English-language YouTube channel. Content, publishing and optimization run hands-free while revenue starts coming in.",
      features: [
        "Done-for-you channel build + monetization strategy",
        "Automated publishing and content cadence",
        "Daily posts optimized for views and revenue",
        "5-channel launch pack to scale fast",
      ],
      cta: "Get a channel built",
      ctaLink: "https://discord.com/users/1201477873719050332",
      featured: true,
    },
    {
      level: "Level 3",
      tagline: "Scale your income",
      title: "Channel Portfolio",
      ringColor: "ring-violet-400/40",
      iconColor: "text-violet-300",
      bgGradient: "from-violet-500/20 to-purple-500/20",
      hoverBorder: "hover:border-violet-400/30",
      desc: "Turn one monetized channel into a portfolio. Expand into multiple English-language channels and markets with unlimited AI-generated content and dedicated infrastructure.",
      features: [
        "Portfolio-wide automation and analytics",
        "Translation and localization for global reach",
        "Unlimited AI-generated videos through our engine",
        "Dedicated infrastructure and priority support",
      ],
      cta: "Scale with us",
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
            <span className="text-gradient">earn with YouTube.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch] mx-auto">
            Whether you want to learn, have us run the channel, or scale a portfolio
            of English-language channels — there is a path for your goal.
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
