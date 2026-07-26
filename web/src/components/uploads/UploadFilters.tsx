import { useEffect } from "react";
import { ChevronDown, ExternalLink, Info, Lock } from "lucide-react";
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
  youtubeChannels,
  drives,
  folderValid,
  isSubmitting,
  firstFieldRef,
}: {
  form: FormValues;
  setForm: React.Dispatch<React.SetStateAction<FormValues>>;
  workspaces: Workspace[];
  youtubeChannels: PlatformAccount[];
  drives: PlatformAccount[];
  folderValid: boolean | null;
  isSubmitting: boolean;
  firstFieldRef: React.RefObject<HTMLInputElement | null>;
}) {
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
          id="uploads-yt-channel"
          label="YouTube Channel"
          value={form.youtubeAccountId}
          onChange={(v) =>
            setForm((f) => ({ ...f, youtubeAccountId: v as number | "" }))
          }
          placeholder="Select a channel…"
          disabled={isSubmitting}
          options={youtubeChannels.map((c) => ({
            value: c.id,
            label: `@${c.username}`,
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

      <div>
        <FormSelect
          id="uploads-drive-account"
          label="Google Drive account"
          value={form.driveAccountId}
          onChange={(v) =>
            setForm((f) => ({ ...f, driveAccountId: v as number | "" }))
          }
          placeholder="Select a Drive account…"
          disabled={isSubmitting}
          options={drives.map((d) => ({
            value: d.id,
            label: `Linked Drive · @${d.username}`,
          }))}
        />
        {drives.length === 0 && (
          <p className="mt-1.5 text-[12px] text-amber-400/80 flex items-start gap-1.5">
            <Info size={11} className="mt-0.5 shrink-0" aria-hidden="true" />
            <span>
              No Google Drive accounts linked. Connect one in /app/linking to
              access private folders.
            </span>
          </p>
        )}
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <div>
          <FormField
            id="uploads-privacy"
            label="Default visibility"
            helpText="Videos are uploaded with this visibility. You can change per-video after import."
          >
            <div className="flex gap-2">
              {(["private", "unlisted", "public"] as const).map((level) => (
                <button
                  key={level}
                  type="button"
                  disabled={isSubmitting}
                  onClick={() =>
                    setForm((f) => ({ ...f, privacyLevel: level }))
                  }
                  className={cn(
                    "flex-1 px-3 py-2 rounded-xl text-[13px] font-semibold border transition-colors capitalize",
                    form.privacyLevel === level
                      ? "bg-white/[0.10] border-white/[0.20] text-white"
                      : "bg-white/[0.03] border-white/[0.08] text-[#9aa0aa] hover:text-white hover:bg-white/[0.06]",
                  )}
                >
                  <Lock size={12} className="inline mr-1.5 -mt-0.5" />
                  {level}
                </button>
              ))}
            </div>
          </FormField>
        </div>
        <div>
          <FormField
            id="uploads-start-at"
            label="Start publishing at"
            helpText="Leave empty to start immediately after import completes."
          >
            <input
              id="uploads-start-at"
              type="datetime-local"
              value={form.startAt}
              disabled={isSubmitting}
              onChange={(e) =>
                setForm((f) => ({ ...f, startAt: e.target.value }))
              }
              className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all"
            />
          </FormField>
        </div>
      </div>

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
              helpText="Prepended to each video's internal title so you can recognise the batch in /app/posts."
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
              label="Description prefix (optional)"
              helpText="Prepended to each video's description when published to YouTube."
            >
              <textarea
                id="uploads-caption"
                rows={2}
                placeholder="New video from my Drive folder — "
                disabled={isSubmitting}
                value={form.descriptionPrefix}
                onChange={(e) =>
                  setForm((f) => ({ ...f, descriptionPrefix: e.target.value }))
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
