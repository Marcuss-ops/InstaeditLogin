import { useState } from "react";
import { AlertTriangle, Bell, CheckCircle2, Copyright, XCircle } from "lucide-react";
import { Link } from "react-router-dom";
import { cn } from "../../lib/utils";
import { useNotifications, type AppNotification, type AppNotificationKind } from "./useNotifications";

const ICONS: Record<AppNotificationKind, typeof CheckCircle2> = {
  published: CheckCircle2,
  error: XCircle,
  copyright: Copyright,
};

function relativeDate(value: string): string {
  const timestamp = new Date(value).getTime();
  if (!Number.isFinite(timestamp)) return "";
  const minutes = Math.max(0, Math.round((Date.now() - timestamp) / 60_000));
  if (minutes < 1) return "adesso";
  if (minutes < 60) return `${minutes} min fa`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h fa`;
  return new Date(value).toLocaleDateString("it-IT");
}

function NotificationRow({ item, unread, onRead, onClose }: {
  item: AppNotification;
  unread: boolean;
  onRead: () => void;
  onClose: () => void;
}) {
  const Icon = ICONS[item.kind];
  const color = item.kind === "published" ? "text-emerald-300" : item.kind === "copyright" ? "text-amber-300" : "text-red-300";
  return (
    <Link
      to={item.href}
      onClick={() => {
        onRead();
        onClose();
      }}
      className={cn(
        "flex gap-3 border-b border-white/[0.07] px-4 py-3 no-underline transition-colors hover:bg-white/[0.06]",
        unread && "bg-white/[0.035]",
      )}
      data-testid="notification-row"
    >
      <Icon size={17} className={cn("mt-0.5 shrink-0", color)} aria-hidden="true" />
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-2">
          <span className="truncate text-[12px] font-semibold text-white">{item.title}</span>
          {unread && <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-blue-400" aria-label="Non letta" />}
        </span>
        <span className="mt-0.5 block text-[11px] leading-4 text-[#aeb4c0]">{item.message}</span>
        <span className="mt-1 block text-[10px] text-white/40">{relativeDate(item.createdAt)}</span>
      </span>
    </Link>
  );
}

export function NotificationCenter() {
  const [open, setOpen] = useState(false);
  const { items, unreadCount, isRead, markRead, markAllRead } = useNotifications();

  return (
    <div className="relative">
      <button
        type="button"
        onClick={() => setOpen((value) => !value)}
        className="relative rounded-xl p-2 text-[#9aa0aa] transition-colors hover:bg-white/[0.06] hover:text-white"
        aria-label={unreadCount > 0 ? `Notifiche, ${unreadCount} non lette` : "Notifiche"}
        aria-expanded={open}
        data-testid="notification-center-toggle"
      >
        <Bell size={19} aria-hidden="true" />
        {unreadCount > 0 && (
          <span className="absolute -right-0.5 -top-0.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-red-500 px-1 text-[9px] font-bold text-white">
            {unreadCount > 99 ? "99+" : unreadCount}
          </span>
        )}
      </button>

      {open && (
        <div className="absolute right-0 top-11 z-40 w-[min(24rem,calc(100vw-2rem))] overflow-hidden rounded-2xl border border-white/[0.12] bg-[#171722] shadow-[0_16px_48px_rgba(0,0,0,0.45)]" role="dialog" aria-label="Centro notifiche" data-testid="notification-center-panel">
          <div className="flex items-center justify-between border-b border-white/[0.08] px-4 py-3">
            <div>
              <h2 className="text-[13px] font-bold text-white">Notifiche</h2>
              <p className="text-[10px] text-white/45">Pubblicazioni, errori e copyright</p>
            </div>
            {unreadCount > 0 && (
              <button type="button" onClick={markAllRead} className="text-[10px] font-semibold text-blue-300 hover:text-blue-200">
                Segna tutte lette
              </button>
            )}
          </div>
          {items.length === 0 ? (
            <div className="flex flex-col items-center gap-2 px-5 py-10 text-center">
              <AlertTriangle size={20} className="text-white/30" aria-hidden="true" />
              <p className="text-[12px] text-[#aeb4c0]">Nessuna notifica recente.</p>
            </div>
          ) : (
            <div className="max-h-[26rem] overflow-y-auto">
              {items.map((item) => (
                <NotificationRow
                  key={item.id}
                  item={item}
                  unread={!isRead(item.id)}
                  onRead={() => markRead(item.id)}
                  onClose={() => setOpen(false)}
                />
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
