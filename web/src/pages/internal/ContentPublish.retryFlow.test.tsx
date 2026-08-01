/**
 * Vitest coverage for `ContentPublish` — AuthError navigation, the
 * retryingId lifecycle, retryErrorById keying, and multi-target
 * isolation.
 *
 * Pinned user-spec behaviors:
 *  3. AuthError path — when `retryPostTarget` rejects with `AuthError`,
 *     `useNavigate` is called with `("/login", { replace: true })`
 *     AND `retryErrorById` for that target is NOT set (silent).
 *  4. Refetch-after-success clearing `retryingId` — on retry success,
 *     `retryingId` returns to `null`, so the button text reverts
 *     from "Riprovando…" back to "Riprova pubblicazione".
 *  5. `retryErrorById` keying by `target.id` — per-target error
 *     state is independent; one target's failure surface does NOT
 *     leak into another target's slot.
 *  6. Multi-target isolation — independent `retryingId` + per-target
 *     `retryErrorById` round-trip cleanly with no cross-contamination.
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

import { ApiError, AuthError } from "../../lib/auth";

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

// ── Section 3: AuthError → /login (silent, no retryError set) ──────────

describe("ContentPublish — AuthError → /login", () => {
  it("navigates to /login when retryPostTarget rejects with AuthError", async () => {
    setMockState({
      targets: [makeTarget(1, "failed")],
      status: "ready",
      error: null,
    });
    retryPostTargetMock.mockRejectedValueOnce(new AuthError());

    renderAtPostIdPath(999);
    await act(async () => {
      fireEvent.click(screen.getByTestId("retry-button-1"));
      await Promise.resolve();
    });

    expect(navigateSpy).toHaveBeenCalledTimes(1);
    const [path, opts] = navigateSpy.mock.calls[0] as [string, { replace?: boolean }];
    expect(path).toBe("/login");
    expect(opts?.replace).toBe(true);
  });

  it("does NOT populate retryErrorById on AuthError (silent path)", async () => {
    setMockState({
      targets: [makeTarget(1, "failed")],
      status: "ready",
      error: null,
    });
    retryPostTargetMock.mockRejectedValueOnce(new AuthError());

    renderAtPostIdPath(999);
    await act(async () => {
      fireEvent.click(screen.getByTestId("retry-button-1"));
      await Promise.resolve();
    });

    expect(screen.queryByTestId("retry-error-1")).toBeNull();
  });

  it("surfaces retryErrorById on ApiError for the SAME target", async () => {
    setMockState({
      targets: [makeTarget(1, "failed")],
      status: "ready",
      error: null,
    });
    retryPostTargetMock.mockRejectedValueOnce(
      new ApiError(500, "velox_unreachable"),
    );

    renderAtPostIdPath(999);
    await act(async () => {
      fireEvent.click(screen.getByTestId("retry-button-1"));
      await Promise.resolve();
    });

    expect(navigateSpy).not.toHaveBeenCalled();
    const errEl = screen.getByTestId("retry-error-1");
    expect(errEl.textContent).toContain("velox_unreachable");
  });
});

// ── Section 4: Refetch-after-success clearing retryingId ──────────────

describe("ContentPublish — retryingId lifecycle", () => {
  it("shows 'Riprovando…' during in-flight retry and reverts to 'Riprova pubblicazione' on success", async () => {
    setMockState({
      targets: [makeTarget(1, "failed")],
      status: "ready",
      error: null,
    });
    let resolveRetry: () => void = () => {};
    retryPostTargetMock.mockImplementationOnce(
      () => new Promise<void>((resolve) => { resolveRetry = resolve; }),
    );
    // Manually-deferred promise so we can assert the in-flight label
    // BEFORE the await resolves. We resolve in the same act() block
    // via .then().
    renderAtPostIdPath(999);
    const btn = screen.getByTestId("retry-button-1") as HTMLButtonElement;

    // Click + advance until retry returns (in-flight label visible).
    await act(async () => {
      fireEvent.click(btn);
      // Spin microtasks so React re-renders with retryingId=1.
      await Promise.resolve();
    });

    expect(btn.textContent).toContain("Riprovando");
    expect(btn.disabled).toBe(true);

    // Now resolve the deferred retry → component flips retryingId back to null.
    await act(async () => {
      resolveRetry();
      await Promise.resolve();
    });

    expect(btn.disabled).toBe(false);
    expect(btn.textContent).toContain("Riprova pubblicazione");
    expect(retryPostTargetMock).toHaveBeenCalledTimes(1);
  });
});

// ── Section 5: retryErrorById keying by target.id ─────────────────────

describe("ContentPublish — retryErrorById isolation", () => {
  it("one target's failure surface does NOT leak into another target's slot", async () => {
    setMockState({
      targets: [
        makeTarget(7, "failed"),
        makeTarget(8, "failed"),
      ],
      status: "ready",
      error: null,
    });
    // Target 7 retry → ApiError. Target 8 retry → success.
    retryPostTargetMock.mockImplementation(async (targetId: number) => {
      if (targetId === 7) {
        throw new ApiError(429, "rate_limit_exceeded");
      }
      // target 8 succeeds
    });

    renderAtPostIdPath(999);

    await act(async () => {
      fireEvent.click(screen.getByTestId("retry-button-7"));
      await Promise.resolve();
    });
    expect(screen.getByTestId("retry-error-7").textContent).toContain(
      "rate_limit_exceeded",
    );
    expect(screen.queryByTestId("retry-error-8")).toBeNull();

    await act(async () => {
      fireEvent.click(screen.getByTestId("retry-button-8"));
      await Promise.resolve();
    });
    // After target 8's successful retry, its error slot must remain empty.
    expect(screen.queryByTestId("retry-error-8")).toBeNull();
  });

  it("clearing an existing error slot fires before retry starts", async () => {
    // After a first failure, click retry again — still fails — the
    // previous error remains until the new retry attempt resolves.
    // The contract: setRetryErrorById((p) => { delete p[id]; return p })
    // is inside handleRetry BEFORE the await, so the slot momentarily
    // disappears during the in-flight phase.
    setMockState({
      targets: [makeTarget(1, "failed")],
      status: "ready",
      error: null,
    });

    renderAtPostIdPath(999);

    // 1st retry → ApiError.
    retryPostTargetMock.mockRejectedValueOnce(
      new ApiError(500, "first_failure"),
    );
    await act(async () => {
      fireEvent.click(screen.getByTestId("retry-button-1"));
      await Promise.resolve();
    });
    expect(screen.getByTestId("retry-error-1").textContent).toContain(
      "first_failure",
    );

    // Re-arm retry for a 2nd attempt. The previous error slot is
    // cleared immediately (before await resolves) per the source's
    // pre-clear in handleRetry.
    let resolveRetry: () => void = () => {};
    retryPostTargetMock.mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          resolveRetry = resolve;
        }),
    );
    await act(async () => {
      fireEvent.click(screen.getByTestId("retry-button-1"));
      await Promise.resolve();
    });
    expect(screen.queryByTestId("retry-error-1")).toBeNull();
    // Now resolve the in-flight retry to clean up.
    await act(async () => {
      resolveRetry();
      await Promise.resolve();
    });
  });
});

// ── Section 6: Multi-target isolation (retryingId + retries) ──────────

describe("ContentPublish — multi-target isolation", () => {
  it("two targets can be retried concurrently without cross-contamination of their retry states", async () => {
    setMockState({
      targets: [
        makeTarget(1, "failed"),
        makeTarget(2, "waiting_provider"),
      ],
      status: "ready",
      error: null,
    });
    let resolveFirst!: () => void;
    let resolveSecond!: () => void;
    retryPostTargetMock.mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          resolveFirst = resolve;
        }),
    );
    retryPostTargetMock.mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          resolveSecond = resolve;
        }),
    );

    renderAtPostIdPath(999);
    const btn1 = screen.getByTestId("retry-button-1") as HTMLButtonElement;
    const btn2 = screen.getByTestId("retry-button-2") as HTMLButtonElement;

    // Click btn1, advance microtasks.
    await act(async () => {
      fireEvent.click(btn1);
      await Promise.resolve();
    });
    expect(btn1.textContent).toContain("Riprovando");
    expect(btn2.textContent).toContain("Riprova pubblicazione");
    expect(btn2.disabled).toBe(false);

    // Click btn2 while btn1 is still in-flight.
    await act(async () => {
      fireEvent.click(btn2);
      await Promise.resolve();
    });
    expect(btn1.textContent).toContain("Riprovando");
    expect(btn2.textContent).toContain("Riprovando");

    // Resolve target 1 first.
    await act(async () => {
      resolveFirst();
      await Promise.resolve();
    });
    expect(btn1.textContent).toContain("Riprova pubblicazione");
    expect(btn1.disabled).toBe(false);
    // Target 2 is still in-flight.
    expect(btn2.textContent).toContain("Riprovando");

    // Then target 2.
    await act(async () => {
      resolveSecond();
      await Promise.resolve();
    });
    expect(btn2.textContent).toContain("Riprova pubblicazione");
    expect(btn2.disabled).toBe(false);
  });
});
