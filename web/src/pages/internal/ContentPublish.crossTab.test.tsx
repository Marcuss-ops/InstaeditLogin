/**
 * Vitest coverage for `ContentPublish` — the cross-tab publish
 * broadcast:
 * 10. `dispatchYouTubePublishChanged` is emitted once per published
 *     target after all targets publish, and NOT while any target is
 *     still in-flight.
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
import {
  makeTarget,
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

// ── Section 10: Cross-tab dispatch (bonus) ────────────────────────────

describe("ContentPublish — cross-tab publish-dispatched broadcast", () => {
  it("emits one dispatchYouTubePublishChanged per published target after all targets publish", () => {
    setMockState({
      targets: [makeTarget(11, "published"), makeTarget(22, "published")],
      status: "ready",
      error: null,
    });

    renderAtPostIdPath(999);

    expect(dispatchMock).toHaveBeenCalledTimes(2);
    const calls = dispatchMock.mock.calls as Array<[unknown]>;
    const accountIds = calls
      .map((c) => {
        const arg = c[0] as { account_id: number };
        return arg?.account_id;
      })
      .sort();
    expect(accountIds).toEqual([111, 122]);
  });

  it("does NOT dispatch while any target is not yet published", () => {
    setMockState({
      targets: [
        makeTarget(11, "published"),
        makeTarget(22, "publishing"),
      ],
      status: "ready",
      error: null,
    });

    renderAtPostIdPath(999);

    expect(dispatchMock).not.toHaveBeenCalled();
  });
});
