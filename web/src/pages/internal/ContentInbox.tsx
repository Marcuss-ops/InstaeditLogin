import { useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { CheckCircle2, FolderSearch, Loader2, Plus, RefreshCw, Sparkles, Video } from "lucide-react";
import {
  claimDriveInboxItem,
  createDriveInbox,
  listDriveInboxItems,
  listDriveInboxes,
  type DriveInbox,
  type DriveInboxItem,
} from "../../features/content/contentPackagesApi";

function formatBytes(value?: number) {
  if (!value || value <= 0) return "Dimensione sconosciuta";
  const units = ["B", "KB", "MB", "GB"];
  let size = value;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${size.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
}

export function ContentInbox() {
  const navigate = useNavigate();
  const [inboxes, setInboxes] = useState<DriveInbox[]>([]);
  const [selectedInboxID, setSelectedInboxID] = useState<number | null>(null);
  const [items, setItems] = useState<DriveInboxItem[]>([]);
  const [busy, setBusy] = useState(true);
  const [scanning, setScanning] = useState(false);
  const [error, setError] = useState("");
  const [accountID, setAccountID] = useState("");
  const [folderID, setFolderID] = useState("");
  const [creating, setCreating] = useState(false);
  const [claimingID, setClaimingID] = useState<number | null>(null);
  const [claimTitle, setClaimTitle] = useState("");
  const [claimDescription, setClaimDescription] = useState("");

  async function loadInboxes(preferredID?: number) {
    setBusy(true);
    setError("");
    try {
      const response = await listDriveInboxes();
      const next = response.items ?? [];
      setInboxes(next);
      const nextID = preferredID ?? selectedInboxID ?? next[0]?.id ?? null;
      setSelectedInboxID(nextID);
      if (nextID) {
        const itemResponse = await listDriveInboxItems(nextID, "ready_for_review");
        setItems(itemResponse.items ?? []);
      } else {
        setItems([]);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Impossibile caricare Drive Inbox");
    } finally {
      setBusy(false);
    }
  }

  async function refreshItems() {
    if (!selectedInboxID) return;
    setScanning(true);
    setError("");
    try {
      const response = await listDriveInboxItems(selectedInboxID, "ready_for_review");
      setItems(response.items ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Impossibile aggiornare i file");
    } finally {
      setScanning(false);
    }
  }

  useEffect(() => {
    void loadInboxes();
  }, []);

  async function createInbox(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const parsedAccountID = Number(accountID);
    if (!Number.isInteger(parsedAccountID) || parsedAccountID <= 0 || !folderID.trim()) {
      setError("Inserisci un platform account ID valido e un folder ID.");
      return;
    }
    setCreating(true);
    setError("");
    try {
      const inbox = await createDriveInbox({ drive_account_id: parsedAccountID, folder_id: folderID.trim() });
      setAccountID("");
      setFolderID("");
      await loadInboxes(inbox.id);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Creazione Inbox non riuscita");
    } finally {
      setCreating(false);
    }
  }

  async function claimItem(item: DriveInboxItem) {
    if (!selectedInboxID) return;
    setClaimingID(item.id);
    setError("");
    try {
      const response = await claimDriveInboxItem(selectedInboxID, item.id, {
        source_language: "it",
        title: claimTitle.trim() || item.filename.replace(/\.[^.]+$/, ""),
        description: claimDescription.trim(),
      });
      navigate(`/app/content/${response.package.id}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Impossibile creare il Content Package");
    } finally {
      setClaimingID(null);
    }
  }

  return (
    <div className="min-h-full bg-[#030308] px-4 py-8 text-[#e8e8ef] md:px-8">
      <div className="mx-auto max-w-7xl">
        <div className="mb-8 flex flex-wrap items-end justify-between gap-4">
          <div>
            <p className="mb-2 flex items-center gap-2 text-xs uppercase tracking-[0.2em] text-[#858b99]"><FolderSearch size={15} /> Content operations</p>
            <h1 className="text-3xl font-bold text-white">Drive Inbox</h1>
            <p className="mt-2 max-w-2xl text-sm leading-6 text-[#9aa0aa]">Scopri i video senza scaricarli, trasformali in un Content Package e prepara una pubblicazione multi-canale quando sei pronto.</p>
          </div>
          <Link to="/app/calendar" className="text-sm text-[#9aa0aa] no-underline hover:text-white">Apri calendario →</Link>
        </div>

        {error && <div role="alert" className="mb-6 rounded-xl border border-red-400/30 bg-red-400/10 p-3 text-sm text-red-200">{error}</div>}

        <div className="mb-6 grid gap-5 lg:grid-cols-[280px_1fr]">
          <section className="rounded-2xl border border-white/[0.09] bg-white/[0.03] p-5">
            <div className="mb-4 flex items-center justify-between"><h2 className="font-semibold text-white">Inbox configurate</h2><button type="button" onClick={() => void loadInboxes()} className="rounded-lg p-2 text-[#9aa0aa] hover:bg-white/[0.06] hover:text-white" aria-label="Aggiorna Inbox"><RefreshCw size={15} /></button></div>
            {busy ? <Loader2 className="animate-spin text-[#9aa0aa]" size={18} /> : inboxes.length === 0 ? <p className="text-sm leading-6 text-[#858b99]">Nessuna Inbox. Collega un account Drive e aggiungi una cartella.</p> : <div className="space-y-2">{inboxes.map((inbox) => <button type="button" key={inbox.id} onClick={() => { setSelectedInboxID(inbox.id); void listDriveInboxItems(inbox.id, "ready_for_review").then((response) => setItems(response.items ?? [])).catch(() => setError("Impossibile caricare i file")); }} className={`w-full rounded-xl border px-3 py-3 text-left transition ${selectedInboxID === inbox.id ? "border-sky-400/40 bg-sky-400/10" : "border-white/[0.08] hover:bg-white/[0.05]"}`}><span className="block truncate text-sm font-medium text-white">{inbox.folder_id}</span><span className="mt-1 block text-xs text-[#858b99]">Account #{inbox.drive_account_id}</span></button>)}</div>}
          </section>

          <section className="rounded-2xl border border-white/[0.09] bg-white/[0.03] p-5">
            <div className="mb-4 flex items-center justify-between gap-3"><div><h2 className="font-semibold text-white">Aggiungi una cartella Drive</h2><p className="mt-1 text-xs text-[#858b99]">La scansione salva solo metadati: il download parte dopo la programmazione.</p></div><Plus size={18} className="text-sky-300" /></div>
            <form onSubmit={(event) => void createInbox(event)} className="grid gap-3 sm:grid-cols-[180px_1fr_auto] sm:items-end">
              <label className="text-xs text-[#9aa0aa]">Drive account ID<input value={accountID} onChange={(event) => setAccountID(event.target.value)} inputMode="numeric" className="mt-2 w-full rounded-xl border border-white/10 bg-black/20 px-3 py-2 text-sm text-white" placeholder="es. 42" /></label>
              <label className="text-xs text-[#9aa0aa]">Folder ID<input value={folderID} onChange={(event) => setFolderID(event.target.value)} className="mt-2 w-full rounded-xl border border-white/10 bg-black/20 px-3 py-2 text-sm text-white" placeholder="Google Drive folder id" /></label>
              <button disabled={creating} className="inline-flex h-10 items-center justify-center gap-2 rounded-xl bg-white px-4 text-sm font-semibold text-black disabled:opacity-50">{creating && <Loader2 size={15} className="animate-spin" />} Salva Inbox</button>
            </form>
          </section>
        </div>

        <section className="rounded-2xl border border-white/[0.09] bg-white/[0.03] p-5">
          <div className="mb-5 flex flex-wrap items-center justify-between gap-3"><div><h2 className="font-semibold text-white">File pronti per la revisione</h2><p className="mt-1 text-xs text-[#858b99]">{items.length} video in attesa di essere trasformati in package.</p></div><button type="button" disabled={!selectedInboxID || scanning} onClick={() => void refreshItems()} className="inline-flex items-center gap-2 rounded-xl border border-white/10 px-3 py-2 text-xs font-semibold text-[#d4d7df] hover:bg-white/[0.06] disabled:opacity-40">{scanning ? <Loader2 size={14} className="animate-spin" /> : <RefreshCw size={14} />} Aggiorna file</button></div>
          {items.length === 0 ? <div className="rounded-xl border border-dashed border-white/10 p-10 text-center"><Sparkles className="mx-auto mb-3 text-[#606777]" size={22} /><p className="text-sm text-[#858b99]">Nessun nuovo file da rivedere in questa Inbox.</p></div> : <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">{items.map((item) => <article key={item.id} className="rounded-xl border border-white/[0.08] bg-black/20 p-4"><div className="mb-3 flex items-start justify-between gap-3"><div className="flex min-w-0 items-center gap-2"><Video size={17} className="shrink-0 text-sky-300" /><h3 className="truncate text-sm font-semibold text-white" title={item.filename}>{item.filename}</h3></div><CheckCircle2 size={16} className="shrink-0 text-emerald-300" /></div><p className="text-xs text-[#858b99]">{item.mime_type || "video"} · {formatBytes(item.size_bytes)}</p><p className="mt-1 truncate text-xs text-[#606777]">{item.drive_file_id}</p><div className="mt-4 space-y-2"><input aria-label={`Titolo ${item.filename}`} value={claimingID === item.id ? claimTitle : ""} onChange={(event) => setClaimTitle(event.target.value)} placeholder="Titolo (opzionale)" className="w-full rounded-lg border border-white/10 bg-white/[0.03] px-3 py-2 text-xs text-white" /><textarea aria-label={`Descrizione ${item.filename}`} value={claimingID === item.id ? claimDescription : ""} onChange={(event) => setClaimDescription(event.target.value)} placeholder="Descrizione (opzionale)" rows={2} className="w-full resize-none rounded-lg border border-white/10 bg-white/[0.03] px-3 py-2 text-xs text-white" /><button type="button" disabled={claimingID !== null} onClick={() => void claimItem(item)} className="inline-flex w-full items-center justify-center gap-2 rounded-lg bg-sky-400 px-3 py-2 text-xs font-bold text-black hover:bg-sky-300 disabled:opacity-50">{claimingID === item.id ? <Loader2 size={14} className="animate-spin" /> : <Sparkles size={14} />} Crea Content Package</button></div></article>)}</div>}
        </section>
      </div>
    </div>
  );
}

export default ContentInbox;
