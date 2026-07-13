import { useEffect, useState } from "react";
import { Cookie } from "lucide-react";

const STORAGE_KEY = "instaedit.cookie-consent.v1";

type Consent = "accepted" | "essential" | null;

/**
 * Minimal GDPR cookie consent banner.
 *
 * We do NOT set any non-essential client-side cookies or run any
 * non-essential analytics/tracking. The HttpOnly session cookie that
 * the backend drops is the only one we need, so the "essential only"
 * choice is functionally equivalent to "accept all" for as long as
 * we don't add tracking. The banner exists so an EU visitor sees an
 * explicit "I agree" prompt on first visit and the consent timestamp
 * is recorded in localStorage for audit.
 */
export function CookieBanner() {
  const [consent, setConsent] = useState<Consent>(null);
  const [visible, setVisible] = useState(false);

  useEffect(() => {
    try {
      const raw = window.localStorage.getItem(STORAGE_KEY);
      if (!raw) {
        setVisible(true);
        return;
      }
      const parsed = JSON.parse(raw) as { choice?: string };
      if (parsed.choice === "accepted" || parsed.choice === "essential") {
        setConsent(parsed.choice);
        setVisible(false);
      } else {
        setVisible(true);
      }
    } catch {
      // Malformed entry — treat as no consent, the banner reappears.
      setVisible(true);
    }
  }, []);

  const choose = (value: Exclude<Consent, null>) => {
    try {
      window.localStorage.setItem(
        STORAGE_KEY,
        JSON.stringify({ choice: value, at: new Date().toISOString() }),
      );
    } catch {
      // localStorage may be unavailable in private mode; we still
      // hide the banner so it doesn't cover the UI forever.
    }
    setConsent(value);
    setVisible(false);
  };

  if (!visible || consent) return null;

  return (
    <div
      role="dialog"
      aria-live="polite"
      aria-label="Cookie notice"
      data-testid="cookie-banner"
      className="fixed bottom-4 left-4 right-4 sm:left-auto sm:right-6 sm:max-w-[460px] z-40 bg-white border border-neutral-200 rounded-2xl shadow-lg p-4 animate-[fadeUp_0.3s_ease-out]"
    >
      <div className="flex items-start gap-3">
        <div className="w-9 h-9 rounded-xl bg-gradient-to-br from-amber-500 to-orange-500 flex items-center justify-center text-white shrink-0 mt-0.5">
          <Cookie size={16} />
        </div>
        <div className="flex-1 min-w-0">
          <p className="text-[13px] font-semibold text-black mb-1">We use cookies</p>
          <p className="text-[12px] text-neutral-600 leading-relaxed">
            We set a single essential cookie to keep you signed in. We don't use tracking or
            analytics cookies. See the{" "}
            <a href="/privacy" className="underline hover:no-underline text-black">
              privacy policy
            </a>
            .
          </p>
          <div className="flex items-center gap-2 mt-3">
            <button
              type="button"
              onClick={() => choose("essential")}
              className="inline-flex items-center px-3 py-1.5 rounded-lg bg-neutral-100 hover:bg-neutral-200 text-[12px] font-semibold text-neutral-800 transition-colors"
              data-testid="cookie-essential"
            >
              Essential only
            </button>
            <button
              type="button"
              onClick={() => choose("accepted")}
              className="inline-flex items-center px-3 py-1.5 rounded-lg bg-black hover:bg-neutral-800 text-[12px] font-semibold text-white transition-colors"
              data-testid="cookie-accept"
            >
              Accept all
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
