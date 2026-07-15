/**
 * Demo mode — the SPA's offline fallback for when the Go backend is
 * unreachable (e.g. Fly.io payment is blocked and the API isn't
 * deployed yet). When `VITE_DEMO_MODE === "true"`, `fetchSession` and
 * `authedFetch` short-circuit to return mock data instead of hitting
 * `API_BASE_URL`, so every page renders with seed data and a
 * `<DemoBanner />` at the top of the viewport.
 *
 * Behaviour contract:
 *
 *   • `DEMO_MODE` is evaluated once at module load from
 *     `import.meta.env.VITE_DEMO_MODE === "true"`. Vite inlines
 *     `VITE_*` at build time so a non-true value (incl. "false",
 *     "1", "TRUE", undefined) all yield `false` — pin the
 *     "true" string literal in the docs.
 *   • `MOCK_SESSION` mirrors the shape of `auth.ts::Session` exactly;
 *     the type-only import below keeps the build cycle-free.
 *   • `mockFetch(path, init)` returns synthetic `Response` objects
 *     for every endpoint the SPA reads or writes. Unknown paths
 *     return `{ ok: true }` so the page state machines don't trip
 *     on missing data.
 *   • The build validator in `web/scripts/verify-api-base-url.ts`
 *     is also aware of `VITE_DEMO_MODE` and skips the URL check
 *     so the Vercel deploy doesn't fail on a missing/empty
 *     `VITE_API_BASE_URL`.
 *
 * When Fly is back and the API is reachable, remove
 * `VITE_DEMO_MODE` from Vercel and the SPA returns to the real
 * network path with zero code changes.
 */
import type { Session } from "./auth";

/**
 * Build-time demo-mode gate. Read once at module load; nothing in
 * this file mutates it. In tests `import.meta.env.VITE_DEMO_MODE`
 * is `undefined` → DEMO_MODE is `false` → every call falls through
 * to the real network path, so the 21 existing tests in
 * `web/src/lib/auth.test.ts` and `web/src/pages/*.test.tsx`
 * continue to exercise the production code path.
 */
export const DEMO_MODE: boolean =
  import.meta.env.VITE_DEMO_MODE === "true";

/**
 * The fake signed-in user that every page sees in demo mode. The
 * dashboard's heading reads "Welcome, Demo User"; the account list
 * is empty; the workspace picker shows a single "Demo Workspace".
 */
export const MOCK_SESSION: Session = {
  userId: 1,
  name: "Marco Rossi",
  username: "marco",
  expiresAt: "2099-12-31T23:59:59Z",
};

/**
 * Synthetic Response helper — keeps `mockFetch` readable.
 */
function jsonResponse(body: unknown, status: number = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function emptyResponse(status: number = 204): Response {
  return new Response(null, { status });
}

/**
 * Match-and-replace helper for the dynamic-path routes
 * (e.g. `/api/v1/posts/123/publish`). Returns the captured segments
 * or null on no match.
 */
function matchPath(
  path: string,
  pattern: RegExp,
): RegExpMatchArray | null {
  return path.match(pattern);
}

/**
 * In demo mode, every `authedFetch` call lands here. We return
 * canned `Response` objects so the rest of the app's `await
 * resp.json()` and `resp.ok` / `resp.status` checks behave
 * identically to a real backend.
 *
 * AbortSignal: callers (Dashboard / Connections / Posts) pass
 * `signal: controller.signal` so a re-render or unmount can
 * cancel the request. In real mode the signal aborts the fetch;
 * in demo mode we IGNORE the signal and return the canned
 * response synchronously. That's safe today because the caller's
 * own `if (controller.signal.aborted) return` guard catches the
 * post-await state and discards the result. If you later add a
 * simulated network delay to mockFetch, honour the signal here
 * (throw a DOMException("aborted", "AbortError") when
 * `init.signal?.aborted`).
 *
 * Coverage:
 *   • /api/v1/auth/{me,login,logout,exchange,session}
 *   • /api/v1/accounts               (GET)
 *   • /api/v1/workspaces             (GET/POST/DELETE /:id)
 *   • /api/v1/posts                  (GET/POST)
 *   • /api/v1/posts/:id/{publish,cancel,retry}  (POST)
 *   • /api/v1/posts/:id              (DELETE)
 *   • /api/v1/api-keys               (GET/POST/DELETE /:id)
 *   • /api/v1/api-keys/:id/rotate    (POST)
 *   • /api/v1/webhooks/endpoints     (GET/POST/DELETE /:id)
 *   • /api/v1/media/presign          (POST) — stub returns a
 *                                       fake presigned URL.
 *
 * Anything else returns `{ ok: true }` so unknown endpoints
 * (e.g. a future telemetry call) don't break the page render.
 */
export function mockFetch(path: string, init: RequestInit = {}): Response {
  const method = (init.method ?? "GET").toUpperCase();

  // ─── Auth ────────────────────────────────────────────────────────
  if (path === "/api/v1/auth/me" && method === "GET") {
    return jsonResponse({ user_id: MOCK_SESSION.userId });
  }
  if (path === "/api/v1/auth/session" && method === "GET") {
    return jsonResponse({ csrf_token: "demo-csrf-token" });
  }
  if (path === "/api/v1/auth/login" && method === "POST") {
    // The Login page checks for 200 and redirects; we return
    // 200 with an empty body. The clearSessionCache() in Login
    // (demo branch) sets the in-memory cache to the mock session.
    return jsonResponse({ ok: true });
  }
  if (path === "/api/v1/auth/logout" && method === "POST") {
    return emptyResponse(204);
  }
  if (path === "/api/v1/auth/exchange" && method === "POST") {
    // OAuth one-time-code exchange (used by /auth/callback). In
    // demo mode we never reach this path, but return 204 for
    // completeness.
    return emptyResponse(204);
  }

  // ─── Accounts ────────────────────────────────────────────────────
  if (path === "/api/v1/accounts" && method === "GET") {
    return jsonResponse({
      accounts: [
        {
          id: 1,
          user_id: MOCK_SESSION.userId,
          platform: "instagram",
          platform_user_id: "17841405822304981",
          username: "demo_brand_official",
          created_at: "2026-07-10T14:30:00Z",
          updated_at: "2026-07-14T09:15:00Z",
        },
        {
          id: 2,
          user_id: MOCK_SESSION.userId,
          platform: "facebook",
          platform_user_id: "102345678901234",
          username: "Demo Brand Page",
          created_at: "2026-07-10T14:35:00Z",
          updated_at: "2026-07-12T18:00:00Z",
        },
        {
          id: 3,
          user_id: MOCK_SESSION.userId,
          platform: "tiktok",
          platform_user_id: "UCaDemoBrand123",
          username: "@demobrand",
          created_at: "2026-07-12T10:00:00Z",
          updated_at: "2026-07-14T11:30:00Z",
        },
      ],
    });
  }

  // ─── Workspaces ──────────────────────────────────────────────────
  if (path === "/api/v1/workspaces" && method === "GET") {
    return jsonResponse({
      workspaces: [
        {
          id: 1,
          name: "Brand Official",
          owner_id: MOCK_SESSION.userId,
          created_at: "2026-07-01T00:00:00Z",
        },
        {
          id: 2,
          name: "Personal Projects",
          owner_id: MOCK_SESSION.userId,
          created_at: "2026-07-05T12:00:00Z",
        },
      ],
    });
  }
  if (path === "/api/v1/workspaces" && method === "POST") {
    const body = safeJson<{ name?: string }>(init.body);
    return jsonResponse({
      id: 2,
      name: body?.name ?? "New workspace",
      owner_id: MOCK_SESSION.userId,
      created_at: new Date().toISOString(),
    });
  }
  if (matchPath(path, /^\/api\/v1\/workspaces\/\d+$/) && method === "DELETE") {
    return emptyResponse(204);
  }

  // ─── Posts ───────────────────────────────────────────────────────
  if (path === "/api/v1/posts" && method === "GET") {
    return jsonResponse({
      posts: [
        {
          id: 1,
          workspace_id: 1,
          title: "Summer Collection Launch",
          caption: "Introducing our new summer collection! Fresh styles, bold colors. Link in bio.",
          media_url: "https://images.unsplash.com/photo-1523381210434-271e8be1f52b?w=800",
          scheduled_at: "2026-07-20T10:00:00Z",
          status: "published",
          created_at: "2026-07-14T09:00:00Z",
          updated_at: "2026-07-14T10:05:00Z",
        },
        {
          id: 2,
          workspace_id: 1,
          title: "Behind the Scenes",
          caption: "A peek behind the curtain at our design studio. The magic happens here.",
          media_url: "https://images.unsplash.com/photo-1558618666-fcd25c85f82e?w=800",
          scheduled_at: "2026-07-18T14:30:00Z",
          status: "queued",
          created_at: "2026-07-15T08:20:00Z",
          updated_at: "2026-07-15T08:20:00Z",
        },
        {
          id: 3,
          workspace_id: 1,
          title: "Customer Spotlight",
          caption: "Shoutout to @happy_customer for rocking our new drop! Tag us in your looks.",
          media_url: "https://images.unsplash.com/photo-1529139574466-a303027c1d8b?w=800",
          scheduled_at: null,
          status: "draft",
          created_at: "2026-07-15T06:00:00Z",
          updated_at: "2026-07-15T06:00:00Z",
        },
        {
          id: 4,
          workspace_id: 1,
          title: "Flash Sale Alert",
          caption: "48h FLASH SALE: 30% off everything. Use code FLASH30 at checkout.",
          media_url: "https://images.unsplash.com/photo-1607082349566-187342175e2f?w=800",
          scheduled_at: "2026-07-16T08:00:00Z",
          status: "failed",
          created_at: "2026-07-14T16:00:00Z",
          updated_at: "2026-07-14T16:02:00Z",
        },
        {
          id: 5,
          workspace_id: 1,
          title: "Weekend Vibes",
          caption: "Saturday mood: coffee, code, and creativity. What are you up to this weekend?",
          media_url: "https://images.unsplash.com/photo-1499750310107-5fef28a66643?w=800",
          scheduled_at: "2026-07-19T09:00:00Z",
          status: "queued",
          created_at: "2026-07-15T07:45:00Z",
          updated_at: "2026-07-15T07:45:00Z",
        },
      ],
    });
  }
  if (path === "/api/v1/posts" && method === "POST") {
    // Returns a fake post id; the composer toasts "Queued for
    // publishing. Post #<id>." and navigates to /posts (which
    // is empty in demo mode).
    return jsonResponse({ id: 1 });
  }
  if (
    matchPath(path, /^\/api\/v1\/posts\/\d+\/(publish|cancel|retry)$/) &&
    method === "POST"
  ) {
    return jsonResponse({ ok: true });
  }
  if (matchPath(path, /^\/api\/v1\/posts\/\d+$/) && method === "DELETE") {
    return emptyResponse(204);
  }

  // ─── API keys ────────────────────────────────────────────────────
  if (path === "/api/v1/api-keys" && method === "GET") {
    return jsonResponse({
      keys: [
        {
          id: 1,
          workspace_id: 1,
          created_by: MOCK_SESSION.userId,
          name: "Production Key",
          environment: "live",
          key_prefix: "ie_live",
          permissions: ["read", "write", "publish"],
          expires_at: null,
          revoked_at: null,
          last_used_at: "2026-07-14T22:10:00Z",
          created_at: "2026-07-10T12:00:00Z",
          updated_at: "2026-07-10T12:00:00Z",
        },
        {
          id: 2,
          workspace_id: 1,
          created_by: MOCK_SESSION.userId,
          name: "CI/CD Pipeline",
          environment: "test",
          key_prefix: "ie_test",
          permissions: ["read", "publish", "media"],
          expires_at: "2026-12-31T23:59:59Z",
          revoked_at: null,
          last_used_at: "2026-07-15T03:00:00Z",
          created_at: "2026-07-11T09:00:00Z",
          updated_at: "2026-07-11T09:00:00Z",
        },
      ],
    });
  }
  if (path === "/api/v1/api-keys" && method === "POST") {
    // The Settings page expects { key, plaintext } and shows the
    // plaintext in a modal. We fabricate both so the "Create key"
    // flow is clickable end-to-end in demo mode.
    const body = safeJson<{ name?: string; environment?: string; permissions?: string[] }>(init.body);
    return jsonResponse({
      key: {
        id: 1,
        workspace_id: 1,
        created_by: MOCK_SESSION.userId,
        name: body?.name ?? "Demo key",
        environment: body?.environment ?? "test",
        key_prefix: "demo",
        permissions: body?.permissions ?? ["read"],
        expires_at: null,
        revoked_at: null,
        last_used_at: null,
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
      },
      plaintext: `demo_${Math.random().toString(36).slice(2, 10)}`,
    });
  }
  if (
    matchPath(path, /^\/api\/v1\/api-keys\/\d+$/) &&
    method === "DELETE"
  ) {
    return emptyResponse(204);
  }
  if (matchPath(path, /^\/api\/v1\/api-keys\/\d+\/rotate$/) && method === "POST") {
    return jsonResponse({
      key: {
        id: 1,
        workspace_id: 1,
        created_by: MOCK_SESSION.userId,
        name: "Rotated demo key",
        environment: "test",
        key_prefix: "demo",
        permissions: ["read"],
        expires_at: null,
        revoked_at: null,
        last_used_at: null,
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
      },
      plaintext: `demo_${Math.random().toString(36).slice(2, 10)}`,
    });
  }

  // ─── Webhooks ────────────────────────────────────────────────────
  if (path === "/api/v1/webhooks/endpoints" && method === "GET") {
    return jsonResponse({
      endpoints: [
        {
          id: 1,
          workspace_id: 1,
          url: "https://hooks.slack.com/services/T00/B00/demo",
          events: ["post.published", "post.failed"],
          status: "active",
          created_at: "2026-07-12T15:00:00Z",
        },
        {
          id: 2,
          workspace_id: 1,
          url: "https://api.example.com/webhooks/instaedit",
          events: ["account.connected", "account.disconnected", "post.created"],
          status: "active",
          created_at: "2026-07-13T10:30:00Z",
        },
      ],
    });
  }
  if (path === "/api/v1/webhooks/endpoints" && method === "POST") {
    const body = safeJson<{ url?: string; events?: string[] }>(init.body);
    return jsonResponse({
      id: 1,
      workspace_id: 1,
      url: body?.url ?? "",
      events: body?.events ?? [],
      status: "active",
      created_at: new Date().toISOString(),
    });
  }
  if (
    matchPath(path, /^\/api\/v1\/webhooks\/endpoints\/\d+$/) &&
    method === "DELETE"
  ) {
    return emptyResponse(204);
  }

  // ─── Media presign ───────────────────────────────────────────────
  if (path === "/api/v1/media/presign" && method === "POST") {
    return jsonResponse({
      upload_url: "https://demo.invalid/upload",
      method: "PUT",
      headers: { "Content-Type": "application/octet-stream" },
      public_url: "https://demo.invalid/object",
      fields: {},
      expires_in: 900,
    });
  }

  // ─── Fallback: any unrecognised path returns a benign 200 ────────
  return jsonResponse({ ok: true });
}

/**
 * Tolerantly parse a JSON request body. Returns undefined for
 * missing / non-JSON / non-object payloads so the caller can
 * fall back to defaults without a try/catch on every branch.
 */
function safeJson<T>(body: RequestInit["body"]): T | undefined {
  if (typeof body !== "string") return undefined;
  try {
    return JSON.parse(body) as T;
  } catch {
    return undefined;
  }
}
