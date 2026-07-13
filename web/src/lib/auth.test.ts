/**
 * Unit tests for auth module (Taglio 5a).
 *
 * Covers: fetchSession, clearSessionCache, authedFetch, logout, AuthError, ApiError.
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthError, ApiError, authedFetch, clearSessionCache, fetchSession, readCookie } from "./auth";

describe("AuthError", () => {
  it("is an Error subclass with name AuthError", () => {
    const err = new AuthError();
    expect(err).toBeInstanceOf(Error);
    expect(err.name).toBe("AuthError");
  });
});

describe("ApiError", () => {
  it("captures status and message", () => {
    const err = new ApiError(500, "boom");
    expect(err.status).toBe(500);
    expect(err.message).toBe("boom");
  });
});

describe("clearSessionCache", () => {
  it("resets the module-level cache", async () => {
    clearSessionCache();
    // After clearing, fetchSession should re-probe.
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true, status: 200, json: async () => ({ user_id: 1 }),
    }));
    const session = await fetchSession();
    expect(session?.userId).toBe(1);
    vi.unstubAllGlobals();
  });
});

describe("authedFetch", () => {
  afterEach(() => { vi.unstubAllGlobals(); });

  it("throws AuthError on 401", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: false, status: 401, json: async () => ({}),
    }));
    await expect(authedFetch("/api/v1/accounts")).rejects.toThrow(AuthError);
  });

  it("throws ApiError on non-401 errors", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: false, status: 500, json: async () => ({ error: "boom" }),
    }));
    await expect(authedFetch("/api/v1/accounts")).rejects.toThrow(ApiError);
  });
});

// Helper: replaces document.cookie with a fixed string for the test
// and restores the previous descriptor in afterEach. JSDOM accumulates
// cookies set via `document.cookie = "..."`, so we go via the
// underlying property descriptor to get a fully deterministic view
// of what `document.cookie` returns.
function setDocumentCookie(value: string) {
  Object.defineProperty(document, "cookie", {
    value,
    writable: true,
    configurable: true,
  });
}

function restoreDocumentCookie(original: PropertyDescriptor | undefined) {
  if (original) {
    Object.defineProperty(document, "cookie", original);
  } else {
    Object.defineProperty(document, "cookie", { value: "", writable: true, configurable: true });
  }
}

describe("readCookie", () => {
  const originalCookieDescriptor = Object.getOwnPropertyDescriptor(document, "cookie");

  afterEach(() => {
    restoreDocumentCookie(originalCookieDescriptor);
  });

  it("returns the value of a single cookie", () => {
    setDocumentCookie("csrf_token=deadbeef");
    expect(readCookie("csrf_token")).toBe("deadbeef");
  });

  it("returns the value among multiple cookies", () => {
    setDocumentCookie("foo=bar; csrf_token=live; baz=qux");
    expect(readCookie("csrf_token")).toBe("live");
  });

  it("decodes URL-encoded values", () => {
    setDocumentCookie("csrf_token=hello%20world");
    expect(readCookie("csrf_token")).toBe("hello world");
  });

  it("returns null when the cookie is missing", () => {
    setDocumentCookie("other=foo");
    expect(readCookie("csrf_token")).toBeNull();
  });

  it("returns null when document.cookie is empty", () => {
    setDocumentCookie("");
    expect(readCookie("csrf_token")).toBeNull();
  });

  it("trims leading whitespace between cookies", () => {
    // Browsers can return "csrf_token=abc; session=xyz" with no
    // leading space, but defensive code should also handle a leading
    // space. Pin the behaviour.
    setDocumentCookie(" csrf_token=trim-me");
    expect(readCookie("csrf_token")).toBe("trim-me");
  });
});

describe("authedFetch — X-CSRF-Token injection (Blocco #2.4)", () => {
  const originalCookieDescriptor = Object.getOwnPropertyDescriptor(document, "cookie");

  afterEach(() => {
    vi.unstubAllGlobals();
    restoreDocumentCookie(originalCookieDescriptor);
  });

  // Captures the init object passed to the next fetch() call so each
  // test can assert against the headers/auth/credentials/method that
  // authedFetch actually wired up.
  function captureFetch(): Promise<RequestInit> {
    return new Promise((resolve) => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockImplementation((_url: string, init: RequestInit) => {
          resolve(init);
          return Promise.resolve({
            ok: true,
            status: 200,
            json: async () => ({}),
          });
        }),
      );
    });
  }

  it.each(["POST", "PUT", "PATCH", "DELETE"] as const)(
    "injects X-CSRF-Token on %s when csrf_token cookie is set",
    async (method) => {
      setDocumentCookie("csrf_token=the-token-value");
      const initPromise = captureFetch();
      await authedFetch("/api/v1/posts", { method, body: "{}" });
      const init = await initPromise;
      const headers = init.headers as Headers;
      expect(headers.get("X-CSRF-Token")).toBe("the-token-value");
    },
  );

  it("does NOT inject X-CSRF-Token on GET", async () => {
    setDocumentCookie("csrf_token=get-cookie");
    const initPromise = captureFetch();
    await authedFetch("/api/v1/posts");
    const init = await initPromise;
    const headers = init.headers as Headers;
    expect(headers.get("X-CSRF-Token")).toBeNull();
  });

  it("skips X-CSRF-Token when no csrf_token cookie is present", async () => {
    setDocumentCookie("other=foo");
    const initPromise = captureFetch();
    await authedFetch("/api/v1/posts", { method: "POST", body: "{}" });
    const init = await initPromise;
    const headers = init.headers as Headers;
    expect(headers.get("X-CSRF-Token")).toBeNull();
  });

  it("does not override a caller-provided X-CSRF-Token header", async () => {
    // Explicit caller value wins — useful for tests that need to
    // simulate a stale / wrong token, and a safe default that
    // matches the `init.headers` precedence everywhere else in
    // authedFetch (caller's headers are not overwritten by Content-Type
    // either).
    setDocumentCookie("csrf_token=cookie-value");
    const initPromise = captureFetch();
    await authedFetch("/api/v1/posts", {
      method: "POST",
      body: "{}",
      headers: { "X-CSRF-Token": "explicit-value" },
    });
    const init = await initPromise;
    const headers = init.headers as Headers;
    expect(headers.get("X-CSRF-Token")).toBe("explicit-value");
  });

  it("uppercases the method before matching (post → POST)", async () => {
    // HTTP method is case-sensitive on the wire but client code
    // sometimes uses lowercase (e.g. axios defaults). Pin the
    // contract: the unsafe-methods check normalises case.
    setDocumentCookie("csrf_token=abc");
    const initPromise = captureFetch();
    await authedFetch("/api/v1/posts", { method: "post", body: "{}" });
    const init = await initPromise;
    const headers = init.headers as Headers;
    expect(headers.get("X-CSRF-Token")).toBe("abc");
  });

  it("still attaches credentials: include on every request", async () => {
    setDocumentCookie("csrf_token=ok");
    const initPromise = captureFetch();
    await authedFetch("/api/v1/posts", { method: "POST", body: "{}" });
    const init = await initPromise;
    expect(init.credentials).toBe("include");
  });
});
