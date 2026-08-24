/**
 * Vitest coverage for `ContentPublish` — RETRIABLE_STATUSES gating on
 * the per-target retry button + force-flag forwarding.
 *
 * Pinned user-spec behaviors:
 *  1. RETRIABLE_STATUSES gating on per-target button — only
 *     statuses in {`failed`, `retrying`, `waiting_provider`,
 *     `partially_published`} render the retry button so creators can
 *     repair only failed destinations.
 *  2. Force flag forwarding — `retryPostTarget` is invoked with
 *     `{ force: forceFlagFor(target.status) }`; forceFlagFor is
 *     `true` for `waiting_provider` and `partially_published`,
 *     `false` for `failed` and `retrying`.
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
import { act, fireEvent, screen } from "@testing-library/react";
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

import type { PostStatus } from "../../features/publishing/api/types";

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

// ── Section 1: RETRIABLE_STATUSES gating on per-target button ─────────

describe("ContentPublish — RETRIABLE_STATUSES gating", () => {
  const RETRIABLE: PostStatus[] = ["failed", "retrying", "waiting_provider", "partially_published"];
  const NON_RETRIABLE: PostStatus[] = [
    "queued",
    "publishing",
    "published",
    "draft",
    "dlq",
  ];

  it("renders retry button only for retriable statuses", () => {
    const s: MockState = {
      targets: [
        ...RETRIABLE.map((status, i) => makeTarget(i + 1, status)),
        ...NON_RETRIABLE.map((status, i) => makeTarget(10 + i, status)),
      ],
      status: "ready",
      error: null,
    };
    setMockState(s);

    renderAtPostIdPath(999);

    // retriable → button rendered
    for (const t of s.targets.filter((x) => RETRIABLE.includes(x.status))) {
      expect(
        screen.getByTestId(`retry-button-${t.id}`),
        `expected retry button on target ${t.id} (status=${t.status})`,
      ).toBeTruthy();
    }

    // non-retriable → button must NOT exist by id (excluding the `retry-error-${id}` testid)
    for (const t of s.targets.filter((x) => !RETRIABLE.includes(x.status))) {
      expect(
        screen.queryByTestId(`retry-button-${t.id}`),
        `expected NO retry button on target ${t.id} (status=${t.status})`,
      ).toBeNull();
    }
  });

  it("labels the waiting_provider button 'Riprova pubblicazione' (force flag path)", () => {
    setMockState({
      targets: [makeTarget(1, "waiting_provider")],
      status: "ready",
      error: null,
    });
    renderAtPostIdPath(999);
    const btn = screen.getByTestId("retry-button-1");
    expect(btn.textContent).toContain("Riprova pubblicazione");
    expect(btn.hasAttribute("disabled")).toBe(false);
  });
});

// ── Section 2: Force flag forwarding ──────────────────────────────────

describe("ContentPublish — force flag forwarding", () => {
  it("forwards { force: true } when retrying a waiting_provider target", async () => {
    setMockState({
      targets: [makeTarget(1, "waiting_provider")],
      status: "ready",
      error: null,
    });
    retryPostTargetMock.mockResolvedValueOnce(undefined);

    renderAtPostIdPath(999);
    await act(async () => {
      fireEvent.click(screen.getByTestId("retry-button-1"));
    });

    expect(retryPostTargetMock).toHaveBeenCalledTimes(1);
    const [targetId, opts] = retryPostTargetMock.mock.calls[0] as [
      number,
      { force: boolean },
    ];
    expect(targetId).toBe(1);
    expect(opts).toEqual({ force: true });
  });

  it("forwards { force: true } when retrying a partially published target", async () => {
    setMockState({
      targets: [makeTarget(1, "partially_published")],
      status: "ready",
      error: null,
    });
    retryPostTargetMock.mockResolvedValueOnce(undefined);

    renderAtPostIdPath(999);
    await act(async () => {
      fireEvent.click(screen.getByTestId("retry-button-1"));
    });

    expect(retryPostTargetMock).toHaveBeenCalledWith(1, { force: true });
  });

  it("forwards { force: false } when retrying a failed target", async () => {
    setMockState({
      targets: [makeTarget(2, "failed")],
      status: "ready",
      error: null,
    });
    retryPostTargetMock.mockResolvedValueOnce(undefined);

    renderAtPostIdPath(999);
    await act(async () => {
      fireEvent.click(screen.getByTestId("retry-button-2"));
    });

    const [, opts] = retryPostTargetMock.mock.calls[0] as [
      number,
      { force: boolean },
    ];
    expect(opts).toEqual({ force: false });
  });

  it("forwards { force: false } when retrying a retrying target", async () => {
    setMockState({
      targets: [makeTarget(3, "retrying")],
      status: "ready",
      error: null,
    });
    retryPostTargetMock.mockResolvedValueOnce(undefined);

    renderAtPostIdPath(999);
    await act(async () => {
      fireEvent.click(screen.getByTestId("retry-button-3"));
    });

    const [, opts] = retryPostTargetMock.mock.calls[0] as [
      number,
      { force: boolean },
    ];
    expect(opts).toEqual({ force: false });
  });
});
