import type { ReactNode } from "react";

export type ProviderId = "meta" | "tiktok" | "twitter" | "youtube";

export type ProviderMeta = {
  id: ProviderId;
  name: string;
  description: string;
  color: string; // tailwind gradient `from-X to-Y`
  icon: ReactNode;
};

const META_SVG = (
  <svg viewBox="0 0 24 24" fill="currentColor" className="w-6 h-6">
    <path d="M13.5 22v-8h2.7l.4-3.2H13.5V8.5c0-.9.3-1.5 1.6-1.5h1.7V4.1c-.3 0-1.3-.1-2.5-.1-2.5 0-4.2 1.5-4.2 4.3v2.5H7.3V14h2.8v8h3.4z" />
  </svg>
);

const TIKTOK_SVG = (
  <svg viewBox="0 0 24 24" fill="currentColor" className="w-6 h-6">
    <path d="M19.6 8.2c-1.2 0-2.3-.4-3.2-1.1v6.4c0 3.5-2.8 6.3-6.3 6.3S3.8 17 3.8 13.5 6.6 7.2 10.1 7.2c.4 0 .7 0 1 .1v2.8c-.3-.1-.7-.2-1-.2-1.9 0-3.5 1.6-3.5 3.6s1.6 3.5 3.5 3.5 3.5-1.6 3.5-3.5V3.5h2.7c.3 1.2 1.3 2.2 2.5 2.5v2.2z" />
  </svg>
);

const TWITTER_SVG = (
  <svg viewBox="0 0 24 24" fill="currentColor" className="w-6 h-6">
    <path d="M17.5 4.5h3.1l-6.8 7.8 8 10.6h-6.3l-4.9-6.4-5.6 6.4H2l7.3-8.3L1.7 4.5h6.4l4.4 5.9 5-5.9z" />
  </svg>
);

const YOUTUBE_SVG = (
  <svg viewBox="0 0 24 24" fill="currentColor" className="w-6 h-6">
    <path d="M21.6 7.2c-.2-.8-.8-1.4-1.6-1.6-1.6-.4-8-.4-8-.4s-6.4 0-8 .4c-.8.2-1.4.8-1.6 1.6C2 8.8 2 12 2 12s0 3.2.4 4.8c.2.8.8 1.4 1.6 1.6 1.6.4 8 .4 8 .4s6.4 0 8-.4c.8-.2 1.4-.8 1.6-1.6.4-1.6.4-4.8.4-4.8s0-3.2-.4-4.8zM10 15.2V8.8l5.2 3.2-5.2 3.2z" />
  </svg>
);

export const PROVIDERS: ProviderMeta[] = [
  {
    id: "meta",
    name: "Instagram & Facebook",
    description: "Connect Instagram Business and Facebook Pages",
    color: "from-blue-500 to-purple-500",
    icon: META_SVG,
  },
  {
    id: "tiktok",
    name: "TikTok",
    description: "Publish videos directly to your TikTok profile",
    color: "from-gray-800 to-gray-900",
    icon: TIKTOK_SVG,
  },
  {
    id: "twitter",
    name: "X (Twitter)",
    description: "Publish tweets and media to your X profile",
    color: "from-neutral-700 to-neutral-900",
    icon: TWITTER_SVG,
  },
  {
    id: "youtube",
    name: "YouTube",
    description: "Upload videos to your YouTube channel",
    color: "from-red-500 to-red-600",
    icon: YOUTUBE_SVG,
  },
];

export function getProvider(id: string): ProviderMeta | undefined {
  return PROVIDERS.find((p) => p.id === id);
}
