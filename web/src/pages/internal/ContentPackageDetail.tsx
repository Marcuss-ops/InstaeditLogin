import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { AlertTriangle, ArrowLeft, CalendarClock, CheckCircle2, Film, Loader2 } from "lucide-react";
import {
  getContentPackage,
  getContentPackageActivity,
  getContentPackagePreview,
  generateContentPackageTranslations,
  scheduleContentPackage,
  type ContentPackageResponse,
  type ContentPreview,
  type PublicationEvent,
} from "../../features/content/contentPackagesApi";

function localDateValue() {
  const date = new Date(Date.now() + 24 * 60 * 60 * 1000);
  date.setMinutes(0, 0, 0);
  return date.toISOString().slice(0, 16);
}

export function ContentPackageDetail() {
  const { packageId = "" } = useParams<{ packageId: string }>();
  const [data, setData] = useState<ContentPackageResponse | null>(null);
  const [preview, setPreview] = useState<ContentPreview | null>(null);
  const [events, setEvents] = useState<PublicationEvent[]>([]);
  const [scheduledAt, setScheduledAt] = useState(localDateValue);
  const [busy, setBusy] = useState(true);
  const [scheduling, setScheduling] = useState(false);
  const [translating, setTranslating] = useState(false);
  const [error, setError] = useState("");

  async function load() {
    setBusy(true);
    setError("");
    try {
      const [packageData, previewData, activityData] = await Promise.all([
        getContentPackage(packageId),
        getContentPackagePreview(packageId),
        getContentPackageActivity(packageId),
      ]);
      setData(packageData);
      setPreview(previewData);
      setEvents(activityData.events ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Impossibile caricare il contenuto");
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    void load();
  }, [packageId]);

  async function schedule() {
    if (!data || !preview) return;
    setScheduling(true);
    setError("");
    try {
      await scheduleContentPackage(packageId, {
        expected_package_version: preview.package_version,
        scheduled_at: new Date(scheduledAt).toISOString(),
        timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC",
      });
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Programmazione non riuscita");
    } finally {
      setScheduling(false);
    }
  }

  async function generateTranslations() {
    if (!preview) return;
    setTranslating(true);
    setError("");
    try {
      await generateContentPackageTranslations(packageId, preview.package_version);
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Generazione traduzioni non riuscita");
    } finally {
      setTranslating(false);
    }
  }

  if (busy) {
    return <div className="p-8 text-white"><Loader2 className="animate-spin" /></div>;
  }
  if (!data || !preview) {
    return <div className="p-8 text-red-300">{error || "Contenuto non trovato"}</div>;
  }

  return (
    <div className="min-h-full bg-[#030308] px-4 py-8 text-[#e8e8ef] md:px-8">
      <div className="mx-auto max-w-6xl">
        <Link to="/app/content/inbox" className="mb-6 inline-flex items-center gap-2 text-sm text-[#9aa0aa] no-underline hover:text-white">
          <ArrowLeft size={16} /> Torna a Drive Inbox
        </Link>
        <div className="mb-8 flex flex-wrap items-start justify-between gap-4">
          <div>
            <p className="mb-2 flex items-center gap-2 text-xs uppercase tracking-[0.2em] text-[#858b99]"><Film size={15} /> Content package</p>
            <h1 className="text-3xl font-bold text-white">{data.package.source_filename || `Contenuto #${data.package.id}`}</h1>
            <p className="mt-2 text-sm text-[#9aa0aa]">Versione {preview.package_version} · stato {data.package.state}</p>
          </div>
          <div className={`rounded-full border px-3 py-1 text-sm ${preview.ready ? "border-emerald-400/30 bg-emerald-400/10 text-emerald-300" : "border-amber-400/30 bg-amber-400/10 text-amber-200"}`}>
            {preview.ready ? "Pronto per la pubblicazione" : "Richiede attenzione"}
          </div>
        </div>

        {error && <div className="mb-6 rounded-xl border border-red-400/30 bg-red-400/10 p-3 text-sm text-red-200">{error}</div>}
        {preview.blockers.length > 0 && (
          <div className="mb-6 rounded-2xl border border-amber-400/20 bg-amber-400/[0.06] p-4 text-sm text-amber-100">
            <div className="mb-2 flex items-center gap-2 font-semibold"><AlertTriangle size={16} /> Blocchi</div>
            {preview.blockers.map((blocker) => <div key={`${blocker.code}-${blocker.message}`}>{blocker.message}</div>)}
            {preview.blockers.some((blocker) => blocker.code === "translation_missing") && <button onClick={() => void generateTranslations()} disabled={translating} className="mt-3 inline-flex items-center gap-2 rounded-lg bg-amber-200 px-3 py-2 text-xs font-semibold text-black disabled:opacity-50">{translating && <Loader2 size={14} className="animate-spin" />} Genera traduzioni NVIDIA</button>}
          </div>
        )}

        <div className="grid gap-5 lg:grid-cols-[1fr_320px]">
          <section className="space-y-4">
            {preview.targets.map((target) => (
              <article key={target.platform_account_id} className="rounded-2xl border border-white/[0.09] bg-white/[0.03] p-5">
                <div className="mb-4 flex items-center justify-between gap-3">
                  <div><h2 className="font-semibold text-white">{target.channel_name || `Canale #${target.platform_account_id}`}</h2><p className="text-xs uppercase text-[#858b99]">{target.language} · {target.privacy_status}</p></div>
                  {target.ready ? <CheckCircle2 className="text-emerald-300" size={19} /> : <AlertTriangle className="text-amber-300" size={19} />}
                </div>
                <h3 className="text-lg font-medium text-white">{target.title || "Titolo mancante"}</h3>
                <p className="mt-2 whitespace-pre-wrap text-sm leading-6 text-[#aeb3be]">{target.description || "Descrizione vuota"}</p>
                {target.blockers.length > 0 && <p className="mt-3 text-xs text-amber-200">{target.blockers.map((b) => b.message).join(" · ")}</p>}
              </article>
            ))}
          </section>

          <aside className="h-fit rounded-2xl border border-white/[0.09] bg-white/[0.03] p-5">
            <h2 className="mb-4 flex items-center gap-2 font-semibold text-white"><CalendarClock size={18} /> Programmazione</h2>
            {data.schedule ? <p className="mb-4 text-sm text-emerald-200">{new Date(data.schedule.scheduled_at).toLocaleString()}<br /><span className="text-xs text-[#858b99]">{data.schedule.status}</span></p> : <p className="mb-4 text-sm text-[#9aa0aa]">Nessuna schedulazione salvata. Il salvataggio non esegue upload immediati.</p>}
            {!data.schedule && <><label className="mb-2 block text-xs text-[#9aa0aa]" htmlFor="content-schedule-at">Data e ora</label><input id="content-schedule-at" type="datetime-local" value={scheduledAt} onChange={(event) => setScheduledAt(event.target.value)} className="mb-3 w-full rounded-xl border border-white/10 bg-black/20 px-3 py-2 text-sm text-white" /><button disabled={!preview.ready || scheduling} onClick={() => void schedule()} className="inline-flex w-full items-center justify-center gap-2 rounded-xl bg-white px-4 py-2 text-sm font-semibold text-black disabled:cursor-not-allowed disabled:opacity-40">{scheduling && <Loader2 size={15} className="animate-spin" />} Programma</button></>}
          </aside>
        </div>

        {data.publications && data.publications.length > 0 && (
          <section className="mt-6 rounded-2xl border border-white/[0.09] bg-white/[0.03] p-5">
            <div className="mb-4 flex items-center justify-between gap-3"><h2 className="font-semibold text-white">Stato per canale</h2><span className="text-xs text-[#858b99]">{data.publications.filter((item) => item.target_status === "published" || item.published_at).length}/{data.publications.length} pubblicati</span></div>
            <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">{data.publications.map((publication) => { const status = publication.published_at ? "published" : publication.target_status || publication.upload_job_status || "waiting"; return <div key={`${publication.content_schedule_id}-${publication.target_account_id}`} className="rounded-xl border border-white/[0.08] bg-black/20 p-3"><div className="flex items-center justify-between gap-3"><span className="text-sm font-medium text-white">Account #{publication.target_account_id}</span><span className={`rounded-full px-2 py-1 text-[10px] font-semibold uppercase tracking-wide ${status === "published" ? "bg-emerald-400/10 text-emerald-300" : status === "failed" || status === "blocked_auth" ? "bg-red-400/10 text-red-300" : "bg-sky-400/10 text-sky-200"}`}>{status}</span></div><p className="mt-2 text-xs text-[#858b99]">{publication.language} · {publication.title || "Titolo non disponibile"}</p>{publication.youtube_video_id && <p className="mt-1 truncate text-xs text-[#606777]">YouTube: {publication.youtube_video_id}</p>}</div>; })}</div>
          </section>
        )}

        <section className="mt-6 rounded-2xl border border-white/[0.09] bg-white/[0.03] p-5">
          <h2 className="mb-4 font-semibold text-white">Attività</h2>
          <div className="space-y-3">{events.length === 0 ? <p className="text-sm text-[#858b99]">Nessun evento ancora.</p> : events.map((event) => <div key={event.id} className="flex items-center justify-between gap-4 text-sm"><span className="text-[#d4d7df]">{event.event_type}</span><span className="text-xs text-[#858b99]">{new Date(event.occurred_at).toLocaleString()}</span></div>)}</div>
        </section>
      </div>
    </div>
  );
}
