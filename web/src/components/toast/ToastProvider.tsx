import type { ReactNode } from "react";
import { ToastViewport } from "./ToastViewport";

/**
 * ToastProvider — placeholder wrapper that simply renders children
 * followed by a single `<ToastViewport/>` at the end of the tree.
 *
 * The provider itself holds no state: the queue lives in the
 * module-level `toastBus`, so we don't need a Context boundary. We
 * keep the `ToastProvider` component anyway for two reasons:
 *   1. The public API of this folder is `<ToastProvider/>` (per the
 *      product spec), so wiring it in App.tsx matches the spec verbatim;
 *   2. The viewport gets unmounted/remounted with the provider, which
 *      makes test setup tidy (one wrapper to bring the viewport into
 *      the DOM instead of two components).
 *
 * The viewport is fixed-position top-right (z-50) so it renders above
 * page content without intercepting page-level clicks (the viewport
 * container has `pointer-events: none`; individual cards re-enable
 * pointer events for the dismiss button).
 */
export function ToastProvider({ children }: { children: ReactNode }) {
  return (
    <>
      {children}
      <ToastViewport />
    </>
  );
}
