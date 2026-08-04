import { TrendingUp, Calculator } from "lucide-react";
import { EARNINGS } from "./content";

/**
 * Sezione "I Guadagni" — tabella realistica per livello di portfolio.
 * Include disclaimer di conformità marketing (i risultati non sono
 * garantiti) e la spiegazione della matematica dell'RPM.
 */
export function Earnings() {
  return (
    <section id="guadagni" className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-amber-500 w-[360px] h-[360px] -top-20 -right-32 animate-drift-slow opacity-40" />
        <div className="glow-orb bg-pink-500 w-[320px] h-[320px] -bottom-32 -left-24 animate-drift-rev opacity-30" />
      </div>
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-amber-300/90 mb-3 inline-flex items-center gap-2">
            <TrendingUp className="w-4 h-4" />
            {EARNINGS.eyebrow}
          </div>
          <h2 className="text-display-2 text-white">{EARNINGS.title}</h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[62ch]">{EARNINGS.subtitle}</p>
        </div>

        <div className="surface-glass border border-white/15 rounded-2xl overflow-hidden animate-fade-up">
          <div className="grid grid-cols-1 md:grid-cols-3 gap-px bg-white/5">
            {EARNINGS.rows.map((row, i) => (
              <div
                key={row.level}
                className={`p-6 ${i === 2 ? "bg-pink-500/[0.08]" : "bg-[#14141c]/80"}`}
              >
                <div className="flex items-center justify-between mb-3">
                  <div className="text-eyebrow text-zinc-500">{row.level}</div>
                  <span
                    className={`inline-flex px-2 py-0.5 rounded-full text-[10px] font-bold uppercase tracking-wider ${
                      i === 2
                        ? "bg-gradient-to-r from-pink-500 to-rose-500 text-white"
                        : "bg-white/[0.06] text-zinc-300"
                    }`}
                  >
                    {row.tag}
                  </span>
                </div>
                <div className="text-2xl sm:text-3xl font-extrabold text-white tabular-nums tracking-tight mb-2">
                  {row.earning}
                </div>
                <div className="text-sm text-zinc-400">{row.reach}</div>
              </div>
            ))}
          </div>
        </div>

        <div className="mt-8 surface-card p-6 animate-fade-up">
          <div className="flex items-start gap-4">
            <span className="mt-0.5 inline-flex w-10 h-10 shrink-0 items-center justify-center rounded-xl bg-amber-500/15 ring-1 ring-amber-400/30 text-amber-300">
              <Calculator className="w-5 h-5" />
            </span>
            <div>
              <div className="text-base font-semibold text-white mb-2">{EARNINGS.mathTitle}</div>
              {EARNINGS.mathParagraphs.map((p) => (
                <p key={p} className="text-sm text-zinc-400 leading-relaxed mb-2 last:mb-0">
                  {p}
                </p>
              ))}
            </div>
          </div>
        </div>

        <p className="text-xs text-zinc-500 mt-4 max-w-[62ch]">{EARNINGS.disclaimer}</p>
      </div>
    </section>
  );
}
