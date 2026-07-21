import { Link } from "react-router-dom";
import { ArrowRight, Phone, Send, Mail } from "lucide-react";
import {
  CONTACT_PHONE_DISPLAY,
  CONTACT_PHONE_TEL,
  CONTACT_DISCORD_URL,
  CONTACT_DISCORD_HANDLE,
  CONTACT_EMAIL,
  CONTACT_EMAIL_DISPLAY,
} from "./shared";

export function EditorContact() {
  return (
    <section className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div
        aria-hidden="true"
        className="absolute inset-0 cta-glow pointer-events-none"
      />
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">\n        <div className="glow-orb bg-violet-500 w-[440px] h-[440px] -top-32 -left-24 animate-drift-slow opacity-55" />\n        <div className="glow-orb bg-emerald-400 w-[380px] h-[380px] -bottom-32 -right-24 animate-drift-rev opacity-40" />\n      </div>

      <div className="relative mx-auto max-w-5xl px-6">
        <div className="surface-glass border border-white/15 rounded-3xl px-8 py-14 sm:px-14 sm:py-16 text-center relative overflow-hidden shadow-[0_40px_120px_-40px_rgba(124,58,237,0.5)] animate-fade-up">
          <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full surface-glass border border-white/15 text-xs font-medium text-zinc-200 mb-6">\n            <Phone
              className="w-3.5 h-3.5 text-emerald-400"
              aria-hidden="true"
              focusable="false"
            />
            <span>            Talk to the team</span>
          </div>

          <h2 className="text-display-2 text-white max-w-[24ch] mx-auto">
            Want a <span className="text-gradient">closer look?</span>
          </h2>

          {/* Italian call-out — surfaces the user-requested wording
              as an intentional bilingual chip so it reads as deliberate
              copy, not a translation patch sitting inside an otherwise
              English meta row. \*/}
          <div className="mt-5 inline-flex items-center gap-2 px-3 py-1.5 rounded-full bg-emerald-500/10 ring-1 ring-emerald-400/25 text-xs font-medium text-emerald-200">
            <Phone
              className="w-3 h-3"
              aria-hidden="true"
              focusable="false"
            />
            <span>
              Call{" "}
              <span className="tabular-nums font-semibold">
                {CONTACT_PHONE_DISPLAY}
              </span>{" "}
              for more info
            </span>
          </div>

          <p className="text-body-lg text-zinc-300/90 mt-6 max-w-[52ch] mx-auto">            For custom demos, tailored workflows, or any need that doesn't fit the self-service flow — give us a call. We'll show you what's possible for your team in under ten minutes.
          </p>

          {/* Primary CTA — phone stays the dominant action (white pill).
              Alt-channel options (Telegram, Email, Login) live in a
              second, equally-weighted row so the visual hierarchy reads
              "call us" → "or pick another channel". Telegram opens in a
              new tab with rel=noopener noreferrer per OWASP guidance. */}
          <div className="mt-9 flex items-center justify-center">
            <a
              href={`tel:${CONTACT_PHONE_TEL}`}
              className="group inline-flex items-center gap-3 px-6 py-3.5 rounded-full bg-white text-black text-base font-semibold hover:bg-zinc-100 transition-colors shadow-[0_10px_40px_-10px_rgba(255,255,255,0.55)]"
              aria-label={`Call ${CONTACT_PHONE_DISPLAY} for more information`}
            >
              <Phone
                className="w-5 h-5 group-hover:rotate-[-12deg] transition-transform"
                aria-hidden="true"
                focusable="false"
              />
              <span className="tabular-nums font-semibold">
                {CONTACT_PHONE_DISPLAY}
              </span>
            </a>
            <Link
              to="/login"
              className="inline-flex items-center gap-2 px-6 py-3.5 rounded-full surface-glass text-zinc-200 font-medium hover:text-white hover:border-white/25 transition-colors"
            >
              Or create an account
              <ArrowRight className="w-5 h-5" />
            </Link>
          </div>

          <div className="mt-5 flex items-center justify-center gap-3 flex-wrap">
            <a
              href={CONTACT_DISCORD_URL}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 px-5 py-3 rounded-full surface-glass text-sm font-medium text-zinc-200 hover:text-white hover:border-white/25 transition-colors"
              aria-label={`Open Discord chat with ${CONTACT_DISCORD_HANDLE}`}
            >
              <Send
                className="w-4 h-4 text-sky-300"
                aria-hidden="true"
                focusable="false"
              />
              <span>
                Discord <span className="text-zinc-400">{CONTACT_DISCORD_HANDLE}</span>
              </span>
            </a>
            <a
              href={`mailto:${CONTACT_EMAIL}`}
              className="inline-flex items-center gap-2 px-5 py-3 rounded-full surface-glass text-sm font-medium text-zinc-200 hover:text-white hover:border-white/25 transition-colors"
              aria-label={`Email ${CONTACT_EMAIL}`}
              title={CONTACT_EMAIL}
            >
              <Mail
                className="w-4 h-4 text-violet-300"
                aria-hidden="true"
                focusable="false"
              />
              <span>
                Email{" "}
                <span className="text-zinc-400">
                  {CONTACT_EMAIL_DISPLAY}
                </span>
              </span>
            </a>
          </div>

          <div className="mt-7 text-xs text-zinc-500 flex items-center justify-center gap-2 flex-wrap">
            <span>Lun–Ven · 09:00–18:00 CET</span>
            <span aria-hidden="true">·</span>
            <span>Anche su WhatsApp</span>
          </div>
        </div>
      </div>
    </section>
  );
}
