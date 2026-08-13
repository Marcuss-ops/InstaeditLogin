import { useEffect } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { AUTH_EXPIRED_EVENT } from "../../lib/auth";
import { isDemoMode } from "../../lib/demo";

/**
 * Global session-loss safety net.
 *
 * Every authed surface reports an unhealable 401 the same way:
 * `authedFetch` clears the session and dispatches
 * `instaedit:auth-expired` (see lib/auth.ts). This component is the
 * single listener that turns that signal into a consistent redirect to
 * /login — preserving the current protected URL as `?next=` so a
 * re-login lands back where the user was (e.g. the Copertine hub of
 * the group they were working on).
 *
 * Without it, behaviour is inconsistent: page hooks that catch
 * `AuthError` navigate to /login, but polling surfaces (the sidebar
 * live badge, shared-query pollers) swallow the error into their
 * snapshot and keep 401-ing every 30-60s after the session dies —
 * exactly the silent 401 spam seen on the Copertine hub. Mounted once
 * inside <BrowserRouter>, this listener makes every surface behave the
 * same ("per tutti uguali").
 */
export function SessionLossRedirect() {
  const navigate = useNavigate();
  const location = useLocation();

  useEffect(() => {
    const onAuthExpired = () => {
      if (isDemoMode()) return;
      const { pathname, search } = location;
      // Only protected areas redirect: an expired session must not
      // hijack public pages, and /login must never redirect to itself
      // (the login page clears the session cache on mount).
      const isProtected = pathname.startsWith("/app") || pathname.startsWith("/admin");
      if (!isProtected || pathname.startsWith("/login")) return;
      const next = encodeURIComponent(`${pathname}${search}`);
      navigate(`/login?next=${next}`, { replace: true });
    };
    window.addEventListener(AUTH_EXPIRED_EVENT, onAuthExpired);
    return () => window.removeEventListener(AUTH_EXPIRED_EVENT, onAuthExpired);
  }, [location.pathname, location.search, navigate]);

  return null;
}
