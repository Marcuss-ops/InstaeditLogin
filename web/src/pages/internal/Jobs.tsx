import { useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import {
  Bot,
  CalendarClock,
  Check,
  ChevronRight,
  CircleDashed,
  Clock3,
  Loader2,
  Pause,
  Play,
  Plus,
  RefreshCw,
  Sparkles,
  Video,
  XCircle,
} from "lucide-react";
import { ApiError, AuthError, authedFetch } from "../../lib/auth";
import {
  cancelVideoJob,
  isVideoJobActive,
  isVideoJobFailed,
  type VideoJob,
  VIDEO_PIPELINE_STAGES,
  useVideoJobs,
  videoJobStageIndex,
  videoJobStageLabel,
} from "./videoJobs";

type ChannelOption = {
  type: "channel";
  platform_account_id: number;
  channel_id: string;
  channel_name: string;
  is_publishable: boolean;
};

type FormState = {
  title: string;
  channelId: string;
  scheduleAt: string;
  quantity: number;
  autoPublish: boolean;
};

const initialForm: FormState = {
  title: "Nuovo video AI",
  channelId: "",
  scheduleAt: "",
  quantity: 1,
  autoPublish: true,
};

function formatDate(value: string): string {
  if (!value) return "—";
  return new Intl.DateTimeFormat("it-IT", {
    day: "2-digit",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}

function jobStatus(job: VideoJob): { label: string; className: string } {
  if (isVideoJobFailed(job)) return { label: "Richiede attenzione", className: "app-job-status-failed" };
  if (isVideoJobActive(job)) return { label: "In lavorazione", className: "app-job-status-active" };
  return { label: "Completato", className: "app-job-status-done" };
}

function PipelineOverview({ jobs }: { jobs: VideoJob[] }) {
  const current = jobs.length === 0
    ? -1
    : Math.max(...jobs.filter(isVideoJobActive).map(videoJobStageIndex), -1);

  return (
    <section className="app-card rounded-2xl p-5 sm:p-6">
      <div className="flex flex-col gap-1 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <p className="app-eyebrow">Autopilot</p>
          <h2 className="app-card-title mt-1 text-lg font-semibold">Pipeline video completa</h2>
        </div>
        <p className="app-card-muted text-xs">Esecuzione autonoma, stato aggiornato ogni 5 secondi</p>
      </div>
      <div className="mt-6 grid gap-2 sm:grid-cols-6">
        {VIDEO_PIPELINE_STAGES.map((stage, index) => {
          const isDone = current > index;
          const isCurrent = current === index;
          return (
            <div key={stage} className="relative flex min-w-0 gap-2 sm:block">
              {index < VIDEO_PIPELINE_STAGES.length - 1 && (
                <div className={`absolute left-3 top-7 hidden h-px w-[calc(100%+0.5rem)] sm:block ${isDone ? "app-pipeline-line-done" : "app-pipeline-line"}`} />
              )}
              <div className={`app-pipeline-node relative z-10 ${isDone ? "is-done" : isCurrent ? "is-current" : ""}`}>
                {isDone ? <Check size={14} /> : isCurrent ? <Loader2 size={14} className="animate-spin" /> : <span>{index + 1}</span>}
              </div>
              <div className="pt-0.5 sm:mt-3 sm:pt-0">
                <p className="app-card-title text-xs font-medium leading-4">{stage}</p>
                <p className="app-card-muted mt-1 text-[11px]">{isDone ? "Pronto" : isCurrent ? "In corso" : "In coda"}</p>
              </div>
            </div>
          );
        })}
      </div>
    </section>
  );
}

function JobRow({ job, onCancel }: { job: VideoJob; onCancel: (job: VideoJob) => void }) {
  const status = jobStatus(job);
  const stageIndex = videoJobStageIndex(job);
  return (
    <article id={job.id} className="app-card rounded-2xl p-4 sm:p-5">
      <div className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className={`app-job-status ${status.className}`}><CircleDashed size={12} />{status.label}</span>
            <span className="app-card-muted truncate text-xs">{job.id}</span>
          </div>
          <h3 className="app-card-title mt-2 truncate text-sm font-semibold">Video AI · {job.project_id || job.id}</h3>
          <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
            <span className="app-card-muted inline-flex items-center gap-1.5"><Clock3 size={13} /> {job.publish_at ? `Pubblicazione ${formatDate(job.publish_at)}` : `Aggiornato ${formatDate(job.updated_at)}`}</span>
            <span className="app-card-muted inline-flex items-center gap-1.5"><Bot size={13} /> {videoJobStageLabel(job)}</span>
          </div>
        </div>
        <div className="flex items-center gap-3">
          <div className="hidden min-w-40 sm:block">
            <div className="flex items-center justify-between text-[11px]">
              <span className="app-card-muted">Avanzamento</span>
              <span className="app-card-title font-semibold">{Math.min(stageIndex + 1, VIDEO_PIPELINE_STAGES.length)}/{VIDEO_PIPELINE_STAGES.length}</span>
            </div>
            <div className="app-progress-track mt-2"><div className="app-progress-value" style={{ width: `${((stageIndex + 1) / VIDEO_PIPELINE_STAGES.length) * 100}%` }} /></div>
          </div>
          {isVideoJobActive(job) && (
            <button type="button" onClick={() => onCancel(job)} className="app-quiet-button" title="Metti in pausa il job">
              <Pause size={14} /> <span className="hidden sm:inline">Ferma</span>
            </button>
          )}
        </div>
      </div>
    </article>
  );
}

function CreateJobPanel({ onCreated }: { onCreated: () => void }) {
  const [open, setOpen] = useState(false);
  const [form, setForm] = useState<FormState>(initialForm);
  const [channels, setChannels] = useState<ChannelOption[]>([]);
  const [loadingChannels, setLoadingChannels] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  const loadChannels = useCallback(async () => {
    setLoadingChannels(true);
    try {
      const response = await authedFetch("/api/v1/publishing/targets");
      const data = (await response.json()) as { channels?: ChannelOption[] };
      const available = (data.channels ?? []).filter((channel) => channel.is_publishable !== false);
      setChannels(available);
      setForm((current) => ({ ...current, channelId: current.channelId || String(available[0]?.platform_account_id ?? "") }));
    } catch (requestError) {
      if (!(requestError instanceof AuthError)) {
        setError(requestError instanceof ApiError ? requestError.message : "Impossibile caricare i canali.");
      }
    } finally {
      setLoadingChannels(false);
    }
  }, []);

  useEffect(() => {
    if (open) void loadChannels();
  }, [loadChannels, open]);

  async function createJob(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSaving(true);
    setError("");
    const selected = channels.find((channel) => String(channel.platform_account_id) === form.channelId);
    if (!selected) {
      setError("Collega almeno un canale pubblicabile prima di creare un job.");
      setSaving(false);
      return;
    }
    try {
      const idempotency = typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : `${Date.now()}-${Math.random()}`;
      const payload = {
        contract_version: "velox.job.v1",
        idempotency_key: `instaedit:ui:${idempotency}`,
        job_type: "scene.composite.v1",
        template_id: "ai-video-pipeline.v1",
        template_version: 1,
        video_name: form.title.trim() || "Nuovo video AI",
        spec: {
          automation: { autonomous: true, no_supervision: true, batch_size: form.quantity, stages: [...VIDEO_PIPELINE_STAGES] },
          scenes: [{ id: "opening", text: form.title.trim() || "Nuovo video AI" }],
        },
        output: { width: 1080, height: 1920, fps: 30, format: "mp4" },
        ...(form.autoPublish && form.scheduleAt ? { publish_at: new Date(form.scheduleAt).toISOString() } : {}),
        target: { type: "channel", platform_account_id: selected.platform_account_id, channel_id: selected.channel_id, channel_name: selected.channel_name },
      };
      await authedFetch("/api/v1/jobs", { method: "POST", body: JSON.stringify(payload) });
      setForm(initialForm);
      setOpen(false);
      onCreated();
    } catch (requestError) {
      if (requestError instanceof AuthError) return;
      setError(requestError instanceof ApiError ? requestError.message : "Impossibile creare il job.");
    } finally {
      setSaving(false);
    }
  }

  if (!open) {
    return <button type="button" onClick={() => { setError(""); setOpen(true); }} className="app-primary-button"><Plus size={16} /> Nuovo job AI</button>;
  }

  return (
    <div className="app-modal-backdrop" role="presentation">
      <form onSubmit={createJob} className="app-modal app-card w-full max-w-xl rounded-3xl p-5 sm:p-7" aria-label="Crea job AI">
        <div className="flex items-start justify-between gap-4">
          <div><p className="app-eyebrow">Nuova automazione</p><h2 className="app-page-title mt-1 text-xl font-semibold">Crea video senza supervisione</h2><p className="app-card-muted mt-1 text-sm">Il job attraversa automaticamente tutti i sei stati della pipeline.</p></div>
          <button type="button" onClick={() => setOpen(false)} className="app-icon-button" aria-label="Chiudi">×</button>
        </div>
        <div className="mt-6 grid gap-4">
          <label className="app-field"><span>Titolo o tema</span><input value={form.title} onChange={(event) => setForm({ ...form, title: event.target.value })} placeholder="Es. 5 tool AI per creator" required /></label>
          <label className="app-field"><span>Canale di pubblicazione</span><select value={form.channelId} onChange={(event) => setForm({ ...form, channelId: event.target.value })} disabled={loadingChannels || channels.length === 0}><option value="">{loadingChannels ? "Caricamento…" : channels.length ? "Seleziona un canale" : "Nessun canale disponibile"}</option>{channels.map((channel) => <option key={channel.platform_account_id} value={channel.platform_account_id}>{channel.channel_name}</option>)}</select></label>
          <div className="grid gap-4 sm:grid-cols-2"><label className="app-field"><span>Prima esecuzione</span><input type="datetime-local" value={form.scheduleAt} onChange={(event) => setForm({ ...form, scheduleAt: event.target.value })} /></label><label className="app-field"><span>Video da preparare</span><input type="number" min={1} max={100} value={form.quantity} onChange={(event) => setForm({ ...form, quantity: Math.max(1, Math.min(100, Number(event.target.value) || 1)) })} /></label></div>
          <label className="app-switch-row"><span><strong>Pubblica automaticamente</strong><small>Quando il video finale e la copertina sono pronti</small></span><input type="checkbox" checked={form.autoPublish} onChange={(event) => setForm({ ...form, autoPublish: event.target.checked })} /></label>
        </div>
        {error && <p className="app-form-error mt-4">{error}</p>}
        <div className="mt-6 flex justify-end gap-2"><button type="button" onClick={() => setOpen(false)} className="app-quiet-button">Annulla</button><button type="submit" disabled={saving || loadingChannels || !form.channelId} className="app-primary-button">{saving ? <Loader2 size={15} className="animate-spin" /> : <Play size={15} />} Avvia pipeline</button></div>
      </form>
    </div>
  );
}

export function JobsPage() {
  const jobs = useVideoJobs();
  const [notice, setNotice] = useState("");
  const readyJobs = jobs.state.kind === "ready" ? jobs.state.jobs : [];
  const scheduled = useMemo(() => readyJobs.filter((job) => job.publish_at), [readyJobs]);

  async function stopJob(job: VideoJob) {
    try { await cancelVideoJob(job.id); setNotice("Job messo in pausa."); await jobs.load(); }
    catch (error) { setNotice(error instanceof Error ? error.message : "Impossibile fermare il job."); }
  }

  return (
    <div className="app-page min-h-full p-5 sm:p-7 lg:p-9">
      <div className="mx-auto max-w-7xl">
        <header className="flex flex-col gap-5 lg:flex-row lg:items-end lg:justify-between">
          <div><p className="app-eyebrow">Content automation</p><h1 className="app-page-title mt-2 text-3xl font-semibold tracking-tight">AI Video Jobs</h1><p className="app-page-subtitle mt-2 max-w-2xl text-sm">Crea, programma e pubblica video in autonomia. La coda continua a lavorare anche quando non sei qui.</p></div>
          <div className="flex flex-wrap items-center gap-2"><Link to="/app/calendar" className="app-quiet-button no-underline"><CalendarClock size={15} /> Calendario</Link><CreateJobPanel onCreated={() => void jobs.load()} /></div>
        </header>

        {notice && <div className="app-notice mt-5" role="status">{notice}</div>}
        <div className="mt-7 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
          {[{ label: "In lavorazione", value: jobs.activeJobs.length, icon: Loader2 }, { label: "Programmati", value: scheduled.length, icon: CalendarClock }, { label: "Completati", value: jobs.completedJobs.length, icon: Check }, { label: "Capacità giornaliera", value: "100", icon: Sparkles }].map(({ label, value, icon: Icon }) => <div key={label} className="app-kpi-card rounded-2xl p-4"><div className="flex items-center justify-between"><span className="app-kpi-label text-xs">{label}</span><span className="app-kpi-icon inline-flex rounded-lg p-2"><Icon size={15} /></span></div><p className="app-kpi-value mt-3 text-2xl font-semibold">{value}</p></div>)}
        </div>
        <div className="mt-5"><PipelineOverview jobs={readyJobs} /></div>

        <section className="mt-7">
          <div className="mb-3 flex items-center justify-between"><div><h2 className="app-card-title text-lg font-semibold">Coda di produzione</h2><p className="app-card-muted mt-1 text-xs">Ogni job viene aggiornato automaticamente dalla pipeline.</p></div><button type="button" className="app-quiet-button" onClick={() => void jobs.load()}><RefreshCw size={14} /> Aggiorna</button></div>
          {jobs.state.kind === "loading" && <div className="app-card rounded-2xl p-8 text-center"><Loader2 className="mx-auto animate-spin" size={22} /><p className="app-card-muted mt-3 text-sm">Caricamento job…</p></div>}
          {jobs.state.kind === "error" && <div className="app-card rounded-2xl p-8 text-center"><XCircle className="mx-auto" size={24} /><p className="app-card-title mt-3 text-sm font-medium">Non riesco a leggere la coda</p><p className="app-card-muted mt-1 text-sm">{jobs.state.message}</p><button type="button" onClick={() => void jobs.load()} className="app-quiet-button mt-4"><RefreshCw size={14} /> Riprova</button></div>}
          {jobs.state.kind === "ready" && readyJobs.length === 0 && <div className="app-card rounded-2xl p-10 text-center"><Video className="app-card-muted mx-auto" size={28} /><p className="app-card-title mt-3 text-sm font-medium">La coda è vuota</p><p className="app-card-muted mt-1 text-sm">Crea il primo job e programma una produzione continua fino a 100 video al giorno.</p><div className="mt-4"><CreateJobPanel onCreated={() => void jobs.load()} /></div></div>}
          {jobs.state.kind === "ready" && readyJobs.length > 0 && <div className="grid gap-3">{readyJobs.map((job) => <JobRow key={job.id} job={job} onCancel={stopJob} />)}</div>}
        </section>
        <div className="mt-6 flex items-center gap-2 text-xs app-card-muted"><ChevronRight size={14} /><span>Gli slot programmati sono visibili anche nel <Link to="/app/calendar" className="app-inline-link">calendario editoriale</Link>.</span></div>
      </div>
    </div>
  );
}
