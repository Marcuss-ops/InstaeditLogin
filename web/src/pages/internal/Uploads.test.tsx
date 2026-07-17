import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { ToastProvider } from "../../components/toast";
import { InternalUploads } from "./Uploads";

function mockJsonResponse(data: unknown, ok = true, status = 202) {
  return {
    ok,
    status,
    json: async () => data,
  } as unknown as Response;
}

const sampleWorkspaces = [{ id: 47, name: "Personal" }];
const samplePages = [
  {
    id: 9,
    platform: "facebook",
    username: "caleb-foster",
    created_at: "2026-07-10T00:00:00Z",
  },
];
const sampleDrives = [
  {
    id: 21,
    platform: "google-drive",
    username: "caleb-foster",
    created_at: "2026-07-10T00:00:00Z",
  },
];

function setupFetchMock(opts: {
  importResponse?: { ok: boolean; body: unknown; status?: number };
  includeDrives?: boolean;
}) {
  const importResponse = opts.importResponse ?? {
    ok: true,
    body: {
      folder_id: "abc123",
      scheduled_count: 3,
      page_count: 1,
      first_publish_at: "2026-07-17T16:00:00Z",
      last_scheduled_at: "2026-07-19T07:00:00Z",
      entries: [
        {
          index: 0,
          drive_file_id: "f-1",
          name: "intro.mp4",
          job_id: 101,
          scheduled_at: "2026-07-17T16:00:00Z",
          relative_hours_from_now: 0.05,
        },
        {
          index: 1,
          drive_file_id: "f-2",
          name: "demo.mp4",
          job_id: 102,
          scheduled_at: "2026-07-17T19:30:00Z",
          relative_hours_from_now: 3.5,
        },
        {
          index: 2,
          drive_file_id: "f-3",
          name: "outro.mp4",
          job_id: 103,
          scheduled_at: "2026-07-18T00:00:00Z",
          relative_hours_from_now: 8.0,
        },
      ],
      partial_failure: false,
      failed_at_page: 0,
      failed_at_page_token: "",
      note: "",
      needs_google_drive_api_key: false,
      needs_drive_account: false,
      error: "",
    },
  };
  const calls: { url: string; method: string; body?: unknown }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation(async (input: RequestInfo, init?: RequestInit) => {
      const url =
        typeof input === "string" ? input : (input as { url: string }).url;
      const method = (init?.method ?? "GET").toUpperCase();
      let body: unknown = undefined;
      if (typeof init?.body === "string") {
        try {
          body = JSON.parse(init.body);
        } catch {
          body = init.body;
        }
      }
      calls.push({ url, method, body });

      if (url.endsWith("/api/v1/workspaces")) {
        return mockJsonResponse({ workspaces: sampleWorkspaces });
      }
      if (url.endsWith("/api/v1/accounts")) {
        const accounts = opts.includeDrives
          ? [...samplePages, ...sampleDrives]
          : [...samplePages];
        return mockJsonResponse({ accounts });
      }
      if (url.includes("/api/v1/uploads/batch/by-folder")) {
        return mockJsonResponse(
          importResponse.body,
          importResponse.ok,
          importResponse.status ?? (importResponse.ok ? 202 : 502),
        );
      }
      return mockJsonResponse({}, false, 404);
    }),
  );
  return calls;
}

function renderPage() {
  // Render with a sibling route so Link-based CTAs (calendar nav) don't
  // 404 in the test environment.
  return render(
    <MemoryRouter initialEntries={["/app/uploads"]}>
      <ToastProvider>
        <Routes>
          <Route path="/app/uploads" element={<InternalUploads />} />
          <Route path="/app/uploads/calendar" element={<div data-testid="calendar-stub" />} />
        </Routes>
      </ToastProvider>
    </MemoryRouter>,
  );
}

describe("InternalUploads (/app/uploads)", () => {
  beforeEach(() => {
    vi.resetAllMocks();
  });

  it("renders the form fields after dependencies load", async () => {
    setupFetchMock({});
    renderPage();
    await waitFor(() => {
      expect(screen.getByTestId("uploads-form")).toBeInTheDocument();
    });
    expect(screen.getByLabelText(/Workspace/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/Facebook Page/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/Google Drive Folder ID/i)).toBeInTheDocument();
  });

  it("blocks submit and shows inline error when the folder id is malformed", async () => {
    const calls = setupFetchMock({});
    const user = userEvent.setup();
    renderPage();
    await waitFor(() =>
      expect(screen.getByLabelText(/Google Drive Folder ID/i)).toBeInTheDocument(),
    );
    await user.type(
      screen.getByLabelText(/Google Drive Folder ID/i),
      "bad id with spaces",
    );
    expect(
      screen.getByText(/must be 1\u2013100 letters, digits, hyphens, or underscores/i),
    ).toBeInTheDocument();
    const submit = screen.getByTestId("uploads-submit") as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
    const importCalls = calls.filter((c) =>
      c.url.includes("/api/v1/uploads/batch/by-folder"),
    );
    expect(importCalls).toHaveLength(0);
  });

  it("happy path: sends the right payload and renders the success view", async () => {
    const calls = setupFetchMock({});
    const user = userEvent.setup();
    renderPage();
    await waitFor(() =>
      expect(screen.getByLabelText(/Google Drive Folder ID/i)).toBeInTheDocument(),
    );
    await user.type(
      screen.getByLabelText(/Google Drive Folder ID/i),
      "1HregS58okcSoe8597qdXgpZM6K4CwEBD",
    );
    await user.click(screen.getByTestId("uploads-submit"));

    await waitFor(() => {
      expect(screen.getByTestId("uploads-success")).toBeInTheDocument();
    });
    expect(screen.getByText(/3 videos scheduled/i)).toBeInTheDocument();
    expect(screen.getByText(/Open calendar/i)).toBeInTheDocument();

    const importCall = calls.find((c) =>
      c.url.includes("/api/v1/uploads/batch/by-folder"),
    );
    expect(importCall).toBeDefined();
    expect(importCall!.method).toBe("POST");
    const body = importCall!.body as Record<string, unknown>;
    expect(body.folder_id).toBe("1HregS58okcSoe8597qdXgpZM6K4CwEBD");
    expect(body.workspace_id).toBe(47);
    expect(body.facebook_account_id).toBe(9);
    // Jitter defaults are NOT sent (advanced toggle closed).
    expect(body.min_jitter_seconds).toBeUndefined();
    expect(body.max_jitter_seconds).toBeUndefined();
    expect(body.drive_account_id).toBeUndefined();
  });

  it("partial-failure response renders resume instructions + summary blocks", async () => {
    setupFetchMock({
      importResponse: {
        ok: true,
        body: {
          folder_id: "abc",
          scheduled_count: 2,
          page_count: 2,
          first_publish_at: "2026-07-17T16:00:00Z",
          last_scheduled_at: "2026-07-17T20:00:00Z",
          entries: [
            {
              index: 0,
              drive_file_id: "a",
              name: "a.mp4",
              job_id: 1,
              scheduled_at: "2026-07-17T16:00:00Z",
              relative_hours_from_now: 0,
            },
            {
              index: 1,
              drive_file_id: "b",
              name: "b.mp4",
              job_id: 2,
              scheduled_at: "2026-07-17T20:00:00Z",
              relative_hours_from_now: 4,
            },
          ],
          partial_failure: true,
          failed_at_page: 3,
          failed_at_page_token: "tok-FAILED",
          note:
            "Drive returned 5xx on page 3 \u2014 retry from cursor_scheduled_at to resume the cumulative jitter.",
          needs_google_drive_api_key: false,
          needs_drive_account: false,
          error: "",
        },
      },
    });
    const user = userEvent.setup();
    renderPage();
    await waitFor(() =>
      expect(screen.getByLabelText(/Google Drive Folder ID/i)).toBeInTheDocument(),
    );
    await user.type(screen.getByLabelText(/Google Drive Folder ID/i), "abc");
    await user.click(screen.getByTestId("uploads-submit"));

    await waitFor(() => {
      expect(screen.getByTestId("uploads-partial")).toBeInTheDocument();
    });
    expect(screen.getByText(/Partial/i)).toBeInTheDocument();
    expect(screen.getByText(/tok-FAILED/)).toBeInTheDocument();
    expect(
      screen.getByText(/POST \/api\/v1\/media\/import\/drive\/folder/i),
    ).toBeInTheDocument();
  });

  it("server-side config guidance view renders when the API key is missing", async () => {
    setupFetchMock({
      importResponse: {
        ok: true,
        body: {
          folder_id: "abc",
          scheduled_count: 0,
          page_count: 0,
          first_publish_at: "",
          last_scheduled_at: "",
          entries: [],
          partial_failure: false,
          note:
            "Server is missing GOOGLE_DRIVE_API_KEY. Either set the env var OR pass a drive_account_id to use your linked Drive.",
          needs_google_drive_api_key: true,
          needs_drive_account: true,
          error: "",
        },
      },
    });
    const user = userEvent.setup();
    renderPage();
    await waitFor(() =>
      expect(screen.getByLabelText(/Google Drive Folder ID/i)).toBeInTheDocument(),
    );
    await user.type(screen.getByLabelText(/Google Drive Folder ID/i), "abc");
    await user.click(screen.getByTestId("uploads-submit"));

    await waitFor(() => {
      expect(screen.getByTestId("uploads-guidance")).toBeInTheDocument();
    });
    expect(screen.getByText(/Server needs configuration/i)).toBeInTheDocument();
    expect(screen.getByText(/GOOGLE_DRIVE_API_KEY/i)).toBeInTheDocument();
  });

  it("renders the error view on a 502 upstream error", async () => {
    setupFetchMock({
      importResponse: {
        ok: false,
        status: 502,
        body: { error: "Drive listing returned 502" },
      },
    });
    const user = userEvent.setup();
    renderPage();
    await waitFor(() =>
      expect(screen.getByLabelText(/Google Drive Folder ID/i)).toBeInTheDocument(),
    );
    await user.type(screen.getByLabelText(/Google Drive Folder ID/i), "abc");
    await user.click(screen.getByTestId("uploads-submit"));

    await waitFor(() => {
      expect(screen.getByTestId("uploads-error")).toBeInTheDocument();
    });
    expect(screen.getByText(/Drive listing returned 502/i)).toBeInTheDocument();
  });

  it("sends advanced jitter settings when the toggle is expanded", async () => {
    const calls = setupFetchMock({});
    const user = userEvent.setup();
    renderPage();
    await waitFor(() =>
      expect(screen.getByLabelText(/Google Drive Folder ID/i)).toBeInTheDocument(),
    );
    await user.click(screen.getByTestId("uploads-advanced-toggle"));
    const minInput = screen.getByLabelText(/Minimum gap/i);
    const maxInput = screen.getByLabelText(/Maximum gap/i);
    await user.clear(minInput);
    await user.type(minInput, "9000"); // 2.5h
    await user.clear(maxInput);
    await user.type(maxInput, "14400"); // 4h
    await user.type(screen.getByLabelText(/Google Drive Folder ID/i), "abc");
    await user.click(screen.getByTestId("uploads-submit"));

    await waitFor(() => {
      expect(screen.getByTestId("uploads-success")).toBeInTheDocument();
    });
    const body = calls.find((c) =>
      c.url.includes("/api/v1/uploads/batch/by-folder"),
    )!.body as Record<string, unknown>;
    expect(body.min_jitter_seconds).toBe(9000);
    expect(body.max_jitter_seconds).toBe(14400);
  });

  it("sends drive_account_id when a linked Drive account is picked", async () => {
    const calls = setupFetchMock({ includeDrives: true });
    const user = userEvent.setup();
    renderPage();
    await waitFor(() =>
      expect(screen.getByLabelText(/Drive account/i)).toBeInTheDocument(),
    );
    const driveSelect = screen.getByLabelText(/Drive account/i);
    await user.selectOptions(driveSelect, "21"); // first linked drive id
    await user.type(
      screen.getByLabelText(/Google Drive Folder ID/i),
      "privatefolder123",
    );
    await user.click(screen.getByTestId("uploads-submit"));

    await waitFor(() => {
      expect(screen.getByTestId("uploads-success")).toBeInTheDocument();
    });
    const body = calls.find((c) =>
      c.url.includes("/api/v1/uploads/batch/by-folder"),
    )!.body as Record<string, unknown>;
    expect(body.drive_account_id).toBe(21);
  });

  it("shows an empty state with CTA when no Facebook Pages are linked", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        if (url.endsWith("/api/v1/workspaces")) {
          return mockJsonResponse({ workspaces: sampleWorkspaces });
        }
        if (url.endsWith("/api/v1/accounts")) {
          return mockJsonResponse({ accounts: [] });
        }
        return mockJsonResponse({}, false, 404);
      }),
    );
    renderPage();
    await waitFor(() => {
      expect(screen.getByText(/No Facebook Pages connected/i)).toBeInTheDocument();
    });
  });
});
