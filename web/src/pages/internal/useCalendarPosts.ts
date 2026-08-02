import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { authedFetch, ApiError, AuthError } from "../../lib/auth";
import type { CalendarGroup, FetchState, Post, Workspace } from "./calendarTypes";

export function useCalendarPosts() {
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const abortRef = useRef<AbortController | null>(null);
  const [state, setState] = useState<FetchState>({ kind: "loading" });

  const statusFilter = searchParams.get("status") || "all";
  const workspaceFilter = searchParams.get("workspace_id") || "all";
  const groupFilter = searchParams.get("group_id") || "all";

  const load = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    setState({ kind: "loading" });

    try {
      const [postsResp, workspacesResp, groupsResp] = await Promise.all([
        authedFetch("/api/v1/posts", { signal: controller.signal }),
        authedFetch("/api/v1/workspaces", { signal: controller.signal }).catch(
          () => null,
        ),
        authedFetch("/api/v1/groups/aggregate", { signal: controller.signal }).catch(
          () => null,
        ),
      ]);
      if (controller.signal.aborted) return;
      const data = (await postsResp.json()) as { posts: Post[] };
      let workspaces: Workspace[] = [];
      if (workspacesResp && workspacesResp.ok) {
        const wsData = (await workspacesResp.json()) as {
          workspaces: Workspace[];
        };
        workspaces = wsData.workspaces ?? [];
      }
      let groups: CalendarGroup[] = [];
      if (groupsResp && groupsResp.ok) {
        const groupsData = (await groupsResp.json()) as { groups?: CalendarGroup[] };
        groups = groupsData.groups ?? [];
      }
      setState({ kind: "ready", posts: data.posts ?? [], workspaces, groups });
    } catch (err) {
      if (controller.signal.aborted) return;
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      const message = err instanceof ApiError ? err.message : "Unable to load posts.";
      setState({ kind: "error", message });
    }
  }, [navigate]);

  useEffect(() => {
    void load();
    return () => abortRef.current?.abort();
  }, [load]);

  const filteredPosts =
    state.kind === "ready"
      ? state.posts.filter((post) => {
          if (statusFilter !== "all" && post.status !== statusFilter) return false;
          if (
            workspaceFilter !== "all" &&
            String(post.workspace_id) !== workspaceFilter
          ) {
            return false;
          }
          if (groupFilter !== "all") {
            const group = state.groups.find((item) => String(item.id) === groupFilter);
            if (group && post.workspace_id !== group.workspace_id) return false;
          }
          return true;
        })
      : [];

  const hasActiveFilters = statusFilter !== "all" || workspaceFilter !== "all" || groupFilter !== "all";

  const setStatusFilter = (value: string) => {
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (value === "all") next.delete("status");
        else next.set("status", value);
        return next;
      },
      { replace: true },
    );
  };

  const setWorkspaceFilter = (value: string) => {
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (value === "all") next.delete("workspace_id");
        else next.set("workspace_id", value);
        return next;
      },
      { replace: true },
    );
  };

  const setGroupFilter = (value: string) => {
    if (typeof window !== "undefined") {
      if (value === "all") window.localStorage.removeItem("instaedit:last-calendar-group-id");
      else window.localStorage.setItem("instaedit:last-calendar-group-id", value);
    }
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (value === "all") next.delete("group_id");
        else next.set("group_id", value);
        next.delete("workspace_id");
        return next;
      },
      { replace: true },
    );
  };

  const clearFilters = () => {
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        next.delete("status");
        next.delete("workspace_id");
        next.delete("group_id");
        return next;
      },
      { replace: true },
    );
  };

  return {
    state,
    filteredPosts,
    statusFilter,
    workspaceFilter,
    hasActiveFilters,
    setStatusFilter,
    setWorkspaceFilter,
    groupFilter,
    setGroupFilter,
    clearFilters,
    load,
  };
}
