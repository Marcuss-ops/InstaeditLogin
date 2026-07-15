import { type FormEvent, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { Zap, Mail, Lock, ArrowRight } from "lucide-react";
import { fetchSession } from "../lib/auth";
import { DEMO_MODE } from "../lib/demo";
import { API_BASE_URL } from "../lib/api";

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

    try {
      if (DEMO_MODE) {
        console.warn("DEMO_MODE is active (VITE_DEMO_MODE=true). Skipping real API login request and redirecting to dashboard.");
        navigate("/app/dashboard");
        return;
      }

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
            Sign in to manage your content
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
                  placeholder="you@company.com"
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
                  Sign in
                  <ArrowRight className="w-4 h-4" />
                </>
              )}
            </button>
          </form>
        </div>

        {/* Footer */}
        <p className="text-center text-xs text-[#9aa0aa]/60 mt-6">
          OAuth 2.0 &middot; AES-256-GCM encryption &middot; No passwords
          stored
        </p>
      </div>
    </div>
  );
}
