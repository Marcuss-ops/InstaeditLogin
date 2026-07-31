import {
  FolderInput,
  X,
} from "lucide-react";
import { ErrorState, Skeleton } from "../../components/feedback";
import { useDriveBatchImport } from "./useDriveBatchImport";
import {
  ErrorView,
  GuidanceView,
  ImportForm,
  SuccessView,
} from "./DriveBatchImportDialogViews";

export function DriveBatchImportDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const {
    cardRef,
    firstFieldRef,
    loadState,
    submitState,
    form,
    setForm,
    folderValid,
    jitterError,
    handleSubmit,
    handleContinuePagination,
    handleViewPosts,
    handleBackToForm,
  } = useDriveBatchImport({ open, onClose });

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
