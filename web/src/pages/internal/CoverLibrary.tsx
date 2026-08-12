import { useEffect, useMemo, useState, type FormEvent } from "react";
import { FileImage, Layers3, Loader2, Plus, RefreshCw, Sparkles } from "lucide-react";
import { authedFetch, fetchSession } from "../../lib/auth";
import { safeAssetUrl } from "./groupYouTubeVideosVisual";
import {
  createCoverTemplate,
  listCoverLibrary,
  listCoverTemplateVersions,
  listCoverTemplates,
  type CoverLibraryItem,
  type CoverTemplate,
  type CoverTemplateVersion,
} from "../../features/content/coverLibraryApi";
import { EmptyState } from "../../components/feedback/EmptyState";

async function resolvePreviewURL(mediaID: string): Promise<string | undefined> {
  try {
    const response = await authedFetch(`/api/v1/media/${encodeURIComponent(mediaID)}`);
    const data = (await response.json()) as { preview_url?: string };
    return safeAssetUrl(data.preview_url);
  } catch {
    return undefined;
  }
}

function formatBytes(value: number) {
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`;
  return `${(value / (1024 * 1024)).toFixed(1)} MB`;
}

export function CoverLibraryPage() {
  const [workspaceId, setWorkspaceId] = useState<number | null>(null);
  const [tab, setTab] = useState<"covers" | "templates">("covers");
  const [covers, setCovers] = useState<CoverLibraryItem[]>([]);
  const [previewUrls, setPreviewUrls] = useState<Record<string, string | undefined>>({});
  const [templates, setTemplates] = useState<CoverTemplate[]>([]);
  const [versions, setVersions] = useState<Record<number, CoverTemplateVersion[]>>({});
  const [expandedTemplate, setExpandedTemplate] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [name, setName] = useState("");
  const [editorProjectId, setEditorProjectId] = useState("");
  const [language, setLanguage] = useState("");
  const [creating, setCreating] = useState(false);

  const load = async (id: number) => {
    setLoading(true);
    setError("");
    try {
      const [coverData, templateData] = await Promise.all([
        listCoverLibrary(id),
        listCoverTemplates(id),
      ]);
      setCovers(coverData.items ?? []);
      setTemplates(templateData.items ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Impossibile caricare la library.");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    let cancelled = false;
    void fetchSession().then((session) => {
      if (cancelled) return;
      const id = session?.workspaceId;
      if (id) {
        setWorkspaceId(id);
        void load(id);
      } else {
        setLoading(false);
        setError("Nessun workspace attivo disponibile.");
      }
    });
    return () => { cancelled = true; };
  }, []);

  const readyCovers = useMemo(() => covers.filter((cover) => cover.status === "ready"), [covers]);

  useEffect(() => {
    let cancelled = false;
    const mediaIDs = Array.from(new Set(readyCovers.map((cover) => cover.media_id).filter(Boolean)));
    void Promise.all(mediaIDs.map(async (mediaID) => [mediaID, await resolvePreviewURL(mediaID)] as const)).then((entries) => {
      if (!cancelled) setPreviewUrls(Object.fromEntries(entries));
    });
    return () => { cancelled = true; };
  }, [readyCovers]);

  async function toggleVersions(template: CoverTemplate) {
    if (!workspaceId) return;
    if (expandedTemplate === template.id) {
      setExpandedTemplate(null);
      return;
    }
    setExpandedTemplate(template.id);
    if (versions[template.id]) return;
    try {
      const data = await listCoverTemplateVersions(workspaceId, template.id);
      setVersions((current) => ({ ...current, [template.id]: data.items ?? [] }));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Impossibile caricare le versioni.");
    }
  }

  async function submitTemplate(event: FormEvent) {
    event.preventDefault();
    if (!workspaceId || !name.trim() || !editorProjectId.trim()) return;
    setCreating(true);
    try {
      const created = await createCoverTemplate({
        workspace_id: workspaceId,
        name: name.trim(),
        language: language.trim() || undefined,
        editor_project_id: editorProjectId.trim(),
        slots: { title: true, subtitle: true, person_image: true, background: true, logo: true, badge: true, language: true },
      });
      setTemplates((current) => [created.template, ...current]);
      setVersions((current) => ({ ...current, [created.template.id]: [created.version] }));
      setName("");
      setEditorProjectId("");
      setLanguage("");
      setShowCreate(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Impossibile creare il template.");
    } finally {
      setCreating(false);
    }
  }

  if (loading) {
    return <div className="flex min-h-full items-center justify-center bg-[#030308] text-white"><Loader2 className="animate-spin" /></div>;
  }

  return (
    <div className="min-h-full bg-[#030308] px-4 py-8 text-[#e8e8ef] sm:px-6 lg:px-10">
      <div className="mx-auto max-w-[1500px]">
        <div className="mb-8 flex flex-wrap items-start justify-between gap-4">
          <div>
            <div className="mb-2 flex items-center gap-2 text-xs font-bold uppercase tracking-[0.22em] text-violet-300"><Sparkles size={14} /> Design system</div>
            <h1 className="text-3xl font-black tracking-tight text-white sm:text-4xl">Copertine Library</h1>
            <p className="mt-2 max-w-2xl text-sm leading-6 text-[#9aa0aa]">Asset esportati persistenti e template versionati. Una revisione già usata da un package resta immutabile.</p>
          </div>
          <button type="button" onClick={() => workspaceId && void load(workspaceId)} className="inline-flex items-center gap-2 rounded-xl border border-white/[0.10] bg-white/[0.04] px-3.5 py-2 text-xs font-bold text-white hover:bg-white/[0.09]"><RefreshCw size={14} /> Aggiorna</button>
        </div>

        {error && <div className="mb-5 rounded-xl border border-red-400/25 bg-red-400/[0.08] px-4 py-3 text-sm text-red-200">{error}</div>}

        <div className="mb-6 flex items-center justify-between gap-3 border-b border-white/[0.08]">
          <div className="flex gap-1">
            <button type="button" onClick={() => setTab("covers")} className={`inline-flex items-center gap-2 border-b-2 px-3 py-3 text-sm font-bold ${tab === "covers" ? "border-violet-400 text-white" : "border-transparent text-[#858b99] hover:text-white"}`}><FileImage size={16} /> Cover Library <span className="rounded-full bg-white/[0.08] px-2 py-0.5 text-[10px]">{readyCovers.length}</span></button>
            <button type="button" onClick={() => setTab("templates")} className={`inline-flex items-center gap-2 border-b-2 px-3 py-3 text-sm font-bold ${tab === "templates" ? "border-violet-400 text-white" : "border-transparent text-[#858b99] hover:text-white"}`}><Layers3 size={16} /> Template Library <span className="rounded-full bg-white/[0.08] px-2 py-0.5 text-[10px]">{templates.length}</span></button>
          </div>
          {tab === "templates" && <button type="button" onClick={() => setShowCreate((value) => !value)} className="mb-2 inline-flex items-center gap-2 rounded-xl bg-violet-500 px-3 py-2 text-xs font-bold text-white hover:bg-violet-400"><Plus size={14} /> Nuovo template</button>}
        </div>

        {tab === "covers" && (
          readyCovers.length === 0 ? <EmptyState title="Nessuna cover esportata" description="Le cover pronte compariranno qui dopo un export idempotente." icon={<FileImage size={24} />} className="border-white/[0.08] bg-white/[0.02]" /> :
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
            {readyCovers.map((cover) => (
              <article key={cover.export_id} className="overflow-hidden rounded-2xl border border-white/[0.09] bg-white/[0.035] transition-all hover:-translate-y-0.5 hover:border-violet-400/35 hover:bg-white/[0.055]">
                <div className="flex aspect-video items-center justify-center overflow-hidden bg-gradient-to-br from-violet-500/20 via-slate-900 to-fuchsia-500/10">
                  {previewUrls[cover.media_id] ? <img src={previewUrls[cover.media_id]} alt={`Preview ${cover.project_name}`} className="h-full w-full object-cover" loading="lazy" /> : <FileImage size={34} className="text-white/25" />}
                </div>
                <div className="p-4"><div className="flex items-start justify-between gap-3"><div><h2 className="truncate text-sm font-bold text-white">{cover.project_name}</h2><p className="mt-1 font-mono text-[10px] text-[#858b99]">{cover.export_id}</p></div><span className="rounded-full border border-emerald-400/20 bg-emerald-400/10 px-2 py-1 text-[10px] font-bold text-emerald-300">READY</span></div><div className="mt-4 grid grid-cols-2 gap-2 text-[11px] text-[#9aa0aa]"><span>{cover.width}×{cover.height}</span><span className="text-right">{formatBytes(cover.file_size)}</span></div><p className="mt-2 truncate font-mono text-[10px] text-[#606777]" title={cover.sha256}>sha256 {cover.sha256.slice(0, 16)}…</p></div>
              </article>
            ))}
          </div>
        )}

        {tab === "templates" && (
          <>
            {showCreate && <form onSubmit={submitTemplate} className="mb-6 grid gap-3 rounded-2xl border border-violet-400/20 bg-violet-400/[0.06] p-5 md:grid-cols-3"><input value={name} onChange={(event) => setName(event.target.value)} placeholder="Nome template" className="rounded-xl border border-white/10 bg-black/20 px-3 py-2.5 text-sm text-white outline-none focus:border-violet-400/60" /><input value={editorProjectId} onChange={(event) => setEditorProjectId(event.target.value)} placeholder="InstaEditor project ID" className="rounded-xl border border-white/10 bg-black/20 px-3 py-2.5 text-sm text-white outline-none focus:border-violet-400/60" /><div className="flex gap-2"><input value={language} onChange={(event) => setLanguage(event.target.value)} placeholder="Lingua (es. it)" className="min-w-0 flex-1 rounded-xl border border-white/10 bg-black/20 px-3 py-2.5 text-sm text-white outline-none focus:border-violet-400/60" /><button disabled={creating} className="rounded-xl bg-white px-4 py-2 text-xs font-bold text-black disabled:opacity-50">{creating ? "…" : "Crea v1"}</button></div></form>}
            {templates.length === 0 ? <EmptyState title="Nessun template" description="Crea il primo template collegando un progetto InstaEditor." icon={<Layers3 size={24} />} className="border-white/[0.08] bg-white/[0.02]" /> : <div className="space-y-3">{templates.map((template) => <article key={template.id} className="rounded-2xl border border-white/[0.09] bg-white/[0.035] p-4"><button type="button" onClick={() => void toggleVersions(template)} className="flex w-full items-center justify-between gap-4 text-left"><div><div className="flex items-center gap-2"><h2 className="font-bold text-white">{template.name}</h2><span className={`rounded-full px-2 py-0.5 text-[10px] font-bold ${template.status === "active" ? "bg-emerald-400/10 text-emerald-300" : "bg-white/[0.08] text-[#9aa0aa]"}`}>{template.status}</span></div><p className="mt-1 text-xs text-[#858b99]">{template.language || "Tutte le lingue"} · versione corrente v{template.current_version_number}</p></div><span className="text-xs font-bold text-violet-300">{expandedTemplate === template.id ? "Nascondi" : "Mostra versioni"}</span></button>{expandedTemplate === template.id && <div className="mt-4 border-t border-white/[0.08] pt-4">{(versions[template.id] ?? []).map((version) => <div key={version.id} className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-white/[0.07] bg-black/20 px-3 py-3"><div><span className="font-mono text-xs font-bold text-white">v{version.version_number}</span><span className="ml-3 text-xs text-[#9aa0aa]">InstaEditor: {version.editor_project_id}</span></div><span className="text-[10px] text-[#606777]">{new Date(version.created_at).toLocaleDateString()}</span></div>)}{(versions[template.id] ?? []).length === 0 && <p className="text-xs text-[#858b99]">Nessuna versione disponibile.</p>}</div>}</article>)}</div>}
          </>
        )}
      </div>
    </div>
  );
}
