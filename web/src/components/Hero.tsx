import { useState } from "react";
import { Link } from "react-router-dom";
import { PlatformPills } from "./PlatformPills";
import { DemoModal } from "./DemoModal";

const portfolioVideos = [
  { id: "K9bbXKdXBPc", title: "Luffy's Gear 5 Awakening", score: 99, remark: "Outstanding hook, viral audio pattern" },
  { id: "uTkE5Gk9s6A", title: "Premium Motion Edit", score: 97, remark: "Ultra-fast cuts, high retention pacing" },
  { id: "HihqIP9e7Z4", title: "Cinematic Pacing Showcase", score: 98, remark: "Perfect sound transitions, high watchtime" },
  { id: "Uee5VFsmlLo", title: "Advanced Visual Color Edit", score: 96, remark: "Stunning grading, high visual engagement" }
];

export function Hero() {
  const [demoOpen, setDemoOpen] = useState(false);

  return (
    <section className="pt-28 pb-10 text-center bg-black text-white overflow-hidden">
      <div className="max-w-[1100px] mx-auto px-6 relative">
        {/* Glow background accent */}
        <div className="absolute top-[-20%] left-[50%] -translate-x-[50%] w-[500px] h-[500px] rounded-full bg-violet-600/10 blur-[120px] pointer-events-none" />

        <h1 className="text-[clamp(44px,7.5vw,78px)] font-extrabold tracking-[-0.03em] leading-[0.9] mb-8 text-gradient text-glow">
          1 long video.<br />
          10 viral shorts.<br />
          <span className="text-gradient-cyan brand-glow">Created in minutes.</span>
        </h1>

        <p className="text-[clamp(16px,2vw,19px)] text-neutral-400 max-w-[700px] mx-auto mb-10 leading-relaxed">
          We are a team of top-tier video editors. We transform your raw files into 
          highly engaging, captioned, multi-format viral videos in minutes, not hours.
        </p>

        <div className="flex gap-4.5 justify-center flex-wrap mb-24">
          <Link
            to="/login"
            className="inline-flex items-center gap-2 px-8 py-3.5 rounded-xl text-sm font-bold bg-white text-black no-underline hover:-translate-y-[1px] hover:shadow-[0_0_25px_rgba(255,255,255,0.45)] transition-all"
          >
            Get started now
          </Link>
          <button
            onClick={() => setDemoOpen(true)}
            className="inline-flex items-center gap-2 px-8 py-3.5 rounded-xl text-sm font-bold bg-[#0c0c0e] text-white border border-neutral-900 no-underline hover:-translate-y-[1px] hover:border-violet-500/40 hover:shadow-[0_0_20px_rgba(139,92,246,0.15)] transition-all cursor-pointer"
          >
            Watch quick demo
          </button>
        </div>

        {/* Video Portfolio Grid */}
        <div className="max-w-[1000px] mx-auto my-24 relative">
          <h2 className="text-3xl font-extrabold tracking-tight text-white mb-2 text-glow">
            Our Video Portfolio
          </h2>
          <p className="text-neutral-500 text-sm mb-12">
            See how we turn raw footage into high-retention viral content.
          </p>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-7">
            {portfolioVideos.map((video) => (
              <div 
                key={video.id} 
                className="bg-[#09090b] border border-neutral-900 rounded-2xl p-5 hover:border-violet-600/30 hover:shadow-[0_0_30px_rgba(139,92,246,0.12)] transition-all group text-left relative overflow-hidden"
              >
                {/* Virality score badge */}
                <div className="absolute top-8 right-8 z-10 flex items-center gap-1.5 bg-[#10b981]/10 border border-[#10b981]/30 py-1.5 px-3 rounded-full shadow-[0_0_15px_rgba(16,185,129,0.15)]">
                  <span className="w-1.5 h-1.5 rounded-full bg-[#10b981] animate-pulse" />
                  <span className="text-[11px] font-bold text-[#10b981] tracking-tight">
                    {video.score} Virality Score
                  </span>
                </div>

                <div className="overflow-hidden rounded-xl aspect-video mb-4 bg-black border border-neutral-900 group-hover:border-neutral-800 transition-colors">
                  <iframe
                    className="w-full h-full border-0"
                    src={`https://www.youtube.com/embed/${video.id}`}
                    title={video.title}
                    allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share"
                    allowFullScreen
                  />
                </div>
                <h3 className="font-bold text-base text-neutral-100 group-hover:text-white transition-colors mb-1">
                  {video.title}
                </h3>
                <p className="text-xs text-neutral-500 font-medium">
                  <span className="text-[#a78bfa] font-semibold">AI Insights: </span>
                  {video.remark}
                </p>
              </div>
            ))}
          </div>
          <PlatformPills />
        </div>

        {/* Platforms row */}
        <div className="mt-24 py-8 border-y border-neutral-900/60">
          <div className="flex items-center justify-center gap-8 flex-wrap">
            <span className="text-[13px] text-neutral-500 font-medium tracking-wide">COMPATIBLE WITH</span>
            <div className="flex gap-8 flex-wrap items-center justify-center">
              {["Instagram Reels", "Facebook Videos", "TikTok Shorts", "YouTube Shorts", "X Media"].map((p) => (
                <span key={p} className="text-[14px] font-bold tracking-tight text-neutral-500 hover:text-white transition-colors cursor-default">
                  {p}
                </span>
              ))}
            </div>
          </div>
        </div>
      </div>

      <DemoModal open={demoOpen} onClose={() => setDemoOpen(false)} />
    </section>
  );
}
