/**
 * Unit tests for the helpers exported by web/src/lib/auth.ts.
 *
 * Covers two surfaces:
 *  1. `isVercelAppHost` — pure predicate, exercised directly across
 *     positive matches, negative matches, look-alike domains, port/path
 *     independence, and invalid-URL safety.
 *  2. `probeBackend`  — integration via mocked fetch + mocked
 *     `../supabase`. The mock forces API_BASE_URL to a `*.vercel.app`
 *     host so the new `vercel_stale_deploy` branch is reachable from a
 *     test. The non-vercel paths are not retested here because they go
 *     through the existing implicit coverage (default API_BASE_URL is
 *     `http://localhost:8080`).
 *
 * Why the URL literal is inlined inside the vi.mock factory (instead of
 * being captured from a module-scoped const): vi.mock factories are
 * evaluated when the mocked module is first imported, so any closure
 * dependency on outer-scope variables races the hoisting of the
 * `import` statements. Literal strings inside the factory have no such
 * dependency, so the mock is guaranteed to take effect. MOCK_VERCEL_URL
 * below mirrors the literal so the test assertions stay readable.
 */
import { afterEach, describe, expect, it, vi } from "vitest";

// Force API_BASE_URL to MOCK_VERCEL_URL via vi.mock so probeBackend's
// vercel_stale_deploy branch is reachable from the integration tests
// below. The literal is inlined to avoid any closure-over-outer-var
// race during hoisted evaluation; the value matches MOCK_VERCEL_URL
// declared right after.
//
// IMPORTANT: the mock path is "./supabase" (NOT "../supabase") because
// auth.test.ts lives next to auth.ts in /web/src/lib/, so its relative
// `./supabase` resolves to the SAME module that auth.ts imports via
// `./supabase`. Using `../supabase` would resolve to /web/src/supabase
// (or fall through to an unrelated module), leaving the real
// API_BASE_URL untouched and silently making the test exercise the
// generic not_found branch instead of the vercel branch under test.
vi.mock("./supabase", () => ({
  API_BASE_URL: "https://instaedit-login-abc123.vercel.app",
}));

// Used by the assertions under "probeBackend with vercel.app base URL".
// Must be the same URL literal the vi.mock factory returns above \u2014
// duplicated for clarity (we don't want test code to depend on the
// factory's internals).
const MOCK_VERCEL_URL = "https://instaedit-login-abc123.vercel.app";

import { isVercelAppHost, probeBackend } from "./auth";

describe("isVercelAppHost", () => {
  describe("positive matches", () => {
    it("matches the bare vercel.app apex", () => {
      expect(isVercelAppHost("https://vercel.app")).toBe(true);
      expect(isVercelAppHost("https://vercel.app/")).toBe(true);
      expect(isVercelAppHost("https://vercel.app/api/v1/health")).toBe(true);
    });

    it("matches a *.vercel.app deployment subdomain", () => {
      expect(isVercelAppHost("https://instaedit-login-abc123.vercel.app")).toBe(true);
      expect(isVercelAppHost("https://my-app-def456.vercel.app/")).toBe(true);
      expect(isVercelAppHost("https://a.b.vercel.app")).toBe(true);
    });

    it("is case-insensitive on the hostname", () => {
      expect(isVercelAppHost("https://My-App.vercel.app")).toBe(true);
      expect(isVercelAppHost("https://VERCEL.APP")).toBe(true);
      expect(isVercelAppHost("https://My-App.VERCEL.app")).toBe(true);
    });

    it("ignores scheme, port, path when matching", () => {
      expect(isVercelAppHost("https://example.vercel.app:8080/api/v1/health")).toBe(true);
      expect(isVercelAppHost("http://example.vercel.app:443")).toBe(true);
      expect(isVercelAppHost("ftp://files.vercel.app/path")).toBe(true);
    });
  });

  describe("negative matches", () => {
    it("rejects unrelated hosts", () => {
      expect(isVercelAppHost("https://api.example.com")).toBe(false);
      expect(isVercelAppHost("http://localhost:8080")).toBe(false);
      expect(isVercelAppHost("https://api.instaedit.org")).toBe(false);
      expect(isVercelAppHost("https://api.railway.app")).toBe(false);
      expect(isVercelAppHost("https://api.render.com")).toBe(false);
    });

    it("rejects strings that contain 'vercel' but aren't a real subdomain", () => {
      // 'vercel' embedded inside a different domain name.
      expect(isVercelAppHost("https://my-vercel.app.example.com")).toBe(false);
      expect(isVercelAppHost("https://vercelapp.example.com")).toBe(false);
      // 'vercel' as a path/query component, not a hostname.
      expect(isVercelAppHost("https://api.example.com/?ref=vercel")).toBe(false);
    });

    it("rejects domains where *.vercel.app appears as a middle label, not the public suffix", () => {
      // Adversarial: a hostname that LOOKS like a vercel.app subdomain but
      // has a different public suffix (TLD+1). These must NOT trigger
      // vercel_stale_deploy — otherwise a malicious or typo'd URL could
      // spoof the probe into recommending a Vercel fix.
      expect(isVercelAppHost("https://example.vercel.app.evil.com")).toBe(false);
      expect(isVercelAppHost("https://myapp.vercel.app.attacker.com")).toBe(false);
      expect(isVercelAppHost("https://a.vercel.app.attacker.io")).toBe(false);
    });

    it("rejects look-alike domains", () => {
      // Domains that look like vercel.app but aren't.
      expect(isVercelAppHost("https://notvercel.app.example.com")).toBe(false);
      expect(isVercelAppHost("https://example.com.vercel.attacker.com")).toBe(false);
    });
  });

  describe("invalid-url safety", () => {
    it("returns false (no throw) for unparseable URLs", () => {
      expect(isVercelAppHost("not a url")).toBe(false);
      expect(isVercelAppHost("")).toBe(false);
      expect(isVercelAppHost("://no-scheme")).toBe(false);
      expect(isVercelAppHost("http:/missing-slash")).toBe(false);
    });
  });
});

/**
 * Integration coverage for probeBackend's vercel_stale_deploy branch.
 *
 * The mock at the top of this file forces API_BASE_URL to
 * `*.vercel.app`, so a fetch 404 from /api/v1/health reliably classifies
 * as vercel_stale_deploy instead of generic not_found. We also assert
 * that a 200 short-circuit returns `ok: true` (sanity \u2014
 * vercel_stale_deploy should never fire on healthy responses, even from
 * a vercel.app host) and that a 5xx falls into `http_error` (the
 * vercel_stale_deploy branch is strictly 404-specific).
 */
describe("probeBackend with vercel.app base URL", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("returns vercel_stale_deploy on 404 from a vercel.app host", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        status: 404,
        headers: new Headers(),
      }),
    );

    const result = await probeBackend();
    expect(result.ok).toBe(false);
    if (!result.ok) {
      expect(result.reason).toBe("vercel_stale_deploy");
      expect(result.status).toBe(404);
      expect(result.url).toBe(MOCK_VERCEL_URL);
      // Diagnostic must be in English (codebase convention) and
      // explicitly call out VITE_API_BASE_URL + redeploy so the operator
      // sees the actionable next step.
      expect(result.message).toContain("VITE_API_BASE_URL");
      expect(result.message.toLowerCase()).toContain("redeploy");
    }
  });

  it("returns ok:true on 200 (vercel_stale_deploy is 404-specific)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        headers: new Headers(),
      }),
    );

    const result = await probeBackend();
    expect(result.ok).toBe(true);
    if (result.ok) {
      expect(result.status).toBe(200);
      expect(result.url).toBe(MOCK_VERCEL_URL);
    }
  });

  it("returns http_error on 5xx (vercel_stale_deploy is 404-specific)", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        status: 500,
        headers: new Headers(),
      }),
    );

    const result = await probeBackend();
    expect(result.ok).toBe(false);
    if (!result.ok) {
      // 5xx stays in the generic http_error bucket; vercel_stale_deploy
      // is strictly a 404 classification.
      expect(result.reason).toBe("http_error");
      expect(result.status).toBe(500);
    }
  });
});
