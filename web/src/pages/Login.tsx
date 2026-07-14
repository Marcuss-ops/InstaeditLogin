import { useCallback, useState } from "react";
import { Lock, Mail, Shield, Sparkles } from "lucide-react";
import { API_BASE_URL } from "../lib/api";
import { clearSessionCache } from "../lib/auth";
import { DEMO_MODE } from "../lib/demo";

type Toast = { kind: "ok" | "err"; message: string } | null;

/**
 * Email/password login page.
 *
 * Flow:
 *   1. User enters email + password and submits.
 *   2. POST /api/v1/auth/login → backend validates credentials,
 *      creates a session row, and sets HttpOnly session/refresh
 *      cookies on the response.
 *   3. On 200 we clear any stale session cache and navigate to
 *      /accounts (the dashboard).
 */
export function Login() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);
  const [toast, setToast] = useState<Toast>(null);

  const showToast = useCallback((t: Toast) => {
    setToast(t);
    if (t) window.setTimeout(() => setToast(null), 3500);
  }, []);

  const submit = useCallback(
    async (e: React.FormEvent) => {
      e.preventDefault();
      const trimmedEmail = email.trim().toLowerCase();
      if (!trimmedEmail || !trimmedEmail.includes("@")) {
        showToast({ kind: "err", message: "Enter a valid email address." });
        return;
      }
      if (!password) {
        showToast({ kind: "err", message: "Enter your password." });
        return;
      }

      // Demo-mode short-circuit: skip the network call and log the
      // user in as the mock demo account. fetchSession() in
      // auth.ts already returns MOCK_SESSION when DEMO_MODE is on,
      // so the dashboard renders without a backend roundtrip.
      // Local-validation errors above (empty email/password) still
      // fire first, so a real-looking form experience is preserved.
      if (DEMO_MODE) {
        clearSessionCache();
        window.location.replace("/accounts");
        return;
      }

      setLoading(true);
      try {
        const resp = await fetch(`${API_BASE_URL}/api/v1/auth/login`, {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ email: trimmedEmail, password }),
        });

        if (resp.status === 200) {
          clearSessionCache();
          window.location.replace("/accounts");
          return;
        }

        let detail = `HTTP ${resp.status}`;
        try {
          const data = (await resp.json()) as { error?: string };
          if (data?.error) detail = data.error;
        } catch {
          // ignore
        }
        showToast({
          kind: "err",
          message:
            resp.status === 401
              ? "Invalid email or password."
              : `Login failed: ${detail}`,
        });
      } catch (err) {
        showToast({
          kind: "err",
          message:
            err instanceof Error
              ? `Could not reach the backend: ${err.message}`
              : "Could not reach the backend.",
        });
      } finally {
        setLoading(false);
      }
    },
    [email, password, showToast],
  );

  return (
    <div className="min-h-screen bg-[#030308] flex flex-col relative isolate">
      <div
        className="fixed inset-0 pointer-events-none -z-10"
        style={{
          background:
            "radial-gradient(600px circle at 20% 10%, rgba(10,132,255,0.15), transparent 60%), radial-gradient(500px circle at 80% 20%, rgba(123,97,255,0.12), transparent 60%)",
        }}
      />

      <div className="flex-1 flex flex-col items-center justify-center px-6 py-16 relative z-10">
        <div className="text-center mb-10 max-w-[640px]">
          <div className="inline-flex items-center gap-2 px-4 py-1.5 rounded-full bg-white/[0.06] border border-white/[0.12] text-[12px] text-[#9aa0aa] mb-6 backdrop-blur-sm">
            <Sparkles size={13} className="text-[#7B61FF]" />
            Sign in with email
          </div>
          <h1 className="text-[clamp(28px,5vw,48px)] font-extrabold tracking-[-0.02em] mb-4 leading-[1.05]">
            <span className="text-white">Welcome to </span>
            <span className="bg-gradient-to-r from-[#0A84FF] to-[#7B61FF] bg-clip-text text-transparent">
              InstaEdit
            </span>
          </h1>
          <p className="text-[#9aa0aa] text-[16px] leading-relaxed">
            Enter your email and password to access your dashboard.
          </p>
        </div>

        {/* Toast */}
        {toast && (
          <div
            role="status"
            className={`fixed bottom-6 right-6 z-50 px-4 py-2.5 rounded-xl text-[13px] shadow-lg animate-[fadeUp_0.3s_ease-out] text-white ${toast.kind === "ok" ? "bg-green-600" : "bg-red-600"}`}
            data-testid={`toast-${toast.kind}`}
          >
            {toast.message}
          </div>
        )}

        {/* Card */}
        <div
          className="w-full max-w-[420px] bg-white/[0.04] border border-white/[0.10] rounded-2xl p-6 backdrop-blur-sm"
          data-testid="login-card"
        >
          <form className="space-y-4" noValidate onSubmit={submit}>
            <div>
              <label
                htmlFor="login-email"
                className="block text-[12px] font-semibold text-[#9aa0aa] mb-1"
              >
                Email address
              </label>
              <div className="relative">
                <Mail
                  size={15}
                  className="absolute left-3 top-1/2 -translate-y-1/2 text-[#9aa0aa] pointer-events-none"
                />
                <input
                  id="login-email"
                  type="email"
                  inputMode="email"
                  autoComplete="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="you@example.com"
                  className="w-full pl-10 pr-3 py-3 rounded-xl bg-white/[0.06] border border-white/[0.12] text-white text-[15px] placeholder:text-[#6b7280] focus:outline-none focus:ring-2 focus:ring-[#0A84FF]/40 focus:border-[#0A84FF]/40 transition-colors"
                  data-testid="login-email"
                />
              </div>
            </div>

            <div>
              <label
                htmlFor="login-password"
                className="block text-[12px] font-semibold text-[#9aa0aa] mb-1"
              >
                Password
              </label>
              <div className="relative">
                <Lock
                  size={15}
                  className="absolute left-3 top-1/2 -translate-y-1/2 text-[#9aa0aa] pointer-events-none"
                />
                <input
                  id="login-password"
                  type="password"
                  autoComplete="current-password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="••••••••"
                  className="w-full pl-10 pr-3 py-3 rounded-xl bg-white/[0.06] border border-white/[0.12] text-white text-[15px] placeholder:text-[#6b7280] focus:outline-none focus:ring-2 focus:ring-[#0A84FF]/40 focus:border-[#0A84FF]/40 transition-colors"
                  data-testid="login-password"
                />
              </div>
            </div>

            <button
              type="submit"
              disabled={loading}
              className="w-full inline-flex items-center justify-center gap-2 px-4 py-3 rounded-xl bg-gradient-to-r from-[#0A84FF] to-[#7B61FF] text-white text-[14px] font-semibold hover:opacity-90 transition-opacity disabled:opacity-50"
              data-testid="login-submit"
            >
              {loading ? "Signing in…" : "Sign in"}
            </button>

            <div className="flex items-center justify-center gap-1.5 text-[11px] text-[#6b7280]">
              <Shield size={11} className="text-[#0A84FF]" />
              OAuth · Tokens encrypted with AES-256-GCM · No passwords stored
            </div>
          </form>
        </div>
      </div>
    </div>
  );
}
