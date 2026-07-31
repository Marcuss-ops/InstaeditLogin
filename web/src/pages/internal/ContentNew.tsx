/**
 * ContentNew — page mounted at /app/content/new.
 *
 * Wizard container that lifts the shared publish-state out of the
 * step components (so Step 3 can read the consolidated payload
 * without prop-drilling or a global store).
 *
 * Layout (lives inside `InternalLayout`'s Outlet):
 *   ┌────────────────────────────────────────────────┐
 *   │  Back-link to dashboard                         │
 *   │  Page heading                                   │
 *   │  StepIndicator (3 steps, current=stepState)     │
 *   │  ┌──────────────────────────────────────┐       │
 *   │  │ Step component                       │       │
 *   │  └──────────────────────────────────────┘       │
 *   │  Debug snippet with the lifted state shape      │
 *   └────────────────────────────────────────────────┘
 *
 * State model:
 *   step           ∈ {1, 2, 3}    current visible step
 *   asset          MediaAsset | null  from Step 1 (useUploadMedia)
 *   internalTitle  string            collected in Step 1
 *   youtubeTarget  ChannelMetadata | null  from Step 2 — Step 3
 *                                          reads this when POSTing
 *                                          /api/v1/posts
 *
 * Lifecycle:
 *   - Back → forward preserves each step's user-entered state
 *     (Step 1 reads `initialTitle`, Step 2 reads `initial`). Going
 *     completely forward+back N times does NOT clear the state.
 *   - Sidebar / browser back resets Step 1 (the useUploadMedia
 *     hook unmounts → state cleared). Steps 2/3 lifted values
 *     survive a same-mount navigation but reset on full page reload.
 */
import { useState } from "react";
import { Link } from "react-router-dom";
import { ArrowLeft } from "lucide-react";
import { StepIndicator } from "../../components/wizard/StepIndicator";
import { VideoUploadStep } from "../../features/publishing/wizard/VideoUploadStep";
import { ChannelMetadataStep } from "../../features/publishing/wizard/ChannelMetadataStep";
import { ConfirmationStep } from "../../features/publishing/wizard/ConfirmationStep";
import type { MediaAsset } from "../../features/publishing/api/mediaApi";
import type { ChannelMetadata } from "../../features/publishing/wizard/ChannelMetadataStep";

const STEPS = [
  { label: "Video" },
  { label: "Canale + Metadati" },
  { label: "Conferma" },
] as const;

export function ContentNew() {
  const [step, setStep] = useState<1 | 2 | 3>(1);
  const [asset, setAsset] = useState<MediaAsset | null>(null);
  const [internalTitle, setInternalTitle] = useState("");
  const [fileName, setFileName] = useState<string | undefined>();
  const [fileSize, setFileSize] = useState<number | undefined>();
  const [youtubeTarget, setYoutubeTarget] = useState<ChannelMetadata | null>(
    null,
  );


  return (
    <div
      className="px-4 md:px-8 py-8 max-w-3xl mx-auto"
      data-testid="content-new-page"
    >
      <Link
        to="/app/dashboard"
        className="inline-flex items-center gap-2 text-sm text-[#9aa0aa] hover:text-white mb-6 no-underline transition-colors"
      >
        <ArrowLeft size={16} aria-hidden="true" />
        Torna alla dashboard
      </Link>

      <h1 className="text-2xl md:text-3xl font-bold text-white mb-1">
        Nuovo contenuto
      </h1>
      <p className="text-sm text-[#9aa0aa] mb-8">
        Carica il video, scegli il canale YouTube e pubblica.
        Per il vertical slice la privacy iniziale è forzata su{" "}
        <code className="px-1 rounded bg-white/[0.06] text-emerald-300 font-mono text-xs">
          private
        </code>{" "}
        — il Dark Editor applicherà la copertina e la visibilità finale.
      </p>

      <div className="mb-8">
        <StepIndicator steps={[...STEPS]} currentStep={step} />
      </div>

      {step === 1 && (
        <VideoUploadStep
          initialTitle={internalTitle}
          onComplete={(next, title, fileInfo) => {
            setAsset(next);
            // MediaAsset intentionally contains server-controlled
            // metadata only; retain local filename/size for the
            // confirmation summary.
            setInternalTitle(title);
            setFileName(fileInfo?.name);
            setFileSize(fileInfo?.size);
            setStep(2);
          }}
        />
      )}

      {step === 2 && (
        <ChannelMetadataStep
          initial={youtubeTarget}
          onComplete={(meta) => {
            setYoutubeTarget(meta);
            setStep(3);
          }}
          onBack={() => setStep(1)}
        />
      )}

      {step === 3 && asset && youtubeTarget && (
        <ConfirmationStep
          asset={asset}
          internalTitle={internalTitle}
          fileName={fileName}
          fileSize={fileSize}
          channel={youtubeTarget}
          onBack={() => setStep(2)}
          onJumpToStep={(jumpTo) => setStep(jumpTo)}
        />
      )}

      {/* Internal dev snippet — confirms the lifted state shape
          across commits (read-only, no controls). Adds `target`
          payload below the asset line for back-compat visibility. */}
      {(asset || youtubeTarget) && (
        <p
          className="mt-6 text-xs text-[#5c6473] font-mono break-all"
          data-testid="debug-summary"
        >
          [dev] asset_id={asset?.id ?? "—"} · title=
          {JSON.stringify(internalTitle)} · step=
          {step}
          {youtubeTarget && (
            <>
              {" "}
              · target: workspace=
              {youtubeTarget.workspaceId} channel=
              {youtubeTarget.channelId} channelTitle=
              {JSON.stringify(youtubeTarget.ytTitle)} tags=
              {youtubeTarget.tags.length}
            </>
          )}
        </p>
      )}
    </div>
  );
}

export default ContentNew;
