import { isValidElement } from "react";
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import {
  FacebookLogo,
  getPlatformLogo,
  getProviderRegistryEntry,
  GoogleDriveLogo,
  normalizeProviderIdentifier,
  InstagramLogo,
  LinkedInLogo,
  PLATFORM_REGISTRY,
  PROVIDER_REGISTRY,
  PlatformLogo,
  ProviderBadge,
  ThreadsLogo,
  TikTokLogo,
  YouTubeLogo,
  XLogo,
} from "./PlatformLogos";
import { getProvider, PROVIDERS } from "../../lib/providers";

describe("PlatformLogos", () => {
  it("keeps canonical provider IDs unique and complete", () => {
    const ids = PROVIDER_REGISTRY.map((entry) => entry.id);

    expect(ids).toEqual(expect.arrayContaining([
      "instagram",
      "facebook",
      "threads",
      "tiktok",
      "twitter",
      "youtube",
      "linkedin",
      "google-drive",
    ]));
    expect(new Set(ids).size).toBe(ids.length);
    expect(PROVIDER_REGISTRY.every((entry) => entry.name.length > 0)).toBe(true);
    expect(PROVIDER_REGISTRY.every((entry) => entry.description.length > 0)).toBe(true);
    expect(PROVIDER_REGISTRY.every((entry) => entry.brandColor.length > 0)).toBe(true);
  });

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
    const youtube = getProviderRegistryEntry("youtube");

    expect(youtube?.Logo).toBe(YouTubeLogo);
    expect(youtube?.id).toBe("youtube");
    expect(youtube?.name).toBe("YouTube");
    expect(youtube?.brandColor).toBe("#FF0000");
    expect(youtube?.includeInMarketing).toBe(true);
    expect(getPlatformLogo("youtube")).toBe(YouTubeLogo);
  });

  it("resolves every canonical provider ID through PlatformLogo", () => {
    const { container } = render(
      <div>
        {PROVIDER_REGISTRY.map((entry) => (
          <PlatformLogo
            key={entry.id}
            platform={entry.id}
            data-provider-logo={entry.id}
            className="catalog-logo"
          />
        ))}
      </div>,
    );

    const logos = container.querySelectorAll<SVGSVGElement>("svg[data-provider-logo]");
    expect(logos).toHaveLength(PROVIDER_REGISTRY.length);
    logos.forEach((logo) => {
      expect(logo).toHaveAttribute("viewBox", "0 0 24 24");
      expect(logo).toHaveClass("catalog-logo");
      expect(logo).toHaveAttribute("aria-hidden", "true");
    });
  });

  it("renders YouTube with the canonical red play-button artwork", () => {
    const { container } = render(<YouTubeLogo data-testid="youtube-logo" className="h-8 w-8" />);
    const logo = container.querySelector("svg");

    expect(logo).toHaveAttribute("data-testid", "youtube-logo");
    expect(logo).toHaveClass("h-8", "w-8");
    expect(logo?.querySelector('rect[fill="#FF0000"]')).toBeInTheDocument();
    expect(logo?.querySelector('path[fill="#fff"]')).toBeInTheDocument();
  });

  it("also exposes the non-marketing Google Drive brand", () => {
    expect(getPlatformLogo("google-drive")).toBe(GoogleDriveLogo);
    expect(PROVIDER_REGISTRY.find((entry) => entry.id === "google-drive")?.includeInMarketing).toBe(false);
  });

  it("resolves the X alias to the canonical Twitter entry", () => {
    const twitter = getProviderRegistryEntry("twitter");

    expect(normalizeProviderIdentifier(" X ")).toBe("twitter");
    expect(normalizeProviderIdentifier("TWITTER")).toBe("twitter");
    expect(twitter?.aliases).toEqual(["x"]);
    expect(getProviderRegistryEntry("x")).toBe(twitter);
    expect(getPlatformLogo("twitter")).toBe(XLogo);
    expect(getPlatformLogo("x")).toBe(XLogo);
    expect(getProvider("x")?.id).toBe("twitter");
    expect(getProvider("x")?.name).toBe("X (Twitter)");
  });

  it("renders the X alias with canonical Twitter identity", () => {
    const { container } = render(
      <>
        <PlatformLogo platform="x" data-testid="x-logo" />
        <ProviderBadge platform="x" showName />
      </>,
    );

    expect(container.querySelector("svg[data-testid='x-logo']")).toBeInTheDocument();
    expect(screen.getByLabelText("X (Twitter)"))
      .toHaveAttribute("data-provider", "twitter");
    expect(screen.getByText("X (Twitter)")).toBeInTheDocument();
  });

  it("renders the canonical YouTube badge and supports compact layout", () => {
    render(<ProviderBadge platform="youtube" compact showName />);

    const badge = screen.getByLabelText("YouTube");
    expect(badge).toHaveAttribute("data-provider", "youtube");
    expect(badge).toHaveTextContent("YouTube");
    expect(badge.className).toContain("p-0");
  });

  it("does not render a badge for an unknown provider", () => {
    const { container } = render(<ProviderBadge platform="unknown" />);
    expect(container).toBeEmptyDOMElement();
  });

  it("keeps the compatibility provider view aligned with the canonical registry", () => {
    expect(PROVIDERS.map((provider) => provider.id)).toEqual(PROVIDER_REGISTRY.map((entry) => entry.id));
    PROVIDERS.forEach((provider) => {
      const entry = getProviderRegistryEntry(provider.id);
      expect(provider.name).toBe(entry?.name);
      expect(provider.solidColor).toBe(entry?.solidColor);
      expect(isValidElement(provider.icon)).toBe(true);
      if (isValidElement(provider.icon)) {
        expect((provider.icon.props as { platform?: string }).platform).toBe(provider.id);
      }
    });
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
