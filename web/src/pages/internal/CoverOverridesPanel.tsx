import { useEffect, useMemo, useState } from "react";
import { FileImage, Layers3, Loader2, Save } from "lucide-react";
import {
  listCoverLibrary,
  listCoverTemplateVersions,
  listCoverTemplates,
  type CoverLibraryItem,
  type CoverTemplate,
  type CoverTemplateVersion,
} from "../../features/content/coverLibraryApi";
import { replaceContentPackageTargets, type ContentPackageTarget } from "../../features/content/contentPackagesApi";

export function CoverOverridesPanel({
  packageId,
  workspaceId,
  packageVersion,
  targets,
  onSaved,
}: {
  packageId: string;
  workspaceId: number;
  packageVersion: number;
  targets: ContentPackageTarget[];
  onSaved: () => Promise<void> | void;
}) {
  const [covers, setCovers] = useState<CoverLibraryItem[]>([]);
  const [templates, setTemplates] = useState<CoverTemplate[]>([]);
  const [versions, setVersions] = useState<CoverTemplateVersion[]>([]);
  const [selected, setSelected] = useState<Record<number, { cover?: string; template?: number }>>({});
  const [busy, setBusy] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    setBusy(true);
    void Promise.all([listCoverLibrary(workspaceId), listCoverTemplates(workspaceId)]).then(async ([coverData, templateData]) => {
      if (cancelled) return;
      setCovers(coverData.items ?? []);
      setTemplates(templateData.items ?? []);
      const versionResults = await Promise.all((templateData.items ?? []).map((template) => listCoverTemplateVersions(workspaceId, template.id)));
      if (cancelled) return;
      setVersions(versionResults.flatMap((result) => result.items ?? []));
      const initial: Record<number, { cover?: string; template?: number }> = {};
      for (const target of targets) initial[target.id] = { cover: target.cover_media_id, template: target.cover_template_version_id };
      setSelected(initial);
    }).catch((err) => {
      if (!cancelled) setError(err instanceof Error ? err.message : "Impossibile caricare gli asset.");
    }).finally(() => { if (!cancelled) setBusy(false); });
    return () => { cancelled = true; };
  }, [workspaceId, targets]);

  const versionByID = useMemo(() => new Map(versions.map((version) => [version.id, version])), [versions]);

  async function save() {
    setSaving(true);
    setError("");
    try {
      await replaceContentPackageTargets(packageId, {
        expected_package_version: packageVersion,
        targets: targets.map((target) => ({
          platform_account_id: target.platform_account_id,
          language: target.language,
          privacy_status: target.privacy_status,
          enabled: target.enabled,
          playlist_id: target.playlist_id,
          cover_media_id: selected[target.id]?.cover || undefined,
          cover_template_version_id: selected[target.id]?.template || undefined,
        })),
      });
      await onSaved();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Impossibile salvare gli override.");
    } finally {
      setSaving(false);
    }
  }

  return (
    <section className="mt-6 rounded-2xl border border-white/[0.09] bg-white/[0.03] p-5">
      <div className="mb-4 flex flex-wrap items-start justify-between gap-3"><div><h2 className="flex items-center gap-2 font-semibold text-white"><Layers3 size={18} /> Cover per lingua e canale</h2><p className="mt-1 text-xs text-[#858b99]">Il default del package resta attivo finché non scegli un override specifico.</p></div><button type="button" onClick={() => void save()} disabled={saving || busy} className="inline-flex items-center gap-2 rounded-xl bg-white px-3.5 py-2 text-xs font-bold text-black disabled:opacity-50">{saving ? <Loader2 size={14} className="animate-spin" /> : <Save size={14} />} Salva override</button></div>
      {error && <p className="mb-3 rounded-lg bg-red-400/10 px-3 py-2 text-xs text-red-200">{error}</p>}
      {busy ? <div className="flex items-center gap-2 text-xs text-[#858b99]"><Loader2 size={14} className="animate-spin" /> Caricamento library…</div> : targets.length === 0 ? <p className="text-xs text-[#858b99]">Configura almeno un target per assegnare override.</p> : <div className="space-y-3">{targets.map((target) => <div key={target.id} className="grid gap-3 rounded-xl border border-white/[0.07] bg-black/20 p-3 md:grid-cols-[150px_1fr_1fr]"><div><p className="text-sm font-semibold text-white">{target.language.toUpperCase()}</p><p className="mt-1 text-[10px] text-[#858b99]">Account #{target.platform_account_id}</p></div><label className="grid gap-1 text-[10px] font-bold uppercase tracking-wide text-[#858b99]">Cover asset<select value={selected[target.id]?.cover ?? ""} onChange={(event) => setSelected((current) => ({ ...current, [target.id]: { ...current[target.id], cover: event.target.value || undefined } }))} className="rounded-lg border border-white/10 bg-[#11131a] px-2.5 py-2 text-xs font-normal normal-case tracking-normal text-white"><option value="">Default package</option>{covers.map((cover) => <option key={cover.media_id} value={cover.media_id}>{cover.project_name} · {cover.width}×{cover.height}</option>)}</select></label><label className="grid gap-1 text-[10px] font-bold uppercase tracking-wide text-[#858b99]">Template version<select value={selected[target.id]?.template ?? ""} onChange={(event) => { const value = event.target.value; setSelected((current) => ({ ...current, [target.id]: { ...current[target.id], template: value ? Number(value) : undefined } })); }} className="rounded-lg border border-white/10 bg-[#11131a] px-2.5 py-2 text-xs font-normal normal-case tracking-normal text-white"><option value="">No template override</option>{versions.map((version) => <option key={version.id} value={version.id}>{templates.find((template) => template.id === version.template_id)?.name ?? "Template"} · v{version.version_number}</option>)}</select>{selected[target.id]?.template && <span className="inline-flex items-center gap-1 text-[10px] font-normal normal-case tracking-normal text-violet-300"><FileImage size={11} /> {versionByID.get(selected[target.id]!.template!)?.editor_project_id}</span>}</label></div>)}</div>}
    </section>
  );
}
