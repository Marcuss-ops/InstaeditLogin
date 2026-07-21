

export function EditorSpeedStats() {
  const stats = [
    { before: "6 ore", after: "8 min", label: "Manual editing" },
    { before: "14 tabs", after: "1 dashboard", label: "Re-uploads per platform" },
    { before: "7 export", after: "1 render", label: "Re-encoding per channel" },
  ];
  return (
    <section className="relative py-20 sm:py-24 bg-elevated overflow-hidden">
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-12 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">
            Why an editor
          </div>
          <h2 className="text-display-2 text-white">
            From hours of manual work to{" "}
            <span className="text-gradient">a single click.</span>
          </h2>
        </div>

        <div className="grid sm:grid-cols-3 gap-4">
          {stats.map((s, i) => (
            <div
              key={s.label}
              className={`surface-card p-7 relative overflow-hidden animate-fade-up ${
                ["", "animation-delay-100", "animation-delay-200"][i]
              }`}
            >
              <div className="text-sm text-zinc-500 line-through mb-3">
                {s.before}
              </div>
              <div className="text-display-3 text-white tabular-nums">
                {s.after}
              </div>
              <div className="text-eyebrow text-zinc-500 mt-3">
                {s.label}
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
