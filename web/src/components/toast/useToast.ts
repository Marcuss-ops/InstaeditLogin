/**
 * useToast — bound to the global toast-bus singleton.
 *
 * Returns four methods (`success` / `error` / `info` / `warning`) that
 * push a toast entry onto the bus and return its id (so a caller could
 * dismiss it manually if needed). Default auto-dismiss is 5000ms;
 * pass `{ duration: ms }` to override per-call.
 *
 * The returned object is wrapped in `useMemo(..., [])` so its identity
 * (and each method's identity) is referentially stable across renders.
 * Drop it into a `useEffect`/`useCallback` dep array without churning
 * effect callbacks every render.
 *
 * The methods close over the `toastBus` module-level singleton (which
 * is itself stable), so memoizing the wrapper is sufficient — even if
 * we were to re-create the object every render, the behavior would
 * be identical. The memo buys us `Object.is` equality.
 */
import { useMemo } from "react";
import { toastBus } from "./toast-bus";

export function useToast() {
  return useMemo(
    () => ({
      success: (message: string, opts?: { duration?: number }) =>
        toastBus.push("success", message, opts?.duration),
      error: (message: string, opts?: { duration?: number }) =>
        toastBus.push("error", message, opts?.duration),
      info: (message: string, opts?: { duration?: number }) =>
        toastBus.push("info", message, opts?.duration),
      warning: (message: string, opts?: { duration?: number }) =>
        toastBus.push("warning", message, opts?.duration),
    }),
    [],
  );
}
