import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { LiveStreamNewPage } from "./LiveStreamNew";
import type { LivestreamChannel } from "./livestreamsTypes";

const { uploadMediaAssetMock } = vi.hoisted(() => ({
  uploadMediaAssetMock: vi.fn(),
}));

vi.mock("../../features/publishing/api/mediaApi", () => ({
  uploadMediaAsset: uploadMediaAssetMock,
}));

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

/** Media Library fixture — one ready, one needs_normalization, one unknown. */
function mediaItem(
  id: string,
  compat: "ready" | "needs_normalization" | "unknown",
  overrides: Partial<import("./livestreamsTypes").MediaLibraryItem> = {},
): import("./livestreamsTypes").MediaLibraryItem {
  return {
    id,
    filename: `${id}.mp4`,
    content_type: "video/mp4",
    size_bytes: 1024 * 1024,
    created_at: new Date().toISOString(),
    preview_url: `https://cdn.test/${id}.mp4`,
    duration_seconds: 845,
    width: 1920,
    height: 1080,
    fps: 30,
    has_audio: true,
    video_codec: "h264",
    audio_codec: "aac",
    probed_at: new Date().toISOString(),
    live_compatibility: compat,
    ...overrides,
  };
}

const fixtureMedia = (): import("./livestreamsTypes").MediaLibraryItem[] => [
  mediaItem("video-01", "ready"),
  mediaItem("video-02", "ready", { filename: "video-02.mp4", width: 1280, height: 720, duration_seconds: 500 }),
  mediaItem("video-vfr", "needs_normalization", { fps: 23.976, duration_seconds: 120, width: 854, height: 480 }),
  mediaItem("video-silent", "unknown", { probed_at: null, has_audio: null, duration_seconds: null, width: null, height: null, fps: null }),
];

function mockFetchWithMedia(channels: LivestreamChannel[], media: import("./livestreamsTypes").MediaLibraryItem[], mediaError = false) {
  return vi.fn().mockImplementation(async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
    if (url.endsWith("/api/v1/auth/me")) {
      return mockJsonResponse({ workspace_id: 7 });
    }
    if (url.includes("/api/v1/livestreams/channels")) {
      return mockJsonResponse({ channels });
    }
    if (url.includes("/api/v1/media")) {
      if (mediaError) return mockJsonResponse({ error: "boom" }, false, 500);
      return mockJsonResponse({ items: media });
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

/** jsdom lacks createObjectURL/revokeObjectURL — patch the methods on
 * the real URL global (keeping `new URL(...)` intact) for the upload test. */
function stubObjectURL() {
  URL.createObjectURL = vi.fn(() => "blob:mock-cover") as unknown as typeof URL.createObjectURL;
  URL.revokeObjectURL = vi.fn() as unknown as typeof URL.revokeObjectURL;
}

describe("LiveStreamNewPage — step 1 (Canale)", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    uploadMediaAssetMock.mockReset();
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

describe("LiveStreamNewPage — step 2 (Configurazione YouTube)", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    uploadMediaAssetMock.mockReset();
  });

  /** Select channel 42 and click Continua → step 2. */
  async function advanceToStep2() {
    vi.stubGlobal("fetch", mockFetch(fixtureChannels()));
    renderPage();
    await waitFor(() => {
      expect(screen.getByTestId("livestream-new-channel-42")).toBeInTheDocument();
    });
    await userEvent.click(screen.getByTestId("livestream-new-channel-42"));
    await userEvent.click(screen.getByTestId("livestream-new-continue"));
    await waitFor(() => {
      expect(screen.getByTestId("livestream-new-step2")).toBeInTheDocument();
    });
  }

  it("advances from step 1 and renders the configuration form", async () => {
    await advanceToStep2();

    expect(screen.getByText(/Passaggio 2 di 5/)).toBeInTheDocument();
    expect(screen.getByTestId("livestream-new-step-badge-2")).toBeInTheDocument();
    expect(screen.getByTestId("ls-step2-title")).toBeInTheDocument();
    expect(screen.getByTestId("ls-step2-description")).toBeInTheDocument();

    // Privacy options
    expect(screen.getByTestId("ls-step2-privacy-private")).toBeInTheDocument();
    expect(screen.getByTestId("ls-step2-privacy-unlisted")).toBeInTheDocument();
    expect(screen.getByTestId("ls-step2-privacy-public")).toBeInTheDocument();

    // Category / language / latency selects
    expect(screen.getByTestId("ls-step2-category")).toBeInTheDocument();
    expect(screen.getByTestId("ls-step2-language")).toBeInTheDocument();
    expect(screen.getByTestId("ls-step2-latency")).toBeInTheDocument();

    // Toggles
    expect(screen.getByTestId("ls-step2-kids-toggle")).toBeInTheDocument();
    expect(screen.getByTestId("ls-step2-dvr-toggle")).toBeInTheDocument();
    expect(screen.getByTestId("ls-step2-auto-start-toggle")).toBeInTheDocument();
    expect(screen.getByTestId("ls-step2-auto-stop-toggle")).toBeInTheDocument();

    // Cover sources
    expect(screen.getByTestId("ls-step2-cover-source-upload")).toBeInTheDocument();
    expect(screen.getByTestId("ls-step2-cover-source-library")).toBeInTheDocument();
    expect(screen.getByTestId("ls-step2-cover-source-dark")).toBeInTheDocument();
  });

  it("keeps Continua blocked until a title is entered", async () => {
    await advanceToStep2();

    expect(screen.getByTestId("livestream-new-continue")).toBeDisabled();
    expect(screen.getByText(/Inserisci un titolo per continuare/)).toBeInTheDocument();

    await userEvent.type(screen.getByTestId("ls-step2-title"), "WWE News 24/7");
    expect(screen.getByTestId("livestream-new-continue")).toBeEnabled();
  });

  it("preserves the channel selection and form entries across back → forward", async () => {
    await advanceToStep2();
    await userEvent.type(screen.getByTestId("ls-step2-title"), "WWE News 24/7");
    await userEvent.selectOptions(screen.getByTestId("ls-step2-category"), "24");
    await userEvent.selectOptions(screen.getByTestId("ls-step2-language"), "it");
    await userEvent.click(screen.getByTestId("ls-step2-dvr-toggle"));

    await userEvent.click(screen.getByTestId("livestream-new-step2-back"));
    expect(screen.getByTestId("livestream-new-step-badge-1")).toBeInTheDocument();
    expect(screen.getByText("Canale selezionato: WWE Insider Italia")).toBeInTheDocument();

    await userEvent.click(screen.getByTestId("livestream-new-continue"));
    await waitFor(() => {
      expect(screen.getByTestId("livestream-new-step2")).toBeInTheDocument();
    });
    expect(screen.getByTestId("ls-step2-title")).toHaveValue("WWE News 24/7");
    expect(screen.getByTestId("ls-step2-category")).toHaveValue("24");
    expect(screen.getByTestId("ls-step2-language")).toHaveValue("it");
    expect(screen.getByTestId("ls-step2-dvr-toggle")).toHaveAttribute("aria-checked", "true");
  });

  it("lets the user pick privacy and flip the broadcast toggles", async () => {
    await advanceToStep2();

    await userEvent.click(screen.getByTestId("ls-step2-privacy-public"));
    expect(screen.getByTestId("ls-step2-privacy-public")).toHaveAttribute("aria-checked", "true");
    expect(screen.getByTestId("ls-step2-privacy-unlisted")).toHaveAttribute("aria-checked", "false");

    await userEvent.click(screen.getByTestId("ls-step2-auto-start-toggle"));
    expect(screen.getByTestId("ls-step2-auto-start-toggle")).toHaveAttribute("aria-checked", "true");
  });

  it("uploads a cover image through the presign pipeline and shows a preview", async () => {
    stubObjectURL();
    uploadMediaAssetMock.mockResolvedValue({ id: "asset-cover-1" });
    await advanceToStep2();

    const file = new File(["fake-image-bytes"], "cover.png", { type: "image/png" });
    await userEvent.upload(screen.getByTestId("ls-step2-cover-file"), file);

    await waitFor(() => {
      expect(screen.getByTestId("ls-step2-cover-preview")).toBeInTheDocument();
    }, { timeout: 10000 });
    expect(uploadMediaAssetMock).toHaveBeenCalledTimes(1);
    const [, options] = uploadMediaAssetMock.mock.calls[0];
    expect(options.contentType).toBe("image/png");
    expect(options.sha256).toMatch(/^[0-9a-f]{64}$/);
    expect(screen.getByText("Copertina pronta")).toBeInTheDocument();

    // Removing the cover clears the selection.
    await userEvent.click(screen.getByTestId("ls-step2-cover-remove"));
    expect(screen.getByTestId("ls-step2-cover-upload")).toBeInTheDocument();
    expect(URL.revokeObjectURL).toHaveBeenCalled();
  }, 15000);

  it("rejects non-image cover files with a toast", async () => {
    stubObjectURL();
    await advanceToStep2();

    const file = new File(["fake"], "video.mp4", { type: "video/mp4" });
    await userEvent.upload(screen.getByTestId("ls-step2-cover-file"), file);

    await waitFor(() => {
      expect(uploadMediaAssetMock).not.toHaveBeenCalled();
    });
    expect(screen.getByTestId("ls-step2-cover-upload")).toBeInTheDocument();
  });

  it("renders the Media Library and Dark Editor sources as in-arrivo panels", async () => {
    await advanceToStep2();

    await userEvent.click(screen.getByTestId("ls-step2-cover-source-library"));
    expect(screen.getByTestId("ls-step2-cover-library")).toBeInTheDocument();
    expect(screen.getByText(/selettore della Media Library arriva con la prossima release/)).toBeInTheDocument();

    await userEvent.click(screen.getByTestId("ls-step2-cover-source-dark"));
    expect(screen.getByTestId("ls-step2-cover-dark")).toBeInTheDocument();
    expect(screen.getByText(/arriva nel secondo rilascio delle live/)).toBeInTheDocument();
  });
});

describe("LiveStreamNewPage — step 3 (Contenuti / Media Library)", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    uploadMediaAssetMock.mockReset();
  });

  /** Select channel 42 → step 2 → title → Continua → step 3. */
  async function advanceToStep3(media: import("./livestreamsTypes").MediaLibraryItem[] = fixtureMedia(), mediaError = false) {
    vi.stubGlobal("fetch", mockFetchWithMedia(fixtureChannels(), media, mediaError));
    renderPage();
    await waitFor(() => {
      expect(screen.getByTestId("livestream-new-channel-42")).toBeInTheDocument();
    });
    await userEvent.click(screen.getByTestId("livestream-new-channel-42"));
    await userEvent.click(screen.getByTestId("livestream-new-continue"));
    await waitFor(() => {
      expect(screen.getByTestId("livestream-new-step2")).toBeInTheDocument();
    });
    await userEvent.type(screen.getByTestId("ls-step2-title"), "WWE News 24/7");
    await userEvent.click(screen.getByTestId("livestream-new-continue"));
    await waitFor(() => {
      expect(screen.getByTestId("livestream-new-step3")).toBeInTheDocument();
    });
  }

  it("renders the Media Library with metadata and compatibility badges", async () => {
    await advanceToStep3();

    expect(screen.getByText(/Passaggio 3 di 5/)).toBeInTheDocument();
    expect(screen.getByTestId("livestream-new-step-badge-3")).toBeInTheDocument();
    expect(screen.getByTestId("ls-step3-list")).toBeInTheDocument();

    expect(screen.getByText("video-01.mp4")).toBeInTheDocument();
    expect(screen.getByText("video-02.mp4")).toBeInTheDocument();
    expect(screen.getByText("video-vfr.mp4")).toBeInTheDocument();
    expect(screen.getByText("video-silent.mp4")).toBeInTheDocument();

    // Metadata: duration, resolution, FPS, audio.
    expect(screen.getByText("14:05")).toBeInTheDocument(); // 845s
    expect(screen.getByText("8:20")).toBeInTheDocument(); // 500s (720p row)
    expect(screen.getByText("1920×1080")).toBeInTheDocument();
    expect(screen.getByText("1280×720")).toBeInTheDocument();
    expect(screen.getByText("854×480")).toBeInTheDocument();
    expect(screen.getAllByText("30 fps")).toHaveLength(2);
    expect(screen.getByText("23.98 fps")).toBeInTheDocument();
    expect(screen.getAllByText("Sì").length).toBeGreaterThanOrEqual(3);

    // Compatibility badges: 2 ready, 1 needs_normalization, 1 unknown.
    expect(screen.getAllByText("Pronto per live")).toHaveLength(2);
    expect(screen.getByText("Da normalizzare")).toBeInTheDocument();
    expect(screen.getByText("Da verificare")).toBeInTheDocument();
    expect(screen.getAllByText(/Bloccato — da normalizzare/)).toHaveLength(2);
  });

  it("keeps Continua blocked until a compatible video is selected", async () => {
    await advanceToStep3();

    expect(screen.getByTestId("livestream-new-continue")).toBeDisabled();
    expect(screen.getByText(/Seleziona almeno un video compatibile/)).toBeInTheDocument();

    // Incompatible rows cannot be selected.
    await userEvent.click(screen.getByTestId("ls-step3-item-video-vfr"));
    expect(screen.getByTestId("livestream-new-continue")).toBeDisabled();
    expect(screen.queryByTestId("ls-step3-order-video-vfr")).not.toBeInTheDocument();

    await userEvent.click(screen.getByTestId("ls-step3-item-video-01"));
    expect(screen.getByTestId("livestream-new-continue")).toBeEnabled();
    expect(screen.getByTestId("ls-step3-order-video-01")).toBeInTheDocument();
    expect(screen.getByText("1 video selezionato")).toBeInTheDocument();
  });

  it("keeps selection ordered and lets the user deselect", async () => {
    await advanceToStep3();

    await userEvent.click(screen.getByTestId("ls-step3-item-video-01"));
    await userEvent.click(screen.getByTestId("ls-step3-item-video-02"));
    expect(screen.getByTestId("ls-step3-order-video-01")).toHaveTextContent("1");
    expect(screen.getByTestId("ls-step3-order-video-02")).toHaveTextContent("2");
    expect(screen.getByText("2 video selezionati")).toBeInTheDocument();

    // Deselect the first → the second becomes position 1 (no gap).
    await userEvent.click(screen.getByTestId("ls-step3-item-video-01"));
    expect(screen.queryByTestId("ls-step3-order-video-01")).not.toBeInTheDocument();
    expect(screen.getByTestId("ls-step3-order-video-02")).toHaveTextContent("1");
  });

  it("preserves the media selection across back → forward", async () => {
    await advanceToStep3();
    await userEvent.click(screen.getByTestId("ls-step3-item-video-02"));

    await userEvent.click(screen.getByTestId("livestream-new-step3-back"));
    expect(screen.getByTestId("livestream-new-step-badge-2")).toBeInTheDocument();
    expect(screen.getByTestId("ls-step2-title")).toHaveValue("WWE News 24/7");

    await userEvent.click(screen.getByTestId("livestream-new-continue"));
    await waitFor(() => {
      expect(screen.getByTestId("livestream-new-step3")).toBeInTheDocument();
    });
    expect(screen.getByTestId("ls-step3-order-video-02")).toHaveTextContent("1");
    expect(screen.getByText("1 video selezionato")).toBeInTheDocument();
  });

  it("shows the empty state when the Media Library has no videos", async () => {
    await advanceToStep3([]);
    expect(screen.getByText("Nessun video nella Media Library")).toBeInTheDocument();
    expect(screen.getByTestId("livestream-new-continue")).toBeDisabled();
  });

  it("shows an error state with retry when the Media Library cannot load", async () => {
    await advanceToStep3(fixtureMedia(), true);
    expect(screen.getByTestId("ls-step3-retry")).toBeInTheDocument();
    expect(screen.getByTestId("livestream-new-continue")).toBeDisabled();
  });
});
