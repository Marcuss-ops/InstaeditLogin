import {
  CheckCircle2, PlayCircle, MonitorPlay
} from "lucide-react";
import {
  SHORT_DEMOS, LONGFORM_DEMOS, YouTubeEmbed
} from "./shared";

export function EditorVideoExamples() {
  return (
    <section className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div aria-hidden="true" className="absolute inset-0 bg-gradient-to-br from-violet-500/12 via-transparent to-cyan-500/10 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-12 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3 inline-flex items-center gap-2">
            <PlayCircle className="w-4 h-4" />
            Real examples
          </div>
          <h2 className="text-display-2 text-white">
            See what the editor produces <span className="text-gradient">in practice.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[60ch]">
            Our system researches the best stock footage and sound effects,
            generates supporting AI images, and places every clip at the right
            moment. A human stays in the loop for the final creative pass —
            After Effects-level quality in minutes, not hours.
          </p>
        </div>

        <div className="grid md:grid-cols-3 gap-4 mb-20">
          {[
            {
              title: "The best resources found automatically",
              copy: "The engine finds the strongest stock footage and SFX for every moment of your story.",
            },
            {
              title: "Placed at the exact moment",
              copy: "AI images and supporting clips are synced with narration, rhythm and emotional arc.",
            },
            {
              title: "Quality with human review",
              copy: "Every cut undergoes creative review for polished results, on par with After Effects, in minutes.",
            },
          ].map((feature) => (
            <div key={feature.title} className="surface-card p-5">
              <CheckCircle2 className="w-5 h-5 text-emerald-400 mb-3" />
              <h3 className="text-sm font-semibold text-white">{feature.title}</h3>
              <p className="text-sm text-zinc-400 leading-relaxed mt-2">{feature.copy}</p>
            </div>
          ))}
        </div>

        <div className="grid lg:grid-cols-12 gap-12 items-start">
          <div className="lg:col-span-5">
            <div className="text-eyebrow text-violet-300/90 mb-3 inline-flex items-center gap-2">
              <PlayCircle className="w-4 h-4" />
              Short-form
            </div>
            <h3 className="text-display-3 text-white mb-3">
              Vertical videos designed for the feed.
            </h3>
            <p className="text-sm text-zinc-400 max-w-[45ch]">
              Native 9:16 outputs for YouTube Shorts, Instagram Reels and TikTok —
              ready to publish without reformatting.
            </p>
          </div>
          <div className="lg:col-span-7 grid grid-cols-1 sm:grid-cols-2 gap-5">
            {SHORT_DEMOS.map((demo) => (
              <YouTubeEmbed key={demo.id} {...demo} aspect="9/16" />
            ))}
          </div>
        </div>

        <div className="mt-24 rounded-3xl border border-cyan-400/15 bg-gradient-to-br from-cyan-500/[0.10] via-[#0d0d15]/80 to-pink-500/[0.08] p-6 sm:p-10 shadow-[0_30px_100px_-45px_rgba(34,211,238,0.45)]">
          <div className="max-w-3xl mb-8">
            <div className="text-eyebrow text-cyan-300/90 mb-3 inline-flex items-center gap-2">
              <MonitorPlay className="w-4 h-4" />
              Long-form
            </div>
            <h3 className="text-display-3 text-white mb-3">
              Horizontal masters for every channel.
            </h3>
            <p className="text-body-lg text-zinc-400 max-w-[60ch]">
              Long-form exports with the right framing, descriptions, thumbnails and chapters
              for YouTube, Facebook, Instagram and LinkedIn.
            </p>
          </div>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
            {LONGFORM_DEMOS.map((demo) => (
              <YouTubeEmbed key={demo.id} {...demo} aspect="16/9" />
            ))}
          </div>
        </div>
      </div>
    </section>
  );
}
