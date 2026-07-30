import type { ReactNode } from "react";
import tiktok from "./data/tiktok";
import instagram from "./data/instagram";
import facebook from "./data/facebook";
import threads from "./data/threads";
import youtube from "./data/youtube";
import linkedin from "./data/linkedin";
import twitter from "./data/twitter";

export type PlatformData = {
  slug: string;
  name: string;
  color: string;
  icon: ReactNode;
  heroTagline: string;
  heroDescription: string;
  noteTitle: string;
  noteDescription: string;
  contentTypes: string[];
  features: { icon: ReactNode; title: string; description: string }[];
  comparison: {
    us: { label: string; items: string[] };
    them: { label: string; items: string[] };
  };
  codeExample: string;
  faq: { q: string; a: string }[];
};

const platforms: Record<string, PlatformData> = {
  tiktok,
  instagram,
  facebook,
  threads,
  youtube,
  linkedin,
  twitter,
};

export function loadPlatformData(slug: string): Promise<PlatformData | null> {
  return Promise.resolve(platforms[slug] ?? null);
}
