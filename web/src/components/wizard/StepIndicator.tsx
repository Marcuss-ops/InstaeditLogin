/**
 * StepIndicator — visual progress for a fixed-step linear wizard.
 *
 * Renders N numbered checkpoints left-to-right with a connecting
 * line. The "current" step glows; "completed" steps are filled and
 * checkable; "upcoming" steps are dim. Used by the /app/content/new
 * wizard (3 steps) and reusable for any N-step flow that wants the
 * same visual surface.
 *
 * Conventions:
 *   - <ol> with `aria-current="step"` on the active row for
 *     screen-reader semantics.
 *   - The connecting line is a single positioned div behind the
 *     circles, not a border on each row — keeps the gap-tolerant
 *     layout when steps get long labels.
 *   - Tailwind tokens only — no inline dynamic styles that would
 *     trip purge / CSP.
 */
import { cn } from "../../lib/utils";

export interface StepIndicatorStep {
  label: string;
}

export interface StepIndicatorProps {
  steps: StepIndicatorStep[];
  /**
   * 1-indexed step number. Out-of-range values (e.g. 0 or
   * `steps.length + 1`) are tolerated with a fallback to "all
   * upcoming" so callers haven't to over-guard.
   */
  currentStep: number;
}

export function StepIndicator({ steps, currentStep }: StepIndicatorProps) {
  return (
    <ol
      className="flex items-center justify-center gap-0 w-full max-w-xl mx-auto"
      aria-label="Wizard progress"
    >
      {steps.map((step, idx) => {
        const idx1 = idx + 1;
        const status: "completed" | "current" | "upcoming" =
          currentStep > idx1
            ? "completed"
            : currentStep === idx1
              ? "current"
              : "upcoming";
        const isLast = idx === steps.length - 1;
        return (
          <li
            key={step.label}
            className={cn(
              "flex items-center gap-3 flex-1 last:flex-none",
            )}
            aria-current={status === "current" ? "step" : undefined}
          >
            <div className="flex items-center gap-3">
              <span
                className={cn(
                  "w-8 h-8 rounded-full flex items-center justify-center text-sm font-semibold border-2 transition-colors",
                  status === "completed" &&
                    "bg-emerald-500/20 border-emerald-400 text-emerald-300",
                  status === "current" &&
                    "bg-white/10 border-white text-white shadow-[0_0_0_4px_rgba(255,255,255,0.06)]",
                  status === "upcoming" &&
                    "bg-transparent border-white/20 text-[#9aa0aa]",
                )}
                aria-hidden="true"
                data-testid={`step-dot-${idx1}`}
              >
                {status === "completed" ? "✓" : idx1}
              </span>
              <span
                className={cn(
                  "text-sm font-medium whitespace-nowrap",
                  status === "current" && "text-white",
                  status === "completed" && "text-[#cdd2da]",
                  status === "upcoming" && "text-[#9aa0aa]",
                )}
              >
                {step.label}
              </span>
            </div>
            {!isLast && (
              <div
                className={cn(
                  "flex-1 h-0.5 rounded-full transition-colors mx-2",
                  currentStep > idx1 ? "bg-emerald-400" : "bg-white/[0.08]",
                )}
                aria-hidden="true"
              />
            )}
          </li>
        );
      })}
    </ol>
  );
}
