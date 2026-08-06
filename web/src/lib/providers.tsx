import type { ReactNode } from "react";
import {
  PROVIDER_REGISTRY,
  PlatformLogo,
  normalizeProviderIdentifier,
  type CanonicalProviderId,
} from "../components/brand/PlatformLogos";

export type ProviderId = CanonicalProviderId;

export type ProviderMeta = {
  id: ProviderId;
  name: string;
  description: string;
  color: string;
  iconBg: string;
  glowColor: string;
  nameGradient: string;
  icon: ReactNode;
  solidColor?: string;
};

/**
 * Compatibility view for connection/login surfaces. Provider identity and
 * brand metadata come exclusively from the canonical brand registry.
 */
export const PROVIDERS: ProviderMeta[] = PROVIDER_REGISTRY.map((entry) => ({
  id: entry.id,
  name: entry.name,
  description: entry.description,
  color: entry.gradient,
  iconBg: entry.iconBg,
  glowColor: entry.glowColor,
  nameGradient: entry.nameGradient,
  icon: <PlatformLogo platform={entry.id} />,
  solidColor: entry.solidColor,
}));

export function getProvider(id: string): ProviderMeta | undefined {
  const canonicalID = normalizeProviderIdentifier(id);
  return PROVIDERS.find((provider) => provider.id === canonicalID);
}
