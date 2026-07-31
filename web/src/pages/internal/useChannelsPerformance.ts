import { useCallback, useEffect, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { authedFetch, AuthError } from "../../lib/auth";
import type { FetchState, SummaryData, WorkspaceOption } from "./channelsPerformanceTypes";

export function useChannelsPerformance() {

  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const [state, setState] = useState<FetchState>({ kind: "loading" });

  const period = Number.parseInt(searchParams.get("days") || "30", 10);

  // Local filter inputs are only committed to the URL (and therefore
  // to the API call) when the user presses Apply. This avoids a
  // re-fetch on every keystroke and keeps the form usable.
  const [localFilters, setLocalFilters] = useState({
    workspace: searchParams.get("workspace") || "",
    group: searchParams.get("group") || "",
    language: searchParams.get("language") || "",
    manager: searchParams.get("manager") || "",
  });
  const [workspaces, setWorkspaces] = useState<WorkspaceOption[]>([]);
  const [workspacesLoading, setWorkspacesLoading] = useState(false);
  const [workspacesError, setWorkspacesError] = useState(false);

  // Keep local inputs in sync with the URL when it changes externally
  // (initial load, back/forward navigation, clear filters).
  useEffect(() => {
    setLocalFilters({
      workspace: searchParams.get("workspace") || "",
      group: searchParams.get("group") || "",
      language: searchParams.get("language") || "",
      manager: searchParams.get("manager") || "",
    });
  }, [searchParams]);

  // Load available workspaces once so the workspace filter can be a
  // dropdown instead of a free-form text field.
  useEffect(() => {
    async function loadWorkspaces() {
      setWorkspacesLoading(true);
      setWorkspacesError(false);
      try {
        const response = await authedFetch("/api/v1/workspaces");
        const data = (await response.json()) as { workspaces: WorkspaceOption[] };
        setWorkspaces(data.workspaces ?? []);
      } catch (err) {
        setWorkspacesError(true);
        console.error("Failed to load workspaces", err);
      } finally {
        setWorkspacesLoading(false);
      }
    }
    void loadWorkspaces();
  }, []);

  const setPeriod = useCallback(
    (days: number) => {
      const next = new URLSearchParams(searchParams);
      next.set("days", String(days));
      setSearchParams(next, { replace: true });
    },
    [searchParams, setSearchParams],
  );

  const applyFilters = useCallback(() => {
    const next = new URLSearchParams(searchParams);
    if (localFilters.workspace) {
      next.set("workspace", localFilters.workspace);
    } else {
      next.delete("workspace");
    }
    if (localFilters.group) {
      next.set("group", localFilters.group);
    } else {
      next.delete("group");
    }
    if (localFilters.language) {
      next.set("language", localFilters.language);
    } else {
      next.delete("language");
    }
    if (localFilters.manager) {
      next.set("manager", localFilters.manager);
    } else {
      next.delete("manager");
    }
    setSearchParams(next, { replace: true });
  }, [localFilters, searchParams, setSearchParams]);

  const clearFilters = useCallback(() => {
    setSearchParams({ days: String(period) }, { replace: true });
  }, [period, setSearchParams]);

  const load = useCallback(async () => {
    setState({ kind: "loading" });
    try {
      const params = new URLSearchParams(searchParams);
      if (!params.has("days")) {
        params.set("days", "30");
      }
      const response = await authedFetch(
        `/api/v1/accounts/performance/summary?${params.toString()}`,
      );
      const data = (await response.json()) as SummaryData;
      setState({ kind: "ready", data });
    } catch (err) {
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      const message =
        err instanceof Error ? err.message : "Unable to load channel performance.";
      setState({ kind: "error", message });
    }
  }, [navigate, searchParams]);

  useEffect(() => {
    void load();
  }, [load]);

  const topSubscribers =
    state.kind === "ready" && state.data.rankings
      ? state.data.rankings.by_subscribers?.slice(0, 5).map((item) => ({
          name: item.username,
          value: item.value,
        })) ?? []
      : [];


  return { state, period, localFilters, setLocalFilters, workspaces, workspacesLoading, workspacesError, setPeriod, applyFilters, clearFilters, load, topSubscribers };
}
