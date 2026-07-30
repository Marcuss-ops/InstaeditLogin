/**
 * Shared types for the global toast notification system.
 *
 * `ToastKind` is the discriminant for visual + ARIA semantics. The four
 * variants are wired to distinct color/icon/role combinations in
 * `ToastViewport.tsx` (success/info/warning → role="status";
 * error → role="alert" + aria-live="assertive").
 *
 * `ToastEntry` is the runtime shape stored in the module-level toast
 * bus (see `toast-bus.ts`). `createdAt` is ms-since-epoch and is used
 * for the flood-guard dedupe check.
 */
export type ToastKind = "success" | "error" | "info" | "warning";

export type ToastEntry = {
  id: string;
  kind: ToastKind;
  message: string;
  createdAt: number;
};
