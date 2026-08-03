import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { LiveStreamNewPage } from "./LiveStreamNew";
import type { LivestreamChannel } from "./livestreamsTypes";

function channel(overrides: Partial<LivestreamChannel>): LivestreamChannel {
  return {
    platform_account_id: 42,
    username: "WWE Insider Italia",
    platform_user_id: "UC123",
    account_state: "valid",
    oauth_ready: true,
    live_enabled: true,
    last_verified_at: new Date(Date.now() - 2 * 60_000).toISOString(),
    active_lives: 1,
    ...overrides,
  };
}

function mockJsonResponse(data: unknown, ok = true, status = 200) {
  return {
    ok,
    status,
    json: async () => data,
  } as unknown as Response;
}

function mockFetch(channels: LivestreamChannel[] | null, error = false) {
  return vi.fn().mockImplementation(async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
    if (url.endsWith("/api/v1/auth/me")) {
      return mockJsonResponse({ workspace_id: 7 });
    }
    if (url.includes("/api/v1/livestreams/channels")) {
      if (error) return mockJsonResponse({ error: "boom" }, false, 500);
      return mockJsonResponse({ channels });
    }
    return mockJsonResponse({}, false, 404);
  });
}

function renderPage() {
  return render(
    <MemoryRouter>
      <Routes>
        <Route path="/" element={<LiveStreamNewPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

const fixtureChannels = (): LivestreamChannel[] => [
  channel({ platform_account_id: 42, username: "WWE Insider Italia" }),
  channel({
    platform_account_id: 43,
    username: "WWE Insider France",
    platform_user_id: "UC-france",
    live_enabled: false,
    active_lives: 0,
  }),
  channel({
    platform_account_id: 44,
    username: "Old Channel",
    platform_user_id: "UC-old",
    oauth_ready: false,
    live_enabled: false,
    account_state: "reconnect_required",
    last_verified_at: null,
    active_lives: 0,
  }),
];

describe("LiveStreamNewPage", () => {
  beforeEach(() => {
    vi.resetAllMocks();
  });

  it("renders the wizard header and channel preflight badges", async () => {
    vi.stubGlobal("fetch", mockFetch(fixtureChannels()));

    renderPage();

    await waitFor(() => {
      expect(screen.getByRole("heading", { name: /Crea nuova live/i })).toBeInTheDocument();
    });
    expect(screen.getByText(/Passaggio 1 di 5/)).toBeInTheDocument();

    expect(screen.getByText("WWE Insider Italia")).toBeInTheDocument();
    expect(screen.getAllByTestId("livestream-new-oauth").length).toBeGreaterThan(0);
    // Channel 43 has a ready grant but no live scope → it shows
    // "OAuth: pronto" alongside "Live: non abilitato".
    expect(screen.getAllByText("OAuth: pronto")).toHaveLength(2);
    expect(screen.getByText("OAuth: assente")).toBeInTheDocument();
    expect(screen.getByText("Live: abilitato")).toBeInTheDocument();
    expect(screen.getAllByText("Live: non abilitato")).toHaveLength(2);
    expect(screen.getAllByText(/Ultima verifica: \d+ min fa/)).toHaveLength(2);
    expect(screen.getByText("Ultima verifica: Mai verificato")).toBeInTheDocument();
    expect(screen.getByText("Live attive: 1")).toBeInTheDocument();
    // Only channel 42 has both a ready grant AND the live scope.
    expect(screen.getByText("1 di 3 pronti per il live")).toBeInTheDocument();
  });

  it("blocks the continue button until a live-enabled channel is selected", async () => {
    vi.stubGlobal("fetch", mockFetch(fixtureChannels()));

    renderPage();
    await waitFor(() => {
      expect(screen.getByTestId("livestream-new-continue")).toBeDisabled();
    });
    expect(screen.getByText(/Seleziona un canale con OAuth pronto/)).toBeInTheDocument();

    // Blocked channel: clicking does not select it.
    await userEvent.click(screen.getByTestId("livestream-new-channel-43"));
    expect(screen.getByTestId("livestream-new-continue")).toBeDisabled();

    await userEvent.click(screen.getByTestId("livestream-new-channel-42"));
    expect(screen.getByTestId("livestream-new-continue")).toBeEnabled();
    expect(screen.getByText("Canale selezionato: WWE Insider Italia")).toBeInTheDocument();
  });

  it("keeps continue blocked when no channel is eligible", async () => {
    vi.stubGlobal(
      "fetch",
      mockFetch([
        channel({ platform_account_id: 43, username: "France", live_enabled: false, active_lives: 0 }),
        channel({ platform_account_id: 44, username: "Old", oauth_ready: false, live_enabled: false, last_verified_at: null, active_lives: 0 }),
      ]),
    );

    renderPage();
    await waitFor(() => {
      expect(screen.getByText("0 di 2 pronti per il live")).toBeInTheDocument();
    });
    await userEvent.click(screen.getByTestId("livestream-new-channel-43"));
    expect(screen.getByTestId("livestream-new-continue")).toBeDisabled();
  });

  it("shows the empty state when the workspace has no YouTube channels", async () => {
    vi.stubGlobal("fetch", mockFetch([]));

    renderPage();

    await waitFor(() => {
      expect(screen.getByText("Nessun canale YouTube nel workspace")).toBeInTheDocument();
    });
    expect(screen.getByRole("link", { name: /Vai ai canali/i })).toHaveAttribute("href", "/app/linking");
  });

  it("shows an error state with retry when the preflight cannot be loaded", async () => {
    vi.stubGlobal("fetch", mockFetch(null, true));

    renderPage();

    await waitFor(() => {
      expect(screen.getByText("Impossibile caricare i canali")).toBeInTheDocument();
    });
    expect(screen.getByRole("button", { name: /Riprova/i })).toBeInTheDocument();
  });
});
