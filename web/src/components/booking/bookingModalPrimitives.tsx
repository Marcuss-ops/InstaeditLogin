import type { ReactNode } from "react";
import { CheckCircle2 } from "lucide-react";

/* --------------------------------------------------------------------------
 * Reusable primitives
 * ------------------------------------------------------------------------ */

/**
 * Visible question number ("01" / "02" / "03") + the question text +
 * a one-line hint. Wrapped in a <fieldset> so the radio group is
 * properly associated with its label for screen readers.
 */
export function Question({
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

export type Accent = "emerald" | "cyan" | "violet" | "zinc";

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

export function RadioCard({
  Icon,
  accent,
  badge,
  title,
  hint,
  selected,
  onSelect,
}: {
  Icon: typeof CheckCircle2;
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
