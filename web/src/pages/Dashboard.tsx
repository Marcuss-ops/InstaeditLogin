import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { ChevronRight, RefreshCw, Sparkles } from "lucide-react";
import { Nav } from "../components/Nav";
import { Skeleton, ErrorState } from "../components/feedback";
import { getProvider, type ProviderId } from "../lib/providers";
import {
  ApiError,
  AuthError,
  authedFetch,
  clearSessionCache,
  fetchSession,
} from "../lib/auth";

type PlatformAccount = {
  id: number;
  user_id: number;
  platform: ProviderId;
  platform_user_id: string;
  username: string;
  created_at: string;
  updated_at: string;
};

type FetchState =
  | { kind: "loading" }
  | { kind: "backend_offline" }
  | { kind: "empty"; name: string }
  | { kind: "ready"; name: string; accounts: PlatformAccount[] }
  | { kind: "error"; message: string };

const NEW_BADGE_HOURS = 24;
const REQUEST_ID = () => crypto.randomUUID?.()?.slice(0, 8) ?? Math.random().toString(36).slice(2, 10);

function isFresh(createdAt: string): boolean {
  const created = new Date(createdAt).getTime();
  if (Number.isNaN(created)) return false;
  return Date.now() - created < NEW_BADGE_HOURS * 60 * 60 * 1000;
}

function formatJoined(createdAt: string): string {
  const created = new Date(createdAt);
  if (Number.isNaN(created.getTime())) return "recently";
  return created.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
  });
}

export function Dashboard() {
  const navigate = useNavigate();
  const [state, setState] = useState<FetchState>({ kind: "loading" });
  const [sessionName, setSessionName] = useState<string>("");
  const abortRef = useRef<AbortController | null>(null);

  const loadAccounts = useCallback(async (nameFallback = "") => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;

    if (controller.signal.aborted) return;
    setState({ kind: "loading" });

    try {
      const response = await authedFetch("/api/v1/accounts", {
        signal: controller.signal,
      });
      if (controller.signal.aborted) return;
      const data = (await response.json()) as { accounts: PlatformAccount[] };
      const accounts = data.accounts ?? [];
      setState(
        accounts.length === 0
          ? { kind: "empty", name: nameFallback }
          : { kind: "ready", name: nameFallback, accounts },
      );
    } catch (err) {
      if (controller.signal.aborted) return;
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      // AbortError from the controller cancels silently.
      if (err instanceof DOMException && err.name === "AbortError") {
        return;
      }
      // Network-level failures (TypeError on fetch) = backend offline.
      if (err instanceof TypeError || (err instanceof Error && err.message.toLowerCase().includes("fetch"))) {
        setState({ kind: "backend_offline" });
        return;
      }
      const message = err instanceof ApiError ? err.message : "Unable to reach the API.";
      setState({ kind: "error", message });
    }
  }, [navigate]);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const session = await fetchSession();
      if (cancelled) return;
      if (!session) {
        clearSessionCache();
        navigate("/login", { replace: true });
        return;
      }
      setSessionName(session.name ?? "");
      void loadAccounts(session.name ?? "");
    })();
    return () => {
      cancelled = true;
      abortRef.current?.abort();
    };
  }, [loadAccounts, navigate]);

  // The OAuth post-callback toast is surfaced on /connections (see
  // Connections.tsx); /accounts is now only the "what's connected"
  // view. The 7 unconnected provider cards were moved to
  // /connections as part of the Login-→-OAuth migration.
  const accountsByProvider: Record<string, PlatformAccount | undefined> = {};
  if (state.kind === "ready") {
    for (const acc of state.accounts) {
      accountsByProvider[acc.platform] = acc;
    }
  }

  return (
    <div className="min-h-screen bg-neutral-50 flex flex-col">
      <Nav />
      <div className="max-w-[1100px] mx-auto px-6 w-full">
        {/* Heading */}
        <div className="flex flex-col items-center justify-center py-8">
          <div className="w-14 h-14 rounded-2xl bg-gradient-to-br from-blue-500 to-violet-500 flex items-center justify-center mb-5 shadow-[0_8px_24px_rgba(123,97,255,0.25)]">
            <Sparkles size={26} className="text-white" />
          </div>
          <h1 className="text-[clamp(28px,4vw,38px)] font-extrabold tracking-[-0.02em] mb-2 text-black text-center">
            {sessionName ? `Welcome, ${sessionName}` : "Your accounts"}
          </h1>
          <p className="text-neutral-500 text-[16px] text-center max-w-[480px]">
            Manage connected accounts, publish content, and track your reach.
          </p>
        </div>

        {/* Connected accounts */}
        <section className="pb-12">
          <div className="flex items-center justify-between mb-5">
            <h2 className="text-[20px] font-bold tracking-[-0.01em] text-black">
              Connected accounts
            </h2>
            {state.kind === "ready" && (
              <button
                type="button"
                onClick={() => void loadAccounts()}
                className="inline-flex items-center gap-1.5 text-[13px] text-neutral-500 hover:text-black transition-colors"
              >
                <RefreshCw size={13} />
                Refresh
              </button>
            )}
          </div>

          {state.kind === "loading" && (
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4" data-testid="accounts-loading">
              <Skeleton variant="card" height={88} />
              <Skeleton variant="card" height={88} />
            </div>
          )}

          {state.kind === "error" && (
            <ErrorState
              title="Couldn't load accounts"
              message={state.message}
              onRetry={() => void loadAccounts()}
              retryLabel="Try again"
            />
          )}

          {state.kind === "backend_offline" && (
            <div className="bg-white border border-neutral-200 rounded-xl p-8 text-center">
              <p className="text-red-500 font-semibold text-[15px] mb-1">
                Backend unavailable
              </p>
              <p className="text-[13px] text-neutral-500 mb-2 font-mono">
                Request ID: {REQUEST_ID()}
              </p>
              <button
                type="button"
                onClick={() => void loadAccounts()}
                className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-black text-white text-[14px] font-semibold hover:bg-neutral-800 transition-colors"
              >
                <RefreshCw size={14} />
                Retry
              </button>
            </div>
          )}

          {(state.kind === "empty" || state.kind === "ready") && (
            <>
              {state.kind === "ready" && state.accounts.length > 0 && (
                <div
                  className="grid grid-cols-1 sm:grid-cols-2 gap-4"
                  data-testid="dashboard-connected-grid"
                >
                  {state.accounts.map((account) => {
                    const provider = getProvider(account.platform);
                    if (!provider) return null;
                    const fresh = isFresh(account.created_at);
                    return (
                      <div
                        key={account.id}
                        data-testid={`connected-card-${account.platform}`}
                        className="relative bg-white border border-neutral-200 rounded-xl p-5 hover:border-neutral-400 hover:shadow-[0_8px_24px_rgba(0,0,0,0.05)] transition-all overflow-hidden"
                      >
                        <div
                          className={`absolute top-0 left-0 right-0 h-1 bg-gradient-to-r ${provider.color}`}
                        />
                        <div className="flex items-center gap-4">
                          <div
                            className={`w-12 h-12 rounded-xl bg-gradient-to-br ${provider.color} flex items-center justify-center text-white shrink-0`}
                          >
                            {provider.icon}
                          </div>
                          <div className="flex-1 min-w-0">
                            <div className="flex items-center gap-2 mb-1">
                              <h3 className="font-bold text-[15px] text-black truncate">
                                {provider.name}
                              </h3>
                              {fresh && (
                                <span className="px-2 py-0.5 rounded-full bg-green-50 border border-green-200 text-green-700 text-[11px] font-semibold uppercase tracking-wider">
                                  New
                                </span>
                              )}
                            </div>
                            <p className="text-[13px] text-neutral-500 truncate">
                              @{account.username || "—"}
                            </p>
                            <p className="text-[11px] text-neutral-400 mt-1">
                              Joined {formatJoined(account.created_at)}
                            </p>
                          </div>
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}

              {/* "Connect more" CTA — the 7 unconnected provider cards
                  moved to /connections (see Connections.tsx). */}
              <div className="mt-6 flex justify-center">
                <button
                  type="button"
                  onClick={() => navigate("/connections")}
                  data-testid="dashboard-connect-more"
                  className="inline-flex items-center gap-2 px-5 py-2.5 rounded-xl bg-black text-white text-[14px] font-semibold hover:bg-neutral-800 transition-colors"
                >
                  Connect more accounts
                  <ChevronRight size={14} />
                </button>
              </div>
            </>
          )}
        </section>
      </div>
    </div>
  );
}
