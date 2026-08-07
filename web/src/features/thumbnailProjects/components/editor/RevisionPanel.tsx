/**
 * RevisionPanel — immutable revision history for a Cover project.
 *
 * Compact read-only list (from the old read-only detail view): each row
 * shows the revision number, renderer version, and the snapshot hash
 * prefix; the row matching the current revision is highlighted.
 */
import { Layers } from "lucide-react";
import { cn } from "../../../../lib/utils";
import type { ThumbnailProjectRevision } from "../../types";

interface RevisionPanelProps {
  revisions: ThumbnailProjectRevision[];
  currentRevisionId: string | null | undefined;
}

export function RevisionPanel({ revisions, currentRevisionId }: RevisionPanelProps) {
  return (
    <div className="rounded-2xl border border-white/[0.08] bg-[#1a1a28] p-4">
      <h2 className="flex items-center gap-2 text-[13px] font-bold text-white">
        <Layers size={14} className="text-white/40" />
        Revisioni immutabili
        <span className="text-[11px] font-medium text-[#9aa0aa]">{revisions.length}</span>
      </h2>
      {revisions.length === 0 ? (
        <p className="mt-3 text-[12px] text-[#9aa0aa]">Nessuna revisione ancora.</p>
      ) : (
        <ul className="mt-3 space-y-1.5" data-testid="revisions-list">
          {revisions.slice(0, 8).map((revision) => (
            <li
              key={revision.id}
              className={cn(
                "flex items-center justify-between gap-2 rounded-lg border px-2.5 py-2 text-[12px]",
                revision.id === currentRevisionId
                  ? "border-emerald-400/20 bg-emerald-500/[0.06]"
                  : "border-white/[0.06] bg-white/[0.02]",
              )}
            >
              <span className="font-semibold text-white">#{revision.revision_number}</span>
              <span className="truncate text-[#9aa0aa]">
                {revision.renderer_version}
                {revision.id === currentRevisionId ? " · corrente" : ""}
              </span>
              <span className="shrink-0 text-[#9aa0aa]" title={revision.snapshot_sha256}>
                {revision.snapshot_sha256.slice(0, 8)}…
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
