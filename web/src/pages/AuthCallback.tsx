import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { API_BASE_URL } from "../lib/api";
import { clearSessionCache } from "../lib/auth";
import { useToast } from "../components/toast";
import { getProvider, type ProviderId } from "../lib/providers";

type CallbackStatus = "processing" | "success" | "error";

/**
 * /auth/callback handles the OAuth one-time code flow:
 *
 *   ?code=… from /auth/{provider}/callback.
 *   We POST to /api/v1/auth/exchange with the code; the backend
 *   consumes the code from the one-time store and writes the
 *   session cookies; 204 → navigate /connections.
 */
export function AuthCallback() {
  const navigate = useNavigate();
  const [status, setStatus] = useState<CallbackStatus>("processing");
  const [error, setError] = useState<string | null>(null);
  const toast = useToast();

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const code = params.get("code");
    const provider = params.get("provider") ?? "";

    if (!code) {
      const message =
        "The callback did not include an authorization code. Please try again from the login page.";
      setStatus("error");
      setError(message);
      toast.error(message);
      return;
    }

    (async () => {
      try {
        const response = await fetch(`${API_BASE_URL}/api/v1/auth/exchange`, {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ code }),
        });
        if (response.status === 204) {
          // Session cookie is set. Force a fresh /api/v1/auth/me fetch
          // on the next page that needs it.
          clearSessionCache();
          setStatus("success");

          const oauthStatus = params.get("status") ?? "";
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
          const target = `/connections?provider=${encodeURIComponent(provider || "")}&status=${encodeURIComponent(oauthStatus)}`;
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
  }, [navigate, toast]);

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
