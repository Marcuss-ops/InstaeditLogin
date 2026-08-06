import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { authedFetchMock, fetchSessionMock, listAllAccountsMock } = vi.hoisted(() => ({
  authedFetchMock: vi.fn(),
  fetchSessionMock: vi.fn(),
  listAllAccountsMock: vi.fn(),
}));

vi.mock("../../lib/auth", () => ({
  ApiError: class ApiError extends Error {},
  AuthError: class AuthError extends Error {},
  authedFetch: authedFetchMock,
  fetchSession: fetchSessionMock,
}));

vi.mock("../../features/channels/api/channelsApi", () => ({
  listAllAccounts: listAllAccountsMock,
}));

import { useGroupsData } from "./useGroupsData";
import type { PlatformAccount } from "./groupsTypes";

const account: PlatformAccount = {
  id: 42,
  platform: "youtube",
  username: "channel-to-assign",
  platform_user_id: "UC-42",
  status: "active",
  created_at: "2026-01-01T00:00:00Z",
};

function jsonResponse(body: unknown, ok = true): Response {
  return {
    ok,
    json: async () => body,
  } as Response;
}

function wrapper({ children }: { children: ReactNode }) {
  return <MemoryRouter initialEntries={["/app/groups"]}>{children}</MemoryRouter>;
}

describe("useGroupsData assignment persistence", () => {
  beforeEach(() => {
    authedFetchMock.mockReset();
    fetchSessionMock.mockReset();
    listAllAccountsMock.mockReset();

    fetchSessionMock.mockResolvedValue({ userId: 1 });
    listAllAccountsMock.mockResolvedValue([account]);
  });

  it("persists an inline group rename and reloads the server name", async () => {
    let groupName = "YouTube";
    authedFetchMock.mockImplementation(async (path: string, init?: RequestInit) => {
      if (path === "/api/v1/auth/me") return jsonResponse({ workspace_id: 9 });
      if (path === "/api/v1/groups/aggregate") {
        return jsonResponse({ groups: [{ id: 7, workspace_id: 9, name: groupName, account_ids: [] }] });
      }
      if (path === "/api/v1/groups/7" && init?.method === "PATCH") {
        groupName = (JSON.parse(String(init.body)) as { name: string }).name;
        return jsonResponse({ id: 7, name: groupName });
      }
      throw new Error(`unexpected request: ${path}`);
    });

    const { result } = renderHook(() => useGroupsData(), { wrapper });
    await waitFor(() => expect(result.current.state.kind).toBe("ready"));

    await act(async () => {
      await result.current.renameGroup(7, "  YouTube WWE  ");
    });

    expect(authedFetchMock).toHaveBeenCalledWith(
      "/api/v1/groups/7",
      expect.objectContaining({
        method: "PATCH",
        body: JSON.stringify({ name: "YouTube WWE" }),
      }),
    );
    await waitFor(() => expect(result.current.state.kind === "ready" && result.current.state.groups[0]?.name).toBe("YouTube WWE"));
  });

  it("rejects invalid rename names before making a request", async () => {
    authedFetchMock.mockImplementation(async (path: string) => {
      if (path === "/api/v1/auth/me") return jsonResponse({ workspace_id: 9 });
      if (path === "/api/v1/groups/aggregate") return jsonResponse({ groups: [{ id: 7, workspace_id: 9, name: "YouTube", account_ids: [] }] });
      throw new Error(`unexpected request: ${path}`);
    });

    const { result } = renderHook(() => useGroupsData(), { wrapper });
    await waitFor(() => expect(result.current.state.kind).toBe("ready"));

    await expect(result.current.renameGroup(7, "   ")).rejects.toThrow("obbligatorio");
    await expect(result.current.renameGroup(7, "x".repeat(81))).rejects.toThrow("80 caratteri");
    expect(authedFetchMock).not.toHaveBeenCalledWith("/api/v1/groups/7", expect.anything());
  });

  it("sends the PUT membership payload and keeps the channel after reconcile", async () => {
    let aggregateAccountIDs: number[] = [];
    authedFetchMock.mockImplementation(async (path: string, init?: RequestInit) => {
      if (path === "/api/v1/auth/me") {
        return jsonResponse({ workspace_id: 9 });
      }
      if (path === "/api/v1/groups/aggregate") {
        return jsonResponse({
          groups: [{ id: 7, workspace_id: 9, name: "YouTube", account_ids: aggregateAccountIDs }],
        });
      }
      if (path === "/api/v1/groups/7/accounts" && init?.method === "PUT") {
        const body = JSON.parse(String(init.body)) as { account_ids?: number[] };
        aggregateAccountIDs = body.account_ids ?? [];
        return jsonResponse({ account_ids: aggregateAccountIDs });
      }
      throw new Error(`unexpected request: ${path}`);
    });

    const { result } = renderHook(() => useGroupsData(), { wrapper });
    await waitFor(() => expect(result.current.state.kind).toBe("ready"));

    await act(async () => {
      await result.current.assignAccountToGroup(account.id, 7);
    });

    expect(authedFetchMock).toHaveBeenCalledWith(
      "/api/v1/groups/7/accounts",
      expect.objectContaining({
        method: "PUT",
        body: JSON.stringify({ account_ids: [account.id] }),
      }),
    );
    await waitFor(() => {
      expect(result.current.tree[0]?.accounts.map((item) => item.id)).toEqual([account.id]);
    });
  });
});
