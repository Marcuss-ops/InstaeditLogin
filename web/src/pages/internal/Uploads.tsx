import { Link } from "react-router-dom";
import {
  AlertTriangle,
  ArrowRight,
  CheckCircle2,
  Clock,
  ExternalLink,
  FolderInput,
  Video,
} from "lucide-react";
import { ErrorState, Skeleton } from "../../components/feedback";
import { EmptyState } from "../../components/feedback";
import { useUploads } from "../../hooks/useUploads";
import { UploadActions } from "../../components/uploads/UploadActions";
import { UploadFilters } from "../../components/uploads/UploadFilters";
import { UploadStatusBadge } from "../../components/uploads/UploadStatusBadge";
import { UploadsTable } from "../../components/uploads/UploadsTable";
import { ScheduleBlock } from "../../components/uploads/ScheduleBlock";
import type { PartialPayload, SuccessPayload } from "../../types/uploads";

export function InternalUploads() {
  const {
    loadState,
    submitState,
    form,
    setForm,
    folderValid,
    canSubmit,
    resetSubmit,
    firstFieldRef,
    handleSubmit,
    handleRunAnother,
  } = useUploads();

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
            <div className="hidden sm:flex items-center gap-3">
              <UploadStatusBadge state={submitState} />
              <Link
                to="/app/uploads/calendar"
                className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors no-underline"
              >
                View calendar <ArrowRight size={14} />
              </Link>
            </div>
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
                  onBack={resetSubmit}
                />
              ) : (
                <form
                  onSubmit={handleSubmit}
                  className="bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-6 space-y-5 shadow-[0_8px_32px_rgba(0,0,0,0.4)]"
                  data-testid="uploads-form"
                >
                  <UploadFilters
                    form={form}
                    setForm={setForm}
                    workspaces={loadState.workspaces}
                    pages={loadState.pages}
                    drives={loadState.drives}
                    folderValid={folderValid}
                    isSubmitting={submitState.kind === "submitting"}
                    firstFieldRef={firstFieldRef}
                  />
                  <UploadActions
                    mode="form"
                    canSubmit={canSubmit}
                    isSubmitting={submitState.kind === "submitting"}
                  />
                </form>
              ))}
          </>
        )}
      </div>
    </div>
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

      {payload.entries.length > 0 && <UploadsTable entries={payload.entries} />}

      <UploadActions
        mode="result"
        onRunAnother={onRunAnother}
        calendarHref="/app/uploads/calendar"
      />
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

      {payload.entries.length > 0 && (
        <details className="text-[13px] text-[#9aa0aa]">
          <summary className="cursor-pointer hover:text-white transition-colors">
            {payload.entries.length} entry
            {payload.entries.length === 1 ? "" : "ies"} already queued
          </summary>
          <div className="mt-2 max-h-48 overflow-y-auto">
            <UploadsTable entries={payload.entries} previewLimit={payload.entries.length} />
          </div>
        </details>
      )}

      <div className="flex items-center justify-between gap-3 pt-2">
        <a
          href={folderUrl}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-white/[0.06] border border-white/[0.10] text-[13px] font-semibold text-white hover:bg-white/[0.10] transition-colors no-underline"
        >
          Open folder in Drive <ExternalLink size={14} aria-hidden="true" />
        </a>
        <UploadActions mode="result" onRunAnother={onRunAnother} />
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
      <UploadActions mode="back" onBack={onBack} />
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
      <UploadActions mode="back" onBack={onBack} />
    </div>
  );
}
