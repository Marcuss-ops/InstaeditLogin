import { useCallback, useEffect, useRef, useState } from "react";

/**
 * useMutation — deps-free mutation hook for the SPA.
 *
 *   • Per-instance state — each `useMutation(fetcher)` call gets its own
 *     `{data, error, loading}` triple, so a row-level action menu and a
 *     page-level submit can coexist without overwriting each other.
 *   • Auto-abort prior — calling `mutate(vars)` while a previous mutate
 *     is still in flight aborts the prior request. Common SPA pitfall:
 *     double-clicking "Save" creates two records. Aborting prior keeps
 *     only the latest intent in flight.
 *   • Unmount cleanup — controller is aborted on unmount, so a navigated-
 *     away page won't silently write to state.
 *   • Reset — `reset()` aborts any in-flight request and clears all
 *     three state fields back to undefined / false.
 *   • Stable fetcher ref — the latest closure is always used inside the
 *     tick that fires `mutate`, so callers don't need to memoize.
 *
 * Taglio: built for splitting Posts.tsx's busyId+setBusyId loop into
 * one mutate per action (publish / cancel / retry / delete).
 */
export type MutationState<TData> = {
  data: TData | undefined;
  error: Error | undefined;
  loading: boolean;
};

export type MutationFetcher<TData, TVar> = (
  vars: TVar,
  signal: AbortSignal,
) => Promise<TData>;

export type UseMutationResult<TData, TVar> = MutationState<TData> & {
  /** Fire the mutation. Returns the data on success. */
  mutate: (vars: TVar) => Promise<TData>;
  /** Abort in-flight + clear all state. */
  reset: () => void;
};

export function useMutation<TData, TVar = void>(
  fetcher: MutationFetcher<TData, TVar>,
): UseMutationResult<TData, TVar> {
  const [state, setState] = useState<MutationState<TData>>({
    data: undefined,
    error: undefined,
    loading: false,
  });

  const isMountedRef = useRef(true);
  const acRef = useRef<AbortController | null>(null);
  const fetcherRef = useRef(fetcher);
  fetcherRef.current = fetcher;

  useEffect(() => {
    isMountedRef.current = true;
    return () => {
      isMountedRef.current = false;
      acRef.current?.abort();
    };
  }, []);

  const mutate = useCallback(async (vars: TVar): Promise<TData> => {
    // Auto-abort prior mutate so rapid clicks don't double-fire.
    acRef.current?.abort();
    const ac = new AbortController();
    acRef.current = ac;

    if (isMountedRef.current) {
      setState((s) => ({ ...s, loading: true, error: undefined }));
    }

    try {
      const data = await fetcherRef.current(vars, ac.signal);
      if (isMountedRef.current && !ac.signal.aborted) {
        setState({ data, error: undefined, loading: false });
      }
      return data;
    } catch (err) {
      const aborted =
        ac.signal.aborted ||
        (err instanceof DOMException && err.name === "AbortError");
      if (isMountedRef.current && !ac.signal.aborted) {
        setState((s) => ({
          ...s,
          error: aborted ? undefined : err instanceof Error ? err : new Error(String(err)),
          loading: false,
        }));
      }
      if (aborted) {
        throw new DOMException("aborted", "AbortError");
      }
      throw err;
    }
  }, []);

  const reset = useCallback(() => {
    acRef.current?.abort();
    acRef.current = null;
    if (isMountedRef.current) {
      setState({ data: undefined, error: undefined, loading: false });
    }
  }, []);

  return { ...state, mutate, reset };
}
