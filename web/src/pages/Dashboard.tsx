import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { AlertTriangle, Calendar, ChevronRight, LogOut, RefreshCw, Sparkles } from "lucide-react";
import { API_BASE_URL } from "../lib/supabase";
import { PROVIDERS, getProvider, type ProviderId } from "../lib/providers";
import {
  ApiError,
  AuthError,
  authedFetch,
  getJwt,
  getSession,
  logout,
  probeBackend,
  type ProbeResult,
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
  | { kind: "backend_offline"; probe: Extract<ProbeResult, { ok: false }> }
  | { kind: "empty"; name: string }
  | { kind: "ready"; name: string; accounts: PlatformAccount[] }
  | { kind: "error"; message: string };

const NEW_BADGE_HOURS = 24;
const DEFAULT_TIMEOUT_MS = 5000;

function isFresh(createdAt: string): boolean {
  const created = new Date(createdAt).getTime();
  if (Number.isNaN(created)) {
    return false;
  }
  return Date.now() - created < NEW_BADGE_HOURS * 60 * 60 * 1000;
}

function formatJoined(createdAt: string): string {
  const created = new Date(createdAt);
  if (Number.isNaN(created.getTime())) {
    return "recently";
  }
  return created.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
  });
}

export function Dashboard() {
  const location = useLocation();
  const navigate = useNavigate();
  const [state, setState] = useState<FetchState>({ kind: "loading" });
  const [sessionName, setSessionName] = useState<string>("");
  // Cancels in-flight probe/fetch on retry so Refresh-button mashing can't
  // race two responses into the state machine.
  const abortRef = useRef<AbortController | null>(null);

  const loadAccounts = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;

    if (controller.signal.aborted) {
      return;
    }
    setState({ kind: "loading" });
    // Probe the backend first so we can tell the user "the API host is down"
    // distinctly from "the API host says no accounts".
    const probe = await probeBackend(DEFAULT_TIMEOUT_MS, controller.signal);
    if (controller.signal.aborted) {
      return;
    }
    if (!probe.ok) {
      setState({ kind: "backend_offline", probe });
      return;
    }
    try {
      const response = await authedFetch("/api/v1/accounts", {
        signal: controller.signal,
      });
      if (controller.signal.aborted) {
        return;
      }
      const data = (await response.json()) as { accounts: PlatformAccount[] };
      const accounts = data.accounts ?? [];
      const name = getSession()?.name ?? "";
      setState(
        accounts.length === 0
          ? { kind: "empty", name }
          : { kind: "ready", name, accounts },
      );
    } catch (err) {
      if (controller.signal.aborted) {
        return;
      }
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      // AbortError from the controller cancels silently.
      if (err instanceof DOMException && err.name === "AbortError") {
        return;
      }
      const message = err instanceof ApiError ? err.message : "Unable to reach the API.";
      setState({ kind: "error", message });
    }
  }, [navigate]);

  useEffect(() => {
    if (!getJwt()) {
      navigate("/login", { replace: true });
      return;
    }
    const session = getSession();
    setSessionName(session?.name ?? "");
    void loadAccounts();
    return () => {
      abortRef.current?.abort();
    };
  }, [loadAccounts, navigate]);

  const justConnected = (location.state as { provider?: string } | null)?.provider;
  const accountsByProvider: Record<string, PlatformAccount | undefined> = {};
  if (state.kind === "ready") {
    for (const acc of state.accounts) {
      accountsByProvider[acc.platform] = acc;
    }
  }

  return (
    <div className="min-h-screen bg-neutral-50 flex flex-col">
      <div className="max-w-[1100px] mx-auto px-6 w-full">
        {/* Top bar */}
        <div className="flex items-center justify-between py-6">
          <Link
            to="/"
            className="text-sm font-medium text-neutral-500 hover:text-black transition-colors no-underline"
          >
            ← Back to home
          </Link>
          <button
            type="button"
            onClick={() => logout("/login")}
            className="inline-flex items-center gap-2 text-sm font-medium text-neutral-500 hover:text-black transition-colors"
          >
            <LogOut size={14} />
            Log out
          </button>
        </div>

        {/* Heading */}
        <div className="flex flex-col items-center justify-center py-8">
          <div className="w-14 h-14 rounded-2xl bg-gradient-to-br from-blue-500 to-violet-500 flex items-center justify-center mb-5 shadow-[0_8px_24px_rgba(123,97,255,0.25)]">
            <Sparkles size={26} className="text-white" />
          </div>
          <h1 className="text-[clamp(28px,4vw,38px)] font-extrabold tracking-[-0.02em] mb-2 text-black text-center">
            {sessionName ? `Welcome, ${sessionName}` : "Your dashboard"}
          </h1>
          <p className="text-neutral-500 text-[16px] text-center max-w-[480px]">
            Manage connected accounts, publish content, and track your reach — all from one place.
          </p>

          {justConnected && (
            <div className="mt-5 inline-flex items-center gap-2 px-4 py-2 rounded-full bg-green-50 border border-green-200 text-green-700 text-[13px] font-medium">
              <span className="w-2 h-2 rounded-full bg-green-500" />
              {getProvider(justConnected)?.name ?? justConnected} connected successfully
            </div>
          )}
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
            <div className="flex flex-col items-center justify-center py-20 gap-3">
              <div className="w-10 h-10 rounded-full border-4 border-neutral-200 border-t-black animate-spin" />
              <p className="text-[14px] text-neutral-500">Loading your accounts…</p>
            </div>
          )}

          {state.kind === "error" && (
            <div className="bg-white border border-neutral-200 rounded-xl p-8 text-center">
              <p className="text-red-500 font-semibold text-[15px] mb-1">
                Couldn't load accounts
              </p>
              <p className="text-[14px] text-neutral-500 mb-5">{state.message}</p>
              <button
                type="button"
                onClick={() => void loadAccounts()}
                className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-black text-white text-[14px] font-semibold hover:bg-neutral-800 transition-colors"
              >
                <RefreshCw size={14} />
                Try again
              </button>
            </div>
          )}

          {state.kind === "backend_offline" && (
            <div
              role="alert"
              aria-live="polite"
              className="bg-red-50 border border-red-200 rounded-xl p-6 flex items-start gap-3"
            >
              <AlertTriangle size={20} className="text-red-500 mt-0.5 shrink-0" />
              <div className="flex-1 min-w-0">
                <p className="text-red-700 font-bold text-[15px] mb-1">
                  Backend is unreachable
                </p>
                <p className="text-red-700/90 text-[13px] leading-relaxed mb-2">
                  {state.probe.message}
                </p>
                <p className="text-neutral-700 text-[13px] leading-relaxed font-mono break-all">
                  Probed: {state.probe.url}
                </p>
                <button
                  type="button"
                  onClick={() => void loadAccounts()}
                  className="mt-4 inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-red-600 text-white text-[13px] font-semibold hover:bg-red-700 transition-colors"
                >
                  <RefreshCw size={14} />
                  Retry probe
                </button>
              </div>
            </div>
          )}

          {(state.kind === "empty" || state.kind === "ready") && (
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              {PROVIDERS.map((provider) => {
                const account = accountsByProvider[provider.id];
                if (!account) {
                  return (
                    <a
                      key={provider.id}
                      href={`${API_BASE_URL}/api/v1/auth/${provider.id}/login`}
                      className="group relative bg-white border border-dashed border-neutral-300 rounded-xl p-5 no-underline text-black hover:border-neutral-500 hover:shadow-[0_8px_24px_rgba(0,0,0,0.05)] transition-all overflow-hidden"
                    >
                      <div className="flex items-center gap-4">
                        <div className={`w-12 h-12 rounded-xl border border-neutral-200 flex items-center justify-center text-neutral-400 group-hover:text-white group-hover:bg-gradient-to-br group-hover:${provider.color} transition-all`}>
                          {provider.icon}
                        </div>
                        <div className="flex-1 min-w-0">
                          <h3 className="font-bold text-[15px] mb-1 text-black">
                            {provider.name}
                          </h3>
                          <p className="text-[13px] text-neutral-500">Not connected</p>
                        </div>
                        <ChevronRight
                          size={18}
                          className="text-neutral-300 group-hover:text-black group-hover:translate-x-[2px] transition-all"
                        />
                      </div>
                    </a>
                  );
                }

                const fresh = isFresh(account.created_at);
                return (
                  <div
                    key={account.id}
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
        </section>

        {/* Quick actions (placeholder cards -> real upcoming features) */}
        {state.kind === "ready" && (
          <section className="pb-16">
            <h2 className="text-[20px] font-bold tracking-[-0.01em] text-black mb-5">
              Quick actions
            </h2>
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
              {[
                {
                  icon: <Calendar size={22} />,
                  title: "Schedule a post",
                  desc: "Calendar view across all connected platforms",
                },
                {
                  icon: <Sparkles size={22} />,
                  title: "AI-assisted drafts",
                  desc: "Generate captions and edits from prompts",
                },
                {
                  icon: <ChevronRight size={22} />,
                  title: "Connect more",
                  desc: "Add another platform to your stack",
                },
              ].map((card) => (
                <div
                  key={card.title}
                  className="bg-white border border-neutral-200 rounded-xl p-6 flex flex-col items-start gap-3 hover:shadow-[0_8px_24px_rgba(0,0,0,0.04)] transition-all"
                >
                  <div className="w-12 h-12 rounded-xl bg-neutral-100 flex items-center justify-center text-black">
                    {card.icon}
                  </div>
                  <div>
                    <h3 className="font-bold text-[15px] text-black mb-1">
                      {card.title}
                    </h3>
                    <p className="text-[13px] text-neutral-500">{card.desc}</p>
                  </div>
                </div>
              ))}
            </div>
          </section>
        )}
      </div>
    </div>
  );
}
