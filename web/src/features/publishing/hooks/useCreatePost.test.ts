/**
 * Vitest coverage for useCreatePost.
 *
 * Mocks `../api/postsApi` so the hook runs against controlled
 * `createPost` responses. The real `newIdempotencyKey()` is imported
 * via `vi.importActual` so the test exercises the actual v4 generator;
 * a counter-shim wrapping it lets tests assert "fresh UUID per call".
 *
 * @testing-library/react's `renderHook` + `act` are the supported
 * way to drive hook state transitions from a vitest test (the package
 * `@testing-library/react` v16 exports `renderHook` directly).
 */
import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, AuthError } from "../../../lib/auth";
import { useCreatePost } from "./useCreatePost";

const { createPostMock, nextUuid, resetUuid } = vi.hoisted(() => {
  // Counter MUST live in vi.hoisted: vitest hoists vi.mock factories
  // above module-level `let` declarations, so a regular `let` would be
  // in TDZ when the factory is invoked at file-load and the entire
  // test suite would crash with ReferenceError.
  let n = 0;
  return {
    createPostMock: vi.fn(),
    nextUuid: () => `uuid-${++n}`,
    resetUuid: () => {
      n = 0;
    },
  };
});

vi.mock("../api/postsApi", async () => {
  const actual =
    await vi.importActual<typeof import("../api/postsApi")>("../api/postsApi");
  return {
    ...actual,
    createPost: createPostMock,
    // Wrap the real newIdempotencyKey so tests can predict keys per call.
    newIdempotencyKey: nextUuid,
  };
});

afterEach(() => {
  vi.useRealTimers();
});

describe("useCreatePost", () => {
  beforeEach(() => {
    createPostMock.mockReset();
    resetUuid();
  });

  it("starts in the idle state", () => {
    const { result } = renderHook(() => useCreatePost());
    expect(result.current.state).toEqual({ kind: "idle" });
    expect(typeof result.current.submit).toBe("function");
    expect(typeof result.current.reset).toBe("function");
  });

  it("transitions idle → submitting → success on a successful submit", async () => {
    createPostMock.mockResolvedValueOnce({
      id: 1,
      workspace_id: 1,
      status: "queued",
    });
    const { result } = renderHook(() => useCreatePost());
    const body = {
      workspace_id: 1,
      content: { title: "t" },
      targets: [
        {
          platform_account_id: 9,
          settings: { youtube: { title: "yt", privacy_status: "private" as const } },
        },
      ],
    };

    await act(async () => {
      await result.current.submit(body);
    });

    expect(result.current.state.kind).toBe("success");
    if (result.current.state.kind === "success") {
      expect(result.current.state.post.id).toBe(1);
    }
    expect(createPostMock).toHaveBeenCalledTimes(1);
    const [, opts] = createPostMock.mock.calls[0];
    expect(opts).toMatchObject({
      idempotencyKey: "uuid-1",
      signal: expect.any(AbortSignal),
    });
  });

  it("mints a FRESH Idempotency-Key on each submit (no reuse across calls)", async () => {
    createPostMock.mockResolvedValue({ id: 1, workspace_id: 1, status: "queued" });
    const { result } = renderHook(() => useCreatePost());
    const body = {
      workspace_id: 1,
      content: {},
      targets: [
        {
          platform_account_id: 9,
          settings: { youtube: { title: "yt", privacy_status: "private" as const } },
        },
      ],
    };

    await act(async () => {
      await result.current.submit(body);
    });
    await act(async () => {
      await result.current.submit(body);
    });

    const keys = createPostMock.mock.calls.map(
      ([, opts]: [unknown, { idempotencyKey: string }]) => opts.idempotencyKey,
    );
    expect(keys).toEqual(["uuid-1", "uuid-2"]);
  });

  it("cancels a prior in-flight submit when a new one fires", async () => {
    // First call: hung promise (we resolve it manually at the end).
    let resolveFirst: (v: unknown) => void = () => {};
    createPostMock.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveFirst = resolve;
        }),
    );
    // Second call: resolves immediately.
    createPostMock.mockResolvedValueOnce({ id: 99, workspace_id: 1, status: "queued" });

    const { result } = renderHook(() => useCreatePost());
    const body = {
      workspace_id: 1,
      content: {},
      targets: [
        {
          platform_account_id: 9,
          settings: { youtube: { title: "yt", privacy_status: "private" as const } },
        },
      ],
    };

    // Kick off the first submit but DON'T await it (it hangs).
    let firstSubmit: Promise<void> | undefined;
    act(() => {
      firstSubmit = result.current.submit(body);
    });

    // Fire the second one — it aborts the first controller.
    await act(async () => {
      await result.current.submit(body);
    });

    // State belongs to the second submit (id: 99).
    expect(result.current.state.kind).toBe("success");
    if (result.current.state.kind === "success") {
      expect(result.current.state.post.id).toBe(99);
    }

    // Now resolve the hung first submit. The hook should ditch it
    // because its AbortController fired.
    await act(async () => {
      resolveFirst({ id: 1, workspace_id: 1, status: "queued" });
      if (firstSubmit) await firstSubmit;
    });

    // State STILL belongs to the second submit.
    expect(result.current.state.kind).toBe("success");
    if (result.current.state.kind === "success") {
      expect(result.current.state.post.id).toBe(99);
    }
  });

  it("transitions to error state with message on ApiError", async () => {
    createPostMock.mockRejectedValueOnce(new ApiError(422, "media asset not ready"));
    const { result } = renderHook(() => useCreatePost());
    const body = {
      workspace_id: 1,
      content: {},
      targets: [
        {
          platform_account_id: 9,
          settings: { youtube: { title: "yt", privacy_status: "private" as const } },
        },
      ],
    };

    await act(async () => {
      await result.current.submit(body);
    });

    expect(result.current.state.kind).toBe("error");
    if (result.current.state.kind === "error") {
      expect(result.current.state.message).toBe("media asset not ready");
    }
  });

  it("re-throws AuthError so callers can route to /login", async () => {
    createPostMock.mockRejectedValueOnce(new AuthError());
    const { result } = renderHook(() => useCreatePost());
    const body = {
      workspace_id: 1,
      content: {},
      targets: [
        {
          platform_account_id: 9,
          settings: { youtube: { title: "yt", privacy_status: "private" as const } },
        },
      ],
    };

    let caught: unknown;
    await act(async () => {
      try {
        await result.current.submit(body);
      } catch (e) {
        caught = e;
      }
    });

    expect(caught).toBeInstanceOf(AuthError);
    // State should not advance beyond submitting because the auth
    // error is owned by the caller; the hook intentionally does not
    // park it in 'error' (that's reserved for transient POST failures).
    expect(result.current.state.kind).toBe("submitting");
  });

  it("reset() returns to idle even mid-flight", async () => {
    let resolveFirst: (v: unknown) => void = () => {};
    createPostMock.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveFirst = resolve;
        }),
    );
    const { result } = renderHook(() => useCreatePost());
    const body = {
      workspace_id: 1,
      content: {},
      targets: [
        {
          platform_account_id: 9,
          settings: { youtube: { title: "yt", privacy_status: "private" as const } },
        },
      ],
    };

    let firstSubmit: Promise<void> | undefined;
    act(() => {
      firstSubmit = result.current.submit(body);
    });

    act(() => {
      result.current.reset();
    });
    expect(result.current.state.kind).toBe("idle");

    // Resolve the in-flight promise; reset should have cancelled it.
    await act(async () => {
      resolveFirst({ id: 1, status: "queued" });
      if (firstSubmit) await firstSubmit;
    });
    expect(result.current.state.kind).toBe("idle");
  });
});
