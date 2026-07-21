import {
  UploadCloud
} from "lucide-react";

export function EditorCanvas() {
  return (
    <div
      aria-disabled="true"
      className="relative surface-glass rounded-3xl border border-white/15 overflow-hidden shadow-[0_30px_120px_-30px_rgba(124,58,237,0.55)] animate-fade-up animation-delay-200"
    >
      {/* Faint outer glow */}
      <div
        aria-hidden="true"
        className="absolute -inset-4 hero-aurora opacity-50 blur-2xl rounded-[2rem] pointer-events-none -z-10"
      />
      {/* Header strip — mimics the dashboard's window chrome so the page
          reads as part of the same product family. */}
      <div className="flex items-center justify-between px-4 py-3 border-b border-white/10">
        <div className="flex items-center gap-1.5">
          <span className="w-3 h-3 rounded-full bg-[#ff5f57]" />
          <span className="w-3 h-3 rounded-full bg-[#febc2e]" />
          <span className="w-3 h-3 rounded-full bg-[#28c840]" />
        </div>
        <div className="text-xs text-zinc-400 font-medium tracking-tight">
          instaedit.app · Editor
        </div>
        <div className="w-12 h-6 rounded-md surface-card-soft flex items-center justify-center">
          <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 mr-1.5" />
          <span className="text-[10px] text-zinc-300">Live</span>
        </div>
      </div>

      {/* Dropzone body */}
      <div className="p-6 sm:p-8">
        <div className="rounded-2xl border-2 border-dashed border-white/15 bg-[#0d0d15]/70 px-6 py-10 sm:py-14 text-center">
          <div className="inline-flex w-14 h-14 items-center justify-center rounded-2xl ring-1 ring-violet-400/40 bg-gradient-to-br from-violet-500/30 to-cyan-500/20 text-violet-200 mb-4">
            <UploadCloud className="w-7 h-7" />
          </div>
          <div className="text-lg font-semibold text-white">
            Drag your raw idea here
          </div>
          <div className="text-sm text-zinc-400 mt-1.5 max-w-[42ch] mx-auto">
            MP4, MOV, WebM or HEVC up to 4 GB. Vertical, horizontal, square — we accept
            what you have.
          </div>
          <div className="mt-5 inline-flex items-center gap-2 px-3 py-1.5 rounded-full bg-white/[0.06] text-[11px] text-zinc-300 ring-1 ring-white/10">
            <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse-glow" />
            or paste a YouTube / Drive link
          </div>
        </div>

        {/* Mock processing state — purely decorative, shows the "engine at
            work" claim with three sub-step indicators. */}
        <div className="mt-5 space-y-2.5">
          {[
            { l: "Encoding · 7 variants per platform", on: true },
            { l: "Subtitles · auto-transcribed + translated", on: true },
            { l: "Thumbnails · generated A/B tests", on: false },
          ].map((step) => (
            <div
              key={step.l}
              className="flex items-center justify-between px-3 py-2 rounded-lg bg-white/[0.04] ring-1 ring-white/5"
            >
              <div className="flex items-center gap-2.5">
                <span
                  className={`w-2 h-2 rounded-full ${
                    step.on
                      ? "bg-emerald-400 animate-pulse-glow"
                      : "bg-zinc-600"
                  }`}
                />
                <span className="text-xs text-zinc-200">{step.l}</span>
              </div>
              <span className="text-[10px] text-zinc-500 tabular-nums">
                {step.on ? "ok" : "queued"}
              </span>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
