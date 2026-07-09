import type { ProbeResult } from "./auth";

/**
 * Shared formatting/display helpers for backend probe state.
 *
 * Used by web/src/pages/Login.tsx (pill + degraded banner above the OAuth
 * provider grid) and web/src/pages/Status.tsx (the public self-debug page).
 *
 * Keeping these in one place means a user who opens /status because the
 * Login page misbehaved sees the exact same diagnosis and fix instructions
 * — only the surrounding chrome changes.
 */

export function statusLabel(result: ProbeResult): string {
  if (result.ok) {
    return `Connected to ${shortHost(result.url)}`;
  }
  switch (result.reason) {
    case "not_found":
      return "Backend URL returned 404";
    case "vercel_stale_deploy":
      return "Vercel stale deployment";
    case "http_error":
      return `Backend returned HTTP ${result.status ?? "?"}`;
    case "timeout":
      return "Backend timed out";
    case "unreachable":
      return "Backend unreachable";
  }
}

export function shortHost(url: string): string {
  try {
    const u = new URL(url);
    return u.host;
  } catch {
    return url;
  }
}

export type BannerCopy = { title: string; body: string; hint: string };

export function bannerCopy(result: Extract<ProbeResult, { ok: false }>): BannerCopy {
  switch (result.reason) {
    case "not_found":
      return {
        title: "Backend URL is dead",
        body: `${shortHost(result.url)} returned 404 DEPLOYMENT_NOT_FOUND — the deployment is gone or deleted.`,
        hint:
          "Set VITE_API_BASE_URL to a reachable Go backend on Vercel (Settings → Environment Variables) and redeploy. Local dev: VITE_API_BASE_URL=http://localhost:8080 in web/.env.",
      };
    case "vercel_stale_deploy":
      return {
        title: "VITE_API_BASE_URL points at a stale Vercel deployment",
        body: `${shortHost(result.url)} is a removed or expired Vercel deployment (404 DEPLOYMENT_NOT_FOUND), not the InstaEdit Go backend. The OAuth buttons below link to this URL via ${result.url}/api/v1/auth/{provider}/login and Vercel returns its standard "deployment gone" page at click time.`,
        hint:
          "Open Vercel → select your FRONTEND project → Settings → Environment Variables → set VITE_API_BASE_URL to the URL of your running Go backend (e.g. https://api.example.com or http://localhost:8080 for local dev) → Deployments → ⋯ → Redeploy with cleared build cache.",
      };
    case "http_error":
      return {
        title: "Backend is responding with errors",
        body: `${shortHost(result.url)} returned HTTP ${result.status ?? "?"}.`,
        hint: "Check backend logs to see why /api/v1/health 5xx'd.",
      };
    case "timeout":
      return {
        title: "Backend is too slow",
        body: `No response from ${shortHost(result.url)} within 5s.`,
        hint: "Verify the Go server is running and not stuck on a slow boot.",
      };
    case "unreachable":
      return {
        title: "Cannot reach the backend",
        body: `The browser couldn't reach ${shortHost(result.url)}. The host is down, firewalled, or its CORS preflight refused the request.`,
        hint:
          "Verify the backend is running and add this frontend's origin (https://instaedit.org) to CORS_ALLOWED_ORIGINS on the backend.",
      };
  }
}
