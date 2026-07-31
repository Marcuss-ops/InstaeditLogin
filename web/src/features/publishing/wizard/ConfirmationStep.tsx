/**
 * ConfirmationStep — Step 3 of the /app/content/new wizard.
 *
 * Responsibilities:
 *   1. Render a read-only summary of the consolidated payload the
 *      user has assembled in Steps 1 + 2 (video, internal title,
 *      channel, YT title, description, tag count, made-for-kids,
 *      locked privacy = "private").
 *   2. On "Carica su YouTube" submit:
 *      - Build the POST /api/v1/posts body (workspace_id, status,
 *        content.media[], targets[0].youtube settings).
 *      - Call the existing `useCreatePost` hook — it mints a fresh
 *        RFC4122 v4 UUID per submit and forwards it via the
 *        `Idempotency-Key` header (server cache: same key + same
 *        payload → replay cached, browser double-click is naturally
 *        idempotent).
 *   3. On submit success → navigate internally to
 *      /app/content/{id}/publish (trust-boundary: the server
 *      returned `id`). The publish-status page self-loads
 *      `targets[]` via /api/v1/posts/{id}/targets, so no extra
 *      client-state hand-off is required from the parent.
 *   4. On AuthError → re-thrown so the caller navigates to /login
 *      (matches useCreatePost's other callers).
 *   5. On ApiError / network / 409 → inline error card with retry.
 *
 * Design choice — submit gate:
 *   `canSubmit` is unconditional: by Step 3, Step 1 already moved
 *   only when `state.kind === "done"` (asset ready) and Step 2 only
 *   moved when workspace/channel/ytTitle were all set. So the gate
 *   here is just `!isSubmitting`. Belt-and-braces validation is
 *   server-side.
 *
 * Read-only summary rule:
 *   Nothing in this step is editable — back → forward preserves
 *   everything (the lifted state lives in ContentNew, not here).
 *   Each summary row carries a small "Modifica" link that calls
 *   `onJumpToStep(n)` so the user can fix an entry without going
 *   through every prior step.
 */
import { useEffect, useRef } from "react";
import { useNavigate } from "react-router-dom";
import {
  AlertCircle,
  ArrowLeft,
  ArrowRight,
  Check,
  Film,
  Lock,
  RotateCw,
  Upload,
} from "lucide-react";
import { cn } from "../../../lib/utils";
import { AuthError } from "../../../lib/auth";
import { useCreatePost } from "../hooks/useCreatePost";
import type { CreatePostRequest } from "../api/types";
import type { MediaAsset } from "../api/mediaApi";
import type { ChannelMetadata } from "./ChannelMetadataStep";

/**
 * Privacy is locked at "private" for the vertical slice — the Dark
 * Editor applies the final visibility AFTER the publish commits.
 * Sourced as a local constant (NOT a prop) so a future PR widening
 * the user's choice must explicitly opt-in here.
 */
const PRIVACY_LOCKED = "private" as const;

/**
 * Step numbers we can jump back to from the summary's "Modifica"
 * link. Keep aligned with ContentNew's local indexed states.
 */
export type WizardStep = 1 | 2 | 3;

export interface ConfirmationStepProps {
  /** Asset from Step 1 (`useUploadMedia` → done state). */
  asset: MediaAsset;
  /** Internal title (collected in Step 1's text input). */
  internalTitle: string;
  /** Channel + metadata from Step 2 (channelId, workspaceId, ytTitle, etc.). */
  channel: ChannelMetadata;
  /** Back button → Step 2. */
  onBack: () => void;
  /** Jump-to-step link on each summary row. */
  onJumpToStep: (step: Exclude<WizardStep, 3>) => void;
}

/** Human-readable byte size string (KB / MB / GB). */
function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 ** 2) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 ** 3) return `${(bytes / 1024 ** 2).toFixed(1)} MB`;
  return `${(bytes / 1024 ** 3).toFixed(2)} GB`;
}

/** Render a single line of the payload preview in monospace. */
function PreviewCode({
  children,
  testId,
}: {
  children: React.ReactNode;
  testId?: string;
}) {
  return (
    <code
      className="px-1.5 py-0.5 rounded bg-white/[0.06] text-[#cdd2da] font-mono text-[11px] break-all"
      data-testid={testId}
    >
      {children}
    </code>
  );
}

export function ConfirmationStep({
  asset,
  internalTitle,
  channel,
  onBack,
  onJumpToStep,
}: ConfirmationStepProps) {
  const navigate = useNavigate();
  const { state, submit, reset } = useCreatePost();
  // Tracks the POST we already announced to the parent so a re-render
  // (e.g. fast-refresh, dev-only StrictMode double-invoke) doesn't
  // re-fire the onComplete and double-navigate.
  const lastFiredPostIdRef = useRef<number | null>(null);

  const isSubmitting = state.kind === "submitting";
  // canSubmit is unconditional by Step 3 — see module docstring.
  const canSubmit = !isSubmitting;

  useEffect(() => {
    if (state.kind !== "success") return;
    if (lastFiredPostIdRef.current === state.post.id) return;
    lastFiredPostIdRef.current = state.post.id;
    // Navigate to the publish-status page. Trust-boundary: the
    // server returned `id` so we don't re-validate form values.
    // ContentPublish reads `post.id` from the URL param and
    // self-loads `targets[]` via GET /api/v1/posts/{id}/targets;
    // no extra client-state hand-off is required.
    navigate(`/app/content/${state.post.id}/publish`);
  }, [state, navigate]);

  const buildPayload = (): CreatePostRequest => ({
    workspace_id: channel.workspaceId,
    status: "queued",
    content: {
      title: internalTitle,
      caption: channel.description.trim().length > 0
        ? channel.description.trim()
        : undefined,
      media: [{ asset_id: asset.id }],
    },
    targets: [
      {
        platform_account_id: channel.channelId,
        settings: {
          youtube: {
            title: channel.ytTitle.trim(),
            description: channel.description.trim().length > 0
              ? channel.description.trim()
              : undefined,
            privacy_status: PRIVACY_LOCKED,
            made_for_kids: channel.madeForKids,
            tags: channel.tags,
          },
        },
      },
    ],
  });

  /**
   * Run the same POST /api/v1/posts payload the form would build.
   * Extracted as a closure so the retry button (and future keyboard
   * shortcut handlers) can invoke it without faking a synthetic
   * React.FormEvent. The form's `onSubmit` calls this with the
   * event-prevented guard.
   */
  const runSubmit = async (): Promise<void> => {
    if (!canSubmit) return;
    try {
      await submit(buildPayload());
    } catch (err) {
      if (err instanceof AuthError) {
        // 401 → log out. useCreatePost raised AuthError unhandled;
        // re-navigate here for safety in case a page outside
        // <ProtectedRoute> ever mounts this step.
        navigate("/login", { replace: true });
        return;
      }
      // ApiError / network → already stored in `state.kind === "error"`
      // by the hook. Re-throwing here would double-surface; no-op.
    }
  };

  const handleSubmit = (e: React.FormEvent): void => {
    e.preventDefault();
    void runSubmit();
  };

  const handleRetry = (): void => {
    reset();
    void runSubmit();
  };

  return (
    <form
      onSubmit={handleSubmit}
      className="rounded-2xl border border-white/[0.08] bg-[#0d0d14]/80 backdrop-blur p-6 md:p-8 shadow-[0_0_0_1px_rgba(255,255,255,0.02)]"
      data-testid="confirmation-step"
      noValidate
    >
      <h2 className="text-xl font-semibold text-white mb-1">
        Step 3 — Conferma
      </h2>
      <p className="text-sm text-[#9aa0aa] mb-6">
        Verifica i dati e invia la pubblicazione. Il video verrà
        caricato su YouTube come privato; successivamente potrai
        modificare copertina e visibilità dal Dark Editor.
      </p>

      {/* ────── Read-only summary ────── */}
      <div className="space-y-3" data-testid="confirm-summary">
        {/* Video row */}
        <SummaryRow
          icon={<Film size={16} aria-hidden="true" />}
          title="Video"
          onEdit={() => onJumpToStep(1)}
          editTestId="edit-step-1"
        >
          <div className="text-white font-medium truncate" data-testid="summary-asset-name">
            {asset.content_type}
          </div>
          <div className="text-xs text-[#9aa0aa] mt-0.5 font-mono break-all">
            asset_id: {asset.id}
          </div>
        </SummaryRow>

        {/* Internal title row */}
        <SummaryRow
          icon={<Check size={16} aria-hidden="true" />}
          title="Titolo interno"
          onEdit={() => onJumpToStep(1)}
          editTestId="edit-step-1-title"
        >
          <div className="text-white" data-testid="summary-internal-title">
            {internalTitle || (
              <span className="text-[#9aa0aa] italic">— non impostato —</span>
            )}
          </div>
        </SummaryRow>

        {/* Workspace + channel row */}
        <SummaryRow
          icon={<Check size={16} aria-hidden="true" />}
          title="Canale YouTube"
          onEdit={() => onJumpToStep(2)}
          editTestId="edit-step-2-channel"
        >
          <div className="text-white" data-testid="summary-channel">
            workspace #{channel.workspaceId} · channel #
            {channel.channelId}
          </div>
        </SummaryRow>

        {/* YT title row */}
        <SummaryRow
          icon={<Check size={16} aria-hidden="true" />}
          title="Titolo YouTube"
          onEdit={() => onJumpToStep(2)}
          editTestId="edit-step-2-title"
        >
          <div className="text-white" data-testid="summary-yt-title">
            {channel.ytTitle}
          </div>
          {channel.description.trim().length > 0 && (
            <div className="text-xs text-[#9aa0aa] mt-1 line-clamp-2">
              {channel.description}
            </div>
          )}
        </SummaryRow>

        {/* Tags row */}
        {channel.tags.length > 0 && (
          <SummaryRow
            icon={<Check size={16} aria-hidden="true" />}
            title={`Tag (${channel.tags.length})`}
            onEdit={() => onJumpToStep(2)}
            editTestId="edit-step-2-tags"
          >
            <div className="flex flex-wrap gap-1.5" data-testid="summary-tags">
              {channel.tags.map((t) => (
                <span
                  key={t}
                  className="px-2 py-0.5 rounded-md bg-white/[0.06] text-[#cdd2da] text-xs font-mono"
                >
                  {t}
                </span>
              ))}
            </div>
          </SummaryRow>
        )}

        {/* Made for kids row */}
        <SummaryRow
          icon={<Check size={16} aria-hidden="true" />}
          title="Made for kids"
          onEdit={() => onJumpToStep(2)}
          editTestId="edit-step-2-mfk"
        >
          <div
            className={cn(
              "text-sm",
              channel.madeForKids ? "text-amber-300" : "text-[#9aa0aa]",
            )}
            data-testid="summary-made-for-kids"
          >
            {channel.madeForKids ? "Sì" : "No"}
          </div>
        </SummaryRow>

        {/* Privacy row — locked chip */}
        <div
          className="rounded-xl border border-emerald-500/30 bg-emerald-500/[0.06] px-4 py-3 flex items-center gap-3"
          data-testid="privacy-locked"
        >
          <Lock
            size={16}
            className="text-emerald-300 shrink-0"
            aria-hidden="true"
          />
          <div className="flex-1 min-w-0">
            <div className="text-sm text-emerald-200 font-medium">
              Privacy iniziale: Privato
            </div>
            <div className="text-xs text-[#9aa0aa] mt-0.5">
              Fissato dal flusso di pubblicazione. Potrai modificare
              la visibilità in seguito dal Dark Editor (unlisted /
              public).
            </div>
          </div>
          <PreviewCode data-testid="privacy-code">
            privacy_status: "{PRIVACY_LOCKED}"
          </PreviewCode>
        </div>

        {/* Payload preview — dev-facing transparency */}
        <details
          className="rounded-xl border border-white/[0.06] bg-[#08080d] px-4 py-2"
          data-testid="payload-preview-details"
        >
          <summary className="text-xs text-[#9aa0aa] cursor-pointer select-none hover:text-white transition-colors">
            Anteprima payload JSON
          </summary>
          <pre className="mt-2 text-[11px] leading-relaxed text-[#9aa0aa] font-mono whitespace-pre-wrap break-all max-h-72 overflow-y-auto">
            {JSON.stringify(buildPayload(), null, 2)}
          </pre>
        </details>
      </div>

      {/* ────── Submitting indicator ────── */}
      {state.kind === "submitting" && (
        <div
          className="mt-6 rounded-xl border border-white/[0.08] bg-[#0a0a10] px-4 py-3 flex items-center gap-3"
          data-testid="submit-progress"
          role="status"
        >
          <RotateCw
            size={18}
            className="text-emerald-300 animate-spin shrink-0"
            aria-hidden="true"
          />
          <div className="flex-1 min-w-0">
            <div className="text-sm text-white font-medium">
              Pubblicazione in corso…
            </div>
            <div className="text-xs text-[#9aa0aa] mt-0.5">
              Stiamo creando il post e accodando il target YouTube.
            </div>
          </div>
        </div>
      )}

      {/* ────── Error card ────── */}
      {state.kind === "error" && (
        <div
          className="mt-6 rounded-xl border border-red-500/30 bg-red-500/[0.06] px-4 py-3 flex items-start gap-3"
          data-testid="submit-error"
          role="alert"
        >
          <AlertCircle
            size={18}
            className="text-red-300 shrink-0 mt-0.5"
            aria-hidden="true"
          />
          <div className="flex-1 min-w-0">
            <div className="text-sm text-red-200 font-medium">
              Pubblicazione non riuscita
            </div>
            <div className="text-sm text-red-200/80 mt-0.5 break-words">
              {state.message}
            </div>
            <p className="text-xs text-[#9aa0aa] mt-1.5">
              Verrà generato un nuovo Idempotency-Key al prossimo
              tentativo: nessun rischio di duplicati.
            </p>
            <button
              type="button"
              onClick={handleRetry}
              className="mt-2 inline-flex items-center gap-1.5 text-sm font-medium text-red-200 hover:text-red-100 underline underline-offset-2"
              data-testid="submit-retry"
            >
              <RotateCw size={14} aria-hidden="true" />
              Riprova
            </button>
          </div>
        </div>
      )}

      {/* ────── Asset size hint (purely informational) ────── */}
      <div className="mt-3 text-[11px] text-[#5c6473] font-mono">
        {asset.content_type} · {formatSize(asset.size_bytes)}
      </div>

      {/* ────── Action row ────── */}
      <div className="mt-6 flex items-center justify-between gap-3">
        <button
          type="button"
          onClick={onBack}
          disabled={isSubmitting}
          className="inline-flex items-center gap-1.5 text-sm text-[#9aa0aa] hover:text-white disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
          data-testid="back-button"
        >
          <ArrowLeft size={14} aria-hidden="true" />
          Indietro
        </button>
        <button
          type="submit"
          disabled={!canSubmit}
          className="inline-flex items-center gap-2 rounded-xl bg-white text-[#030308] px-4 py-2.5 text-sm font-semibold hover:bg-[#e8ecf2] disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
          data-testid="submit-button"
        >
          {isSubmitting ? (
            <>
              <RotateCw
                size={16}
                className="animate-spin"
                aria-hidden="true"
              />
              Caricamento…
            </>
          ) : (
            <>
              <Upload size={16} aria-hidden="true" />
              Carica su YouTube
              <ArrowRight size={14} aria-hidden="true" />
            </>
          )}
        </button>
      </div>
    </form>
  );
}

// ─────────────────────────────────────────────────────────────────
// Helper — SummaryRow (kept local; promote to components/forms if a
// second wizard reuses it).
// ─────────────────────────────────────────────────────────────────

function SummaryRow({
  icon,
  title,
  onEdit,
  editTestId,
  children,
}: {
  icon: React.ReactNode;
  title: string;
  onEdit: () => void;
  editTestId: string;
  children: React.ReactNode;
}) {
  return (
    <div
      className="rounded-xl border border-white/[0.06] bg-[#0a0a10] px-4 py-3 flex items-start gap-3"
      data-testid={`summary-row-${title.toLowerCase().replace(/\s+/g, "-")}`}
    >
      <div className="w-7 h-7 rounded-md bg-white/[0.06] flex items-center justify-center text-[#cdd2da] shrink-0 mt-0.5">
        {icon}
      </div>
      <div className="flex-1 min-w-0">{children}</div>
      <button
        type="button"
        onClick={onEdit}
        className="text-xs text-[#9aa0aa] hover:text-white underline underline-offset-2 transition-colors shrink-0"
        data-testid={editTestId}
      >
        Modifica
      </button>
    </div>
  );
}
