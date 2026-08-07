import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { installChunkLoadRecovery } from "./chunkLoadRecovery";

describe("installChunkLoadRecovery", () => {
  const reloadMock = vi.fn();

  beforeEach(() => {
    sessionStorage.clear();
    reloadMock.mockReset();
    vi.useFakeTimers();
    // jsdom's location is configurable; replace it so we can observe reload.
    Object.defineProperty(window, "location", {
      configurable: true,
      value: { reload: reloadMock },
    });
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("reloads exactly once when a dynamically imported module fails to load", () => {
    installChunkLoadRecovery();

    window.dispatchEvent(
      new ErrorEvent("error", {
        message:
          "Failed to fetch dynamically imported module: https://app.instaedit.org/assets/Groups-abc123.js",
      }),
    );
    vi.advanceTimersByTime(300);
    expect(reloadMock).toHaveBeenCalledTimes(1);

    // A second failure in the same tab session must not loop forever.
    window.dispatchEvent(
      new ErrorEvent("error", {
        message:
          "Failed to fetch dynamically imported module: https://app.instaedit.org/assets/Groups-def456.js",
      }),
    );
    vi.advanceTimersByTime(300);
    expect(reloadMock).toHaveBeenCalledTimes(1);
  });

  it("reloads once for an unhandledrejection carrying a module-load error", () => {
    installChunkLoadRecovery();

    const reason = new Error(
      "error loading dynamically imported module: https://app.instaedit.org/assets/Calendar-xyz.js",
    );
    window.dispatchEvent(
      new PromiseRejectionEvent("unhandledrejection", {
        promise: Promise.resolve(),
        reason,
      }),
    );
    vi.advanceTimersByTime(300);
    expect(reloadMock).toHaveBeenCalledTimes(1);
  });

  it("ignores unrelated errors", () => {
    installChunkLoadRecovery();

    window.dispatchEvent(new ErrorEvent("error", { message: "TypeError: x is not a function" }));
    window.dispatchEvent(
      new PromiseRejectionEvent("unhandledrejection", {
        promise: Promise.resolve(),
        reason: new Error("network 500"),
      }),
    );
    vi.advanceTimersByTime(300);
    expect(reloadMock).not.toHaveBeenCalled();
  });
});
