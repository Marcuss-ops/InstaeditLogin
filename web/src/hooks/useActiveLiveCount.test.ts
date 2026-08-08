import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { clearSessionCache } from "../lib/auth";
import { clearSharedQueryCache } from "../lib/queryRegistry";
import { countActiveLives, livestreamsURL, useActiveLiveCount } from "./useActiveLiveCount";

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

describe("livestreamsURL", () => {
  it("includes the required workspace scope", () => {
    expect(livestreamsURL(7)).toMatch(/\/api\/v1\/livestreams\?workspace_id=7$/);
  });
});

describe("useActiveLiveCount", () => {
  afterEach(() => {
    vi.useRealTimers();
    clearSharedQueryCache();
    clearSessionCache();
    vi.unstubAllGlobals();
  });

  it("loads the active workspace before the scoped livestream list", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({ ok: true, json: async () => ({ workspace_id: 7 }) })
      .mockResolvedValueOnce({ ok: true, json: async () => ({ items: [{ actual_state: "live" }] }) });
    vi.stubGlobal("fetch", fetchMock);

    const { result, unmount } = renderHook(() => useActiveLiveCount());
    await waitFor(() => expect(result.current).toBe(1));

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      expect.stringMatching(/\/api\/v1\/auth\/me$/),
      expect.objectContaining({ credentials: "include" }),
    );
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      expect.stringMatching(/\/api\/v1\/livestreams\?workspace_id=7$/),
      expect.objectContaining({ credentials: "include" }),
    );
    unmount();
  });

  it("hides the badge and caches an unauthenticated session after a 401", async () => {
    vi.useFakeTimers();
    const fetchMock = vi.fn().mockResolvedValue({
      ok: false,
      status: 401,
      json: async () => ({ error: "unauthorized" }),
    });
    vi.stubGlobal("fetch", fetchMock);

    const { result, unmount } = renderHook(() => useActiveLiveCount());
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
      await Promise.resolve();
    });
    // /auth/me 401 → one session-refresh attempt → 401 again → cached as
    // unauthenticated. The refresh attempt is the intended new behaviour
    // (an expired access JWT is healed transparently); it also 401s here
    // because no refresh cookie exists, so the badge stays hidden.
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(result.current).toBeNull();

    // The shared query schedules a later poll, but fetchSession has cached
    // the failed session lookup; advancing beyond that poll must not hit
    // /auth/me (or /auth/refresh) again.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_001);
    });
    expect(fetchMock).toHaveBeenCalledTimes(2);
    unmount();
  });
});
