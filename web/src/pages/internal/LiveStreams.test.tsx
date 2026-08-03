import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { LiveStreamsPage } from "./LiveStreams";
import { toastBus } from "../../components/toast/toast-bus";
import type { LivestreamRow } from "./livestreamsTypes";
import { matchesTab, stateLabel, summarize } from "./livestreamsVisual";

function row(overrides: Partial<LivestreamRow>): LivestreamRow {
  return {
    id: "ls_1",
    workspace_id: 7,
    platform_account_id: 42,
    channel_name: "WWE Insider Italia",
    title: "WWE NEWS 24/7",
    description: "",
    privacy_status: "unlisted",
    playback_mode: "loop_continuous",
    schedule_type: "manual",
    scheduled_start_at: null,
    desired_state: "draft",
    actual_state: "draft",
    resolution: "1080p30",
    frame_rate: 30,
    auto_restart: true,
    created_at: "2026-08-03T10:00:00Z",
    updated_at: "2026-08-03T10:00:00Z",
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

function mockFetch(items: LivestreamRow[] | null, error = false) {
  return vi.fn().mockImplementation(async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
    if (url.endsWith("/api/v1/auth/me")) {
      return mockJsonResponse({ workspace_id: 7 });
    }
    if (url.includes("/api/v1/livestreams")) {
      if (error) return mockJsonResponse({ error: "boom" }, false, 500);
      return mockJsonResponse({ items });
    }
    return mockJsonResponse({}, false, 404);
  });
}

function fixtureRows(): LivestreamRow[] {
  return [
    row({ id: "ls_live", actual_state: "live", desired_state: "live" }),
    row({ id: "ls_degraded", actual_state: "degraded", desired_state: "live" }),
    row({ id: "ls_scheduled", actual_state: "scheduled", desired_state: "scheduled", schedule_type: "scheduled", scheduled_start_at: "2026-08-05T18:00:00Z" }),
    row({ id: "ls_reconnecting", actual_state: "reconnecting", desired_state: "live" }),
    row({ id: "ls_failed", actual_state: "failed", desired_state: "live" }),
    row({ id: "ls_draft", title: "Draft test", actual_state: "draft" }),
  ];
}

describe("LiveStreamsPage", () => {
  beforeEach(() => {
    vi.resetAllMocks();
  });

  it("renders the header, summary counts and live cards", async () => {
    vi.stubGlobal("fetch", mockFetch(fixtureRows()));

    render(<LiveStreamsPage />);

    await waitFor(() => {
      expect(screen.getByRole("heading", { name: /Live streaming/i })).toBeInTheDocument();
    });
    expect(screen.getByTestId("livestreams-create-cta")).toBeInTheDocument();

    // Live ora counts live + degraded (both on air); the other cards
    // are strict per state.
    expect(screen.getByTestId("livestreams-summary-live")).toHaveTextContent("2");
    expect(screen.getByTestId("livestreams-summary-scheduled")).toHaveTextContent("1");
    expect(screen.getByTestId("livestreams-summary-reconnecting")).toHaveTextContent("1");
    expect(screen.getByTestId("livestreams-summary-errors")).toHaveTextContent("1");

    expect(screen.getAllByTestId("livestream-card")).toHaveLength(6);
    expect(screen.getAllByText("WWE NEWS 24/7").length).toBeGreaterThan(0);
    expect(screen.getAllByText("WWE Insider Italia").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Playlist in loop").length).toBeGreaterThan(0);
    expect(screen.getAllByText(/Salute: Ottima/).length).toBeGreaterThan(0);
  });

  it("filters cards by tab", async () => {
    vi.stubGlobal("fetch", mockFetch(fixtureRows()));

    render(<LiveStreamsPage />);
    await waitFor(() => {
      expect(screen.getAllByTestId("livestream-card")).toHaveLength(6);
    });

    await userEvent.click(screen.getByTestId("livestreams-tab-live"));
    expect(screen.getAllByTestId("livestream-card")).toHaveLength(2);

    await userEvent.click(screen.getByTestId("livestreams-tab-scheduled"));
    expect(screen.getAllByTestId("livestream-card")).toHaveLength(1);

    await userEvent.click(screen.getByTestId("livestreams-tab-drafts"));
    expect(screen.getByText("Draft test")).toBeInTheDocument();

    await userEvent.click(screen.getByTestId("livestreams-tab-errors"));
    expect(screen.getAllByTestId("livestream-card")).toHaveLength(1);
  });

  it("shows the empty state with a CTA when no livestreams exist", async () => {
    vi.stubGlobal("fetch", mockFetch([]));

    render(<LiveStreamsPage />);

    await waitFor(() => {
      expect(screen.getByText("Nessuna live configurata")).toBeInTheDocument();
    });
    expect(screen.getByText(/Trasmetti un video o una playlist preregistrata/)).toBeInTheDocument();

    await userEvent.click(screen.getByTestId("livestreams-empty-cta"));
    expect(toastBus.__sizeForTests()).toBe(1);
  });

  it("shows an error state with retry when the list cannot be loaded", async () => {
    vi.stubGlobal("fetch", mockFetch(null, true));

    render(<LiveStreamsPage />);

    await waitFor(() => {
      expect(screen.getByText("Impossibile caricare le live")).toBeInTheDocument();
    });
    expect(screen.getByRole("button", { name: /Riprova/i })).toBeInTheDocument();
  });

  it("deletes a livestream behind a two-step confirmation", async () => {
    const fetchMock = mockFetch([row({ id: "ls_live", actual_state: "live", desired_state: "live" })]);
    vi.stubGlobal("fetch", fetchMock);

    render(<LiveStreamsPage />);
    await waitFor(() => {
      expect(screen.getAllByTestId("livestream-card")).toHaveLength(1);
    });

    await userEvent.click(screen.getByRole("button", { name: "Altre azioni" }));
    await userEvent.click(screen.getByRole("menuitem", { name: /Elimina/i }));
    expect(screen.getByTestId("livestream-delete-confirm")).toBeInTheDocument();

    await userEvent.click(screen.getByTestId("livestream-delete-confirm-button"));
    await waitFor(() => {
      expect(screen.queryAllByTestId("livestream-card")).toHaveLength(0);
    });

    const deleteCall = fetchMock.mock.calls.find(
      ([, init]) => (init as RequestInit | undefined)?.method === "DELETE",
    );
    expect(deleteCall).toBeDefined();
    expect(String(deleteCall?.[0])).toContain("/api/v1/livestreams/ls_live");
  });

  it("cancels the delete confirmation without calling the API", async () => {
    const fetchMock = mockFetch([row({ id: "ls_live", actual_state: "live" })]);
    vi.stubGlobal("fetch", fetchMock);

    render(<LiveStreamsPage />);
    await waitFor(() => {
      expect(screen.getAllByTestId("livestream-card")).toHaveLength(1);
    });

    await userEvent.click(screen.getByRole("button", { name: "Altre azioni" }));
    await userEvent.click(screen.getByRole("menuitem", { name: /Elimina/i }));
    await userEvent.click(screen.getByRole("button", { name: /Annulla/i }));

    expect(screen.queryByTestId("livestream-delete-confirm")).not.toBeInTheDocument();
    const deleteCall = fetchMock.mock.calls.find(
      ([, init]) => (init as RequestInit | undefined)?.method === "DELETE",
    );
    expect(deleteCall).toBeUndefined();
  });
});

describe("livestreamsVisual", () => {
  it("partitions states across the correct tabs", () => {
    expect(matchesTab(row({ actual_state: "live" }), "live")).toBe(true);
    expect(matchesTab(row({ actual_state: "degraded" }), "live")).toBe(true);
    expect(matchesTab(row({ actual_state: "testing" }), "live")).toBe(true);
    expect(matchesTab(row({ actual_state: "scheduled" }), "scheduled")).toBe(true);
    expect(matchesTab(row({ actual_state: "draft" }), "drafts")).toBe(true);
    expect(matchesTab(row({ actual_state: "completed" }), "ended")).toBe(true);
    expect(matchesTab(row({ actual_state: "cancelled" }), "ended")).toBe(true);
    expect(matchesTab(row({ actual_state: "failed" }), "errors")).toBe(true);
    // Transient lifecycle states only appear under "Tutte".
    expect(matchesTab(row({ actual_state: "reconnecting" }), "live")).toBe(false);
    expect(matchesTab(row({ actual_state: "starting" }), "live")).toBe(false);
    expect(matchesTab(row({ actual_state: "reconnecting" }), "all")).toBe(true);
  });

  it("summarizes counts per card semantics", () => {
    const summary = summarize([
      row({ actual_state: "live" }),
      row({ actual_state: "degraded" }),
      row({ actual_state: "scheduled" }),
      row({ actual_state: "reconnecting" }),
      row({ actual_state: "failed" }),
    ]);
    expect(summary).toEqual({ live: 2, scheduled: 1, reconnecting: 1, errors: 1 });
  });

  it("labels every state machine value", () => {
    for (const state of [
      "draft", "preparing", "ready", "scheduled", "starting", "waiting_for_ingest",
      "testing", "live", "degraded", "reconnecting", "stopping", "completed",
      "failed", "cancelled",
    ]) {
      expect(stateLabel(state).length).toBeGreaterThan(0);
    }
  });
});
