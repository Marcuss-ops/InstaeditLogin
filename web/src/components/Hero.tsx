import { useState } from "react";
import { Link } from "react-router-dom";
import { DemoModal } from "./DemoModal";

const portfolioVideos = [
  { id: "K9bbXKdXBPc", title: "Luffy's Gear 5 Awakening", score: 99, remark: "Outstanding hook, viral audio pattern" },
  { id: "uTkE5Gk9s6A", title: "Premium Production Edit", score: 97, remark: "Ultra-fast cuts, high retention pacing" },
  { id: "HihqIP9e7Z4", title: "Cinematic Pacing Showcase", score: 98, remark: "Perfect sound transitions, high watchtime" },
  { id: "_KzMwN31QCk", title: "Viral Anime Montage", score: 99, remark: "Flawless transitions and visual flow" }
];

export function Hero() {
  const [demoOpen, setDemoOpen] = useState(false);

  return (
    <>
      <section className="hero reveal">
        <h1>
          1 long video.<br />
          10 viral shorts.<br />
          <span className="text-white">Created in minutes.</span>
        </h1>
        <p>
          We are a team of top-tier video editors. We transform your raw files into 
          highly engaging, captioned, multi-format viral videos in minutes, not hours.
        </p>
        <div className="hero-ctas">
          <Link className="btn btn-primary" to="/login">
            Get started now →
          </Link>
          <button className="btn cursor-pointer" onClick={() => setDemoOpen(true)}>
            ◎ Watch quick demo
          </button>
          <a className="btn hover:border-[#5865F2] hover:text-[#5865F2]" href="https://discord.com/users/1201477873719050332" target="_blank" rel="noopener noreferrer">
            <svg viewBox="0 0 127.14 96.36" fill="currentColor" className="w-4 h-4 shrink-0">
              <path d="M107.7,8.07A105.15,105.15,0,0,0,77.26,0a77.19,77.19,0,0,0-3.3,6.83A96.67,96.67,0,0,0,53.22,6.83,77.19,77.19,0,0,0,49.88,0,105.15,105.15,0,0,0,19.44,8.07C3.66,31.58-1.86,54.65,1,77.53A105.73,105.73,0,0,0,32,96.36a77.7,77.7,0,0,0,7.14-11.59A68.52,68.52,0,0,1,28.8,79.52c.9-.66,1.8-1.34,2.66-2a75.58,75.58,0,0,0,74.22,0c.87.69,1.76,1.37,2.66,2a68.52,68.52,0,0,1-10.37,5.25,77.7,77.7,0,0,0,7.14,11.59,105.73,105.73,0,0,0,31-18.83C129,54.65,123.51,31.58,107.7,8.07ZM42.45,65.69C36.18,65.69,31,60,31,53S36.18,40.36,42.45,40.36,53.83,46,53.83,53,48.72,65.69,42.45,65.69Zm42.24,0C78.41,65.69,73.24,60,73.24,53S78.41,40.36,84.69,40.36,96.07,46,96.07,53,91,65.69,84.69,65.69Z" />
            </svg>
            Chat on Discord
          </a>
        </div>
      </section>

      <section className="section">
        <div className="section-head">
          <h2>Our Video Portfolio</h2>
          <p>See how we turn raw footage into high-retention viral content.</p>
        </div>
        <div className="portfolio-grid">
          {portfolioVideos.map((video) => (
            <div key={video.id} className="card">
              <div className="thumb">
                <div className="badge">{video.score} Virality Score</div>
                <iframe
                  className="w-full h-full border-0 absolute inset-0"
                  src={`https://www.youtube.com/embed/${video.id}`}
                  title={video.title}
                  allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share"
                  allowFullScreen
                />
              </div>
              <div className="card-body">
                <div className="card-title text-left text-white">{video.title}</div>
                <div className="card-meta text-left text-neutral-400">
                  <span className="text-[#8b5cf6] font-semibold">AI Insights: </span>
                  {video.remark}
                </div>
              </div>
            </div>
          ))}
        </div>
      </section>

      <DemoModal open={demoOpen} onClose={() => setDemoOpen(false)} />
    </>
  );
}
