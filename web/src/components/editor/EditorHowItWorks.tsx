import {
  UploadCloud, Clock, Sparkles
} from "lucide-react";

export function EditorHowItWorks() {
  const steps = [
    {
      n: "01",
      title: "Drag your raw idea",
      copy: "Vertical, horizontal or square — upload what you shot. We accept MP4, MOV, WebM and HEVC up to 4 GB.",
      Icon: UploadCloud,
      ring: "ring-violet-400/40",
      iconColor: "text-violet-300",
    },
    {
      n: "02",
      title: "The engine rewrites for every platform",
      copy: "Subtitles, thumbnails, hashtags and chapters are generated automatically per platform in one pass.",
      Icon: Sparkles,
      ring: "ring-cyan-400/40",
      iconColor: "text-cyan-300",
    },
    {
      n: "03",
      title: "Schedule once. Publish everywhere.",
      copy: "Pick a time. InstaEdit distributes slots per platform so every audience sees it at peak engagement.",
      Icon: Clock,
      ring: "ring-pink-400/40",
      iconColor: "text-pink-300",
    },
  ];
  return (
    <section className="relative py-24 sm:py-32 overflow-hidden">
      <div
        aria-hidden="true"
        className="hidden lg:block absolute top-[58%] left-0 right-0 h-px bg-gradient-to-r from-transparent via-violet-400/40 to-transparent pointer-events-none"
      />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">
            How it works
          </div>
          <h2 className="text-display-2 text-white">
            From raw idea to <span className="text-gradient">publication.</span>
          </h2>
        </div>

        <ol className="grid sm:grid-cols-2 lg:grid-cols-3 gap-5 relative">
          {steps.map((s, i) => (
            <li
              key={s.n}
              className={`surface-card p-7 relative overflow-hidden animate-fade-up ${
                ["", "animation-delay-100", "animation-delay-200"][i]
              }`}
            >
              <div
                aria-hidden="true"
                className="absolute -top-16 -right-16 w-44 h-44 rounded-full bg-radial from-violet-500/30 to-violet-500/0 opacity-70 blur-2xl pointer-events-none"
              />
              <div className="relative">
                <div className="flex items-center justify-between mb-5">
                  <span
                    className={`inline-flex w-11 h-11 rounded-xl items-center justify-center ring-1 ${s.ring} surface-glass ${s.iconColor}`}
                  >
                    <s.Icon className="w-5 h-5" />
                  </span>
                  <span className="text-eyebrow text-zinc-500 tabular-nums">
                    Step {s.n}
                  </span>
                </div>
                <h3 className="text-display-3 text-white mb-2">
                  {s.title}
                </h3>
                <p className="text-sm text-zinc-400 leading-relaxed">
                  {s.copy}
                </p>
              </div>
            </li>
          ))}
        </ol>
      </div>
    </section>
  );
}
