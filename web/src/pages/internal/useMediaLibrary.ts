import { useCallback, useEffect, useRef, useState } from "react";
import { authedFetch, AuthError } from "../../lib/auth";
import { isDemoMode } from "../../lib/demo";
import type { MediaLibraryItem, MediaLibraryResponse } from "./livestreamsTypes";

export type MediaLibraryState =
  | { kind: "loading" }
  | {
      kind: "ready";
      items: MediaLibraryItem[];
      nextCursor?: string;
      hasMore: boolean;
      loadingMore?: boolean;
      loadMoreError?: string;
    }
  | { kind: "error"; message: string };

/**
 * Fetch the caller's ready media assets with their ffprobe metadata
 * (GET /api/v1/media — the Media Library). Powers the live wizard's
 * step 3 picker. Same abort/state pattern as useLivestreamChannels.
 */
async function fetchMediaLibrary(
  signal: AbortSignal,
  cursor?: string,
): Promise<MediaLibraryResponse> {
  if (isDemoMode()) return { items: [] };
  const params = new URLSearchParams({ limit: "100" });
  if (cursor) params.set("cursor", cursor);
  const response = await authedFetch(`/api/v1/media?${params.toString()}`, { signal });
  return (await response.json()) as MediaLibraryResponse;
}

export function useMediaLibrary() {
  const [state, setState] = useState<MediaLibraryState>({ kind: "loading" });
  const abortRef = useRef<AbortController | null>(null);

  const load = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    setState({ kind: "loading" });
    try {
      const data = await fetchMediaLibrary(controller.signal);
      if (controller.signal.aborted) return;
      setState({
        kind: "ready",
        items: Array.isArray(data.items) ? data.items : [],
        nextCursor: data.next_cursor,
        hasMore: data.has_more === true,
      });
    } catch (err) {
      if (controller.signal.aborted) return;
      if (err instanceof AuthError) {
        setState({ kind: "error", message: "Sessione scaduta. Accedi di nuovo." });
        return;
      }
      const message = err instanceof Error ? err.message : "Impossibile caricare la Media Library.";
      setState({ kind: "error", message });
    }
  }, []);

  useEffect(() => {
    void load();
    return () => abortRef.current?.abort();
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
      setState((previous) =>
        previous.kind === "ready"
          ? {
              kind: "ready",
              items: [...previous.items, ...(data.items ?? [])],
              nextCursor: data.next_cursor,
              hasMore: data.has_more === true,
            }
          : previous,
      );
    } catch (err) {
      if (controller.signal.aborted) return;
      setState((previous) =>
        previous.kind === "ready"
          ? { ...previous, loadingMore: false, loadMoreError: err instanceof Error ? err.message : "Impossibile caricare altri asset." }
          : previous,
      );
    }
  }, [state]);

  return { state, reload: load, loadMore };
}
