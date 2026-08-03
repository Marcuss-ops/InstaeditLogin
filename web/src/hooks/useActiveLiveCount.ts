import { useEffect, useState } from "react";
import { API_BASE_URL } from "../lib/api";
import { isDemoMode } from "../lib/demo";

/**
 * A livestream row as returned by GET /api/v1/livestreams (see the
 * livestream module plan). Only the state machine's `actual_state` is
 * needed here: the sidebar badge must count streams that are REALLY
 * live, not merely scheduled.
 */
export type LivestreamSummary = {
  actual_state?: string;
};

const POLL_INTERVAL_MS = 30_000;

/**
 * Count how many rows are actually live.
 *
 * Accepts both the `{ items: [...] }` envelope and a bare array so the
 * badge keeps working no matter which shape the endpoint settles on.
 */
export function countActiveLives(payload: unknown): number {
  if (!payload) return 0;
  const items = Array.isArray(payload)
    ? payload
    : Array.isArray((payload as { items?: unknown }).items)
      ? (payload as { items: unknown[] }).items
      : [];
  return items.reduce(
    (total, item) =>
      total + ((item as LivestreamSummary | null)?.actual_state === "live" ? 1 : 0),
    0,
  );
}

/**
 * Polls the livestreams endpoint and reports how many streams are
 * actually live. Returns `null` while the endpoint is unavailable (or
 * the user is in demo mode) so the sidebar simply hides the badge
 * instead of spamming error toasts for a hint that is not yet backed
 * by the API.
 */
export function useActiveLiveCount(): number | null {
  const [count, setCount] = useState<number | null>(null);

  useEffect(() => {
    if (isDemoMode()) return;

    let cancelled = false;

    const refresh = async () => {
      if (document.visibilityState === "hidden") return;
      try {
        const response = await fetch(`${API_BASE_URL}/api/v1/livestreams`, {
          credentials: "include",
        });
        if (!response.ok) return; // endpoint not deployed yet → keep badge hidden
        const payload: unknown = await response.json();
        if (!cancelled) setCount(countActiveLives(payload));
      } catch {
        // Never toast for a sidebar hint; a missing backend is expected
        // until the livestream module lands.
      }
    };

    // Pause while the tab is hidden so a sidebar hint never polls a
    // background session; refresh immediately when it becomes visible.
    const onVisibilityChange = () => {
      if (document.visibilityState === "visible") void refresh();
    };
    document.addEventListener("visibilitychange", onVisibilityChange);

    void refresh();
    const timer = window.setInterval(() => void refresh(), POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisibilityChange);
    };
  }, []);

  return count;
}
