/**
 * Shared test utilities for the `ContentPublish` feature-test suite
 * (`/app/content/:postId/publish` status-asincrono page).
 *
 * This module holds the **pure** helpers only (fixtures, state shape,
 * render helper). It deliberately does NOT export the module mocks:
 * Vitest forbids exporting `vi.hoisted` variables from an external
 * module ("Cannot export hoisted variable"), so each feature test file
 * declares its own local `vi.hoisted` block + `vi.mock` factories —
 * matching the repo convention (see ConfirmationStep.test.tsx,
 * Groups.test.tsx, useCreateYouTubeEditorSession.test.ts).
 *
 * The feature files live alongside:
 *
 *   ContentPublish.retryGating.test.tsx  — RETRIABLE_STATUSES gating +
 *                                          force flag forwarding
 *   ContentPublish.retryFlow.test.tsx    — AuthError → /login,
 *                                          retryingId lifecycle,
 *                                          retryErrorById isolation,
 *                                          multi-target isolation
 *   ContentPublish.states.test.tsx       — invalid postId, loading,
 *                                          fetch-error rendering paths
 *   ContentPublish.crossTab.test.tsx     — cross-tab publish broadcast
 *
 * Mocking strategy (kept from the original single-file suite): static
 * `vi.mock(...)` factories (no dynamic-imports + `vi.spyOn` — that
 * pattern broke under some vitest ESM loader configurations). All
 * mocks are hoisted via `vi.hoisted` so the factories can close over
 * the same references used in tests.
 */
import { vi } from "vitest";
import { render } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { ContentPublish } from "./ContentPublish";
import type { PostStatus, PostTarget } from "../../features/publishing/api/types";

// ── Fixtures ──────────────────────────────────────────────────────────

export function makeTarget(
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

export interface MockState {
  targets: PostTarget[];
  status: "loading" | "ready" | "error";
  error: string | null;
}

/**
 * Builds the object the `usePostTargetStatus` mock must return for a
 * given MockState. Pure — does not touch any module mock, so it can
 * live here safely. Each test file wraps it with its own
 * `setMockState` that points at its local hoisted mock.
 */
export function stateProps(s: MockState) {
  return {
    targets: s.targets,
    status: s.status,
    error: s.error,
    refetch: vi.fn().mockResolvedValue(undefined),
  };
}

// Helper: render the page inside MemoryRouter at the canonical path.
export function renderAtPostIdPath(postId: string | number) {
  return render(
    <MemoryRouter initialEntries={[`/app/content/${postId}/publish`]}>
      <Routes>
        <Route path="/app/content/:postId/publish" element={<ContentPublish />} />
      </Routes>
    </MemoryRouter>,
  );
}
