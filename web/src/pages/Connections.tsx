import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { ChevronRight, RefreshCw, Sparkles } from "lucide-react";
import { Nav } from "../components/Nav";
import { Skeleton, ErrorState } from "../components/feedback";
import { toastBus } from "../components/toast";
import { API_BASE_URL } from "../lib/api";
import { PROVIDERS, type ProviderId } from "../lib/providers";
import { DEMO_MODE } from "../lib/demo";
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
  | { kind: "ready"; accounts: PlatformAccount[] }
  | { kind: "error"; message: string };

const NEW_BADGE_HOURS = 24;

/**
 * /connections — connect (and view) social provider accounts.
 *
 * Previously these 7 buttons lived in /login, but the OAuth
 * callback requires an authenticated InstaEdit session; placing
 * them here keeps the Login flow email/password-only and
 * consolidates account linking behind a session gate.
 *
 * OAuth flow:
 *   1. User clicks a provider card → <a href="/api/v1/auth/{provider}/login">
 *      triggers a full-page navigation to the backend.
 *   2. Backend completes the OAuth dance, exchanges a one-time
 *      code via /auth/callback, then 302s the browser to
 *      /connections?provider={id}&status={connected|failed}.
 *   3. `AuthCallback.tsx` emits the success/error toast BEFORE the
 *      navigate, so the global ToastViewport (see web/src/components/toast/)
 *      surfaces the OAuth outcome. This page no longer reads the
 *      ?provider=…&status=… params — the only thing it needs from
 *      the redirect is the fresh accounts list (loadAccounts() runs
 *      on mount regardless). The URL params are preserved so anyone
 *      reading the address bar still sees the OAuth-flow context.
 */
export function Connections() {
  const navigate = useNavigate();
  const [state, setState] = useState<FetchState>({ kind: "loading" });
  const abortRef = useRef<AbortController | null>(null);

  const loadAccounts = useCallback(async () => {
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
      setState({ kind: "ready", accounts: data.accounts ?? [] });
    } catch (err) {
      if (controller.signal.aborted) return;
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      if (err instanceof DOMException && err.name === "AbortError") return;
      const message =
        err instanceof ApiError ? err.message : "Unable to reach the API.";
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
      void loadAccounts();
    })();
    return () => {
      cancelled = true;
      abortRef.current?.abort();
    };
  }, [loadAccounts, navigate]);

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
            Connect your accounts
          </h1>
          <p className="text-neutral-500 text-[16px] text-center max-w-[480px]">
            Link your social profiles to publish from a single inbox.
          </p>
        </div>

        {/* Grid */}
        <section className="pb-12">
          <div className="flex items-center justify-between mb-5">
            <h2 className="text-[20px] font-bold tracking-[-0.01em] text-black">
              Available providers
            </h2>
            {state.kind === "ready" && (
              <button
                type="button"
                onClick={() => void loadAccounts()}
                className="inline-flex items-center gap-1.5 text-[13px] text-neutral-500 hover:text-black transition-colors"
                data-testid="connections-refresh"
              >
                <RefreshCw size={13} /> Refresh
              </button>
            )}
          </div>

          {state.kind === "loading" && (
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4" data-testid="connections-loading">
              <Skeleton variant="card" height={88} />
              <Skeleton variant="card" height={88} />
              <Skeleton variant="card" height={88} />
              <Skeleton variant="card" height={88} />
            </div>
          )}

          {state.kind === "error" && (
            <ErrorState
              title="Couldn't load providers"
              message={state.message}
              onRetry={() => void loadAccounts()}
              retryLabel="Try again"
            />
          )}

          {state.kind === "ready" && (
            <div
              className="grid grid-cols-1 sm:grid-cols-2 gap-4"
              data-testid="connections-grid"
            >
              {PROVIDERS.map((provider) => {
                const account = accountsByProvider[provider.id];
                const isConnected = !!account;
                const dataTestId = `connection-card-${provider.id}`;
                if (!isConnected) {
                  return (
                    <a
                      key={provider.id}
                      href={`${API_BASE_URL}/api/v1/auth/${provider.id}/login`}
                      data-testid={dataTestId}
                      data-provider={provider.id}
                      // Demo-mode intercept: preventDefault + toast when
                      // the backend isn't reachable. The href stays in
                      // the DOM so the existing Connections.test.tsx
                      // assertion `expect(card.getAttribute("href"))
                      // .toBe(...)` keeps passing; middle-click / "open
                      // in new tab" still navigates (and shows the same
                      // toast on the destination page if it's also in
                      // demo mode).
                      onClick={(e) => {
                        if (DEMO_MODE) {
                          e.preventDefault();
                          toastBus.push(
                            "error",
                            "Connecting accounts requires a live backend. Deploy the Go API to enable OAuth.",
                          );
                        }
                      }}
                      className="group relative bg-white border border-dashed border-neutral-300 rounded-xl p-5 no-underline text-black hover:border-neutral-500 hover:shadow-[0_8px_24px_rgba(0,0,0,0.05)] transition-all overflow-hidden"
                    >
                      <div className="flex items-center gap-4">
                        <div
                          className={`w-12 h-12 rounded-xl border border-neutral-200 flex items-center justify-center text-neutral-400 group-hover:text-white group-hover:bg-gradient-to-br group-hover:${provider.color} transition-all`}
                        >
                          {provider.icon}
                        </div>
                        <div className="flex-1 min-w-0">
                          <h3 className="font-bold text-[15px] mb-1 text-black">
                            {provider.name}
                          </h3>
                          <p className="text-[13px] text-neutral-500">
                            Not connected
                          </p>
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
                    data-testid={dataTestId}
                    data-provider={provider.id}
                    className="relative bg-white border border-neutral-200 rounded-xl p-5 overflow-hidden"
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
                        <div className="flex items-center gap-2 mb-1 flex-wrap">
                          <h3 className="font-bold text-[15px] text-black truncate">
                            {provider.name}
                          </h3>
                          {fresh && (
                            <span className="px-2 py-0.5 rounded-full bg-green-50 border border-green-200 text-green-700 text-[11px] font-semibold uppercase tracking-wider">
                              New
                            </span>
                          )}
                          <span
                            className="px-2 py-0.5 rounded-full bg-blue-50 border border-blue-200 text-blue-700 text-[11px] font-semibold"
                            data-testid={`connection-pill-${provider.id}`}
                          >
                            Connected
                          </span>
                        </div>
                        <p className="text-[13px] text-neutral-500 truncate">
                          @{account.username || "—"}
                        </p>
                      </div>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </section>
      </div>
    </div>
  );
}

function isFresh(createdAt: string): boolean {
  const created = new Date(createdAt).getTime();
  if (Number.isNaN(created)) return false;
  return Date.now() - created < NEW_BADGE_HOURS * 60 * 60 * 1000;
}
