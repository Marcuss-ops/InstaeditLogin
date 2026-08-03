/**
 * Base URL for the Go backend API.
 *
 * Resolved order:
 *   1. `VITE_API_BASE_URL` (set on Vercel via Project Settings → Environment Variables)
 *   2. `http://localhost:8081` (canonical local Compose fallback)
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
 *   echo "VITE_API_BASE_URL=http://localhost:8081" > web/.env
 * Vercel prod:
 *   Settings → Environment Variables → add VITE_API_BASE_URL
 *     pointing at the deployed Go API host.
 *
 */
const configuredApiBaseUrl = import.meta.env.VITE_API_BASE_URL;
const publicHost =
  typeof window !== "undefined" && window.location.hostname.endsWith("instaedit.org");

// Never let a production/dev-host bundle call the browser's localhost. The
// explicit VITE value remains the deployment override; this host-aware
// fallback keeps a manually built public bundle usable even when the build
// environment forgot to provide it.
export const API_BASE_URL: string =
  configuredApiBaseUrl || (publicHost ? "https://api.instaedit.org" : "http://localhost:8081");
