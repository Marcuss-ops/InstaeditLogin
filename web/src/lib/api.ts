/**
 * Base URL for the Go backend API.
 *
 * Resolved order:
 *   1. `VITE_API_BASE_URL` (set on Vercel via Project Settings → Environment Variables)
 *   2. `http://localhost:8080` (local dev fallback)
 *
 * OAuth provider buttons under /login and /dashboard redirect to
 * `${API_BASE_URL}/api/v1/auth/{provider}/login`, so this env var MUST point
 * at a running, reachable Go backend or login will 404.
 *
 * If VITE_API_BASE_URL points to a decommissioned deployment (e.g. an old
 * Vercel project), the buttons will simply render an error page when clicked.
 * The /login page runs a health probe on mount and shows a degraded banner
 * with the URL it's probing so the misconfiguration is visible.
 *
 * Local dev:
 *   echo "VITE_API_BASE_URL=http://localhost:8080" > web/.env
 * Vercel prod:
 *   Settings → Environment Variables → add VITE_API_BASE_URL
 *     pointing at the deployed Go API host.
 *
 */
const CANONICAL_PUBLIC_API_BASE_URL = "https://api.instaedit.org";

function isPublicInstaEditHost(hostname: string): boolean {
  const normalized = hostname.trim().toLowerCase();
  return normalized === "instaedit.org" || normalized.endsWith(".instaedit.org");
}

/**
 * Resolves the API URL used by the browser.
 *
 * `dev.instaedit.org` is a legacy compatibility host. Public bundles must not
 * keep using it: a Vercel environment variable pointing there can send the
 * session request to the stale deployment and produce an apparently random
 * 401. Keep the override for local/non-public environments, but canonicalize
 * that one legacy host whenever the SPA is served on an InstaEdit domain.
 */
export function resolveApiBaseUrl(
  configuredUrl: string | undefined,
  hostname: string | undefined,
): string {
  const configured = configuredUrl?.trim() ?? "";
  const publicHost = hostname ? isPublicInstaEditHost(hostname) : false;

  if (configured && publicHost) {
    try {
      const parsed = new URL(configured);
      if (parsed.hostname.toLowerCase() === "dev.instaedit.org") {
        parsed.hostname = "api.instaedit.org";
        return parsed.toString().replace(/\/$/, "");
      }
    } catch {
      // Preserve the configured value so the existing build-time validator or
      // backend error remains visible instead of hiding a malformed setting.
    }
  }

  // Never let a production/public bundle call the browser's localhost. The
  // explicit VITE value remains the deployment override; this host-aware
  // fallback keeps a manually built public bundle usable when the build
  // environment forgot to provide it.
  return configured || (publicHost ? CANONICAL_PUBLIC_API_BASE_URL : "http://localhost:8080");
}

export const API_BASE_URL: string = resolveApiBaseUrl(
  import.meta.env.VITE_API_BASE_URL,
  typeof window !== "undefined" ? window.location.hostname : undefined,
);
