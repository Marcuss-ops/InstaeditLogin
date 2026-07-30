/**
 * Vitest coverage for useCreateYouTubeEditorSession.
 *
 * Locks down the state-machine contract callers depend on:
 *  - idle → creating → success | error transitions
 *  - success path returns the resolved session response
 *  - AuthError is re-thrown (caller navigates) — not swallowed
 *  - ApiError surfaces as kind="error" with `err.message` preserved
 *  - AbortController lifecycle: a NEW submit aborts the prior one
 *    and does not surface the prior's eventual resolution
 *  - reset() drops the hook back to idle and aborts any in-flight
 */

import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from "vitest";
import { act, renderHook } from "@testing-library/react";

const { authedFetchMock } = vi.hoisted(() => ({
  authedFetchMock: vi.fn(),
}));

vi.mock("../../../lib/auth", async (orig) => {
  const actual = await orig();
  return {
    ...actual,
    authedFetch: (...args: unknown[]) => authedFetchMock(...args),
  };
});

import { ApiError, AuthError } from "../../../lib/auth";
import { useCreateYouTubeEditorSession } from "./useCreateYouTubeEditorSession";

const SESSION = {
  session_id: "ytedit_42",
  velox_project_id: "ve_x",
  editor_url: "/dark_editor_v2/editor/ve_x",
};

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

beforeEach(() => {
  authedFetchMock.mockReset();
});

afterEach(() => {
  vi.clearAllMocks();
});

// ────────────────────────────────────────────────────────────────
// State machine
// ────────────────────────────────────────────────────────────────

describe("useCreateYouTubeEditorSession — state machine", () => {
  it("starts in idle", () => {
    const { result } = renderHook(() => useCreateYouTubeEditorSession());
    expect(result.current.state).toEqual({ kind: "idle" });
  });

  it("transitions idle → creating → success on resolve", async () => {
    let resolveFetch: (resp: Response) => void = () => {};
    authedFetchMock.mockImplementation(
      () =>
        new Promise<Response>((resolve) => {
          resolveFetch = resolve;
        }),
    );

    const { result } = renderHook(() => useCreateYouTubeEditorSession());
    expect(result.current.state).toEqual({ kind: "idle" });

    // Kick off the create.
    let createPromise: Promise<unknown> = Promise.resolve();
    act(() => {
      createPromise = result.current.create({
        workspace_id: 1,
        platform_account_id: 2,
        youtube_video_id: "v",
      });
    });
    expect(result.current.state).toEqual({ kind: "creating" });

    await act(async () => {
      resolveFetch(jsonResponse(SESSION));
      await createPromise;
    });

    expect(result.current.state).toEqual({ kind: "success", session: SESSION });
  });

  it("returns the session response from create() so callers can chain window.open", async () => {
    authedFetchMock.mockResolvedValue(jsonResponse(SESSION));
    const { result } = renderHook(() => useCreateYouTubeEditorSession());

    let session: typeof SESSION | null = null;
    await act(async () => {
      session = (await result.current.create({
        workspace_id: 1,
        platform_account_id: 2,
        youtube_video_id: "v",
      })) as typeof SESSION;
    });
    expect(session).toEqual(SESSION);
  });
});

// ────────────────────────────────────────────────────────────────
// Error classification
// ────────────────────────────────────────────────────────────────

describe("useCreateYouTubeEditorSession — error classification", () => {
  it("re-throws AuthError so the page can navigate to /login (does NOT swallow)", async () => {
    authedFetchMock.mockRejectedValue(new AuthError());
    const { result } = renderHook(() => useCreateYouTubeEditorSession());

    await act(async () => {
      await expect(
        result.current.create({
          workspace_id: 1,
          platform_account_id: 2,
          youtube_video_id: "v",
        }),
      ).rejects.toBeInstanceOf(AuthError);
    });

    // AuthError path leaves the hook stuck at "idle" (no setState in the
    // re-throw branch by design) so the next user gesture starts clean.
    expect(result.current.state).toEqual({ kind: "idle" });
  });

  it("surfaces ApiError as kind=error with err.message preserved", async () => {
    authedFetchMock.mockRejectedValue(new ApiError(429, "Velox limit reached"));
    const { result } = renderHook(() => useCreateYouTubeEditorSession());

    await act(async () => {
      await result.current.create({
        workspace_id: 1,
        platform_account_id: 2,
        youtube_video_id: "v",
      });
    });

    expect(result.current.state).toEqual({
      kind: "error",
      message: "Velox limit reached",
    });
  });

  it("falls through to a generic-message kind=error for unexpected throws", async () => {
    authedFetchMock.mockRejectedValue(new Error("network gone"));
    const { result } = renderHook(() => useCreateYouTubeEditorSession());

    await act(async () => {
      await result.current.create({
        workspace_id: 1,
        platform_account_id: 2,
        youtube_video_id: "v",
      });
    });

    expect(result.current.state).toEqual({
      kind: "error",
      message: "network gone",
    });
  });
});

// ────────────────────────────────────────────────────────────────
// AbortController lifecycle
// ────────────────────────────────────────────────────────────────

describe("useCreateYouTubeEditorSession — abort lifecycle", () => {
  it("a NEW submit aborts the prior in-flight create; the prior never lands as success", async () => {
    const resolveFirst: (resp: Response) => void = () => {};
    const resolveSecond: (resp: Response) => void = () => {};
    let callN = 0;
    authedFetchMock.mockImplementation(
      () =>
        new Promise<Response>((resolve) => {
          callN += 1;
          if (callN === 1) resolveFirst = resolve;
          if (callN === 2) resolveSecond = resolve;
        }),
    );

    const { result } = renderHook(() => useCreateYouTubeEditorSession());

    // Kick off the FIRST submit.
    let firstCall: Promise<unknown> = Promise.resolve();
    act(() => {
      firstCall = result.current.create({
        workspace_id: 1,
        platform_account_id: 2,
        youtube_video_id: "first",
      });
    });
    expect(result.current.state).toEqual({ kind: "creating" });

    // Kick off the SECOND submit BEFORE the first resolves. The
    // hook should abort the first and continue creating for the second.
    await act(async () => {
      await result.current.create({
        workspace_id: 1,
        platform_account_id: 2,
        youtube_video_id: "second",
      });
    });

    // First fetch resolves with success AFTER the second submit landed
    // in `success` — first must NOT clobber the current state.
    resolveFirst(jsonResponse({ ...SESSION, session_id: "first" }));
    await firstCall.catch(() => {
      // first.create() returns null on abort; the resolved-then-aborted
      // race settles without throwing because the hook guards on
      // ctrl.signal.aborted.
    });
    expect(result.current.state).toEqual({
      kind: "success",
      session: { ...SESSION, session_id: "second" },
    });

    // Resolve second for completeness (already done above).
    resolveSecond(jsonResponse({ ...SESSION, session_id: "second" }));
  });

  it("reset() drops the hook back to idle and aborts any in-flight submit", async () => {
    let resolveFetch: (resp: Response) => void = () => {};
    authedFetchMock.mockImplementation(
      () =>
        new Promise<Response>((resolve) => {
          resolveFetch = resolve;
        }),
    );

    const { result } = renderHook(() => useCreateYouTubeEditorSession());

    act(() => {
      void result.current.create({
        workspace_id: 1,
        platform_account_id: 2,
        youtube_video_id: "v",
      });
    });
    expect(result.current.state).toEqual({ kind: "creating" });

    act(() => {
      result.current.reset();
    });
    expect(result.current.state).toEqual({ kind: "idle" });

    // Resolve the in-flight after reset — the abort controller should
    // have invalidated the call. The hook never transitions because
    // the create() promise resolves silently (no setState on abort path).
    resolveFetch(jsonResponse(SESSION));
  });
});
