import { useCallback, useEffect, useRef } from "react";
import type { MouseEvent as ReactMouseEvent } from "react";

/**
 * Event handlers returned by `useLongPress`. Spread them onto any element
 * (button, span, div, anchor, …) to make it respond to:
 *
 *  - **Right-click** (desktop): `onContextMenu` calls `preventDefault()` to
 *    suppress the browser's native context menu and invokes `onTrigger`.
 *  - **Long-press** (touch OR mouse, ≥ `delayMs`): pointer-down starts a
 *    timer; if the user keeps holding past the threshold, `onTrigger`
 *    fires. Any pointer-up / leave / cancel before that clears the timer.
 *  - **Duplicate-suppression**: if a touch-long-press also dispatches a
 *    `contextmenu` event (some mobile browsers), the dedupe window in
 *    `fire()` prevents the handler from running twice within `dedupeMs`.
 *
 * Use case in this app: attach to the /login status pill and the
 * /status green/red cards so a dev (or power user) can force-clear the
 * sessionStorage probe cache and re-probe immediately, bypassing the
 * 5-minute TTL.
 */
export type LongPressHandlers = {
  onTouchStart: () => void;
  onTouchEnd: () => void;
  onTouchCancel: () => void;
  onPointerDown: () => void;
  onPointerUp: () => void;
  onPointerLeave: () => void;
  onContextMenu: (e: ReactMouseEvent) => void;
};

export function useLongPress(
  onTrigger: () => void,
  delayMs: number = 600,
  dedupeMs: number = 200,
): LongPressHandlers {
  const timerRef = useRef<number | null>(null);
  const lastTriggeredAtRef = useRef(0);
  // Keep a ref to the latest callback so callers can pass an inline
  // function without invalidating the returned handlers.
  const onTriggerRef = useRef(onTrigger);
  onTriggerRef.current = onTrigger;

  const fire = useCallback(() => {
    const now = Date.now();
    if (now - lastTriggeredAtRef.current < dedupeMs) {
      return;
    }
    lastTriggeredAtRef.current = now;
    onTriggerRef.current();
  }, [dedupeMs]);

  const start = useCallback(() => {
    if (timerRef.current !== null) {
      clearTimeout(timerRef.current);
    }
    timerRef.current = window.setTimeout(() => {
      timerRef.current = null;
      fire();
    }, delayMs);
  }, [delayMs, fire]);

  const cancel = useCallback(() => {
    if (timerRef.current !== null) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  useEffect(
    () => () => {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current);
        timerRef.current = null;
      }
    },
    [],
  );

  return {
    onTouchStart: start,
    onTouchEnd: cancel,
    onTouchCancel: cancel,
    onPointerDown: start,
    onPointerUp: cancel,
    onPointerLeave: cancel,
    onContextMenu: (e) => {
      // Suppress the browser's "Open link in new tab / Save / Inspect"
      // menu so the affordance feels intentional, not accidental.
      e.preventDefault();
      fire();
    },
  };
}
