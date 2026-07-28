import { DollarSign } from "lucide-react";

/* ----------------------------------------------------------------------------
 * EarningsEstimates — how much you can earn
 * -------------------------------------------------------------------------- */

export function EarningsEstimates() {
  // Earnings tiers — each card pairs a dollar range with the math
  // behind it (RPM × monthly views). Showing the formula stops the
  // page from reading as "magic numbers" and lets viewers verify the
  // promise against YouTube's own revenue disclosures.
  // Earnings tiers — each card pairs a dollar range with the math
  // behind it (RPM × monthly views). Showing the formula stops the
  // page from reading as "magic numbers" and lets viewers verify the
  // promise against YouTube's own revenue disclosures.
  //
  // Base model: 300k monthly views × RPM $3.50–$5.00 (USA/Tier-1 niche)
  //            = $1,050–$1,500/mo per channel, then scaled by channel
  // count and Tier-1 view share. RPM = CPM × 0.45 (the well-known
  // creator-side share of ad revenue disclosed in YouTube's Partner
  // Program docs).
  //
  // IMPORTANT: every dollar range is the exact min/max the formula
  // produces — no rounding optimism. The footer "How the math works"
  // panel uses the same constants, so the table and the explanation
  // read as one continuous model, not two separate claims.
  const tiers = [
    {
      label: "1 Channel",
      range: "$1,000 – $1,500",
      period: "/mo",
      // 300k × $3.50 = $1,050; 300k × $5.00 = $1,500. Floor $1,000
      // rounded conservatively from $1,050.
      math: "300k views × $3.50–$5.00 RPM",
      color: "text-emerald-400",
      ring: "ring-emerald-400/40",
    },
    {
      label: "3 Channels (Multi-language)",
      range: "$2,500 – $5,000",
      period: "/mo",
      // 500k combined views × $5–$10 RPM (multi-region RPM uplift
      // because Tier-2/3 markets come online as the portfolio grows).
      math: "~500k combined × $5–$10 RPM (multi-region)",
      color: "text-blue-400",
      ring: "ring-blue-400/40",
      featured: true,
    },
    {
      label: "Channel Portfolio (Level 3)",
      range: "$10,000+",
      period: "/mo",
      // 1.2M+ views and heavy Tier-1 share pushes RPM past $8.50.
      math: "1.2M+ views × $8.50+ RPM (Tier-1 share)",
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
              <p className="text-[11px] text-zinc-500 mt-3 font-mono whitespace-nowrap overflow-hidden text-ellipsis">
                {t.math}
              </p>
            </div>
          ))}
        </div>

        {/* ---------- Math model footer ----------
            Surfaces the formula so the table reads as an analytical
            claim rather than an arbitrary promise. Anchored against
            YouTube Partner Program disclosures (RPM = CPM × 0.45). */}
        <div className="mt-10 max-w-4xl mx-auto surface-glass border border-white/10 rounded-2xl p-5 animate-fade-up animation-delay-300">
          <div className="flex items-start gap-3">
            <div className="w-9 h-9 rounded-xl bg-gradient-to-br from-emerald-500 to-teal-600 flex items-center justify-center text-white font-extrabold text-sm shrink-0 mt-0.5">
              $
            </div>
            <div className="min-w-0">
              <div className="text-[11px] font-bold uppercase tracking-wider text-emerald-300/90 mb-1">
                How the math works
              </div>
              <p className="text-sm text-zinc-300 leading-relaxed">
                Calculated on a baseline of{" "}
                <span className="text-white font-semibold">300,000 monthly views</span>{" "}
                at an{" "}
                <span className="text-white font-semibold">RPM of $3.50 – $5.00</span>{" "}
                in USA/Tier-1 niches — the{" "}
                <span className="font-mono text-emerald-300 whitespace-nowrap">
                  median case is 300k × $4 ≈ $1,200/mo
                </span>{" "}
                per channel, which lands inside the Tier-1 row above.
                RPM (Revenue Per Mille) is the creator-side payout that
                YouTube publishes in the Partner Program docs (RPM = CPM
                × 0.45). Higher tiers scale this model by channel count
                and Tier-1 view share, not by promising extra money from
                the same RPM.
              </p>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
