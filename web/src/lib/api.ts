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
 * Taglio 3.1: this file replaces web/src/lib/supabase.ts. The Supabase
 * client and the dual-storage selection logic were removed; storage is
 * now exclusively S3-compatible and wired server-side.
 */
export const API_BASE_URL: string =
  import.meta.env.VITE_API_BASE_URL || "http://localhost:8080";
