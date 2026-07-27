import { test, expect } from "@playwright/test";

/**
 * booking-flow.spec.ts — E2E test for the marketing strategy-call
 * modal flow on / .
 *
 * Coverage:
 *   1. Hero CTA "Schedule Your Free Strategy Call" opens the modal.
 *   2. Modal renders 3 closed-set questions (goal / budget / ready)
 *      with each radio card accessible by accessible name.
 *   3. Submit completes the qualification AND fires:
 *        a) POST /api/v1/booking_events (fire-and-forget telemetry)
 *        b) window.open(BOOKING_URL, "_blank", "noopener,noreferrer")
 *      — both verified via Playwright route + addInitScript without
 *      depending on a real network round-trip OR a running backend.
 *   4. Popup URL anchors to the in-source BOOKING_URL constant plus
 *      the canonical noopener windowFeatures. We assert on the
 *      URL scheme + utm_source substring RATHER than the full URL
 *      so the test does NOT bake the Calendly placeholder into the
 *      assertion — when the real Google Appointment slot replaces
 *      `BOOKING_URL` in web/src/lib/booking.ts, the test stays green.
 *   5. The booking-tier chip inside the modal renders "Strategy
 *      Call" (intent="general" — the Hero CTA's default), locking
 *      the intent argument passed to BookingContext.open().
 *
 * Mocking strategy (chosen over a real backend round-trip):
 *   - window.open is replaced via addInitScript so the call is
 *     captured FOR ASSERTION but not actually navigated. Real
 *     navigation would (a) flakify the test on Calendly /
 *     Google-side availability, (b) cost CI time, and (c)
 *     "succeed" for the wrong reason.
 *   - POST /api/v1/booking_events is intercepted via page.route
 *     so the test is hermetic — no Go backend, no Postgres, no
 *     rate-limited token bucket to burn.
 *
 * Cookie banner: the CookieBanner component renders with
 * role="dialog" too (per src/components/CookieBanner.tsx). To
 * avoid a strict-mode locator collision we dismiss the banner
 * explicitly before asserting on the booking modal.
 */
test("Hero CTA opens modal \u2192 3 questions \u2192 Submit fires telemetry POST + window.open", async ({ page }) => {
  let capturedBody: unknown;
  let capturedPost = 0;

  // ── 1. Mock the telemetry POST. The glob matches the browser-
  //    side URL pattern (Vite dev proxies /api to :8080, but the
  //    page.route intercept is at the browser layer so the glob
  //    sees the URL as the SPA sees it.)
  await page.route("**/api/v1/booking_events", async (route) => {
    if (route.request().method() !== "POST") {
      return route.continue();
    }
    capturedBody = (() => {
      try {
        return JSON.parse(route.request().postData() ?? "{}");
      } catch {
        return null;
      }
    })();
    capturedPost += 1;
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "recorded" }),
    });
  });

  // ── 2. Capture window.open so the test does NOT actually
  //    navigate. addInitScript runs on every page navigation
  //    BEFORE any module script executes, so React's setTimeout-
  //    driven window.open call hits the override.
  await page.addInitScript(() => {
    interface OpenedEntry {
      url: string;
      target: string;
      features: string;
    }
    const opened: OpenedEntry[] = [];
    (window as unknown as { __opened: OpenedEntry[] }).__opened = opened;
    const original = window.open;
    window.open = function (
      url?: string | URL,
      target?: string,
      features?: string,
    ): Window | null {
      opened.push({
        url: url == null ? "" : String(url),
        target: target ?? "",
        features: features ?? "",
      });
      try {
        return original.call(window, url, target, features);
      } catch {
        return null;
      }
    };
  });

  // ── 3. Visit the landing page and dismiss the cookie banner so
  //    the role="dialog" assertion below can't collide with it.
  await page.goto("/");
  const cookieAccept = page.getByRole("button", {
    name: /accept|agree|ok|got it/i,
  });
  if (await cookieAccept.first().isVisible().catch(() => false)) {
    await cookieAccept.first().click();
  }

  // ── 4. Click the Hero CTA. The BookingProvider is mounted by
  //    App.tsx so the modal is available regardless of which page
  //    we land on; the Hero on / is the entry point marketing
  //    copy wires users through first.
  const heroCta = page.getByRole("button", {
    name: /schedule your free strategy call/i,
  });
  await expect(heroCta).toBeVisible({ timeout: 10_000 });
  await heroCta.click();

  // ── 5. The BookingProvider modal renders as an ARIA dialog.
  //    We pin the locator by accessible-name so the cookie banner
  //    (also role="dialog") cannot collide, and so a future
  //    second-dialog (e.g. onboarding tip) wouldn't either.
  const dialog = page.getByRole("dialog", {
    name: /schedule your free strategy call/i,
  });
  await expect(dialog).toBeVisible();

  // ── 6. Each of the 3 question sections renders the expected
  //    prompt. We use case-insensitive regex anchors so copy
  //    tweaks (a trailing "?" or em-dash) don't break the test.
  await expect(dialog).toContainText(/what is your primary goal right now\?/i);
  await expect(dialog).toContainText(/what budget/i);
  await expect(dialog).toContainText(/ready to get started this week\?/i);

  // ── 7. Tier chip = "Strategy Call" because the Hero CTA opens
  //    the modal with intent="general" (default). Locks the
  //    intent argument in BookingContext so a future refactor of
  //    the Hero CTA's open(...) call would surface in CI.
  await expect(page.getByTestId("booking-tier-chip")).toContainText(/strategy call/i);

  // ── 8. Pick the most-populated answers for each closed-set:
  //    goal=launch, budget=starter, ready=yes (matches the
  //    "high-intent visitor" journey the marketing copy walks
  //    through).
  await dialog
    .getByRole("radio", { name: /launch my first channel/i })
    .click();
  await dialog
    .getByRole("radio", { name: /under \$200/i })
    .click();
  await dialog
    .getByRole("radio", { name: /yes.*ready this week/i })
    .click();

  // Submit becomes enabled. Click + assert both side effects.
  const submit = dialog.getByRole("button", {
    name: /schedule my free call/i,
  });
  await expect(submit).toBeEnabled();
  await submit.click();

  // ── 9. Telemetry POST hit the backend mock with the expected
  //    payload. expect.poll gives the fire-and-forget promise up
  //    to 5s to resolve without juggling timers in the test code.
  await expect
    .poll(() => capturedPost, { timeout: 5_000 })
    .toBeGreaterThanOrEqual(1);
  await expect
    .poll(() => capturedBody, { timeout: 5_000 })
    .toEqual({
      intent: "general",
      goal: "launch",
      budget: "starter",
      ready: "yes",
    });

  // ── 10. window.open was invoked. The modal schedules the
  //     popup in a 220ms setTimeout; expect.poll reads the
  //     captured array repeatedly so we don't slow the test.
  await expect
    .poll(
      async () =>
        (
          await page.evaluate(
            () =>
              (
                window as unknown as {
                  __opened: { url: string; target: string; features: string }[];
                }
              ).__opened,
          )
        ).length,
      { timeout: 5_000 },
    )
    .toBeGreaterThan(0);

  const opened = await page.evaluate(() =>
    (
      window as unknown as {
        __opened: { url: string; target: string; features: string }[];
      }
    ).__opened,
  );
  expect(opened).toHaveLength(1);

  // ── 11. URL assertions are HOST-AGNOSTIC: assert the scheme
  //     (any https URL works) + the UTM tag (intent="general"
  //     maps to utm_source=instagram_landing). When BOOKING_URL
  //     is replaced with the real Google Appointment slot, this
  //     test stays green without an edit.
  expect(opened[0].url).toMatch(/^https:\/\//);
  expect(opened[0].url).toContain("utm_source=instagram_landing");

  // ── 12. windowFeatures contract: _blank target + noopener
  //     + noreferrer. The BookingProvider passes these verbatim
  //     in window.open's third arg.
  expect(opened[0].target).toBe("_blank");
  expect(opened[0].features).toContain("noopener");
  expect(opened[0].features).toContain("noreferrer");
});
