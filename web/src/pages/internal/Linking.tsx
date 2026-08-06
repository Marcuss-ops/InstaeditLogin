import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import {
  Link2,
  RefreshCw,
  CheckCircle2,
  ChevronDown,
  Plus,
  FolderInput,
} from "lucide-react";
import { authedFetch, AuthError, fetchSession } from "../../lib/auth";
import { listAllAccounts } from "../../features/channels/api/channelsApi";
import { API_BASE_URL } from "../../lib/api";
import { PROVIDERS, type ProviderId } from "../../lib/providers";
import { ErrorState } from "../../components/feedback";
import { cn } from "../../lib/utils";
import { DriveBatchImportDialog } from "./DriveBatchImportDialog";
import {
  accountStateLabel,
  isPublishableAccount,
} from "../../types/uploads";

type PlatformAccount = {
  id: number;
  platform: ProviderId;
  platform_user_id: string;
  username: string;
  avatar_url?: string;
  status: string;
  account_state?: "valid" | "reconnect_required" | "suspended" | "deleted";
  is_publishable?: boolean;
  created_at: string;
};

type FetchState =
  | { kind: "loading" }
  | { kind: "ready"; accounts: PlatformAccount[] }
  | { kind: "error"; message: string };

function AccountAvatar({ account }: { account: PlatformAccount }) {
  const [imageFailed, setImageFailed] = useState(false);
  const initial = account.username?.charAt(0).toUpperCase() || "?";

  return (
    <div className="w-8 h-8 rounded-full bg-gradient-to-br from-white/[0.18] to-white/[0.06] flex items-center justify-center text-white shrink-0 text-[11px] font-bold overflow-hidden">
      {account.avatar_url && !imageFailed ? (
        <img
          src={account.avatar_url}
          alt={`${account.username} avatar`}
          className="w-full h-full object-cover"
          referrerPolicy="no-referrer"
          onError={() => setImageFailed(true)}
        />
      ) : (
        initial
      )}
    </div>
  );
}

const LINKABLE_IDS: ProviderId[] = [
  "youtube",
  "tiktok",
  "facebook",
  "instagram",
  "threads",
  "google-drive",
];

export function InternalLinking() {
  const navigate = useNavigate();
  const [state, setState] = useState<FetchState>({ kind: "loading" });
  const abortRef = useRef<AbortController | null>(null);
  // "Refresh all channels" state: enqueues a background snapshot refresh
  // for every account (POST /accounts/sync-all) and reloads the list. The
  // actual provider calls happen in the sweep worker with bounded
  // concurrency — never one request per channel from the browser.
  const [syncingAll, setSyncingAll] = useState(false);
  const [syncAllNotice, setSyncAllNotice] = useState<string | null>(null);

  const loadAccounts = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    setState({ kind: "loading" });

    try {
      // listAllAccounts returns the uploads PlatformAccount shape (platform:
      // string); the page's local type narrows it to ProviderId for the card
      // grid, so the assertion is required (not redundant).
      const accounts = (await listAllAccounts(controller.signal)) as PlatformAccount[];
      if (controller.signal.aborted) return;
      // The list endpoint is the single source of truth for the cards: it
      // already returns avatar_url (cached snapshot fallback) and
      // snapshot_stale per account via the aggregated backend query. Missing
      // avatars render as the account-initial placeholder below; the page
      // NEVER fires per-account /accounts/{id} requests (N+1 fan-out gone).
      if (controller.signal.aborted) return;
      setState({ kind: "ready", accounts });
    } catch (err) {
      if (controller.signal.aborted) return;
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      const message = err instanceof Error ? err.message : "Unable to load accounts.";
      setState({ kind: "error", message });
    }
  }, [navigate]);

  const syncAllAccounts = useCallback(async () => {
    setSyncingAll(true);
    setSyncAllNotice(null);
    try {
      const response = await authedFetch("/api/v1/accounts/sync-all", {
        method: "POST",
      });
      const data = (await response.json()) as { status?: string; count?: number };
      const count = typeof data.count === "number" ? data.count : 0;
      setSyncAllNotice(
        count > 0
          ? `Refresh scheduled for ${count} channel${count === 1 ? "" : "s"} — snapshots update in the background.`
          : "No channels to refresh.",
      );
      // Reload the list so freshly written snapshots appear as the worker
      // finishes; the N+1 fan-out stays gone (single batch request).
      await loadAccounts();
    } catch {
      setSyncAllNotice("Could not schedule the refresh. Please try again.");
    } finally {
      setSyncingAll(false);
    }
  }, [loadAccounts]);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const session = await fetchSession();
      if (cancelled) return;
      if (!session) {
        navigate("/login", { replace: true });
        return;
      }
      void loadAccounts();
    })();
    return () => {
      cancelled = true;
      abortRef.current?.abort();
    };
  }, [loadAccounts, navigate]);

  const [expandedProvider, setExpandedProvider] = useState<ProviderId | null>(null);
  const [isBatchOpen, setIsBatchOpen] = useState(false);

  const groupedAccounts = useMemo(() => {
    const grouped: Partial<Record<ProviderId, PlatformAccount[]>> = {};
    if (state.kind === "ready") {
      for (const acc of state.accounts) {
        if (!grouped[acc.platform]) {
          grouped[acc.platform] = [];
        }
        grouped[acc.platform]!.push(acc);
      }
    }
    return grouped;
  }, [state]);

  const linkableProviders = PROVIDERS.filter((p) => LINKABLE_IDS.includes(p.id));

  return (
    <div className="min-h-full p-8 bg-[#030308] text-[#e8e8ef]">
      <div className="max-w-5xl mx-auto">
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-8">
          <div>
            <h1 className="text-[28px] font-extrabold tracking-[-0.02em] text-white flex items-center gap-3">
              <Link2 size={28} className="text-white/40" />
              Linking
            </h1>
            <p className="text-[15px] text-[#9aa0aa] mt-1">
              Connect your social profiles to publish from a single inbox.
            </p>
          </div>
          {state.kind === "ready" && (
            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={() => setIsBatchOpen(true)}
                data-testid="open-drive-batch"
                className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white text-black text-[13px] font-semibold hover:bg-white/90 transition-colors shadow-[0_2px_8px_rgba(255,255,255,0.10)]"
              >
                <FolderInput size={14} /> Auto-post Drive folder
              </button>
              <button
                type="button"
                onClick={() => void syncAllAccounts()}
                disabled={syncingAll}
                data-testid="sync-all-accounts"
                className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors disabled:opacity-50 disabled:pointer-events-none"
              >
                <RefreshCw size={14} className={cn(syncingAll && "animate-spin")} />
                {syncingAll ? "Scheduling…" : "Refresh all channels"}
              </button>
              <button
                type="button"
                onClick={() => void loadAccounts()}
                className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors"
              >
                <RefreshCw size={14} /> Refresh
              </button>
            </div>
          )}
        </div>

        {syncAllNotice && (
          <p
            className="text-[12px] text-[#9aa0aa] -mt-4 mb-6"
            data-testid="sync-all-notice"
          >
            {syncAllNotice}
          </p>
        )}

        {state.kind === "loading" && (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
            {Array.from({ length: 5 }).map((_, i) => (
              <div key={i} className="h-32 rounded-2xl bg-white/[0.06] animate-pulse" />
            ))}
          </div>
        )}

        {state.kind === "error" && (
          <ErrorState
            title="Couldn't load providers"
            message={state.message}
            onRetry={() => void loadAccounts()}
            className="bg-[#1f1f2e] border-white/[0.12]"
          />
        )}

        {state.kind === "ready" && (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
            {linkableProviders.map((provider) => {
              const accounts = groupedAccounts[provider.id] ?? [];
              const isConnected = accounts.length > 0;
              const hasPublishableAccount = accounts.some(isPublishableAccount);
              const isExpanded = expandedProvider === provider.id;
              return (
                <div
                  key={provider.id}
                  className={cn(
                    "relative surface-card bg-[#1f1f2e] border rounded-2xl transition-all overflow-hidden block",
                    isConnected
                      ? "border-white/[0.12]"
                      : "border-dashed border-white/[0.16] hover:border-white/[0.30] hover:shadow-[0_8px_32px_rgba(0,0,0,0.4)]",
                  )}
                >
                  <div
                    className={cn(
                      "absolute top-0 left-0 right-0 h-1 bg-gradient-to-r",
                      provider.color,
                    )}
                  />
                  <button
                    type="button"
                    onClick={() => setExpandedProvider(isExpanded ? null : provider.id)}
                    className="w-full text-left p-5"
                  >
                    <div className="flex items-start justify-between gap-4">
                      <div className="flex items-center gap-4">
                        <div
                          className={cn(
                            "w-12 h-12 rounded-xl bg-gradient-to-br flex items-center justify-center text-white shrink-0",
                            provider.color,
                          )}
                        >
                          {provider.icon}
                        </div>
                        <div>
                          <h3 className="font-bold text-[15px] text-white">{provider.name}</h3>
                          <p className="text-[13px] text-[#9aa0aa] mt-0.5">
                            {isConnected
                              ? `${accounts.length} account${accounts.length === 1 ? "" : "s"} connected`
                              : "Not connected"}
                          </p>
                        </div>
                      </div>
                      <div className="flex items-center gap-2">
                        {isConnected && hasPublishableAccount && (
                          <span className="inline-flex items-center gap-1 px-2 py-1 rounded-full bg-emerald-500/[0.08] border border-emerald-500/[0.15] text-emerald-400 text-[11px] font-semibold">
                            <CheckCircle2 size={11} /> Connected
                          </span>
                        )}
                        {isConnected && !hasPublishableAccount && (
                          <span className="inline-flex items-center gap-1 px-2 py-1 rounded-full bg-amber-500/[0.08] border border-amber-500/[0.15] text-amber-300 text-[11px] font-semibold">
                            Attention required
                          </span>
                        )}
                        <ChevronDown
                          size={16}
                          className={cn(
                            "text-[#9aa0aa] transition-transform duration-200",
                            isExpanded && "rotate-180",
                          )}
                        />
                      </div>
                    </div>
                  </button>

                  {isExpanded && (
                    <div className="px-5 pb-5 pt-0">
                      <div className="border-t border-white/[0.08] pt-4 mt-0">
                        {isConnected ? (
                          <div className="space-y-2">
                            {accounts.map((account) => (
                              <Link
                                key={account.id}
                                to={`/app/dashboard-channels/${account.id}`}
                                className={`flex items-center justify-between gap-3 p-3 rounded-xl border transition-colors no-underline ${account.is_publishable === false ? "bg-amber-500/[0.05] border-amber-400/[0.18]" : "bg-white/[0.04] border-white/[0.08] hover:bg-white/[0.08]"}`}
                              >
                                <div className="flex items-center gap-3 min-w-0">
                                  <AccountAvatar account={account} />
                                  <div className="min-w-0">
                                    <p className="text-[13px] font-semibold text-white truncate">
                                      @{account.username}
                                    </p>
                                    <p className="text-[11px] text-[#9aa0aa]">
                                      {accountStateLabel(account)} · Linked on {new Date(account.created_at).toLocaleDateString()}
                                    </p>
                                  </div>
                                </div>
                                <RefreshCw
                                  size={14}
                                  className="text-[#9aa0aa] shrink-0"
                                />
                              </Link>
                            ))}
                            <a
                              href={`${API_BASE_URL}/api/v1/auth/${provider.id}/login?mode=add`}
                              onClick={(e) => e.stopPropagation()}
                              className="flex items-center justify-center gap-2 w-full px-4 py-2.5 rounded-xl border border-dashed border-white/[0.16] text-[13px] font-semibold text-[#9aa0aa] hover:bg-white/[0.04] hover:text-white transition-colors no-underline"
                            >
                              <Plus size={14} /> Connect another {provider.name} account
                            </a>
                          </div>
                        ) : (
                          <a
                            href={`${API_BASE_URL}/api/v1/auth/${provider.id}/login?mode=add`}
                            className="inline-flex items-center justify-center w-full px-4 py-2.5 rounded-xl bg-white text-black text-[13px] font-semibold hover:bg-white/90 transition-colors no-underline"
                          >
                            Connect {provider.name}
                          </a>
                        )}
                      </div>
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </div>
      <DriveBatchImportDialog
        open={isBatchOpen}
        onClose={() => setIsBatchOpen(false)}
      />
    </div>
  );
}
