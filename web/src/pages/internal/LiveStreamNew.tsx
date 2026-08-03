import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { ArrowLeft, ChevronRight, Radio } from "lucide-react";
import { toastBus } from "../../components/toast/toast-bus";
import { useLivestreamChannels } from "./useLivestreamChannels";
import { LiveStreamWizardStep1 } from "./livestreamWizardStep1";
import { LiveStreamWizardStep2 } from "./livestreamWizardStep2";
import { LiveStreamWizardStep3 } from "./livestreamWizardStep3";
import {
  EMPTY_STEP2_FORM,
  type LivestreamStep2Form,
} from "./livestreamsTypes";

/**
 * Creation wizard — container page at /app/livestreams/new.
 *
 * Lifts the shared wizard state out of the step components so back →
 * forward preserves every entry (same pattern as ContentNew):
 *   - step        ∈ {1, 2, 3}  currently visible step
 *   - selectedID            channel picked in step 1
 *   - form                  step-2 metadata (titolo, privacy, …)
 *   - thumbnailPreview      object URL of the uploaded cover (kept in
 *                           the page so returning from step 1 still
 *                           renders the image)
 *   - mediaIds              ordered Media Library selection from step 3
 *                           (order = future playlist order)
 *
 * Step 1 (Canale) asks for the workspace's YouTube channel with the
 * OAuth/live-scope preflight. Step 2 (Configurazione YouTube) collects
 * the broadcast metadata. Step 3 (Contenuti) picks compatible videos
 * from the Media Library. Steps 4–5 (riproduzione → riepilogo) land in
 * the next module increments; for now step 3's "Continua" announces
 * the pending step.
 */
export function LiveStreamNewPage() {
  const { state, reload } = useLivestreamChannels();
  const [step, setStep] = useState<1 | 2 | 3>(1);
  const [selectedID, setSelectedID] = useState<number | null>(null);
  const [form, setForm] = useState<LivestreamStep2Form>(EMPTY_STEP2_FORM);
  const [thumbnailPreview, setThumbnailPreview] = useState<string | null>(null);
  const [mediaIds, setMediaIds] = useState<string[]>([]);

  const channels = state.kind === "ready" ? state.channels : [];
  const selected = channels.find((channel) => channel.platform_account_id === selectedID) ?? null;

  // Release the cover's blob URL when leaving the wizard — otherwise
  // the object stays pinned in memory until the document unloads.
  useEffect(() => {
    return () => {
      if (thumbnailPreview) URL.revokeObjectURL(thumbnailPreview);
    };
  }, [thumbnailPreview]);

  return (
    <div className="min-h-full bg-[#030308] p-8 text-[#e8e8ef]">
      <div className="mx-auto max-w-3xl">
        <Link
          to="/app/livestreams"
          className="inline-flex items-center gap-1.5 text-[13px] font-medium text-[#9aa0aa] no-underline hover:text-white transition-colors"
          data-testid="livestream-new-back"
        >
          <ArrowLeft size={14} aria-hidden="true" />
          Live streaming
        </Link>

        <header className="mt-4 flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
          <div>
            <h1 className="flex items-center gap-3 text-[28px] font-extrabold tracking-[-0.02em] text-white">
              <Radio size={26} className="text-violet-300" aria-hidden="true" />
              Crea nuova live
            </h1>
            <p className="mt-1 text-[15px] text-[#9aa0aa]">
              {step === 1
                ? "Passaggio 1 di 5 — scegli il canale YouTube che trasmetterà."
                : step === 2
                  ? "Passaggio 2 di 5 — configura metadati, copertina e opzioni di trasmissione."
                  : "Passaggio 3 di 5 — scegli i video che andranno in onda dalla Media Library."}
            </p>
          </div>
          {step === 1 && (
            <span
              className="inline-flex items-center gap-1.5 self-start rounded-lg border border-white/[0.08] bg-white/[0.04] px-2.5 py-1.5 text-[11px] font-semibold text-[#cdd2da]"
              data-testid="livestream-new-step-badge-1"
            >
              1 di 5
              <ChevronRight size={12} className="text-white/30" aria-hidden="true" />
              <span className="text-[#9aa0aa]">Canale</span>
            </span>
          )}
          {step === 2 && (
            <span
              className="inline-flex items-center gap-1.5 self-start rounded-lg border border-violet-500/25 bg-violet-500/[0.08] px-2.5 py-1.5 text-[11px] font-semibold text-violet-200"
              data-testid="livestream-new-step-badge-2"
            >
              2 di 5
              <ChevronRight size={12} className="text-violet-300/50" aria-hidden="true" />
              <span className="text-violet-200/70">Configurazione YouTube</span>
            </span>
          )}
          {step === 3 && (
            <span
              className="inline-flex items-center gap-1.5 self-start rounded-lg border border-violet-500/25 bg-violet-500/[0.08] px-2.5 py-1.5 text-[11px] font-semibold text-violet-200"
              data-testid="livestream-new-step-badge-3"
            >
              3 di 5
              <ChevronRight size={12} className="text-violet-300/50" aria-hidden="true" />
              <span className="text-violet-200/70">Contenuti</span>
            </span>
          )}
        </header>

        {step === 1 && (
          <LiveStreamWizardStep1
            state={state}
            reload={reload}
            channels={channels}
            selectedID={selectedID}
            onSelect={setSelectedID}
            onContinue={() => setStep(2)}
          />
        )}

        {step === 2 && selected && (
          <LiveStreamWizardStep2
            channelName={selected.username}
            form={form}
            onChange={setForm}
            thumbnailPreview={thumbnailPreview}
            onThumbnailPreviewChange={setThumbnailPreview}
            onBack={() => setStep(1)}
            onContinue={() => setStep(3)}
          />
        )}

        {step === 3 && selected && (
          <LiveStreamWizardStep3
            selectedIds={mediaIds}
            onSelectionChange={setMediaIds}
            onBack={() => setStep(2)}
            onContinue={() => {
              // Steps 4–5 (riproduzione → riepilogo) land in the next
              // module increments.
              toastBus.push("info", "Passaggio 4 (Riproduzione e programmazione) in arrivo.");
            }}
          />
        )}
      </div>
    </div>
  );
}
