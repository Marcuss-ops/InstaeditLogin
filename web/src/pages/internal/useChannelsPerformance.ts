import { useCallback, useEffect, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { authedFetch, AuthError } from "../../lib/auth";
import type { FetchState, SummaryData, GroupOption } from "./channelsPerformanceTypes";

export function useChannelsPerformance() {

  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const [state, setState] = useState<FetchState>({ kind: "loading" });

  const period = Number.parseInt(searchParams.get("days") || "30", 10);

  // Local filter inputs are only committed to the URL (and therefore
  // to the API call) when the user presses Apply. This avoids a
  // re-fetch on every keystroke and keeps the form usable.
  const [localFilters, setLocalFilters] = useState({
    group: searchParams.get("group") || "",
  });
  const [groups, setGroups] = useState<GroupOption[]>([]);

  // Keep local inputs in sync with the URL when it changes externally
  // (initial load, back/forward navigation, clear filters).
  useEffect(() => {
    setLocalFilters({
      group: searchParams.get("group") || "",
    });
  }, [searchParams]);

  // Load the groups once so Performance can be scoped without exposing
  // the internal workspace/manager filters.
  useEffect(() => {
    async function loadGroups() {
      try {
        const response = await authedFetch("/api/v1/groups/aggregate");
        const data = (await response.json()) as { groups?: GroupOption[] };
        setGroups(data.groups ?? []);
      } catch (err) {
        console.error("Failed to load groups", err);
      }
    }
    void loadGroups();
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
    if (localFilters.group) {
      next.set("group", localFilters.group);
    } else {
      next.delete("group");
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

  return { state, period, localFilters, setLocalFilters, groups, setPeriod, applyFilters, clearFilters, load };
}
