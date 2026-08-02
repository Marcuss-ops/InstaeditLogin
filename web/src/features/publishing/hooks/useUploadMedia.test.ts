/**
 * Vitest coverage of useUploadMedia.
 *
 * Mocks `../api/mediaApi` via `vi.hoisted` (so the factory runs
 * before module-level `let`). The `crypto.subtle.digest` mock
 * is installed/restored around each test to keep React state and
 * the deterministic SHA output isolated.
 *
 * What is locked down:
 *   - starts in `idle`
 *   - happy path: hashing → uploading → done with the asset
 *     returned by the mediaApi orchestrator
 *   - SHA-256 hex is computed locally AND forwarded to mediaApi
 *     so the server's Task 6/10 enforcement at /complete passes
 *   - non-video files are rejected BEFORE mediaApi is called
 *     (the user's SHA compute is wasted otherwise)
 *   - ApiError → kind='error' with the server message preserved
 *   - AuthError is RE-THROWN (not caught into state) so the
 *     caller can navigate to /login
 *   - generic Error / TypeError → kind='error' with err.message
 *   - reset() aborts any in-flight upload AND clears state
 *   - start() aborts any prior in-flight upload before starting
 *     a new one (no two uploads in parallel)
 */
import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, AuthError } from "../../../lib/auth";

const { uploadMediaAssetMock } = vi.hoisted(() => ({
  uploadMediaAssetMock: vi.fn(),
}));

vi.mock("../api/mediaApi", () => ({
  uploadMediaAsset: uploadMediaAssetMock,
  uploadToPresignedUrl: vi.fn(),
  presignMedia: vi.fn(),
  completeMediaAsset: vi.fn(),
}));

import { useUploadMedia } from "./useUploadMedia";
import type { MediaAsset } from "../api/mediaApi";
import type { UploadMediaAssetOptions } from "../api/mediaApi";

const READY_ASSET: MediaAsset = {
  id: "ma_test_ready",
  status: "ready",
  size_bytes: 100,
  content_type: "video/mp4",
  sha256: null,
  expires_at: "2030-01-01T00:00:00Z",
};

function mkFile(name: string, size: number, type = "video/mp4"): File {
  return new File([new Uint8Array(size)], name, { type });
}

describe("useUploadMedia", () => {
  let digestSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    uploadMediaAssetMock.mockReset();
    // Deterministic SHA: 32 bytes of zeros → 64 hex zeros.
    // vitest hoists vi.spyOn via the test file scope, so this
    // restores naturally via vi.restoreAllMocks in afterEach.
    digestSpy = vi
      .spyOn(crypto.subtle, "digest")
      .mockResolvedValue(new ArrayBuffer(32));
  });

  afterEach(() => {
    digestSpy.mockRestore();
  });

  it("starts in idle", () => {
    const { result } = renderHook(() => useUploadMedia());
    expect(result.current.state).toEqual({ kind: "idle" });
  });

  it("happy path ends in done with the asset returned by mediaApi", async () => {
    uploadMediaAssetMock.mockImplementation(
      async (_f, _o, onProgress) => {
        onProgress?.({ phase: "presign" });
        onProgress?.({ phase: "upload", loaded: 0, total: 1 });
        onProgress?.({ phase: "complete", loaded: 1, total: 1 });
        return READY_ASSET;
      },
    );

    const { result } = renderHook(() => useUploadMedia());
    await act(async () => {
      await result.current.start(mkFile("v.mp4", 100));
    });

    expect(result.current.state).toEqual({
      kind: "done",
      asset: READY_ASSET,
    });
    expect(uploadMediaAssetMock).toHaveBeenCalledTimes(1);
  });

  it("computes SHA-256 locally and forwards it to mediaApi (64-hex digest)", async () => {
    uploadMediaAssetMock.mockResolvedValue(READY_ASSET);

    const { result } = renderHook(() => useUploadMedia());
    await act(async () => {
      await result.current.start(mkFile("v.mp4", 100));
    });

    expect(digestSpy).toHaveBeenCalledTimes(1);
    const [algorithm, data] = digestSpy.mock.calls[0]!;
    expect(algorithm).toBe("SHA-256");
    expect(data).toBeInstanceOf(ArrayBuffer);

    // Second call = the mediaApi.forward:
    const mediaCall = uploadMediaAssetMock.mock.calls[0];
    expect(mediaCall).toBeDefined();
    const opts = mediaCall?.[1] as UploadMediaAssetOptions | undefined;
    expect(opts?.sha256).toMatch(/^[0-9a-f]{64}$/);
  });

  it("rejects non-video files without calling mediaApi", async () => {
    const { result } = renderHook(() => useUploadMedia());
    await act(async () => {
      await result.current.start(mkFile("photo.png", 100, "image/png"));
    });

    expect(result.current.state.kind).toBe("error");
    if (result.current.state.kind === "error") {
      expect(result.current.state.message).toMatch(/video files/i);
    }
    expect(uploadMediaAssetMock).not.toHaveBeenCalled();
    expect(digestSpy).not.toHaveBeenCalled();
  });

  it("ApiError from mediaApi → kind=error with server message preserved", async () => {
    uploadMediaAssetMock.mockRejectedValue(
      new ApiError(410, "Upload window expired"),
    );

    const { result } = renderHook(() => useUploadMedia());
    await act(async () => {
      await result.current.start(mkFile("v.mp4", 100));
    });

    expect(result.current.state).toEqual({
      kind: "error",
      message: "Upload window expired",
    });
  });

  it("AuthError from mediaApi IS RE-THROWN so caller can navigate to /login", async () => {
    uploadMediaAssetMock.mockRejectedValue(new AuthError());

    const { result } = renderHook(() => useUploadMedia());
    await expect(
      act(async () => {
        await result.current.start(mkFile("v.mp4", 100));
      }),
    ).rejects.toBeInstanceOf(AuthError);
    // State must not flip to done; the unhandled AuthError is the
    // signal to the caller's router-level ProtectedRoute.
    expect(result.current.state.kind).not.toBe("done");
  });

  it("network / generic Error → kind=error with err.message preserved", async () => {
    uploadMediaAssetMock.mockRejectedValue(new TypeError("Failed to fetch"));

    const { result } = renderHook(() => useUploadMedia());
    await act(async () => {
      await result.current.start(mkFile("v.mp4", 100));
    });

    expect(result.current.state).toEqual({
      kind: "error",
      message: "Failed to fetch",
    });
  });

  it("reset() aborts a pending upload and returns state to idle", async () => {
    let rejectFirst: (err: unknown) => void = () => {};
    uploadMediaAssetMock.mockImplementationOnce(
      async (_f, _o, onProgress) => {
        onProgress?.({ phase: "presign" });
        await new Promise<unknown>((_resolve, reject) => {
          rejectFirst = reject;
        });
      },
    );

    const { result } = renderHook(() => useUploadMedia());
    // Kick off the upload without awaiting — we want it in flight.
    void act(() => {
      void result.current.start(mkFile("v.mp4", 100));
    });

    // Flush microtasks so SHA mock + initial setState + first progress callback settle.
    await act(async () => {
      await Promise.resolve();
    });
    expect(result.current.state.kind).toBe("uploading");

    // Trigger reset (which aborts AND clears state).
    act(() => {
      result.current.reset();
      // Simulate the AbortController firing the rejection into mediaApi.
      rejectFirst(new DOMException("aborted", "AbortError"));
    });

    // After the DOMException lands inside the catch, the hook sets kind='idle'.
    await act(async () => {
      await Promise.resolve();
    });
    expect(result.current.state.kind).toBe("idle");
  });

  it("start() aborts any prior in-flight upload before launching a new one", async () => {
    uploadMediaAssetMock
      .mockImplementationOnce(
        async (_f, _o, onProgress) => {
          onProgress?.({ phase: "presign" });
          await new Promise<void>(() => {});
        },
      )
      .mockImplementationOnce(
        async (_f, _o, onProgress) => {
          onProgress?.({ phase: "presign" });
          onProgress?.({ phase: "upload", loaded: 0, total: 1 });
          onProgress?.({ phase: "complete", loaded: 1, total: 1 });
          return READY_ASSET;
        },
      );

    const { result } = renderHook(() => useUploadMedia());

    // First upload — gets stuck on `presign` (never resolves).
    void act(() => {
      void result.current.start(mkFile("first.mp4", 100));
    });
    await act(async () => {
      await Promise.resolve();
    });
    expect(result.current.state.kind).toBe("uploading");

    // Second upload. This MUST abort the first one and replace it.
    await act(async () => {
      await result.current.start(mkFile("second.mp4", 200));
    });

    expect(result.current.state).toEqual({
      kind: "done",
      asset: READY_ASSET,
    });
    expect(uploadMediaAssetMock).toHaveBeenCalledTimes(2);

    // The rejected-first promise never lands in the hook's state
    // because start() swapped the AbortController before the second
    // submit even ran. (If it had not aborted, the first would
    // still hold a stale `inFlightRef` racing with the second.)
  });
});
