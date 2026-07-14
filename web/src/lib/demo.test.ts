/**
 * Direct unit tests for `mockFetch` — the demo-mode response
 * router. These tests cover the path/method shape of every endpoint
 * the SPA reads or writes, without depending on `import.meta.env`
 * stubbing (which is flaky across vitest versions). The
 * auth.ts integration (`fetchSession` / `authedFetch` short-circuit
 * to `mockFetch` when `DEMO_MODE=true`) is exercised implicitly by
 * the 21 existing tests in `auth.test.ts` — they run with
 * `DEMO_MODE=false` so the real fetch path is hit. The runtime
 * branches in `Login.tsx` / `Connections.tsx` are similarly pinned
 * by their own test files.
 */
import { describe, it, expect } from "vitest";
import { mockFetch } from "./demo";

/**
 * Generic-typed wrapper around `Response.json()`. The `T` parameter
 * lets future tests assert against a concrete shape without
 * re-casting at every call site:
 *
 *   const body = await readJson<{ accounts: Account[] }>(resp);
 *   expect(body.accounts).toEqual([]);
 *
 * Default `Record<string, unknown>` keeps the existing loose-typed
 * call sites (e.g. `body.csrf_token`) working unchanged.
 */
async function readJson<T = Record<string, unknown>>(resp: Response): Promise<T> {
  return (await resp.json()) as T;
}

describe("mockFetch — /api/v1/auth/*", () => {
  it("/auth/me returns user_id", async () => {
    const resp = mockFetch("/api/v1/auth/me", { method: "GET" });
    expect(resp.status).toBe(200);
    const body = await readJson<{ user_id: number }>(resp);
    expect(body.user_id).toBe(1);
  });

  it("/auth/session returns a csrf_token", async () => {
    const resp = mockFetch("/api/v1/auth/session", { method: "GET" });
    expect(resp.status).toBe(200);
    const body = await readJson(resp);
    expect(typeof body.csrf_token).toBe("string");
  });

  it("/auth/login (POST) returns 200", () => {
    const resp = mockFetch("/api/v1/auth/login", {
      method: "POST",
      body: "{}",
    });
    expect(resp.status).toBe(200);
  });

  it("/auth/logout (POST) returns 204", () => {
    const resp = mockFetch("/api/v1/auth/logout", { method: "POST" });
    expect(resp.status).toBe(204);
  });

  it("/auth/exchange (POST) returns 204", () => {
    const resp = mockFetch("/api/v1/auth/exchange", {
      method: "POST",
      body: '{"code":"abc"}',
    });
    expect(resp.status).toBe(204);
  });
});

describe("mockFetch — /api/v1/accounts + workspaces", () => {
  it("/accounts returns an empty accounts list", async () => {
    const resp = mockFetch("/api/v1/accounts", { method: "GET" });
    const body = await readJson(resp);
    expect(body.accounts).toEqual([]);
  });

  it("/workspaces (GET) returns the demo workspace", async () => {
    const resp = mockFetch("/api/v1/workspaces", { method: "GET" });
    const body = await readJson<{ workspaces: Array<{ id: number; name: string }> }>(resp);
    expect(body.workspaces).toHaveLength(1);
    expect(body.workspaces[0].name).toBe("Demo Workspace");
  });

  it("/workspaces (POST) returns the created workspace", async () => {
    const resp = mockFetch("/api/v1/workspaces", {
      method: "POST",
      body: JSON.stringify({ name: "My new team" }),
    });
    const body = await readJson(resp);
    expect(body.name).toBe("My new team");
    expect(typeof body.id).toBe("number");
  });

  it("/workspaces/:id (DELETE) returns 204", () => {
    const resp = mockFetch("/api/v1/workspaces/42", { method: "DELETE" });
    expect(resp.status).toBe(204);
  });
});

describe("mockFetch — /api/v1/posts", () => {
  it("/posts (GET) returns an empty list", async () => {
    const resp = mockFetch("/api/v1/posts", { method: "GET" });
    const body = await readJson(resp);
    expect(body.posts).toEqual([]);
  });

  it("/posts (POST) returns a numeric id", async () => {
    const resp = mockFetch("/api/v1/posts", {
      method: "POST",
      body: JSON.stringify({ workspace_id: 1, content: {} }),
    });
    const body = await readJson(resp);
    expect(typeof body.id).toBe("number");
  });

  it("/posts/:id/publish (POST) returns ok", async () => {
    const resp = mockFetch("/api/v1/posts/7/publish", { method: "POST" });
    expect(resp.status).toBe(200);
  });

  it("/posts/:id/cancel (POST) returns ok", async () => {
    const resp = mockFetch("/api/v1/posts/7/cancel", { method: "POST" });
    expect(resp.status).toBe(200);
  });

  it("/posts/:id/retry (POST) returns ok", async () => {
    const resp = mockFetch("/api/v1/posts/7/retry", { method: "POST" });
    expect(resp.status).toBe(200);
  });

  it("/posts/:id (DELETE) returns 204", () => {
    const resp = mockFetch("/api/v1/posts/7", { method: "DELETE" });
    expect(resp.status).toBe(204);
  });
});

describe("mockFetch — /api/v1/api-keys", () => {
  it("/api-keys (GET) returns empty list", async () => {
    const resp = mockFetch("/api/v1/api-keys", { method: "GET" });
    const body = await readJson(resp);
    expect(body.keys).toEqual([]);
  });

  it("/api-keys (POST) returns key + plaintext", async () => {
    const resp = mockFetch("/api/v1/api-keys", {
      method: "POST",
      body: JSON.stringify({ name: "ci", environment: "test", permissions: ["read"] }),
    });
    const body = await readJson<{ key: { name: string }; plaintext: string }>(resp);
    expect(body.key.name).toBe("ci");
    expect(typeof body.plaintext).toBe("string");
  });

  it("/api-keys/:id (DELETE) returns 204", () => {
    const resp = mockFetch("/api/v1/api-keys/3", { method: "DELETE" });
    expect(resp.status).toBe(204);
  });

  it("/api-keys/:id/rotate (POST) returns key + plaintext", async () => {
    const resp = mockFetch("/api/v1/api-keys/3/rotate", { method: "POST" });
    const body = await readJson(resp);
    expect(typeof body.plaintext).toBe("string");
  });
});

describe("mockFetch — /api/v1/webhooks/endpoints", () => {
  it("(GET) returns empty list", async () => {
    const resp = mockFetch("/api/v1/webhooks/endpoints", { method: "GET" });
    const body = await readJson(resp);
    expect(body.endpoints).toEqual([]);
  });

  it("(POST) echoes url + events", async () => {
    const resp = mockFetch("/api/v1/webhooks/endpoints", {
      method: "POST",
      body: JSON.stringify({ url: "https://example.com/hook", events: ["post.published"], secret: "s" }),
    });
    const body = await readJson<{ url: string; status: string }>(resp);
    expect(body.url).toBe("https://example.com/hook");
    expect(body.status).toBe("active");
  });

  it("/:id (DELETE) returns 204", () => {
    const resp = mockFetch("/api/v1/webhooks/endpoints/9", { method: "DELETE" });
    expect(resp.status).toBe(204);
  });
});

describe("mockFetch — /api/v1/media/presign", () => {
  it("(POST) returns a presigned upload_url + public_url", async () => {
    const resp = mockFetch("/api/v1/media/presign", {
      method: "POST",
      body: JSON.stringify({ content_type: "image/png" }),
    });
    expect(resp.status).toBe(200);
    const body = await readJson(resp);
    expect(typeof body.upload_url).toBe("string");
    expect(typeof body.public_url).toBe("string");
    expect(body.method).toBe("PUT");
  });
});

describe("mockFetch — unknown paths are benign 200s", () => {
  it("returns ok for a future telemetry endpoint", async () => {
    const resp = mockFetch("/api/v1/telemetry/heartbeat", { method: "POST" });
    expect(resp.status).toBe(200);
    const body = await readJson(resp);
    expect(body.ok).toBe(true);
  });
});
