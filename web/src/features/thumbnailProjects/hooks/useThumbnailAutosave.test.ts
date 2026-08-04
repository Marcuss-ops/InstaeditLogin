/**
 * Vitest coverage for useThumbnailAutosave.
 *
 * Certifies the honest-save contract:
 *   • a freshly loaded snapshot is NEVER re-saved (baseline adoption);
 *   • a change → "dirty" → debounce → PUT → "saved" only after the ack;
 *   • unchanged snapshots are never persisted;
 *   • a failed PUT → "error" (last snapshot NOT acked), retry works;
 *   • a 409 PROJECT_VERSION_CONFLICT pauses autosave and exposes the
 *     conflict; reset() un-pauses after a server reload;
 *   • flush() saves immediately.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { ApiError } from "../../../lib/auth";
import { useThumbnailAutosave } from "./useThumbnailAutosave";
import type {
  ThumbnailCanvasSnapshot,
  ThumbnailProjectSnapshotResult,
} from "../types";

const { saveSnapshotMock } = vi.hoisted(() => ({
  saveSnapshotMock: vi.fn(),
}));

vi.mock("../api/thumbnailProjectsApi", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/thumbnailProjectsApi")>();
  return { ...actual, saveThumbnailSnapshot: saveSnapshotMock };
});

const SNAP_A: ThumbnailCanvasSnapshot = {
  canvas: { width: 1920, height: 1080, background: "#30305a" },
  objects: [],
};
const SNAP_B: ThumbnailCanvasSnapshot = {
  canvas: { width: 1920, height: 1080, background: "#30305a" },
  objects: [{ id: "text-1", type: "text", text: "CIAO", x: 10, y: 20 }],
};

const RESULT: ThumbnailProjectSnapshotResult = {
  project_id: "thumbproj_1",
  revision_id: "thumbrev_2",
  revision_number: 2,
  version: 2,
  saved_at: "2026-08-04T10:00:00Z",
  snapshot_sha256: "aabbccdd",
};

function setup(initial: ThumbnailCanvasSnapshot = SNAP_A) {
  return renderHook(
    ({ snapshot, version, enabled }: { snapshot: ThumbnailCanvasSnapshot; version: number; enabled: boolean }) =>
      useThumbnailAutosave({
        workspaceId: 7,
        projectId: "thumbproj_1",
        snapshot,
        version,
        enabled,
        onSaved: () => {},
      }),
    { initialProps: { snapshot: initial, version: 1, enabled: true } },
  );
}

beforeEach(() => {
  vi.useFakeTimers();
  saveSnapshotMock.mockReset();
  saveSnapshotMock.mockResolvedValue(RESULT);
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("useThumbnailAutosave", () => {
  it("never saves the freshly loaded snapshot (baseline adoption)", async () => {
    const { result } = setup();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(saveSnapshotMock).not.toHaveBeenCalled();
    expect(result.current.status).toBe("idle");
  });

  it("goes dirty → debounce → saving → saved with the correct payload", async () => {
    const { result, rerender } = setup();
    rerender({ snapshot: SNAP_B, version: 1, enabled: true });
    // Before the debounce elapses: dirty.
    expect(result.current.status).toBe("dirty");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1499);
    });
    expect(result.current.status).toBe("dirty");

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(saveSnapshotMock).toHaveBeenCalledTimes(1);
    const [wsId, projectId, body] = saveSnapshotMock.mock.calls[0] as [
      number,
      string,
      { schema_version: number; snapshot: ThumbnailCanvasSnapshot; renderer_version: string; base_version: number },
    ];
    expect(wsId).toBe(7);
    expect(projectId).toBe("thumbproj_1");
    expect(body.schema_version).toBe(1);
    expect(body.base_version).toBe(1);
    expect(body.renderer_version).toBe("go-canvas-v1");
    expect(body.snapshot).toEqual(SNAP_B);

    expect(result.current.status).toBe("saved");
    expect(result.current.lastSavedAt).toBeInstanceOf(Date);
    expect(result.current.lastHash).toBe("aabbccdd");
  });

  it("persists a change made while a save is in flight (no lost edits)", async () => {
    let resolveFirst: ((r: ThumbnailProjectSnapshotResult) => void) | null = null;
    saveSnapshotMock.mockImplementation(
      () =>
        new Promise<ThumbnailProjectSnapshotResult>((resolve) => {
          resolveFirst = resolve;
        }),
    );
    const { result, rerender } = setup();
    rerender({ snapshot: SNAP_B, version: 1, enabled: true });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1500);
    });
    expect(saveSnapshotMock).toHaveBeenCalledTimes(1);

    // Edit while the PUT is still in flight.
    const SNAP_C: ThumbnailCanvasSnapshot = {
      ...SNAP_B,
      objects: [{ id: "text-1", type: "text", text: "CIAO!", x: 50, y: 20 }],
    };
    rerender({ snapshot: SNAP_C, version: 1, enabled: true });
    await act(async () => {
      resolveFirst?.(RESULT);
      await Promise.resolve();
    });
    // The finally block re-schedules a save for the unpersisted SNAP_C.
    expect(saveSnapshotMock).toHaveBeenCalledTimes(1); // second is debounced
    expect(result.current.status).toBe("dirty");
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1500);
    });
    expect(saveSnapshotMock).toHaveBeenCalledTimes(2);
    const [, , secondBody] = saveSnapshotMock.mock.calls[1] as [
      number,
      string,
      { snapshot: ThumbnailCanvasSnapshot },
    ];
    expect(secondBody.snapshot).toEqual(SNAP_C);
  });

  it("does not save when the snapshot is unchanged after a save", async () => {
    const { rerender } = setup();
    rerender({ snapshot: SNAP_B, version: 1, enabled: true });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1500);
    });
    expect(saveSnapshotMock).toHaveBeenCalledTimes(1);
    // Rerender with the SAME snapshot (e.g. parent state churn).
    rerender({ snapshot: SNAP_B, version: 2, enabled: true });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(saveSnapshotMock).toHaveBeenCalledTimes(1);
  });

  it("surfaces error state when the PUT fails and retry re-attempts", async () => {
    saveSnapshotMock.mockRejectedValueOnce(new Error("network down"));
    const { result, rerender } = setup();
    rerender({ snapshot: SNAP_B, version: 1, enabled: true });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1500);
    });
    expect(result.current.status).toBe("error");
    expect(result.current.error).toContain("network down");
    // retry after the network recovers
    saveSnapshotMock.mockResolvedValue(RESULT);
    act(() => result.current.retry());
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(saveSnapshotMock).toHaveBeenCalledTimes(2);
    expect(result.current.status).toBe("saved");
  });

  it("pauses on 409 PROJECT_VERSION_CONFLICT and resumes after reset()", async () => {
    saveSnapshotMock.mockRejectedValueOnce(
      new ApiError(409, "conflict", {
        code: "PROJECT_VERSION_CONFLICT",
        current_version: 9,
      }),
    );
    const { result, rerender } = setup();
    rerender({ snapshot: SNAP_B, version: 1, enabled: true });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1500);
    });
    expect(result.current.status).toBe("error");
    expect(result.current.conflict).toEqual({
      code: "PROJECT_VERSION_CONFLICT",
      current_version: 9,
    });

    // Further edits must NOT trigger saves while paused.
    rerender({ snapshot: SNAP_B, version: 9, enabled: false });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(saveSnapshotMock).toHaveBeenCalledTimes(1);

    // Editor reloaded server truth → reset un-pauses and re-baselines.
    act(() => result.current.reset(SNAP_B, 9));
    rerender({ snapshot: SNAP_B, version: 9, enabled: true });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });
    expect(result.current.conflict).toBeNull();
    expect(saveSnapshotMock).toHaveBeenCalledTimes(1); // baseline == B, nothing new
  });

  it("sends If-Match with the current version on every save (no silent last-write-wins)", async () => {
    const { rerender } = setup();
    rerender({ snapshot: SNAP_B, version: 1, enabled: true });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1500);
    });
    const [, , , opts] = saveSnapshotMock.mock.calls[0] as [
      number,
      string,
      unknown,
      { ifMatchVersion?: number },
    ];
    expect(opts.ifMatchVersion).toBe(1);

    // After the server acked version 2, the next save must carry it.
    rerender({ snapshot: { ...SNAP_B, objects: [{ id: "t", type: "text", text: "X" }] }, version: 2, enabled: true });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1500);
    });
    const [, , , opts2] = saveSnapshotMock.mock.calls[1] as [
      number,
      string,
      unknown,
      { ifMatchVersion?: number },
    ];
    expect(opts2.ifMatchVersion).toBe(2);
  });

  it("flush() cancels the debounce and saves immediately", async () => {
    const { result, rerender } = setup();
    rerender({ snapshot: SNAP_B, version: 1, enabled: true });
    let ok = false;
    await act(async () => {
      ok = await result.current.flush();
    });
    expect(ok).toBe(true);
    expect(saveSnapshotMock).toHaveBeenCalledTimes(1);
  });

  it("flush() resolves true when the latest snapshot is already persisted", async () => {
    const { result } = setup();
    let ok = false;
    await act(async () => {
      ok = await result.current.flush();
    });
    expect(ok).toBe(true);
    expect(saveSnapshotMock).not.toHaveBeenCalled();
  });

  it("flush() AWAITS a save in flight and persists edits made during it", async () => {
    let resolveFirst: ((r: ThumbnailProjectSnapshotResult) => void) | null = null;
    let callCount = 0;
    saveSnapshotMock.mockImplementation(() => {
      callCount += 1;
      // Only the FIRST save (SNAP_B) is held in flight; later calls
      // resolve immediately so the flush can complete its loop.
      if (callCount === 1) {
        return new Promise<ThumbnailProjectSnapshotResult>((resolve) => {
          resolveFirst = resolve;
        });
      }
      return Promise.resolve({ ...RESULT, snapshot_sha256: `hash${callCount}` });
    });
    const { result, rerender } = setup();
    // Trigger the debounced save for SNAP_B and let it start (in flight).
    rerender({ snapshot: SNAP_B, version: 1, enabled: true });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1500);
    });
    expect(saveSnapshotMock).toHaveBeenCalledTimes(1);

    // An edit lands WHILE the save is still in flight.
    const SNAP_C: ThumbnailCanvasSnapshot = {
      ...SNAP_B,
      objects: [{ id: "text-1", type: "text", text: "BBBB", x: 90, y: 90 }],
    };
    rerender({ snapshot: SNAP_C, version: 1, enabled: true });

    // flush() must JOIN the in-flight save, then persist SNAP_C before
    // resolving — never resolve while the server still holds a stale
    // revision (DoD: preview/export always derives from the latest edit).
    let flushResolved = false;
    let flushOk: boolean | null = null;
    void result.current.flush().then((ok) => {
      flushOk = ok;
      flushResolved = true;
    });
    await act(async () => {
      await Promise.resolve();
    });
    expect(flushResolved).toBe(false); // still waiting on the in-flight save

    await act(async () => {
      resolveFirst?.(RESULT); // the in-flight save (SNAP_B) completes
      await Promise.resolve();
      await Promise.resolve();
    });
    // SNAP_C must now be persisted immediately (flush, not debounce).
    expect(saveSnapshotMock).toHaveBeenCalledTimes(2);
    const [, , secondBody] = saveSnapshotMock.mock.calls[1] as [
      number,
      string,
      { snapshot: ThumbnailCanvasSnapshot },
    ];
    expect(secondBody.snapshot).toEqual(SNAP_C);
    expect(flushResolved).toBe(true);
    expect(flushOk).toBe(true);
    expect(result.current.status).toBe("saved");
  });

  it("flush() resolves false when the in-flight save fails", async () => {
    let rejectFirst: ((e: Error) => void) | null = null;
    saveSnapshotMock.mockImplementation(
      () =>
        new Promise<ThumbnailProjectSnapshotResult>((_resolve, reject) => {
          rejectFirst = reject;
        }),
    );
    const { result, rerender } = setup();
    rerender({ snapshot: SNAP_B, version: 1, enabled: true });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1500);
    });
    expect(saveSnapshotMock).toHaveBeenCalledTimes(1);

    let flushOk: boolean | null = null;
    void result.current.flush().then((ok) => {
      flushOk = ok;
    });
    await act(async () => {
      await Promise.resolve();
    });
    await act(async () => {
      rejectFirst?.(new Error("network down"));
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(flushOk).toBe(false);
    expect(result.current.status).toBe("error");
  });
});
