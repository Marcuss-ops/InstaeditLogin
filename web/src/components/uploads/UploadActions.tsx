import { ArrowRight, Loader2, Sparkles } from "lucide-react";
import { Link } from "react-router-dom";

type UploadActionsProps =
  | {
      mode: "form";
      canSubmit: boolean;
      isSubmitting: boolean;
    }
  | {
      mode: "result";
      onRunAnother: () => void;
      calendarHref?: string;
    }
  | {
      mode: "back";
      onBack: () => void;
    };

export function UploadActions(props: UploadActionsProps) {
  if (props.mode === "form") {
    const { canSubmit, isSubmitting } = props;
    return (
      <div className="flex items-center justify-between gap-3 pt-2 border-t border-white/[0.06]">
        <p className="text-[12px] text-[#9aa0aa]/80 flex items-center gap-1.5">
          <Sparkles size={11} aria-hidden="true" />1 round-trip · up to 50
          Drive pages (≈10 k clips) per import.
        </p>
        <button
          type="submit"
          disabled={!canSubmit}
          data-testid="uploads-submit"
          className="inline-flex items-center gap-2 px-5 py-2.5 rounded-xl bg-white text-black text-[14px] font-semibold hover:bg-white/90 transition-all disabled:opacity-50 disabled:cursor-not-allowed shadow-[0_4px_12px_rgba(255,255,255,0.18)]"
        >
          {isSubmitting ? (
            <Loader2 size={16} className="animate-spin" aria-hidden="true" />
          ) : (
            <Sparkles size={16} aria-hidden="true" />
          )}
          {isSubmitting ? "Scheduling…" : "Import folder"}
        </button>
      </div>
    );
  }

  if (props.mode === "result") {
    return (
      <div className="flex items-center justify-end gap-2 pt-2">
        {props.calendarHref && (
          <Link
            to={props.calendarHref}
            className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-white/[0.06] border border-white/[0.10] text-[13px] font-semibold text-white hover:bg-white/[0.10] transition-colors no-underline"
          >
            Open calendar <ArrowRight size={14} aria-hidden="true" />
          </Link>
        )}
        <button
          type="button"
          onClick={props.onRunAnother}
          data-testid="uploads-run-another"
          className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-white text-black text-[13px] font-semibold hover:bg-white/90 transition-colors"
        >
          Import another folder
        </button>
      </div>
    );
  }

  return (
    <div className="flex items-center justify-end gap-2">
      <button
        type="button"
        onClick={props.onBack}
        className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-white text-black text-[13px] font-semibold hover:bg-white/90 transition-colors"
      >
        Back to form
      </button>
    </div>
  );
}
