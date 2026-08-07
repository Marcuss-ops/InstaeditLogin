/**
 * AssignmentsPanel — YouTube links for a Cover project.
 *
 * Lists thumbnail_assignments (export → YouTube video) in read-only
 * compact rows. An empty list keeps the "autonomous cover" framing.
 */
import { Link2 } from "lucide-react";
import type { ThumbnailProjectAssignment } from "../../types";

interface AssignmentsPanelProps {
  assignments: ThumbnailProjectAssignment[];
}

export function AssignmentsPanel({ assignments }: AssignmentsPanelProps) {
  return (
    <div className="rounded-2xl border border-white/[0.08] bg-[#1a1a28] p-4">
      <h2 className="flex items-center gap-2 text-[13px] font-bold text-white">
        <Link2 size={14} className="text-white/40" />
        Collegamenti YouTube
        <span className="text-[11px] font-medium text-[#9aa0aa]">{assignments.length}</span>
      </h2>
      {assignments.length === 0 ? (
        <p className="mt-3 text-[12px] text-[#9aa0aa]">
          Nessun collegamento — la copertina esiste in modo autonomo.
        </p>
      ) : (
        <ul className="mt-3 space-y-1.5">
          {assignments.map((assignment) => (
            <li
              key={assignment.id}
              className="rounded-lg border border-white/[0.06] bg-white/[0.02] px-2.5 py-2 text-[12px]"
            >
              <span className="font-medium text-white">{assignment.youtube_video_id}</span>
              <span className="ml-2 text-[#9aa0aa]">account #{assignment.platform_account_id}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
