import { CheckCircle2 } from "lucide-react";

/* ----------------------------------------------------------------------------
 * Problem & Solution
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
              You're leaving money{" "}
              <span className="text-gradient-animated">on the table.</span>
            </h2>
            <p className="text-body-lg text-zinc-400 max-w-[52ch] mb-6">
              You watch creators monetize their channels and earn online, but you're
              stuck spending hours editing, re-formatting, and uploading to every
              platform manually. Meanwhile, the algorithm rewards those who post
              consistently — and you can't keep up.
            </p>
            <div className="space-y-4">
              {[
                "You know YouTube can make money but don't know where to start",
                "You spend 10+ hours a week on editing and uploading instead of creating",
                "You post once a week while successful channels post daily",
                "You see others earning from their content but can't figure out the system",
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
            <div className="text-eyebrow text-emerald-300/90 mb-3">Our solution</div>
            <h2 className="text-display-2 text-white mb-6">
              Turn content into{" "}
              <span className="text-gradient-animated">income.</span>
            </h2>
            <p className="text-body-lg text-zinc-400 max-w-[52ch] mb-6">
              We give you the strategy to create videos people actually watch, and
              the automation to publish everywhere — so you can focus on growing
              your channel and reaching monetization faster.
            </p>
            <div className="space-y-4">
              {[
                "Aged YouTube channels to skip the sandbox and monetize sooner",
                "ChronoN generates professional videos from a text brief — no camera needed",
                "One video → 7 platforms, each optimized for maximum reach and views",
                "Post daily across all platforms without spending hours on editing",
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


