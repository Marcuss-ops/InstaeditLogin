import { useCallback, useEffect, useRef, useState } from "react";
import { authedFetch, AuthError } from "../../lib/auth";
import { isDemoMode } from "../../lib/demo";
import type { MediaLibraryDetail, MediaLibraryItem, MediaLibraryResponse } from "./livestreamsTypes";

export type MediaLibraryState =
  | { kind: "loading" }
  | {
      kind: "ready";
      items: MediaLibraryItem[];
      details: Record<string, MediaLibraryDetail | undefined>;
      nextCursor?: string;
      hasMore: boolean;
      loadingMore?: boolean;
      loadMoreError?: string;
    }
  | { kind: "error"; message: string };

async function fetchMediaLibrary(signal: AbortSignal, cursor?: string): Promise<MediaLibraryResponse> {
  if (isDemoMode()) return { items: [] };
  const params = new URLSearchParams({ limit: "100" });
  if (cursor) params.set("cursor", cursor);
  const response = await authedFetch(`/api/v1/media?${params.toString()}`, { signal });
  return (await response.json()) as MediaLibraryResponse;
}

async function fetchMediaDetail(id: string, signal: AbortSignal): Promise<MediaLibraryDetail> {
  const response = await authedFetch(`/api/v1/media/${encodeURIComponent(id)}`, { signal });
  return (await response.json()) as MediaLibraryDetail;
}

export function useMediaLibrary() {
  const [state, setState] = useState<MediaLibraryState>({ kind: "loading" });
  const abortRef = useRef<AbortController | null>(null);
  const detailCacheRef = useRef<Record<string, MediaLibraryDetail | undefined>>({});
  const detailRequestsRef = useRef(new Map<string, Promise<MediaLibraryDetail | undefined>>());
  const detailControllersRef = useRef(new Map<string, AbortController>());

  const loadDetail = useCallback((item: MediaLibraryItem) => {
    if (detailCacheRef.current[item.id] || detailRequestsRef.current.has(item.id)) return;
    const controller = new AbortController();
    detailControllersRef.current.set(item.id, controller);
    const request = fetchMediaDetail(item.id, controller.signal)
      .then((detail) => {
        if (controller.signal.aborted || detailControllersRef.current.get(item.id) !== controller) return undefined;
        detailCacheRef.current[item.id] = detail;
        setState((previous) => previous.kind === "ready"
          ? { ...previous, details: { ...previous.details, [item.id]: detail } }
          : previous,
        );
        return detail;
      })
      .catch(() => undefined)
      .finally(() => {
        if (detailControllersRef.current.get(item.id) === controller) {
          detailRequestsRef.current.delete(item.id);
          detailControllersRef.current.delete(item.id);
        }
      });
    detailRequestsRef.current.set(item.id, request);
  }, []);

  const loadDetails = useCallback((items: MediaLibraryItem[]) => {
    const firstViewport = items.slice(0, 4);
    for (const item of firstViewport) loadDetail(item);
  }, [loadDetail]);

  const abortDetails = useCallback(() => {
    for (const controller of detailControllersRef.current.values()) controller.abort();
    detailControllersRef.current.clear();
    detailRequestsRef.current.clear();
  }, []);

  const load = useCallback(async () => {
    abortRef.current?.abort();
    abortDetails();
    const controller = new AbortController();
    abortRef.current = controller;
    setState({ kind: "loading" });
    try {
      const data = await fetchMediaLibrary(controller.signal);
      if (controller.signal.aborted) return;
      const items = Array.isArray(data.items) ? data.items : [];
      setState({
        kind: "ready",
        items,
        details: { ...detailCacheRef.current },
        nextCursor: data.next_cursor,
        hasMore: data.has_more === true,
      });
      loadDetails(items);
    } catch (err) {
      if (controller.signal.aborted) return;
      if (err instanceof AuthError) {
        setState({ kind: "error", message: "Sessione scaduta. Accedi di nuovo." });
        return;
      }
      const message = err instanceof Error ? err.message : "Impossibile caricare la Media Library.";
      setState({ kind: "error", message });
    }
  }, [abortDetails, loadDetails]);

  useEffect(() => {
    void load();
    return () => {
      abortRef.current?.abort();
      abortDetails();
    };
  }, [load]);

  const loadMore = useCallback(async () => {
    if (state.kind !== "ready" || !state.hasMore || !state.nextCursor || state.loadingMore) return;
    const controller = new AbortController();
    abortRef.current?.abort();
    abortRef.current = controller;
    setState((previous) =>
      previous.kind === "ready" ? { ...previous, loadingMore: true, loadMoreError: undefined } : previous,
    );
    try {
      const data = await fetchMediaLibrary(controller.signal, state.nextCursor);
      if (controller.signal.aborted) return;
      const appended = data.items ?? [];
      setState((previous) =>
        previous.kind === "ready"
          ? {
              kind: "ready",
              items: [...previous.items, ...appended],
              details: { ...previous.details },
              nextCursor: data.next_cursor,
              hasMore: data.has_more === true,
            }
          : previous,
      );
      loadDetails(appended);
    } catch (err) {
      if (controller.signal.aborted) return;
      setState((previous) =>
        previous.kind === "ready"
          ? { ...previous, loadingMore: false, loadMoreError: err instanceof Error ? err.message : "Impossibile caricare altri asset." }
          : previous,
      );
    }
  }, [loadDetails, state]);

  return { state, reload: load, loadMore, loadDetail };
}
