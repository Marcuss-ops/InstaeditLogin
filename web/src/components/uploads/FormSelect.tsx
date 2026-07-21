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
        {options.map((opt) => (
          <option key={opt.value} value={opt.value} className="bg-[#1f1f2e]">
            {opt.label}
          </option>
        ))}
      </select>
    </div>
  );
}
