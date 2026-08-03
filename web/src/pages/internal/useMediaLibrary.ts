import { useCallback, useEffect, useRef, useState } from "react";
import { authedFetch, AuthError } from "../../lib/auth";
import { isDemoMode } from "../../lib/demo";
import type { MediaLibraryItem, MediaLibraryResponse } from "./livestreamsTypes";

export type MediaLibraryState =
  | { kind: "loading" }
  | { kind: "ready"; items: MediaLibraryItem[] }
  | { kind: "error"; message: string };

/**
 * Fetch the caller's ready media assets with their ffprobe metadata
 * (GET /api/v1/media — the Media Library). Powers the live wizard's
 * step 3 picker. Same abort/state pattern as useLivestreamChannels.
 */
async function fetchMediaLibrary(signal: AbortSignal): Promise<MediaLibraryItem[]> {
  if (isDemoMode()) return [];
  const response = await authedFetch("/api/v1/media?limit=100", { signal });
  const data = (await response.json()) as MediaLibraryResponse;
  return Array.isArray(data.items) ? data.items : [];
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
      const items = await fetchMediaLibrary(controller.signal);
      if (controller.signal.aborted) return;
      setState({ kind: "ready", items });
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

  return { state, reload: load };
}
