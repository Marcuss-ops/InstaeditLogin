import { useState } from "react";
import { Link } from "react-router-dom";
import { PlatformPills } from "./PlatformPills";
import { DemoModal } from "./DemoModal";

export function Hero() {
  const [demoOpen, setDemoOpen] = useState(false);

  return (
    <section className="pt-24 pb-10 text-center">
      <div className="max-w-[1100px] mx-auto px-6">
        <h1 className="text-[clamp(44px,8vw,76px)] font-extrabold tracking-[-0.03em] leading-[0.95] mb-6 text-black">
          High-quality videos in{" "}
          <span className="text-[#0A84FF]">minutes.</span>{" "}
          Not{" "}
          <span className="text-[#0A84FF]">hours.</span>
        </h1>

        <p className="text-[clamp(16px,2.1vw,19px)] text-neutral-700 max-w-[740px] mx-auto mb-8 leading-relaxed">
          We are a dedicated group of professional editors who edit, polish, and optimize your videos.
          Get premium quality content for TikTok, Reels, YouTube, and X in minutes, not hours.
        </p>

        <div className="flex gap-3 justify-center flex-wrap mb-[72px]">
          <Link
            to="/login"
            className="inline-flex items-center gap-2 px-[18px] py-[10px] rounded-xl text-sm font-semibold bg-black text-white no-underline hover:-translate-y-[1px] hover:bg-neutral-900 transition-all"
          >
            Get started
          </Link>
          <button
            onClick={() => setDemoOpen(true)}
            className="inline-flex items-center gap-2 px-[18px] py-[10px] rounded-xl text-sm font-semibold bg-white text-black border border-black no-underline hover:-translate-y-[1px] hover:bg-neutral-50 transition-all cursor-pointer"
          >
            Watch demo
          </button>
        </div>

        {/* Upload mockup */}
        <div className="max-w-[920px] mx-auto">
          <div className="max-w-[420px] mx-auto bg-white border border-neutral-100 rounded-xl shadow-[0_10px_40px_rgba(0,0,0,0.06)] overflow-hidden">
            <div className="flex items-center gap-2.5 py-3 px-3.5 bg-neutral-50 border-b border-neutral-100">
              <div className="flex gap-[5px]">
                <span className="w-2 h-2 rounded-full bg-neutral-300" />
                <span className="w-2 h-2 rounded-full bg-neutral-300" />
              </div>
              <span className="text-[12px] text-neutral-500 font-medium">New editing request</span>
            </div>
            <div className="py-8 px-6 text-center">
              <div className="w-12 h-12 mx-auto mb-3.5 rounded-[10px] bg-neutral-100 grid place-items-center">
                <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                  <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                  <polyline points="17 8 12 3 7 8" />
                  <line x1="12" y1="3" x2="12" y2="15" />
                </svg>
              </div>
              <p className="mb-3.5 font-semibold text-sm text-black">Project: summer-promo.mp4</p>
              <div className="flex items-center justify-center gap-1.5 text-xs text-[#0A84FF] font-semibold bg-blue-50/50 py-1.5 px-3 rounded-lg max-w-[180px] mx-auto animate-pulse">
                <span>Team is editing...</span>
              </div>
            </div>
          </div>
          <PlatformPills />
        </div>

        {/* Platforms row */}
        <div className="mt-20 py-7 border-y border-neutral-100">
          <div className="flex items-center justify-center gap-8 flex-wrap">
            <span className="text-[13px] text-neutral-400 font-medium">Premium editing for</span>
            <div className="flex gap-7 flex-wrap items-center justify-center">
              {["Instagram", "Facebook", "TikTok", "YouTube", "X"].map((p) => (
                <span key={p} className="text-[15px] font-semibold tracking-tight text-black/55">
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
