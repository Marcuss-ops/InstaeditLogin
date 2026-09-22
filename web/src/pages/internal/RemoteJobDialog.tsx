import { useEffect, useMemo, useState } from "react";
import { AlertCircle, CheckCircle2, CircleDot, Loader2, Send, X } from "lucide-react";
import { authedFetch } from "../../lib/auth";

type JsonObject = Record<string, unknown>;

type RemoteJobDialogProps = {
  open: boolean;
  onClose: () => void;
};

const TERMINAL_STATUSES = new Set([
  "completed",
  "complete",
  "succeeded",
  "success",
  "failed",
  "error",
  "cancelled",
  "canceled",
]);

function asObject(value: unknown): JsonObject {
  return value && typeof value === "object" && !Array.isArray(value) ? value as JsonObject : {};
}

function getJobId(value: unknown): string {
  const object = asObject(value);
  const nested = asObject(object.job);
  for (const candidate of [object.job_id, object.id, nested.job_id, nested.id]) {
    if (typeof candidate === "string" && candidate.trim()) return candidate;
  }
  return "";
}

function getStatus(value: unknown): string {
  const object = asObject(value);
  const nested = asObject(object.job);
  for (const candidate of [object.status, object.state, object.overall_status, nested.status, nested.state]) {
    if (typeof candidate === "string" && candidate.trim()) return candidate;
  }
  return "queued";
}

function getProgress(value: unknown): string {
  const object = asObject(value);
  const progress = object.progress;
  if (typeof progress === "number") return `${Math.round(progress)}%`;
  if (typeof progress === "string") return progress;
  if (progress && typeof progress === "object") {
    const nested = asObject(progress);
    if (typeof nested.percent === "number") return `${Math.round(nested.percent)}%`;
    if (typeof nested.progress === "number") return `${Math.round(nested.progress)}%`;
  }
  return "";
}

function getTimeline(value: unknown): Array<{ label: string; status: string }> {
  const object = asObject(value);
  const raw = Array.isArray(object.timeline)
    ? object.timeline
    : Array.isArray(asObject(object.job).timeline) ? asObject(object.job).timeline as unknown[] : [];
  return raw.map((entry, index) => {
    const item = asObject(entry);
    const label = [item.label, item.name, item.stage, item.type]
      .find((candidate): candidate is string => typeof candidate === "string" && Boolean(candidate.trim()))
      ?? `Step ${index + 1}`;
    const status = [item.status, item.state]
      .find((candidate): candidate is string => typeof candidate === "string" && Boolean(candidate.trim()))
      ?? "queued";
    return { label, status };
  });
}

function normalizeTypes(value: unknown): string[] {
  const object = asObject(value);
  const list = Array.isArray(value) ? value : Array.isArray(object.types) ? object.types : [];
  return list
    .map((entry) => {
      if (typeof entry === "string") return entry;
      const item = asObject(entry);
      return typeof item.type === "string" ? item.type : typeof item.name === "string" ? item.name : "";
    })
    .map((entry) => entry.trim())
    .filter(Boolean);
}

async function responseJSON(response: Response): Promise<unknown> {
  const body = await response.json().catch(() => ({}));
  if (!response.ok) {
    const message = asObject(body).error;
    throw new Error(typeof message === "string" ? message : `Job master error (${response.status})`);
  }
  return body;
}

export function RemoteJobDialog({ open, onClose }: RemoteJobDialogProps) {
  const [types, setTypes] = useState<string[]>([]);
  const [type, setType] = useState("");
  const [project, setProject] = useState("");
  const [idempotencyKey, setIdempotencyKey] = useState(() => `calendar-${Date.now()}`);
  const [payload, setPayload] = useState("{}");
  const [job, setJob] = useState<unknown>(null);
  const [jobID, setJobID] = useState("");
  const [loadingTypes, setLoadingTypes] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!open) return;
    let active = true;
    setLoadingTypes(true);
    setError("");
    void authedFetch("/api/v1/automation/jobs/types")
      .then(responseJSON)
      .then((body) => {
        if (!active) return;
        const available = normalizeTypes(body);
        setTypes(available);
        setType((current) => current || available[0] || "");
      })
      .catch((err: unknown) => {
        if (active) setError(err instanceof Error ? err.message : "Impossibile leggere i job disponibili.");
      })
      .finally(() => {
        if (active) setLoadingTypes(false);
      });
    return () => { active = false; };
  }, [open]);

  useEffect(() => {
    if (!open || !jobID) return;
    let active = true;
    let timer: number | undefined;
    const poll = async () => {
      try {
        const body = await responseJSON(await authedFetch(`/api/v1/automation/jobs/${encodeURIComponent(jobID)}`));
        if (!active) return;
        setJob(body);
        if (!TERMINAL_STATUSES.has(getStatus(body).toLowerCase())) {
          timer = window.setTimeout(() => void poll(), 3000);
        }
      } catch (err) {
        if (active) setError(err instanceof Error ? err.message : "Polling del job fallito.");
      }
    };
    void poll();
    return () => {
      active = false;
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [jobID, open]);

  const timeline = useMemo(() => getTimeline(job), [job]);
  const status = getStatus(job);
  const progress = getProgress(job);
  const terminal = TERMINAL_STATUSES.has(status.toLowerCase());

  async function submit() {
    setError("");
    let parsedPayload: unknown;
    try {
      parsedPayload = JSON.parse(payload);
    } catch {
      setError("Il payload deve essere JSON valido.");
      return;
    }
    setSubmitting(true);
    try {
      const body = await responseJSON(await authedFetch("/api/v1/automation/jobs", {
        method: "POST",
        body: JSON.stringify({ type, project, idempotency_key: idempotencyKey, payload: parsedPayload }),
      }));
      setJob(body);
      setJobID(getJobId(body));
      if (!getJobId(body)) setError("Il Master non ha restituito un job_id.");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Invio del job fallito.");
    } finally {
      setSubmitting(false);
    }
  }

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/45 p-4 backdrop-blur-sm" role="dialog" aria-modal="true" aria-label="Invia job remoto">
      <div className="w-full max-w-2xl overflow-hidden rounded-3xl border border-white/[0.12] bg-[#1f1f1f] text-white shadow-[0_24px_80px_rgba(0,0,0,0.4)]">
        <div className="flex items-start justify-between border-b border-white/[0.08] px-6 py-5">
          <div>
            <p className="text-[11px] font-semibold uppercase tracking-[0.18em] text-white/45">Execution plane</p>
            <h2 className="mt-1 text-xl font-bold">Invia job remoto</h2>
            <p className="mt-1 text-sm text-white/50">Il Master seleziona il worker disponibile e aggiorna il risultato.</p>
          </div>
          <button type="button" onClick={onClose} className="rounded-xl p-2 text-white/50 hover:bg-white/[0.08] hover:text-white" aria-label="Chiudi"><X size={18} /></button>
        </div>

        <div className="grid gap-5 px-6 py-5 md:grid-cols-[minmax(0,1fr)_220px]">
          <div className="space-y-3">
            <label className="block text-xs font-semibold text-white/60">Tipo job
              <select value={type} onChange={(event) => setType(event.target.value)} disabled={loadingTypes || types.length === 0} className="mt-1.5 w-full rounded-xl border border-white/[0.12] bg-white/[0.06] px-3 py-2.5 text-sm text-white outline-none focus:border-white/30">
                {types.length === 0 && <option value="">{loadingTypes ? "Caricamento…" : "Nessun tipo disponibile"}</option>}
                {types.map((entry) => <option key={entry} value={entry}>{entry}</option>)}
              </select>
            </label>
            <label className="block text-xs font-semibold text-white/60">Progetto
              <input value={project} onChange={(event) => setProject(event.target.value)} placeholder="video-01" className="mt-1.5 w-full rounded-xl border border-white/[0.12] bg-white/[0.06] px-3 py-2.5 text-sm text-white outline-none placeholder:text-white/25 focus:border-white/30" />
            </label>
            <label className="block text-xs font-semibold text-white/60">Idempotency key
              <input value={idempotencyKey} onChange={(event) => setIdempotencyKey(event.target.value)} className="mt-1.5 w-full rounded-xl border border-white/[0.12] bg-white/[0.06] px-3 py-2.5 font-mono text-xs text-white outline-none focus:border-white/30" />
            </label>
            <label className="block text-xs font-semibold text-white/60">Payload JSON
              <textarea value={payload} onChange={(event) => setPayload(event.target.value)} rows={6} spellCheck={false} className="mt-1.5 w-full resize-y rounded-xl border border-white/[0.12] bg-black/20 px-3 py-2.5 font-mono text-xs leading-5 text-white outline-none focus:border-white/30" />
            </label>
            <button type="button" onClick={() => void submit()} disabled={submitting || !type || !project || !idempotencyKey} className="inline-flex items-center gap-2 rounded-xl bg-white px-4 py-2.5 text-sm font-semibold text-black transition-opacity hover:opacity-85 disabled:cursor-not-allowed disabled:opacity-40">
              {submitting ? <Loader2 size={16} className="animate-spin" /> : <Send size={16} />} Invia job
            </button>
          </div>

          <div className="rounded-2xl border border-white/[0.08] bg-white/[0.035] p-4">
            <div className="flex items-center justify-between gap-2">
              <p className="text-xs font-semibold text-white/55">Stato</p>
              {jobID && <span className="max-w-[130px] truncate font-mono text-[10px] text-white/35">{jobID}</span>}
            </div>
            {!job && <p className="mt-5 text-sm leading-6 text-white/40">Invia un job per iniziare il polling del Master.</p>}
            {job !== null && (
              <>
                <div className="mt-4 flex items-center gap-2">
                  {terminal ? <CheckCircle2 size={18} className={status.toLowerCase().includes("fail") || status.toLowerCase() === "error" ? "text-red-300" : "text-emerald-300"} /> : <CircleDot size={18} className="animate-pulse text-amber-300" />}
                  <span className="text-sm font-bold capitalize">{status}</span>
                  {progress && <span className="ml-auto text-xs text-white/50">{progress}</span>}
                </div>
                {timeline.length > 0 && <div className="mt-5 space-y-2 border-l border-white/[0.12] pl-3">{timeline.map((entry, index) => <div key={`${entry.label}-${index}`} className="relative text-xs"><span className="absolute -left-[18px] top-0.5 h-2 w-2 rounded-full bg-white/35" /><span className="text-white/75">{entry.label}</span><span className="ml-2 text-white/35">{entry.status}</span></div>)}</div>}
                <pre className="mt-5 max-h-44 overflow-auto rounded-xl bg-black/20 p-3 text-[10px] leading-4 text-white/45">{JSON.stringify(job, null, 2)}</pre>
              </>
            )}
          </div>
        </div>
        {error && <div className="mx-6 mb-5 flex items-start gap-2 rounded-xl border border-red-300/20 bg-red-300/[0.08] px-3 py-2.5 text-xs text-red-100"><AlertCircle size={15} className="mt-0.5 shrink-0" />{error}</div>}
      </div>
    </div>
  );
}
