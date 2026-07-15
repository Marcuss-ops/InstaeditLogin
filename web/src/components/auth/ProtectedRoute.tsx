import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import { Navigate } from "react-router-dom";
import { fetchSession, type Session } from "../../lib/auth";

/**
 * ProtectedRoute — gate for the internal app area.
 *
 * Resolves the current session before rendering children.
 * Shows a centered spinner while the session promise is pending.
 * Redirects to /login if the user is not authenticated.
 */
export function ProtectedRoute({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<Session | null | "loading">("loading");

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const s = await fetchSession();
      if (!cancelled) setSession(s);
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  if (session === "loading") {
    return (
      <div
        className="h-screen w-full flex items-center justify-center bg-neutral-50"
        aria-live="polite"
        aria-busy="true"
      >
        <div className="w-8 h-8 rounded-full border-4 border-neutral-200 border-t-black animate-spin" />
        <span className="sr-only">Loading session…</span>
      </div>
    );
  }

  if (!session) {
    return <Navigate to="/login" replace />;
  }

  return <>{children}</>;
}
