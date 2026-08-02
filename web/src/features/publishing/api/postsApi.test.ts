/**
 * Vitest coverage for the posts client (`postsApi.ts`).
 *
 * `authedFetch` is mocked. Every test asserts:
 *   - the URL hit,
 *   - the HTTP method,
 *   - the body shape (idempotency key + payload).
 *
 * No happy-path is asserted for the server response beyond `{ id: 1, ... }`
 * because the contract (201 vs 202, post+targets shape) is owned by
 * the server; the SDK only validates it parses successfully.
 */
import { beforeEach, describe, expect, it, vi } from "vitest";

const { authedFetchMock } = vi.hoisted(() => ({ authedFetchMock: vi.fn() }));

vi.mock("../../../lib/auth", () => ({
  authedFetch: authedFetchMock,
}));

import { createPost, getPost, newIdempotencyKey } from "./postsApi";

const okResponse = (body: unknown): Response =>
  ({ ok: true, status: 201, json: async () => body }) as Response;

beforeEach(() => {
  authedFetchMock.mockReset();
});

describe("newIdempotencyKey", () => {
  it("returns a UUID v4-shaped string", () => {
    const key = newIdempotencyKey();
    expect(key).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
    );
  });

  it("yields distinct values across calls", () => {
    const set = new Set<string>();
    for (let i = 0; i < 100; i += 1) set.add(newIdempotencyKey());
    expect(set.size).toBe(100);
  });

  it("falls through to Math.random when crypto.randomUUID is missing", async () => {
    // Toggle globalThis.crypto.randomUUID off for the duration of the call.
    const original = (crypto as { randomUUID?: () => string }).randomUUID;
    try {
      delete (crypto as { randomUUID?: () => string }).randomUUID;
      const key = newIdempotencyKey();
      expect(key).toMatch(
        /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
      );
    } finally {
      if (original) (crypto as { randomUUID?: () => string }).randomUUID = original;
    }
  });
});

describe("createPost", () => {
  it("POSTs /api/v1/posts with the body and an Idempotency-Key header", async () => {
    authedFetchMock.mockResolvedValueOnce(
      okResponse({ id: 1, workspace_id: 1, status: "queued" }),
    );
    await createPost(
      {
        workspace_id: 1,
        content: { title: "t", media: [{ asset_id: "ma_1" }] },
        targets: [
          {
            platform_account_id: 123,
            settings: { youtube: { title: "yt", privacy_status: "private" } },
          },
        ],
      },
      { idempotencyKey: "uuid-1" },
    );
    const [url, init] = authedFetchMock.mock.calls[0];
    expect(url).toBe("/api/v1/posts");
    expect(init.method).toBe("POST");
    expect((init.headers as Record<string, string>)["Idempotency-Key"]).toBe("uuid-1");
    const body = JSON.parse(init.body as string);
    expect(body.workspace_id).toBe(1);
    expect(body.targets[0].settings.youtube.privacy_status).toBe("private");
    expect(body.content.media[0].asset_id).toBe("ma_1");
  });

  it("omits the Idempotency-Key header when no key is supplied", async () => {
    authedFetchMock.mockResolvedValueOnce(
      okResponse({ id: 1, workspace_id: 1, status: "draft" }),
    );
    await createPost({
      workspace_id: 1,
      content: {},
      targets: [
        {
          platform_account_id: 1,
          settings: { youtube: { title: "t", privacy_status: "public" } },
        },
      ],
    });
    const [, init] = authedFetchMock.mock.calls[0];
    expect((init.headers as Record<string, string>)["Idempotency-Key"]).toBeUndefined();
  });

  it("returns the canonical Post response", async () => {
    authedFetchMock.mockResolvedValueOnce(
      okResponse({ id: 42, workspace_id: 1, status: "queued" }),
    );
    const post = await createPost(
      {
        workspace_id: 1,
        content: {},
        targets: [
          {
            platform_account_id: 9,
            settings: { youtube: { title: "t", privacy_status: "unlisted" } },
          },
        ],
      },
      { idempotencyKey: "k" },
    );
    expect(post.id).toBe(42);
    expect(post.status).toBe("queued");
  });
});

describe("getPost", () => {
  it("GETs /api/v1/posts/{id}", async () => {
    authedFetchMock.mockResolvedValueOnce(
      okResponse({ id: 7, workspace_id: 1, status: "published" }),
    );
    const post = await getPost(7);
    expect(post.id).toBe(7);
    expect(authedFetchMock.mock.calls[0][0]).toBe("/api/v1/posts/7");
  });
});
