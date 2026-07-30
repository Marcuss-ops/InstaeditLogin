import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import { Navigate } from "react-router-dom";
import { fetchSession, type Session } from "../../lib/auth";

/**
 * AdminProtectedRoute — gate for admin-only pages.
 *
 * Resolves the current session and renders children only when the
 * user is authenticated AND has the admin flag. Redirects to /login
 * when no session exists, or /app/dashboard when the user is not an
 * admin.
 */
export function AdminProtectedRoute({ children }: { children: ReactNode }) {
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
        className="h-screen w-full flex items-center justify-center bg-[#030308]"
        aria-live="polite"
        aria-busy="true"
      >
        <div className="w-8 h-8 rounded-full border-4 border-neutral-700 border-t-white animate-spin" />
        <span className="sr-only">Loading session…</span>
      </div>
    );
  }

  if (!session) {
    return <Navigate to="/login" replace />;
  }

  if (!session.isAdmin) {
    return <Navigate to="/app/dashboard" replace />;
  }

  return <>{children}</>;
}
