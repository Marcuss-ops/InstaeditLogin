import { TrendingUp, Calculator } from "lucide-react";
import { SectionVideo } from "./SectionVideo";
import { EARNINGS } from "./content";

/**
 * Sezione "I Guadagni" — tabella realistica per livello di portfolio.
 * Video di sfondo rilassante + glow caldi per un tono più umano.
 * Include disclaimer di conformità marketing (i risultati non sono
 * garantiti) e la spiegazione della matematica dell'RPM.
 */
export function Earnings() {
  return (
    <section id="guadagni" className="relative py-24 sm:py-32 overflow-hidden bg-[#F6F3F7]">
      <SectionVideo src={EARNINGS.bgVideo} />
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="absolute w-[420px] h-[420px] -top-24 -right-24 rounded-full bg-[#F7E7D8]/50 blur-[110px]" />
        <div className="absolute w-[380px] h-[380px] -bottom-24 -left-24 rounded-full bg-[#F0E2F4]/40 blur-[110px]" />
      </div>
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="donne-video-scrim max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-[#A4708C] mb-3 inline-flex items-center gap-2">
            <TrendingUp className="w-4 h-4" />
            {EARNINGS.eyebrow}
          </div>
          <h2 className="text-display-2 text-[#4A3E56] donne-text-halo">
            {EARNINGS.titleStart}
            <span className="text-gradient-gold">{EARNINGS.titleAccent}</span>
          </h2>
          <p className="text-body-lg text-[#6E6677] mt-5 max-w-[62ch] donne-text-halo">
            {EARNINGS.subtitleStart}
            <span className="font-semibold text-[#C78A4B]">{EARNINGS.subtitleAccent}</span>
            {EARNINGS.subtitleEnd}
          </p>
        </div>

        <div className="grid md:grid-cols-3 gap-5 animate-fade-up">
          {EARNINGS.rows.map((row, i) => (
            <div
              key={row.level}
              className={`donne-card p-6 ${i === 2 ? "ring-2 ring-[#E28743]/40 shadow-[0_16px_40px_-18px_rgba(226,135,67,0.5)]" : ""}`}
            >
              <div className="flex items-center justify-between mb-3">
                <div className="text-eyebrow text-[#7A7280]">{row.level}</div>
                <span
                  className={`inline-flex px-2 py-0.5 rounded-full text-[10px] font-bold uppercase tracking-wider ${
                    i === 2
                      ? "bg-gradient-to-r from-[#E07A5F] to-[#E28743] text-white"
                      : "bg-[#F3EFF5] text-[#6B4E71]"
                  }`}
                >
                  {row.tag}
                </span>
              </div>
              <div
                className={`text-2xl sm:text-3xl font-extrabold tabular-nums tracking-tight mb-2 ${
                  i === 2 ? "text-gradient-gold" : "text-[#4A3E56]"
                }`}
              >
                {row.earning}
              </div>
              <div className="text-sm text-[#6E6677]">{row.reach}</div>
            </div>
          ))}
        </div>

        <div className="mt-8 donne-card p-6 animate-fade-up">
          <div className="flex items-start gap-4">
            <span className="mt-0.5 inline-flex w-10 h-10 shrink-0 items-center justify-center rounded-xl bg-[#FBF1E7] ring-1 ring-[#EDDFCE] text-[#C78A4B]">
              <Calculator className="w-5 h-5" />
            </span>
            <div>
              <div className="text-base font-semibold text-[#4A3E56] mb-2">{EARNINGS.mathTitle}</div>
              {EARNINGS.mathParagraphs.map((p) => (
                <p key={p} className="text-sm text-[#6E6677] leading-relaxed mb-2 last:mb-0">
                  {p}
                </p>
              ))}
            </div>
          </div>
        </div>

        <p className="text-xs text-[#7A7280] mt-4 max-w-[62ch]">{EARNINGS.disclaimer}</p>
      </div>
    </section>
  );
}
