/**
 * Barrel for the toast system. Production callers should import from
 * here only — no deeper paths. The bus is exported because a handful
 * of files (auth.ts, page-level async flows like Posts.tsx's
 * publish-state diff) need to push toasts from non-React code where
 * `useToast()` would violate the rules-of-hooks.
 */
export { ToastProvider } from "./ToastProvider";
export { ToastViewport } from "./ToastViewport";
export { useToast } from "./useToast";
export { toastBus } from "./toast-bus";
export type { ToastKind, ToastEntry } from "./types";
