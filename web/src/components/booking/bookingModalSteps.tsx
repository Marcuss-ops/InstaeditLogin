import type { FormEvent } from "react";
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
} from "lucide-react";
import {
  BOOKING_URL,
  GOAL_OPTIONS,
  MONTHLY_CAPACITY_LABEL,
  READY_OPTIONS,
  type BookingGoal,
  type BookingQualification,
  type BookingReady,
} from "../../lib/booking";
import { Question, RadioCard } from "./bookingModalPrimitives";

/* --------------------------------------------------------------------------
 * Step views
 * ------------------------------------------------------------------------ */

export function FormStep({
  titleId,
  descriptionId,
  tierChip,
  qualification,
  complete,
  onGoal,
  onReady,
  onSubmit,
}: {
  titleId: string;
  descriptionId: string;
  tierChip: string;
  qualification: BookingQualification;
  complete: boolean;
  onGoal: (value: BookingGoal) => void;
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
          Two quick questions so we map the right plan in 30 minutes —
          even if you're starting from scratch.
        </p>
      </header>

      <div className="space-y-7">
        <Question
          index={1}
          question="What is your primary goal right now?"
          hint="Pick the path that matches where you are today."
        >
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
          className="group relative inline-flex items-center justify-center gap-2 px-7 py-4 rounded-xl bg-gradient-to-r from-orange-500 via-red-500 to-pink-500 text-white font-semibold text-base transition-all disabled:opacity-40 disabled:cursor-not-allowed enabled:hover:shadow-[0_0_50px_-8px_rgba(239,68,68,0.55)] enabled:hover:scale-[1.02] enabled:active:scale-100 [text-shadow:0_1px_2px_rgba(0,0,0,0.45),0_0_12px_rgba(0,0,0,0.25)]"
        >
          <Calendar className="w-5 h-5" />
          Schedule your free strategy call
          <ArrowRight className="w-5 h-5 group-hover:translate-x-1 transition-transform" />
        </button>
      </div>

      <div className="mt-5 flex items-center gap-2 text-[11px] text-zinc-500">
        <ShieldCheck className="w-3.5 h-3.5 text-emerald-400/80" aria-hidden="true" />
        <span>Three clicks, ~45 seconds. No spam — and we won't surprise-call you.</span>
      </div>
    </form>
  );
}

export function SuccessStep({
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
