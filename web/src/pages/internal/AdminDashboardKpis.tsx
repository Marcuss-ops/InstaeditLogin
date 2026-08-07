import type { ElementType, ReactNode } from "react";
import { cn } from "../../lib/utils";

// Shared KPI card primitives and number/date formatters for the Admin Dashboard
// (extracted from AdminDashboard.tsx, pure file move).

export function formatNumber(value: number | string): string {
  const n = typeof value === "string" ? Number.parseFloat(value) : value;
  if (Number.isNaN(n)) return String(value);
  if (n >= 1_000_000_000) return `${(n / 1_000_000_000).toFixed(1)}B`;
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return `${n}`;
}

export function formatPercent(value: number): string {
  return `${(value * 100).toFixed(1)}%`;
}

export function formatDate(value: number): string {
  return new Date(value * 1000).toLocaleString();
}

export function Card({
  title,
  value,
  subtitle,
  icon: Icon,
  variant = "default",
}: {
  title: string;
  value: number | string;
  subtitle?: string;
  icon: ElementType;
  variant?: "default" | "success" | "warning" | "danger";
}) {
  const variantClasses = {
    default: "bg-[#1f1f2e] border-white/[0.12] text-white",
    success: "bg-emerald-500/[0.08] border-emerald-500/20 text-emerald-400",
    warning: "bg-amber-500/[0.08] border-amber-500/20 text-amber-400",
    danger: "bg-red-500/[0.08] border-red-500/20 text-red-400",
  };

  return (
    <div
      className={cn(
        "rounded-2xl p-5 border",
        variantClasses[variant],
      )}
    >
      <div className="flex items-start justify-between">
        <div>
          <p className="text-[13px] font-medium text-[#9aa0aa]">{title}</p>
          <p className="text-[28px] font-extrabold tracking-tight mt-1">
            {value}
          </p>
          {subtitle && (
            <p className="text-[12px] text-[#9aa0aa] mt-1">{subtitle}</p>
          )}
        </div>
        <div
          className={cn(
            "w-10 h-10 rounded-xl flex items-center justify-center",
            variant === "default"
              ? "bg-white/[0.04] border border-white/[0.08] text-[#9aa0aa]"
              : "bg-white/[0.08]",
          )}
        >
          <Icon size={20} />
        </div>
      </div>
    </div>
  );
}

export function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-6">
      <h2 className="text-[16px] font-bold text-white mb-4">{title}</h2>
      {children}
    </div>
  );
}
