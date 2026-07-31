import type { ReactNode } from "react";

export function FormField({
  id,
  label,
  helpText,
  error,
  children,
}: {
  id: string;
  label: string;
  helpText?: string;
  error?: string | null;
  children: ReactNode;
}) {
  return (
    <div>
      <label
        htmlFor={id}
        className="block text-[13px] font-semibold text-[#9aa0aa] mb-1.5"
      >
        {label}
      </label>
      {children}
      {helpText && !error && (
        <p className="mt-1.5 text-[12px] text-[#9aa0aa]/80">{helpText}</p>
      )}
      {error && (
        <p className="mt-1.5 text-[12px] text-red-400" role="status">
          {error}
        </p>
      )}
    </div>
  );
}

export function FormSelect({
  id,
  label,
  value,
  onChange,
  placeholder,
  disabled,
  options,
}: {
  id: string;
  label: string;
  value: number | "";
  onChange: (v: number | "") => void;
  placeholder: string;
  disabled?: boolean;
  options: Array<{ value: number; label: string }>;
}) {
  return (
    <div>
      <label
        htmlFor={id}
        className="block text-[13px] font-semibold text-[#9aa0aa] mb-1.5"
      >
        {label}
      </label>
      <select
        id={id}
        value={value === "" ? "" : String(value)}
        disabled={disabled}
        onChange={(e) =>
          onChange(e.target.value === "" ? "" : Number(e.target.value))
        }
        className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all disabled:opacity-50"
      >
        <option value="" disabled className="bg-[#1f1f2e]">
          {placeholder}
        </option>
        {options.map((o) => (
          <option key={o.value} value={o.value} className="bg-[#1f1f2e]">
            {o.label}
          </option>
        ))}
      </select>
    </div>
  );
}
