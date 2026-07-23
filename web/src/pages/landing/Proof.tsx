import { YouTubeEmbed } from "./shared";

/* ----------------------------------------------------------------------------
 * Proof — compact YouTube results gallery
 * -------------------------------------------------------------------------- */

const SHORT_DEMOS = [
  { id: "MVwXsmRLnwM", title: "YouTube Shorts demo MVwXsmRLnwM" },
  { id: "XCIWzK2BuRo", title: "YouTube Shorts demo XCIWzK2BuRo" },
] as const;

const LONGFORM_DEMOS = [
  { id: "fLhv7d6N_3c", title: "YouTube long-form demo fLhv7d6N_3c" },
  { id: "iA1WT69NFbw", title: "YouTube long-form demo iA1WT69NFbw" },
  { id: "R18AVWQ92fs", title: "YouTube long-form demo R18AVWQ92fs" },
  { id: "lpKX9SKqSMw", title: "YouTube long-form demo lpKX9SKqSMw" },
] as const;

export function Proof() {
  return (
    <section id="proof" className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora opacity-15 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-12 text-center mx-auto animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">Proof</div>
          <h2 className="text-display-2 text-white">
            See the channels{" "}
            <span className="text-gradient">that are earning.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch] mx-auto">
            These are real channels built and automated by our system. Watch the
            content our students' channels are publishing — and the revenue they generate.
          </p>
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-5 animate-fade-up animation-delay-200">
          {SHORT_DEMOS.map((demo) => (
            <YouTubeEmbed key={demo.id} id={demo.id} title={demo.title} aspect="9/16" />
          ))}
          {LONGFORM_DEMOS.slice(0, 2).map((demo) => (
            <YouTubeEmbed key={demo.id} id={demo.id} title={demo.title} aspect="16/9" />
          ))}
        </div>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-5 mt-5 animate-fade-up animation-delay-300">
          {LONGFORM_DEMOS.slice(2).map((demo) => (
            <YouTubeEmbed key={demo.id} id={demo.id} title={demo.title} aspect="16/9" />
          ))}
        </div>
      </div>
    </section>
  );
}
