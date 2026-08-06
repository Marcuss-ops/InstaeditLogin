import { describe, expect, it } from "vitest";
import { detectChannelLanguage } from "./groupChannelLanguage";

describe("detectChannelLanguage", () => {
  it.each([
    ["BoxeClubITA", "it"],
    ["BoxeClubFr", "fr"],
    ["BoxeClubEs", "es"],
    ["BoxeClubPt", "pt"],
    ["RedGloveTR", "tr"],
    ["RedGloveRU", "ru"],
    ["BoxeClubDE", "de"],
  ])("detects %s as %s", (title, language) => {
    expect(detectChannelLanguage(title)).toEqual({
      language,
      candidates: [language],
      reason: "explicit-marker",
    });
  });

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

  it.each(["Boxing Prime", "Boxing Zone", "Wrestling Discovery"])(
    "reports %s without a reliable marker for manual review",
    (title) => {
      expect(detectChannelLanguage(title)).toEqual({
        language: null,
        candidates: [],
        reason: "insufficient-signal",
      });
    },
  );
});
