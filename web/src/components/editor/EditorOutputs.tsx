import { type LogoProps, PLATFORM_REGISTRY } from "./shared";

function OutputCard({
  Logo,
  color,
  format,
  title,
  delay,
}: {
  Logo: (props: LogoProps) => React.ReactElement;
  color: string;
  format: string;
  title: string;
  delay: string;
}) {
  return (
    <div
      className={`surface-card p-5 relative overflow-hidden animate-fade-up ${delay}`}
    >
      <div
        aria-hidden="true"
        className="absolute -top-12 -right-12 w-32 h-32 rounded-full blur-2xl pointer-events-none"
        style={{ background: color, opacity: 0.25 }}
      />
      <div className="relative">
        <div className="flex items-center justify-between mb-3.5">
          <div className="inline-flex w-9 h-9 rounded-lg overflow-hidden ring-1 ring-white/15">
            <Logo className="w-full h-full" />
          </div>
          <span className="inline-flex items-center gap-1 text-[10px] font-medium text-emerald-300/90 uppercase tracking-wider">
            <span className="w-1.5 h-1.5 rounded-full bg-emerald-400" />
            Auto
          </span>
        </div>
        <div className="text-[11px] text-zinc-500 uppercase tracking-wider mb-1">
          {format}
        </div>
        <div className="text-sm font-semibold text-white leading-snug line-clamp-2">
          {title}
        </div>
        <div
          className="mt-3 h-1 rounded-full"
          style={{ background: color, opacity: 0.7 }}
        />
      </div>
    </div>
  );
}

export function EditorOutputs() {
  const sample = {
    instagram: { format: "9:16 · Reels", title: "How we ship 10× faster" },
    tiktok: { format: "9:16 · Autoplay", title: "10× faster — full demo" },
    youtube: { format: "16:9 · Short", title: "How we ship 10× faster" },
    facebook: { format: "1:1 · Reels", title: "How we ship 10× faster" },
    x: { format: "16:9 · Native", title: "10× faster, one render" },
    linkedin: { format: "1.91:1 · Post", title: "How we ship 10× faster" },
    threads: { format: "4:5 · Post", title: "10× faster, one render" },
  } as const;

  return (
    <section className="relative py-24 sm:py-32 overflow-hidden">
      <div
        aria-hidden="true"
        className="absolute inset-0 bg-gradient-to-br from-violet-500/12 via-transparent to-cyan-500/10 pointer-events-none"
      />
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-violet-500 w-[420px] h-[420px] -top-20 -right-32 animate-drift-slow opacity-50" />
        <div className="glow-orb bg-cyan-400 w-[380px] h-[380px] -bottom-32 -left-24 animate-drift-rev opacity-45" />
      </div>

      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-12 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">
            One render. Seven native posts.
          </div>
          <h2 className="text-display-2 text-white">
            Every output is modeled for its{" "}
            <span className="text-gradient">platform.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[60ch]">
            Our engine reads each platform's quirks — aspect ratio,
            duration limits, thumbnail rules, caption tone — so a single
            raw render lands natively without you opening another tab.
          </p>
        </div>

        <div className="grid sm:grid-cols-2 lg:grid-cols-4 gap-4">
          {PLATFORM_REGISTRY.map((p, i) => {
            const d = sample[p.key];
            return (
              <OutputCard
                key={p.key}
                Logo={p.Logo}
                color={p.color}
                format={d.format}
                title={d.title}
                delay={`animation-delay-${i * 100}`}
              />
            );
          })}
        </div>
      </div>
    </section>
  );
}
