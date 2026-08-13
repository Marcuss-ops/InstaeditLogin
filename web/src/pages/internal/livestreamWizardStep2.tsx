import { useRef, useState, type ReactNode } from "react";
import {
  ArrowLeft,
  ArrowRight,
  CheckCircle2,
  FolderOpen,
  ImagePlus,
  Loader2,
  Sparkles,
  UploadCloud,
  X,
} from "lucide-react";
import { ApiError, AuthError } from "../../lib/auth";
import { cn } from "../../lib/utils";
import { toastBus } from "../../components/toast/toast-bus";
import { uploadMediaAsset } from "../../features/publishing/api/mediaApi";
import type { MediaAssetContentType } from "../../features/publishing/api/types";
import {
  LATENCY_OPTIONS,
  LIVESTREAM_LANGUAGES,
  type LivestreamStep2Form,
} from "./livestreamsTypes";
import { YOUTUBE_CATEGORIES } from "../../features/youtube/api/categoriesApi";
import { categoryLabel, languageLabel, latencyLabel, privacyLabel } from "./livestreamsVisual";

const TITLE_MAX = 100;
const DESCRIPTION_MAX = 5000;

/** Content types the cover upload accepts (server allowlist subset). */
const IMAGE_TYPES: MediaAssetContentType[] = ["image/png", "image/jpeg", "image/webp"];

/**
 * Wizard step 2 of 5 (Configurazione YouTube) — broadcast metadata
 * form:
 *   titolo / descrizione, privacy (privata / non in elenco / pubblica),
 *   categoria, destinato ai bambini, lingua, copertina (upload / Media
 *   Library / InstaEditor), attiva DVR, auto-start, auto-stop e
 *   latenza.
 *
 * The cover's "Carica immagine" source is fully wired to the existing
 * presign → S3 → /complete pipeline (client-computed SHA-256). The
 * Media Library picker and the InstaEditor source land with later
 * releases and are shown as disabled tabs with an honest explanation.
 *
 * All state is lifted to the page; this component only reports back
 * through `onChange` so back → forward preserves the entries.
 */
export function LiveStreamWizardStep2({
  channelName,
  form,
  onChange,
  thumbnailPreview,
  onThumbnailPreviewChange,
  onBack,
  onContinue,
}: {
  channelName: string;
  form: LivestreamStep2Form;
  onChange: (next: LivestreamStep2Form) => void;
  thumbnailPreview: string | null;
  onThumbnailPreviewChange: (preview: string | null) => void;
  onBack: () => void;
  onContinue: () => void;
}) {
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [coverSource, setCoverSource] = useState<"upload" | "library" | "dark">("upload");
  const [uploading, setUploading] = useState(false);

  const canContinue = form.title.trim().length > 0;

  const patch = (next: Partial<LivestreamStep2Form>) => onChange({ ...form, ...next });

  const handleCoverFile = async (file: File | undefined) => {
    if (!file) return;
    if (!IMAGE_TYPES.includes(file.type as MediaAssetContentType)) {
      toastBus.push("error", "Formato non supportato: usa PNG, JPEG o WebP.");
      return;
    }
    setUploading(true);
    try {
      const sha = await sha256Hex(file);
      const asset = await uploadMediaAsset(file, {
        contentType: file.type as MediaAssetContentType,
        sha256: sha,
      });
      if (thumbnailPreview) URL.revokeObjectURL(thumbnailPreview);
      onThumbnailPreviewChange(URL.createObjectURL(file));
      patch({ thumbnailMediaId: asset.id });
      toastBus.push("success", "Copertina caricata.");
    } catch (err) {
      if (err instanceof AuthError) {
        toastBus.push("error", "Sessione scaduta. Accedi di nuovo.");
        return;
      }
      const message = err instanceof ApiError ? err.message : "Caricamento copertina non riuscito.";
      toastBus.push("error", message);
    } finally {
      setUploading(false);
      if (fileInputRef.current) fileInputRef.current.value = "";
    }
  };

  const clearCover = () => {
    if (thumbnailPreview) URL.revokeObjectURL(thumbnailPreview);
    onThumbnailPreviewChange(null);
    patch({ thumbnailMediaId: null });
  };

  return (
    <section className="mt-8" data-testid="livestream-new-step2">
      <div className="grid items-start gap-5 lg:grid-cols-2">
      {/* Titolo + Descrizione */}
      <Card title="Identità della trasmissione">
        <Field label="Titolo" htmlFor="ls-title" required counter={`${form.title.length}/${TITLE_MAX}`}>
          <input
            id="ls-title"
            type="text"
            value={form.title}
            maxLength={TITLE_MAX}
            onChange={(e) => patch({ title: e.target.value })}
            placeholder="es. WWE News 24/7"
            className={inputCls}
            data-testid="ls-step2-title"
          />
        </Field>
        <Field label="Descrizione" htmlFor="ls-description" optional counter={`${form.description.length}/${DESCRIPTION_MAX}`}>
          <textarea
            id="ls-description"
            value={form.description}
            maxLength={DESCRIPTION_MAX}
            rows={4}
            onChange={(e) => patch({ description: e.target.value })}
            placeholder="Aggiungi una descrizione della live…"
            className={cn(inputCls, "resize-y min-h-[96px] py-2 leading-relaxed")}
            data-testid="ls-step2-description"
          />
        </Field>
      </Card>

      {/* Privacy */}
      <Card title="Impostazioni YouTube">
        <div>
          <div className="mb-1.5 block text-[13px] font-medium text-[#cdd2da]">
            Privacy
          </div>
          <div className="grid grid-cols-3 gap-2" role="radiogroup" aria-label="Privacy della live" data-testid="ls-step2-privacy">
            {(["private", "unlisted", "public"] as const).map((value) => (
              <button
                key={value}
                type="button"
                role="radio"
                aria-checked={form.privacy === value}
                onClick={() => patch({ privacy: value })}
                className={cn(
                  "rounded-xl border px-3 py-2.5 text-[13px] font-semibold transition-colors",
                  form.privacy === value
                    ? "border-violet-400/50 bg-violet-500/[0.12] text-violet-100"
                    : "border-white/[0.10] bg-white/[0.03] text-[#9aa0aa] hover:border-white/25 hover:text-white",
                )}
                data-testid={`ls-step2-privacy-${value}`}
              >
                {privacyLabel(value)}
              </button>
            ))}
          </div>
          <p className="mt-1.5 text-[11px] text-[#5c6473]">
            La privacy scelta qui viene applicata al broadcast al momento della preparazione.
          </p>
        </div>

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Field label="Categoria" htmlFor="ls-category" optional>
            <select
              id="ls-category"
              value={form.category}
              onChange={(e) => patch({ category: e.target.value })}
              className={selectCls}
              data-testid="ls-step2-category"
            >
              <option value="">Nessuna categoria</option>
              {YOUTUBE_CATEGORIES.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.label}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Lingua" htmlFor="ls-language" optional>
            <select
              id="ls-language"
              value={form.language}
              onChange={(e) => patch({ language: e.target.value })}
              className={selectCls}
              data-testid="ls-step2-language"
            >
              <option value="">Non impostata</option>
              {LIVESTREAM_LANGUAGES.map((l) => (
                <option key={l.code} value={l.code}>
                  {l.label}
                </option>
              ))}
            </select>
          </Field>
        </div>

        <ToggleRow
          label="Destinato ai bambini"
          hint="Obbligatorio per la conformità YouTube COPPA. Una volta attivato, YouTube disabilita commenti e notifiche."
          checked={form.madeForKids}
          onChange={(v) => patch({ madeForKids: v })}
          testId="ls-step2-kids"
        />
      </Card>

      {/* Copertina */}
      <Card title="Copertina">
        <div className="mb-3 grid grid-cols-3 gap-2" role="tablist" aria-label="Origine della copertina">
          {(
            [
              { id: "upload", label: "Carica immagine", icon: UploadCloud },
              { id: "library", label: "Media Library", icon: FolderOpen },
              { id: "dark", label: "InstaEditor", icon: Sparkles },
            ] as const
          ).map(({ id, label, icon: Icon }) => (
            <button
              key={id}
              type="button"
              role="tab"
              id={`ls-cover-tab-${id}`}
              aria-selected={coverSource === id}
              aria-controls={`ls-cover-panel-${id}`}
              onClick={() => setCoverSource(id)}
              className={cn(
                "inline-flex items-center justify-center gap-1.5 rounded-xl border px-2 py-2 text-[12px] font-semibold transition-colors",
                coverSource === id
                  ? "border-violet-400/50 bg-violet-500/[0.10] text-violet-100"
                  : "border-white/[0.10] bg-white/[0.03] text-[#9aa0aa] hover:border-white/25 hover:text-white",
              )}
              data-testid={`ls-step2-cover-source-${id}`}
            >
              <Icon size={13} aria-hidden="true" />
              {label}
            </button>
          ))}
        </div>

        {coverSource === "upload" && (
          <div className="space-y-3" role="tabpanel" id="ls-cover-panel-upload" aria-labelledby="ls-cover-tab-upload">
            {form.thumbnailMediaId && thumbnailPreview ? (
              <div className="relative overflow-hidden rounded-2xl border border-white/[0.10]">
                <img
                  src={thumbnailPreview}
                  alt="Anteprima copertina"
                  className="h-44 w-full object-cover"
                  data-testid="ls-step2-cover-preview"
                />
                <button
                  type="button"
                  onClick={clearCover}
                  className="absolute right-2 top-2 inline-flex items-center gap-1 rounded-lg bg-black/70 px-2.5 py-1.5 text-[11px] font-semibold text-white backdrop-blur transition-colors hover:bg-black/90"
                  data-testid="ls-step2-cover-remove"
                >
                  <X size={12} aria-hidden="true" />
                  Rimuovi
                </button>
                <span className="absolute bottom-2 left-2 inline-flex items-center gap-1 rounded-lg bg-black/70 px-2 py-1 text-[10px] font-medium text-emerald-300 backdrop-blur">
                  <CheckCircle2 size={11} aria-hidden="true" />
                  Copertina pronta
                </span>
              </div>
            ) : (
              <button
                type="button"
                onClick={() => fileInputRef.current?.click()}
                disabled={uploading}
                className="flex h-44 w-full flex-col items-center justify-center gap-2 rounded-2xl border border-dashed border-white/[0.15] bg-white/[0.02] text-[#9aa0aa] transition-colors hover:border-violet-400/40 hover:text-white disabled:cursor-not-allowed disabled:opacity-60"
                data-testid="ls-step2-cover-upload"
              >
                {uploading ? (
                  <>
                    <Loader2 size={22} className="animate-spin text-violet-300" aria-hidden="true" />
                    <span className="text-[12px] font-medium">Caricamento in corso…</span>
                  </>
                ) : (
                  <>
                    <ImagePlus size={22} aria-hidden="true" />
                    <span className="text-[12px] font-medium">Scegli un&apos;immagine (PNG, JPEG, WebP)</span>
                  </>
                )}
              </button>
            )}
            <input
              ref={fileInputRef}
              type="file"
              accept="image/png,image/jpeg,image/webp"
              className="hidden"
              onChange={(e) => void handleCoverFile(e.target.files?.[0])}
              data-testid="ls-step2-cover-file"
            />
          </div>
        )}

        {coverSource === "library" && (
          <div className="rounded-2xl border border-white/[0.10] bg-white/[0.02] p-4" role="tabpanel" id="ls-cover-panel-library" aria-labelledby="ls-cover-tab-library" data-testid="ls-step2-cover-library">
            <p className="text-[13px] font-semibold text-white">Scegli dalla Media Library</p>
            <p className="mt-1 text-[12px] leading-relaxed text-[#9aa0aa]">
              Il selettore della Media Library arriva con la prossima release. Nel frattempo puoi
              caricare la copertina dalla scheda{" "}
              <span className="text-[#cdd2da]">Carica immagine</span> o gestire i tuoi file dalla
              pagina Caricamenti.
            </p>
          </div>
        )}

        {coverSource === "dark" && (
          <div className="rounded-2xl border border-white/[0.10] bg-white/[0.02] p-4" role="tabpanel" id="ls-cover-panel-dark" aria-labelledby="ls-cover-tab-dark" data-testid="ls-step2-cover-dark">
            <p className="text-[13px] font-semibold text-white">Crea in InstaEditor</p>
            <p className="mt-1 text-[12px] leading-relaxed text-[#9aa0aa]">
              InstaEditor modifica la copertina di un video YouTube già esistente: arriva nel
              secondo rilascio delle live, quando il flusso di pubblicazione lo supporterà.
            </p>
          </div>
        )}
      </Card>

      {/* Contenuti broadcast: advanced controls stay collapsed so the main
          live setup remains compact while the existing settings remain available. */}
      <details className="rounded-2xl border border-white/[0.08] bg-white/[0.025] p-5 lg:col-span-2">
        <summary className="cursor-pointer list-none text-[15px] font-bold text-white marker:hidden">Opzioni avanzate <span className="ml-2 text-[11px] font-normal text-[#7f8591]">DVR, auto-start, auto-stop e latenza</span></summary>
        <div className="mt-4 space-y-4">
        <ToggleRow
          label="Attiva DVR"
          hint="Permette agli spettatori di mettere in pausa e riavvolgere la live."
          checked={form.dvr}
          onChange={(v) => patch({ dvr: v })}
          testId="ls-step2-dvr"
        />
        <ToggleRow
          label="Auto-start"
          hint="Avvia automaticamente il broadcast quando l'encoder invia il primo frame."
          checked={form.autoStart}
          onChange={(v) => patch({ autoStart: v })}
          testId="ls-step2-auto-start"
        />
        <ToggleRow
          label="Auto-stop"
          hint="Termina automaticamente il broadcast quando l'encoder smette di inviare il segnale."
          checked={form.autoStop}
          onChange={(v) => patch({ autoStop: v })}
          testId="ls-step2-auto-stop"
        />
        <Field label="Latenza" htmlFor="ls-latency" optional>
          <select
            id="ls-latency"
            value={form.latency}
            onChange={(e) => patch({ latency: e.target.value as LivestreamStep2Form["latency"] })}
            className={selectCls}
            data-testid="ls-step2-latency"
          >
            {LATENCY_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label} — {o.description}
              </option>
            ))}
          </select>
        </Field>
        </div>
      </details>
      </div>

      {/* Action bar */}
      <div className="sticky bottom-4 mt-5 flex flex-col gap-3 rounded-2xl border border-white/[0.10] bg-[#14141e]/95 p-4 shadow-[0_-8px_40px_rgba(0,0,0,0.5)] backdrop-blur sm:flex-row sm:items-center sm:justify-between">
        <div className="min-w-0">
          <p className="text-[12px] text-[#9aa0aa]" data-testid="livestream-new-step2-hint">
            Canale: <span className="text-[#cdd2da]">{channelName}</span>
            {form.title.trim() ? "" : " · Inserisci un titolo per continuare."}
          </p>
          {(form.category || form.language || form.latency !== "normal") && (
            <div className="mt-1.5 flex flex-wrap gap-1.5 text-[10px] font-medium">
              {form.category && (
                <span className="rounded-md border border-white/[0.08] bg-white/[0.04] px-1.5 py-0.5 text-[#9aa0aa]">
                  Categoria: {categoryLabel(form.category)}
                </span>
              )}
              {form.language && (
                <span className="rounded-md border border-white/[0.08] bg-white/[0.04] px-1.5 py-0.5 text-[#9aa0aa]">
                  Lingua: {languageLabel(form.language)}
                </span>
              )}
              {form.latency !== "normal" && (
                <span className="rounded-md border border-white/[0.08] bg-white/[0.04] px-1.5 py-0.5 text-[#9aa0aa]">
                  Latenza: {latencyLabel(form.latency)}
                </span>
              )}
            </div>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <button
            type="button"
            onClick={onBack}
            className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.10] px-3.5 py-2 text-[12px] font-semibold text-[#cdd2da] transition-colors hover:bg-white/[0.06]"
            data-testid="livestream-new-step2-back"
          >
            <ArrowLeft size={14} aria-hidden="true" />
            Indietro
          </button>
          <button
            type="button"
            onClick={onContinue}
            disabled={!canContinue}
            title={canContinue ? undefined : "Inserisci il titolo della live"}
            className="inline-flex items-center gap-2 rounded-lg bg-violet-500 px-4 py-2 text-[12px] font-bold text-white transition-colors hover:bg-violet-400 disabled:cursor-not-allowed disabled:bg-white/[0.06] disabled:text-[#6b7280]"
            data-testid="livestream-new-continue"
          >
            Continua
            <ArrowRight size={14} aria-hidden="true" />
          </button>
        </div>
      </div>
    </section>
  );
}

// ─────────────────────────────────────────────────────────────────
// Local primitives (mirror the publishing wizard's form conventions).
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

function Card({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="space-y-4 rounded-2xl border border-white/[0.08] bg-white/[0.025] p-5">
      <h2 className="text-[15px] font-bold text-white">{title}</h2>
      {children}
    </div>
  );
}

function Field({
  label,
  htmlFor,
  required,
  optional,
  counter,
  children,
}: {
  label: string;
  htmlFor: string;
  required?: boolean;
  optional?: boolean;
  counter?: string;
  children: ReactNode;
}) {
  return (
    <div>
      <div className="mb-1.5 flex items-center justify-between">
        <label htmlFor={htmlFor} className="block text-[13px] font-medium text-[#cdd2da]">
          {label}
          {required && <span className="ml-1 text-red-400">*</span>}
          {optional && <span className="ml-1.5 text-[11px] font-normal text-[#5c6473]">opzionale</span>}
        </label>
        {counter && <span className="text-[11px] text-[#5c6473] tabular-nums">{counter}</span>}
      </div>
      {children}
    </div>
  );
}

function ToggleRow({
  label,
  hint,
  checked,
  onChange,
  testId,
}: {
  label: string;
  hint: string;
  checked: boolean;
  onChange: (next: boolean) => void;
  testId: string;
}) {
  return (
    <div className="flex items-center justify-between gap-4 rounded-xl border border-white/[0.10] bg-[#0a0a10] px-4 py-3" data-testid={testId}>
      <div>
        <div className="text-[13px] font-semibold text-white">{label}</div>
        <p className="mt-0.5 max-w-md text-[11px] text-[#9aa0aa]">{hint}</p>
      </div>
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
        data-testid={`${testId}-toggle`}
      >
        <span
          className={cn(
            "inline-block h-4 w-4 transform rounded-full bg-white shadow-sm transition-transform",
            checked ? "translate-x-6" : "translate-x-1",
          )}
        />
      </button>
    </div>
  );
}

/** Client-side SHA-256 hex digest required by /complete enforcement. */
async function sha256Hex(file: File | Blob): Promise<string> {
  if (typeof crypto === "undefined" || !crypto.subtle) {
    throw new Error("SubtleCrypto non disponibile — apri l'app su HTTPS o localhost.");
  }
  const buf = await file.arrayBuffer();
  const digest = await crypto.subtle.digest("SHA-256", buf);
  return Array.from(new Uint8Array(digest))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}
