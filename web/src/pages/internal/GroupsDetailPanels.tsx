import { useEffect, useState, type ElementType } from "react";
import { Link } from "react-router-dom";
import {
  CalendarClock,
  CheckCircle2,
  Folder,
  Link2,
  PauseCircle,
  Pencil,
  RefreshCw,
  Trash2,
  FolderMinus,
} from "lucide-react";
import { authedFetch } from "../../lib/auth";
import { cn } from "../../lib/utils";
import { type PlatformAccount, type TreeNode } from "./groupsTypes";
import { GroupYouTubeVideos } from "./GroupYouTubeVideos";
import { ProviderBadge } from "../../components/brand/PlatformLogos";
import { LanguagePicker } from "../../components/brand/LanguagePicker";

export function GroupDetailPanel({
  group,
  onPickAccount,
  onDeleteGroup,
  onSaved,
  onRename,
}: {
  group: TreeNode;
  onPickAccount: (id: number) => void;
  onDeleteGroup: () => void;
  onSaved: () => void | Promise<void>;
  onRename: (name: string) => void | Promise<void>;
}) {
  const [editingName, setEditingName] = useState(false);
  const [groupName, setGroupName] = useState(group.name);
  const [savingName, setSavingName] = useState(false);
  const [renameError, setRenameError] = useState<string | null>(null);
  const [languages, setLanguages] = useState<Record<number, string>>(() => Object.fromEntries(group.accounts.map((account) => [account.id, account.language ?? ""])));
  const [savingLanguageId, setSavingLanguageId] = useState<number | null>(null);
  const [languageError, setLanguageError] = useState<Record<number, string>>({});
  const [removedAccountIds, setRemovedAccountIds] = useState<Set<number>>(new Set());
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  useEffect(() => {
    setGroupName(group.name);
    setEditingName(false);
    setRenameError(null);
    setLanguages((current) => {
      const next = { ...current };
      for (const account of group.accounts) {
        // A successful language PATCH can be followed by a fresh account
        // manifest. Sync a non-empty server value over an empty local value,
        // but preserve an in-progress/manual local choice until it is saved.
        if (!(account.id in next) || (!next[account.id]?.trim() && account.language?.trim())) {
          next[account.id] = account.language ?? "";
        }
      }
      return next;
    });
    setRemovedAccountIds(new Set());
    setSaveError(null);
  }, [group.id, group.accounts]);

  const saveLanguage = async (accountId: number, language: string): Promise<boolean> => {
    const previous = languages[accountId] ?? "";
    setLanguages((current) => ({ ...current, [accountId]: language }));
    setLanguageError((current) => {
      const next = { ...current };
      delete next[accountId];
      return next;
    });
    setSavingLanguageId(accountId);
    try {
      await authedFetch(`/api/v1/accounts/${accountId}`, {
        method: "PATCH",
        body: JSON.stringify({ metadata: { language } }),
      });
      return true;
    } catch (error) {
      // On failure, restore the previous value and surface the error so the
      // operator can retry from the per-channel dropdown.
      setLanguages((current) => ({ ...current, [accountId]: previous }));
      setLanguageError((current) => ({
        ...current,
        [accountId]: error instanceof Error ? error.message : "Unable to save language",
      }));
      return false;
    } finally {
      setSavingLanguageId((current) => (current === accountId ? null : current));
    }
  };

  const visibleAccounts = group.accounts.filter((account) => !removedAccountIds.has(account.id));

  const saveManualLanguage = async (accountId: number, language: string) => {
    const saved = await saveLanguage(accountId, language);
    if (saved) await onSaved();
  };
  // "Rimuovi dalla cartella": dedicated endpoint that only deletes the
  // group_accounts membership and resyncs workspace_channels — it never
  // disconnects the channel or touches OAuth tokens.
  const removeAccount = async (accountId: number, username: string) => {
    if (!window.confirm(
      `Rimuovere ${username || `il canale #${accountId}`} soltanto dalla cartella "${group.name}"?\n\nIl canale rimarrà collegato a InstaEdit e continuerà a essere disponibile altrove.`,
    )) return;
    setRemovedAccountIds((current) => new Set(current).add(accountId));
    setSaving(true);
    setSaveError(null);
    let removalCommitted = false;
    try {
      const response = await authedFetch(`/api/v1/groups/${group.id}/accounts/${accountId}`, {
        method: "DELETE",
      });
      if (!response.ok) throw new Error("Impossibile rimuovere il canale dalla cartella");
      removalCommitted = true;
      await onSaved();
    } catch (error) {
      if (!removalCommitted) {
        setRemovedAccountIds((current) => {
          const next = new Set(current);
          next.delete(accountId);
          return next;
        });
      }
      setSaveError(
        removalCommitted
          ? "Canale rimosso, ma impossibile aggiornare la cartella. Ricarica la pagina."
          : error instanceof Error ? error.message : "Impossibile rimuovere il canale dalla cartella",
      );
    } finally {
      setSaving(false);
    }
  };

  return (
    <div>
      <div className="flex items-start justify-between gap-3 mb-5">
        <div className="min-w-0 flex-1">
          {editingName ? (
            <form
              className="flex max-w-xl items-center gap-2"
              onSubmit={async (event) => {
                event.preventDefault();
                const nextName = groupName.trim();
                if (savingName) return;
                if (!nextName) {
                  setRenameError("Il nome del gruppo è obbligatorio.");
                  return;
                }
                if (nextName.length > 80) {
                  setRenameError("Il nome del gruppo può contenere al massimo 80 caratteri.");
                  return;
                }
                if (nextName === group.name) {
                  setRenameError("Inserisci un nome diverso da quello attuale.");
                  return;
                }
                setRenameError(null);
                setSavingName(true);
                try {
                  await onRename(nextName);
                  setEditingName(false);
                } catch (error) {
                  setRenameError(error instanceof Error ? error.message : "Impossibile rinominare il gruppo.");
                } finally {
                  setSavingName(false);
                }
              }}
            >
              <Folder size={20} className="shrink-0 text-amber-300/80" />
              <input
                autoFocus
                value={groupName}
                onChange={(event) => {
                  setGroupName(event.target.value);
                  if (renameError) setRenameError(null);
                }}
                maxLength={80}
                aria-label="Nome del gruppo"
                className="min-w-0 flex-1 rounded-lg border border-violet-400/50 bg-black/30 px-2.5 py-1.5 text-[16px] font-bold text-white outline-none focus:ring-2 focus:ring-violet-500/30"
              />
              <button type="submit" disabled={savingName} className="rounded-lg bg-white px-2.5 py-1.5 text-[11px] font-bold text-black disabled:opacity-50">{savingName ? "Salvo…" : "Salva"}</button>
              <button type="button" onClick={() => { setEditingName(false); setGroupName(group.name); setRenameError(null); }} disabled={savingName} className="rounded-lg p-1.5 text-[#9aa0aa] hover:bg-white/[0.08] hover:text-white" aria-label="Annulla rinomina"><XIcon /></button>
              {renameError ? <p className="basis-full text-[11px] text-red-300" role="alert">{renameError}</p> : null}
            </form>
          ) : (
            <div className="flex items-center gap-2">
              <h2 className="truncate text-[18px] font-bold text-white flex items-center gap-2">
                <Folder size={20} className="shrink-0 text-amber-300/80" />
                {group.name}
              </h2>
              <button type="button" onClick={() => setEditingName(true)} className="rounded-md p-1.5 text-[#9aa0aa] hover:bg-white/[0.08] hover:text-white" aria-label="Rinomina gruppo" title="Rinomina gruppo"><Pencil size={14} /></button>
            </div>
          )}
          <p className="text-[12px] text-[#9aa0aa] mt-0.5">
            {visibleAccounts.length} canali · {group.children.length} sottocartelle
          </p>
          {saveError ? <p className="mt-2 text-[12px] text-red-300" role="alert">{saveError}</p> : null}
        </div>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={onDeleteGroup}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-white/[0.04] border border-white/[0.08] text-[12px] font-medium text-red-300 hover:bg-red-500/[0.12] hover:border-red-500/40 transition-colors"
            aria-label="Elimina cartella"
            title="Elimina cartella e le relative membership"
          >
            <Trash2 size={13} /> Elimina cartella
          </button>
        </div>
      </div>

      {/* Current accounts in this group */}
      <div className="mb-6">
          <h3 className="mb-2 text-[11px] font-bold uppercase tracking-wider text-[#9aa0aa]">
            Canali in questa cartella ({visibleAccounts.length})
          </h3>

        {visibleAccounts.length === 0 ? (
          <p className="text-[12px] text-[#9aa0aa] italic">Nessun canale in questa cartella. Trascina un canale dal pannello qui sotto.</p>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
            {visibleAccounts.map((a) => (
              <div key={a.id} className="flex items-center gap-1.5 rounded-lg border border-white/[0.10] bg-white/[0.04] p-1.5">
                <button type="button" onClick={() => onPickAccount(a.id)} className="w-44 min-w-0 truncate text-left text-[14px] font-semibold text-white hover:text-violet-200" title={`Apri ${a.username || a.platform_user_id}`}>{a.username || a.platform_user_id}</button>
                <LanguagePicker
                  value={languages[a.id]}
                  label={`Language for ${a.username || a.platform_user_id}`}
                  disabled={savingLanguageId === a.id}
                  error={Boolean(languageError[a.id])}
                  onChange={(language) => void saveManualLanguage(a.id, language)}
                />
                {languageError[a.id] ? <span className="shrink-0 text-[11px] font-bold leading-none text-red-300" title={languageError[a.id]} aria-label={`Errore lingua: ${languageError[a.id]}`}>!</span> : null}
                <button type="button" onClick={() => void removeAccount(a.id, a.username)} disabled={saving} className="shrink-0 rounded-md p-1.5 text-[#9aa0aa] hover:bg-amber-500/15 hover:text-amber-300 disabled:cursor-progress disabled:opacity-50" aria-label={`Rimuovi ${a.username || `canale #${a.id}`} dalla cartella`} title="Rimuovi dalla cartella — il canale resta collegato"><FolderMinus size={14} /></button>
              </div>
            ))}
          </div>
        )}
      </div>

      <GroupYouTubeVideos groupId={group.id} />
    </div>
  );
}

export function AccountDetailPanel({
  account,
  onClose,
  onUpdated,
}: {
  account: PlatformAccount;
  onClose: () => void;
  onUpdated: () => void;
}) {
  const [busy, setBusy] = useState<null | "reconnect" | "validate">(null);
  const [details, setDetails] = useState<{ user_id?: number; posts?: { queued: number; published: number; failed: number } } | null>(null);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const [accountsResp, postsResp] = await Promise.all([
          authedFetch(`/api/v1/accounts/${account.id}`),
          authedFetch("/api/v1/posts"),
        ]);
        if (cancelled) return;
        const acct = (await accountsResp.json()) as PlatformAccount & { user_id: number };
        const posts = (await postsResp.json()) as { posts: Array<{ status: string }> };
        const summary = { queued: 0, published: 0, failed: 0 };
        for (const p of posts.posts ?? []) {
          if (p.status === "queued") summary.queued += 1;
          else if (p.status === "published") summary.published += 1;
          else if (p.status === "failed") summary.failed += 1;
        }
        setDetails({ user_id: acct.user_id, posts: summary });
      } catch {
        // optional details — silently fall back to username only
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [account.id]);

  const StatusIcon =
    account.status === "active" ? CheckCircle2 :
    PauseCircle;

  const runAction = async (
    action: "reconnect" | "validate",
    method: string,
    endpoint: string,
    body?: string,
  ) => {
    setBusy(action);
    try {
      await authedFetch(endpoint, { method, body });
      onUpdated();
    } finally {
      setBusy(null);
    }
  };

  return (
    <div>
      <div className="flex items-start gap-4 mb-6">
        <ProviderBadge
          platform={account.platform}
          className="h-16 w-16 shrink-0 justify-center rounded-xl"
          compact
          logoClassName="h-9 w-9"
        />
        <div className="flex-1 min-w-0">
          <h2 className="text-[20px] font-extrabold tracking-[-0.01em] text-white flex items-center gap-2 flex-wrap">
            {account.username || account.platform_user_id}
            <span className="text-[11px] font-medium uppercase tracking-wider text-[#9aa0aa]">
              {account.platform === "youtube" ? "YouTube" : account.platform}
            </span>
          </h2>
          <p className="text-[12px] text-[#9aa0aa] mt-1 flex items-center gap-2">
            <StatusIcon size={14} className={account.status === "active" ? "text-emerald-400" : "text-amber-400"} />
            {account.status.replace(/_/g, " ")}
            {details?.user_id ? <> · ID #{details.user_id}</> : null}
          </p>
        </div>
        <button
          type="button"
          onClick={onClose}
          className="px-3 py-1.5 rounded-lg bg-white/[0.04] border border-white/[0.08] text-[12px] font-medium text-[#9aa0aa] hover:bg-white/[0.08] hover:text-white transition-colors"
        >
          Back
        </button>
      </div>

      {/* Quick stats */}
      <div className="grid grid-cols-3 gap-3 mb-6">
        <StatMini icon={CalendarClock} label="Workspace queued" value={details?.posts?.queued ?? "—"} accent="text-amber-300" />
        <StatMini icon={CheckCircle2} label="Workspace published" value={details?.posts?.published ?? "—"} accent="text-emerald-300" />
        <StatMini icon={PauseCircle} label="Workspace failed" value={details?.posts?.failed ?? "—"} accent="text-red-300" />
      </div>

      {/* Quick actions */}
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
        <ActionTile
          icon={RefreshCw}
          label="Reconnect"
          description="Re-run the OAuth flow to refresh tokens."
          onClick={() => void runAction("reconnect", "POST", `/api/v1/accounts/${account.id}/reconnect`)}
          busy={busy === "reconnect"}
        />
        <ActionTile
          icon={Pencil}
          label="Validate"
          description="Test that the stored tokens still work."
          onClick={() => void runAction("validate", "POST", `/api/v1/accounts/${account.id}/validate`)}
          busy={busy === "validate"}
        />
      </div>

      <div className="mt-6 flex items-center gap-2 text-[12px] text-[#9aa0aa]">
        <Link2 size={14} />
        Quick jump:
        <Link className="text-white underline hover:no-underline" to="/app/posts">All posts</Link>
        <span className="opacity-50">·</span>
        <Link className="text-white underline hover:no-underline" to="/app/compose">Compose new post</Link>
      </div>
    </div>
  );
}

function XIcon() {
  return <span aria-hidden="true" className="text-[16px] leading-none">×</span>;
}

function StatMini({
  icon: Icon,
  label,
  value,
  accent,
}: {
  icon: ElementType;
  label: string;
  value: number | string;
  accent: string;
}) {
  return (
    <div className="bg-white/[0.04] border border-white/[0.08] rounded-xl p-3">
      <div className="flex items-center justify-between">
        <p className="text-[10px] font-bold uppercase tracking-wider text-[#9aa0aa]">{label}</p>
        <Icon size={14} className={accent} />
      </div>
      <p className={cn("text-[24px] font-extrabold tracking-tight tabular-nums mt-1", accent)}>{value}</p>
    </div>
  );
}

function ActionTile({
  icon: Icon,
  label,
  description,
  onClick,
  busy,
}: {
  icon: ElementType;
  label: string;
  description: string;
  onClick: () => void;
  busy: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={busy}
      className={cn(
        "text-left p-4 rounded-xl border transition-colors",
        "bg-white/[0.06] border-white/[0.12] hover:bg-white/[0.10] hover:border-white/[0.20]",
        busy && "opacity-60 cursor-progress",
      )}
    >
      <div className="flex items-center gap-2 mb-1">
        <Icon size={16} className="text-white" />
        <span className="text-[14px] font-bold text-white">
          {label}
        </span>
        {busy && <RefreshCw size={12} className="animate-spin text-[#9aa0aa] ml-auto" />}
      </div>
      <p className="text-[12px] text-[#9aa0aa] leading-snug">{description}</p>
    </button>
  );
}
