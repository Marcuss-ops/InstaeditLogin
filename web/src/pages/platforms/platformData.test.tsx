import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import { getProviderRegistryEntry } from "../../components/brand/PlatformLogos";
import { getPlatformData } from "./platformData";

const PUBLIC_PLATFORM_SLUGS = [
  "instagram",
  "facebook",
  "tiktok",
  "threads",
  "linkedin",
  "twitter",
  "youtube",
] as const;

describe("public platform data branding", () => {
  it.each(PUBLIC_PLATFORM_SLUGS)(
    "derives %s accent and logo from the canonical provider registry",
    (slug) => {
      const platform = getPlatformData(slug);
      const provider = getProviderRegistryEntry(slug);

      expect(platform).not.toBeNull();
      expect(provider).not.toBeUndefined();
      expect(platform?.color).toBe(provider?.accentColor);
      expect(platform?.name).toBe(provider?.name);
      expect(platform?.slug).toBe(slug);

      const { container } = render(<>{platform?.icon}</>);
      expect(container.querySelector("svg")).toBeInTheDocument();
    },
  );

  it("normalizes the public x alias to the Twitter content page", () => {
    const xPage = getPlatformData("x");
    const twitterPage = getPlatformData("twitter");

    expect(xPage?.slug).toBe("twitter");
    expect(xPage?.name).toBe(twitterPage?.name);
    expect(xPage?.color).toBe(twitterPage?.color);
  });
});
