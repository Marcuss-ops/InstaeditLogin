import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { authedFetch, ApiError, AuthError } from "../../lib/auth";
import type { CalendarGroup, FetchState, Post, Workspace, YouTubeCopyrightAlert } from "./calendarTypes";
import { STORAGE_KEYS } from "../../lib/storageKeys";

export function useCalendarPosts() {
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const abortRef = useRef<AbortController | null>(null);
  const hasLoadedRef = useRef(false);
  const [state, setState] = useState<FetchState>({ kind: "loading" });

  const statusFilter = searchParams.get("status") || "all";
  const workspaceFilter = searchParams.get("workspace_id") || "all";
  const groupFilter = searchParams.get("group_id") || "all";

  const load = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    if (!hasLoadedRef.current) setState({ kind: "loading" });

    try {
      const from = new Date(Date.now() - 90 * 24 * 60 * 60 * 1000).toISOString();
      const to = new Date(Date.now() + 180 * 24 * 60 * 60 * 1000).toISOString();
      const [postsResp, workspacesResp, groupsResp, uploadsResp, copyrightResp] = await Promise.all([
        authedFetch("/api/v1/posts", { signal: controller.signal }),
        authedFetch("/api/v1/workspaces", { signal: controller.signal }).catch(
          () => null,
        ),
        authedFetch("/api/v1/groups/aggregate", { signal: controller.signal }).catch(
          () => null,
        ),
        authedFetch(`/api/v1/uploads?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}&limit=500`, { signal: controller.signal }).catch(
          () => null,
        ),
        authedFetch("/api/v1/youtube/copyright-alerts", { signal: controller.signal }).catch(() => null),
      ]);
      if (controller.signal.aborted) return;
      const data = (await postsResp.json()) as { posts: Post[] };
      const uploadsData = uploadsResp && uploadsResp.ok
        ? (await uploadsResp.json()) as { uploads?: Array<{
            id: number;
            workspace_id: number;
            title?: string;
            caption?: string;
            publish_at?: string | null;
            scheduled_at?: string | null;
            status: string;
            created_at: string;
            targets?: number[];
            source_type?: string;
          }> }
        : { uploads: [] };
      const copyrightData = copyrightResp && copyrightResp.ok
        ? (await copyrightResp.json()) as { alerts?: YouTubeCopyrightAlert[] }
        : { alerts: [] };
      const alertsByPost = new Map<number, YouTubeCopyrightAlert[]>();
      for (const alert of copyrightData.alerts ?? []) {
        for (const key of [alert.post_id, alert.upload_job_id]) {
          if (!key) continue;
          const existing = alertsByPost.get(key) ?? [];
          existing.push(alert);
          alertsByPost.set(key, existing);
        }
      }
      const scheduledUploads: Post[] = (uploadsData.uploads ?? [])
        .filter((upload) => Boolean(upload.publish_at || upload.scheduled_at))
        .map((upload) => ({
          id: upload.id,
          workspace_id: upload.workspace_id,
          title: upload.title,
          caption: upload.caption,
          scheduled_at: upload.publish_at ?? upload.scheduled_at,
          status: upload.status === "ingest_completed" ? "queued" : upload.status,
          created_at: upload.created_at,
          targets: upload.targets ?? [],
          source_type: upload.source_type,
          source: "upload",
          copyright_alerts: alertsByPost.get(upload.id),
        }));
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
      const posts = (data.posts ?? []).map((post) => {
        const metadata = post.metadata ?? {};
        return {
          ...post,
          generation_status: post.generation_status ?? (typeof metadata.generation_status === "string" ? metadata.generation_status : undefined),
          generation_at: post.generation_at ?? (typeof metadata.generation_at === "string" ? metadata.generation_at : undefined),
          generation_timezone: post.generation_timezone ?? (typeof metadata.schedule_timezone === "string" ? metadata.schedule_timezone : undefined),
          generation_progress: post.generation_progress ?? (typeof metadata.generation_progress === "number" ? metadata.generation_progress : undefined),
          generation_phase: post.generation_phase ?? (typeof metadata.generation_phase === "string" ? metadata.generation_phase : undefined),
          generation_snapshot: post.generation_snapshot ?? (metadata.generation_snapshot && typeof metadata.generation_snapshot === "object" ? metadata.generation_snapshot as Record<string, unknown> : undefined),
          targets: post.targets ?? (Array.isArray(metadata.agent_target_ids) ? metadata.agent_target_ids.filter((id): id is number => typeof id === "number") : undefined),
          copyright_alerts: alertsByPost.get(post.id),
        };
      });
      hasLoadedRef.current = true;
      setState({ kind: "ready", posts: [...posts, ...scheduledUploads], workspaces, groups });
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

  useEffect(() => {
    if (state.kind !== "ready") return;
    const active = state.posts.some((post) => post.generation_status && !["SCHEDULED", "CANCELLED", "CONTENT_READY", "FAILED", "completed", "failed"].includes(post.generation_status));
    if (!active) return;
    const timer = window.setInterval(() => { void load(); }, 5000);
    return () => window.clearInterval(timer);
  }, [state, load]);

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
      if (value === "all") window.localStorage.removeItem(STORAGE_KEYS.lastCalendarGroupId);
      else window.localStorage.setItem(STORAGE_KEYS.lastCalendarGroupId, value);
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
