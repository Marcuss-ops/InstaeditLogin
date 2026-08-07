import { Folder } from "lucide-react";
import { cn } from "../../lib/utils";
import { groupAccent } from "./groupAccent";

/**
 * GroupBadges renders small colored chips showing the group folder(s) a
 * YouTube channel already belongs to. Each chip is tinted with the
 * folder's stable accent color (see groupAccent), so the same group is
 * recognizable at a glance wherever its badge appears. Used in three
 * places on the Groups page:
 *   - the "Tutti i canali YouTube" tray cards (all groups),
 *   - the group detail panel member rows (other groups only),
 *   - the "Aggiungi canali" dialog rows (other groups only).
 *
 * `names` must already be filtered by the caller when the current group
 * should be excluded (filtering is done by name, matching the tray
 * design which also stores names only). `max` controls how many chips
 * render before the "+N" overflow marker. Returns null when empty.
 */
export function GroupBadges({
  names,
  label,
  max = 2,
  className,
}: {
  names: string[];
  label?: string;
  max?: number;
  className?: string;
}) {
  if (names.length === 0) return null;
  return (
    <div className={cn("flex flex-wrap items-center gap-1", className)}>
      {label ? <span className="text-[10px] text-[#9aa0aa]">{label}</span> : null}
      {names.slice(0, max).map((name) => {
        const accent = groupAccent(name);
        return (
          <span
            key={name}
            title={name}
            className="inline-flex max-w-[96px] items-center gap-1 rounded-[5px] px-1.5 py-[1px] text-[10px] font-semibold"
            style={{ backgroundColor: accent.bg, color: accent.text }}
          >
            <Folder size={9} className="shrink-0" />
            <span className="truncate">{name}</span>
          </span>
        );
      })}
      {names.length > max ? (
        <span className="text-[10px] font-semibold text-[#9aa0aa]">+{names.length - max}</span>
      ) : null}
    </div>
  );
}
