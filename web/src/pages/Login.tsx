import { type FormEvent, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { Zap, Mail, Lock, ArrowRight } from "lucide-react";
import { fetchSession } from "../lib/auth";
import { API_BASE_URL } from "../lib/api";
import { isDemoMode } from "../lib/demo";
import { PROVIDERS } from "../lib/providers";

export function Login() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const navigate = useNavigate();

  async function submit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setLoading(true);

    if (isDemoMode()) {
      await fetchSession();
      navigate("/app/dashboard");
      return;
    }

    try {
      const resp = await fetch(`${API_BASE_URL}/api/v1/auth/login`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password }),
      });

      if (!resp.ok) {
        const data = await resp.json().catch(() => ({}));
        throw new Error(data.error || "Invalid credentials");
      }

      await fetchSession();
      navigate("/app/dashboard");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong");
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="min-h-screen bg-[#030308] flex items-center justify-center px-6">
      {/* Background gradients */}
      <div className="fixed inset-0 overflow-hidden pointer-events-none">
        <div className="absolute top-[-20%] left-[-10%] w-[600px] h-[600px] rounded-full bg-[#0A84FF]/[0.07] blur-[120px]" />
        <div className="absolute bottom-[-20%] right-[-10%] w-[600px] h-[600px] rounded-full bg-[#7B61FF]/[0.07] blur-[120px]" />
      </div>

      <div className="relative w-full max-w-[400px]">
        {/* Logo */}
        <Link
          to="/"
          className="flex items-center justify-center gap-2.5 mb-10 group"
        >
          <div className="w-9 h-9 rounded-xl bg-gradient-to-br from-[#0A84FF] to-[#7B61FF] flex items-center justify-center group-hover:scale-105 transition-transform">
            <Zap className="w-4.5 h-4.5 text-white" />
          </div>
          <span className="text-lg font-semibold tracking-tight">
            InstaEdit
          </span>
        </Link>

        {/* Card */}
        <div className="rounded-2xl border border-white/[0.20] bg-[#1f1f2e] p-10 shadow-[inset_0_1px_0_0_rgba(255,255,255,0.18)]">
          <h1 className="text-xl font-semibold tracking-tight mb-1">
            Welcome back
          </h1>
          <p className="text-sm text-[#9aa0aa] mb-10">
            Log in to manage your content
          </p>

          <form onSubmit={submit} className="space-y-5">
            <div>
              <label
                htmlFor="email"
                className="block text-sm font-medium text-[#9aa0aa] mb-2"
              >
                Email
              </label>
              <div className="relative">
                <Mail className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-[#9aa0aa]/60" />
                <input
                  id="email"
                  type="email"
                  autoComplete="email"
                  required
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="yourname@company.com"
                  className="w-full h-11 pl-10 pr-4 rounded-xl bg-white/[0.04] border border-white/[0.10] text-sm placeholder:text-[#9aa0aa]/40 focus:outline-none focus:border-[#0A84FF]/50 focus:ring-1 focus:ring-[#0A84FF]/20 transition-all"
                />
              </div>
            </div>

            <div>
              <label
                htmlFor="password"
                className="block text-sm font-medium text-[#9aa0aa] mb-2"
              >
                Password
              </label>
              <div className="relative">
                <Lock className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-[#9aa0aa]/60" />
                <input
                  id="password"
                  type="password"
                  autoComplete="current-password"
                  required
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="Enter your password"
                  className="w-full h-11 pl-10 pr-4 rounded-xl bg-white/[0.04] border border-white/[0.10] text-sm placeholder:text-[#9aa0aa]/40 focus:outline-none focus:border-[#0A84FF]/50 focus:ring-1 focus:ring-[#0A84FF]/20 transition-all"
                />
              </div>
            </div>

            {error && (
              <div className="text-sm text-red-400 bg-red-400/[0.08] border border-red-400/[0.15] rounded-lg px-4 py-2.5">
                {error}
              </div>
            )}

            <button
              type="submit"
              disabled={loading}
              className="w-full h-11 rounded-xl bg-white text-[#030308] font-medium text-sm flex items-center justify-center gap-2 hover:bg-white/90 disabled:opacity-50 transition-all"
            >
              {loading ? (
                <div className="w-4 h-4 border-2 border-[#030308]/20 border-t-[#030308] rounded-full animate-spin" />
              ) : (
                <>
                  Log in
                  <ArrowRight className="w-4 h-4" />
                </>
              )}
            </button>
          </form>

          {/*
           * OAuth provider buttons. The same pattern is used in
           * web/src/pages/internal/Linking.tsx (Connect account flow): the
           * redirect middleware (oauthSessionRedirect in
           * pkg/api/oauth_session_redirect.go) gates the path behind an
           * InstaEdit session cookie so anonymous visitors from /login are
           * first minted one and then continue to the provider's OAuth
           * dance. The /api/v1/auth/{provider}/login endpoint is shared
           * between sign-in and connect-account flows; payment / checkout
           * does not use OAuth (it goes through /api/v1/billing/*).
           *
           * Some providers (YouTube, Threads) are also publishing
           * destinations — the same OAuth URL works for both sign-up and
           * subsequent publish-channel linking.
           */}
          <div className="mt-6 pt-6 border-t border-white/[0.08]">
            <p className="text-xs text-center text-[#9aa0aa] mb-4">
              Or continue with
            </p>
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-2">
              {PROVIDERS.map((p) => {
                // PROVIDERS glowColor is "rgba(R,G,B,A)" — include the alpha
                // for ambient glow contexts, but as the icon `currentColor`
                // we want a fully opaque solid RGB so the icon is visible on
                // the dark Login card. (Threads' `rgba(0,0,0,0.25)` and X's
                // `rgba(200,200,210,0.2)` would otherwise render practically
                // invisible.) Parse the 4-tuple and emit a 3-tuple `rgb()`.
                const rgbaMatch = p.glowColor?.match(/^rgba?\(([^)]+)\)/);
                const parts = rgbaMatch ? rgbaMatch[1].split(",").map((s) => s.trim()) : [];
                const solidColor =
                  parts.length >= 3 ? `rgb(${parts.slice(0, 3).join(", ")})` : undefined;
                return (
                  <a
                    key={p.id}
                    href={`${API_BASE_URL}/api/v1/auth/${p.id}/login`}
                    className="group flex items-center justify-center gap-2 h-11 px-3 rounded-xl bg-white/[0.04] border border-white/[0.10] text-xs font-medium text-zinc-300 hover:bg-white/[0.08] hover:border-white/[0.20] hover:text-white transition-all focus:outline-none focus:border-[#0A84FF]/50 focus:ring-1 focus:ring-[#0A84FF]/20"
                    aria-label={`Continue with ${p.name}`}
                  >
                    <span
                      className="w-5 h-5 flex shrink-0"
                      style={solidColor ? { color: solidColor } : undefined}
                    >
                      {p.icon}
                    </span>
                    <span className="truncate">{p.name}</span>
                  </a>
                );
              })}
            </div>
          </div>
        </div>

        {/* Footer */}
        <p className="text-center text-xs text-[#9aa0aa]/60 mt-6">
          OAuth 2.0 &middot; AES-256-GCM encryption &middot; No password
          stored
        </p>
      </div>
    </div>
  );
}
