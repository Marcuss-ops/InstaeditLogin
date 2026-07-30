/**
 * ContentNew — page mounted at /app/content/new.
 *
 * Wizard container that lifts the shared publish-state out of the
 * step components (so Step 2 can read the asset id and title that
 * Step 1 produced without prop-drilling or a global store). Today
 * only Step 1 is functional; Steps 2/3 are placeholders that will
 * be implemented in subsequent vertical-slice commits.
 *
 * Layout (lives inside `InternalLayout`'s Outlet):
 *   ┌────────────────────────────────────────────────┐
 *   │  Page heading                                  │
 *   │  StepIndicator (3 steps, current=stepState)    │
 *   │  ┌──────────────────────────────────────┐      │
 *   │  │ Step component (Slide in/out later)  │      │
 *   │  └──────────────────────────────────────┘      │
 *   │  Footer note on private-by-default             │
 *   └────────────────────────────────────────────────┘
 *
 * State model:
 *   - `step` ∈ {1, 2, 3} — which step is currently visible
 *   - `asset` — MediaAsset returned by Step 1's useUploadMedia
 *   - `internalTitle` — free-form title collected in Step 1
 *
 * The wizard is intentionally linear: forward-only navigation
 * inside the slice; Escape-hatches (browser back, Sidebar nav)
 * re-mount Step 1 with cleared state.
 */
import { useState } from "react";
import { Link } from "react-router-dom";
import { ArrowLeft } from "lucide-react";
import { StepIndicator } from "../../components/wizard/StepIndicator";
import { VideoUploadStep } from "../../features/publishing/wizard/VideoUploadStep";
import type { MediaAsset } from "../../features/publishing/api/mediaApi";

const STEPS = [
  { label: "Video" },
  { label: "Canale + Metadati" },
  { label: "Conferma" },
] as const;

/**
 * Placeholder shown for Steps 2 + 3. Stays in place until the
 * follow-up commits land. The vertical-slice plan keeps these
 * trivial so we can review the wizard seams before filling them.
 */
function PlaceholderStep({
  number,
  label,
  body,
}: {
  number: 2 | 3;
  label: string;
  body: string;
}) {
  return (
    <div
      className="rounded-2xl border border-white/[0.08] bg-[#0d0d14]/80 backdrop-blur p-6 md:p-8"
      data-testid={`placeholder-step-${number}`}
    >
      <h2 className="text-xl font-semibold text-white mb-1">
        Step {number} — {label}
      </h2>
      <p className="text-sm text-[#9aa0aa]">{body}</p>
    </div>
  );
}

export function ContentNew() {
  const [step, setStep] = useState<1 | 2 | 3>(1);
  const [asset, setAsset] = useState<MediaAsset | null>(null);
  const [internalTitle, setInternalTitle] = useState("");

  return (
    <div className="px-4 md:px-8 py-8 max-w-3xl mx-auto" data-testid="content-new-page">
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
          onComplete={(next, title) => {
            setAsset(next);
            setInternalTitle(title);
            setStep(2);
          }}
        />
      )}

      {step === 2 && (
        <PlaceholderStep
          number={2}
          label="Canale + Metadati"
          body="Step 2 (selezione canale YouTube + titolo/descrizione/tag) verrà implementato in un commit successivo del Blocco #1."
        />
      )}

      {step === 3 && (
        <PlaceholderStep
          number={3}
          label="Conferma"
          body="Step 3 (riepilogo + chiamata POST /api/v1/posts con Idempotency-Key) verrà implementato in un commit successivo."
        />
      )}

      {/* Internal dev snippet — confirms the lifted state shape
          for the next commit (read-only, no controls). */}
      {asset && (
        <p className="mt-6 text-xs text-[#5c6473] font-mono" data-testid="debug-summary">
          [dev] asset_id={asset.id} · title={JSON.stringify(internalTitle)} ·
          step={step}
        </p>
      )}
    </div>
  );
}

export default ContentNew;
