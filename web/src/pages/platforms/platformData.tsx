import type { ReactNode } from "react";
import {
  getProviderRegistryEntry,
} from "../../components/brand/PlatformLogos";
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

export type PlatformContent = Omit<PlatformData, "color" | "icon">;

const platformContent: Record<string, PlatformContent> = {
  tiktok,
  instagram,
  facebook,
  threads,
  youtube,
  linkedin,
  twitter,
};

/**
 * Combines editorial platform content with canonical provider branding. This
 * keeps public platform pages from owning a second copy of logo/color data.
 */
export function getPlatformData(slug: string): PlatformData | null {
  const provider = getProviderRegistryEntry(slug);
  const canonicalSlug = provider?.id ?? slug;
  const content = platformContent[canonicalSlug];
  if (!content || !provider) return null;

  return {
    ...content,
    color: provider.accentColor,
    icon: <provider.Logo className="w-6 h-6" />,
  };
}

export function loadPlatformData(slug: string): Promise<PlatformData | null> {
  return Promise.resolve(getPlatformData(slug));
}
