import { describe, expect, it } from "vitest";
import { GROUP_ACCENT_COLORS, groupAccent } from "./groupAccent";

describe("groupAccent", () => {
  it("is deterministic for the same group name", () => {
    expect(groupAccent("Boxe")).toEqual(groupAccent("Boxe"));
    expect(groupAccent("Wwe")).toEqual(groupAccent("Wwe"));
  });

  it("returns a palette color with a matching translucent background", () => {
    const accent = groupAccent("Boxe");
    expect(GROUP_ACCENT_COLORS).toContain(accent.text);
    expect(accent.bg).toBe(`${accent.text}1f`);
  });

  it("assigns several distinct colors across the real folder names", () => {
    const names = ["Boxe", "Comedy", "Crime", "Discovery", "HipHop", "Rap", "Wwe"];
    const colors = new Set(names.map((name) => groupAccent(name).text));
    expect(colors.size).toBeGreaterThan(1);
  });
});
