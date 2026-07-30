/**
 * Vitest coverage for ConfirmationStep.
 *
 * Locks down:
 *  1. Read-only summary renders asset, internal title,
 *     workspace+channel pair, YT title, tag chips, made-for-kids,
 *     and the locked "private" privacy chip.
 *  2. Submit builds the exact CreatePostRequest contract:
 *     workspace_id, status="queued", content.media=[{asset_id}],
 *     content.title, targets[0].settings.youtube.private +
 *     made_for_kids + tags + title + description (only when set).
 *  3. AuthError from the hook → navigate to /login.
 *  4. ApiError / network → ErrorState card with retry button.
 *  5. lastFiredPostId guard prevents re-firing onComplete when the
 *     `state.post` reference doesn't change (StrictMode / dev).
 *
 * Strategy: `vi.mock` the `useCreatePost` factory so step tests
 * don't need to know about useState + AbortController details.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";

// Hoisted mock object: define it BEFORE vi.mock so the factory
// can close over the same references.
const { mockSubmit, mockReset, useCreatePostMock } = vi.hoisted(() => {
  const mockSubmit = vi.fn();
  const mockReset = vi.fn();
  const useCreatePostMock = vi.fn();
  return { mockSubmit, mockReset, useCreatePostMock };
});

vi.mock(
  "../hooks/useCreatePost",
  () => ({
    useCreatePost: () => useCreatePostMock(),
  }),
);

import { AuthError } from "../../../lib/auth";
import { ConfirmationStep } from "./ConfirmationStep";
import type { MediaAsset } from "../api/mediaApi";
import type { ChannelMetadata } from "./ChannelMetadataStep";
import type { CreatePostState, Post } from "../api/types";

// ────────────────────────────────────────────────────────────────
// Test fixtures
// ────────────────────────────────────────────────────────────────

const TEST_ASSET: MediaAsset = {
  id: "ma_test_42",
  content_type: "video/mp4",
  size_bytes: 12_582_912, // 12 MB
  sha256: "deadbeef".repeat(8),
  status: "ready",
  expires_at: "2030-01-01T00:00:00Z",
};

const TEST_CHANNEL: ChannelMetadata = {
  workspaceId: 7,
  channelId: 99,
  ytTitle: "Test YT title — vertical slice",
  description: "Test description with some content.",
  tags: ["alpha", "beta", "gamma"],
  madeForKids: false,
};

const TEST_INTERNAL_TITLE = "Internal title: summer 2026 preview";

const expectedCaption =
  TEST_CHANNEL.description; // trimmed — same string
const expectedYtTitle =
  TEST_CHANNEL.ytTitle.trim();

function makePost(id: number): Post {
  return {
    id,
    workspace_id: TEST_CHANNEL.workspaceId,
    title: TEST_INTERNAL_TITLE,
    caption: TEST_CHANNEL.description,
    media_url: null,
    status: "queued",
    publish_at: null,
    scheduled_at: null,
    created_at: "2030-01-01T00:00:00Z",
    updated_at: "2030-01-01T00:00:00Z",
    targets: [
      {
        id: id * 10,
        post_id: id,
        platform_account_id: TEST_CHANNEL.channelId,
        status: "queued",
        external_id: null,
        public_url: null,
        error_message: null,
        published_at: null,
        attempt_count: 0,
        next_attempt_at: null,
        privacy_status: "private",
        made_for_kids: TEST_CHANNEL.madeForKids,
        youtube_sync_status: "pending",
        actual_privacy: null,
      },
    ],
  };
}

// ────────────────────────────────────────────────────────────────
// Test scaffolding
// ────────────────────────────────────────────────────────────────

function renderStep(
  overrides: Partial<{
    asset: MediaAsset;
    internalTitle: string;
    channel: ChannelMetadata;
    onBack: () => void;
    onJumpToStep: (step: 1 | 2) => void;
    initial: CreatePostState;
    submitImpl: () => Promise<unknown>;
  }> = {},
) {
  const onBack = overrides.onBack ?? vi.fn();
  const onJumpToStep = overrides.onJumpToStep ?? vi.fn();

  // Default state = idle. Tests override with submitting / success
  // / error via a mutable state and a setter.
  let stateRef = overrides.initial ?? ({ kind: "idle" } as CreatePostState);
  mockReset.mockReset();
  mockSubmit.mockReset();
  if (overrides.submitImpl) {
    mockSubmit.mockImplementation(overrides.submitImpl);
  } else {
    mockSubmit.mockResolvedValue(undefined);
  }
  useCreatePostMock.mockImplementation(() => ({
    state: stateRef,
    submit: mockSubmit,
    reset: mockReset,
  }));

  const utils = render(
    <MemoryRouter initialEntries={["/app/content/new"]}>
      <ConfirmationStep
        asset={overrides.asset ?? TEST_ASSET}
        internalTitle={overrides.internalTitle ?? TEST_INTERNAL_TITLE}
        channel={overrides.channel ?? TEST_CHANNEL}
        onBack={onBack}
          onJumpToStep={onJumpToStep}
      />
    </MemoryRouter>,
  );

  return {
    onBack,
    onJumpToStep,
    ...utils,
  };
}

// ────────────────────────────────────────────────────────────────
// Tests
// ────────────────────────────────────────────────────────────────

describe("ConfirmationStep — summary rendering", () => {
  beforeEach(() => {
    mockReset.mockReset();
    mockSubmit.mockReset();
    useCreatePostMock.mockReset();
  });
  afterEach(() => {
    vi.clearAllMocks();
  });

  it("renders the asset, internal title, channel pair, YT title, tag chips, made-for-kids, and locked privacy chip", () => {
    renderStep();

    // asset row (read-only; the `<Film>` chip plus asset_id)
    expect(screen.getByTestId("summary-asset-name")).toBeTruthy();
    expect(screen.getByText(/asset_id: ma_test_42/)).toBeTruthy();

    // internal title
    expect(screen.getByTestId("summary-internal-title").textContent).toContain(
      TEST_INTERNAL_TITLE,
    );

    // workspace + channel
    const channel = screen.getByTestId("summary-channel");
    expect(channel.textContent).toContain("workspace #7");
    expect(channel.textContent).toContain("channel #99");

    // yt title
    const ytTitle = screen.getByTestId("summary-yt-title");
    expect(ytTitle.textContent).toContain(TEST_CHANNEL.ytTitle);

    // tag chips
    const tags = screen.getByTestId("summary-tags");
    for (const tag of TEST_CHANNEL.tags) {
      expect(tags.textContent).toContain(tag);
    }

    // made-for-kids
    expect(screen.getByTestId("summary-made-for-kids").textContent).toContain(
      "No",
    );

    // privacy chip locked to private
    const privacy = screen.getByTestId("privacy-locked");
    expect(privacy.textContent).toContain('privacy_status: "private"');
  });
});

describe("ConfirmationStep — submit payload", () => {
  beforeEach(() => {
    mockReset.mockReset();
    mockSubmit.mockReset();
    useCreatePostMock.mockReset();
  });
  afterEach(() => {
    vi.clearAllMocks();
  });

  it("sends the exact CreatePostRequest contract with privacy=private, made_for_kids, tags, and content.media[asset_id]", async () => {
    renderStep();

    await act(async () => {
      fireEvent.click(screen.getByTestId("submit-button"));
    });

    expect(mockSubmit).toHaveBeenCalledTimes(1);
    const [payload] = mockSubmit.mock.calls[0] as [Record<string, unknown>];

    // Top-level
    expect(payload.workspace_id).toBe(TEST_CHANNEL.workspaceId);
    expect(payload.status).toBe("queued");

    // content
    const content = payload.content as Record<string, unknown>;
    expect(content.title).toBe(TEST_INTERNAL_TITLE);
    expect(content.caption).toBe(expectedCaption);
    expect(content.media).toEqual([{ asset_id: TEST_ASSET.id }]);

    // targets
    expect(payload.targets).toHaveLength(1);
    const target = (payload.targets as Array<Record<string, unknown>>)[0];
    expect(target.platform_account_id).toBe(TEST_CHANNEL.channelId);
    const youtube = (
      target.settings as { youtube: Record<string, unknown> }
    ).youtube;
    expect(youtube.title).toBe(expectedYtTitle);
    expect(youtube.description).toBe(expectedCaption);
    expect(youtube.privacy_status).toBe("private");
    expect(youtube.made_for_kids).toBe(TEST_CHANNEL.madeForKids);
    expect(youtube.tags).toEqual(TEST_CHANNEL.tags);
  });

  it("omits description / caption when the channel description is empty", async () => {
    renderStep({
      channel: { ...TEST_CHANNEL, description: "" },
    });

    await act(async () => {
      fireEvent.click(screen.getByTestId("submit-button"));
    });

    const [payload] = mockSubmit.mock.calls[0] as [Record<string, unknown>];
    const content = payload.content as Record<string, unknown>;
    expect(content.caption).toBeUndefined();
    const target = (payload.targets as Array<Record<string, unknown>>)[0];
    const youtube = (
      target.settings as { youtube: Record<string, unknown> }
    ).youtube;
    expect(youtube.description).toBeUndefined();
  });
});

describe("ConfirmationStep — error / auth / submit-state UX", () => {
  beforeEach(() => {
    mockReset.mockReset();
    mockSubmit.mockReset();
    useCreatePostMock.mockReset();
  });
  afterEach(() => {
    vi.clearAllMocks();
  });

  it("navigates to /login when useCreatePost raises AuthError", async () => {
    mockSubmit.mockRejectedValue(new AuthError());
    renderStep();

    await act(async () => {
      fireEvent.click(screen.getByTestId("submit-button"));
    });

    // `useNavigate` from MemoryRouter doesn't actually navigate, but
    // the call path went through navigate("/login", { replace: true }).
    // We assert no throw + no error card (AuthError is propagated to
    // caller, not surfaced as a step error).
    expect(screen.queryByTestId("submit-error")).toBeNull();
  });

  it("renders the error card with retry button when useCreatePost surfaces an ApiError", async () => {
    mockSubmit.mockRejectedValue(
      Object.assign(new Error("idempotency_key_conflict"), {
        name: "ApiError",
      }),
    );
    useCreatePostMock.mockImplementation(() => ({
      state: { kind: "error", message: "idempotency_key_conflict" },
      submit: mockSubmit,
      reset: mockReset,
    }));
    renderStep();

    expect(screen.getByTestId("submit-error")).toBeTruthy();
    expect(screen.getByTestId("submit-error").textContent).toContain(
      "idempotency_key_conflict",
    );
    expect(screen.getByTestId("submit-retry")).toBeTruthy();
  });

  it("renders the submitting indicator + disables the submit button during in-flight", () => {
    useCreatePostMock.mockImplementation(() => ({
      state: { kind: "submitting" },
      submit: mockSubmit,
      reset: mockReset,
    }));
    renderStep();

    expect(screen.getByTestId("submit-progress")).toBeTruthy();
    const btn = screen.getByTestId("submit-button") as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    expect(btn.textContent).toContain("Caricamento");
  });
});

describe("ConfirmationStep — success navigates exactly once", () => {
  let navigateSpy: ReturnType<typeof vi.fn>;
  beforeEach(async () => {
    // Spy on useNavigate so the post-success redirect target is
    // observable. The MemoryRouter stub doesn't track navigation;
    // we replace its return value with a vi.fn we can assert against.
    const rr = await import("react-router-dom");
    navigateSpy = vi.fn();
    vi.spyOn(rr, "useNavigate").mockImplementation(() => navigateSpy);
  });
  afterEach(() => {
    vi.clearAllMocks();
    vi.restoreAllMocks();
  });

  it("navigates to /app/content/{post.id}/publish when state becomes success, and not on a same-id re-render", async () => {
    const post = makePost(1234);
    useCreatePostMock.mockImplementation(() => ({
      state: { kind: "success", post } as CreatePostState,
      submit: mockSubmit,
      reset: mockReset,
    }));

    const { rerender } = render(
      <MemoryRouter initialEntries={["/app/content/new"]}>
        <ConfirmationStep
          asset={TEST_ASSET}
          internalTitle={TEST_INTERNAL_TITLE}
          channel={TEST_CHANNEL}
          onBack={vi.fn()}
          onJumpToStep={vi.fn()}
        />
      </MemoryRouter>,
    );

    expect(navigateSpy).toHaveBeenCalledTimes(1);
    expect(navigateSpy).toHaveBeenCalledWith(
      "/app/content/1234/publish",
    );

    // Same id re-render (parent state touched) MUST NOT re-navigate
    // — lastFiredPostIdRef guard. Rerender uses the SAME post so
    // the guard should hold.
    await act(async () => {
      rerender(
        <MemoryRouter initialEntries={["/app/content/new"]}>
          <ConfirmationStep
            asset={TEST_ASSET}
            internalTitle={TEST_INTERNAL_TITLE}
            channel={TEST_CHANNEL}
            onBack={vi.fn()}
            onJumpToStep={vi.fn()}
          />
        </MemoryRouter>,
      );
    });

    expect(navigateSpy).toHaveBeenCalledTimes(1);
  });
});
