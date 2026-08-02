import { useEffect, type Dispatch, type FormEvent, type RefObject, type SetStateAction } from "react";
import {
  ChevronDown,
  Loader2,
  Sparkles,
} from "lucide-react";
import { cn } from "../../lib/utils";
import {
  MAX_JITTER_MIN,
  MIN_JITTER_MIN,
  type FormValues,
  type PlatformAccount,
  type Workspace,
} from "./driveBatchImportTypes";
import { FormField, FormSelect } from "./driveBatchImportPrimitives";

export function ImportForm({
  form,
  setForm,
  workspaces,
  pages,
  folderValid,
  jitterError,
  isSubmitting,
  firstFieldRef,
  onSubmit,
}: {
  form: FormValues;
  setForm: Dispatch<SetStateAction<FormValues>>;
  workspaces: Workspace[];
  pages: PlatformAccount[];
  folderValid: boolean | null;
  jitterError: string | null;
  isSubmitting: boolean;
  firstFieldRef: RefObject<HTMLInputElement | null>;
  onSubmit: (e: FormEvent) => void;
}) {
  const canSubmit =
    form.workspaceId !== "" &&
    form.facebookAccountId !== "" &&
    folderValid === true &&
    jitterError === null &&
    !isSubmitting;

  // Focus the folder ID input on mount so keyboard users land on a labeled
  // field the moment the form becomes visible (workspaces + pages fetch
  // already resolved). Runs once per ImportForm mount.
  useEffect(() => {
    firstFieldRef.current?.focus();
  }, []);

  return (
    <form onSubmit={onSubmit} className="p-6 space-y-5">
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <FormSelect
          id="drive-batch-workspace"
          label="Workspace"
          value={form.workspaceId}
          onChange={(v) =>
            setForm((f) => ({ ...f, workspaceId: v as number | "" }))
          }
          placeholder="Select a workspace…"
          disabled={isSubmitting}
          options={workspaces.map((w) => ({ value: w.id, label: w.name }))}
        />
        <FormSelect
          id="drive-batch-page"
          label="Facebook Page"
          value={form.facebookAccountId}
          onChange={(v) =>
            setForm((f) => ({ ...f, facebookAccountId: v as number | "" }))
          }
          placeholder="Select a Page…"
          disabled={isSubmitting}
          options={pages.map((p) => ({
            value: p.id,
            label: `@${p.username}`,
          }))}
        />
      </div>

      <div>
        <FormField
          id="drive-batch-folder"
          label="Google Drive Folder ID or link"
          helpText="Paste the part after /folders/ in any Google Drive URL, e.g. 1HregS58okcSoe8597qdXgpZM6K4CwEBD."
          error={
            folderValid === false
              ? "Folder ID must be 1–100 letters, digits, hyphens, or underscores."
              : null
          }
        >
          <input
            ref={firstFieldRef}
            id="drive-batch-folder"
            type="text"
            placeholder="1HregS58okcSoe8597qdXgpZM6K4CwEBD"
            value={form.folderId}
            disabled={isSubmitting}
            onChange={(e) =>
              setForm((f) => ({ ...f, folderId: e.target.value }))
            }
            className={cn(
              "w-full px-3 py-2 bg-white/[0.04] border rounded-xl text-[14px] text-white placeholder:text-white/20 focus:outline-none focus:ring-1 focus:ring-white/10 transition-all",
              folderValid === false
                ? "border-red-500/40 focus:border-red-500/60"
                : "border-white/[0.08] focus:border-white/[0.20]",
            )}
            spellCheck={false}
            autoComplete="off"
          />
        </FormField>
      </div>

      <button
        type="button"
        onClick={() => setForm((f) => ({ ...f, advanced: !f.advanced }))}
        className="inline-flex items-center gap-1.5 text-[12px] font-semibold text-[#9aa0aa] hover:text-white transition-colors"
        aria-expanded={form.advanced}
        aria-controls="drive-batch-advanced-panel"
        data-testid="drive-batch-advanced-toggle"
      >
        <ChevronDown
          size={14}
          className={cn(
            "transition-transform",
            form.advanced && "rotate-180",
          )}
        />
        {form.advanced ? "Hide advanced options" : "Show advanced options"}
      </button>

      {form.advanced && (
        <div
          id="drive-batch-advanced-panel"
          className="space-y-4 pl-1 border-l border-white/[0.08] ml-1"
        >
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <div>
              <FormField
                id="drive-batch-min-jitter"
                label="Minimum gap (minutes)"
                helpText="Random lower bound between posts."
              >
                <input
                  id="drive-batch-min-jitter"
                  type="number"
                  min={MIN_JITTER_MIN}
                  max={MAX_JITTER_MIN}
                  step={15}
                  value={form.minJitterMinutes}
                  disabled={isSubmitting}
                  onChange={(e) =>
                    setForm((f) => ({
                      ...f,
                      minJitterMinutes: Number(e.target.value),
                    }))
                  }
                  className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all"
                />
              </FormField>
            </div>
            <div>
              <FormField
                id="drive-batch-max-jitter"
                label="Maximum gap (minutes)"
                helpText="Must be ≥ minimum. 270 = 4.5 hours."
                error={jitterError}
              >
                <input
                  id="drive-batch-max-jitter"
                  type="number"
                  min={MIN_JITTER_MIN}
                  max={MAX_JITTER_MIN}
                  step={15}
                  value={form.maxJitterMinutes}
                  disabled={isSubmitting}
                  onChange={(e) =>
                    setForm((f) => ({
                      ...f,
                      maxJitterMinutes: Number(e.target.value),
                    }))
                  }
                  className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all"
                />
              </FormField>
            </div>
          </div>

          <div>
            <FormField
              id="drive-batch-title"
              label="Internal title prefix (optional)"
              helpText="Prepended to each post's internal title so you can recognise the batch in /app/posts."
            >
              <input
                id="drive-batch-title"
                type="text"
                placeholder="Vacation videos"
                disabled={isSubmitting}
                value={form.title}
                onChange={(e) =>
                  setForm((f) => ({ ...f, title: e.target.value }))
                }
                className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white placeholder:text-white/20 focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all"
              />
            </FormField>
          </div>

          <div>
            <FormField
              id="drive-batch-caption"
              label="Caption prefix (optional)"
              helpText="Prepended to each post's caption when published to Facebook."
            >
              <textarea
                id="drive-batch-caption"
                rows={2}
                placeholder="New video from my Drive folder — "
                disabled={isSubmitting}
                value={form.captionPrefix}
                onChange={(e) =>
                  setForm((f) => ({ ...f, captionPrefix: e.target.value }))
                }
                className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white placeholder:text-white/20 focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all resize-y"
              />
            </FormField>
          </div>
        </div>
      )}

      <div className="flex items-center justify-end gap-3 pt-2">
        <button
          type="submit"
          disabled={!canSubmit}
          data-testid="drive-batch-submit"
          className="inline-flex items-center gap-2 px-5 py-2.5 rounded-xl bg-white text-black text-[14px] font-semibold hover:bg-white/90 transition-all disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {isSubmitting ? (
            <Loader2 size={16} className="animate-spin" aria-hidden="true" />
          ) : (
            <Sparkles size={16} aria-hidden="true" />
          )}
          {isSubmitting ? "Scheduling…" : "Schedule the folder"}
        </button>
      </div>
    </form>
  );
}
