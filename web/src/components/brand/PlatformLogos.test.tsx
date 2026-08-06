import { describe, expect, it } from "vitest";
import {
  FacebookLogo,
  getPlatformLogo,
  getProviderRegistryEntry,
  GoogleDriveLogo,
  InstagramLogo,
  LinkedInLogo,
  PLATFORM_REGISTRY,
  PROVIDER_REGISTRY,
  ThreadsLogo,
  TikTokLogo,
  YouTubeLogo,
  XLogo,
} from "./PlatformLogos";

describe("PlatformLogos", () => {
  it("keeps every marketing provider in the canonical registry", () => {
    expect(PLATFORM_REGISTRY.map((platform) => platform.key)).toEqual([
      "instagram",
      "tiktok",
      "youtube",
      "facebook",
      "x",
      "linkedin",
      "threads",
    ]);
    expect(PLATFORM_REGISTRY.map((platform) => platform.Logo)).toEqual([
      InstagramLogo,
      TikTokLogo,
      YouTubeLogo,
      FacebookLogo,
      XLogo,
      LinkedInLogo,
      ThreadsLogo,
    ]);
  });

  it("keeps YouTube in the canonical registry", () => {
    expect(PLATFORM_REGISTRY.find((platform) => platform.key === "youtube")?.Logo).toBe(YouTubeLogo);
    expect(getPlatformLogo("youtube")).toBe(YouTubeLogo);
  });

  it("also exposes the non-marketing Google Drive brand", () => {
    expect(getPlatformLogo("google-drive")).toBe(GoogleDriveLogo);
    expect(PROVIDER_REGISTRY.find((entry) => entry.id === "google-drive")?.includeInMarketing).toBe(false);
  });

  it("resolves the X alias to the canonical Twitter entry", () => {
    expect(getProviderRegistryEntry("x")?.id).toBe("twitter");
    expect(getPlatformLogo("twitter")).toBe(XLogo);
    expect(getPlatformLogo("x")).toBe(XLogo);
  });

  it("keeps provider metadata and brand assets in one entry", () => {
    const youtube = getProviderRegistryEntry("youtube");
    expect(youtube).toMatchObject({
      name: "YouTube",
      brandColor: "#FF0000",
      includeInMarketing: true,
    });
    expect(youtube?.Logo).toBe(YouTubeLogo);
  });
});
