import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { API_BASE_URL } from "../lib/api";
import { clearSessionCache } from "../lib/auth";
import { useToast } from "../components/toast";
import { getProvider, type ProviderId } from "../lib/providers";

type CallbackStatus = "processing" | "success" | "error";

/**
 * /auth/callback handles TWO incoming flows:
 *
 *   1. OAuth one-time code: ?code=… from /auth/{provider}/callback.
 *      We POST to /api/v1/auth/exchange with the code; the backend
 *      consumes the code from the one-time store and writes the
 *      session cookies; 204 → navigate /accounts.
 *
 *   2. Magic-link token:    ?token=… from the email magic link (or
 *      the dev "Verify now" surface in /login).
 *      We POST to /api/v1/auth/magic-link/verify with the token;
 *      the backend consumes the SHA-256 hashed token from
 *      magic_link_tokens, runs MagicLinkSignupOrLookup, mints a
 *      session, sets cookies; 204 → navigate /accounts.
 *
 * Both flows succeed by setting the session cookie and redirecting.
 * We deliberately use raw fetch (not authedFetch) because the
 * request is unauthenticated in both paths — the cookie we WANT
 * is the one this very request is supposed to set.
 */
export function AuthCallback() {
  const navigate = useNavigate();
  const [status, setStatus] = useState<CallbackStatus>("processing");
  const [error, setError] = useState<string | null>(null);
  const toast = useToast();

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const code = params.get("code");
    const token = params.get("token");
    const provider = params.get("provider") ?? "";

    if (!code && !token) {
      const message =
        "The callback did not include a code or a magic-link token. Please try again from the login page.";
      setStatus("error");
      setError(message);
      toast.error(message);
      return;
    }

    const path = code
      ? "/api/v1/auth/exchange"
      : "/api/v1/auth/magic-link/verify";
    const body = code ? { code } : { token };

    (async () => {
      try {
        const response = await fetch(`${API_BASE_URL}${path}`, {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        });
        if (response.status === 204) {
          // Session cookie is set. Force a fresh /api/v1/auth/me fetch
          // on the next page that needs it.
          clearSessionCache();
          setStatus("success");
          // Two landing sites:
          //   - OAuth (code present)  → /connections with the post-callback
          //     query params so Connections can refresh the accounts list.
          //     The TOAST for OAuth success/failure is emitted HERE (in
          //     the global ToastViewport, see web/src/components/toast/);
          //     Renderer state survives across the navigate() call.
          //   - Magic-link (token)    → /accounts (the dashboard). The
          //     sign-in is the action; no provider was linked.
          //
          // We propagate the backend's `?status=` value rather than
          // hardcoding "connected" — if the OAuth flow failed at any
          // point (user denied consent, token-exchange error, account
          // already taken, rate limit, …) the backend's
          // /auth/{provider}/callback (the backend endpoint, distinct
          // from this `/auth/callback` page) will land here with
          // `?status=failed` and the user should see the failed
          // toast on /connections, not a misleading success.
          //
          // Default to "" (no toast on OAuth) rather than "connected":
          // on a deploy-critical path, silence is debuggable, a false
          // success is the bug class we just fixed.
          const oauthStatus = params.get("status") ?? "";
          if (code) {
            // Only emit if the provider id is one we recognize — defends
            // against ?provider=garbage URLs producing ". connected."
            // (a literal dot + space + "connected.") from a missing-name
            // fallback. Unknown providers and empty/missing status
            // stay silent: the OAuth flow's outcome is unverified, so
            // announcing success would be a shipping a false-positive.
            const providerMeta = provider
              ? getProvider(provider as ProviderId)
              : undefined;
            if (providerMeta) {
              if (oauthStatus === "failed") {
                toast.error(
                  `${providerMeta.name} connection failed. Please try again from Connections.`,
                );
              } else if (oauthStatus === "connected") {
                toast.success(`${providerMeta.name} connected.`);
              }
            }
          } else {
            // Magic-link sign-in completed; the toast confirms the action.
            toast.success("Signed in.");
          }
          const target = code
            ? `/connections?provider=${encodeURIComponent(provider || "")}&status=${encodeURIComponent(oauthStatus)}`
            : "/accounts";
          navigate(target, { replace: true });
          return;
        }
        let detail = `HTTP ${response.status}`;
        try {
          const data = (await response.json()) as { error?: string };
          if (data?.error) {
            detail = data.error;
          }
        } catch {
          // body wasn't JSON
        }
        const message = `Could not finalize sign-in: ${detail}. Please try again from the login page.`;
        setStatus("error");
        setError(message);
        toast.error(message);
      } catch (err) {
        const message =
          err instanceof Error
            ? `Could not reach the backend: ${err.message}`
            : "Could not reach the backend.";
        setStatus("error");
        setError(message);
        toast.error(message);
      }
    })();
  }, [navigate]);

  return (
    <div className="min-h-screen bg-neutral-50 flex flex-col">
      <div className="max-w-[640px] mx-auto px-6 w-full flex-1 flex flex-col items-center justify-center py-20 text-center">
        {status === "processing" && (
          <>
            <div className="w-12 h-12 rounded-full border-4 border-neutral-200 border-t-black animate-spin mb-6" />
            <h1 className="text-2xl font-bold tracking-[-0.02em] text-black mb-2">
              Finishing sign-in…
            </h1>
            <p className="text-[15px] text-neutral-500">
              Securing your session and loading your connected accounts.
            </p>
          </>
        )}

        {status === "success" && (
          <>
            <h1 className="text-2xl font-bold tracking-[-0.02em] text-black mb-2">
              Signed in.
            </h1>
            <p className="text-[15px] text-neutral-500">
              Redirecting to your accounts…
            </p>
          </>
        )}

        {status === "error" && (
          <>
            <div className="w-14 h-14 rounded-full bg-red-50 border border-red-200 flex items-center justify-center mb-6">
              <span className="text-2xl text-red-500">!</span>
            </div>
            <h1 className="text-2xl font-bold tracking-[-0.02em] text-black mb-2">
              Sign-in failed
            </h1>
            <p className="text-[15px] text-neutral-500 mb-8">
              {error ?? "Unknown error"}
            </p>
            <a
              href="/login"
              className="inline-flex items-center gap-2 px-5 py-2.5 rounded-xl bg-black text-white text-[14px] font-semibold no-underline hover:bg-neutral-800 transition-colors"
            >
              Back to login
            </a>
          </>
        )}
      </div>
    </div>
  );
}
