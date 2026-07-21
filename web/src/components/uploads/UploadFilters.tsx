import { useEffect } from "react";
import { ChevronDown, ExternalLink, Info } from "lucide-react";
import { cn } from "../../lib/utils";
import { FormField } from "./FormField";
import { FormSelect } from "./FormSelect";
import {
  DEFAULT_MAX_JITTER_SEC,
  DEFAULT_MIN_JITTER_SEC,
  MAX_JITTER_SEC,
  MIN_JITTER_SEC,
} from "../../types/uploads";
import type { FormValues, PlatformAccount, Workspace } from "../../types/uploads";
import { formatSeconds } from "../../lib/formatters";

export function UploadFilters({
  form,
  setForm,
  workspaces,
  pages,
  drives,
  folderValid,
  isSubmitting,
  firstFieldRef,
}: {
  form: FormValues;
  setForm: React.Dispatch<React.SetStateAction<FormValues>>;
  workspaces: Workspace[];
  pages: PlatformAccount[];
  drives: PlatformAccount[];
  folderValid: boolean | null;
  isSubmitting: boolean;
  firstFieldRef: React.RefObject<HTMLInputElement | null>;
}) {
  // Focus the folder ID input on mount so keyboard users land on a
  // labelled field without a tab trip.
  useEffect(() => {
    firstFieldRef.current?.focus();
  }, []);

  return (
    <div className="space-y-5">
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <FormSelect
          id="uploads-workspace"
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
          id="uploads-fb-account"
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
          id="uploads-folder"
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
            id="uploads-folder"
            type="text"
            placeholder="1HregS58okcSoe8597qdXgpZM6K4CwEBD"
            value={form.folderId}
            disabled={isSubmitting}
            onChange={(e) =>
              setForm((f) => ({ ...f, folderId: e.target.value }))
            }
            className={cn(
              "w-full px-3 py-2 bg-white/[0.04] border rounded-xl text-[14px] text-white placeholder:text-white/20 focus:outline-none focus:ring-1 focus:ring-white/10 transition-all font-mono",
              folderValid === false
                ? "border-red-500/40 focus:border-red-500/60"
                : "border-white/[0.08] focus:border-white/[0.20]",
            )}
            spellCheck={false}
            autoComplete="off"
          />
          {folderValid === true && (
            <a
              href={`https://drive.google.com/drive/folders/${form.folderId.trim()}`}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1 mt-1.5 text-[12px] text-[#9aa0aa] hover:text-white transition-colors no-underline"
            >
              Open in Google Drive <ExternalLink size={11} aria-hidden="true" />
            </a>
          )}
        </FormField>
      </div>

      {drives.length > 0 && (
        <div>
          <FormSelect
            id="uploads-drive-account"
            label="Drive account (optional)"
            value={form.driveAccountId}
            onChange={(v) =>
              setForm((f) => ({ ...f, driveAccountId: v as number | "" }))
            }
            placeholder="Public folder — server API key"
            disabled={isSubmitting}
            options={drives.map((d) => ({
              value: d.id,
              label: `Linked Drive · @${d.username}`,
            }))}
          />
          <p className="mt-1.5 text-[12px] text-[#9aa0aa]/80 flex items-start gap-1.5">
            <Info size={11} className="mt-0.5 shrink-0" aria-hidden="true" />
            <span>
              Pick a linked Drive if this folder is private to you. Leave
              blank to use the server&apos;s public-folder API key.
            </span>
          </p>
        </div>
      )}

      <button
        type="button"
        onClick={() => setForm((f) => ({ ...f, advanced: !f.advanced }))}
        className="inline-flex items-center gap-1.5 text-[12px] font-semibold text-[#9aa0aa] hover:text-white transition-colors"
        aria-expanded={form.advanced}
        aria-controls="uploads-advanced-panel"
        data-testid="uploads-advanced-toggle"
      >
        <ChevronDown
          size={14}
          className={cn("transition-transform", form.advanced && "rotate-180")}
        />
        {form.advanced
          ? "Hide advanced options"
          : `Show advanced options (jitter — currently ${formatSeconds(DEFAULT_MIN_JITTER_SEC)} → ${formatSeconds(DEFAULT_MAX_JITTER_SEC)})`}
      </button>

      {form.advanced && (
        <div
          id="uploads-advanced-panel"
          className="space-y-4 pl-1 border-l border-white/[0.08] ml-1"
        >
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <div>
              <FormField
                id="uploads-min-jitter"
                label="Minimum gap (seconds)"
                helpText="Random lower bound between posts. 60 s minimum — anything less trips platform anti-pattern detection."
              >
                <input
                  id="uploads-min-jitter"
                  type="number"
                  min={MIN_JITTER_SEC}
                  max={MAX_JITTER_SEC}
                  step={60}
                  value={form.minJitterSeconds}
                  disabled={isSubmitting}
                  onChange={(e) =>
                    setForm((f) => ({
                      ...f,
                      minJitterSeconds: Number(e.target.value),
                    }))
                  }
                  className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all"
                />
              </FormField>
            </div>
            <div>
              <FormField
                id="uploads-max-jitter"
                label="Maximum gap (seconds)"
                helpText={`Must be ≥ minimum. Cap is ${MAX_JITTER_SEC}s (7 days).`}
              >
                <input
                  id="uploads-max-jitter"
                  type="number"
                  min={MIN_JITTER_SEC}
                  max={MAX_JITTER_SEC}
                  step={60}
                  value={form.maxJitterSeconds}
                  disabled={isSubmitting}
                  onChange={(e) =>
                    setForm((f) => ({
                      ...f,
                      maxJitterSeconds: Number(e.target.value),
                    }))
                  }
                  className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all"
                />
              </FormField>
            </div>
          </div>

          <div>
            <FormField
              id="uploads-title"
              label="Internal title prefix (optional)"
              helpText="Prepended to each post's internal title so you can recognise the batch in /app/posts."
            >
              <input
                id="uploads-title"
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
              id="uploads-caption"
              label="Caption prefix (optional)"
              helpText="Prepended to each post's caption when published to Facebook."
            >
              <textarea
                id="uploads-caption"
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
    </div>
  );
}
