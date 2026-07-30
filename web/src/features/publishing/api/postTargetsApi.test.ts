/**
 * Vitest coverage for the post-target client (`postTargetsApi.ts`).
 *
 * Covers all three endpoint shapes:
 *   - GET  /api/v1/post_targets/{id}                  (single, blueprint)
 *   - GET  /api/v1/posts/{id}/targets                 (parent, plural)
 *   - POST /api/v1/post-targets/{id}/retry            (force flag)
 */
import { beforeEach, describe, expect, it, vi } from "vitest";

const { authedFetchMock } = vi.hoisted(() => ({ authedFetchMock: vi.fn() }));

vi.mock("../../../lib/auth", () => ({
  authedFetch: authedFetchMock,
}));

import { getPostTarget, getPostTargets, retryPostTarget } from "./postTargetsApi";

const okResponse = (body: unknown): Response =>
  ({ ok: true, status: 200, json: async () => body }) as Response;

beforeEach(() => {
  authedFetchMock.mockReset();
});

describe("getPostTarget", () => {
  it("GETs /api/v1/post_targets/{id} and returns the parsed detail", async () => {
    authedFetchMock.mockResolvedValueOnce(
      okResponse({
        id: 5,
        post_id: 1,
        platform_account_id: 123,
        status: "publishing",
        privacy_status: "private",
        youtube_sync_status: "pending",
      }),
    );
    const t = await getPostTarget(5);
    expect(t.id).toBe(5);
    expect(t.status).toBe("publishing");
    expect(authedFetchMock.mock.calls[0][0]).toBe("/api/v1/post_targets/5");
  });
});

describe("getPostTargets", () => {
  it("GETs /api/v1/posts/{postId}/targets and unwraps the targets array", async () => {
    authedFetchMock.mockResolvedValueOnce(
      okResponse({
        targets: [
          { id: 1, post_id: 99, platform_account_id: 1, status: "queued" },
          { id: 2, post_id: 99, platform_account_id: 2, status: "publishing" },
        ],
      }),
    );
    const list = await getPostTargets(99);
    expect(list).toHaveLength(2);
    expect(list[0].id).toBe(1);
    expect(list[1].status).toBe("publishing");
    expect(authedFetchMock.mock.calls[0][0]).toBe("/api/v1/posts/99/targets");
  });

  it("returns an empty array when the server replies with no targets", async () => {
    authedFetchMock.mockResolvedValueOnce(okResponse({ targets: [] }));
    expect(await getPostTargets(99)).toEqual([]);
  });

  it("tolerates a missing targets field (defensive)", async () => {
    authedFetchMock.mockResolvedValueOnce(okResponse({}));
    expect(await getPostTargets(99)).toEqual([]);
  });
});

describe("retryPostTarget", () => {
  it("POSTs /api/v1/post_targets/{id}/retry without a query string by default", async () => {
    authedFetchMock.mockResolvedValueOnce(okResponse({ status: "queued" }));
    const out = await retryPostTarget(5);
    expect(out.status).toBe("queued");
    const [url, init] = authedFetchMock.mock.calls[0];
    expect(url).toBe("/api/v1/post_targets/5/retry");
    expect(init.method).toBe("POST");
  });

  it("forwards force=true via query string", async () => {
    authedFetchMock.mockResolvedValueOnce(okResponse({ status: "queued" }));
    await retryPostTarget(5, { force: true });
    expect(authedFetchMock.mock.calls[0][0]).toBe(
      "/api/v1/post_targets/5/retry?force=true",
    );
  });
});
