import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { InternalLinking } from "./Linking";

/**
 * N+1 DoD / Fase 8 performance measurement.
 *
 * For each simulated dataset (10 / 50 / 100 / 200 accounts) it locks:
 *   - HTTP request count on page open (target: 2-3, must NOT grow with N),
 *   - ZERO per-account /accounts/{id} fan-out (and therefore ZERO external
 *     provider / YouTube calls — the page only talks to our API),
 *   - time-to-interactive bounded by a generous CI-safe ceiling. The real
 *     measured values are logged and reported in
 *     docs/NPLUS1_PERFORMANCE.md alongside the backend handler benchmark.
 *
 * The backend side of the same page load is exactly ONE indexed LEFT JOIN
 * query (pkg/api/accounts_list_benchmark_test.go measures the handler at
 * the same dataset sizes).
 */
const DATASETS = [10, 50, 100, 200];
const INTERACTIVE_CEILING_MS = 5000;

function mockJsonResponse(data: unknown, ok = true, status = 200) {
  return { ok, status, json: async () => data } as unknown as Response;
}

function accountsFixture(n: number) {
  return Array.from({ length: n }, (_, i) => ({
    id: i + 1,
    platform: "youtube",
    platform_user_id: `UC-${i + 1}`,
    username: `channel-${i + 1}`,
    status: "active",
    account_state: "valid" as const,
    is_publishable: true,
    created_at: "2026-08-05T10:00:00Z",
  }));
}

describe.each(DATASETS)("N+1 perf — %d accounts", (n) => {
  it("page opens with ≤3 API requests, zero per-account fan-out, interactive under ceiling", async () => {
    const requestedUrls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        requestedUrls.push(url);
        if (url.includes("/api/v1/auth/me")) {
          return mockJsonResponse({ user_id: 1 });
        }
        if (url.includes("/api/v1/accounts")) {
          return mockJsonResponse({
            accounts: accountsFixture(n),
            has_more: false,
          });
        }
        return mockJsonResponse({}, false, 404);
      }),
    );

    const t0 = performance.now();
    render(
      <MemoryRouter>
        <Routes>
          <Route path="/" element={<InternalLinking />} />
        </Routes>
      </MemoryRouter>,
    );

    // Interactive = the app shell + provider cards are rendered.
    await waitFor(() => {
      expect(screen.getByRole("heading", { name: /Linking/i })).toBeInTheDocument();
    });
    await waitFor(() => {
      expect(screen.getByText("YouTube")).toBeInTheDocument();
    });
    const timeToInteractive = Math.round(performance.now() - t0);

    const apiCalls = requestedUrls.filter((u) => u.includes("/api/v1/accounts"));
    const detailCalls = requestedUrls.filter((u) => /\/api\/v1\/accounts\/\d+$/.test(u));
    // eslint-disable-next-line no-console
    console.log(
      `[perf] accounts=${n} api_requests=${apiCalls.length} total_requests=${requestedUrls.length} ` +
        `per_account_fanout=${detailCalls.length} time_to_interactive=${timeToInteractive}ms`,
    );

    expect(detailCalls.length).toBe(0); // no per-account fan-out (N+1 gone)
    expect(apiCalls.length).toBe(1); // exactly ONE GET /accounts (shared cache)
    expect(requestedUrls.length).toBeLessThanOrEqual(3); // auth/me + accounts + slack
    expect(timeToInteractive).toBeLessThan(INTERACTIVE_CEILING_MS);
  });
});
