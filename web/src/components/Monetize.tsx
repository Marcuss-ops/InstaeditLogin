import { TrendingUp, Repeat, Handshake, Link2, Wallet } from "lucide-react";

const streams = [
  {
    icon: TrendingUp,
    title: "Optimized for CPM",
    desc: "We engineer every cut to maximize mid-roll ad placements and viewer retention, so every minute watched pays out more.",
  },
  {
    icon: Repeat,
    title: "Cross-platform reposting",
    desc: "One master edit flows to YouTube, TikTok, Instagram and Shorts — turning one video into 3x revenue streams.",
  },
  {
    icon: Handshake,
    title: "Sponsor-ready portfolio",
    desc: "Polished, on-brand edits that attract premium sponsorship deals and long-term brand partnerships.",
  },
  {
    icon: Link2,
    title: "Affiliate integration",
    desc: "Tasteful product mentions, pinned comments and on-screen CTAs convert viewers into commissions.",
  },
];

const stats = [
  { value: "$4.8K", label: "Avg. monthly creator earnings" },
  { value: "6.4×", label: "Watch-time vs. raw footage" },
  { value: "1,200+", label: "Brand deals unlocked monthly" },
];

export function Monetize() {
  return (
    <section className="section monetize reveal" id="monetize">
      <div className="section-head">
        <div className="eyebrow">
          <Wallet size={12} aria-hidden="true" />
          Earn while you create
        </div>
        <h2>
          Monetize and make money<br />
          <span style={{ color: "var(--muted)" }}>while we edit your videos.</span>
        </h2>
        <p>
          Your views, your revenue. We optimize every cut for watch time and
          ad performance — so you earn back the cost of editing, every upload.
        </p>
      </div>

      <div className="monetize-grid">
        {streams.map(({ icon: Icon, ...rest }) => (
          <div key={rest.title} className="monetize-card">
            <div className="monetize-icon">
              <Icon size={18} />
            </div>
            <h4 className="text-white font-bold">{rest.title}</h4>
            <p>{rest.desc}</p>
          </div>
        ))}
      </div>

      <div className="earnings-stats">
        {stats.map((s) => (
          <div key={s.label} className="stat">
            <div className="stat-value">{s.value}</div>
            <div className="stat-label">{s.label}</div>
          </div>
        ))}
      </div>
    </section>
  );
}
