import { Calendar, Layout, BarChart3, Users, Shield, Globe } from "lucide-react";

const features = [
  {
    icon: <Users size={20} />,
    title: "Top 1% Hand-vetted editors",
    desc: "Your projects are matched with elite, professional video editors.",
  },
  {
    icon: <Calendar size={20} />,
    title: "Ultra-fast delivery",
    desc: "Get your fully polished video drafts back in minutes or hours, not days.",
  },
  {
    icon: <Layout size={20} />,
    title: "Multi-format scaling",
    desc: "We auto-crop and optimize for Reels, TikToks, Shorts, and YouTube in a single request.",
  },
  {
    icon: <BarChart3 size={20} />,
    title: "Engagement-focused hook & pacing",
    desc: "We design hooks, subtitles, sound design, and zoom-cuts to maximize your retention rates.",
  },
  {
    icon: <Globe size={20} />,
    title: "Global translation (100+ languages)",
    desc: "We translate and subtitle your videos into over 100 languages to reach a worldwide audience.",
  },
];

export function Features() {
  return (
    <section id="features" className="py-24 bg-black border-y border-neutral-900 text-white">
      <div className="max-w-[1100px] mx-auto px-6">
        <div className="text-center max-w-[640px] mx-auto mb-14">
          <h2 className="text-[clamp(32px,4.5vw,44px)] font-extrabold tracking-[-0.02em] mb-3 text-white text-glow">
            High-quality editing. Simplified.
          </h2>
          <p className="text-neutral-400 text-[17px]">
            Clean design, premium results. Zero communication friction.
          </p>
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
          {features.map((f) => (
            <div
              key={f.title}
              className="bg-[#0c0c0e] border border-neutral-900 rounded-xl p-6 flex gap-4 items-start hover:border-neutral-800 transition-colors"
            >
              <div className="w-10 h-10 min-w-10 rounded-[10px] bg-neutral-900 border border-neutral-800 grid place-items-center text-[#0A84FF] brand-glow">
                {f.icon}
              </div>
              <div>
                <h4 className="mt-0.5 mb-1.5 text-[17px] font-bold text-white">{f.title}</h4>
                <p className="text-neutral-400 text-sm leading-relaxed">{f.desc}</p>
              </div>
            </div>
          ))}
        </div>

        {/* Security banner */}
        <div className="mt-14 max-w-[860px] mx-auto bg-[#0c0c0e] border border-neutral-900 rounded-xl py-4.5 px-5.5 flex items-center justify-center gap-3 text-center flex-wrap">
          <Shield size={18} className="text-[#0A84FF] shrink-0 brand-glow" />
          <p className="text-sm font-medium text-neutral-300">
            100% Satisfaction Guarantee. We edit until you love the result. Cancel at any time.
          </p>
        </div>
      </div>
    </section>
  );
}
