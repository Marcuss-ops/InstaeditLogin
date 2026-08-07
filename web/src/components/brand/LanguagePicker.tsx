import { useEffect, useRef, useState } from "react";
import { Check, ChevronDown } from "lucide-react";
import { LANGUAGE_OPTIONS, LanguageFlag, languageLabel } from "./LanguageFlag";

const CLEAR_OPTION = { code: "", name: "Nessuna lingua" } as const;
const PICKER_OPTIONS = [CLEAR_OPTION, ...LANGUAGE_OPTIONS];

export function LanguagePicker({
  value,
  onChange,
  label,
  disabled = false,
  error = false,
}: {
  value?: string;
  onChange: (code: string) => void;
  label: string;
  disabled?: boolean;
  error?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [activeIndex, setActiveIndex] = useState(0);
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const itemRefs = useRef<Array<HTMLButtonElement | null>>([]);
  const currentLabel = languageLabel(value) ?? "Lingua non impostata";
  const selectedIndex = Math.max(0, PICKER_OPTIONS.findIndex(({ code }) => code === (value?.trim().toLowerCase() ?? "")));

  useEffect(() => {
    if (!open) return;
    setActiveIndex(selectedIndex);
    itemRefs.current[selectedIndex]?.focus();
    const closeOnOutsidePointer = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
        triggerRef.current?.focus();
      }
    };
    document.addEventListener("pointerdown", closeOnOutsidePointer);
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("pointerdown", closeOnOutsidePointer);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [open, selectedIndex]);

  const choose = (code: string) => {
    onChange(code);
    setOpen(false);
    triggerRef.current?.focus();
  };

  const moveActive = (nextIndex: number) => {
    const normalized = (nextIndex + PICKER_OPTIONS.length) % PICKER_OPTIONS.length;
    setActiveIndex(normalized);
    itemRefs.current[normalized]?.focus();
  };

  return (
    <div ref={rootRef} className="relative shrink-0">
      <button
        ref={triggerRef}
        type="button"
        disabled={disabled}
        aria-label={label}
        aria-haspopup="menu"
        aria-expanded={open}
        title={`${currentLabel} · clicca per cambiare`}
        onClick={() => setOpen((current) => !current)}
        className={`group/pill inline-flex h-7 items-center gap-2 rounded-full border bg-white/[0.04] px-2.5 transition-colors hover:border-white/25 hover:bg-white/[0.08] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-violet-300/70 disabled:cursor-progress disabled:opacity-50 ${error ? "border-red-300/70" : "border-white/[0.10]"}`}
      >
        <LanguageFlag code={value} className="h-5 w-[30px] rounded-[3px] drop-shadow-[0_1px_2px_rgba(0,0,0,0.4)]" />
        <span className={`text-[11px] font-medium ${value?.trim() ? "text-[#cdd2da] group-hover/pill:text-white" : "text-[#7f8591]"}`}>
          {value?.trim() ? currentLabel : "Nessuna"}
        </span>
        <ChevronDown size={11} className={`text-[#9aa0aa] transition-transform group-hover/pill:text-white ${open ? "rotate-180" : ""}`} aria-hidden="true" />
      </button>

      {open ? (
        <div
          role="menu"
          aria-label={`${label} options`}
          onKeyDown={(event) => {
            if (event.key === "ArrowDown") {
              event.preventDefault();
              moveActive(activeIndex + 1);
            } else if (event.key === "ArrowUp") {
              event.preventDefault();
              moveActive(activeIndex - 1);
            } else if (event.key === "Home") {
              event.preventDefault();
              moveActive(0);
            } else if (event.key === "End") {
              event.preventDefault();
              moveActive(PICKER_OPTIONS.length - 1);
            } else if (event.key === "Enter" || event.key === " ") {
              event.preventDefault();
              choose(PICKER_OPTIONS[activeIndex].code);
            }
          }}
          className="absolute right-0 top-[calc(100%+0.4rem)] z-30 max-h-72 min-w-[190px] overflow-auto rounded-xl border border-white/15 bg-[#171722]/95 p-1.5 shadow-[0_18px_50px_-18px_rgba(0,0,0,0.9)] backdrop-blur-xl"
        >
          {PICKER_OPTIONS.map(({ code, name }, index) => (
            <button
              key={code || "none"}
              ref={(element) => { itemRefs.current[index] = element; }}
              type="button"
              role="menuitem"
              tabIndex={index === activeIndex ? 0 : -1}
              onMouseEnter={() => setActiveIndex(index)}
              onClick={() => choose(code)}
              className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-[11px] text-zinc-300 transition-colors hover:bg-white/10 hover:text-white focus-visible:bg-white/10 focus-visible:outline-none"
            >
              <LanguageFlag code={code} className="h-5 w-[30px] rounded-[4px] drop-shadow-[0_1px_2px_rgba(0,0,0,0.4)]" />
              <span className="flex-1">{name}</span>
              {selectedIndex === index ? <Check size={13} className="text-violet-300" aria-hidden="true" /> : null}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}
