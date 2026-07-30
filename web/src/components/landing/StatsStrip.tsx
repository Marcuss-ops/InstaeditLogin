
/* ----------------------------------------------------------------------------
 * Stats strip
 * -------------------------------------------------------------------------- */

export function StatsStrip() {
  const stats: Array<{ v: string; l: string }> = [
    { v: "$2K+", l: "Avg. monthly earnings" },
    { v: "50+", l: "Channels monetized" },
    { v: "500+", l: "Videos published" },
    { v: "7", l: "Platforms at once" },
  ];
  return (
    <section className="relative py-10 border-y border-white/10 bg-[#0c0c14]/60">
      <div className="mx-auto max-w-7xl px-6">
        <ul className="grid grid-cols-2 sm:grid-cols-4 gap-y-6 gap-x-8 text-center sm:text-left">
          {stats.map((s, idx) => (
            <li
              key={s.l}
              className={`flex items-center ${idx < stats.length - 1 ? "sm:border-r sm:border-white/10 sm:pr-8" : ""} justify-center sm:justify-start gap-4`}
            >
              <span className="text-3xl sm:text-4xl font-extrabold text-white tabular-nums tracking-tight">{s.v}</span>
              <span className="text-eyebrow text-zinc-500 max-w-[14ch]">{s.l}</span>
            </li>
          ))}
        </ul>
      </div>
    </section>
  );
}


