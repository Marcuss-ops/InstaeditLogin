import { useCallback, useEffect, useRef, useState } from "react";
import { Link2, RefreshCw, CheckCircle2 } from "lucide-react";
import { authedFetch, AuthError, fetchSession } from "../../lib/auth";
import { API_BASE_URL } from "../../lib/api";
import { PROVIDERS, type ProviderId } from "../../lib/providers";
import { ErrorState } from "../../components/feedback";
import { cn } from "../../lib/utils";

type PlatformAccount = {
  id: number;
  platform: ProviderId;
  username: string;
  created_at: string;
};

type FetchState =
  | { kind: "loading" }
  | { kind: "ready"; accounts: PlatformAccount[] }
  | { kind: "error"; message: string };

const LINKABLE_IDS: ProviderId[] = [
  "youtube",
  "tiktok",
  "facebook",
  "instagram",
  "threads",
];

export function InternalLinking() {
  const [state, setState] = useState<FetchState>({ kind: "loading" });
  const abortRef = useRef<AbortController | null>(null);

  const loadAccounts = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
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
        window.location.href = "/login";
        return;
      }
      const message = err instanceof Error ? err.message : "Unable to load accounts.";
      setState({ kind: "error", message });
    }
  }, []);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const session = await fetchSession();
      if (cancelled) return;
      if (!session) {
        window.location.href = "/login";
        return;
      }
      void loadAccounts();
    })();
    return () => {
      cancelled = true;
      abortRef.current?.abort();
    };
  }, [loadAccounts]);

  const accountsByProvider: Record<string, PlatformAccount | undefined> = {};
  if (state.kind === "ready") {
    for (const acc of state.accounts) {
      accountsByProvider[acc.platform] = acc;
    }
  }

  const linkableProviders = PROVIDERS.filter((p) => LINKABLE_IDS.includes(p.id));

  return (
    <div className="min-h-full p-8">
      <div className="max-w-5xl mx-auto">
        {/* Header */}
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-8">
          <div>
            <h1 className="text-[28px] font-extrabold tracking-[-0.02em] text-black flex items-center gap-3">
              <Link2 size={28} className="text-neutral-400" />
              Linking
            </h1>
            <p className="text-[15px] text-neutral-500 mt-1">
              Connect your social profiles to publish from a single inbox.
            </p>
          </div>
          {state.kind === "ready" && (
            <button
              type="button"
              onClick={() => void loadAccounts()}
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white border border-neutral-200 text-[13px] font-semibold text-neutral-700 hover:border-neutral-400 transition-colors"
            >
              <RefreshCw size={14} /> Refresh
            </button>
          )}
        </div>

        {state.kind === "loading" && (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
            {Array.from({ length: 5 }).map((_, i) => (
              <div
                key={i}
                className="h-32 rounded-2xl bg-neutral-200 animate-pulse"
              />
            ))}
          </div>
        )}

        {state.kind === "error" && (
          <ErrorState
            title="Couldn't load providers"
            message={state.message}
            onRetry={() => void loadAccounts()}
          />
        )}

        {state.kind === "ready" && (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
            {linkableProviders.map((provider) => {
              const account = accountsByProvider[provider.id];
              const isConnected = !!account;
              return (
                <div
                  key={provider.id}
                  className={cn(
                    "relative bg-white border rounded-2xl p-5 transition-all overflow-hidden",
                    isConnected
                      ? "border-neutral-200"
                      : "border-dashed border-neutral-300 hover:border-neutral-500 hover:shadow-[0_8px_24px_rgba(0,0,0,0.05)]",
                  )}
                >
                  <div
                    className={cn(
                      "absolute top-0 left-0 right-0 h-1 bg-gradient-to-r",
                      provider.color,
                    )}
                  />
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
                        <h3 className="font-bold text-[15px] text-black">{provider.name}</h3>
                        <p className="text-[13px] text-neutral-500 mt-0.5">
                          {isConnected ? `@${account.username}` : "Not connected"}
                        </p>
                      </div>
                    </div>
                    {isConnected && (
                      <span className="inline-flex items-center gap-1 px-2 py-1 rounded-full bg-green-50 border border-green-200 text-green-700 text-[11px] font-semibold">
                        <CheckCircle2 size={11} /> Connected
                      </span>
                    )}
                  </div>

                  <div className="mt-5">
                    {isConnected ? (
                      <div className="flex items-center gap-2 text-[13px] text-neutral-500">
                        <span className="w-2 h-2 rounded-full bg-green-500" />
                        Linked on {new Date(account.created_at).toLocaleDateString()}
                      </div>
                    ) : (
                      <a
                        href={`${API_BASE_URL}/api/v1/auth/${provider.id}/login`}
                        className="inline-flex items-center justify-center w-full px-4 py-2.5 rounded-xl bg-black text-white text-[13px] font-semibold hover:bg-neutral-800 transition-colors no-underline"
                      >
                        Connect {provider.name}
                      </a>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
