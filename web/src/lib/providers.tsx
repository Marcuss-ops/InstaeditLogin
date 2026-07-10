import type { ReactNode } from "react";

export type ProviderId = "meta" | "tiktok" | "twitter" | "youtube" | "linkedin";

export type ProviderMeta = {
  id: ProviderId;
  name: string;
  description: string;
  color: string; // tailwind gradient `from-X to-Y` (for hover accent bar)
  iconBg: string; // tailwind gradient for icon background
  glowColor: string; // CSS color for icon glow
  nameGradient: string; // CSS gradient for hover name text effect
  icon: ReactNode;
};

const META_SVG = (
  <svg viewBox="0 0 24 24" fill="currentColor" className="w-5 h-5">
    <path d="M13.5 22v-8h2.7l.4-3.2H13.5V8.5c0-.9.3-1.5 1.6-1.5h1.7V4.1c-.3 0-1.3-.1-2.5-.1-2.5 0-4.2 1.5-4.2 4.3v2.5H7.3V14h2.8v8h3.4z" />
  </svg>
);

const TIKTOK_SVG = (
  <svg viewBox="0 0 24 24" fill="currentColor" className="w-5 h-5">
    <path d="M19.6 8.2c-1.2 0-2.3-.4-3.2-1.1v6.4c0 3.5-2.8 6.3-6.3 6.3S3.8 17 3.8 13.5 6.6 7.2 10.1 7.2c.4 0 .7 0 1 .1v2.8c-.3-.1-.7-.2-1-.2-1.9 0-3.5 1.6-3.5 3.6s1.6 3.5 3.5 3.5 3.5-1.6 3.5-3.5V3.5h2.7c.3 1.2 1.3 2.2 2.5 2.5v2.2z" />
  </svg>
);

const TWITTER_SVG = (
  <svg viewBox="0 0 24 24" fill="currentColor" className="w-5 h-5">
    <path d="M17.5 4.5h3.1l-6.8 7.8 8 10.6h-6.3l-4.9-6.4-5.6 6.4H2l7.3-8.3L1.7 4.5h6.4l4.4 5.9 5-5.9z" />
  </svg>
);

const YOUTUBE_SVG = (
  <svg viewBox="0 0 24 24" fill="currentColor" className="w-5 h-5">
    <path d="M21.6 7.2c-.2-.8-.8-1.4-1.6-1.6-1.6-.4-8-.4-8-.4s-6.4 0-8 .4c-.8.2-1.4.8-1.6 1.6C2 8.8 2 12 2 12s0 3.2.4 4.8c.2.8.8 1.4 1.6 1.6 1.6.4 8 .4 8 .4s6.4 0 8-.4c.8-.2 1.4-.8 1.6-1.6.4-1.6.4-4.8.4-4.8s0-3.2-.4-4.8zM10 15.2V8.8l5.2 3.2-5.2 3.2z" />
  </svg>
);

const LINKEDIN_SVG = (
  <svg viewBox="0 0 24 24" fill="currentColor" className="w-5 h-5">
    <path d="M20.5 2h-17A1.5 1.5 0 002 3.5v17A1.5 1.5 0 003.5 22h17a1.5 1.5 0 001.5-1.5v-17A1.5 1.5 0 0020.5 2zM8 19H5v-9h3zM6.5 8.25A1.75 1.75 0 118.3 6.5a1.78 1.78 0 01-1.8 1.75zM19 19h-3v-4.74c0-1.42-.6-1.93-1.38-1.93A1.74 1.74 0 0013 14.19a.66.66 0 000 .14V19h-3v-9h2.9v1.3a3.11 3.11 0 012.7-1.4c1.55 0 3.4.86 3.4 3.66z" />
  </svg>
);

export const PROVIDERS: ProviderMeta[] = [
  {
    id: "meta",
    name: "Instagram & Facebook",
    description: "Connect Instagram Business and Facebook Pages",
    color: "from-[#0A84FF] to-[#7B61FF]",
    iconBg: "from-[#0A84FF] to-[#7B61FF]",
    glowColor: "rgba(10,132,255,0.35)",
    nameGradient: "linear-gradient(135deg, #0A84FF, #7B61FF)",
    icon: META_SVG,
  },
  {
    id: "tiktok",
    name: "TikTok",
    description: "Publish videos directly to your TikTok profile",
    color: "from-[#ff0050] to-[#00f2ea]",
    iconBg: "from-[#ff0050] to-[#00f2ea]",
    glowColor: "rgba(255,0,80,0.35)",
    nameGradient: "linear-gradient(135deg, #ff0050, #00f2ea)",
    icon: TIKTOK_SVG,
  },
  {
    id: "twitter",
    name: "X (Twitter)",
    description: "Publish tweets and media to your X profile",
    color: "from-[#e8e8ef] to-[#9aa0aa]",
    iconBg: "from-[#2a2a32] to-[#1a1a22]",
    glowColor: "rgba(200,200,210,0.2)",
    nameGradient: "linear-gradient(135deg, #e8e8ef, #9aa0aa)",
    icon: TWITTER_SVG,
  },
  {
    id: "youtube",
    name: "YouTube",
    description: "Upload videos to your YouTube channel",
    color: "from-[#ff0000] to-[#cc0000]",
    iconBg: "from-[#ff0000] to-[#cc0000]",
    glowColor: "rgba(255,0,0,0.35)",
    nameGradient: "linear-gradient(135deg, #ff4444, #ff0000)",
    icon: YOUTUBE_SVG,
  },
  {
    id: "linkedin",
    name: "LinkedIn",
    description: "Publish posts and articles to your LinkedIn profile",
    color: "from-[#0A66C2] to-[#004182]",
    iconBg: "from-[#0A66C2] to-[#004182]",
    glowColor: "rgba(10,102,194,0.35)",
    nameGradient: "linear-gradient(135deg, #0A66C2, #6aa8e0)",
    icon: LINKEDIN_SVG,
  },
];

export function getProvider(id: string): ProviderMeta | undefined {
  return PROVIDERS.find((p) => p.id === id);
}
