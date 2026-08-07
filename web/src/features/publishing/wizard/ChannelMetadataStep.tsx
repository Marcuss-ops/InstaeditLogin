/**
 * ChannelMetadataStep — Step 2 of the /app/content/new wizard.
 *
 * Form fields:
 *   - canale YouTube              (single-select, filtered on
 *                                  `platform === "youtube"` from
 *                                  GET /api/v1/accounts)
 *   - area di lavoro              (workspace_id required by POST
 *                                  /api/v1/posts — auto-picks if
 *                                  exactly one workspace exists)
 *   - titolo YouTube              (text, YT Data API v3 max ~100
 *                                  chars; platform cap enforced
 *                                  client-side)
 *   - descrizione                 (textarea, ≤ 5000 chars)
 *   - tag                         (TagInput, max 30 = hard YT limit)
 *   - made for kids               (boolean toggle)
 *
 * Locked (NOT user-editable per the spec):
 *   - privacy_status: "private" — surfaced as a locked chip with a
 *     `Lock` icon + explanatory caption. InstaEditor applies the
 *     final privacy (unlisted / public) AFTER the wizard commits
 *     the upload.
 *
 * Submit gating:
 *   `canSubmit = workspaceId != null && channelId != null && ytTitle.trim() != ""`
 *
 * On submit, fires `onComplete(ChannelMetadata)` and the parent
 * (ContentNew) advances to Step 3. Back invokes `onBack`.
 */
import { useEffect, useState } from "react";
import { ArrowLeft, ChevronRight, Lock } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { cn } from "../../../lib/utils";
import { ProviderBadge } from "../../../components/brand/PlatformLogos";
import { AuthError } from "../../../lib/auth";
import { Skeleton } from "../../../components/feedback/Skeleton";
import { ErrorState } from "../../../components/feedback/ErrorState";
import { EmptyState } from "../../../components/feedback/EmptyState";
import { TagInput } from "../../../components/forms/TagInput";
import { useYouTubeChannels } from "../../channels/hooks/useYouTubeChannels";
import type { ChannelsLoadState } from "../../channels/hooks/useYouTubeChannels";

const YT_TITLE_MAX = 100;
const YT_DESCRIPTION_MAX = 5000;
const YT_TAGS_MAX = 30;
const YOUTUBE_PRIVACY_LOCKED = "private" as const;

export interface ChannelMetadata {
  workspaceId: number;
  channelId: number;
  /** YouTube target title — goes into post target settings.youtube.title. */
  ytTitle: string;
  description: string;
  tags: string[];
  madeForKids: boolean;
}

export interface ChannelMetadataStepProps {
  /** Pre-fill from lifted state (back → forward preserves the entry). */
  initial?: Partial<ChannelMetadata> | null;
  onComplete: (meta: ChannelMetadata) => void;
  onBack: () => void;
}

export function ChannelMetadataStep({
  initial,
  onComplete,
  onBack,
}: ChannelMetadataStepProps) {
  const navigate = useNavigate();
  const { state, refetch } = useYouTubeChannels();

  // Constants are not user-modifiable. We capture into a separate
  // form-state so a future schema extension (e.g. unlisted-target)
  // only widens this union without touching the rest of the form.
  const [workspaceId, setWorkspaceId] = useState<number | null>(
    initial?.workspaceId ?? null,
  );
  const [channelId, setChannelId] = useState<number | null>(
    initial?.channelId ?? null,
  );
  const [ytTitle, setYtTitle] = useState(initial?.ytTitle ?? "");
  const [description, setDescription] = useState(initial?.description ?? "");
  const [tags, setTags] = useState<string[]>(initial?.tags ?? []);
  const [madeForKids, setMadeForKids] = useState<boolean>(
    initial?.madeForKids ?? false,
  );
  const [submitError, setSubmitError] = useState<string | null>(null);

  // Auto-pick when the LoadState lands with a unique default. The
  // user's choice is preserved on subsequent re-renders (the
  // local workspaceId/channelId wins until reset).
  useEffect(() => {
    if (state.kind !== "ready") return;
    if (workspaceId == null && state.defaultWorkspaceId != null) {
      setWorkspaceId(state.defaultWorkspaceId);
    }
    if (channelId == null && state.defaultChannelId != null) {
      setChannelId(state.defaultChannelId);
    }
  }, [state, workspaceId, channelId]);

  const canSubmit =
    state.kind === "ready" &&
    workspaceId != null &&
    channelId != null &&
    ytTitle.trim().length > 0;

  const handleSubmit = async (e: React.FormEvent): Promise<void> => {
    e.preventDefault();
    if (!canSubmit) return;
    setSubmitError(null);

    try {
      onComplete({
        workspaceId: workspaceId!,
        channelId: channelId!,
        ytTitle: ytTitle.trim(),
        description: description.trim(),
        tags,
        madeForKids,
      });
    } catch (err) {
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      setSubmitError(
        err instanceof Error ? err.message : "Salvataggio non riuscito.",
      );
    }
  };

  return (
    <form
      onSubmit={(e) => void handleSubmit(e)}
      className="rounded-2xl border border-white/[0.08] bg-[#0d0d14]/80 backdrop-blur p-6 md:p-8 shadow-[0_0_0_1px_rgba(255,255,255,0.02)]"
      data-testid="channel-metadata-step"
      noValidate
    >
      <div className="mb-2 flex items-center gap-2">
        <ProviderBadge
          platform="youtube"
          className="h-7 rounded-md border-0"
          compact
          logoClassName="h-6 w-6"
          showName
        />
        <h2 className="text-xl font-semibold text-white">
          Step 2 — Canale + Metadati YouTube
        </h2>
      </div>
      <p className="text-sm text-[#9aa0aa] mb-6">
        Seleziona il canale YouTube di destinazione e i metadati
        iniziali. La privacy è fissata a{" "}
        <code className="px-1 rounded bg-white/[0.06] text-emerald-300 font-mono text-xs">
          private
        </code>
        : la visibilità finale la imposterai dopo la prima
        pubblicazione tramite InstaEditor.
      </p>

      <LoadBody
        state={state}
        onRetry={() => void refetch()}
        onLinkChannel={() => navigate("/app/linking")}
      >
        <div className="space-y-5">
          {/* Selettore area di lavoro + canale YouTube */}
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <Field label="Area di lavoro" htmlFor="workspace-select">
              <select
                id="workspace-select"
                value={workspaceId ?? ""}
                onChange={(e) => {
                  const v = e.target.value;
                  setWorkspaceId(v === "" ? null : Number(v));
                }}
                className={selectCls}
                data-testid="workspace-select"
                disabled={state.kind !== "ready"}
              >
                <option value="">— seleziona —</option>
                {state.kind === "ready" &&
                  state.workspaces.map((w) => (
                    <option key={w.id} value={w.id}>
                      {w.name}
                    </option>
                  ))}
              </select>
            </Field>

            <Field label="Canale YouTube" htmlFor="channel-select">
              <select
                id="channel-select"
                value={channelId ?? ""}
                onChange={(e) => {
                  const v = e.target.value;
                  setChannelId(v === "" ? null : Number(v));
                }}
                className={selectCls}
                data-testid="channel-select"
                disabled={state.kind !== "ready"}
              >
                <option value="">— seleziona —</option>
                {state.kind === "ready" &&
                  state.channels.map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.username}
                      {c.status !== "connected"
                        ? ` · ${c.status}`
                        : ""}
                    </option>
                  ))}
              </select>
            </Field>
          </div>

          {/* Titolo YouTube */}
          <Field
            label="Titolo YouTube"
            htmlFor="yt-title-input"
            counter={`${ytTitle.length}/${YT_TITLE_MAX}`}
          >
            <input
              id="yt-title-input"
              type="text"
              value={ytTitle}
              onChange={(e) => setYtTitle(e.target.value.slice(0, YT_TITLE_MAX))}
              maxLength={YT_TITLE_MAX}
              placeholder="es. Promo estate 2026"
              className={inputCls}
              data-testid="yt-title-input"
            />
          </Field>

          {/* Descrizione */}
          <Field
            label="Descrizione"
            htmlFor="yt-description-input"
            counter={`${description.length}/${YT_DESCRIPTION_MAX}`}
            optional
          >
            <textarea
              id="yt-description-input"
              value={description}
              onChange={(e) =>
                setDescription(e.target.value.slice(0, YT_DESCRIPTION_MAX))
              }
              maxLength={YT_DESCRIPTION_MAX}
              placeholder="Aggiungi una descrizione… (supporta link e hashtag)"
              rows={4}
              className={cn(
                inputCls,
                "resize-y min-h-[96px] py-2 leading-relaxed",
              )}
              data-testid="yt-description-input"
            />
          </Field>

          {/* Tag */}
          <Field
            label="Tag"
            htmlFor="tag-draft"
            counter={`${tags.length}/${YT_TAGS_MAX}`}
            hint="Premi Invio o virgola per aggiungere."
            optional
          >
            <TagInput
              value={tags}
              onChange={setTags}
              maxTags={YT_TAGS_MAX}
              placeholder="es. tutorial, vlog, estate2026"
              testIdPrefix="yt-tag"
            />
          </Field>

          {/* Made for kids */}
          <div
            className="flex items-center justify-between gap-4 rounded-xl bg-[#0a0a10] border border-white/[0.12] px-4 py-3"
            data-testid="made-for-kids-row"
          >
            <div>
              <div className="text-sm font-medium text-white">
                Realizzato per bambini
              </div>
              <p className="text-xs text-[#9aa0aa] mt-0.5 max-w-md">
                Obbligatorio per la conformità YouTube COPPA. Una
                volta attivato, YouTube disabilita commenti e
                notifiche.
              </p>
            </div>
            <Toggle
              checked={madeForKids}
              onChange={setMadeForKids}
              label="Realizzato per bambini"
              testId="made-for-kids-toggle"
            />
          </div>

          {/* Privacy chip locked */}
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
                Fissato dal flusso di pubblicazione. Potrai
                modificare la visibilità in seguito da InstaEditor
                (unlisted / public).
              </div>
            </div>
            <code
              className="px-2 py-1 rounded-md bg-white/[0.06] text-emerald-300 font-mono text-xs shrink-0"
              data-testid="privacy-code"
            >
              privacy_status: "{YOUTUBE_PRIVACY_LOCKED}"
            </code>
          </div>

          {/* Submit error */}
          {submitError && (
            <div
              className="rounded-xl border border-red-500/30 bg-red-500/[0.06] px-4 py-3 text-sm text-red-200"
              role="alert"
              data-testid="submit-error"
            >
              {submitError}
            </div>
          )}
        </div>
      </LoadBody>

      {/* Action row */}
      <div className="mt-6 flex items-center justify-between gap-3">
        <button
          type="button"
          onClick={onBack}
          className="inline-flex items-center gap-1.5 text-sm text-[#9aa0aa] hover:text-white transition-colors"
          data-testid="back-button"
        >
          <ArrowLeft size={14} aria-hidden="true" />
          Indietro
        </button>
        <button
          type="submit"
          disabled={!canSubmit}
          className="inline-flex items-center gap-2 rounded-xl bg-white text-[#030308] px-4 py-2.5 text-sm font-semibold hover:bg-[#e8ecf2] disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
          data-testid="next-button"
        >
          Avanti
          <ChevronRight size={16} aria-hidden="true" />
        </button>
      </div>
    </form>
  );
}

// ─────────────────────────────────────────────────────────────────
// Helpers (kept local — not exported; if a future step reuses any
// of these, promote them to a shared form module.
// ─────────────────────────────────────────────────────────────────

const selectCls = cn(
  "w-full rounded-xl bg-[#0a0a10] border border-white/[0.12] px-3.5 py-2.5 text-white",
  "focus:outline-none focus:ring-2 focus:ring-white/30 focus:border-white/30",
  "transition-colors appearance-none cursor-pointer",
);

const inputCls = cn(
  "w-full rounded-xl bg-[#0a0a10] border border-white/[0.12] px-3.5 py-2.5",
  "text-white placeholder:text-[#5c6473]",
  "focus:outline-none focus:ring-2 focus:ring-white/30 focus:border-white/30",
  "transition-colors",
);

function Field({
  label,
  htmlFor,
  counter,
  hint,
  optional,
  children,
}: {
  label: string;
  htmlFor: string;
  counter?: string;
  hint?: string;
  optional?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div>
      <div className="flex items-center justify-between mb-1.5">
        <label
          htmlFor={htmlFor}
          className="block text-sm font-medium text-[#cdd2da]"
        >
          {label}
          {optional && (
            <span className="ml-1.5 text-xs font-normal text-[#5c6473]">
              opzionale
            </span>
          )}
        </label>
        {counter && (
          <span className="text-xs text-[#5c6473] tabular-nums">
            {counter}
          </span>
        )}
      </div>
      {children}
      {hint && (
        <p className="text-xs text-[#5c6473] mt-1.5">{hint}</p>
      )}
    </div>
  );
}

function Toggle({
  checked,
  onChange,
  label,
  testId,
}: {
  checked: boolean;
  onChange: (next: boolean) => void;
  label: string;
  testId: string;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      onClick={() => onChange(!checked)}
      className={cn(
        "relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors focus:outline-none focus:ring-2 focus:ring-white/30",
        checked ? "bg-emerald-500/80" : "bg-white/[0.12]",
      )}
      data-testid={testId}
    >
      <span
        className={cn(
          "inline-block h-4 w-4 transform rounded-full bg-white shadow-sm transition-transform",
          checked ? "translate-x-6" : "translate-x-1",
        )}
      />
    </button>
  );
}

/**
 * Wraps the form body with a loading/error/empty state shell.
 * Kept inline so future step components can copy/paste the shape
 * (mirrors the useUploads.ts 'loadState' convention).
 */
function LoadBody({
  state,
  onRetry,
  onLinkChannel,
  children,
}: {
  state: ChannelsLoadState;
  onRetry: () => void;
  onLinkChannel: () => void;
  children: React.ReactNode;
}) {
  if (state.kind === "loading") {
    return (
      <div className="space-y-4" data-testid="loading-skeleton">
        <Skeleton variant="card" height={48} />
        <Skeleton variant="card" height={48} />
        <Skeleton variant="card" height={48} />
      </div>
    );
  }
  if (state.kind === "error") {
    return (
      <ErrorState
        title="Impossibile caricare i canali"
        message={state.message}
        onRetry={onRetry}
        retryLabel="Riprova"
      />
    );
  }
  if (state.channels.length === 0) {
    return (
      <EmptyState
        title="Nessun canale YouTube collegato"
        description="Collega almeno un canale YouTube per procedere con la pubblicazione."
        cta={
          <button
            type="button"
            onClick={onLinkChannel}
            className="inline-flex items-center gap-2 rounded-xl bg-white text-[#030308] px-4 py-2 text-sm font-semibold hover:bg-[#e8ecf2] transition-colors"
            data-testid="link-channel-cta"
          >
            Vai a Linking
          </button>
        }
      />
    );
  }
  return <>{children}</>;
}
