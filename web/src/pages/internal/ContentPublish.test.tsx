/**
 * Vitest coverage for `ContentPublish` (the
 * `/app/content/:postId/publish` status-asincrono page).
 *
 * Pinned user-spec behaviors:
 *  1. RETRIABLE_STATUSES gating on per-target button — only
 *     statuses in {`failed`, `retrying`, `waiting_provider`}
 *     render the `Riprova pubblicazione` button. `partially_published`
 *     intentionally does NOT (per the source's RETRIABLE_STATUSES Set).
 *  2. Force flag forwarding — `retryPostTarget` is invoked with
 *     `{ force: forceFlagFor(target.status) }`; forceFlagFor is
 *     `true` ONLY for `waiting_provider`, `false` for `failed` and
 *     `retrying`.
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
 * Plus 3 edge-case rendering paths:
 *  7. Invalid `postId` (non-numeric) → `ErrorState` "postId non valido".
 *  8. Initial loading (`status=loading, targets=[]`) → `Skeleton` cards.
 *  9. Fetch error (`status=error, targets=[]`) → `ErrorState`
 *      "Impossibile leggere lo stato".
 *
 * Mocking strategy: file-level static `vi.mock(...)` factories (no
 * dynamic-imports + `vi.spyOn` — that pattern broke under some
 * vitest ESM loader configurations per the audit doc). All mocks
 * are hoisted via `vi.hoisted` so the `vi.mock` factories can close
 * over the same references used in tests.
 */

import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";

// ── Hoisted mocks ─────────────────────────────────────────────────────
//
// All module-level mocks reference these; `vi.hoisted` ensures the
// references exist before the `vi.mock(...)` factory runs at
// module-initialisation time.
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
import { ApiError, AuthError } from "../../lib/auth";
import type { PostStatus, PostTarget } from "../../features/publishing/api/types";

// ── Fixtures ──────────────────────────────────────────────────────────

function makeTarget(
  id: number,
  status: PostStatus,
  overrides: Partial<PostTarget> = {},
): PostTarget {
  return {
    id,
    post_id: 999,
    platform_account_id: 100 + id,
    status,
    external_id: null,
    public_url: null,
    error_message:
      status === "failed"
        ? "provider refused the upload"
        : status === "partially_published"
          ? "thumbnail ok, privacy update pending"
          : null,
    published_at: status === "published" ? "2030-01-01T00:00:00Z" : null,
    privacy_status: "private",
    made_for_kids: false,
    youtube_sync_status:
      status === "published" ? "confirmed" : "pending",
    actual_privacy: status === "published" ? "unlisted" : null,
    attempt_count: status === "retrying" ? 3 : 0,
    next_attempt_at: status === "retrying" ? "2030-01-01T00:00:00Z" : null,
    ...overrides,
  };
}

interface MockState {
  targets: PostTarget[];
  status: "loading" | "ready" | "error";
  error: string | null;
}

function stateProps(s: MockState) {
  return {
    targets: s.targets,
    status: s.status,
    error: s.error,
    refetch: vi.fn().mockResolvedValue(undefined),
  };
}

function setMockState(s: MockState) {
  usePostTargetStatusMock.mockReturnValue(stateProps(s));
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

function navigationSpyClear() {
  navigateSpy.mockClear();
}

// Helper: render the page inside MemoryRouter at the canonical path.
function renderAtPostIdPath(postId: string | number) {
  return render(
    <MemoryRouter initialEntries={[`/app/content/${postId}/publish`]}>
      <Routes>
        <Route path="/app/content/:postId/publish" element={<ContentPublish />} />
      </Routes>
    </MemoryRouter>,
  );
}

// ── Section 1: RETRIABLE_STATUSES gating on per-target button ─────────

describe("ContentPublish — RETRIABLE_STATUSES gating", () => {
  const RETRIABLE: PostStatus[] = ["failed", "retrying", "waiting_provider"];
  const NON_RETRIABLE: PostStatus[] = [
    "queued",
    "publishing",
    "published",
    "draft",
    "partially_published",
    "dlq",
    "blocked_auth",
  ];

  it("renders retry button ONLY for the three retriable statuses", () => {
    const s: MockState = {
      targets: [
        ...RETRIABLE.map((status) => makeTarget(1, status)),
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
    // Manually-deferred promise so we can assert the in-flight label
    // BEFORE the await resolves. We resolve in the same act() block
    // via .then().
    let resolveRetry: () => void = () => {};
    retryPostTargetMock.mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          resolveRetry = resolve;
        }),
    );

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

    let resolveRetry: () => void = () => {};
    retryPostTargetMock.mockImplementationOnce(
      () =>
        new Promise<void>((resolve) => {
          resolveRetry = resolve;
        }),
    );

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
      <MemoryRouter initialEntries={["/app/content//publish"]}>
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
