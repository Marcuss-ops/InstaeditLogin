import { useCallback, useEffect, useState } from "react";
import { CheckCircle2, Mail, Shield, Sparkles, MailCheck, AlertCircle, RefreshCw } from "lucide-react";
import { API_BASE_URL } from "../lib/api";
import { ApiError, clearSessionCache } from "../lib/auth";

type Phase =
  | { kind: "email" }
  | { kind: "sent"; email: string; devToken: string | null }
  | { kind: "verifying" };

type Toast = { kind: "ok" | "err"; message: string } | null;

/**
 * Magic-link email-first login.
 *
 * Flow:
 *   1. User enters email + clicks "Send magic link".
 *   2. POST /api/v1/auth/magic-link/start → the response includes
 *      magic_link_token only in dev. The plaintext never goes via
 *      email in production — a transactional mailer (Postmark, SES,
 *      …) emits the link to {FRONTEND_URL}/auth/callback?token=xxx.
 *   3. The user opens that link in their browser → /auth/callback
 *      → POST /magic-link/verify → 204 + session cookies set →
 *      navigate to /accounts.
 *   4. In dev, we surface the token here so the developer can
 *      either click "Verify now" (which pushes the magic-link URL
 *      onto the SPA router and lets AuthCallback do the verify) or
 *      paste the token elsewhere.
 *
 * The 7 OAuth social provider buttons that previously lived here
 * have been moved to /accounts (a separate Connect-an-account card
 * grid). The product login is email-only now so the OAuth callback
 * can require an InstaEdit session without looping back to /login.
 */
export function Login() {
  const [phase, setPhase] = useState<Phase>({ kind: "email" });
  const [email, setEmail] = useState("");
  const [toast, setToast] = useState<Toast>(null);
  const [sending, setSending] = useState(false);

  const showToast = useCallback((t: Toast) => {
    setToast(t);
    if (t) window.setTimeout(() => setToast(null), 3500);
  }, []);

  // If we landed here with a magic-link ?token= already on the URL
  // (rare — AuthCallback is the canonical consumer, but a deep link
  // copy-paste could land here), bounce to /auth/callback so the
  // single source of truth handles verify.
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    if (params.get("token")) {
      window.location.replace(
        `/auth/callback?${params.toString()}`,
      );
    }
  }, []);

  const send = useCallback(async () => {
    const trimmed = email.trim().toLowerCase();
    if (!trimmed || !trimmed.includes("@")) {
      showToast({ kind: "err", message: "Enter a valid email address." });
      return;
    }
    setSending(true);
    try {
      const resp = await fetch(
        `${API_BASE_URL}/api/v1/auth/magic-link/start`,
        {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ email: trimmed }),
        },
      );
      if (!resp.ok) {
        const data = (await resp.json().catch(() => ({}))) as { error?: string };
        showToast({
          kind: "err",
          message: data.error ?? `Server returned ${resp.status}`,
        });
        return;
      }
      const data = (await resp.json().catch(() => ({}))) as {
        magic_link_token?: string;
        email?: string;
      };
      setPhase({
        kind: "sent",
        email: trimmed,
        devToken: data.magic_link_token ?? null,
      });
      // Clear any stale session cache — we expect a fresh login.
      clearSessionCache();
    } catch (err) {
      showToast({
        kind: "err",
        message:
          err instanceof ApiError
            ? err.message
            : "Could not send the magic link.",
      });
    } finally {
      setSending(false);
    }
  }, [email, showToast]);

  const verify = useCallback(async (token: string) => {
    setPhase({ kind: "verifying" });
    try {
      const resp = await fetch(
        `${API_BASE_URL}/api/v1/auth/magic-link/verify`,
        {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ token }),
        },
      );
      if (resp.status === 204) {
        clearSessionCache();
        // Replace history (the ?token= URL never lands in the back stack).
        window.location.replace("/accounts");
        return;
      }
      const data = (await resp.json().catch(() => ({}))) as { error?: string };
      showToast({
        kind: "err",
        message: data.error ?? `Verification failed (HTTP ${resp.status})`,
      });
      setPhase({ kind: "sent", email, devToken: token });
    } catch (err) {
      showToast({
        kind: "err",
        message:
          err instanceof ApiError
            ? err.message
            : "Could not verify the magic link.",
      });
      setPhase({ kind: "sent", email, devToken: token });
    }
  }, [email, showToast]);

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
            Enter your email and we'll send you a one-time sign-in link.
            After login you'll connect your social accounts from
            the dashboard.
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
          {phase.kind === "email" && (
            <form
              className="space-y-3"
              noValidate
              onSubmit={(e) => {
                e.preventDefault();
                void send();
              }}
            >
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
              <button
                type="submit"
                disabled={sending}
                className="w-full inline-flex items-center justify-center gap-2 px-4 py-3 rounded-xl bg-gradient-to-r from-[#0A84FF] to-[#7B61FF] text-white text-[14px] font-semibold hover:opacity-90 transition-opacity disabled:opacity-50"
                data-testid="login-send"
              >
                {sending ? (
                  <>
                    <RefreshCw size={14} className="animate-spin" /> Sending…
                  </>
                ) : (
                  <>Send magic link</>
                )}
              </button>
              <div className="flex items-center justify-center gap-1.5 text-[11px] text-[#6b7280] mt-2">
                <Shield size={11} className="text-[#0A84FF]" />
                Single-use link · expires in 15 minutes
              </div>
            </form>
          )}

          {phase.kind === "sent" && (
            <div className="space-y-4" data-testid="login-sent">
              <div className="flex items-start gap-3">
                <div className="w-10 h-10 shrink-0 rounded-xl bg-[#0A84FF]/15 border border-[#0A84FF]/30 flex items-center justify-center text-[#0A84FF]">
                  <MailCheck size={18} />
                </div>
                <div className="flex-1 min-w-0">
                  <h2 className="text-[15px] font-semibold text-white mb-1">
                    Check your inbox
                  </h2>
                  <p className="text-[12px] text-[#9aa0aa] leading-relaxed break-words">
                    We sent a sign-in link to{" "}
                    <span className="font-mono text-white">{phase.email}</span>.
                    Click it within 15 minutes to finish signing in.
                  </p>
                </div>
              </div>

              {/* Defense-in-depth: the backend's pkg/api/magic_link.go
                  is the canonical gate on magic_link_token emission,
                  but this Vite build-time import.meta.env.DEV also
                  hides the token panel in prod builds so a future
                  backend regression can never leak it into the UI.
                  Note: a production frontend pointed at a dev/staging
                  API that emits the token will still show "Check your
                  inbox" without the dev panel; inspect the network
                  response from DevTools. */}
              {phase.devToken && import.meta.env.DEV && (
                <div className="rounded-xl border border-amber-500/30 bg-amber-500/10 p-4 space-y-2">
                  <div className="flex items-center gap-2 text-[12px] font-semibold text-amber-300">
                    <AlertCircle size={13} />
                    Dev mode — no email was sent
                  </div>
                  <p className="text-[11px] text-amber-200/80 leading-relaxed">
                    In production, the magic link goes via a transactional
                    mailer. In dev, the token below is what the email
                    would have included.
                  </p>
                  <div className="bg-black/40 rounded-lg p-2.5 font-mono text-[11px] text-white break-all select-all" data-testid="dev-magic-link-token">
                    {phase.devToken}
                  </div>
                  <button
                    type="button"
                    onClick={() => void verify(phase.devToken ?? "")}
                    disabled={!phase.devToken}
                    className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-amber-500/20 hover:bg-amber-500/30 text-amber-200 text-[12px] font-semibold transition-colors"
                    data-testid="login-verify-dev"
                  >
                    <CheckCircle2 size={12} />
                    Verify now (dev)
                  </button>
                </div>
              )}

              <button
                type="button"
                onClick={() => setPhase({ kind: "email" })}
                className="w-full inline-flex items-center justify-center gap-2 px-4 py-2.5 rounded-xl bg-white/[0.06] hover:bg-white/[0.10] text-[#9aa0aa] text-[13px] font-medium transition-colors"
              >
                Send to a different email
              </button>
            </div>
          )}

          {phase.kind === "verifying" && (
            <div className="flex flex-col items-center justify-center py-4 gap-3" data-testid="login-verifying">
              <div className="w-10 h-10 rounded-full border-4 border-white/[0.10] border-t-[#0A84FF] animate-spin" />
              <p className="text-[13px] text-[#9aa0aa]">Signing you in…</p>
            </div>
          )}
        </div>

        <div className="mt-10 flex flex-col items-center gap-3">
          <div className="flex items-center gap-2 text-[11px] text-[#6b7280]">
            <Shield size={11} className="text-[#0A84FF]" />
            <span>OAuth · Tokens encrypted with AES-256-GCM · No passwords stored</span>
          </div>
        </div>
      </div>
    </div>
  );
}
