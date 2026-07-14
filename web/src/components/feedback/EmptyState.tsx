import type { ReactNode } from "react";
import { cn } from "../../lib/utils";

/**
 * EmptyState \u2014 zero-data friendly card. Used on Posts (no posts yet),
 * Dashboard (no accounts), Compose (no eligible accounts in workspace),
 * and Settings tabs (no api-keys / workspaces / webhook endpoints).
 *
 * The `icon` is a ReactNode so callers pass any Lucide icon with
 * whatever className/size they want. `cta` is a ReactNode so callers
 * can pass one button, a pair of buttons, or a Link with text + icon.
 */
export type EmptyStateProps = {
  title: string;
  description?: string;
  icon?: ReactNode;
  cta?: ReactNode;
  className?: string;
};

export function EmptyState({
  title,
  description,
  icon,
  cta,
  className,
}: EmptyStateProps) {
  return (
    <div
      role="status"
      className={cn(
        "bg-white border border-dashed border-neutral-300 rounded-xl p-12 text-center",
        className,
      )}
      data-testid="empty-state"
    >
      {icon && (
        <div className="mx-auto mb-3 text-neutral-300" aria-hidden="true">
          {icon}
        </div>
      )}
      <h3 className="font-bold text-[16px] text-black mb-1">{title}</h3>
      {description && (
        <p className="text-[14px] text-neutral-500 mb-5">{description}</p>
      )}
      {cta}
    </div>
  );
}
