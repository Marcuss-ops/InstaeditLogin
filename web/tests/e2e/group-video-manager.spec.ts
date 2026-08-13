import { test, expect } from "@playwright/test";

/**
 * group-video-manager.spec.ts — E2E for the Copertine hub's
 * Video/Cover manager (web/src/pages/internal/GroupVideoManager.tsx +
 * GroupCovers.tsx) on /app/covers?group=7.
 *
 * Coverage:
 *   1. Visibility tabs + category filter over the canonical video list.
 *   2. The "Dettagli" drawer: prefilled title/category/visibility,
 *      edits, and a single metadata PATCH on save.
 *   3. "Modifica copertina": resolves the workspace, creates the
 *      idempotent editor session, mints a launch token, and opens
 *      InstaEditor in a new tab.
 *
 * Mocking strategy (hermetic — no Go backend, no Postgres):
 *   - Every /api/v1/** call is intercepted by page.route and answered
 *     from static fixtures, exactly the shapes the SPA consumers
 *     expect. window.open is overridden via addInitScript so the
 *     editor "tab" is a plain in-memory object whose location.href we
 *     can assert on without navigating to the real editor origin.
 *
 * The page is the same surface a contributor drives manually, so these
 * specs lock the user-visible behaviour (filters, save payload, popup)
 * rather than implementation details.
 */

interface VideoFixture {
  youtube_video_id: string;
  title: string;
  description?: string;
  thumbnail_url: string;
  platform_account_id: number;
  channel_name: string;
  privacy_status?: "public" | "private" | "unlisted";
  actual_privacy?: string;
  category_id?: string;
  category_title?: string;
}

const videoFixture = (overrides: Partial<VideoFixture> = {}): VideoFixture => ({
  youtube_video_id: "video-1",
  title: "Video privato",
  thumbnail_url: "https://i.ytimg.com/vi/video-1/hqdefault.jpg",
  platform_account_id: 42,
  channel_name: "Wrestling Insider RU",
  actual_privacy: "private",
  ...overrides,
});

const CATEGORIES = [
  { id: "17", label: "Sport" },
  { id: "20", label: "Gaming" },
  { id: "24", label: "Intrattenimento" },
];

/**
 * Route every API call the hub + manager make. Returns the captured
 * PATCH / POST bodies so tests can assert on the save payloads.
 */
async function mockInstaEditAPI(
  page: import("@playwright/test").Page,
  videos: VideoFixture[],
): Promise<{ patchBodies: unknown[]; editorSessionBodies: unknown[] }> {
  const patchBodies: unknown[] = [];
  const editorSessionBodies: unknown[] = [];

  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const method = request.method();
    const json = (body: unknown, status = 200) =>
      route.fulfill({
        status,
        contentType: "application/json",
        body: JSON.stringify(body),
      });

    // ── Session (fetchSession + useGroupsData both hit /auth/me) ──
    if (path === "/api/v1/auth/me") {
      return json({ user_id: 1, name: "Test User", workspace_id: 7 });
    }

    // ── Groups tree + accounts manifest ───────────────────────────
    if (path === "/api/v1/groups/aggregate") {
      return json({ groups: [{ id: 7, name: "Wwe", account_ids: [42] }] });
    }
    if (path === "/api/v1/accounts") {
      return json({
        accounts: [
          {
            id: 42,
            platform: "youtube",
            status: "active",
            is_publishable: true,
            username: "Wrestling Insider RU",
            platform_user_id: "UC123",
            created_at: "2026-01-01T00:00:00Z",
          },
        ],
        has_more: false,
      });
    }

    // ── Covers zone (empty grid is enough for these specs) ────────
    if (path === "/api/v1/groups/7/covers") {
      return json({ covers: [] });
    }

    // ── Canonical video list ──────────────────────────────────────
    if (path === "/api/v1/groups/7/youtube/videos") {
      return json({ videos, warnings: [], has_more: false });
    }

    // ── Category resource (centralized drawer select) ─────────────
    if (path === "/api/v1/youtube/video-categories") {
      return json({ categories: CATEGORIES });
    }

    // ── Workspace resolve + editor session + launch token ─────────
    if (path === "/api/v1/groups/7") {
      return json({ workspace_id: 7 });
    }
    if (path === "/api/v1/youtube/editor-sessions" && method === "POST") {
      editorSessionBodies.push(JSON.parse(request.postData() ?? "{}"));
      return json(
        {
          session_id: "sess_1",
          velox_project_id: "ve_1",
          editor_url: "https://editor.instaedit.test/editor/ve_1",
          youtube_video_id: "cover-1",
          title: "Video copertina",
          description: "",
          thumbnail_url: "https://i.ytimg.com/vi/cover-1/hqdefault.jpg",
          category_id: "24",
          privacy_status: "private",
          source: "youtube",
        },
        201,
      );
    }
    if (path === "/api/v1/editor/launch" && method === "POST") {
      return json({ launch_token: "launch_tok_1" });
    }

    // ── Metadata PATCH (the "Dettagli" drawer save) ───────────────
    if (/^\/api\/v1\/groups\/7\/youtube\/videos\/[^/]+$/.test(path) && method === "PATCH") {
      patchBodies.push(JSON.parse(request.postData() ?? "{}"));
      return json({
        youtube_video_id: path.split("/").pop(),
        title: "Titolo aggiornato",
        description: "",
        category_id: "20",
        privacy_status: "public",
      });
    }

    // ── Everything else (favicons are not /api/v1; unknown API = {}) ──
    return json({});
  });

  return { patchBodies, editorSessionBodies };
}

/** Capture window.open as an in-memory tab so "Modifica copertina" can
 *  be asserted without navigating to the real editor origin. */
async function captureWindowOpen(page: import("@playwright/test").Page): Promise<void> {
  await page.addInitScript(() => {
    interface FakeTab {
      closed: boolean;
      location: { href: string };
      close: () => void;
    }
    const tabs: FakeTab[] = [];
    (window as unknown as { __tabs: FakeTab[] }).__tabs = tabs;
    window.open = function (url?: string | URL, _target?: string, _features?: string): Window | null {
      const tab: FakeTab = {
        closed: false,
        location: { href: url == null ? "" : String(url) },
        close() {
          this.closed = true;
        },
      };
      tabs.push(tab);
      return tab as unknown as Window;
    };
  });
}

async function dismissCookieBanner(page: import("@playwright/test").Page): Promise<void> {
  const accept = page.getByRole("button", {
    name: /accept|accetta|agree|ok|got it|consenti|essenziali/i,
  });
  if (await accept.first().isVisible().catch(() => false)) {
    await accept.first().click();
  }
}

test("filters the video grid by visibility tabs and category", async ({ page }) => {
  await mockInstaEditAPI(page, [
    videoFixture({ youtube_video_id: "pub-1", title: "Video pubblico", privacy_status: "public", actual_privacy: "public" }),
    videoFixture({ youtube_video_id: "priv-1", title: "Video privato", privacy_status: "private", actual_privacy: "private" }),
    videoFixture({ youtube_video_id: "unl-1", title: "Video non in elenco", privacy_status: "unlisted", actual_privacy: "unlisted", category_id: "17", category_title: "Sport" }),
  ]);
  await page.goto("/app/covers?group=7");
  await dismissCookieBanner(page);

  // All three videos render in the canonical list.
  await expect(page.getByText("Video pubblico")).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText("Video privato")).toBeVisible();
  await expect(page.getByText("Video non in elenco")).toBeVisible();

  // Visibility tabs carry derived counts.
  await expect(page.getByTestId("group-videos-filter-all")).toContainText("3");
  await expect(page.getByTestId("group-videos-filter-private")).toContainText("1");
  await expect(page.getByTestId("group-videos-filter-unlisted")).toContainText("1");
  await expect(page.getByTestId("group-videos-filter-public")).toContainText("1");

  // "Privati" narrows to the private row only.
  await page.getByTestId("group-videos-filter-private").click();
  await expect(page.getByText("Video privato")).toBeVisible();
  await expect(page.getByText("Video pubblico")).toHaveCount(0);
  await expect(page.getByText("Video non in elenco")).toHaveCount(0);

  // "Pubblici" narrows to the public row only.
  await page.getByTestId("group-videos-filter-public").click();
  await expect(page.getByText("Video pubblico")).toBeVisible();
  await expect(page.getByText("Video privato")).toHaveCount(0);

  // Category filter narrows further (Sport = the unlisted row).
  await page.getByTestId("group-videos-filter-all").click();
  await page.getByTestId("group-videos-category").selectOption("17");
  await expect(page.getByText("Video non in elenco")).toBeVisible();
  await expect(page.getByText("Video pubblico")).toHaveCount(0);
  await expect(page.getByText("Video privato")).toHaveCount(0);
});

test("saves metadata through the Dettagli drawer PATCH", async ({ page }) => {
  const { patchBodies } = await mockInstaEditAPI(page, [
    videoFixture({
      youtube_video_id: "meta-1",
      title: "Video modificabile",
      description: "Descrizione esistente",
      category_id: "24",
      category_title: "Intrattenimento",
      privacy_status: "public",
      actual_privacy: "public",
    }),
  ]);
  await page.goto("/app/covers?group=7");
  await dismissCookieBanner(page);

  await expect(page.getByText("Video modificabile")).toBeVisible({ timeout: 10_000 });
  // exact: the card <article> is also role="button" and its accessible
  // name contains "Dettagli", so a substring match would hit both.
  await page.getByRole("button", { name: "Dettagli", exact: true }).click();

  const drawer = page.getByTestId("edit-metadata-drawer");
  await expect(drawer).toBeVisible();

  // Prefilled from the video's canonical metadata.
  await expect(page.getByTestId("edit-metadata-title-input")).toHaveValue("Video modificabile");
  await expect(page.getByTestId("edit-metadata-category")).toHaveValue("24");
  const privacySelect = page.getByRole("combobox", { name: /visibilità/i });
  await expect(privacySelect).toHaveValue("public");

  // Edit title + category, then save.
  await page.getByTestId("edit-metadata-title-input").fill("Titolo aggiornato");
  await page.getByTestId("edit-metadata-category").selectOption("20");
  await page.getByTestId("edit-metadata-save").click();

  await expect
    .poll(() => patchBodies.length, { timeout: 5_000 })
    .toBeGreaterThanOrEqual(1);
  expect(patchBodies[0]).toEqual({
    platform_account_id: 42,
    title: "Titolo aggiornato",
    description: "Descrizione esistente",
    category_id: "20",
    privacy_status: "public",
  });
});

test("Modifica copertina creates the editor session and opens InstaEditor", async ({ page }) => {
  const { editorSessionBodies } = await mockInstaEditAPI(page, [
    videoFixture({
      youtube_video_id: "cover-1",
      title: "Video copertina",
      thumbnail_url: "https://i.ytimg.com/vi/cover-1/hqdefault.jpg",
    }),
  ]);
  await captureWindowOpen(page);
  await page.goto("/app/covers?group=7");
  await dismissCookieBanner(page);

  await expect(page.getByText("Video copertina")).toBeVisible({ timeout: 10_000 });
  await page.getByRole("button", { name: "Modifica copertina", exact: true }).click();

  // The idempotent session create carried the canvas + binding.
  await expect
    .poll(() => editorSessionBodies.length, { timeout: 5_000 })
    .toBeGreaterThanOrEqual(1);
  expect(editorSessionBodies[0]).toEqual({
    workspace_id: 7,
    platform_account_id: 42,
    youtube_video_id: "cover-1",
    source_thumbnail_url: "https://i.ytimg.com/vi/cover-1/hqdefault.jpg",
  });

  // The reserved tab was navigated to the editor launch URL (not closed).
  // The launch token is minted AFTER the session POST, so poll the tab's
  // href until the async launch round-trip lands instead of reading once.
  await expect
    .poll(
      async () => {
        const tabs = await page.evaluate(() =>
          (window as unknown as { __tabs: { closed: boolean; location: { href: string } }[] }).__tabs,
        );
        return tabs.length > 0 ? tabs[0].location.href : "";
      },
      { timeout: 5_000 },
    )
    .toContain("https://editor.instaedit.test/editor/ve_1");

  const tabs = await page.evaluate(() =>
    (window as unknown as { __tabs: { closed: boolean; location: { href: string } }[] }).__tabs,
  );
  expect(tabs).toHaveLength(1);
  expect(tabs[0].closed).toBe(false);
  expect(tabs[0].location.href).toContain("launch_token=launch_tok_1");
  expect(tabs[0].location.href).toContain("return_to=%2Fapp%2Fcovers%3Fgroup%3D7");
});
