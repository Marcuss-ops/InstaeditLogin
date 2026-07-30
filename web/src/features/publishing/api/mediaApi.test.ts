/**
 * Vitest coverage for the 3-step upload client (`mediaApi.ts`).
 *
 * Mocks `authedFetch` (network) AND the global `fetch` (raw PUT to S3).
 * Every test asserts on URL / method / body / headers so future
 * refactors that silently change the wire shape fail fast.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { authedFetchMock, fetchSpy } = vi.hoisted(() => ({
  authedFetchMock: vi.fn(),
  fetchSpy: vi.fn(),
}));

vi.mock("../../../lib/auth", () => {
  class ApiError extends Error {
    status: number;
    constructor(m: string, s: number) {
      super(m);
      this.name = "ApiError";
      this.status = s;
    }
  }
  return {
    authedFetch: authedFetchMock,
    ApiError,
  };
});

import {
  presignMedia,
  uploadToPresignedUrl,
  completeMediaAsset,
  uploadMediaAsset,
} from "./mediaApi";

const okResponse = (body: unknown): Response =>
  ({ ok: true, status: 200, json: async () => body }) as Response;

beforeEach(() => {
  authedFetchMock.mockReset();
  fetchSpy.mockReset();
  // Inject a global fetch stub the module can reach.
  globalThis.fetch = fetchSpy as unknown as typeof fetch;
});

afterEach(() => {
  // Restore to keep cross-test isolation.
  delete (globalThis as { fetch?: unknown }).fetch;
});

describe("presignMedia", () => {
  it("POSTs /api/v1/media/presign and returns the parsed JSON", async () => {
    authedFetchMock.mockResolvedValueOnce(
      okResponse({
        asset_id: "ma_1",
        upload_url: "https://s3.example/x",
        upload_method: "PUT",
        upload_headers: {},
        expires_at: "2026-07-30T12:00:00Z",
        content_type: "video/mp4",
        max_size_bytes: 200_000_000,
      }),
    );
    const grant = await presignMedia({
      filename: "v.mp4",
      content_type: "video/mp4",
      size_bytes: 1234,
    });
    expect(authedFetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = authedFetchMock.mock.calls[0];
    expect(url).toBe("/api/v1/media/presign");
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body as string)).toEqual({
      filename: "v.mp4",
      content_type: "video/mp4",
      size_bytes: 1234,
    });
    expect(grant.asset_id).toBe("ma_1");
  });

  it("forwards the optional sha256 + publish_at fields", async () => {
    authedFetchMock.mockResolvedValueOnce(okResponse({}));
    await presignMedia({
      filename: "v.mp4",
      content_type: "video/mp4",
      size_bytes: 100,
      sha256: "deadbeef",
      publish_at: "2026-08-01T00:00:00Z",
    });
    const [, init] = authedFetchMock.mock.calls[0];
    const body = JSON.parse(init.body as string);
    expect(body.sha256).toBe("deadbeef");
    expect(body.publish_at).toBe("2026-08-01T00:00:00Z");
  });
});

describe("uploadToPresignedUrl", () => {
  it("does a raw PUT with the declared Content-Type", async () => {
    fetchSpy.mockResolvedValueOnce({ ok: true, status: 200 } as Response);
    const blob = new Blob(["payload"], { type: "video/mp4" });
    await uploadToPresignedUrl("https://s3.example/x", blob, "video/mp4");
    expect(fetchSpy).toHaveBeenCalledTimes(1);
    const [url, init] = fetchSpy.mock.calls[0];
    expect(url).toBe("https://s3.example/x");
    expect(init.method).toBe("PUT");
    expect((init.headers as Record<string, string>)["Content-Type"]).toBe("video/mp4");
    expect(init.body).toBe(blob);
  });

  it("throws an ApiError carrying the status on non-2xx", async () => {
    fetchSpy.mockResolvedValueOnce({ ok: false, status: 403 } as Response);
    await expect(
      uploadToPresignedUrl("https://s3", new Blob(), "video/mp4"),
    ).rejects.toMatchObject({ name: "ApiError", status: 403 });
  });
});

describe("completeMediaAsset", () => {
  it("POSTs /api/v1/media/{asset_id}/complete and returns the parsed asset", async () => {
    authedFetchMock.mockResolvedValueOnce(
      okResponse({
        id: "ma_1",
        content_type: "video/mp4",
        size_bytes: 1234,
        sha256: "deadbeef",
        status: "ready",
        expires_at: "2026-07-30T12:00:00Z",
      }),
    );
    const asset = await completeMediaAsset("ma_1");
    expect(authedFetchMock.mock.calls[0][0]).toBe("/api/v1/media/ma_1/complete");
    expect(asset.status).toBe("ready");
  });

  it("URL-encodes the asset id (defensive against future UUID slugs)", async () => {
    authedFetchMock.mockResolvedValueOnce(okResponse({ id: "ma/abc", status: "ready" }));
    await completeMediaAsset("ma/abc");
    expect(authedFetchMock.mock.calls[0][0]).toBe("/api/v1/media/ma%2Fabc/complete");
  });
});

describe("uploadMediaAsset (high-level)", () => {
  it("runs presign → PUT → complete end-to-end and returns the ready asset", async () => {
    authedFetchMock.mockResolvedValueOnce(
      okResponse({
        asset_id: "ma_1",
        upload_url: "https://s3.example/x",
        upload_method: "PUT",
        upload_headers: {},
        expires_at: "2026-07-30T12:00:00Z",
        content_type: "video/mp4",
        max_size_bytes: 100,
      }),
    );
    fetchSpy.mockResolvedValueOnce({ ok: true, status: 200 } as Response);
    authedFetchMock.mockResolvedValueOnce(
      okResponse({
        id: "ma_1",
        content_type: "video/mp4",
        size_bytes: 1,
        sha256: null,
        status: "ready",
        expires_at: "2026-07-30T12:00:00Z",
      }),
    );
    const file = new File(["x"], "v.mp4", { type: "video/mp4" });
    const asset = await uploadMediaAsset(file);
    expect(asset.status).toBe("ready");
    expect(authedFetchMock.mock.calls.map(([url]) => url)).toEqual([
      "/api/v1/media/presign",
      "/api/v1/media/ma_1/complete",
    ]);
    expect(fetchSpy).toHaveBeenCalledTimes(1);
  });

  it("emits progress events in presign → upload → complete order", async () => {
    authedFetchMock.mockResolvedValueOnce(
      okResponse({
        asset_id: "ma_1",
        upload_url: "https://s3.example/x",
        upload_method: "PUT",
        upload_headers: {},
        expires_at: "2026-07-30T12:00:00Z",
        content_type: "video/mp4",
        max_size_bytes: 100,
      }),
    );
    fetchSpy.mockResolvedValueOnce({ ok: true, status: 200 } as Response);
    authedFetchMock.mockResolvedValueOnce(
      okResponse({
        id: "ma_1",
        content_type: "video/mp4",
        size_bytes: 1,
        sha256: null,
        status: "ready",
        expires_at: "2026-07-30T12:00:00Z",
      }),
    );
    const onProgress = vi.fn();
    const file = new File(["x"], "v.mp4", { type: "video/mp4" });
    await uploadMediaAsset(file, {}, onProgress);
    expect(onProgress.mock.calls.map(([p]) => p.phase)).toEqual([
      "presign",
      "upload",
      "complete",
    ]);
  });

  it("falls back to video/mp4 when the File has no detected MIME", async () => {
    authedFetchMock.mockResolvedValueOnce(
      okResponse({
        asset_id: "ma_1",
        upload_url: "https://s3.example/x",
        upload_method: "PUT",
        upload_headers: {},
        expires_at: "2026-07-30T12:00:00Z",
        content_type: "video/mp4",
        max_size_bytes: 100,
      }),
    );
    fetchSpy.mockResolvedValueOnce({ ok: true, status: 200 } as Response);
    authedFetchMock.mockResolvedValueOnce(okResponse({ id: "ma_1", status: "ready" }));
    const file = new File(["x"], "v.mp4");
    await uploadMediaAsset(file);
    const [, init] = authedFetchMock.mock.calls[0];
    expect(JSON.parse(init.body as string).content_type).toBe("video/mp4");
  });
});
