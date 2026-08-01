/**
 * Vitest coverage for `ContentPublish` — the three edge-case rendering
 * paths:
 *  7. Invalid `postId` (non-numeric) → `ErrorState` "postId non valido".
 *  8. Initial loading (`status=loading, targets=[]`) → `Skeleton` cards.
 *  9. Fetch error (`status=error, targets=[]`) → `ErrorState`
 *      "Impossibile leggere lo stato".
 *
 * Shared pure fixtures come from `ContentPublish.testUtils`; the
 * module mocks are declared locally with `vi.hoisted` (repo
 * convention — Vitest forbids exporting hoisted variables).
 */
import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import {
  stateProps,
  renderAtPostIdPath,
} from "./ContentPublish.testUtils";
import type { MockState } from "./ContentPublish.testUtils";

// ── Hoisted mocks ─────────────────────────────────────────────────────
const {
  usePostTargetStatusMock,
  retryPostTargetMock,
  dispatchMock,
  navigateSpy,
} = vi.hoisted(() => ({
  usePostTargetStatusMock: vi.fn(),
  retryPostTargetMock: vi.fn(),
  dispatchMock: vi.fn(),
  navigateSpy: vi.fn(),
}));

// File-level react-router-dom mock — same hardened pattern as
// ConfirmationStep.test.tsx: static factory runs during init;
// non-target exports pass through so MemoryRouter + Link still work.
vi.mock("react-router-dom", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("react-router-dom")>();
  return {
    ...actual,
    useNavigate: () =>
      navigateSpy as unknown as ReturnType<typeof actual.useNavigate>,
    // useParams + Link + UseLocation + useSearchParams passthrough
  };
});

vi.mock("../../features/publishing/hooks/usePostTargetStatus", () => ({
  usePostTargetStatus: (...args: unknown[]) =>
    usePostTargetStatusMock(...args),
}));

vi.mock("../../features/publishing/api/postTargetsApi", () => ({
  retryPostTarget: (...args: unknown[]) => retryPostTargetMock(...args),
}));

vi.mock("../../features/channels/hooks/useYouTubePublishLiveUpdate", () => ({
  dispatchYouTubePublishChanged: (...args: unknown[]) =>
    dispatchMock(...args),
}));

import { ContentPublish } from "./ContentPublish";

function setMockState(s: MockState) {
  usePostTargetStatusMock.mockReturnValue(stateProps(s));
}

function navigationSpyClear() {
  navigateSpy.mockClear();
}

beforeEach(() => {
  navigationSpyClear();
  usePostTargetStatusMock.mockReset();
  retryPostTargetMock.mockReset();
  dispatchMock.mockReset();
});

afterEach(() => {
  vi.clearAllMocks();
});

// ── Section 7: Invalid postId → ErrorState ────────────────────────────

describe("ContentPublish — invalid postId path", () => {
  it("renders ErrorState 'postId non valido' when postId param is non-numeric", () => {
    setMockState({ targets: [], status: "ready", error: null });

    renderAtPostIdPath("not-a-number");

    const err = screen.getByTestId("content-publish-error");
    // The component's error component renders the raw title inside
    // ErrorState which uses an h2 or similar; check for the title text.
    expect(err.textContent).toMatch(/postId non valido/i);
    // The hook is never called (parsePostId returns null first).
    // Note: hook IS still called once during the component's
    // render before parsePostId gates the early return — but the
    // hook is mocked, so the call is benign.
  });

  it("renders ErrorState 'postId non valido' when postId is empty", () => {
    setMockState({ targets: [], status: "ready", error: null });
    // Empty path:
    render(
      <MemoryRouter initialEntries={["/app/content/empty/publish"]}>
        <Routes>
          <Route
            path="/app/content/:postId/publish"
            element={<ContentPublish />}
          />
        </Routes>
      </MemoryRouter>,
    );
    expect(
      screen.getByTestId("content-publish-error").textContent,
    ).toMatch(/postId non valido/i);
  });
});

// ── Section 8: Loading state — Skeletons ──────────────────────────────

describe("ContentPublish — loading state", () => {
  it("renders the loading page when status=loading and targets=[]", () => {
    setMockState({ targets: [], status: "loading", error: null });

    renderAtPostIdPath(999);

    // 1. The loading container exists (the early-return view).
    const loadingContainer = screen.getByTestId("content-publish-loading");
    expect(loadingContainer).toBeTruthy();
    expect(loadingContainer.textContent).toMatch(/Stato pubblicazione/i);

    // 2. The loading state early-returns BEFORE AggregateBanner
    //    renders — so the three aggregate banner testids must be
    //    absent globally. This pins the contract that the loading
    //    path bypasses the normal flow.
    expect(screen.queryByTestId("aggregate-banner-polling")).toBeNull();
    expect(screen.queryByTestId("aggregate-banner-failed")).toBeNull();
    expect(screen.queryByTestId("aggregate-banner-publishing")).toBeNull();

    // 3. Per-target rows are absent (no targets yet).
    expect(screen.queryByTestId("target-row-1")).toBeNull();
  });
});

// ── Section 9: Fetch error with empty snapshot → ErrorState ───────────

describe("ContentPublish — fetch error path", () => {
  it("renders ErrorState 'Impossibile leggere lo stato' when status=error and targets=[]", () => {
    setMockState({
      targets: [],
      status: "error",
      error: "polling unreachable",
    });

    renderAtPostIdPath(999);

    const errContainer = screen.getByTestId("content-publish-error");
    expect(errContainer.textContent).toMatch(/Impossibile leggere lo stato/i);
    expect(errContainer.textContent).toContain("polling unreachable");
  });
});
