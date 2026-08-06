import { useEffect, useState, type ElementType } from "react";
import { Link } from "react-router-dom";
import {
  CalendarClock,
  CheckCircle2,
  Folder,
  Link2,
  PauseCircle,
  Plus,
  Pencil,
  RefreshCw,
  Trash2,
  FolderMinus,
} from "lucide-react";
import { authedFetch } from "../../lib/auth";
import { cn } from "../../lib/utils";
import { PLATFORM_GRADIENT, type PlatformAccount, type TreeNode } from "./groupsTypes";
import { GroupYouTubeVideos } from "./GroupYouTubeVideos";

const LANGUAGE_OPTIONS = [
  { code: "it", flag: "🇮🇹", name: "Italiano" },
  { code: "en", flag: "🇬🇧", name: "English" },
  { code: "es", flag: "🇪🇸", name: "Español" },
  { code: "fr", flag: "🇫🇷", name: "Français" },
  { code: "de", flag: "🇩🇪", name: "Deutsch" },
  { code: "pl", flag: "🇵🇱", name: "Polski" },
  { code: "ru", flag: "🇷🇺", name: "Русский" },
  { code: "tr", flag: "🇹🇷", name: "Türkçe" },
  { code: "hi", flag: "🇮🇳", name: "हिन्दी" },
  { code: "id", flag: "🇮🇩", name: "Bahasa Indonesia" },
] as const;

function languageMeta(language: unknown) {
  return LANGUAGE_OPTIONS.find((option) => option.code === String(language ?? ""));
}

export function GroupDetailPanel({
  group,
  onPickAccount,
  onCreateSubgroup,
  onDeleteGroup,
  onSaved,
  onRename,
}: {
  group: TreeNode;
  onPickAccount: (id: number) => void;
  onCreateSubgroup: (name: string) => void;
  onDeleteGroup: () => void;
  onSaved: () => void | Promise<void>;
  onRename?: (name: string) => void | Promise<void>;
}) {
  const [subName, setSubName] = useState("");
  const [editingName, setEditingName] = useState(false);
  const [groupName, setGroupName] = useState(group.name);
  const [savingName, setSavingName] = useState(false);
  const [languages, setLanguages] = useState<Record<number, string>>(() => Object.fromEntries(group.accounts.map((account) => [account.id, account.language ?? ""])));
  const [savingLanguageId, setSavingLanguageId] = useState<number | null>(null);
  const [languageError, setLanguageError] = useState<Record<number, string>>({});
  const [removedAccountIds, setRemovedAccountIds] = useState<Set<number>>(new Set());
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  useEffect(() => {
    setGroupName(group.name);
    setEditingName(false);
    setLanguages((current) => {
      const next = { ...current };
      for (const account of group.accounts) {
        if (!(account.id in next)) next[account.id] = account.language ?? "";
      }
      return next;
    });
    setRemovedAccountIds(new Set());
    setSaveError(null);
  }, [group.id, group.accounts]);

  const saveLanguage = async (accountId: number, language: string) => {
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
    } catch (error) {
      setLanguages((current) => ({ ...current, [accountId]: previous }));
      setLanguageError((current) => ({
        ...current,
        [accountId]: error instanceof Error ? error.message : "Unable to save language",
      }));
    } finally {
      setSavingLanguageId((current) => (current === accountId ? null : current));
    }
  };

  const visibleAccounts = group.accounts.filter((account) => !removedAccountIds.has(account.id));
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

  const saveSettings = async () => {
    setSaving(true);
    setSaveError(null);
    try {
      const response = await authedFetch(`/api/v1/groups/${group.id}/settings`, {
        method: "PATCH",
        body: JSON.stringify({
          accounts: visibleAccounts.map((account) => ({
            account_id: account.id,
            language: languages[account.id] ?? "",
          })),
        }),
      });
      if (!response.ok) throw new Error("Unable to save group settings");
      await onSaved();
    } catch (error) {
      setSaveError(error instanceof Error ? error.message : "Unable to save group settings");
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
                if (!nextName || nextName === group.name || savingName) {
                  setEditingName(false);
                  setGroupName(group.name);
                  return;
                }
                setSavingName(true);
                try {
                  await onRename?.(nextName);
                  setEditingName(false);
                } catch {
                  // authedFetch already surfaces the error via the global toast.
                  setGroupName(group.name);
                } finally {
                  setSavingName(false);
                }
              }}
            >
              <Folder size={20} className="shrink-0 text-amber-300/80" />
              <input
                autoFocus
                value={groupName}
                onChange={(event) => setGroupName(event.target.value)}
                aria-label="Nome del gruppo"
                className="min-w-0 flex-1 rounded-lg border border-violet-400/50 bg-black/30 px-2.5 py-1.5 text-[16px] font-bold text-white outline-none focus:ring-2 focus:ring-violet-500/30"
              />
              <button type="submit" disabled={savingName || !groupName.trim()} className="rounded-lg bg-white px-2.5 py-1.5 text-[11px] font-bold text-black disabled:opacity-50">{savingName ? "Salvo…" : "Salva"}</button>
              <button type="button" onClick={() => { setEditingName(false); setGroupName(group.name); }} disabled={savingName} className="rounded-lg p-1.5 text-[#9aa0aa] hover:bg-white/[0.08] hover:text-white" aria-label="Annulla rinomina"><XIcon /></button>
            </form>
          ) : (
            <div className="flex items-center gap-2">
              <h2 className="truncate text-[18px] font-bold text-white flex items-center gap-2">
                <Folder size={20} className="shrink-0 text-amber-300/80" />
                {group.name}
              </h2>
              {onRename ? <button type="button" onClick={() => setEditingName(true)} className="rounded-md p-1.5 text-[#9aa0aa] hover:bg-white/[0.08] hover:text-white" aria-label="Rinomina gruppo" title="Rinomina gruppo"><Pencil size={14} /></button> : null}
            </div>
          )}
          <p className="text-[12px] text-[#9aa0aa] mt-0.5">
            {visibleAccounts.length} accounts · {group.children.length} sub-folders
          </p>
          {saveError ? <p className="mt-2 text-[12px] text-red-300" role="alert">{saveError}</p> : null}
        </div>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => void saveSettings()}
            disabled={saving}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-white text-black text-[12px] font-semibold disabled:opacity-50 disabled:cursor-progress hover:bg-zinc-100 transition-colors"
          >
            {saving ? <RefreshCw size={13} className="animate-spin" /> : <CheckCircle2 size={13} />}
            {saving ? "Saving..." : "Save changes"}
          </button>
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

      {/* Add sub-group */}
      <div className="flex items-center gap-2 mb-5">
        <input
          type="text"
          value={subName}
          onChange={(e) => setSubName(e.target.value)}
          placeholder="New sub-folder name..."
          className="flex-1 px-3 py-2 rounded-lg bg-white/[0.04] border border-white/[0.08] text-[13px] text-white placeholder:text-[#9aa0aa] focus:outline-none focus:ring-2 focus:ring-violet-500/40"
          aria-label="New sub-group name"
        />
        <button
          type="button"
          onClick={() => {
            onCreateSubgroup(subName);
            setSubName("");
          }}
          disabled={!subName.trim()}
          className="inline-flex items-center gap-1 px-3 py-2 rounded-lg bg-white text-black text-[12px] font-semibold disabled:opacity-50 disabled:cursor-not-allowed hover:bg-zinc-100 transition-colors"
        >
          <Plus size={14} /> Add
        </button>
      </div>

      {/* Current accounts in this group */}
      <div className="mb-6">
        <h3 className="text-[11px] font-bold uppercase tracking-wider text-[#9aa0aa] mb-2">
          Accounts in this folder ({visibleAccounts.length})
        </h3>
        {visibleAccounts.length === 0 ? (
          <p className="text-[12px] text-[#9aa0aa] italic">No accounts yet. Drag one in from below.</p>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
            {visibleAccounts.map((a) => (
              <div key={a.id} className="flex items-center gap-1 rounded-lg border border-white/[0.10] bg-white/[0.04] p-1.5">
                <div className="flex min-w-0 flex-1 items-center gap-2 px-1.5 py-1">
                  <span className="text-[13px]">{languageMeta(languages[a.id])?.flag ?? "🌍"}</span>
                  <button type="button" onClick={() => onPickAccount(a.id)} className="truncate text-left text-[12px] font-semibold text-white hover:text-violet-200" title={`Apri ${a.username}`}>{a.username || a.platform_user_id}</button>
                </div>
                <select value={languages[a.id] ?? ""} disabled={savingLanguageId === a.id} onChange={(event) => void saveLanguage(a.id, event.target.value)} className="max-w-[120px] rounded bg-black/30 border border-white/[0.10] px-1 py-1 text-[10px] text-[#c8cbd4] disabled:opacity-60" aria-label={`Language for ${a.username}`}>
                  <option value="">Lingua</option>
                  {LANGUAGE_OPTIONS.map(({ code, flag, name }) => <option key={code} value={code}>{flag} {name}</option>)}
                </select>
                {languageError[a.id] ? <span className="text-[10px] text-red-300" title={languageError[a.id]}>!</span> : null}
                <button type="button" onClick={() => void removeAccount(a.id, a.username)} disabled={saving} className="rounded-md p-2 text-[#9aa0aa] hover:bg-amber-500/15 hover:text-amber-300 disabled:cursor-progress disabled:opacity-50" aria-label={`Rimuovi ${a.username || `canale #${a.id}`} dalla cartella`} title="Rimuovi dalla cartella — il canale resta collegato"><FolderMinus size={14} /></button>
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

  const grad = PLATFORM_GRADIENT[account.platform] ?? "from-zinc-500 to-zinc-700";
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
        <div
          className={cn(
            "w-16 h-16 rounded-xl bg-gradient-to-br flex items-center justify-center text-white text-[18px] font-extrabold shrink-0",
            grad,
          )}
        >
          {(account.platform[0] ?? "?").toUpperCase()}
        </div>
        <div className="flex-1 min-w-0">
          <h2 className="text-[20px] font-extrabold tracking-[-0.01em] text-white flex items-center gap-2 flex-wrap">
            {account.username || account.platform_user_id}
            <span className="text-[11px] font-medium uppercase tracking-wider text-[#9aa0aa]">
              {account.platform}
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
