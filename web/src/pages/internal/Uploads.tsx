import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { Link, useNavigate } from "react-router-dom";
import {
  AlertTriangle,
  ArrowRight,
  CheckCircle2,
  ChevronDown,
  Clock,
  ExternalLink,
  FolderInput,
  Info,
  Loader2,
  Sparkles,
  Video,
} from "lucide-react";
import { AuthError, authedFetch } from "../../lib/auth";
import { EmptyState, ErrorState, Skeleton } from "../../components/feedback";
import { useToast } from "../../components/toast";
import { cn } from "../../lib/utils";

type Workspace = { id: number; name: string };
type PlatformAccount = {
  id: number;
  platform: string;
  username: string;
  created_at: string;
};

type LoadState =
  | { kind: "loading" }
  | {
      kind: "ready";
      workspaces: Workspace[];
      pages: PlatformAccount[];
      drives: PlatformAccount[];
    }
  | { kind: "error"; message: string };

// Mirrors pkg/api/uploads_batch.go → UploadsBatchByFolderResponse.
// Only the fields the SPA renders are typed here; the Go side may add
// more without breaking this page (TS structural typing).
type BatchResponse = {
  folder_id: string;
  scheduled_count: number;
  page_count: number;
  total_runtime_estimate_seconds?: number;
  first_publish_at: string;
  last_scheduled_at: string;
  entries?: Array<{
    index: number;
    drive_file_id: string;
    name: string;
    job_id: number;
    scheduled_at: string;
    relative_hours_from_now: number;
  }>;
  partial_failure?: boolean;
  failed_at_page?: number;
  failed_at_page_token?: string;
  note?: string;
  needs_google_drive_api_key?: boolean;
  needs_drive_account?: boolean;
  error?: string;
};

type SuccessPayload = {
  folderId: string;
  scheduledCount: number;
  pageCount: number;
  firstPublishAt: string;
  lastScheduledAt: string;
  entries: NonNullable<BatchResponse["entries"]>;
};

type PartialPayload = {
  folderId: string;
  scheduledCount: number;
  pageCount: number;
  entries: NonNullable<BatchResponse["entries"]>;
  failedAtPage: number;
  failedAtPageToken: string;
  note: string;
  firstPublishAt: string;
  lastScheduledAt: string;
};

type SubmitState =
  | { kind: "idle" }
  | { kind: "submitting" }
  | { kind: "success"; payload: SuccessPayload }
  | { kind: "partial"; payload: PartialPayload }
  | { kind: "guidance"; note: string }
  | { kind: "error"; message: string };

type FormValues = {
  workspaceId: number | "";
  facebookAccountId: number | "";
  driveAccountId: number | "";
  folderId: string;
  advanced: boolean;
  title: string;
  captionPrefix: string;
  minJitterSeconds: number;
  maxJitterSeconds: number;
};

// Folder IDs in Google Drive are URL-safe base64ish — the suffix of
// https://drive.google.com/drive/folders/<ID>. Server enforces
// `^[A-Za-z0-9_-]{1,100}$`; mirror it here for inline feedback.
const FOLDER_ID_PATTERN = /^[A-Za-z0-9_-]{1,100}$/;
// Jitter: 60 s floor (server also enforces) — anything below collapses
// into "publish back-to-back" anti-pattern detection.
const MIN_JITTER_SEC = 60;
const MAX_JITTER_SEC = 7 * 24 * 60 * 60;
// Defaults mirror the CLI: 4 h ± 30 min (centre-anchored). Operators
// wanting different cadences flip the advanced toggle.
const DEFAULT_MIN_JITTER_SEC = 4 * 60 * 60 - 30 * 60;
const DEFAULT_MAX_JITTER_SEC = 4 * 60 * 60 + 30 * 60;

export function InternalUploads() {
  const navigate = useNavigate();
  const toast = useToast();
  const firstFieldRef = useRef<HTMLInputElement | null>(null);

  const [loadState, setLoadState] = useState<LoadState>({ kind: "loading" });
  const [submitState, setSubmitState] = useState<SubmitState>({ kind: "idle" });
  const [form, setForm] = useState<FormValues>({
    workspaceId: "",
    facebookAccountId: "",
    driveAccountId: "",
    folderId: "",
    advanced: false,
    title: "",
    captionPrefix: "",
    minJitterSeconds: DEFAULT_MIN_JITTER_SEC,
    maxJitterSeconds: DEFAULT_MAX_JITTER_SEC,
  });
  const abortRef = useRef<AbortController | null>(null);

  // Fetch dependencies (workspaces + facebook pages + drive accounts)
  // every time the page mounts. Mirrors the pattern from Compose.tsx so
  // the dropdowns are never stale (a freshly disconnected drive account
  // disappears from the list on next mount).
  useEffect(() => {
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
        const drives = accts.filter((a) => a.platform === "google-drive");
        setLoadState({ kind: "ready", workspaces: ws, pages, drives });
        setForm((f) => ({
          ...f,
          workspaceId:
            f.workspaceId && ws.find((w) => w.id === f.workspaceId)
              ? f.workspaceId
              : ws.length === 1
                ? ws[0].id
                : "",
          facebookAccountId:
            f.facebookAccountId &&
            pages.find((p) => p.id === f.facebookAccountId)
              ? f.facebookAccountId
              : pages.length === 1
                ? pages[0].id
                : "",
        }));
      } catch (err) {
        if (ctrl.signal.aborted) return;
        if (err instanceof AuthError) {
          navigate("/login", { replace: true });
          return;
        }
        setLoadState({
          kind: "error",
          message:
            err instanceof Error
              ? err.message
              : "Unable to load workspaces or connected accounts.",
        });
      }
    })();
    return () => ctrl.abort();
  }, [navigate]);

  const folderValid = useMemo(() => {
    if (!form.folderId.trim()) return null;
    return FOLDER_ID_PATTERN.test(form.folderId.trim());
  }, [form.folderId]);

  const jitterError = useMemo<string | null>(() => {
    if (!form.advanced) return null;
    if (form.minJitterSeconds < 1) {
      return "Minimum gap must be at least 1 second.";
    }
    if (form.minJitterSeconds < MIN_JITTER_SEC) {
      return `Minimum gap must be ≥ ${MIN_JITTER_SEC}s to avoid back-to-back anti-pattern detection.`;
    }
    if (form.maxJitterSeconds > MAX_JITTER_SEC) {
      return `Maximum gap cannot exceed ${MAX_JITTER_SEC}s (7 days).`;
    }
    if (form.maxJitterSeconds < form.minJitterSeconds) {
      return "Maximum gap must be greater than or equal to the minimum.";
    }
    return null;
  }, [form.advanced, form.minJitterSeconds, form.maxJitterSeconds]);

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
      try {
        const body: Record<string, unknown> = {
          folder_id: form.folderId.trim(),
          workspace_id: form.workspaceId,
          facebook_account_id: form.facebookAccountId,
        };
        if (form.driveAccountId !== "") {
          body.drive_account_id = form.driveAccountId;
        }
        if (form.title.trim()) body.title = form.title.trim();
        if (form.captionPrefix.trim()) body.caption_prefix = form.captionPrefix.trim();
        if (form.advanced) {
          body.min_jitter_seconds = form.minJitterSeconds;
          body.max_jitter_seconds = form.maxJitterSeconds;
        }
        const response = await authedFetch("/api/v1/uploads/batch/by-folder", {
          method: "POST",
          body: JSON.stringify(body),
        });
        if (!response.ok) {
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
        if (payload.needs_google_drive_api_key || payload.needs_drive_account) {
          setSubmitState({
            kind: "guidance",
            note:
              payload.note ||
              "Server is missing configuration to list this public Drive folder.",
          });
          return;
        }
        if (payload.partial_failure) {
          setSubmitState({
            kind: "partial",
            payload: {
              folderId: payload.folder_id,
              scheduledCount: payload.scheduled_count,
              pageCount: payload.page_count,
              entries: payload.entries ?? [],
              failedAtPage: payload.failed_at_page ?? -1,
              failedAtPageToken: payload.failed_at_page_token ?? "",
              note: payload.note ?? "",
              firstPublishAt: payload.first_publish_at,
              lastScheduledAt: payload.last_scheduled_at,
            },
          });
          toast.warning("Import partially completed — see resume instructions below.");
          return;
        }
        setSubmitState({
          kind: "success",
          payload: {
            folderId: payload.folder_id,
            scheduledCount: payload.scheduled_count,
            pageCount: payload.page_count,
            firstPublishAt: payload.first_publish_at,
            lastScheduledAt: payload.last_scheduled_at,
            entries: payload.entries ?? [],
          },
        });
        toast.success(
          `Scheduled ${payload.scheduled_count} video${payload.scheduled_count === 1 ? "" : "s"} from ${payload.folder_id.slice(0, 6)}…`,
        );
      } catch (err) {
        if (err instanceof AuthError) {
          navigate("/login", { replace: true });
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
      form.advanced,
      form.captionPrefix,
      form.driveAccountId,
      form.facebookAccountId,
      form.folderId,
      form.maxJitterSeconds,
      form.minJitterSeconds,
      form.title,
      form.workspaceId,
      navigate,
      toast,
    ],
  );

  const handleRunAnother = useCallback(() => {
    setSubmitState({ kind: "idle" });
    setForm((f) => ({
      ...f,
      folderId: "",
      title: "",
      captionPrefix: "",
    }));
  }, []);

  return (
    <div className="min-h-full p-8 bg-[#030308] text-[#e8e8ef]">
      <div className="max-w-3xl mx-auto">
        <header className="mb-8">
          <p className="text-[12px] font-semibold uppercase tracking-[0.16em] text-[#9aa0aa] mb-2">
            / app / uploads
          </p>
          <div className="flex items-center justify-between gap-4">
            <div>
              <h1 className="text-[28px] font-extrabold tracking-[-0.02em] text-white flex items-center gap-3">
                <span className="inline-flex w-10 h-10 rounded-xl bg-gradient-to-br from-emerald-500 via-blue-500 to-violet-500 items-center justify-center text-white shadow-[0_4px_16px_rgba(59,130,246,0.30)]">
                  <FolderInput size={20} aria-hidden="true" />
                </span>
                Import a Drive folder
              </h1>
              <p className="text-[15px] text-[#9aa0aa] mt-2 max-w-xl">
                Schedule every video in a Google Drive folder to a Facebook
                Page, with random gaps between posts. One round-trip, even for
                folders with thousands of clips.
              </p>
            </div>
            <Link
              to="/app/uploads/calendar"
              className="hidden sm:inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors no-underline"
            >
              View calendar <ArrowRight size={14} />
            </Link>
          </div>
        </header>

        {loadState.kind === "loading" && (
          <div className="space-y-4" data-testid="uploads-loading">
            <Skeleton variant="card" height={56} />
            <Skeleton variant="card" height={56} />
            <Skeleton variant="card" height={56} />
            <Skeleton variant="card" height={56} />
            <Skeleton variant="card" height={120} />
          </div>
        )}

        {loadState.kind === "error" && (
          <ErrorState
            title="Couldn't load dependencies"
            message={loadState.message}
            helpText="Sign in again or reload the page to retry."
            onRetry={() => window.location.reload()}
            className="bg-[#1f1f2e] border-white/[0.12]"
          />
        )}

        {loadState.kind === "ready" && (
          <>
            {loadState.workspaces.length === 0 && (
              <EmptyState
                title="Create a workspace first"
                description="Workspaces group your scheduled posts. Once you create one, come back here to start importing."
                icon={<FolderInput size={32} />}
                cta={
                  <Link
                    to="/app/linking"
                    className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white text-black text-[13px] font-semibold hover:bg-white/90 transition-colors no-underline"
                  >
                    Manage workspaces
                    <ArrowRight size={14} />
                  </Link>
                }
                className="bg-[#1f1f2e] border-white/[0.12]"
              />
            )}

            {loadState.workspaces.length > 0 && loadState.pages.length === 0 && (
              <EmptyState
                title="No Facebook Pages connected"
                description="Connect a Facebook Page in /app/linking — this importer schedules to Pages you own. (Personal profiles are not eligible.)"
                icon={<Video size={32} />}
                cta={
                  <Link
                    to="/app/linking"
                    className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white text-black text-[13px] font-semibold hover:bg-white/90 transition-colors no-underline"
                  >
                    Connect a Page
                    <ArrowRight size={14} />
                  </Link>
                }
                className="bg-[#1f1f2e] border-white/[0.12]"
              />
            )}

            {loadState.workspaces.length > 0 &&
              loadState.pages.length > 0 &&
              (submitState.kind === "success" ? (
                <SuccessView
                  payload={submitState.payload}
                  onRunAnother={handleRunAnother}
                />
              ) : submitState.kind === "partial" ? (
                <PartialView
                  payload={submitState.payload}
                  onRunAnother={handleRunAnother}
                  folderUrl={`https://drive.google.com/drive/folders/${form.folderId.trim()}`}
                />
              ) : submitState.kind === "guidance" ? (
                <GuidanceView
                  note={submitState.note}
                  onBack={handleRunAnother}
                />
              ) : submitState.kind === "error" ? (
                <ErrorView
                  message={submitState.message}
                  onBack={() => setSubmitState({ kind: "idle" })}
                />
              ) : (
                <ImportForm
                  form={form}
                  setForm={setForm}
                  workspaces={loadState.workspaces}
                  pages={loadState.pages}
                  drives={loadState.drives}
                  folderValid={folderValid}
                  jitterError={jitterError}
                  isSubmitting={submitState.kind === "submitting"}
                  firstFieldRef={firstFieldRef}
                  onSubmit={handleSubmit}
                />
              ))}
          </>
        )}
      </div>
    </div>
  );
}

function ImportForm({
  form,
  setForm,
  workspaces,
  pages,
  drives,
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
  drives: PlatformAccount[];
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

  // Focus the folder ID input on mount so keyboard users land on a
  // labelled field without a tab trip.
  useEffect(() => {
    firstFieldRef.current?.focus();
  }, []);

  return (
    <form
      onSubmit={onSubmit}
      className="bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-6 space-y-5 shadow-[0_8px_32px_rgba(0,0,0,0.4)]"
      data-testid="uploads-form"
    >
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
                error={jitterError}
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

      <div className="flex items-center justify-between gap-3 pt-2 border-t border-white/[0.06]">
        <p className="text-[12px] text-[#9aa0aa]/80 flex items-center gap-1.5">
          <Info size={11} aria-hidden="true" />
          1 round-trip · up to 50 Drive pages (≈10 k clips) per import.
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
    </form>
  );
}

function SuccessView({
  payload,
  onRunAnother,
}: {
  payload: SuccessPayload;
  onRunAnother: () => void;
}) {
  const firstDate = payload.firstPublishAt
    ? new Date(payload.firstPublishAt)
    : null;
  const lastDate = payload.lastScheduledAt
    ? new Date(payload.lastScheduledAt)
    : null;
  const preview = payload.entries.slice(0, 5);

  return (
    <div
      className="bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-6 space-y-5 shadow-[0_8px_32px_rgba(0,0,0,0.4)]"
      data-testid="uploads-success"
    >
      <div className="flex items-start gap-3">
        <div className="w-10 h-10 rounded-full bg-emerald-500/[0.12] border border-emerald-500/[0.30] flex items-center justify-center text-emerald-400 shrink-0">
          <CheckCircle2 size={20} aria-hidden="true" />
        </div>
        <div className="min-w-0">
          <p className="text-[16px] font-bold text-white">
            {payload.scheduledCount > 0
              ? `${payload.scheduledCount} video${payload.scheduledCount === 1 ? "" : "s"} scheduled from ${payload.folderId.slice(0, 10)}${payload.folderId.length > 10 ? "…" : ""}`
              : "Folder imported (no publishable videos)"}
          </p>
          <p className="text-[13px] text-[#9aa0aa] mt-0.5">
            Scanned {payload.pageCount} Drive page
            {payload.pageCount === 1 ? "" : "s"} in one round-trip.
          </p>
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
                  <span className="text-[#9aa0aa] font-mono text-[11px] mr-2 tabular-nums">
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
              +{payload.entries.length - preview.length} more — open the
              calendar to see them all.
            </p>
          )}
        </div>
      )}

      <div className="flex items-center justify-end gap-2 pt-2">
        <Link
          to="/app/uploads/calendar"
          className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-white/[0.06] border border-white/[0.10] text-[13px] font-semibold text-white hover:bg-white/[0.10] transition-colors no-underline"
          data-testid="uploads-view-calendar"
        >
          Open calendar <ArrowRight size={14} aria-hidden="true" />
        </Link>
        <button
          type="button"
          onClick={onRunAnother}
          data-testid="uploads-run-another"
          className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-white text-black text-[13px] font-semibold hover:bg-white/90 transition-colors"
        >
          Import another folder
        </button>
      </div>
    </div>
  );
}

function PartialView({
  payload,
  onRunAnother,
  folderUrl,
}: {
  payload: PartialPayload;
  onRunAnother: () => void;
  folderUrl: string;
}) {
  return (
    <div
      className="bg-[#1f1f2e] border border-amber-500/[0.30] rounded-2xl p-6 space-y-5 shadow-[0_8px_32px_rgba(0,0,0,0.4)]"
      data-testid="uploads-partial"
    >
      <div className="flex items-start gap-3">
        <div className="w-10 h-10 rounded-full bg-amber-500/[0.12] border border-amber-500/[0.30] flex items-center justify-center text-amber-400 shrink-0">
          <AlertTriangle size={20} aria-hidden="true" />
        </div>
        <div className="min-w-0">
          <p className="text-[16px] font-bold text-white">
            Partial — some pages succeeded, others didn&apos;t
          </p>
          <p className="text-[13px] text-[#9aa0aa] mt-0.5">
            {payload.scheduledCount} video
            {payload.scheduledCount === 1 ? "" : "s"} queued before the
            upstream blip. The remaining pages were not processed — please
            resume manually after the upstream issue clears.
          </p>
        </div>
      </div>

      <dl className="grid grid-cols-1 sm:grid-cols-3 gap-3">
        <ScheduleBlock
          label="Scheduled so far"
          icon={<CheckCircle2 size={14} />}
          statValue={payload.scheduledCount}
        />
        <ScheduleBlock
          label="Pages scanned"
          icon={<FolderInput size={14} />}
          statValue={payload.pageCount}
        />
        <ScheduleBlock
          label="Failed at page"
          icon={<AlertTriangle size={14} />}
          statValue={payload.failedAtPage}
        />
      </dl>

      <div className="rounded-xl border border-amber-500/[0.20] bg-amber-500/[0.06] p-4 text-[13px] text-amber-100 space-y-2">
        <p className="font-semibold text-amber-200">How to resume</p>
        <p>
          Hit the existing single-page endpoint with the two tokens shown
          below — your Drive folder id, the failed page token, and the
          resume cursor. The handler will pick up where this import left
          off, preserving the cumulative jitter.
        </p>
        <dl className="text-[12px] grid grid-cols-[110px_1fr] gap-x-3 gap-y-1 font-mono">
          <dt className="text-amber-300/80">folder_id</dt>
          <dd className="text-white">{payload.folderId}</dd>
          <dt className="text-amber-300/80">page_token</dt>
          <dd className="text-white break-all">
            {payload.failedAtPageToken || "(empty — try page 1 again)"}
          </dd>
          <dt className="text-amber-300/80">cursor_scheduled_at</dt>
          <dd className="text-white">{payload.lastScheduledAt}</dd>
        </dl>
        {payload.note && (
          <p className="text-amber-200/80 text-[12px] mt-2">{payload.note}</p>
        )}
        <p className="text-[12px] text-amber-200/70 pt-1">
          Resume endpoint:{" "}
          <code className="font-mono">POST /api/v1/media/import/drive/folder</code>
        </p>
      </div>

      <details className="text-[13px] text-[#9aa0aa]">
        <summary className="cursor-pointer hover:text-white transition-colors">
          {payload.entries.length} entry
          {payload.entries.length === 1 ? "" : "ies"} already queued
        </summary>
        <ul className="mt-2 space-y-1 max-h-48 overflow-y-auto">
          {payload.entries.map((e) => (
            <li
              key={e.job_id}
              className="flex items-center justify-between gap-2 p-2 rounded-lg bg-white/[0.04]"
            >
              <span className="text-white truncate">{e.name}</span>
              <time className="text-[11px] text-[#9aa0aa] tabular-nums whitespace-nowrap">
                {new Date(e.scheduled_at).toLocaleString()}
              </time>
            </li>
          ))}
        </ul>
      </details>

      <div className="flex items-center justify-between gap-3 pt-2">
        <a
          href={folderUrl}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-white/[0.06] border border-white/[0.10] text-[13px] font-semibold text-white hover:bg-white/[0.10] transition-colors no-underline"
        >
          Open folder in Drive <ExternalLink size={14} aria-hidden="true" />
        </a>
        <button
          type="button"
          onClick={onRunAnother}
          className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-white text-black text-[13px] font-semibold hover:bg-white/90 transition-colors"
        >
          Import another folder
        </button>
      </div>
    </div>
  );
}

function GuidanceView({
  note,
  onBack,
}: {
  note: string;
  onBack: () => void;
}) {
  return (
    <div
      className="bg-[#1f1f2e] border border-amber-500/[0.30] rounded-2xl p-6 space-y-4 shadow-[0_8px_32px_rgba(0,0,0,0.4)]"
      data-testid="uploads-guidance"
    >
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
          data-testid="uploads-guidance-back"
          className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-white text-black text-[13px] font-semibold hover:bg-white/90 transition-colors"
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
    <div
      className="bg-[#1f1f2e] border border-red-500/[0.30] rounded-2xl p-6 space-y-4 shadow-[0_8px_32px_rgba(0,0,0,0.4)]"
      data-testid="uploads-error"
    >
      <ErrorState title="Import failed" message={message} />
      <div className="flex items-center justify-end gap-2">
        <button
          type="button"
          onClick={onBack}
          className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-white text-black text-[13px] font-semibold hover:bg-white/90 transition-colors"
        >
          Back to form
        </button>
      </div>
    </div>
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

function ScheduleBlock({
  label,
  icon,
  date,
  empty,
  statValue,
}: {
  label: string;
  icon: React.ReactNode;
  date?: Date | null;
  empty?: string;
  statValue?: number;
}) {
  return (
    <div className="p-3 rounded-xl bg-white/[0.04] border border-white/[0.08]">
      <dt className="text-[11px] font-semibold text-[#9aa0aa] uppercase tracking-wider flex items-center gap-1">
        {icon} {label}
      </dt>
      <dd className="mt-1 text-[14px] text-white font-medium">
        {statValue !== undefined ? (
          <span className="tabular-nums">{statValue}</span>
        ) : date ? (
          formatDateTime(date)
        ) : (
          empty
        )}
      </dd>
    </div>
  );
}

function formatSeconds(s: number): string {
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.round(s / 60)}m`;
  if (s < 86400) return `${(s / 3600).toFixed(1)}h`;
  return `${(s / 86400).toFixed(1)}d`;
}

function formatDateTime(date: Date): string {
  if (Number.isNaN(date.getTime())) return "—";
  const diffMs = date.getTime() - Date.now();
  const absMinutes = Math.round(Math.abs(diffMs) / 60_000);
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
