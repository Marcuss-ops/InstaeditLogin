import { describe, expect, it } from "vitest";
import { resolveApiBaseUrl } from "./api";

describe("resolveApiBaseUrl", () => {
  it("canonicalizes the legacy dev API on public InstaEdit hosts", () => {
    expect(resolveApiBaseUrl("https://dev.instaedit.org", "app.instaedit.org")).toBe("https://api.instaedit.org");
    expect(resolveApiBaseUrl("https://dev.instaedit.org/", "dev.instaedit.org")).toBe("https://api.instaedit.org");
  });

  it("keeps the canonical API host unchanged", () => {
    expect(resolveApiBaseUrl("https://api.instaedit.org", "app.instaedit.org")).toBe("https://api.instaedit.org");
  });

  it("does not rewrite local development overrides", () => {
    expect(resolveApiBaseUrl("http://localhost:8080", "localhost")).toBe("http://localhost:8080");
    expect(resolveApiBaseUrl("https://dev.instaedit.org", "localhost")).toBe("https://dev.instaedit.org");
  });

  it("uses the canonical fallback when a public bundle has no configured URL", () => {
    expect(resolveApiBaseUrl(undefined, "app.instaedit.org")).toBe("https://api.instaedit.org");
  });
});
