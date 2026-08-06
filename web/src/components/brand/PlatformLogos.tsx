import type { SVGProps } from "react";

export type LogoProps = SVGProps<SVGSVGElement> & { className?: string };

export type CanonicalProviderId =
  | "instagram"
  | "facebook"
  | "threads"
  | "tiktok"
  | "twitter"
  | "youtube"
  | "linkedin"
  | "google-drive";

export type PlatformLogoId = CanonicalProviderId | "x";

export function InstagramLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} {...rest} aria-hidden="true">
      <rect x="2" y="2" width="20" height="20" rx="5" fill="#E4405F" />
      <circle cx="12" cy="12" r="4.2" stroke="#fff" strokeWidth="1.6" />
      <circle cx="17.4" cy="6.6" r="0.95" fill="#fff" />
    </svg>
  );
}

export function FacebookLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} {...rest} aria-hidden="true">
      <rect x="2" y="2" width="20" height="20" rx="4" fill="#1877F2" />
      <path d="M13.6 21v-7.2h2.4l.36-2.8H13.6V9.05c0-.81.23-1.35 1.4-1.35h1.5V5.15c-.26-.03-1.15-.11-2.18-.11-2.16 0-3.64 1.32-3.64 3.74v2.22H8.32v2.8h2.36V21h2.92z" fill="#fff" />
    </svg>
  );
}

export function YouTubeLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} {...rest} aria-hidden="true">
      <rect x="2" y="5" width="20" height="14" rx="3.5" fill="#FF0000" />
      <path d="M10 9.2v5.6l4.4-2.8L10 9.2z" fill="#fff" />
    </svg>
  );
}

export function TikTokLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} {...rest} aria-hidden="true">
      <rect x="2" y="2" width="20" height="20" rx="4.5" fill="#000" />
      <path d="M15.6 4.5v8.2a2.45 2.45 0 1 1-2.45-2.45" stroke="#25F4EE" strokeWidth="1.7" strokeLinecap="round" />
      <path d="M15.85 4.5v8.2a2.45 2.45 0 1 1-2.45-2.45" stroke="#FE2C55" strokeWidth="1.7" strokeLinecap="round" transform="translate(0.5 -0.4)" />
      <path d="M15.6 4.5a4.2 4.2 0 0 0 4.2 4.2" stroke="#25F4EE" strokeWidth="1.7" strokeLinecap="round" />
    </svg>
  );
}

export function XLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} {...rest} aria-hidden="true">
      <rect width="24" height="24" rx="4" fill="#fff" />
      <path d="M14.65 11l4.05-5h-1.55l-3.45 4.34L10.85 6h-4.4l4.5 7.5L6 19h1.55l3.8-4.74L14.6 19h4l-4.65-8h.7zm-2 7l-.5-.7L7.85 7h1.4l4.4 6.3 1.95 2.7.5.7-3.45 0z" fill="#000" />
    </svg>
  );
}

export function LinkedInLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} {...rest} aria-hidden="true">
      <rect x="2" y="2" width="20" height="20" rx="3" fill="#0A66C2" />
      <circle cx="7" cy="8" r="1.15" fill="#fff" />
      <rect x="6.05" y="10" width="2.1" height="6.5" rx="0.3" fill="#fff" />
      <path d="M10 16.5v-6.5h2v1.1c.45-.7 1.3-1.3 2.4-1.3 1.7 0 2.6 1.1 2.6 3V16.5h-2v-3.4c0-.9-.4-1.5-1.2-1.5s-1.2.5-1.2 1.5V16.5H10z" fill="#fff" />
    </svg>
  );
}

export function ThreadsLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} {...rest} aria-hidden="true">
      <rect width="24" height="24" rx="6" fill="#000" />
      <path d="M12 6.5c2.7 0 4.7 1.6 4.7 4.7s-2 4.7-4.7 4.7-4.7-1.6-4.7-4.7" stroke="#fff" strokeWidth="1.4" strokeLinecap="round" />
      <path d="M12 6.5c-3 0-5 2-5 5s2 5 5 5" stroke="#fff" strokeWidth="1.4" strokeLinecap="round" />
    </svg>
  );
}

export function GoogleDriveLogo({ className = "w-6 h-6", ...rest }: LogoProps) {
  return (
    <svg viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" className={className} {...rest} aria-hidden="true">
      <path d="M7.2 3h5.1l6.5 11.2h-5.1L7.2 3z" fill="#0F9D58" />
      <path d="M5.1 6.6L2.5 11l3.7 6.4h5.1L7.6 11l2.6-4.4H5.1z" fill="#F4B400" />
      <path d="M7.2 19h10.2l2.1-3.6h-10.2L7.2 19z" fill="#4285F4" />
    </svg>
  );
}

export type ProviderRegistryEntry = {
  id: CanonicalProviderId;
  aliases: readonly PlatformLogoId[];
  name: string;
  Logo: (props: LogoProps) => React.ReactElement;
  brandColor: string;
  gradient: string;
  iconBg: string;
  glowColor: string;
  nameGradient: string;
  description: string;
  solidColor: string;
  includeInMarketing: boolean;
};

/**
 * The single source of truth for provider identity, brand artwork and visual
 * metadata. Consumers should derive views from this registry instead of
 * defining provider logos or colors locally.
 */
export const PROVIDER_REGISTRY: readonly ProviderRegistryEntry[] = [
  {
    id: "instagram",
    aliases: [],
    name: "Instagram",
    Logo: InstagramLogo,
    brandColor: "#E4405F",
    gradient: "from-[#E1306C] to-[#C13584]",
    iconBg: "from-[#E1306C] to-[#C13584]",
    glowColor: "rgba(225,48,108,0.35)",
    nameGradient: "linear-gradient(135deg, #E1306C, #C13584)",
    description: "Image and Reel publishing to Instagram Business",
    solidColor: "rgb(225, 48, 108)",
    includeInMarketing: true,
  },
  {
    id: "facebook",
    aliases: [],
    name: "Facebook",
    Logo: FacebookLogo,
    brandColor: "#1877F2",
    gradient: "from-[#0A84FF] to-[#0866FF]",
    iconBg: "from-[#0A84FF] to-[#0866FF]",
    glowColor: "rgba(10,132,255,0.35)",
    nameGradient: "linear-gradient(135deg, #0A84FF, #0866FF)",
    description: "Text and image publishing to Facebook Pages",
    solidColor: "rgb(10, 132, 255)",
    includeInMarketing: true,
  },
  {
    id: "threads",
    aliases: [],
    name: "Threads",
    Logo: ThreadsLogo,
    brandColor: "#FFFFFF",
    gradient: "from-[#000000] to-[#333333]",
    iconBg: "from-[#000000] to-[#333333]",
    glowColor: "rgba(0,0,0,0.25)",
    nameGradient: "linear-gradient(135deg, #000000, #444444)",
    description: "Text and image publishing to Threads",
    solidColor: "rgb(0, 0, 0)",
    includeInMarketing: true,
  },
  {
    id: "tiktok",
    aliases: [],
    name: "TikTok",
    Logo: TikTokLogo,
    brandColor: "#25F4EE",
    gradient: "from-[#ff0050] to-[#00f2ea]",
    iconBg: "from-[#ff0050] to-[#00f2ea]",
    glowColor: "rgba(255,0,80,0.35)",
    nameGradient: "linear-gradient(135deg, #ff0050, #00f2ea)",
    description: "Video publishing with privacy and comment/duet/stitch controls",
    solidColor: "rgb(255, 0, 80)",
    includeInMarketing: true,
  },
  {
    id: "twitter",
    aliases: ["x"],
    name: "X (Twitter)",
    Logo: XLogo,
    brandColor: "#FFFFFF",
    gradient: "from-[#e8e8ef] to-[#9aa0aa]",
    iconBg: "from-[#2a2a32] to-[#1a1a22]",
    glowColor: "rgba(200,200,210,0.2)",
    nameGradient: "linear-gradient(135deg, #e8e8ef, #9aa0aa)",
    description: "Text and single-image publishing to X (Twitter)",
    solidColor: "rgb(200, 200, 210)",
    includeInMarketing: true,
  },
  {
    id: "youtube",
    aliases: [],
    name: "YouTube",
    Logo: YouTubeLogo,
    brandColor: "#FF0000",
    gradient: "from-[#ff0000] to-[#cc0000]",
    iconBg: "from-[#ff0000] to-[#cc0000]",
    glowColor: "rgba(255,0,0,0.35)",
    nameGradient: "linear-gradient(135deg, #ff4444, #ff0000)",
    description: "Video upload with title, description, and privacy settings",
    solidColor: "rgb(255, 0, 0)",
    includeInMarketing: true,
  },
  {
    id: "linkedin",
    aliases: [],
    name: "LinkedIn",
    Logo: LinkedInLogo,
    brandColor: "#0A66C2",
    gradient: "from-[#0A66C2] to-[#004182]",
    iconBg: "from-[#0A66C2] to-[#004182]",
    glowColor: "rgba(10,102,194,0.35)",
    nameGradient: "linear-gradient(135deg, #0A66C2, #6aa8e0)",
    description: "Text and single-image publishing to your personal LinkedIn profile",
    solidColor: "rgb(10, 102, 194)",
    includeInMarketing: true,
  },
  {
    id: "google-drive",
    aliases: [],
    name: "Google Drive",
    Logo: GoogleDriveLogo,
    brandColor: "#0F9D58",
    gradient: "from-[#34A853] to-[#0F9D58]",
    iconBg: "from-[#34A853] to-[#0F9D58]",
    glowColor: "rgba(52,168,83,0.35)",
    nameGradient: "linear-gradient(135deg, #34A853, #0F9D58)",
    description: "Import videos from your Google Drive",
    solidColor: "rgb(52, 168, 83)",
    includeInMarketing: false,
  },
];

export function getProviderRegistryEntry(id: PlatformLogoId): ProviderRegistryEntry | undefined {
  return PROVIDER_REGISTRY.find(
    (entry) => entry.id === id || entry.aliases.includes(id),
  );
}

/** Canonical brand-logo renderer with support for legacy aliases. */
export function PlatformLogo({
  platform,
  ...props
}: { platform: PlatformLogoId } & LogoProps) {
  const entry = getProviderRegistryEntry(platform);
  if (!entry) return null;
  return <entry.Logo {...props} />;
}

export function getPlatformLogo(platform: PlatformLogoId) {
  return getProviderRegistryEntry(platform)?.Logo;
}

type MarketingPlatformKey = Exclude<PlatformLogoId, "twitter" | "google-drive">;
const MARKETING_PROVIDER_IDS = [
  "instagram",
  "tiktok",
  "youtube",
  "facebook",
  "twitter",
  "linkedin",
  "threads",
] as const;

/** Legacy marketing view derived from PROVIDER_REGISTRY, preserving UI order. */
export const PLATFORM_REGISTRY: ReadonlyArray<{
  key: MarketingPlatformKey;
  name: string;
  Logo: ProviderRegistryEntry["Logo"];
  color: string;
}> = MARKETING_PROVIDER_IDS.map((id) => {
  const entry = PROVIDER_REGISTRY.find((candidate) => candidate.id === id);
  if (!entry || !entry.includeInMarketing) {
    throw new Error(`Missing marketing provider registry entry: ${id}`);
  }
  return {
    key: (id === "twitter" ? "x" : id) as MarketingPlatformKey,
    name: id === "twitter" ? "X" : entry.name,
    Logo: entry.Logo,
    color: entry.brandColor,
  };
});
