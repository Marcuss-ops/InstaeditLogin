import { useCallback, useEffect, useRef, useState } from "react";
import { useToast } from "../../components/toast";
import { authedFetch, fetchSession } from "../../lib/auth";

export type AppNotificationKind = "published" | "error" | "copyright";

export type AppNotification = {
  id: string;
  kind: AppNotificationKind;
  title: string;
  message: string;
  createdAt: string;
  href: string;
};

type PostNotificationRecord = {
  id: number;
  title?: string;
  status?: string;
  updated_at?: string;
  created_at?: string;
};

type CopyrightNotificationRecord = {
  id: number;
  youtube_video_id: string;
  status: string;
  message?: string;
  checked_at?: string | null;
};

const READ_STORAGE_PREFIX = "instaedit.notifications.read.v1";
const POLL_INTERVAL_MS = 30_000;

function storageKey(userId: number): string {
  return `${READ_STORAGE_PREFIX}.${userId}`;
}

function readIds(userId: number): string[] {
  try {
    const value = JSON.parse(localStorage.getItem(storageKey(userId)) ?? "[]");
    return Array.isArray(value) ? value.filter((id): id is string => typeof id === "string") : [];
  } catch {
    return [];
  }
}

function saveIds(userId: number, ids: string[]): void {
  try {
    localStorage.setItem(storageKey(userId), JSON.stringify(ids.slice(-500)));
  } catch {
    // Notifications remain usable in memory when storage is unavailable.
  }
}

function postNotification(post: PostNotificationRecord): AppNotification | null {
  const status = post.status ?? "";
  const timestamp = post.updated_at || post.created_at || new Date().toISOString();
  const label = post.title?.trim() || `Post #${post.id}`;
  if (status === "published") {
    return {
      id: `post:published:${post.id}:${timestamp}`,
      kind: "published",
      title: "Pubblicazione completata",
      message: `${label} è stato pubblicato correttamente.`,
      createdAt: timestamp,
      href: `/app/content/${post.id}/publish`,
    };
  }
  if (["failed", "dlq", "dead_letter", "blocked_auth"].includes(status)) {
    return {
      id: `post:error:${post.id}:${timestamp}`,
      kind: "error",
      title: "Errore di pubblicazione",
      message: `${label} richiede un controllo (${status}).`,
      createdAt: timestamp,
      href: `/app/content/${post.id}/publish`,
    };
  }
  return null;
}

function copyrightNotification(alert: CopyrightNotificationRecord): AppNotification {
  const timestamp = alert.checked_at || new Date().toISOString();
  return {
    id: `copyright:${alert.id}:${timestamp}`,
    kind: "copyright",
    title: "Problema copyright rilevato",
    message: alert.message || `Video ${alert.youtube_video_id}: ${alert.status}.`,
    createdAt: timestamp,
    href: "/app/youtube/studio",
  };
}

function sortNotifications(items: AppNotification[]): AppNotification[] {
  return items
    .slice()
    .sort((a, b) => new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime())
    .slice(0, 100);
}

export function useNotifications() {
  const toast = useToast();
  const [items, setItems] = useState<AppNotification[]>([]);
  const [read, setRead] = useState<Set<string>>(new Set());
  const userIdRef = useRef<number | null>(null);
  const knownIdsRef = useRef<Set<string> | null>(null);

  const refresh = useCallback(async () => {
    const session = await fetchSession();
    if (!session) return;
    userIdRef.current = session.userId;
    if (knownIdsRef.current === null) {
      knownIdsRef.current = new Set();
      setRead(new Set(readIds(session.userId)));
    }

    const [postsResult, alertsResult] = await Promise.allSettled([
      authedFetch("/api/v1/posts?limit=100"),
      authedFetch("/api/v1/youtube/copyright-alerts"),
    ]);
    const next: AppNotification[] = [];
    if (postsResult.status === "fulfilled") {
      try {
        const data = (await postsResult.value.json()) as { posts?: PostNotificationRecord[] };
        for (const post of data.posts ?? []) {
          const notification = postNotification(post);
          if (notification) next.push(notification);
        }
      } catch {
        // Another view may have consumed a shared test/mock response body;
        // notification polling must never create an unhandled rejection.
      }
    }
    if (alertsResult.status === "fulfilled") {
      try {
        const data = (await alertsResult.value.json()) as { alerts?: CopyrightNotificationRecord[] };
        for (const alert of data.alerts ?? []) next.push(copyrightNotification(alert));
      } catch {
        // Optional copyright alerts are best-effort for the global center.
      }
    }

    const sorted = sortNotifications(next);
    const known = knownIdsRef.current;
    if (known && known.size > 0) {
      const fresh = sorted.filter((item) => !known.has(item.id)).slice(0, 3);
      for (const item of fresh) {
        if (item.kind === "published") toast.success(item.message);
        else if (item.kind === "copyright") toast.warning(item.message);
        else toast.error(item.message);
      }
    }
    knownIdsRef.current = new Set(sorted.map((item) => item.id));
    setItems(sorted);
  }, [toast]);

  useEffect(() => {
    void refresh();
    const interval = window.setInterval(() => void refresh(), POLL_INTERVAL_MS);
    return () => window.clearInterval(interval);
  }, [refresh]);

  const markRead = useCallback((id: string) => {
    const userId = userIdRef.current;
    setRead((current) => {
      const next = new Set(current);
      next.add(id);
      if (userId !== null) saveIds(userId, [...next]);
      return next;
    });
  }, []);

  const markAllRead = useCallback(() => {
    const userId = userIdRef.current;
    setRead((current) => {
      const next = new Set([...current, ...items.map((item) => item.id)]);
      if (userId !== null) saveIds(userId, [...next]);
      return next;
    });
  }, [items]);

  return {
    items,
    unreadCount: items.reduce((count, item) => count + (read.has(item.id) ? 0 : 1), 0),
    isRead: (id: string) => read.has(id),
    markRead,
    markAllRead,
  };
}
