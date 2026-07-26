import { Link } from "react-router-dom";
import {
  ArrowRight,
  CheckCircle2,
  Clock,
  ExternalLink,
  FolderInput,
  Loader2,
  Video,
} from "lucide-react";
import { ErrorState, Skeleton } from "../../components/feedback";
import { EmptyState } from "../../components/feedback";
import { useUploads } from "../../hooks/useUploads";
import { UploadActions } from "../../components/uploads/UploadActions";
import { UploadFilters } from "../../components/uploads/UploadFilters";

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
                Import a Drive folder to YouTube
              </h1>
              <p className="text-[15px] text-[#9aa0aa] mt-2 max-w-xl">
                Download videos from Drive → upload to YouTube → edit thumbnail
                → publish as private on a schedule. One round-trip, even for
                folders with thousands of clips.
              </p>
            </div>
            <div className="hidden sm:flex items-center gap-3">
              <Link
                to="/app/youtube/studio"
                className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors no-underline"
              >
                YouTube Studio <ArrowRight size={14} />
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

            {loadState.workspaces.length > 0 &&
              loadState.youtubeChannels.length === 0 && (
                <EmptyState
                  title="No YouTube channels connected"
                  description="Connect a YouTube channel in /app/linking — this importer uploads to channels you own."
                  icon={<Video size={32} />}
                  cta={
                    <Link
                      to="/app/linking"
                      className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white text-black text-[13px] font-semibold hover:bg-white/90 transition-colors no-underline"
                    >
                      Connect a channel
                      <ArrowRight size={14} />
                    </Link>
                  }
                  className="bg-[#1f1f2e] border-white/[0.12]"
                />
              )}

            {loadState.workspaces.length > 0 &&
              loadState.youtubeChannels.length > 0 &&
              loadState.driveAccounts.length === 0 && (
                <EmptyState
                  title="No Google Drive account linked"
                  description="Link a Google Drive account in /app/linking — the importer needs OAuth to read your Drive folder."
                  icon={<FolderInput size={32} />}
                  cta={
                    <Link
                      to="/app/linking"
                      className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white text-black text-[13px] font-semibold hover:bg-white/90 transition-colors no-underline"
                    >
                      Link a Drive account
                      <ArrowRight size={14} />
                    </Link>
                  }
                  className="bg-[#1f1f2e] border-white/[0.12]"
                />
              )}

            {loadState.workspaces.length > 0 &&
              loadState.youtubeChannels.length > 0 &&
              loadState.driveAccounts.length > 0 &&
              (submitState.kind === "queued" ? (
                <QueuedView
                  batchId={submitState.batchId}
                  onRunAnother={handleRunAnother}
                />
              ) : submitState.kind === "polling" ? (
                <PollingView batchId={submitState.batchId} />
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
                    youtubeChannels={loadState.youtubeChannels}
                    drives={loadState.driveAccounts}
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

function QueuedView({
  batchId,
  onRunAnother,
}: {
  batchId: string;
  onRunAnother: () => void;
}) {
  return (
    <div
      className="bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-6 space-y-5 shadow-[0_8px_32px_rgba(0,0,0,0.4)]"
      data-testid="uploads-queued"
    >
      <div className="flex items-start gap-3">
        <div className="w-10 h-10 rounded-full bg-emerald-500/[0.12] border border-emerald-500/[0.30] flex items-center justify-center text-emerald-400 shrink-0">
          <CheckCircle2 size={20} aria-hidden="true" />
        </div>
        <div className="min-w-0">
          <p className="text-[16px] font-bold text-white">Batch queued</p>
          <p className="text-[13px] text-[#9aa0aa] mt-0.5">
            Your Drive folder import has been queued. Videos will be uploaded to
            YouTube as the background crawler processes them.
          </p>
        </div>
      </div>

      <dl className="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <div className="bg-white/[0.03] border border-white/[0.08] rounded-xl p-3">
          <dt className="text-[11px] text-[#9aa0aa] uppercase tracking-wider mb-1 flex items-center gap-1.5">
            <Clock size={12} aria-hidden="true" /> Batch ID
          </dt>
          <dd className="text-[13px] font-mono text-white">{batchId}</dd>
        </div>
        <div className="bg-white/[0.03] border border-white/[0.08] rounded-xl p-3">
          <dt className="text-[11px] text-[#9aa0aa] uppercase tracking-wider mb-1 flex items-center gap-1.5">
            <Clock size={12} aria-hidden="true" /> Status
          </dt>
          <dd className="text-[13px] font-semibold text-emerald-400">
            Processing
          </dd>
        </div>
      </dl>

      <div className="rounded-xl border border-blue-500/[0.20] bg-blue-500/[0.06] p-4 text-[13px] text-blue-100 space-y-2">
        <p className="font-semibold text-blue-200">What happens next</p>
        <p>
          The background crawler scans your Drive folder, uploads each video to
          YouTube as private, and creates editor sessions for thumbnail editing.
          When videos are ready, visit{" "}
          <Link
            to="/app/youtube/studio"
            className="underline text-blue-200 hover:text-white"
          >
            YouTube Studio
          </Link>{" "}
          to edit thumbnails and publish.
        </p>
      </div>

      <div className="flex items-center justify-between gap-3 pt-2">
        <Link
          to="/app/youtube/studio"
          className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-white/[0.06] border border-white/[0.10] text-[13px] font-semibold text-white hover:bg-white/[0.10] transition-colors no-underline"
        >
          Open YouTube Studio <ExternalLink size={14} aria-hidden="true" />
        </Link>
        <button
          type="button"
          onClick={onRunAnother}
          className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white/[0.06] border border-white/[0.10] text-[13px] font-semibold text-white hover:bg-white/[0.10] transition-colors"
        >
          Import another folder
        </button>
      </div>
    </div>
  );
}

function PollingView({ batchId }: { batchId: string }) {
  return (
    <div
      className="bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-6 space-y-5 shadow-[0_8px_32px_rgba(0,0,0,0.4)]"
      data-testid="uploads-polling"
    >
      <div className="flex items-start gap-3">
        <div className="w-10 h-10 rounded-full bg-blue-500/[0.12] border border-blue-500/[0.30] flex items-center justify-center text-blue-400 shrink-0">
          <Loader2 size={20} className="animate-spin" aria-hidden="true" />
        </div>
        <div className="min-w-0">
          <p className="text-[16px] font-bold text-white">
            Processing your import…
          </p>
          <p className="text-[13px] text-[#9aa0aa] mt-0.5">
            The crawler is scanning your Drive folder and uploading videos to
            YouTube. This may take a while for large folders.
          </p>
        </div>
      </div>

      <div className="bg-white/[0.03] border border-white/[0.08] rounded-xl p-3">
        <p className="text-[11px] text-[#9aa0aa] uppercase tracking-wider mb-1">
          Batch ID
        </p>
        <p className="text-[13px] font-mono text-white">{batchId}</p>
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
      <UploadActions mode="back" onBack={onBack} />
    </div>
  );
}
