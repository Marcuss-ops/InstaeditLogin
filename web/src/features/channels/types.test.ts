/**
 * Smoke tests for the channel-feature type helpers.
 *
 * The helpers in `features/channels/types.ts` are now a public
 * surface (consumed by ChannelHeader, ChannelVideoFilters,
 * ChannelVideoCard, and any future channel-page entrypoint). This
 * file locks down the contract:
 *
 *   • Case-normalization on the OAuth-connection status (`active` /
 *     `connected` regardless of casing)
 *   • Privacy normalization into the chip taxonomy
 *   • The `all` filter maps to "no ?privacy= param" rather than an
 *     empty string (caller can pass it to URLSearchParams without an
 *     extra check)
 *   • Views extraction tolerates missing or sparse metrics rows
 *   • Fallback URL escape for unusually-shaped identifiers
 */
import { describe, expect, it } from "vitest";
import {
  buildPrivacyParam,
  buildYouTubeFallbackUrl,
  getStatusTone,
  getViewsDisplay,
  normalizePrivacy,
} from "./types";

describe("getStatusTone", () => {
  it("returns emerald for lowercase 'active'", () => {
    expect(getStatusTone("active").bg).toContain("emerald-500");
    expect(getStatusTone("active").text).toContain("emerald-400");
  });

  it("returns emerald for lowercase 'connected'", () => {
    expect(getStatusTone("connected").bg).toContain("emerald-500");
  });

  it("returns emerald for uppercase variants (casing-normalized)", () => {
    expect(getStatusTone("ACTIVE").bg).toContain("emerald-500");
    expect(getStatusTone("Connected").bg).toContain("emerald-500");
  });

  it("returns amber for unknown statuses", () => {
    expect(getStatusTone("pending_reauth").bg).toContain("amber-500");
    expect(getStatusTone("revoked").bg).toContain("amber-500");
    expect(getStatusTone("failed").bg).toContain("amber-500");
  });

  it("returns amber for undefined / empty input", () => {
    expect(getStatusTone(undefined).bg).toContain("amber-500");
    expect(getStatusTone(null).bg).toContain("amber-500");
    expect(getStatusTone("").bg).toContain("amber-500");
  });

  it("always returns the { bg, border, text } triple", () => {
    const tone = getStatusTone("active");
    expect(Object.keys(tone).sort()).toEqual(["bg", "border", "text"]);
  });
});

describe("normalizePrivacy", () => {
  it('returns "private" / "unlisted" / "public" canonically', () => {
    expect(normalizePrivacy("private")).toBe("private");
    expect(normalizePrivacy("unlisted")).toBe("unlisted");
    expect(normalizePrivacy("public")).toBe("public");
  });

  it('returns "unknown" for legacy / unrecognized values', () => {
    expect(normalizePrivacy("legacy_draft")).toBe("unknown");
    expect(normalizePrivacy("PRIVATE")).toBe("unknown"); // casing NOT normalized here
    expect(normalizePrivacy(undefined)).toBe("unknown");
    expect(normalizePrivacy(null)).toBe("unknown");
  });
});

describe("buildPrivacyParam", () => {
  it("returns undefined for 'all' (caller omits the param)", () => {
    expect(buildPrivacyParam("all")).toBeUndefined();
  });

  it("returns the canonical server values for the rest", () => {
    expect(buildPrivacyParam("private")).toBe("private");
    expect(buildPrivacyParam("unlisted")).toBe("unlisted");
    expect(buildPrivacyParam("public")).toBe("public");
  });
});

describe("getViewsDisplay", () => {
  it("pulls display_value out of a metrics row keyed 'views'", () => {
    expect(
      getViewsDisplay([
        { key: "likes", label: "Likes", value: 5, display_value: "5" },
        { key: "views", label: "Views", value: 1234, display_value: "1.2K" },
      ]),
    ).toBe("1.2K");
  });

  it("returns undefined when no 'views' row is present", () => {
    expect(getViewsDisplay([])).toBeUndefined();
    expect(
      getViewsDisplay([
        { key: "likes", label: "Likes", value: 5, display_value: "5" },
      ]),
    ).toBeUndefined();
  });

  it("returns undefined when metrics is omitted entirely", () => {
    expect(getViewsDisplay(undefined)).toBeUndefined();
  });
});

describe("buildYouTubeFallbackUrl", () => {
  it("builds https://youtu.be/{id}", () => {
    expect(buildYouTubeFallbackUrl("yt_demo_001")).toBe(
      "https://youtu.be/yt_demo_001",
    );
  });

  it("URI-encodes unfriendly characters", () => {
    expect(buildYouTubeFallbackUrl("a/b c")).toBe(
      "https://youtu.be/a%2Fb%20c",
    );
  });
});
