import { DollarSign } from "lucide-react";

/* ----------------------------------------------------------------------------
 * EarningsEstimates — how much you can earn
 * -------------------------------------------------------------------------- */

export function EarningsEstimates() {
  const tiers = [
    {
      label: "1 Automated Channel",
      range: "$500 – $1,500",
      period: "/mo",
      desc: "A single AI-managed channel in a profitable niche",
      color: "text-emerald-400",
      ring: "ring-emerald-400/40",
    },
    {
      label: "3 Channels (Multi-language)",
      range: "$2,000 – $5,000",
      period: "/mo",
      desc: "Multiple channels across English, Spanish & Portuguese",
      color: "text-blue-400",
      ring: "ring-blue-400/40",
      featured: true,
    },
    {
      label: "Channel Portfolio (Level 3)",
      range: "$10,000+",
      period: "/mo",
      desc: "Full network of monetized channels with global reach",
      color: "text-violet-400",
      ring: "ring-violet-400/40",
    },
  ];

  return (
    <section className="relative py-24 sm:py-32 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-emerald-500 w-[400px] h-[400px] top-0 -right-24 animate-drift-slow opacity-15" />
      </div>
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 text-center mx-auto animate-fade-up">
          <div className="text-eyebrow text-emerald-300/90 mb-3">Earnings</div>
          <h2 className="text-display-2 text-white">
            How much can{" "}
            <span className="text-gradient">you earn?</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch] mx-auto">
            Realistic income ranges based on our current students. The more channels
            you automate, the more revenue you generate.
          </p>
        </div>

        <div className="grid md:grid-cols-3 gap-6 max-w-4xl mx-auto">
          {tiers.map((t, i) => (
            <div
              key={t.label}
              className={`surface-card p-7 text-center animate-fade-up transition-all duration-300 ${
                t.featured ? "ring-1 ring-blue-400/30 shadow-[0_8px_40px_rgba(59,130,246,0.12)]" : ""
              } ${["", "animation-delay-100", "animation-delay-200"][i]}`}
            >
              <div className={`inline-flex w-12 h-12 rounded-xl items-center justify-center ring-1 ${t.ring} surface-glass ${t.color} mb-4`}>
                <DollarSign className="w-6 h-6" />
              </div>
              <div className="text-sm text-zinc-400 mb-2">{t.label}</div>
              <div className="flex items-baseline justify-center gap-1">
                <span className={`text-3xl font-extrabold ${t.color}`}>{t.range}</span>
                <span className="text-sm text-zinc-500">{t.period}</span>
              </div>
              <p className="text-xs text-zinc-500 mt-3">{t.desc}</p>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
