import { useState } from "react";
import { Link } from "react-router-dom";
import { PlatformPills } from "./PlatformPills";
import { DemoModal } from "./DemoModal";

const portfolioVideos = [
  { id: "K9bbXKdXBPc", title: "One Piece Best Moments" },
  { id: "uTkE5Gk9s6A", title: "Premium Production Edit" },
  { id: "HihqIP9e7Z4", title: "Cinematic Cut & Pacing" },
  { id: "Uee5VFsmlLo", title: "Advanced Color Grading" }
];

export function Hero() {
  const [demoOpen, setDemoOpen] = useState(false);

  return (
    <section className="pt-24 pb-10 text-center bg-[#050505] text-white">
      <div className="max-w-[1100px] mx-auto px-6">
        <h1 className="text-[clamp(44px,8vw,76px)] font-extrabold tracking-[-0.03em] leading-[0.95] mb-6 text-white text-glow">
          High-quality videos in{" "}
          <span className="text-[#0A84FF] brand-glow">minutes.</span>{" "}
          Not{" "}
          <span className="text-[#0A84FF] brand-glow">hours.</span>
        </h1>

        <p className="text-[clamp(16px,2.1vw,19px)] text-neutral-400 max-w-[740px] mx-auto mb-8 leading-relaxed">
          We are a dedicated group of professional editors who edit, polish, and optimize your videos.
          Get premium quality content for TikTok, Reels, YouTube, and X in minutes, not hours.
        </p>

        <div className="flex gap-3 justify-center flex-wrap mb-16">
          <Link
            to="/login"
            className="inline-flex items-center gap-2 px-6 py-3 rounded-xl text-sm font-semibold bg-white text-black no-underline hover:-translate-y-[1px] hover:shadow-[0_0_20px_rgba(255,255,255,0.4)] transition-all"
          >
            Get started
          </Link>
          <button
            onClick={() => setDemoOpen(true)}
            className="inline-flex items-center gap-2 px-6 py-3 rounded-xl text-sm font-semibold bg-[#111] text-white border border-neutral-800 no-underline hover:-translate-y-[1px] hover:bg-neutral-900 transition-all cursor-pointer"
          >
            Watch demo
          </button>
        </div>

        {/* Video Portfolio Grid */}
        <div className="max-w-[1000px] mx-auto my-20">
          <h2 className="text-3xl font-extrabold tracking-tight text-white mb-10 text-glow">
            Our Video Portfolio
          </h2>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
            {portfolioVideos.map((video) => (
              <div 
                key={video.id} 
                className="bg-[#0c0c0e] border border-neutral-900 rounded-2xl p-4 hover:border-neutral-700 hover:shadow-[0_0_25px_rgba(10,132,255,0.15)] transition-all group"
              >
                <div className="overflow-hidden rounded-xl aspect-video mb-3 bg-black">
                  <iframe
                    className="w-full h-full border-0"
                    src={`https://www.youtube.com/embed/${video.id}`}
                    title={video.title}
                    allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share"
                    allowFullScreen
                  />
                </div>
                <h3 className="text-left font-bold text-sm text-neutral-300 group-hover:text-white transition-colors px-1">
                  {video.title}
                </h3>
              </div>
            ))}
          </div>
          <PlatformPills />
        </div>

        {/* Platforms row */}
        <div className="mt-20 py-7 border-y border-neutral-900">
          <div className="flex items-center justify-center gap-8 flex-wrap">
            <span className="text-[13px] text-neutral-500 font-medium">Premium editing for</span>
            <div className="flex gap-7 flex-wrap items-center justify-center">
              {["Instagram", "Facebook", "TikTok", "YouTube", "X"].map((p) => (
                <span key={p} className="text-[15px] font-semibold tracking-tight text-neutral-400 hover:text-white transition-colors">
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
