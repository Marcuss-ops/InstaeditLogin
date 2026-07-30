import type React from "react";

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
  children: React.ReactNode;
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
