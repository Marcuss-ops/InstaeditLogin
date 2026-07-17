import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { useNavigate } from "react-router-dom";
import {
  AlertTriangle,
  ArrowRight,
  CheckCircle2,
  ChevronDown,
  Clock,
  ExternalLink,
  FolderInput,
  Loader2,
  Sparkles,
  X,
} from "lucide-react";
import { authedFetch, AuthError } from "../../lib/auth";
import { ErrorState, Skeleton } from "../../components/feedback";
import { cn } from "../../lib/utils";

type Workspace = { id: number; name: string };
type PlatformAccount = { id: number; platform: string; username: string };

type LoadState =
  | { kind: "loading" }
  | { kind: "ready"; workspaces: Workspace[]; pages: PlatformAccount[] }
  | { kind: "error"; message: string };

type BatchEntry = {
  index: number;
  drive_file_id: string;
  name: string;
  job_id: number;
  scheduled_at: string;
  relative_hours_from_now: number;
};

type BatchResponse = {
  folder_id: string;
  scheduled_count: number;
  first_publish_at: string;
  last_scheduled_at: string;
  entries: BatchEntry[];
  next_page_token: string;
  note: string;
  cursor_clamped_to_now: boolean;
  needs_google_drive_api_key: boolean;
  needs_drive_account: boolean;
  error: string;
};

type SuccessPayload = {
  folderId: string;
  scheduledCount: number;
  firstPublishAt: string;
  lastScheduledAt: string;
  entries: BatchEntry[];
  cursorClampedToNow: boolean;
};

type SubmitState =
  | { kind: "idle" }
  | { kind: "submitting" }
  | { kind: "success"; payload: SuccessPayload; nextPageToken: string }
  | { kind: "guidance"; note: string }
  | { kind: "error"; message: string };

type FormValues = {
  workspaceId: number | "";
  facebookAccountId: number | "";
  folderId: string;
  advanced: boolean;
  title: string;
  captionPrefix: string;
  minJitterMinutes: number;
  maxJitterMinutes: number;
};

// Folder IDs in Google Drive are URL-safe base64ish strings (the suffix
// of https://drive.google.com/drive/folders/<ID>). Examples:
//   1HregS58okcSoe8597qdXgpZM6K4CwEBD
//   1AbCdEfGhIjKlMnOpQrStUvWxYz-_123
// Server-side `^[A-Za-z0-9_-]{1,100}$` is the authoritative validator; we
// mirror it here so users see an inline error instead of a 422 round-trip.
const FOLDER_ID_PATTERN = /^[A-Za-z0-9_-]{1,100}$/;
// Jitter window: 30 minutes minimum to avoid clumping, 7 days max as a
// safety cap (anything longer risks the user forgetting the post exists).
const MIN_JITTER_MIN = 30;
const MAX_JITTER_MIN = 7 * 24 * 60;

const DEFAULT_MIN_JITTER_MIN = 180; // 3 hours
const DEFAULT_MAX_JITTER_MIN = 270; // 4.5 hours

export function DriveBatchImportDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const navigate = useNavigate();
  const cardRef = useRef<HTMLDivElement | null>(null);
  const firstFieldRef = useRef<HTMLInputElement | null>(null);

  const [loadState, setLoadState] = useState<LoadState>({ kind: "loading" });
  const [submitState, setSubmitState] = useState<SubmitState>({ kind: "idle" });

  const [form, setForm] = useState<FormValues>({
    workspaceId: "",
    facebookAccountId: "",
    folderId: "",
    advanced: false,
    title: "",
    captionPrefix: "",
    minJitterMinutes: DEFAULT_MIN_JITTER_MIN,
    maxJitterMinutes: DEFAULT_MAX_JITTER_MIN,
  });
  // Pagination cursors. They live as plain state so the success view can
  // promote them before re-submitting. Don't bind them to the form so
  // users don't see advanced fields they shouldn't touch by hand.
  const [pageToken, setPageToken] = useState("");
  const [cursorScheduledAt, setCursorScheduledAt] = useState("");

  const abortRef = useRef<AbortController | null>(null);

  // Re-fetch workspaces + Facebook pages every time the dialog opens so we
  // don't show stale targets (the user may have unlinked a page between
  // visits). Mirrors the pattern from Compose.tsx.
  useEffect(() => {
    if (!open) return;
    setLoadState({ kind: "loading" });
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    void (async () => {
      try {
        const [wsR, acctsR] = await Promise.all([
          authedFetch("/api/v1/workspaces", { signal: ctrl.signal }),
          authedFetch("/api/v1/accounts", { signal: ctrl.signal }),
        ]);
        if (ctrl.signal.aborted) return;
        const ws =
          ((await wsR.json()) as { workspaces: Workspace[] }).workspaces ??
          [];
        const accts =
          ((await acctsR.json()) as { accounts: PlatformAccount[] })
            .accounts ?? [];
        const pages = accts.filter((a) => a.platform === "facebook");
        setLoadState({ kind: "ready", workspaces: ws, pages });
        setForm((f) => ({
          ...f,
          // Pre-select when only one option exists; reset if it disappeared.
          workspaceId:
            f.workspaceId && ws.find((w) => w.id === f.workspaceId)
              ? f.workspaceId
              : ws.length === 1
                ? ws[0].id
                : "",
          facebookAccountId:
            f.facebookAccountId && pages.find((p) => p.id === f.facebookAccountId)
              ? f.facebookAccountId
              : pages.length === 1
                ? pages[0].id
                : "",
        }));
      } catch (err) {
        if (ctrl.signal.aborted) return;
        if (err instanceof AuthError) {
          onClose();
          return;
        }
        setLoadState({
          kind: "error",
          message:
            err instanceof Error
              ? err.message
              : "Unable to load workspaces or pages.",
        });
      }
    })();
    return () => ctrl.abort();
  }, [open, onClose]);

  // Reset submit state + cursors when the dialog re-opens so a "back" from
  // the success view doesn't carry pagination state into a fresh import.
  useEffect(() => {
    if (open) {
      setSubmitState({ kind: "idle" });
      setPageToken("");
      setCursorScheduledAt("");
    }
  }, [open]);

  // Close on Escape (matches CookieBanner.tsx + AccountSwitcher pattern).
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  // Focus-on-open is intentionally NOT done at the dialog-root level
  // because `firstFieldRef` is attached inside `ImportForm`, which renders
  // only AFTER `loadState` flips to `ready` (loadState=loading shows a
  // skeleton during the workspaces + accounts fetch). Running the focus
  // here would no-op on the first open with `firstFieldRef.current === null`.
  // See `ImportForm`'s mount effect for the actual focus trigger.

  // Block background scroll while the modal is open. Capture the previous
  // value so cleanup can restore it; with only one modal in the app today
  // there's no contention, but a single capture keeps the behaviour robust
  // if a second modal ever arrives.
  useEffect(() => {
    if (!open) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = prev;
    };
  }, [open]);

  const folderValid = useMemo(() => {
    if (!form.folderId.trim()) return null;
    return FOLDER_ID_PATTERN.test(form.folderId.trim());
  }, [form.folderId]);

  const jitterError = useMemo<string | null>(() => {
    if (!form.advanced) return null;
    if (form.minJitterMinutes < MIN_JITTER_MIN) return null;
    if (form.minJitterMinutes > MAX_JITTER_MIN) return null;
    if (form.maxJitterMinutes < form.minJitterMinutes) return null;
    return null;
  }, [form.advanced, form.minJitterMinutes, form.maxJitterMinutes]);

  const canSubmit =
    submitState.kind !== "submitting" &&
    form.workspaceId !== "" &&
    form.facebookAccountId !== "" &&
    folderValid === true &&
    jitterError === null;

  const handleSubmit = useCallback(
    async (e: FormEvent) => {
      e.preventDefault();
      if (!canSubmit) return;
      setSubmitState({ kind: "submitting" });
      const minutes = (n: number) => Math.round(n * 60);
      try {
        const response = await authedFetch(
          "/api/v1/media/import/drive/folder",
          {
            method: "POST",
            body: JSON.stringify({
              folder_id: form.folderId.trim(),
              workspace_id: form.workspaceId,
              facebook_account_id: form.facebookAccountId,
              title: form.title.trim() || undefined,
              caption_prefix: form.captionPrefix.trim() || undefined,
              min_jitter_seconds: form.advanced
                ? minutes(form.minJitterMinutes)
                : undefined,
              max_jitter_seconds: form.advanced
                ? minutes(form.maxJitterMinutes)
                : undefined,
              page_token: pageToken || undefined,
              cursor_scheduled_at: cursorScheduledAt || undefined,
            }),
          },
        );
        if (!response.ok) {
          // authedFetch already toasts but we also surface inline. Try to
          // pull a server-side message first; fall back to the status code.
          let message = `Request failed (status ${response.status})`;
          try {
            const data = (await response.json()) as Partial<BatchResponse>;
            if (data.error) message = data.error;
            else if (data.note) message = data.note;
          } catch {
            // Body wasn't JSON.
          }
          setSubmitState({ kind: "error", message });
          return;
        }
        const payload = (await response.json()) as BatchResponse;
        // Configuration guidance from the server (no API key on server,
        // public folder that needs OAuth). 200 + structural flags rather
        // than a hard error so the user can fix config without a refresh.
        if (payload.needs_google_drive_api_key || payload.needs_drive_account) {
          setSubmitState({
            kind: "guidance",
            note:
              payload.note ||
              "Server is missing configuration to list this public Drive folder.",
          });
          return;
        }
        setSubmitState({
          kind: "success",
          payload: {
            folderId: payload.folder_id,
            scheduledCount: payload.scheduled_count,
            firstPublishAt: payload.first_publish_at,
            lastScheduledAt: payload.last_scheduled_at,
            entries: payload.entries ?? [],
            cursorClampedToNow: payload.cursor_clamped_to_now,
          },
          nextPageToken: payload.next_page_token,
        });
        // Promote response cursors so the next-page submit can honour them.
        // Done outside React's render path so the success view shows the
        // just-imported batch with a clear "Continue" affordance.
        if (payload.next_page_token) {
          setPageToken(payload.next_page_token);
          setCursorScheduledAt(payload.last_scheduled_at);
        }
      } catch (err) {
        if (err instanceof AuthError) {
          onClose();
          return;
        }
        setSubmitState({
          kind: "error",
          message:
            err instanceof Error ? err.message : "Unable to start the import.",
        });
      }
    },
    [
      canSubmit,
      cursorScheduledAt,
      form.folderId,
      form.workspaceId,
      form.facebookAccountId,
      form.title,
      form.captionPrefix,
      form.advanced,
      form.minJitterMinutes,
      form.maxJitterMinutes,
      onClose,
      pageToken,
    ],
  );

  const handleContinuePagination = useCallback(() => {
    if (submitState.kind !== "success") return;
    setSubmitState({ kind: "idle" });
  }, [submitState]);

  const handleViewPosts = useCallback(() => {
    onClose();
    navigate("/app/posts");
  }, [navigate, onClose]);

  const handleBackToForm = useCallback(() => {
    setSubmitState({ kind: "idle" });
  }, []);

  if (!open) return null;

  const back = (
    <button
      type="button"
      onClick={onClose}
      className="px-3 py-2 text-[13px] font-semibold text-[#9aa0aa] hover:text-white transition-colors"
      data-testid="drive-batch-close"
    >
      Close
    </button>
  );

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="drive-batch-dialog-title"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
      className="fixed inset-0 z-50 bg-black/60 backdrop-blur-sm flex items-start sm:items-center justify-center p-4 overflow-y-auto"
    >
      <div
        ref={cardRef}
        className="w-full max-w-2xl bg-[#1f1f2e] border border-white/[0.12] rounded-2xl shadow-[0_8px_32px_rgba(0,0,0,0.4)] overflow-hidden my-4 sm:my-8"
        data-testid="drive-batch-dialog"
      >
        <header className="flex items-start justify-between gap-3 p-6 border-b border-white/[0.08]">
          <div className="flex items-center gap-3 min-w-0">
            <div className="w-11 h-11 rounded-xl bg-gradient-to-br from-emerald-500 via-blue-500 to-violet-500 flex items-center justify-center text-white shrink-0 shadow-[0_4px_16px_rgba(59,130,246,0.30)]">
              <FolderInput size={20} aria-hidden="true" />
            </div>
            <div className="min-w-0">
              <h2
                id="drive-batch-dialog-title"
                className="text-[18px] font-bold text-white leading-tight"
              >
                Auto-post my Drive folder
              </h2>
              <p className="text-[13px] text-[#9aa0aa] mt-0.5">
                Schedule every video in a Google Drive folder to a Facebook
                Page, with random gaps between posts.
              </p>
            </div>
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label="Close dialog"
            className="shrink-0 p-1.5 rounded-lg text-[#9aa0aa] hover:text-white hover:bg-white/[0.06] transition-colors"
          >
            <X size={18} aria-hidden="true" />
          </button>
        </header>

        {loadState.kind === "loading" && (
          <div className="p-6 space-y-4">
            <Skeleton variant="card" height={48} />
            <Skeleton variant="card" height={48} />
            <Skeleton variant="card" height={48} />
          </div>
        )}

        {loadState.kind === "error" && (
          <div className="p-6">
            <ErrorState
              title="Couldn't load dependencies"
              message={loadState.message}
              helpText="Sign in again or re-open this dialog to retry."
            />
          </div>
        )}

        {loadState.kind === "ready" && (
          <>
            {loadState.pages.length === 0 ? (
              <div className="p-6">
                <ErrorState
                  title="No Facebook Pages connected"
                  message="Link a Facebook Page first — this feature schedules to a Page you control."
                />
              </div>
            ) : submitState.kind === "success" ? (
              <SuccessView
                payload={submitState.payload}
                hasNextPage={!!submitState.nextPageToken}
                onContinue={handleContinuePagination}
                onViewPosts={handleViewPosts}
              />
            ) : submitState.kind === "guidance" ? (
              <GuidanceView note={submitState.note} onBack={handleBackToForm} />
            ) : submitState.kind === "error" ? (
              <ErrorView
                message={submitState.message}
                onBack={handleBackToForm}
              />
            ) : (
              <ImportForm
                form={form}
                setForm={setForm}
                workspaces={loadState.workspaces}
                pages={loadState.pages}
                folderValid={folderValid}
                jitterError={jitterError}
                isSubmitting={submitState.kind === "submitting"}
                firstFieldRef={firstFieldRef}
                onSubmit={handleSubmit}
              />
            )}
          </>
        )}

        <footer className="flex items-center justify-end gap-2 p-4 border-t border-white/[0.08] bg-[#16161e]/40">
          {back}
        </footer>
      </div>
    </div>
  );
}

function ImportForm({
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
  setForm: React.Dispatch<React.SetStateAction<FormValues>>;
  workspaces: Workspace[];
  pages: PlatformAccount[];
  folderValid: boolean | null;
  jitterError: string | null;
  isSubmitting: boolean;
  firstFieldRef: React.RefObject<HTMLInputElement | null>;
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

function FormField({
  id,
  label,
  helpText,
  error,
  children,
}: {
  id: string;
  label: string;
  helpText?: string;
  error?: string | null;
  children: React.ReactNode;
}) {
  return (
    <div>
      <label
        htmlFor={id}
        className="block text-[13px] font-semibold text-[#9aa0aa] mb-1.5"
      >
        {label}
      </label>
      {children}
      {helpText && !error && (
        <p className="mt-1.5 text-[12px] text-[#9aa0aa]/80">{helpText}</p>
      )}
      {error && (
        <p className="mt-1.5 text-[12px] text-red-400" role="status">
          {error}
        </p>
      )}
    </div>
  );
}

function FormSelect({
  id,
  label,
  value,
  onChange,
  placeholder,
  disabled,
  options,
}: {
  id: string;
  label: string;
  value: number | "";
  onChange: (v: number | "") => void;
  placeholder: string;
  disabled?: boolean;
  options: Array<{ value: number; label: string }>;
}) {
  return (
    <div>
      <label
        htmlFor={id}
        className="block text-[13px] font-semibold text-[#9aa0aa] mb-1.5"
      >
        {label}
      </label>
      <select
        id={id}
        value={value === "" ? "" : String(value)}
        disabled={disabled}
        onChange={(e) =>
          onChange(e.target.value === "" ? "" : Number(e.target.value))
        }
        className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all disabled:opacity-50"
      >
        <option value="" disabled className="bg-[#1f1f2e]">
          {placeholder}
        </option>
        {options.map((opt) => (
          <option key={opt.value} value={opt.value} className="bg-[#1f1f2e]">
            {opt.label}
          </option>
        ))}
      </select>
    </div>
  );
}

function SuccessView({
  payload,
  hasNextPage,
  onContinue,
  onViewPosts,
}: {
  payload: SuccessPayload;
  hasNextPage: boolean;
  onContinue: () => void;
  onViewPosts: () => void;
}) {
  const firstDate = payload.firstPublishAt
    ? new Date(payload.firstPublishAt)
    : null;
  const lastDate = payload.lastScheduledAt
    ? new Date(payload.lastScheduledAt)
    : null;
  const preview = payload.entries.slice(0, 3);

  return (
    <div className="p-6 space-y-5">
      <div className="flex items-start gap-3" data-testid="drive-batch-success">
        <div className="w-10 h-10 rounded-full bg-emerald-500/[0.12] border border-emerald-500/[0.30] flex items-center justify-center text-emerald-400 shrink-0">
          <CheckCircle2 size={20} aria-hidden="true" />
        </div>
        <div className="min-w-0">
          <p className="text-[15px] font-bold text-white">
            {payload.scheduledCount > 0
              ? `${payload.scheduledCount} video${payload.scheduledCount === 1 ? "" : "s"} scheduled`
              : "Folder imported (no publishable videos)"}
          </p>
          {payload.cursorClampedToNow && (
            <p className="text-[12px] text-amber-400 mt-1 inline-flex items-center gap-1">
              <AlertTriangle size={12} aria-hidden="true" />
              Cursor was too far in the past — restart anchor clamped to now.
            </p>
          )}
        </div>
      </div>

      {payload.scheduledCount > 0 && (
        <dl className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <ScheduleBlock
            label="First publish"
            icon={<Clock size={14} />}
            date={firstDate}
            empty="immediately"
          />
          <ScheduleBlock
            label="Last publish"
            icon={<Clock size={14} />}
            date={lastDate}
            empty="—"
          />
        </dl>
      )}

      {preview.length > 0 && (
        <div>
          <p className="text-[12px] font-semibold text-[#9aa0aa] uppercase tracking-wider mb-2">
            First scheduled
          </p>
          <ul className="space-y-1.5">
            {preview.map((e) => (
              <li
                key={e.job_id}
                className="flex items-center justify-between gap-3 p-2.5 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px]"
              >
                <span className="text-white truncate min-w-0">
                  <span className="text-[#9aa0aa] font-mono text-[11px] mr-2">
                    {formatRelHours(e.relative_hours_from_now)}
                  </span>
                  {e.name}
                </span>
                <time className="text-[11px] text-[#9aa0aa] tabular-nums whitespace-nowrap">
                  {new Date(e.scheduled_at).toLocaleString()}
                </time>
              </li>
            ))}
          </ul>
          {payload.entries.length > preview.length && (
            <p className="mt-2 text-[12px] text-[#9aa0aa]/80">
              +{payload.entries.length - preview.length} more in /app/posts.
            </p>
          )}
        </div>
      )}

      <div className="flex items-center justify-between gap-3 pt-2">
        {hasNextPage ? (
          <p className="text-[12px] text-[#9aa0aa] flex items-center gap-1">
            <AlertTriangle size={12} className="text-amber-400" aria-hidden="true" />
            More pages remain in this folder.
          </p>
        ) : (
          <span />
        )}
        <div className="flex items-center gap-2">
          {hasNextPage && (
            <button
              type="button"
              onClick={onContinue}
              data-testid="drive-batch-continue"
              className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-white/[0.06] border border-white/[0.10] text-[13px] font-semibold text-white hover:bg-white/[0.10] transition-colors"
            >
              Continue next page <ArrowRight size={14} aria-hidden="true" />
            </button>
          )}
          <button
            type="button"
            onClick={onViewPosts}
            data-testid="drive-batch-view-posts"
            className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-white text-black text-[13px] font-semibold hover:bg-white/90 transition-colors"
          >
            View posts <ExternalLink size={14} aria-hidden="true" />
          </button>
        </div>
      </div>
    </div>
  );
}

function ScheduleBlock({
  label,
  icon,
  date,
  empty,
}: {
  label: string;
  icon: React.ReactNode;
  date: Date | null;
  empty: string;
}) {
  return (
    <div className="p-3 rounded-xl bg-white/[0.04] border border-white/[0.08]">
      <dt className="text-[11px] font-semibold text-[#9aa0aa] uppercase tracking-wider flex items-center gap-1">
        {icon} {label}
      </dt>
      <dd className="mt-1 text-[14px] text-white font-medium">
        {date ? formatDateTime(date) : empty}
      </dd>
    </div>
  );
}

function formatDateTime(date: Date): string {
  if (Number.isNaN(date.getTime())) return "—";
  const diffMs = date.getTime() - Date.now();
  const absMinutes = Math.round(Math.abs(diffMs) / 60_000);
  // Decide the main unit string first, then attach a direction-aware
  // prefix. Promoting the <1 min case to a single "just now" prevents
  // the `in just now` / `just now ago` artifacts when the slug falls
  // below the rounding threshold in either direction.
  let rel: string;
  if (absMinutes < 1) rel = "just now";
  else if (absMinutes < 60) rel = `${absMinutes} min`;
  else if (absMinutes < 24 * 60) rel = `${Math.round(absMinutes / 60)} h`;
  else rel = `${Math.round(absMinutes / (60 * 24))} d`;
  const relText =
    absMinutes < 1
      ? "just now"
      : diffMs >= 0
        ? `in ${rel}`
        : `${rel} ago`;
  const absolute = date.toLocaleString();
  return `${relText} · ${absolute}`;
}

function formatRelHours(hours: number): string {
  const sign = hours < 0 ? "-" : "+";
  const abs = Math.abs(hours);
  if (abs < 1) {
    const minutes = Math.round(abs * 60);
    return `${sign}${minutes}m`;
  }
  if (abs < 24) {
    return `${sign}${abs.toFixed(1)}h`;
  }
  return `${sign}${(abs / 24).toFixed(1)}d`;
}

function GuidanceView({
  note,
  onBack,
}: {
  note: string;
  onBack: () => void;
}) {
  return (
    <div className="p-6 space-y-4" data-testid="drive-batch-guidance">
      <div className="flex items-start gap-3">
        <div className="w-10 h-10 rounded-full bg-amber-500/[0.12] border border-amber-500/[0.30] flex items-center justify-center text-amber-400 shrink-0">
          <AlertTriangle size={20} aria-hidden="true" />
        </div>
        <div>
          <p className="text-[15px] font-bold text-white">
            Server needs configuration
          </p>
          <p className="text-[13px] text-[#9aa0aa] mt-1 leading-relaxed">
            {note}
          </p>
        </div>
      </div>
      <div className="flex items-center justify-end gap-2 pt-2">
        <button
          type="button"
          onClick={onBack}
          data-testid="drive-batch-back"
          className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-white/[0.06] border border-white/[0.10] text-[13px] font-semibold text-white hover:bg-white/[0.10] transition-colors"
        >
          Back to form
        </button>
      </div>
    </div>
  );
}

function ErrorView({
  message,
  onBack,
}: {
  message: string;
  onBack: () => void;
}) {
  return (
    <div className="p-6 space-y-4" data-testid="drive-batch-error">
      <ErrorState title="Import failed" message={message} />
      <div className="flex items-center justify-end gap-2">
        <button
          type="button"
          onClick={onBack}
          className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-white/[0.06] border border-white/[0.10] text-[13px] font-semibold text-white hover:bg-white/[0.10] transition-colors"
        >
          Back to form
        </button>
      </div>
    </div>
  );
}
