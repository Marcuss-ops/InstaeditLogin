import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { ChevronDown, User, Check } from "lucide-react";
import { authedFetch, AuthError, fetchSession } from "../../lib/auth";
import { getProvider, type ProviderId } from "../../lib/providers";
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
      const response = await authedFetch("/api/v1/accounts");
      const data = (await response.json()) as { accounts: PlatformAccount[] };
      setState({ kind: "ready", accounts: data.accounts ?? [] });
      const firstPublishable = data.accounts.find(isPublishableAccount);
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
          "flex items-center gap-2 pl-3 pr-2 py-1.5 rounded-full border transition-colors",
          isOpen
            ? "bg-white/[0.08] border-white/[0.16]"
            : "bg-transparent border-white/[0.08] hover:bg-white/[0.04] hover:border-white/[0.16]",
        )}
      >
        <div className="w-7 h-7 rounded-full bg-gradient-to-br from-[#0A84FF] to-[#7B61FF] flex items-center justify-center text-white text-[11px] font-bold">
          {displayInitial ? (
            displayInitial
          ) : (
            <User size={14} />
          )}
        </div>
        <span className="hidden sm:inline text-[13px] font-medium text-white max-w-[120px] truncate">
          {displayName ?? "Account"}
        </span>
        <ChevronDown
          size={14}
          className={cn(
            "text-[#9aa0aa] transition-transform duration-200",
            isOpen && "rotate-180",
          )}
        />
      </button>

      {isOpen && (
        <div
          id="account-switcher-menu"
          role="menu"
          aria-labelledby="account-switcher-button"
          className="absolute right-0 top-full mt-2 w-72 bg-[#1f1f2e] border border-white/[0.12] rounded-2xl shadow-[0_8px_32px_rgba(0,0,0,0.4)] overflow-hidden z-50"
        >
          <div className="p-3 border-b border-white/[0.08]">
            <p className="text-[11px] font-semibold text-[#9aa0aa] uppercase tracking-wider">
              Connected accounts
            </p>
          </div>

          {state.kind === "loading" && (
            <div className="p-3 space-y-2">
              {Array.from({ length: 3 }).map((_, i) => (
                <div
                  key={i}
                  className="h-10 rounded-xl bg-white/[0.06] animate-pulse"
                />
              ))}
            </div>
          )}

          {state.kind === "error" && (
            <div className="p-4 text-[13px] text-[#9aa0aa]">
              Unable to load accounts.
            </div>
          )}

          {state.kind === "ready" && (
            <>
              {state.accounts.length === 0 ? (
                <div className="p-4 text-[13px] text-[#9aa0aa]">
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
                          isSelected
                            ? "bg-white/[0.08]"
                            : "hover:bg-white/[0.04]",
                          !publishable && "cursor-not-allowed opacity-60",
                        )}
                      >
                        <div
                          className={cn(
                            "w-9 h-9 rounded-lg bg-gradient-to-br flex items-center justify-center text-white shrink-0",
                            provider?.color ?? "from-[#9aa0aa] to-[#6b7280]",
                          )}
                        >
                          {provider?.icon ?? <User size={16} />}
                        </div>
                        <div className="min-w-0 flex-1">
                          <p className="text-[13px] font-semibold text-white truncate">
                            @{account.username}
                          </p>
                          <p className="text-[11px] text-[#9aa0aa] truncate">
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

          <div className="p-2 border-t border-white/[0.08]">
            <Link
              to="/app/linking"
              onClick={() => setIsOpen(false)}
              className="flex items-center justify-center gap-2 w-full px-4 py-2 rounded-xl text-[13px] font-semibold text-white bg-white/[0.04] hover:bg-white/[0.08] transition-colors no-underline"
            >
              Manage accounts
            </Link>
          </div>
        </div>
      )}
    </div>
  );
}
