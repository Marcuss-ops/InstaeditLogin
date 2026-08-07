import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import {
  ChevronDown,
  Clock,
  LayoutDashboard,
  Link2,
  Plus,
} from "lucide-react";
import { Skeleton, ErrorState } from "../../components/feedback";
import { useDashboardData } from "./useDashboardData";
import { API_BASE_URL } from "../../lib/api";
import { PROVIDERS, type ProviderId } from "../../lib/providers";
import { ProviderBadge } from "../../components/brand/PlatformLogos";
import { cn } from "../../lib/utils";

const LINKABLE_IDS: ProviderId[] = [
  "youtube",
  "tiktok",
  "facebook",
  "instagram",
  "threads",
  "google-drive",
];

// "Add channel" picker: same OAuth add flow used by the Linking page, so a
// channel can be connected straight from the analytics dashboard.
function AddChannelMenu() {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const closeOnOutsidePointer = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("pointerdown", closeOnOutsidePointer);
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("pointerdown", closeOnOutsidePointer);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);

  const linkableProviders = PROVIDERS.filter((provider) => LINKABLE_IDS.includes(provider.id));

  return (
    <div ref={rootRef} className="relative">
      <button
        type="button"
        onClick={() => setOpen((current) => !current)}
        aria-haspopup="menu"
        aria-expanded={open}
        data-testid="dashboard-add-channel"
        className="inline-flex items-center gap-1.5 rounded-xl bg-white px-4 py-2 text-[13px] font-semibold text-black shadow-[0_2px_8px_rgba(255,255,255,0.10)] transition-colors hover:bg-white/90"
      >
        <Plus size={14} aria-hidden="true" /> Aggiungi canale
        <ChevronDown size={13} className={cn("transition-transform", open && "rotate-180")} aria-hidden="true" />
      </button>
      {open ? (
        <div
          role="menu"
          aria-label="Aggiungi canale"
          className="absolute right-0 top-[calc(100%+0.5rem)] z-30 min-w-[220px] overflow-hidden rounded-xl border border-white/15 bg-[#171722]/95 p-1.5 shadow-[0_18px_50px_-18px_rgba(0,0,0,0.9)] backdrop-blur-xl"
        >
          {linkableProviders.map((provider) => (
            <a
              key={provider.id}
              role="menuitem"
              href={`${API_BASE_URL}/api/v1/auth/${provider.id}/login?mode=add`}
              onClick={() => setOpen(false)}
              className="flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-[12px] text-zinc-300 no-underline transition-colors hover:bg-white/10 hover:text-white"
            >
              <ProviderBadge
                platform={provider.id}
                className="h-5 w-5 shrink-0 justify-center rounded"
                compact
                logoClassName="h-3.5 w-3.5"
              />
              {provider.name}
            </a>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function StatCard({
  label,
  value,
  icon: Icon,
  to,
}: {
  label: string;
  value: number;
  icon: React.ElementType;
  to: string;
}) {
  return (
    <Link
      to={to}
      className="group surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-5 hover:border-white/[0.24] hover:shadow-[0_8px_32px_rgba(0,0,0,0.4)] transition-all no-underline block"
    >
      <div className="flex items-start justify-between">
        <div>
          <p className="text-[13px] font-medium text-[#9aa0aa] mb-1">{label}</p>
          <p className="text-[28px] font-extrabold tracking-tight text-white">{value}</p>
        </div>
        <div className="w-10 h-10 rounded-xl bg-white/[0.04] border border-white/[0.08] flex items-center justify-center text-[#9aa0aa] group-hover:bg-white group-hover:text-[#030308] transition-colors">
          <Icon size={20} />
        </div>
      </div>
    </Link>
  );
}

export function InternalDashboard() {
  const { state, refetch: load } = useDashboardData();

  const stats =
    state.kind === "ready"
      ? {
          connected: state.data.accounts.length,
          queuedUploads: state.data.pendingUploads,
        }
      : null;

  return (
    <div className="min-h-full p-8 bg-[#030308] text-[#e8e8ef]">
      <div className="max-w-6xl mx-auto">
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-8">
          <div>
            <h1 className="text-[28px] font-extrabold tracking-[-0.02em] text-white flex items-center gap-3">
              <LayoutDashboard size={28} className="text-white/40" />
              Dashboard
            </h1>
            <p className="text-[15px] text-[#9aa0aa] mt-1">
              Overview of your connected accounts and publishing activity.
            </p>
          </div>
          <AddChannelMenu />
        </div>

        {state.kind === "loading" && (
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 mb-8">
            <Skeleton variant="card" height={96} />
            <Skeleton variant="card" height={96} />
          </div>
        )}

        {state.kind === "error" && (
          <ErrorState
            title="Couldn't load dashboard"
            message={state.message}
            onRetry={() => void load()}
            className="mb-8 bg-[#1f1f2e] border-white/[0.12]"
          />
        )}

        {state.kind === "ready" && stats && (
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <StatCard label="Connected accounts" value={stats.connected} icon={Link2} to="/app/linking" />
            <div className="surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-5">
              <Link
                to={`/app/calendar?group_id=all`}
                className="group block no-underline"
              >
                <div className="flex items-start justify-between">
                  <div>
                    <p className="text-[13px] font-medium text-[#9aa0aa] mb-1">Upload in coda</p>
                    <p className="text-[28px] font-extrabold tracking-tight text-white">{stats.queuedUploads}</p>
                  </div>
                  <div className="w-10 h-10 rounded-xl bg-white/[0.04] border border-white/[0.08] flex items-center justify-center text-[#9aa0aa] group-hover:bg-white group-hover:text-[#030308] transition-colors">
                    <Clock size={20} />
                  </div>
                </div>
              </Link>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
