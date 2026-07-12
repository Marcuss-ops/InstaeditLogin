import { describe, it, expect } from "vitest";
import { validateApiBaseUrl } from "./verify-api-base-url";

/**
 * Helper to construct a synthetic env object for the validator. We avoid
 * mutating process.env in the tests so other suites that read VITE_*
 * are not affected. The `undefined` entries simulate "not set".
 */
function env(
  overrides: Record<string, string | undefined>,
): Record<string, string | undefined> {
  return { ...overrides };
}

describe("validateApiBaseUrl", () => {
  describe("local builds (VERCEL_ENV unset)", () => {
    it("ok on http://localhost:8080 (the canonical dev URL)", () => {
      const r = validateApiBaseUrl(env({ VITE_API_BASE_URL: "http://localhost:8080" }));
      expect(r.level).toBe("ok");
      expect(r.context).toBe("local");
    });

    it("ok on a real https URL (dev previews against staging)", () => {
      const r = validateApiBaseUrl(env({ VITE_API_BASE_URL: "https://api.example.com" }));
      expect(r.level).toBe("ok");
    });

    it("warn (not error) on empty URL — dev might bootstrap without it", () => {
      const r = validateApiBaseUrl(env({ VITE_API_BASE_URL: "" }));
      expect(r.level).toBe("warn");
      expect(r.context).toBe("local");
      expect(r.messages[0]).toMatch(/empty/);
    });

    it("warn on malformed URL", () => {
      const r = validateApiBaseUrl(env({ VITE_API_BASE_URL: "not a url" }));
      expect(r.level).toBe("warn");
    });

    it("ok on 127.0.0.1 (loopback, same idea as localhost)", () => {
      const r = validateApiBaseUrl(env({ VITE_API_BASE_URL: "http://127.0.0.1:9000" }));
      expect(r.level).toBe("ok");
    });
  });

  describe("vercel dev (VERCEL_ENV=development)", () => {
    it("ok on localhost (vercel dev runs on the same machine)", () => {
      const r = validateApiBaseUrl(
        env({ VITE_API_BASE_URL: "http://localhost:8080", VERCEL_ENV: "development" }),
      );
      expect(r.level).toBe("ok");
      expect(r.context).toBe("vercel_dev");
    });
  });

  describe("preview builds (VERCEL_ENV=preview)", () => {
    it("error on empty URL", () => {
      const r = validateApiBaseUrl(
        env({ VITE_API_BASE_URL: "", VERCEL_ENV: "preview" }),
      );
      expect(r.level).toBe("error");
      expect(r.messages[0]).toMatch(/empty/);
    });

    it("error on localhost", () => {
      const r = validateApiBaseUrl(
        env({ VITE_API_BASE_URL: "http://localhost:8080", VERCEL_ENV: "preview" }),
      );
      expect(r.level).toBe("error");
      expect(r.messages.some((m) => m.includes("localhost"))).toBe(true);
    });

    it("error on 127.0.0.1", () => {
      const r = validateApiBaseUrl(
        env({ VITE_API_BASE_URL: "http://127.0.0.1:9000", VERCEL_ENV: "preview" }),
      );
      expect(r.level).toBe("error");
    });

    it("error on http://", () => {
      const r = validateApiBaseUrl(
        env({ VITE_API_BASE_URL: "http://api.example.com", VERCEL_ENV: "preview" }),
      );
      expect(r.level).toBe("error");
      expect(r.messages.some((m) => m.includes("https"))).toBe(true);
    });

    it("ok on https URL", () => {
      const r = validateApiBaseUrl(
        env({ VITE_API_BASE_URL: "https://api.example.com", VERCEL_ENV: "preview" }),
      );
      expect(r.level).toBe("ok");
    });

    it("error on malformed URL in preview (caught early)", () => {
      const r = validateApiBaseUrl(
        env({ VITE_API_BASE_URL: "ht tp:// broken", VERCEL_ENV: "preview" }),
      );
      expect(r.level).toBe("error");
    });
  });

  describe("production builds (VERCEL_ENV=production)", () => {
    it("error on empty URL with DEPLOYMENT_NOT_FOUND hint", () => {
      const r = validateApiBaseUrl(
        env({ VITE_API_BASE_URL: "", VERCEL_ENV: "production" }),
      );
      expect(r.level).toBe("error");
      expect(r.messages[0]).toMatch(/DEPLOYMENT_NOT_FOUND|DEPLOYMENT/);
    });

    it("error on localhost with port", () => {
      const r = validateApiBaseUrl(
        env({ VITE_API_BASE_URL: "http://localhost:8080", VERCEL_ENV: "production" }),
      );
      expect(r.level).toBe("error");
    });

    it("error on 0.0.0.0", () => {
      const r = validateApiBaseUrl(
        env({ VITE_API_BASE_URL: "http://0.0.0.0:8080", VERCEL_ENV: "production" }),
      );
      expect(r.level).toBe("error");
    });

    it("error on http://", () => {
      const r = validateApiBaseUrl(
        env({ VITE_API_BASE_URL: "http://api.example.com", VERCEL_ENV: "production" }),
      );
      expect(r.level).toBe("error");
    });

    it("error on malformed URL", () => {
      const r = validateApiBaseUrl(
        env({ VITE_API_BASE_URL: "ht tp:// broken", VERCEL_ENV: "production" }),
      );
      expect(r.level).toBe("error");
    });

    it("ok on https URL", () => {
      const r = validateApiBaseUrl(
        env({ VITE_API_BASE_URL: "https://api.example.com", VERCEL_ENV: "production" }),
      );
      expect(r.level).toBe("ok");
      expect(r.context).toBe("production");
    });

    it("ok on subdomain https", () => {
      const r = validateApiBaseUrl(
        env({
          VITE_API_BASE_URL: "https://instaedit.example.dev",
          VERCEL_ENV: "production",
        }),
      );
      expect(r.level).toBe("ok");
    });
  });

  describe("redaction (verify via the success message)", () => {
    it("strips query string so secrets don't leak into build logs", () => {
      const r = validateApiBaseUrl(
        env({ VITE_API_BASE_URL: "https://api.example.com?token=secret" }),
      );
      expect(r.messages[0]).not.toContain("secret");
      expect(r.messages[0]).toContain("redacted");
    });

    it("strips hash fragment (e.g. #access_token=...) — these are sent by the browser and show in logs", () => {
      const r = validateApiBaseUrl(
        env({ VITE_API_BASE_URL: "https://api.example.com/path#access_token=topsecret" }),
      );
      expect(r.messages[0]).not.toContain("topsecret");
      expect(r.messages[0]).toContain("redacted");
    });

    it("strips userinfo password", () => {
      const r = validateApiBaseUrl(
        env({ VITE_API_BASE_URL: "https://user:hunter2@api.example.com" }),
      );
      expect(r.messages[0]).not.toContain("hunter2");
      expect(r.messages[0]).toContain("***");
    });

    it("does NOT redact the pathname (paths aren't secrets; pinning the contract)", () => {
      const r = validateApiBaseUrl(
        env({ VITE_API_BASE_URL: "https://api.example.com/api/v1/auth/instagram/login?token=x" }),
      );
      // Pathname verbatim
      expect(r.messages[0]).toContain("/api/v1/auth/instagram/login");
      // Query stripped
      expect(r.messages[0]).not.toContain("token=x");
    });
  });
});
