import { Calendar, Layout, BarChart3, Users, Shield } from "lucide-react";

const features = [
  {
    icon: <Calendar size={20} />,
    title: "Editorial calendar",
    desc: "drag and drop scheduling",
  },
  {
    icon: <Layout size={20} />,
    title: "Auto-adaptation",
    desc: "9:16, 1:1, 16:9 in one click",
  },
  {
    icon: <BarChart3 size={20} />,
    title: "Unified Analytics",
    desc: "performance in a single dashboard",
  },
  {
    icon: <Users size={20} />,
    title: "Team & Approvals",
    desc: "drafts, comments, and roles",
  },
];

export function Features() {
  return (
    <section id="features" className="py-24 bg-neutral-100 border-y border-neutral-200">
      <div className="max-w-[1100px] mx-auto px-6">
        <div className="text-center max-w-[640px] mx-auto mb-14">
          <h2 className="text-[clamp(32px,4.5vw,44px)] font-extrabold tracking-[-0.02em] mb-3 text-black">
            Everything you need, nothing more
          </h2>
          <p className="text-neutral-500 text-[17px]">
            Clean design, inspired by Notion and Figma. Fast, minimal, powerful.
          </p>
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          {features.map((f) => (
            <div
              key={f.title}
              className="bg-white border border-neutral-100 rounded-xl p-6 flex gap-4 items-start"
            >
              <div className="w-10 h-10 min-w-10 rounded-[10px] bg-neutral-100 grid place-items-center text-black">
                {f.icon}
              </div>
              <div>
                <h4 className="mt-0.5 mb-1.5 text-[17px] font-bold text-black">{f.title}</h4>
                <p className="text-neutral-500 text-sm leading-relaxed">{f.desc}</p>
              </div>
            </div>
          ))}
        </div>

        {/* Security banner */}
        <div className="mt-14 max-w-[860px] mx-auto bg-white border border-neutral-100 rounded-xl py-4.5 px-5.5 flex items-center justify-center gap-3 text-center flex-wrap">
          <Shield size={18} className="text-[#0A84FF] shrink-0" />
          <p className="text-sm font-medium text-neutral-700">
            Official OAuth. We do not store passwords. Revoke access at any time.
          </p>
        </div>
      </div>
    </section>
  );
}
