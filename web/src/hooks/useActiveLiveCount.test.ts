import { describe, expect, it } from "vitest";
import { countActiveLives } from "./useActiveLiveCount";

describe("countActiveLives", () => {
  it("counts only rows whose actual_state is 'live'", () => {
    const payload = {
      items: [
        { actual_state: "live" },
        { actual_state: "live" },
        { actual_state: "scheduled" },
        { actual_state: "reconnecting" },
        { actual_state: "completed" },
        { actual_state: "testing" },
      ],
    };
    expect(countActiveLives(payload)).toBe(2);
  });

  it("accepts a bare array", () => {
    expect(countActiveLives([{ actual_state: "live" }, { actual_state: "draft" }])).toBe(1);
  });

  it("treats scheduled streams as not live", () => {
    expect(countActiveLives({ items: [{ actual_state: "scheduled" }] })).toBe(0);
  });

  it("returns 0 for empty or malformed payloads", () => {
    expect(countActiveLives(undefined)).toBe(0);
    expect(countActiveLives(null)).toBe(0);
    expect(countActiveLives({})).toBe(0);
    expect(countActiveLives({ items: "nope" })).toBe(0);
  });
});
