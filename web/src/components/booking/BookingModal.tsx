import {
  useEffect,
  useId,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { X } from "lucide-react";
import {
  BOOKING_URL,
  INTENT_TIER_LABEL,
  isQualificationComplete,
  type BookingGoal,
  type BookingIntent,
  type BookingQualification,
  type BookingReady,
} from "../../lib/booking";
import { submitBookingEvent } from "../../lib/booking-api";
import { FormStep, SuccessStep } from "./bookingModalSteps";

/* --------------------------------------------------------------------------
 * Modal
 * ------------------------------------------------------------------------ */

// Budget is intentionally hidden from the visitor per operator direction
// ("senza chiedere budget — troppo confusa"), but the booking_events
// row in Postgres still requires the column. We default it to "starter"
// so the analytics payload keeps a low-friction value without ever
// showing a money question in the modal UI.
const EMPTY_QUALIFICATION: BookingQualification = {
  goal: null,
  budget: "starter",
  ready: null,
};

export function BookingModal({
  intent,
  isOpen,
  onClose,
}: {
  intent: BookingIntent;
  isOpen: boolean;
  onClose: () => void;
}) {
  const titleId = useId();
  const descriptionId = useId();
  const panelRef = useRef<HTMLDivElement>(null);
  const closeButtonRef = useRef<HTMLButtonElement>(null);

  const [step, setStep] = useState<"form" | "scheduling">("form");
  const [qualification, setQualification] = useState<BookingQualification>(
    EMPTY_QUALIFICATION,
  );

  /* Body-scroll lock + reset state on close. Reset is deferred so the
   * fade-out animation finishes painting first. */
  useEffect(() => {
    if (!isOpen) {
      const timeout = setTimeout(() => {
        setStep("form");
        setQualification(EMPTY_QUALIFICATION);
      }, 280);
      return () => clearTimeout(timeout);
    }
    const previous = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    // Drive initial focus to the close button so keyboard users land
    // somewhere predictable; Tab then walks into the form.
    closeButtonRef.current?.focus();
    return () => {
      document.body.style.overflow = previous;
    };
  }, [isOpen]);

  /* Escape closes — also acts as a safety net if focus ever escapes the
   * panel (the trap below should prevent that in practice). */
  useEffect(() => {
    if (!isOpen) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [isOpen, onClose]);

  /* Focus trap — only traps Tab/Shift+Tab when focus is inside the
   * panel; if focus has wandered (e.g. via a stray click) we still
   * pull it back in. Without this, screen-reader users can tab past
   * the close button into the marketing page beneath. */
  useEffect(() => {
    if (!isOpen) return;
    function handle(e: KeyboardEvent) {
      if (e.key !== "Tab" || !panelRef.current) return;
      const root = panelRef.current;
      const focusables = Array.from(
        root.querySelectorAll<HTMLElement>(
          'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex="0"]',
        ),
      ).filter((el) => !el.hasAttribute("disabled") && el.tabIndex !== -1);
      if (focusables.length === 0) return;
      const first = focusables[0];
      const last = focusables[focusables.length - 1];
      const active = document.activeElement as HTMLElement | null;
      const inside = active && root.contains(active);
      if (e.shiftKey) {
        if (!inside || active === first) {
          e.preventDefault();
          last.focus();
        }
      } else {
        if (!inside || active === last) {
          e.preventDefault();
          first.focus();
        }
      }
    }
    document.addEventListener("keydown", handle);
    return () => document.removeEventListener("keydown", handle);
  }, [isOpen]);

  function setGoal(value: BookingGoal) {
    setQualification((prev) => ({ ...prev, goal: value }));
  }
  function setReady(value: BookingReady) {
    setQualification((prev) => ({ ...prev, ready: value }));
  }

  function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (!isQualificationComplete(qualification)) return;
    setStep("scheduling");

    // Fire-and-forget the telemetry POST. We deliberately do NOT
    // await — the visitor→booked-call conversion is worth orders of
    // magnitude more than the lead capture ping, so a 5xx on this
    // telemetry endpoint must NEVER block the Calendly popup from
    // opening. Errors are logged so an operator can run a batch
    // query on the backend if telemetry suddenly disappears, but
    // the UX stays conversion-first.
    //
    // The qualification payload is non-null at this point because
    // `isQualificationComplete` returned true; the bang assertions
    // are safe (and the TS narrowing survives the setTimeout).
    //
    // `metadata.utm_source` rides the POST as a fallback for the
    // case where Google Appointment Schedules strips the query
    // string on its 302 hop (verified empirically: the redirect
    // from calendar.app.google/<id>?utm_source=… drops everything
    // past `?` before forwarding). Without this passthrough the
    // booking_events row would lack any source-attribution even
    // though the SPA knows the entry point — the sales dashboard
    // would then aggregate leads as "unknown" while the same user
    // is logged as `utm_source=instagram_landing` on the analytics
    // side. Middleware: pkg/api/booking_events.go → handler reads
    // payload.Metadata and the repository JSON-marshals it into
    // the migration 076 metadata JSONB column.
    void submitBookingEvent({
      intent,
      goal: qualification.goal!,
      budget: qualification.budget,
      ready: qualification.ready!,
      metadata: { utm_source: "instagram_landing" },
    }).catch((err: unknown) => {
      // eslint-disable-next-line no-console -- intentional diagnostic
      console.warn(
        "[booking] telemetry POST failed; continuing to scheduler",
        err,
      );
    });

    // Defer the popup so React paints the success state first. If
    // popups are blocked, the success screen still shows the manual
    // "Open scheduler" button so the user isn't stranded.
    setTimeout(() => {
      try {
        window.open(BOOKING_URL, "_blank", "noopener,noreferrer");
      } catch {
        /* popup blocked — handled by the on-screen fallback link */
      }
    }, 220);
  }

  const complete = isQualificationComplete(qualification);
  const tierChip = INTENT_TIER_LABEL[intent];

  return (
    <div
      aria-hidden={!isOpen}
      data-state={isOpen ? "open" : "closed"}
      className={`fixed inset-0 z-[60] flex items-center justify-center px-4 sm:px-6 py-8 sm:py-12 transition-opacity duration-300 ${
        isOpen
          ? "opacity-100 pointer-events-auto"
          : "opacity-0 pointer-events-none"
      }`}
    >
      {/* Backdrop. Using <button> so keyboard users can dismiss by
          Tab → Enter; click-outside-to-close is the standard modal
          contract. aria-label gives screen readers a name since the
          element has no visible text. */}
      <button
        type="button"
        tabIndex={-1}
        onClick={onClose}
        aria-label="Close booking dialog"
        className="absolute inset-0 bg-[#020208]/80 backdrop-blur-md"
      />

      {/* Ambient color orbs — same recipe as the marketing pages so
          the modal feels like an extension of the page rather than
          a hard overlay. */}
      <div aria-hidden="true" className="pointer-events-none absolute inset-0 overflow-hidden">
        <div className="glow-orb bg-violet-500 w-[440px] h-[440px] -top-32 -left-24 animate-drift-slow opacity-50" />
        <div className="glow-orb bg-cyan-400 w-[380px] h-[380px] -bottom-32 -right-24 animate-drift-rev opacity-40" />
      </div>

      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={descriptionId}
        // max-h + overflow-y-auto makes the panel scroll on short
        // viewports (≤720px) where the 3-question form exceeds the
        // viewport height. Without it, the submit button is clipped
        // by the panel and Playwright's actionability check reports
        // "element is outside of the viewport" — same failure mode
        // real users on small laptops hit. `100dvh` (mobile-safe
        // viewport unit that handles browser chrome collapse) is the
        // base; `100vh` takes over from `sm:` upward where the
        // viewport is stable. `overflow-hidden` is dropped because
        // scrollbars need `overflow-y-auto` to render.
        className="relative w-full max-w-2xl surface-card rounded-3xl shadow-[0_50px_140px_-50px_rgba(124,58,237,0.65)] animate-fade-up max-h-[calc(100dvh-4rem)] sm:max-h-[calc(100vh-4rem)] overflow-y-auto"
      >
        {/* Top accent ribbon — same gradient family as the marketing
            "featured" tier card. `sticky top-0` keeps it pinned at
            the top of the panel as the form scrolls underneath. */}
        <div
          aria-hidden="true"
          className="sticky top-0 left-0 right-0 z-10 h-1 bg-gradient-to-r from-violet-500 via-cyan-400 to-pink-400"
        />

        {/* Close button — `sticky top-1 right-4` pins it to the top-
            right of the scrollable panel so it stays reachable when
            the user scrolls past the header to fill the form.
            Otherwise the X disappears off-screen mid-scroll and the
            user has to scroll back up to dismiss the modal.
            z-20 keeps it above the z-10 accent ribbon. */}
        <button
          ref={closeButtonRef}
          type="button"
          onClick={onClose}
          aria-label="Close dialog"
          className="sticky top-1 right-4 z-20 inline-flex w-9 h-9 items-center justify-center rounded-full bg-white/[0.06] border border-white/10 text-zinc-300 hover:bg-white/[0.12] hover:text-white transition-colors"
        >
          <X className="w-4 h-4" />
        </button>

        {step === "form" ? (
          <FormStep
            titleId={titleId}
            descriptionId={descriptionId}
            tierChip={tierChip}
            qualification={qualification}
            complete={complete}
            onGoal={setGoal}
            onReady={setReady}
            onSubmit={handleSubmit}
          />
        ) : (
          <SuccessStep
            titleId={titleId}
            descriptionId={descriptionId}
            onClose={onClose}
          />
        )}
      </div>
    </div>
  );
}
