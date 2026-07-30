/**
 * TagInput — controlled chip-list input.
 *
 * Add semantics:
 *   - Enter / "," commit the current draft
 *   - Blur commits the draft (autosaves partial typing)
 *   - Backspace on empty input pops the last tag (convenience)
 *
 * Dedup is case-insensitive (the first-entered casing is kept).
 * Max 30 tags matches the YouTube Data API v3 hard limit so the
 * wizard UI gives immediate feedback before the server rejects at
 * /complete. ChannelMetadataStep passes its own limit per platform
 * later.
 *
 * Strict no-state-on-unmount: the inner state is `draft` text only;
 * the parent owns the canonical `value: string[]` so a back-then-
 * forward wizard navigation preserves the entry verbatim.
 */
import { useState } from "react";
import { X } from "lucide-react";
import { cn } from "../../lib/utils";

export interface TagInputProps {
  /** Controlled tag list — order is preserved. */
  value: string[];
  onChange: (next: string[]) => void;
  /** Cap; default 30. */
  maxTags?: number;
  placeholder?: string;
  disabled?: boolean;
  /** Aria-label for the inner input. */
  ariaLabel?: string;
  /** Optional test-id prefix to identify this instance. */
  testIdPrefix?: string;
}

const DEFAULT_MAX_TAGS = 30;

export function TagInput({
  value,
  onChange,
  maxTags = DEFAULT_MAX_TAGS,
  placeholder = "Aggiungi un tag…",
  disabled = false,
  ariaLabel = "Tag",
  testIdPrefix = "tag",
}: TagInputProps) {
  const [draft, setDraft] = useState("");

  const commit = (raw: string): void => {
    const next = raw.trim();
    if (!next) return;
    if (value.length >= maxTags) return;
    if (
      value.some((t) => t.toLowerCase() === next.toLowerCase())
    ) {
      // Drop the draft silently — the user already saw the visual
      // chip before retrying.
      setDraft("");
      return;
    }
    onChange([...value, next]);
    setDraft("");
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>): void => {
    if (e.key === "Enter" || e.key === ",") {
      e.preventDefault();
      commit(draft);
    } else if (e.key === "Backspace" && draft === "" && value.length > 0) {
      // Convenience: empty input + backspace pops the last chip.
      onChange(value.slice(0, -1));
    }
  };

  const handleBlur = (): void => {
    commit(draft);
  };

  const removeAt = (i: number): void => {
    onChange(value.filter((_, idx) => idx !== i));
  };

  const atCap = value.length >= maxTags;

  return (
    <div
      className={cn(
        "flex flex-wrap gap-1.5 items-center rounded-xl bg-[#0a0a10] border border-white/[0.12] px-2.5 py-2 transition-colors",
        "focus-within:ring-2 focus-within:ring-white/30 focus-within:border-white/30",
        disabled && "opacity-60 cursor-not-allowed",
      )}
      data-testid={`${testIdPrefix}-container`}
      role="group"
      aria-label={ariaLabel}
    >
      {value.map((tag, i) => (
        <span
          // Stable key for React reconciliation: tag text +
          // index ensures dedups after a re-add don't collapse.
          key={`${tag}-${i}`}
          className="inline-flex items-center gap-1 rounded-lg bg-white/[0.08] border border-white/[0.12] px-2 py-0.5 text-sm text-white"
          data-testid={`${testIdPrefix}-chip`}
        >
          {tag}
          <button
            type="button"
            onClick={() => removeAt(i)}
            disabled={disabled}
            className="text-[#9aa0aa] hover:text-white ml-0.5 disabled:cursor-not-allowed"
            aria-label={`Rimuovi tag ${tag}`}
            data-testid={`${testIdPrefix}-remove`}
          >
            <X size={12} aria-hidden="true" />
          </button>
        </span>
      ))}
      <input
        type="text"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={handleKeyDown}
        onBlur={handleBlur}
        disabled={disabled || atCap}
        placeholder={value.length === 0 ? placeholder : ""}
        aria-label={ariaLabel}
        className="flex-1 min-w-[140px] bg-transparent text-white placeholder:text-[#5c6473] text-sm outline-none"
        data-testid={`${testIdPrefix}-draft`}
      />
      {atCap && (
        <span
          className="text-xs text-[#9aa0aa]"
          data-testid={`${testIdPrefix}-capped`}
        >
          max {maxTags}
        </span>
      )}
    </div>
  );
}
