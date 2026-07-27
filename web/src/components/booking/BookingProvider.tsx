import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
} from "react";
import {
  ArrowRight,
  Bot,
  Calendar,
  CheckCircle2,
  Clock,
  Rocket,
  ShieldCheck,
  Sparkles,
  TrendingUp,
  Wallet,
  X,
} from "lucide-react";
import {
  BOOKING_URL,
  BUDGET_OPTIONS,
  GOAL_OPTIONS,
  INTENT_TIER_LABEL,
  MONTHLY_CAPACITY_LABEL,
  READY_OPTIONS,
  isQualificationComplete,
  type BookingBudget,
  type BookingGoal,
  type BookingIntent,
  type BookingQualification,
  type BookingReady,
} from "../../lib/booking";
import { submitBookingEvent } from "../../lib/booking-api";

// Re-export so consumers (Mentoring, CTASection) can import the type
// alongside `useBooking` from the same module — keeps the public
// surface of the booking feature in one place.
export type { BookingIntent };

/* ----------------------------------------------------------------------------
 * Booking context — exposes `open(intent?)` to every CTA in the app so
 * any component can fan-in to the same modal without prop drilling.
 *
 * Wrapping lives in App.tsx so the modal can be opened from anywhere
 * inside the Router (Nav, Hero, FinalCTA on /, the Mentoring page,
 * EditorContact inside InternalLayout, and the OAuth Login page).
 *
 * The funnel is strictly single-track: every CTA in the app — Nav,
 * Hero, FinalCTA on /, the Mentoring page, EditorContact inside the
 * authenticated shell, and the OAuth Login page — opens the same
 * modal. Conversion is concentrated on the booking CTA so leads can't
 * spike a chat channel with low-intent traffic; the modal still
 * carries the three qualification fields so the qualify→schedule
 * handoff stays warm.
 * -------------------------------------------------------------------------- */

type BookingCtx = {
  isOpen: boolean;
  intent: BookingIntent;
  open: (intent?: BookingIntent) => void;
  close: () => void;
};

const Ctx = createContext<BookingCtx | null>(null);

export function BookingProvider({ children }: { children: ReactNode }) {
  const [isOpen, setIsOpen] = useState(false);
  // Last intent passed to `open()`. Used by the modal to render a
  // tier-tinted chip ("Tier 2 · Growth"). Default "general" on mount
  // — after open() it always reflects the most recent call.
  const [intent, setIntent] = useState<BookingIntent>("general");
  // Whether <BookingModal> is currently mounted in the DOM tree.
  // We track this separately from `isOpen` so the modal stays
  // mounted during the 280ms opacity fade-out AFTER `isOpen`
  // flips to false, but is COMPLETELY absent from the DOM when
  // the modal has never been opened (initial mount) or after
  // the fade-out has finished. This guarantees the modal's
  // backdrop button can never appear as the topmost
  // hit-test target at any pixel on the page — solving the
  // Playwright actionability failure where the backdrop was
  // intercepting pointer events on the Hero CTA below.
  const [mounted, setMounted] = useState(false);

  const open = useCallback((next: BookingIntent = "general") => {
    setIntent(next);
    setIsOpen(true);
    setMounted(true);
  }, []);
  const close = useCallback(() => setIsOpen(false), []);

  // Unmount after the fade-out completes. Only runs when isOpen
  // toggles to false; on initial mount the modal is already not
  // mounted (mounted=false) so the CTA underneath is fully
  // hit-testable from frame 1.
  useEffect(() => {
    if (isOpen) return;
    const timeout = setTimeout(() => setMounted(false), 280);
    return () => clearTimeout(timeout);
  }, [isOpen]);

  const value = useMemo<BookingCtx>(
    () => ({ isOpen, intent, open, close }),
    [isOpen, intent, open, close],
  );

  return (
    <Ctx.Provider value={value}>
      {children}
      {mounted && (
        <BookingModal intent={intent} isOpen={isOpen} onClose={close} />
      )}
    </Ctx.Provider>
  );
}

export function useBooking(): BookingCtx {
  const ctx = useContext(Ctx);
  if (!ctx) {
    throw new Error("useBooking must be used inside <BookingProvider>");
  }
  return ctx;
}

/* --------------------------------------------------------------------------
 * Modal
 * ------------------------------------------------------------------------ */

const EMPTY_QUALIFICATION: BookingQualification = {
  goal: null,
  budget: null,
  ready: null,
};

function BookingModal({
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
  function setBudget(value: BookingBudget) {
    setQualification((prev) => ({ ...prev, budget: value }));
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
      budget: qualification.budget!,
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
            onBudget={setBudget}
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

/* --------------------------------------------------------------------------
 * Step views
 * ------------------------------------------------------------------------ */

function FormStep({
  titleId,
  descriptionId,
  tierChip,
  qualification,
  complete,
  onGoal,
  onBudget,
  onReady,
  onSubmit,
}: {
  titleId: string;
  descriptionId: string;
  tierChip: string;
  qualification: BookingQualification;
  complete: boolean;
  onGoal: (value: BookingGoal) => void;
  onBudget: (value: BookingBudget) => void;
  onReady: (value: BookingReady) => void;
  onSubmit: (e: FormEvent) => void;
}) {
  return (
    <form onSubmit={onSubmit} className="p-7 sm:p-10" noValidate={false}>
      <header className="mb-7">
        <div className="flex flex-wrap items-center gap-2 mb-4">
          <span className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full surface-glass border border-red-400/30 text-[11px] font-semibold text-red-300">
            <Clock className="w-3 h-3" />
            <span>Limited — {MONTHLY_CAPACITY_LABEL}</span>
          </span>
          <span
            data-testid="booking-tier-chip"
            className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-white/[0.06] border border-white/10 text-[11px] font-semibold text-zinc-200"
          >
            <Sparkles className="w-3 h-3 text-violet-300" />
            <span>{tierChip}</span>
          </span>
        </div>
        <h2
          id={titleId}
          className="text-display-2 text-white max-w-[22ch]"
        >
          Schedule your{" "}
          <span className="text-gradient">free strategy call.</span>
        </h2>
        <p
          id={descriptionId}
          className="text-sm text-zinc-400 mt-3 max-w-[52ch]"
        >
          Three quick questions so we map the right plan in 30 minutes —
          even if you're starting from scratch.
        </p>
      </header>

      <div className="space-y-7">
        <Question index={1} question="What is your primary goal right now?" hint="Pick the path that matches where you are today.">
          <div role="radiogroup" aria-label="Primary goal" className="grid sm:grid-cols-3 gap-3">
            {GOAL_OPTIONS.map((opt, i) => {
              const Icon = i === 0 ? Rocket : i === 1 ? TrendingUp : Bot;
              return (
                <RadioCard
                  key={opt.value}
                  Icon={Icon}
                  accent={opt.accent}
                  badge={opt.tier}
                  title={opt.label}
                  hint={opt.hint}
                  selected={qualification.goal === opt.value}
                  onSelect={() => onGoal(opt.value)}
                />
              );
            })}
          </div>
        </Question>

        <Question
          index={2}
          question="What budget have you reserved for channel setup and production?"
          hint="We'll only recommend a plan that matches this number."
        >
          <div role="radiogroup" aria-label="Budget" className="grid gap-2.5">
            {BUDGET_OPTIONS.map((opt) => (
              <BudgetRadio
                key={opt.value}
                title={opt.label}
                selected={qualification.budget === opt.value}
                onSelect={() => onBudget(opt.value)}
              />
            ))}
          </div>
        </Question>

        <Question
          index={3}
          question="If it's a fit on the call, ready to get started this week?"
          hint="This helps us block time for the right plan immediately."
        >
          <div role="radiogroup" aria-label="Readiness" className="grid sm:grid-cols-2 gap-3">
            {READY_OPTIONS.map((opt) => (
              <RadioCard
                key={opt.value}
                Icon={opt.value === "yes" ? CheckCircle2 : Clock}
                accent={opt.accent}
                badge={opt.value === "yes" ? "Hot lead" : "Exploring"}
                title={opt.label}
                hint={opt.hint}
                selected={qualification.ready === opt.value}
                onSelect={() => onReady(opt.value)}
              />
            ))}
          </div>
        </Question>
      </div>

      <div className="mt-8 flex flex-col sm:flex-row items-stretch sm:items-center gap-3">
        <button
          type="submit"
          disabled={!complete}
          aria-disabled={!complete}
          className="group relative inline-flex items-center justify-center gap-2 px-7 py-3.5 rounded-xl bg-white text-black font-semibold text-sm transition-all disabled:opacity-40 disabled:cursor-not-allowed enabled:hover:bg-white/90 enabled:hover:shadow-[0_0_40px_-8px_rgba(255,255,255,0.35)]"
        >
          <Calendar className="w-4 h-4" />
          Schedule my free call
          <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
        </button>
      </div>

      <div className="mt-5 flex items-center gap-2 text-[11px] text-zinc-500">
        <ShieldCheck className="w-3.5 h-3.5 text-emerald-400/80" aria-hidden="true" />
        <span>Three clicks, ~45 seconds. No spam — and we won't surprise-call you.</span>
      </div>
    </form>
  );
}

function SuccessStep({
  titleId,
  descriptionId,
  onClose,
}: {
  titleId: string;
  descriptionId: string;
  onClose: () => void;
}) {
  return (
    <div className="p-10 sm:p-14 text-center">
      <div
        aria-hidden="true"
        className="inline-flex w-14 h-14 items-center justify-center rounded-2xl bg-gradient-to-br from-violet-500 to-cyan-400 text-white mb-5 shadow-[0_10px_40px_-10px_rgba(124,58,237,0.6)]"
      >
        <Sparkles className="w-6 h-6" />
      </div>
      <h3 id={titleId} className="text-display-3 text-white">
        Opening the scheduler…
      </h3>
      <p id={descriptionId} className="text-sm text-zinc-400 mt-3 max-w-[44ch] mx-auto">
        A new tab should have opened with the available slots. If it
        didn't, hit the button below — and we'll see you on the call.
      </p>
      <a
        href={BOOKING_URL}
        target="_blank"
        rel="noopener noreferrer"
        className="mt-6 inline-flex items-center gap-2 px-6 py-3 rounded-xl bg-white text-black text-sm font-semibold hover:bg-white/90 transition-colors"
      >
        <Calendar className="w-4 h-4" />
        Open scheduler
        <ArrowRight className="w-4 h-4" />
      </a>
      <div className="mt-8">
        <button
          type="button"
          onClick={onClose}
          className="text-xs text-zinc-500 hover:text-zinc-300 transition-colors"
        >
          Close
        </button>
      </div>
    </div>
  );
}

/* --------------------------------------------------------------------------
 * Reusable primitives
 * ------------------------------------------------------------------------ */

/**
 * Visible question number ("01" / "02" / "03") + the question text +
 * a one-line hint. Wrapped in a <fieldset> so the radio group is
 * properly associated with its label for screen readers.
 */
function Question({
  index,
  question,
  hint,
  children,
}: {
  index: number;
  question: string;
  hint: string;
  children: ReactNode;
}) {
  return (
    <fieldset className="border-0 p-0 m-0 space-y-3">
      <div className="flex items-start gap-3">
        <span
          aria-hidden="true"
          className="mt-0.5 inline-flex w-7 h-7 items-center justify-center rounded-lg bg-violet-500/15 ring-1 ring-violet-400/30 text-[11px] font-bold text-violet-300 tabular-nums"
        >
          {String(index).padStart(2, "0")}
        </span>
        <div className="flex-1">
          <legend className="text-sm font-semibold text-white leading-snug">
            {question}
          </legend>
          <p className="text-xs text-zinc-500 mt-1 leading-snug">{hint}</p>
        </div>
      </div>
      {children}
    </fieldset>
  );
}

type Accent = "emerald" | "cyan" | "violet" | "zinc";

const ACCENT: Record<
  Accent,
  {
    ring: string;
    border: string;
    bg: string;
    icon: string;
    chip: string;
  }
> = {
  emerald: {
    ring: "ring-emerald-400/40",
    border: "border-emerald-400/50",
    bg: "bg-emerald-500/10",
    icon: "text-emerald-300",
    chip: "bg-emerald-500/15 text-emerald-200 ring-1 ring-emerald-400/25",
  },
  cyan: {
    ring: "ring-cyan-400/40",
    border: "border-cyan-400/50",
    bg: "bg-cyan-500/10",
    icon: "text-cyan-300",
    chip: "bg-cyan-500/15 text-cyan-200 ring-1 ring-cyan-400/25",
  },
  violet: {
    ring: "ring-violet-400/40",
    border: "border-violet-400/50",
    bg: "bg-violet-500/10",
    icon: "text-violet-300",
    chip: "bg-violet-500/15 text-violet-200 ring-1 ring-violet-400/25",
  },
  zinc: {
    ring: "ring-zinc-500/30",
    border: "border-zinc-400/30",
    bg: "bg-white/[0.04]",
    icon: "text-zinc-300",
    chip: "bg-white/[0.06] text-zinc-300 ring-1 ring-white/10",
  },
};

function RadioCard({
  Icon,
  accent,
  badge,
  title,
  hint,
  selected,
  onSelect,
}: {
  Icon: typeof Rocket;
  accent: Accent;
  badge: string;
  title: string;
  hint: string;
  selected: boolean;
  onSelect: () => void;
}) {
  const a = ACCENT[accent];
  return (
    <button
      type="button"
      role="radio"
      aria-checked={selected}
      onClick={onSelect}
      className={`text-left p-4 rounded-2xl border transition-all relative ${
        selected
          ? `${a.border} ${a.bg} ring-1 ${a.ring} shadow-[0_0_30px_-12px_rgba(124,58,237,0.45)]`
          : "border-white/10 bg-white/[0.03] hover:border-white/25 hover:bg-white/[0.06]"
      }`}
    >
      <div className="flex items-center justify-between mb-2.5">
        <span
          className={`inline-flex w-9 h-9 items-center justify-center rounded-lg ring-1 ${a.ring} ${a.bg} ${a.icon}`}
        >
          <Icon className="w-4 h-4" aria-hidden="true" />
        </span>
        {selected && (
          <CheckCircle2 className={`w-4 h-4 ${a.icon}`} aria-hidden="true" />
        )}
      </div>
      <div
        className={`inline-flex px-1.5 py-0.5 rounded-md text-[10px] uppercase tracking-wider font-bold mb-1.5 ${a.chip}`}
      >
        {badge}
      </div>
      <div className="text-sm font-semibold text-white leading-tight">
        {title}
      </div>
      <div className="text-[11px] text-zinc-500 mt-1 leading-snug">
        {hint}
      </div>
    </button>
  );
}

function BudgetRadio({
  title,
  selected,
  onSelect,
}: {
  title: string;
  selected: boolean;
  onSelect: () => void;
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={selected}
      onClick={onSelect}
      className={`flex items-center gap-3 p-3.5 rounded-xl border text-left transition-all ${
        selected
          ? "border-cyan-400/50 bg-cyan-500/10 ring-1 ring-cyan-400/30 shadow-[0_0_24px_-12px_rgba(34,211,238,0.45)]"
          : "border-white/10 bg-white/[0.03] hover:border-white/25 hover:bg-white/[0.06]"
      }`}
    >
      <span
        className={`inline-flex w-9 h-9 items-center justify-center rounded-lg shrink-0 ${
          selected
            ? "bg-cyan-500/20 text-cyan-200"
            : "bg-white/[0.04] text-zinc-400"
        }`}
      >
        <Wallet className="w-4 h-4" aria-hidden="true" />
      </span>
      <span className="flex-1 min-w-0">
        <span className="block text-sm font-semibold text-white">
          {title}
        </span>
      </span>
      {selected && (
        <CheckCircle2
          className="w-4 h-4 text-cyan-300 shrink-0"
          aria-hidden="true"
        />
      )}
    </button>
  );
}
