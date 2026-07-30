/**
 * ChannelVideoFilters — chip-row filter above the video grid on the
 * channel page (Blocco #2).
 *
 * Spec chips (Italian): Tutti / Privati / Non in elenco / Pubblici.
 *
 * Selection drives the parent page's call to
 *   GET /api/v1/accounts/{accountId}/content?privacy=…
 * with `?privacy=` omitted when the filter is `"all"` (so the server
 * returns the unfiltered page). See {@link buildPrivacyParam}.
 *
 * Initial value is **always** `"all"` — the spec explicitly warns
 * that starting on "Privati" hides the just-promoted public video
 * and looks like a bug.
 *
 * Accessibility:
 *   • Render as `role="radiogroup"` with role="radio" children so
 *     keyboard users can ←/→ between filters (mirrors the
 *     ChannelMetadataStep chip pattern).
 *   • `aria-checked` mirrors the visual selection.
 *   • Each chip is a `<button>` so left-click + Enter both work.
 */
import { cn } from "../../../lib/utils";
import type { PrivacyFilter } from "../types";

export interface ChannelVideoFiltersProps {
  value: PrivacyFilter;
  onChange: (next: PrivacyFilter) => void;
  /**
   * Optional per-chip counts (rendered as a small subscript pill on
   * the chip). `undefined` hides the counter — pages call this with
   * any precomputed shape; if no counts are passed, the row renders
   * as a plain filter strip.
   */
  counts?: Partial<Record<PrivacyFilter, number>>;
  disabled?: boolean;
}

interface ChipDef {
  id: PrivacyFilter;
  label: string;
}

const CHIPS: ChipDef[] = [
  { id: "all", label: "Tutti" },
  { id: "private", label: "Privati" },
  { id: "unlisted", label: "Non in elenco" },
  { id: "public", label: "Pubblici" },
];

export function ChannelVideoFilters({
  value,
  onChange,
  counts,
  disabled,
}: ChannelVideoFiltersProps) {
  return (
    <div
      role="radiogroup"
      aria-label="Filtra i video per privacy"
      className="flex flex-wrap items-center gap-2 mb-4"
      data-testid="channel-video-filters"
    >
      {CHIPS.map((chip) => {
        const isActive = chip.id === value;
        const count = counts?.[chip.id];
        return (
          <button
            key={chip.id}
            type="button"
            role="radio"
            aria-checked={isActive}
            disabled={disabled}
            onClick={() => {
              if (chip.id !== value) onChange(chip.id);
            }}
            className={cn(
              "inline-flex items-center gap-2 px-3 py-1.5 rounded-full text-[12px] font-semibold border transition-colors",
              "focus:outline-none focus:ring-2 focus:ring-white/30",
              isActive
                ? "bg-white text-[#030308] border-white"
                : "bg-white/[0.04] text-[#9aa0aa] border-white/[0.08] hover:bg-white/[0.08] hover:text-white",
              disabled && "opacity-50 cursor-not-allowed",
            )}
            data-testid={`channel-video-filter-${chip.id}`}
          >
            <span>{chip.label}</span>
            {count != null && (
              <span
                className={cn(
                  "inline-flex items-center justify-center min-w-[20px] h-[20px] px-1 rounded-full text-[10px] tabular-nums",
                  isActive
                    ? "bg-[#030308]/15 text-[#030308]"
                    : "bg-white/[0.08] text-[#9aa0aa]",
                )}
                aria-hidden="true"
              >
                {count}
              </span>
            )}
          </button>
        );
      })}
    </div>
  );
}
