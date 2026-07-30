import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { DriveBatchImportDialog } from "./DriveBatchImportDialog";

function mockJsonResponse(data: unknown, ok = true, status = 200) {
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

function setupFetchMock(opts: {
  importResponse?: { ok: boolean; body: unknown; status?: number };
}) {
  const importResponse = opts.importResponse ?? {
    ok: true,
    body: {
      folder_id: "abc123",
      scheduled_count: 3,
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
      next_page_token: "",
      note: "",
      cursor_clamped_to_now: false,
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
        typeof input === "string"
          ? input
          : (input as { url: string }).url;
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
        return mockJsonResponse({ accounts: samplePages });
      }
      if (url.includes("/api/v1/media/import/drive/folder")) {
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

function renderDialog(props: Partial<React.ComponentProps<typeof DriveBatchImportDialog>> = {}) {
  return render(
    <MemoryRouter>
      <DriveBatchImportDialog open={true} onClose={() => {}} {...props} />
    </MemoryRouter>,
  );
}

describe("DriveBatchImportDialog", () => {
  beforeEach(() => {
    vi.resetAllMocks();
  });

  it("renders the dialog title and form fields on open", async () => {
    setupFetchMock({});
    renderDialog();
    await waitFor(() => {
      expect(screen.getByText(/Auto-post my Drive folder/i)).toBeInTheDocument();
    });
    expect(screen.getByLabelText(/Workspace/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/Facebook Page/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/Google Drive Folder ID/i)).toBeInTheDocument();
  });

  it("does not render when closed", () => {
    setupFetchMock({});
    renderDialog({ open: false });
    expect(
      screen.queryByText(/Auto-post my Drive folder/i),
    ).not.toBeInTheDocument();
  });

  it("shows a friendly error when no Facebook Pages are linked", async () => {
    setupFetchMock({});
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async (input: RequestInfo) => {
        const url = typeof input === "string" ? input : input.url;
        if (url.endsWith("/api/v1/workspaces")) {
          return mockJsonResponse({ workspaces: sampleWorkspaces });
        }
        if (url.endsWith("/api/v1/accounts")) {
          return mockJsonResponse({ accounts: [] }); // no pages
        }
        return mockJsonResponse({}, false, 404);
      }),
    );
    renderDialog();
    await waitFor(() => {
      expect(
        screen.getByText(/No Facebook Pages connected/i),
      ).toBeInTheDocument();
    });
  });

  it("blocks submit when the folder ID is malformed", async () => {
    const calls = setupFetchMock({});
    const user = userEvent.setup();
    renderDialog();
    await waitFor(() =>
      expect(screen.getByLabelText(/Google Drive Folder ID/i)).toBeInTheDocument(),
    );
    // Workspace + Page pre-selected (single option each). Paste a bad id.
    await user.type(
      screen.getByLabelText(/Google Drive Folder ID/i),
      "bad id with spaces",
    );
    expect(
      screen.getByText(/must be 1–100 letters, digits, hyphens, or underscores/i),
    ).toBeInTheDocument();
    const submit = screen.getByTestId("drive-batch-submit") as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
    // No import call should have been issued.
    const importCalls = calls.filter((c) =>
      c.url.includes("/api/v1/media/import/drive/folder"),
    );
    expect(importCalls).toHaveLength(0);
  });

  it("happy path: submits with correct payload and renders success view", async () => {
    const calls = setupFetchMock({});
    const user = userEvent.setup();
    renderDialog();
    await waitFor(() =>
      expect(screen.getByLabelText(/Google Drive Folder ID/i)).toBeInTheDocument(),
    );
    await user.type(
      screen.getByLabelText(/Google Drive Folder ID/i),
      "1HregS58okcSoe8597qdXgpZM6K4CwEBD",
    );
    await user.click(screen.getByTestId("drive-batch-submit"));

    await waitFor(() => {
      expect(screen.getByTestId("drive-batch-success")).toBeInTheDocument();
    });
    expect(
      screen.getByText(/3 videos scheduled/i),
    ).toBeInTheDocument();

    const importCall = calls.find(
      (c) => c.url.includes("/api/v1/media/import/drive/folder"),
    );
    expect(importCall).toBeDefined();
    expect(importCall!.method).toBe("POST");
    const body = importCall!.body as Record<string, unknown>;
    expect(body.folder_id).toBe("1HregS58okcSoe8597qdXgpZM6K4CwEBD");
    expect(body.workspace_id).toBe(47);
    expect(body.facebook_account_id).toBe(9);
    // No jitter — by default the advanced toggle is off so we don't send.
    expect(body.min_jitter_seconds).toBeUndefined();
    expect(body.max_jitter_seconds).toBeUndefined();
  });

  it("happy path with cursor clamp: shows the warning copy", async () => {
    setupFetchMock({
      importResponse: {
        ok: true,
        body: {
          folder_id: "abc",
          scheduled_count: 2,
          first_publish_at: "2026-07-17T16:00:00Z",
          last_scheduled_at: "2026-07-17T20:00:00Z",
          entries: [],
          next_page_token: "",
          note: "",
          cursor_clamped_to_now: true,
          needs_google_drive_api_key: false,
          needs_drive_account: false,
          error: "",
        },
      },
    });
    const user = userEvent.setup();
    renderDialog();
    await waitFor(() =>
      expect(screen.getByLabelText(/Google Drive Folder ID/i)).toBeInTheDocument(),
    );
    await user.type(screen.getByLabelText(/Google Drive Folder ID/i), "abc");
    await user.click(screen.getByTestId("drive-batch-submit"));
    await waitFor(() =>
      expect(screen.getByText(/Cursor was too far in the past/i)).toBeInTheDocument(),
    );
  });

  it("renders the guidance view when the server reports missing API key", async () => {
    setupFetchMock({
      importResponse: {
        ok: true,
        body: {
          folder_id: "abc",
          scheduled_count: 0,
          first_publish_at: "",
          last_scheduled_at: "",
          entries: [],
          next_page_token: "",
          note:
            "Server is missing GOOGLE_DRIVE_API_KEY. Either set the env var OR pass a drive_account_id to use your linked Drive.",
          cursor_clamped_to_now: false,
          needs_google_drive_api_key: true,
          needs_drive_account: true,
          error: "",
        },
      },
    });
    const user = userEvent.setup();
    renderDialog();
    await waitFor(() =>
      expect(screen.getByLabelText(/Google Drive Folder ID/i)).toBeInTheDocument(),
    );
    await user.type(screen.getByLabelText(/Google Drive Folder ID/i), "abc");
    await user.click(screen.getByTestId("drive-batch-submit"));

    await waitFor(() => {
      expect(screen.getByTestId("drive-batch-guidance")).toBeInTheDocument();
    });
    expect(screen.getByText(/Server needs configuration/i)).toBeInTheDocument();
    expect(screen.getByText(/GOOGLE_DRIVE_API_KEY/i)).toBeInTheDocument();
  });

  it("renders the error view when the server returns a non-2xx with an error body", async () => {
    setupFetchMock({
      importResponse: {
        ok: false,
        status: 422,
        body: { error: "min_jitter_seconds must be <= max_jitter_seconds" },
      },
    });
    const user = userEvent.setup();
    renderDialog();
    await waitFor(() =>
      expect(screen.getByLabelText(/Google Drive Folder ID/i)).toBeInTheDocument(),
    );
    await user.type(screen.getByLabelText(/Google Drive Folder ID/i), "abc");
    await user.click(screen.getByTestId("drive-batch-submit"));

    await waitFor(() => {
      expect(screen.getByTestId("drive-batch-error")).toBeInTheDocument();
    });
    expect(
      screen.getByText(/min_jitter_seconds must be <= max_jitter_seconds/i),
    ).toBeInTheDocument();
  });

  it("sends advanced jitter settings when the toggle is expanded", async () => {
    const calls = setupFetchMock({});
    const user = userEvent.setup();
    renderDialog();
    await waitFor(() =>
      expect(screen.getByLabelText(/Google Drive Folder ID/i)).toBeInTheDocument(),
    );
    await user.click(screen.getByTestId("drive-batch-advanced-toggle"));
    const minInput = screen.getByLabelText(/Minimum gap/i);
    const maxInput = screen.getByLabelText(/Maximum gap/i);
    await user.clear(minInput);
    await user.type(minInput, "120");
    await user.clear(maxInput);
    await user.type(maxInput, "240");
    await user.type(screen.getByLabelText(/Google Drive Folder ID/i), "abc");
    await user.click(screen.getByTestId("drive-batch-submit"));

    await waitFor(() => {
      expect(screen.getByTestId("drive-batch-success")).toBeInTheDocument();
    });
    const body = calls.find(
      (c) => c.url.includes("/api/v1/media/import/drive/folder"),
    )!.body as Record<string, unknown>;
    // 120 minutes × 60 = 7200 seconds (2h); 240 × 60 = 14400 (4h).
    expect(body.min_jitter_seconds).toBe(120 * 60);
    expect(body.max_jitter_seconds).toBe(240 * 60);
  });
});
