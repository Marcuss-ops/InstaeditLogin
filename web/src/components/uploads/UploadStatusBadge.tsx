import {
  CheckCircle2,
  AlertTriangle,
  Info,
  XCircle,
  Loader2,
  Clock,
} from "lucide-react";
import type { SubmitState } from "../../types/uploads";

export function UploadStatusBadge({ state }: { state: SubmitState }) {
  switch (state.kind) {
    case "idle":
      return (
        <span className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-white/[0.06] border border-white/[0.08] text-[12px] text-[#9aa0aa]">
          <Clock size={12} /> Ready
        </span>
      );
    case "submitting":
      return (
        <span className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-blue-500/[0.12] border border-blue-500/[0.30] text-[12px] text-blue-300">
          <Loader2 size={12} className="animate-spin" /> Scheduling…
        </span>
      );
    case "success":
      return (
        <span className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-emerald-500/[0.12] border border-emerald-500/[0.30] text-[12px] text-emerald-300">
          <CheckCircle2 size={12} /> {state.payload.scheduledCount} scheduled
        </span>
      );
    case "partial":
      return (
        <span className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-amber-500/[0.12] border border-amber-500/[0.30] text-[12px] text-amber-300">
          <AlertTriangle size={12} /> Partial ({state.payload.scheduledCount})
        </span>
      );
    case "guidance":
      return (
        <span className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-amber-500/[0.12] border border-amber-500/[0.30] text-[12px] text-amber-300">
          <Info size={12} /> Guidance
        </span>
      );
    case "error":
      return (
        <span className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-red-500/[0.12] border border-red-500/[0.30] text-[12px] text-red-300">
          <XCircle size={12} /> Error
        </span>
      );
    default:
      return null;
  }
}
