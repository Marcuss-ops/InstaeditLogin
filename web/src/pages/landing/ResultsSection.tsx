/* ----------------------------------------------------------------------------
 * Results — Numbers + Testimonials
 * -------------------------------------------------------------------------- */

export function ResultsSection() {
  const stats = [
    { v: "<3 wk", l: "Avg. monetization time", desc: "from channel start to Partner Program" },
    { v: "50+", l: "Channels monetized", desc: "across every major niche" },
    { v: "500+", l: "Videos published", desc: "across YouTube, TikTok, Insta & more" },
    { v: "7", l: "Platforms", desc: "one automated workflow" },
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
            Real channels.{" "}
            <span className="text-gradient-animated">Real monetization.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch] mx-auto">
            Most creators take months to even think about revenue. Our automated
            system is built to hit the YouTube Partner Program and start earning
            in under 3 weeks.
          </p>
        </div>

        {/* Stats */}
        <div className="grid grid-cols-2 lg:grid-cols-4 gap-5 mb-16">
          {stats.map((s, i) => (
            <div
              key={s.l}
              className={`surface-card p-6 text-center animate-fade-up hover:border-violet-400/30 transition-all duration-300 ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300"][i]}`}
            >
              <div className="text-3xl sm:text-4xl font-extrabold text-white tabular-nums tracking-tight">{s.v}</div>
              <div className="text-sm font-medium text-zinc-300 mt-2">{s.l}</div>
              <div className="text-xs text-zinc-500 mt-1">{s.desc}</div>
            </div>
          ))}
        </div>

        {/* Channel results gallery */}
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
