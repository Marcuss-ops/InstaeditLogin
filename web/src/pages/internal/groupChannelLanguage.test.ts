import { describe, expect, it } from "vitest";
import { detectChannelLanguage } from "./groupChannelLanguage";

describe("detectChannelLanguage", () => {
  it("detects a unique explicit language marker", () => {
    expect(detectChannelLanguage("WWE Italia ufficiale")).toEqual({
      language: "it",
      candidates: ["it"],
      reason: "explicit-marker",
    });
  });

  it("does not choose between conflicting language markers", () => {
    expect(detectChannelLanguage("Italia English Wrestling")).toEqual({
      language: null,
      candidates: ["it", "en"],
      reason: "ambiguous-markers",
    });
  });

  it("reports titles without a reliable marker for manual review", () => {
    expect(detectChannelLanguage("Wrestling Discovery")).toEqual({
      language: null,
      candidates: [],
      reason: "insufficient-signal",
    });
  });
});
