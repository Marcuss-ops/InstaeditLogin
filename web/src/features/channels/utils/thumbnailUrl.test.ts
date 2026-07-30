/**
 * Tests for withThumbnailCacheBust — the channel-feature thumbnail
 * cache-buster helper.
 *
 * Covers:
 *  • undefined / empty-url passthrough (no throw)
 *  • no-bustKey passthrough (helper must NOT generate its own timestamp)
 *  • non-YouTube-CDN URL — must NEVER be modified (signature safety)
 *  • i.ytimg.com with no query string → "?v=N"
 *  • i.ytimg.com with existing query string → "&v=N"
 *  • img.youtube.com alternate host → also busted
 *  • special characters in bustKey — encodedURIComponent applies
 */
import { describe, expect, it } from "vitest";
import { withThumbnailCacheBust } from "./thumbnailUrl";

describe("withThumbnailCacheBust", () => {
  it("returns undefined for undefined URL", () => {
    expect(withThumbnailCacheBust(undefined, 1)).toBeUndefined();
    expect(withThumbnailCacheBust(null, 1)).toBeUndefined();
  });

  it("returns undefined for empty string URL", () => {
    expect(withThumbnailCacheBust("", 1)).toBeUndefined();
  });

  it("returns the URL unchanged when bustKey is null/undefined", () => {
    const url = "https://i.ytimg.com/vi/abc/maxresdefault.jpg";
    expect(withThumbnailCacheBust(url, undefined)).toBe(url);
    expect(withThumbnailCacheBust(url, null)).toBe(url);
  });

  it("does NOT modify non-YouTube-CDN URLs (signed-URL safety contract)", () => {
    const s3 =
      "https://signed-bucket.s3.amazonaws.com/uuid?X-Amz-Signature=abc&X-Amz-Date=def";
    expect(withThumbnailCacheBust(s3, 1234567890)).toBe(s3);
    const cloudfront =
      "https://d111111abcdef8.cloudfront.net/thumbs/abc.jpg?Policy=xyz&Signature=abc";
    expect(withThumbnailCacheBust(cloudfront, 1)).toBe(cloudfront);
  });

  it("appends ?v=N for i.ytimg.com URLs without a query string", () => {
    expect(
      withThumbnailCacheBust(
        "https://i.ytimg.com/vi/yt_AAA/maxresdefault.jpg",
        1234567890,
      ),
    ).toBe("https://i.ytimg.com/vi/yt_AAA/maxresdefault.jpg?v=1234567890");
  });

  it("appends &v=N for i.ytimg.com URLs with existing query string", () => {
    const url =
      "https://i.ytimg.com/vi/yt_AAA/maxresdefault.jpg?sqp=abc&sqm=def";
    expect(withThumbnailCacheBust(url, 42)).toBe(
      "https://i.ytimg.com/vi/yt_AAA/maxresdefault.jpg?sqp=abc&sqm=def&v=42",
    );
  });

  it("also busts img.youtube.com alternative host", () => {
    expect(
      withThumbnailCacheBust("https://img.youtube.com/abc.jpg", 7),
    ).toBe("https://img.youtube.com/abc.jpg?v=7");
  });

  it("encodes special characters in the bust key", () => {
    expect(
      withThumbnailCacheBust(
        "https://i.ytimg.com/vi/abc/maxresdefault.jpg",
        "v 1?a",
      ),
    ).toBe("https://i.ytimg.com/vi/abc/maxresdefault.jpg?v=v%201%3Fa");
  });

  it("accepts a number OR string bust key (caller freedom)", () => {
    const url = "https://i.ytimg.com/vi/abc/maxresdefault.jpg";
    expect(withThumbnailCacheBust(url, 42)).toBe(`${url}?v=42`);
    expect(withThumbnailCacheBust(url, "rev-7")).toBe(`${url}?v=rev-7`);
  });
});
