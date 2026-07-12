import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { API_BASE_URL } from "../lib/supabase";
import { clearSessionCache } from "../lib/auth";

type CallbackStatus = "processing" | "success" | "error";

export function AuthCallback() {
  const navigate = useNavigate();
  const [status, setStatus] = useState<CallbackStatus>("processing");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const code = params.get("code");
    const provider = params.get("provider") ?? "";

    if (!code) {
      setStatus("error");
      setError(
        "The OAuth callback did not include a one-time code. Please try again from the login page.",
      );
      return;
    }

    (async () => {
      try {
        const response = await fetch(
          `${API_BASE_URL}/api/v1/auth/exchange`,
          {
            method: "POST",
            credentials: "include",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ code }),
          },
        );
        if (response.status === 204) {
          // Session cookie is set. Force a fresh /api/v1/auth/me fetch on
          // the next page that needs it.
          clearSessionCache();
          setStatus("success");
          // Replace history so the ?code= URL never lands in browser history.
          navigate("/dashboard", {
            replace: true,
            state: { provider },
          });
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
        setStatus("error");
        setError(
          `Could not finalize sign-in: ${detail}. Please try again from the login page.`,
        );
      } catch (err) {
        setStatus("error");
        setError(
          err instanceof Error
            ? `Could not reach the backend: ${err.message}`
            : "Could not reach the backend.",
        );
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
              Redirecting to your dashboard…
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
