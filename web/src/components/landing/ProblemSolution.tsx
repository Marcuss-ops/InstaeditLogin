import { CheckCircle2 } from "lucide-react";

/* ----------------------------------------------------------------------------
 * Problem & Solution — pain vs. easy gain
 * -------------------------------------------------------------------------- */

export function ProblemSolution() {
  return (
    <section className="relative py-24 sm:py-32 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-red-500 w-[360px] h-[360px] -top-32 -right-32 animate-drift-slow opacity-20" />
        <div className="glow-orb bg-emerald-500 w-[400px] h-[400px] -bottom-40 -left-24 animate-drift-rev opacity-25" />
      </div>
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="grid lg:grid-cols-2 gap-16 items-center">
          <div className="animate-fade-up">
            <div className="text-eyebrow text-red-300/90 mb-3">The problem</div>
            <h2 className="text-display-2 text-white mb-6">
              You're losing money{" "}
              <span className="text-gradient-animated">every single day.</span>
            </h2>
            <p className="text-body-lg text-zinc-400 max-w-[52ch] mb-6">
              You want to earn online but editing steals 15 hours a week. You're
              scared to show your face or don't know where to start. You've been
              creating for months &mdash; 50 views and zero dollars.
            </p>
            <div className="space-y-4">
              {[
                "Editing eats 15+ hours per week and you see no return",
                "You're afraid to show your face or don't know the tech side",
                "You've posted for months — 50 views and $0 earned",
                "The algorithm rewards daily posting but you can't keep up",
              ].map((item) => (
                <div key={item} className="flex items-start gap-3">
                  <span className="w-5 h-5 rounded-full bg-red-500/20 flex items-center justify-center shrink-0 mt-0.5">
                    <span className="w-2 h-2 rounded-full bg-red-400" />
                  </span>
                  <span className="text-sm text-zinc-300">{item}</span>
                </div>
              ))}
            </div>
          </div>
          <div className="animate-fade-up animation-delay-200">
            <div className="text-eyebrow text-emerald-300/90 mb-3">The InstaEdit shortcut</div>
            <h2 className="text-display-2 text-white mb-6">
              The easy way to{" "}
              <span className="text-gradient-animated">real income.</span>
            </h2>
            <p className="text-body-lg text-zinc-400 max-w-[52ch] mb-6">
              We hand you the keys: a channel already past YouTube's trust filter,
              AI that produces professional videos from a single line of text, and
              a mentor who tells you exactly what to post and when.
            </p>
            <div className="space-y-4">
              {[
                "Ready-made channel — you skip the \"grind\" phase entirely",
                "ChronoN AI: type one sentence, get a ready-to-publish video",
                "One video becomes 7 posts across 7 platforms automatically",
                "Daily content without lifting a finger — 100% hands-free",
              ].map((item) => (
                <div key={item} className="flex items-start gap-3">
                  <span className="w-5 h-5 rounded-full bg-emerald-500/20 flex items-center justify-center shrink-0 mt-0.5">
                    <CheckCircle2 className="w-3 h-3 text-emerald-400" />
                  </span>
                  <span className="text-sm text-zinc-300">{item}</span>
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
