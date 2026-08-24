import { AlertCircle, Loader2 } from "lucide-react";
import type { PostTarget } from "../../features/publishing/api/types";

/**
 * AggregateBanner — the primary progress banner describing the
 * aggregate flow ("In coda → Pubblicazione su YouTube → Pubblicato").
 *
 * States:
 *   - empty targets  → "In coda…" (still polling the worker)
 *   - allPublished   → null (the SuccessCard takes over)
 *   - anyFailed      → red "Pubblicazione non riuscita"
 *   - otherwise      → blue "In coda → Pubblicazione su YouTube"
 */
export function AggregateBanner({
  targets,
  allPublished,
  anyFailed,
}: {
  targets: PostTarget[];
  allPublished: boolean;
  anyFailed: boolean;
}) {
  if (targets.length === 0) {
    return (
      <div
        className="mb-6 rounded-2xl border border-blue-500/30 bg-blue-500/[0.06] px-5 py-4 flex items-center gap-3"
        data-testid="aggregate-banner-polling"
      >
        <Loader2
          size={20}
          className="text-blue-200 animate-spin"
          aria-hidden="true"
        />
        <div className="flex-1 min-w-0">
          <div className="text-sm font-medium text-blue-100">
            In coda…
          </div>
          <p className="text-xs text-[#9aa0aa] mt-0.5">
            Stiamo recuperando lo stato dei target dal worker.
          </p>
        </div>
      </div>
    );
  }
  if (allPublished) {
    return null; // Success card takes over.
  }
  if (anyFailed) {
    return (
      <div
        className="mb-6 rounded-2xl border border-red-500/30 bg-red-500/[0.06] px-5 py-4 flex items-center gap-3"
        data-testid="aggregate-banner-failed"
      >
        <AlertCircle
          size={20}
          className="text-red-300 shrink-0"
          aria-hidden="true"
        />
        <div className="flex-1 min-w-0">
          <div className="text-sm font-medium text-red-100">
            Pubblicazione non riuscita
          </div>
          <p className="text-xs text-[#9aa0aa] mt-0.5">
            Una o più destinazioni non sono riuscite. Le destinazioni già
            pubblicate restano intatte: puoi riprovare solo quelle fallite.
          </p>
        </div>
      </div>
    );
  }
  return (
    <div
      className="mb-6 rounded-2xl border border-blue-500/30 bg-blue-500/[0.06] px-5 py-4 flex items-center gap-3"
      data-testid="aggregate-banner-publishing"
    >
      <Loader2
        size={20}
        className="text-blue-200 animate-spin"
        aria-hidden="true"
      />
      <div className="flex-1 min-w-0">
        <div className="text-sm font-medium text-blue-100">
          In coda → Pubblicazione in corso
        </div>
        <p className="text-xs text-[#9aa0aa] mt-0.5">
          Il worker sta processando le destinazioni. La pagina si
          aggiorna automaticamente.
        </p>
      </div>
    </div>
  );
}
