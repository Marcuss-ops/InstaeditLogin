import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { ChevronDown, User, Check } from "lucide-react";
import { AuthError, fetchSession } from "../../lib/auth";
import { listAllAccounts } from "../../features/channels/api/channelsApi";
import { getProvider, type ProviderId } from "../../lib/providers";
import { ProviderBadge } from "../../components/brand/PlatformLogos";
import { cn } from "../../lib/utils";
import {
  accountStateLabel,
  isPublishableAccount,
  type AccountState,
} from "../../types/uploads";

type PlatformAccount = {
  id: number;
  platform: ProviderId;
  username: string;
  status: string;
  account_state?: AccountState;
  is_publishable?: boolean;
  created_at: string;
};

type FetchState =
  | { kind: "loading" }
  | { kind: "ready"; accounts: PlatformAccount[] }
  | { kind: "error" };

export function AccountSwitcher() {
  const [isOpen, setIsOpen] = useState(false);
  const [state, setState] = useState<FetchState>({ kind: "loading" });
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [sessionName, setSessionName] = useState<string | null>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  const loadAccounts = useCallback(async () => {
    try {
      const accounts = (await listAllAccounts()) as PlatformAccount[];
      setState({ kind: "ready", accounts });
      const firstPublishable = accounts.find(isPublishableAccount);
      if (firstPublishable) {
        setSelectedId((prev) => (prev === null ? firstPublishable.id : prev));
      }
    } catch (err) {
      if (err instanceof AuthError) {
        return;
      }
      setState({ kind: "error" });
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const session = await fetchSession();
      if (cancelled) return;
      if (!session) return;
      // The header shows the logged-in InstaEdit account (name or email),
      // NOT the first linked channel — the dropdown below still lists the
      // connected channels for quick switching.
      setSessionName(session.name || session.email || null);
      void loadAccounts();
    })();
    return () => {
      cancelled = true;
    };
  }, [loadAccounts]);

  useEffect(() => {
    if (!isOpen) return;
    const handleClickOutside = (event: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        setIsOpen(false);
      }
    };
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setIsOpen(false);
      }
    };
    document.addEventListener("mousedown", handleClickOutside);
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("mousedown", handleClickOutside);
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [isOpen]);

  const activeAccount =
    state.kind === "ready"
      ? state.accounts.find((a) => a.id === selectedId && isPublishableAccount(a)) ??
        state.accounts.find(isPublishableAccount) ??
        null
      : null;

  const displayName = sessionName ?? activeAccount?.username ?? null;
  const displayInitial = sessionName
    ? sessionName.charAt(0).toUpperCase()
    : activeAccount?.username.charAt(0).toUpperCase();

  const handleSelect = (account: PlatformAccount) => {
    if (!isPublishableAccount(account)) return;
    setSelectedId(account.id);
    setIsOpen(false);
  };

  return (
    <div ref={containerRef} className="relative">
      <button
        id="account-switcher-button"
        type="button"
        onClick={() => setIsOpen((prev) => !prev)}
        aria-expanded={isOpen}
        aria-haspopup="menu"
        aria-controls="account-switcher-menu"
        title={displayName ?? undefined}
        className={cn(
          "app-account-button flex items-center gap-2 pl-3 pr-2 py-1.5 rounded-full border transition-colors",
          isOpen
            ? "app-account-button-open"
            : "",
        )}
      >
        <div className="w-7 h-7 rounded-full bg-gradient-to-br from-[#0A84FF] to-[#1f2937] flex items-center justify-center text-white text-[11px] font-bold">
          {displayInitial ? (
            displayInitial
          ) : (
            <User size={14} />
          )}
        </div>
        <span className="hidden sm:inline text-[13px] font-medium max-w-[120px] truncate app-account-name">
          {displayName ?? "Account"}
        </span>
        <ChevronDown
          size={14}
          className={cn(
            "app-account-chevron transition-transform duration-200",
            isOpen && "rotate-180",
          )}
        />
      </button>

      {isOpen && (
        <div
          id="account-switcher-menu"
          role="menu"
          aria-labelledby="account-switcher-button"
          className="app-account-menu absolute right-0 top-full mt-2 w-72 rounded-2xl overflow-hidden z-50"
        >
          <div className="app-account-menu-header p-3">
            <p className="app-account-muted text-[11px] font-semibold uppercase tracking-wider">
              Connected accounts
            </p>
          </div>

          {state.kind === "loading" && (
            <div className="p-3 space-y-2">
              {Array.from({ length: 3 }).map((_, i) => (
                <div
                  key={i}
                  className="app-account-skeleton h-10 rounded-xl animate-pulse"
                />
              ))}
            </div>
          )}

          {state.kind === "error" && (
            <div className="p-4 text-[13px] app-account-muted">
              Unable to load accounts.
            </div>
          )}

          {state.kind === "ready" && (
            <>
              {state.accounts.length === 0 ? (
                <div className="p-4 text-[13px] app-account-muted">
                  No accounts connected.
                </div>
              ) : (
                <div className="p-2 max-h-[320px] overflow-y-auto">
                  {state.accounts.map((account) => {
                    const provider = getProvider(account.platform);
                    const isSelected = account.id === selectedId;
                    const publishable = isPublishableAccount(account);
                    return (
                      <button
                        key={account.id}
                        role="menuitem"
                        type="button"
                        onClick={() => handleSelect(account)}
                        disabled={!publishable}
                        aria-disabled={!publishable}
                        className={cn(
                          "flex items-center gap-3 w-full p-2.5 rounded-xl transition-colors text-left",
                          isSelected ? "app-account-row-selected" : "app-account-row-hover",
                          !publishable && "cursor-not-allowed opacity-60",
                        )}
                      >
                        <div
                          className={cn(
                            "w-9 h-9 rounded-lg bg-gradient-to-br flex items-center justify-center text-white shrink-0",
                            provider?.color ?? "from-[#9aa0aa] to-[#6b7280]",
                          )}
                        >
                          {provider ? (
                            <ProviderBadge
                              platform={provider.id}
                              className="h-9 w-9 justify-center rounded-lg border-0"
                              compact
                              logoClassName="h-6 w-6"
                            />
                          ) : (
                            <User size={16} />
                          )}
                        </div>
                        <div className="min-w-0 flex-1">
                          <p className="app-account-name text-[13px] font-semibold truncate">
                            @{account.username}
                          </p>
                          <p className="app-account-muted text-[11px] truncate">
                            {provider?.name ?? account.platform} · {accountStateLabel(account)}
                          </p>
                        </div>
                        {isSelected && <Check size={14} className="text-emerald-400 shrink-0" />}
                      </button>
                    );
                  })}
                </div>
              )}
            </>
          )}

          <div className="app-account-menu-footer p-2">
            <Link
              to="/app/linking"
              onClick={() => setIsOpen(false)}
              className="app-account-manage flex items-center justify-center gap-2 w-full px-4 py-2 rounded-xl text-[13px] font-semibold transition-colors no-underline"
            >
              Manage accounts
            </Link>
          </div>
        </div>
      )}
    </div>
  );
}
