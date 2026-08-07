import { useEffect, useRef, useState, type ElementType } from "react";
import { Link } from "react-router-dom";
import {
  CalendarClock,
  Check,
  CheckCircle2,
  Folder,
  FolderMinus,
  Link2,
  MoreVertical,
  PauseCircle,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  Trash2,
  X,
} from "lucide-react";
import { authedFetch } from "../../lib/auth";
import { cn } from "../../lib/utils";
import { type PlatformAccount, type TreeNode } from "./groupsTypes";
import { GroupYouTubeVideos } from "./GroupYouTubeVideos";
import { GroupBadges } from "./GroupBadges";
import { groupAccent } from "./groupAccent";
import { ProviderBadge } from "../../components/brand/PlatformLogos";
import { LanguagePicker } from "../../components/brand/LanguagePicker";

function GroupActionsMenu({ onRename, onDelete }: { onRename: () => void; onDelete: () => void }) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const closeOnOutsidePointer = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("pointerdown", closeOnOutsidePointer);
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("pointerdown", closeOnOutsidePointer);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);

  return (
    <div ref={rootRef} className="relative shrink-0">
      <button
        type="button"
        onClick={() => setOpen((current) => !current)}
        aria-label="Azioni cartella"
        aria-haspopup="menu"
        aria-expanded={open}
        title="Azioni cartella"
        className="rounded-md p-1.5 text-[#9aa0aa] transition-colors hover:bg-white/[0.08] hover:text-white"
      >
        <MoreVertical size={16} aria-hidden="true" />
      </button>
      {open ? (
        <div
          role="menu"
          aria-label="Azioni cartella"
          className="absolute right-0 top-[calc(100%+0.4rem)] z-30 min-w-[190px] overflow-hidden rounded-xl border border-white/15 bg-[#171722]/95 p-1.5 shadow-[0_18px_50px_-18px_rgba(0,0,0,0.9)] backdrop-blur-xl"
        >
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              setOpen(false);
              onRename();
            }}
            className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-[12px] text-zinc-300 transition-colors hover:bg-white/10 hover:text-white"
          >
            <Pencil size={13} aria-hidden="true" /> Rinomina gruppo
          </button>
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              setOpen(false);
              onDelete();
            }}
            className="flex w-full items-center gap-2 rounded-lg px-2.5 py-2 text-left text-[12px] text-red-300 transition-colors hover:bg-red-500/[0.12] hover:text-red-200"
          >
            <Trash2 size={13} aria-hidden="true" /> Elimina cartella
          </button>
        </div>
      ) : null}
    </div>
  );
}

export function GroupDetailPanel({
  group,
  onPickAccount,
  onDeleteGroup,
  onSaved,
  onRename,
  availableAccounts,
  onAddAccounts,
  groupNamesByAccountId,
}: {
  group: TreeNode;
  onPickAccount: (id: number) => void;
  onDeleteGroup: () => void;
  onSaved: () => void | Promise<void>;
  onRename: (name: string) => void | Promise<void>;
  /** YouTube channels (publishable) that are not yet members of this group. */
  availableAccounts: PlatformAccount[];
  /** Add channels to this group directly (no drag & drop needed). */
  onAddAccounts: (accountIds: number[]) => void;
  /** accountId → group names the channel belongs to (from the tray map), so
   *  each channel row can show which OTHER folders it already lives in. */
  groupNamesByAccountId?: Map<number, string[]>;
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
  const [addChannelsOpen, setAddChannelsOpen] = useState(false);
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
              <Folder size={20} className="shrink-0" style={{ color: groupAccent(group.name).text }} />
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
              <h2 className="flex items-center gap-2 truncate text-[18px] font-bold text-white">
                <Folder size={20} className="shrink-0" style={{ color: groupAccent(group.name).text }} />
                {group.name}
              </h2>
            </div>
          )}
          <p className="mt-0.5 text-[12px] text-[#9aa0aa]">
            {visibleAccounts.length} canali · {group.children.length} sottocartelle
          </p>
          {saveError ? <p className="mt-2 text-[12px] text-red-300" role="alert">{saveError}</p> : null}
        </div>
        <GroupActionsMenu onRename={() => setEditingName(true)} onDelete={onDeleteGroup} />
      </div>

      {/* Current accounts in this group */}
      <div className="mb-6">
        <div className="mb-2 flex items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            <h3 className="text-[13px] font-bold text-white">Canali</h3>
            <span className="rounded-full bg-white/[0.08] px-2 py-0.5 text-[11px] font-semibold text-[#cdd2da]" aria-hidden="true">{visibleAccounts.length}</span>
          </div>
          <button
            type="button"
            onClick={() => setAddChannelsOpen(true)}
            className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.10] bg-white/[0.04] px-2.5 py-1.5 text-[12px] font-semibold text-[#cdd2da] transition-colors hover:bg-white/[0.08] hover:text-white"
            data-testid="group-add-channels"
          >
            <Plus size={13} aria-hidden="true" /> Aggiungi canali
          </button>
        </div>

        {visibleAccounts.length === 0 ? (
          <p className="text-[12px] text-[#9aa0aa] italic">Nessun canale in questa cartella. Trascina un canale dal pannello qui sotto.</p>
        ) : (
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
            {visibleAccounts.map((a) => (
              <div key={a.id} className="group flex items-center gap-2.5 rounded-xl border border-white/[0.10] bg-white/[0.04] p-3 transition-colors hover:border-white/[0.20] hover:bg-white/[0.06]">
                <div className="flex h-9 w-9 shrink-0 items-center justify-center overflow-hidden rounded-full bg-gradient-to-br from-violet-500/25 to-fuchsia-500/15 text-[13px] font-bold text-white ring-1 ring-white/15">
                  {a.avatar_url ? (
                    <img src={a.avatar_url} alt="" className="h-full w-full object-cover" loading="lazy" />
                  ) : (
                    (a.username || a.platform_user_id).charAt(0).toUpperCase()
                  )}
                </div>
                <div className="min-w-0 flex-1">
                  <button type="button" onClick={() => onPickAccount(a.id)} className="block w-full truncate text-left text-[14px] font-semibold text-white hover:text-violet-200" title={`Apri ${a.username || a.platform_user_id}`}>{a.username || a.platform_user_id}</button>
                  <GroupBadges
                    names={(groupNamesByAccountId?.get(a.id) ?? []).filter((name) => name !== group.name)}
                    label="già in"
                    className="mt-1"
                  />
                  <div className="mt-1.5 flex items-center gap-1.5">
                    <LanguagePicker
                      value={languages[a.id]}
                      label={`Language for ${a.username || a.platform_user_id}`}
                      disabled={savingLanguageId === a.id}
                      error={Boolean(languageError[a.id])}
                      onChange={(language) => void saveManualLanguage(a.id, language)}
                    />
                    {languageError[a.id] ? <span className="shrink-0 text-[11px] font-bold leading-none text-red-300" title={languageError[a.id]} aria-label={`Errore lingua: ${languageError[a.id]}`}>!</span> : null}
                    <button type="button" onClick={() => void removeAccount(a.id, a.username)} disabled={saving} className="shrink-0 rounded-md p-1.5 text-[#9aa0aa] opacity-0 transition-opacity hover:bg-amber-500/15 hover:text-amber-300 focus-visible:opacity-100 disabled:cursor-progress group-hover:opacity-100" aria-label={`Rimuovi ${a.username || `canale #${a.id}`} dalla cartella`} title="Rimuovi dalla cartella — il canale resta collegato"><FolderMinus size={14} /></button>
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      <GroupYouTubeVideos groupId={group.id} />

      {addChannelsOpen ? (
        <AddChannelsDialog
          groupName={group.name}
          available={availableAccounts}
          groupNamesByAccountId={groupNamesByAccountId}
          onAdd={(accountIds) => {
            setAddChannelsOpen(false);
            onAddAccounts(accountIds);
          }}
          onClose={() => setAddChannelsOpen(false)}
        />
      ) : null}
    </div>
  );
}

function AddChannelsDialog({
  groupName,
  available,
  groupNamesByAccountId,
  onAdd,
  onClose,
}: {
  groupName: string;
  available: PlatformAccount[];
  /** accountId → group names, so each row shows which OTHER folders the
   *  channel already lives in before the operator adds it here. */
  groupNamesByAccountId?: Map<number, string[]>;
  onAdd: (accountIds: number[]) => void;
  onClose: () => void;
}) {
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<Set<number>>(new Set());

  useEffect(() => {
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    document.addEventListener("keydown", closeOnEscape);
    return () => document.removeEventListener("keydown", closeOnEscape);
  }, [onClose]);

  const filtered = available.filter((account) => {
    const query = search.trim().toLocaleLowerCase();
    if (!query) return true;
    return [account.username, account.platform_user_id]
      .some((value) => value.toLocaleLowerCase().includes(query));
  });
  const allVisibleSelected = filtered.length > 0 && filtered.every((account) => selected.has(account.id));

  const toggle = (accountId: number) => {
    setSelected((current) => {
      const next = new Set(current);
      if (next.has(accountId)) next.delete(accountId);
      else next.add(accountId);
      return next;
    });
  };

  const toggleAll = () => {
    setSelected((current) => {
      const next = new Set(current);
      for (const account of filtered) {
        if (allVisibleSelected) next.delete(account.id);
        else next.add(account.id);
      }
      return next;
    });
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4 backdrop-blur-sm"
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="add-channels-title"
        className="flex max-h-[85vh] w-full max-w-lg flex-col overflow-hidden rounded-2xl border border-white/[0.12] bg-[#11131a] shadow-2xl"
        data-testid="group-add-channels-dialog"
      >
        <div className="flex items-center justify-between gap-3 border-b border-white/[0.08] px-5 py-4">
          <div className="min-w-0">
            <h3 id="add-channels-title" className="truncate text-[16px] font-bold text-white">Aggiungi canali a «{groupName}»</h3>
            <p className="mt-0.5 text-[12px] text-[#9aa0aa]">Seleziona i canali YouTube da aggiungere a questa cartella.</p>
          </div>
          <button type="button" onClick={onClose} aria-label="Chiudi" className="rounded-lg p-1.5 text-[#9aa0aa] hover:bg-white/[0.08] hover:text-white"><X size={16} aria-hidden="true" /></button>
        </div>

        {available.length > 0 ? (
          <div className="border-b border-white/[0.08] px-5 py-3">
            <label className="relative block">
              <Search size={14} className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[#9aa0aa]" />
              <input
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                placeholder="Cerca canale…"
                aria-label="Cerca canali da aggiungere"
                className="w-full rounded-lg border border-white/[0.10] bg-black/20 py-2 pl-9 pr-3 text-[12px] text-white outline-none transition focus:border-violet-400/60"
              />
            </label>
          </div>
        ) : null}

        {available.length === 0 ? (
          <div className="px-5 py-8 text-center">
            <p className="text-[13px] font-semibold text-white">Nessun canale da aggiungere</p>
            <p className="mt-1 text-[12px] text-[#9aa0aa]">Tutti i canali YouTube disponibili sono già in questa cartella.</p>
          </div>
        ) : (
          <div className="max-h-72 flex-1 overflow-y-auto p-2">
            <button
              type="button"
              onClick={toggleAll}
              aria-label={allVisibleSelected ? "Deseleziona tutti i canali da aggiungere" : "Seleziona tutti i canali da aggiungere"}
              className="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-[11px] font-semibold text-[#cdd2da] transition-colors hover:bg-white/[0.06] hover:text-white"
            >
              <span className={cn("grid h-4 w-4 shrink-0 place-items-center rounded border transition-colors", allVisibleSelected ? "border-violet-300 bg-violet-500 text-white" : "border-white/25")}>
                {allVisibleSelected ? <Check size={11} aria-hidden="true" /> : null}
              </span>
              {allVisibleSelected ? "Deseleziona tutti" : "Seleziona tutti"}
            </button>
            {filtered.map((account) => {
              const label = account.username || account.platform_user_id || `canale #${account.id}`;
              const isSelected = selected.has(account.id);
              return (
                <button
                  key={account.id}
                  type="button"
                  onClick={() => toggle(account.id)}
                  aria-pressed={isSelected}
                  className="flex w-full items-center gap-2.5 rounded-xl px-2 py-2 text-left transition-colors hover:bg-white/[0.06]"
                >
                  <span className={cn("grid h-4 w-4 shrink-0 place-items-center rounded border transition-colors", isSelected ? "border-violet-300 bg-violet-500 text-white" : "border-white/25")}>
                    {isSelected ? <Check size={11} aria-hidden="true" /> : null}
                  </span>
                  <div className="flex h-7 w-7 shrink-0 items-center justify-center overflow-hidden rounded-full bg-white/[0.06] text-[11px] font-bold text-white ring-1 ring-white/15">
                    {account.avatar_url ? (
                      <img src={account.avatar_url} alt="" className="h-full w-full object-cover" loading="lazy" />
                    ) : (
                      label.charAt(0).toUpperCase()
                    )}
                  </div>
                  <span className="min-w-0 flex-1 truncate text-[13px] font-semibold text-white">{label}</span>
                  <GroupBadges
                    names={(groupNamesByAccountId?.get(account.id) ?? []).filter((name) => name !== groupName)}
                    max={1}
                    className="shrink-0"
                  />
                  <span className="shrink-0 text-[10px] uppercase tracking-wider text-[#9aa0aa]">{account.platform === "youtube" ? "YouTube" : account.platform}</span>
                </button>
              );
            })}
            {filtered.length === 0 ? (
              <p className="px-3 py-6 text-center text-[12px] text-[#9aa0aa]">Nessun canale corrisponde alla ricerca.</p>
            ) : null}
          </div>
        )}

        <div className="flex items-center justify-between gap-3 border-t border-white/[0.08] px-5 py-4">
          <p className="text-[12px] text-[#9aa0aa]">{selected.size > 0 ? `${selected.size} selezionati` : "Nessun canale selezionato"}</p>
          <div className="flex items-center gap-2">
            <button type="button" onClick={onClose} className="rounded-lg border border-white/[0.08] bg-white/[0.04] px-3 py-1.5 text-[12px] font-semibold text-[#cdd2da] transition-colors hover:bg-white/[0.08] hover:text-white">Annulla</button>
            <button
              type="button"
              onClick={() => onAdd(Array.from(selected))}
              disabled={selected.size === 0}
              className="inline-flex items-center gap-1.5 rounded-lg bg-white px-3 py-1.5 text-[12px] font-bold text-black transition-opacity disabled:cursor-not-allowed disabled:opacity-40"
            >
              <Plus size={13} aria-hidden="true" /> Aggiungi{selected.size > 0 ? ` (${selected.size})` : ""}
            </button>
          </div>
        </div>
      </div>
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
