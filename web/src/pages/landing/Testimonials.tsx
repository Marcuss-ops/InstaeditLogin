import { Quote } from "lucide-react";

/* ----------------------------------------------------------------------------
 * Testimonials — student quotes, placed after the founder story and before
 * the FAQ. Quotes mirror the verified student voices on the Mentoring page
 * (keep them in sync with Mentoring.tsx when copy changes).
 * -------------------------------------------------------------------------- */

const TESTIMONIALS = [
  {
    quote:
      "In three months I went from 2 to 12 posts per week without hiring anyone. Mentoring gave me the right workflow.",
    author: "Sara M.",
    role: "Tech creator",
  },
  {
    quote:
      "My team no longer loses hours to uploads and reformatting. The path saved us dozens of hours per month.",
    author: "Marco B.",
    role: "Content Strategist",
  },
  {
    quote:
      "I learned how to use AI not to replace me, but to amplify my style. View results grew steadily.",
    author: "Giulia T.",
    role: "Lifestyle creator",
  },
];

export function Testimonials() {
  return (
    <section id="testimonials" className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora opacity-20 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 text-center mx-auto animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">Testimonials</div>
          <h2 className="text-display-2 text-white">
            In their{" "}
            <span className="text-gradient">own words.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch] mx-auto">
            Creators, strategists and teams who replaced the manual grind with
            the InstaEdit pipeline — and ship more, every single week.
          </p>
        </div>
        <div className="grid sm:grid-cols-2 lg:grid-cols-3 gap-5">
          {TESTIMONIALS.map((t, i) => (
            <div
              key={t.author}
              className={`surface-card p-6 relative overflow-hidden animate-fade-up hover:border-violet-400/30 hover:shadow-[0_8px_32px_rgba(139,92,246,0.12)] transition-all duration-300 ${["", "animation-delay-100", "animation-delay-200"][i]}`}
            >
              <div aria-hidden="true" className="absolute top-0 left-0 right-0 h-0.5 bg-gradient-to-r from-violet-500/60 to-cyan-400/60" />
              <Quote className="w-5 h-5 text-violet-300 mb-4" />
              <p className="text-sm text-zinc-300 leading-relaxed mb-5">
                &ldquo;{t.quote}&rdquo;
              </p>
              <div className="flex items-center gap-3">
                <div className="w-10 h-10 rounded-full bg-gradient-to-br from-violet-500 to-cyan-500 flex items-center justify-center text-white text-sm font-semibold">
                  {t.author.charAt(0)}
                </div>
                <div>
                  <div className="text-sm font-semibold text-white">{t.author}</div>
                  <div className="text-xs text-zinc-500">{t.role}</div>
                </div>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
