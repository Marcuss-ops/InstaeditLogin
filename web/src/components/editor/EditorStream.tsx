import {
  CheckCircle2, Radio
} from "lucide-react";
import {
  PLATFORM_REGISTRY
} from "./shared";

export function EditorStream() {
  type ChannelTile = {
    platform: "youtube" | "facebook" | "instagram" | "tiktok" | "x" | "linkedin" | "threads";
    viewers: number;
    status: "live" | "queued";
    show: string;
  };
  // Per-platform program names so the control-room tiles don't read as a
  // copy-paste — each destination shows what's actually airing on it.
  // Threads runs later (queued → SOON), the rest are LIVE in the mocked
  // snapshot. Aggregate viewers correctly excludes the queued channel.
  const channels: ReadonlyArray<ChannelTile> = [
    { platform: "youtube", viewers: 12483, status: "live", show: "Loop → AMA replay" },
    { platform: "facebook", viewers: 3142, status: "live", show: "Loop → Live Q&A" },
    { platform: "instagram", viewers: 4217, status: "live", show: "Loop → Reels-live" },
    { platform: "tiktok", viewers: 9821, status: "live", show: "Loop → Live drop" },
    { platform: "x", viewers: 1844, status: "live", show: "Loop → Spaces prep" },
    { platform: "linkedin", viewers: 612, status: "live", show: "Loop → Industry chat" },
    { platform: "threads", viewers: 504, status: "queued", show: "Scheduled · 18:30" },
  ];
  const aggregateViewers = channels
    .filter((c) => c.status === "live")
    .reduce((sum, c) => sum + c.viewers, 0);

  return (
    <section className="relative py-24 sm:py-32 overflow-hidden">
      <div
        aria-hidden="true"
        className="absolute inset-0 bg-gradient-to-bl from-violet-500/[0.10] via-transparent to-rose-500/[0.10] pointer-events-none"
      />
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-violet-500 w-[440px] h-[440px] -top-32 -left-24 animate-drift-slow opacity-50" />
        <div className="glow-orb bg-rose-400 w-[360px] h-[360px] -bottom-32 -right-20 animate-drift-rev opacity-40" />
      </div>

      <div className="relative mx-auto max-w-7xl px-6 grid lg:grid-cols-12 gap-12 items-center">
        <div className="lg:col-span-5 animate-fade-up">
          <div className="text-eyebrow text-rose-300/90 mb-3 inline-flex items-center gap-2">
            <Radio className="w-4 h-4" />
            Streaming 24/7
          </div>
          <h2 className="text-display-2 text-white">
            One source.{" "}
            <span className="text-gradient">Seven live destinations.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">            A single control room fans out to every channel — automatic loop of your library, scheduled programming, and 24/7 streaming, even while you sleep.
          </p>

          <ul className="mt-7 space-y-3">
            {[
              {
                t: "Multistream to 7 channels",
                d: "Simultaneous push to YouTube, Facebook, Instagram, TikTok, X, LinkedIn and Threads.",
              },
              {
                t: "Loop + scheduled programming",
                d: "Built-in scheduler for program blocks, replays and live sessions.",
              },
              {
                t: "Always-on with fallback",
                d: "If a source drops, the loop continues until the next slot.",
              },
            ].map((it) => (
              <li key={it.t} className="flex items-start gap-3">
                <span className="mt-0.5 inline-flex w-5 h-5 items-center justify-center rounded-md bg-emerald-500/15 ring-1 ring-emerald-400/25 flex-shrink-0">
                  <CheckCircle2
                    className="w-3.5 h-3.5 text-emerald-300"
                    aria-hidden="true"
                  />
                </span>
                <div>
                  <div className="text-sm font-medium text-white leading-snug">
                    {it.t}
                  </div>
                  <div className="text-[13px] text-zinc-400 mt-0.5 leading-relaxed">
                    {it.d}
                  </div>
                </div>
              </li>
            ))}
          </ul>
        </div>

        <div className="lg:col-span-7 animate-fade-up animation-delay-200">
          <div className="surface-glass border border-white/15 rounded-2xl overflow-hidden shadow-[0_30px_100px_-40px_rgba(244,114,182,0.45)]">
            <div className="flex items-center justify-between px-4 py-3 border-b border-white/10">
              <div className="flex items-center gap-1.5">
                <span className="w-2.5 h-2.5 rounded-full bg-[#ff5f57]" />
                <span className="w-2.5 h-2.5 rounded-full bg-[#febc2e]" />
                <span className="w-2.5 h-2.5 rounded-full bg-[#28c840]" />
              </div>
              <div className="text-xs text-zinc-400 font-medium tracking-tight">
                Control room
                <span className="ml-2 text-zinc-500">
                  · {channels.length} destinations
                </span>
              </div>
              <div className="inline-flex items-center gap-1.5 px-2 py-1 rounded-md bg-rose-500/15 ring-1 ring-rose-400/30">
                <span className="w-1.5 h-1.5 rounded-full bg-rose-400 animate-pulse-glow" />
                <span className="text-[10px] font-bold text-rose-200 tracking-wider">
                  LIVE
                </span>
              </div>
            </div>

            <div className="p-5 sm:p-6 space-y-5">
              {/* Hero live preview */}
              <div className="relative aspect-[16/8] rounded-xl overflow-hidden ring-1 ring-white/10 bg-gradient-to-br from-violet-700 via-fuchsia-600 to-cyan-500">
                <div
                  aria-hidden="true"
                  className="absolute inset-0 bg-[radial-gradient(circle_at_30%_40%,rgba(255,255,255,0.25),transparent_55%),radial-gradient(circle_at_72%_62%,rgba(0,0,0,0.45),transparent_60%)]"
                />
                <div className="absolute top-3 left-3 inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md bg-rose-500/90 text-[10px] font-bold tracking-wider text-white shadow-[0_0_18px_-2px_rgba(244,63,94,0.6)]">
                  <span className="w-1.5 h-1.5 rounded-full bg-white animate-pulse-glow" />
                  LIVE 24/7
                </div>
                <div className="absolute top-3 right-3 text-[10px] text-white/85">
                  Uptime{" "}
                  <span className="tabular-nums font-semibold">21h 04m</span>
                </div>
                <div className="absolute bottom-3 left-3 right-3 flex items-end justify-between gap-3">
                  <div className="min-w-0">
                    <div className="text-[10px] uppercase tracking-wider text-white/70 mb-0.5">
                      Multistream
                    </div>
                    <div className="text-base sm:text-lg font-semibold text-white leading-tight truncate">
                      Shared broadcast ·{" "}
                      <span className="text-white/70">6 destinations live</span>
                    </div>
                  </div>
                  <div className="text-right flex-shrink-0">
                    <div className="text-[10px] uppercase tracking-wider text-white/70 mb-0.5">
                      Aggregate viewers
                    </div>
                    <div className="text-lg sm:text-2xl font-bold text-white tabular-nums leading-none">
                      {aggregateViewers.toLocaleString()}
                    </div>
                  </div>
                </div>
              </div>

              {/* Channels grid */}
              <div>
                <div className="flex items-center justify-between mb-2.5">
                  <div className="text-eyebrow text-zinc-500">Channels</div>
                  <div className="text-[10px] text-zinc-500">
                    <span className="text-white font-semibold tabular-nums">
                      6
                    </span>
                    <span className="mx-1">live ·</span>
                    <span className="text-white font-semibold tabular-nums">
                      1
                    </span>
                    <span className="ml-1">scheduled</span>
                  </div>
                </div>
                <div className="grid grid-cols-2 sm:grid-cols-4 gap-2.5">
                  {channels.map((ch) => {
                    const entry = PLATFORM_REGISTRY.find(
                      (p) => p.key === ch.platform,
                    );
                    if (!entry) return null;
                    const Logo = entry.Logo;
                    const isLive = ch.status === "live";
                    return (
                      <div
                        key={ch.platform}
                        className="surface-card-soft rounded-lg p-3 relative overflow-hidden"
                      >
                        <div
                          aria-hidden="true"
                          className="absolute -top-12 -right-12 w-24 h-24 rounded-full blur-2xl pointer-events-none"
                          style={{
                            background: entry.color,
                            opacity: isLive ? 0.30 : 0.14,
                          }}
                        />
                        <div className="relative">
                          <div className="flex items-center justify-between mb-2">
                            <span className="inline-flex w-6 h-6 rounded-md overflow-hidden ring-1 ring-white/15">
                              <Logo className="w-full h-full" />
                            </span>
                            {isLive ? (
                              <span className="inline-flex items-center gap-1 text-[10px] font-bold tracking-wider text-rose-300">
                                <span className="w-1.5 h-1.5 rounded-full bg-rose-400 animate-pulse-glow" />
                                LIVE
                              </span>
                            ) : (
                              <span className="text-[10px] font-bold tracking-wider text-amber-300">
                                SOON
                              </span>
                            )}
                          </div>
                          <div className="text-[10px] uppercase tracking-wider text-zinc-500">
                            {entry.name}
                          </div>
                          <div className="flex items-baseline gap-1.5 mt-0.5">
                            <span
                              className={`text-sm font-bold tabular-nums ${isLive ? "text-white" : "text-zinc-400"}`}
                            >
                              {isLive
                                ? ch.viewers.toLocaleString()
                                : "—"}
                            </span>
                            <span className="text-[10px] text-zinc-500">
                              {isLive ? "viewers" : ch.show}
                            </span>
                          </div>
                        </div>
                      </div>
                    );
                  })}
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
