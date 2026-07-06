import { Shield } from "lucide-react";

const features = [
  {
    icon: "✦",
    title: "Top 1% Hand-vetted editors",
    desc: "Your projects are matched with elite, professional video editors.",
  },
  {
    icon: "⚡",
    title: "Ultra-fast delivery",
    desc: "Get your fully polished video drafts back in minutes or hours, not days.",
  },
  {
    icon: "▦",
    title: "Multi-format scaling",
    desc: "We auto-crop and optimize for Reels, TikToks, Shorts, and YouTube in a single request.",
  },
  {
    icon: "↗",
    title: "Engagement-focused hook & pacing",
    desc: "We design hooks, subtitles, sound design, and zoom-cuts to maximize your retention rates.",
  },
  {
    icon: "🌐",
    title: "Global translation (100+ languages)",
    desc: "We translate and subtitle your videos into over 100 languages to reach a worldwide audience.",
  },
];

export function Features() {
  return (
    <section className="section" id="features">
      <div className="section-head">
        <h2>
          High-quality editing.<br />
          Simplified.
        </h2>
        <p>Clean design, premium results. Zero communication friction.</p>
      </div>
      <div className="features">
        {features.map((f) => (
          <div key={f.title} className="feature">
            <div className="feature-icon">{f.icon}</div>
            <h4 className="text-white font-bold">{f.title}</h4>
            <p>{f.desc}</p>
          </div>
        ))}
      </div>
      <div className="guarantee">
        <Shield size={16} className="text-[#0A84FF] shrink-0" />
        <span>100% Satisfaction Guarantee. We edit until you love the result. Cancel at any time.</span>
      </div>
    </section>
  );
}
