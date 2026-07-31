import { useEffect, useState, type ElementType } from "react";
import { Link } from "react-router-dom";
import {
  CalendarClock,
  CheckCircle2,
  Folder,
  Link2,
  PauseCircle,
  Pencil,
  Plus,
  RefreshCw,
  Trash2,
} from "lucide-react";
import { authedFetch } from "../../lib/auth";
import { cn } from "../../lib/utils";
import { PLATFORM_GRADIENT, type PlatformAccount, type TreeNode } from "./groupsTypes";

export function GroupDetailPanel({
  group,
  accounts,
  onPickAccount,
  onCreateSubgroup,
  onDeleteGroup,
  onSetGroupAccounts,
}: {
  group: TreeNode;
  accounts: PlatformAccount[];
  onPickAccount: (id: number) => void;
  onCreateSubgroup: (name: string) => void;
  onDeleteGroup: () => void;
  onSetGroupAccounts: (ids: number[]) => void;
}) {
  const [subName, setSubName] = useState("");
  const [draggingAccountId, setDraggingAccountId] = useState<number | null>(null);
  const availableAccounts = accounts.filter((a) => !group.accounts.find((ga) => ga.id === a.id));
  return (
    <div>
      <div className="flex items-start justify-between gap-3 mb-5">
        <div>
          <h2 className="text-[18px] font-bold text-white flex items-center gap-2">
            <Folder size={20} className="text-amber-300/80" />
            {group.name}
          </h2>
          <p className="text-[12px] text-[#9aa0aa] mt-0.5">
            {group.accounts.length} accounts · {group.children.length} sub-folders
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={onDeleteGroup}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-white/[0.04] border border-white/[0.08] text-[12px] font-medium text-red-300 hover:bg-red-500/[0.12] hover:border-red-500/40 transition-colors"
            aria-label="Delete folder"
          >
            <Trash2 size={13} /> Delete
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
          Accounts in this folder ({group.accounts.length})
        </h3>
        {group.accounts.length === 0 ? (
          <p className="text-[12px] text-[#9aa0aa] italic">No accounts yet. Drag one in from below.</p>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
            {group.accounts.map((a) => (
              <AccountChip key={a.id} account={a} onClick={() => onPickAccount(a.id)} />
            ))}
          </div>
        )}
      </div>

      {/* Available accounts (drag-into-this-group) */}
      <div>
        <h3 className="text-[11px] font-bold uppercase tracking-wider text-[#9aa0aa] mb-2">
          Available accounts ({availableAccounts.length})
        </h3>
        <div
          className={cn(
            "min-h-[80px] p-2 rounded-xl border border-dashed border-white/[0.12] bg-white/[0.02]",
            draggingAccountId != null && "ring-2 ring-emerald-500/40 bg-emerald-500/[0.04]",
          )}
          onDragOver={(e) => {
            if (e.dataTransfer.types.includes("text/plain")) {
              e.preventDefault();
              e.dataTransfer.dropEffect = "move";
            }
          }}
          onDrop={(e) => {
            e.preventDefault();
            const id = Number(e.dataTransfer.getData("text/plain"));
            setDraggingAccountId(null);
            if (!Number.isFinite(id) || id <= 0) return;
            if (group.accounts.find((ga) => ga.id === id)) return;
            onSetGroupAccounts([...group.accounts.map((a) => a.id), id]);
          }}
        >
          {availableAccounts.length === 0 ? (
            <p className="text-[12px] text-center text-[#9aa0aa] italic">
              Every account is already in this folder. Connect more from the Linking page.
            </p>
          ) : (
            <div className="flex flex-wrap gap-2">
              {availableAccounts.map((a) => (
                <div
                  key={a.id}
                  draggable
                  onDragStart={(e) => {
                    e.dataTransfer.setData("text/plain", String(a.id));
                    e.dataTransfer.effectAllowed = "move";
                    setDraggingAccountId(a.id);
                  }}
                  onDragEnd={() => setDraggingAccountId(null)}
                  className="cursor-grab active:cursor-grabbing"
                >
                  <AccountChip account={a} onClick={() => onPickAccount(a.id)} subtle />
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function AccountChip({
  account,
  onClick,
  subtle,
}: {
  account: PlatformAccount;
  onClick: () => void;
  subtle?: boolean;
}) {
  const grad = PLATFORM_GRADIENT[account.platform] ?? "from-zinc-500 to-zinc-700";
  const StatusIcon =
    account.status === "active" ? CheckCircle2 :
    account.status === "reauth_required" ? PauseCircle :
    PauseCircle;
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "flex items-center gap-2 px-2.5 py-2 rounded-lg border text-left transition-colors w-full",
        subtle
          ? "bg-white/[0.04] border-white/[0.08] text-[#9aa0aa] hover:bg-white/[0.08] hover:text-white"
          : "bg-white/[0.06] border-white/[0.16] text-white hover:bg-white/[0.10]",
      )}
    >
      <div
        className={cn(
          "w-8 h-8 rounded-lg bg-gradient-to-br flex items-center justify-center text-white text-[11px] font-bold shrink-0",
          grad,
        )}
      >
        {(account.platform[0] ?? "?").toUpperCase()}
      </div>
      <div className="min-w-0 flex-1">
        <p className="text-[12px] font-semibold truncate">{account.username || account.platform_user_id}</p>
        <p className="text-[10px] text-[#9aa0aa] truncate">{account.platform}</p>
      </div>
      <StatusIcon
        size={14}
        className={cn(
          "shrink-0",
          account.status === "active" ? "text-emerald-400" : "text-amber-400",
        )}
      />
    </button>
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
  const [busy, setBusy] = useState<null | "reconnect" | "validate" | "remove">(null);
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
    action: "reconnect" | "validate" | "remove",
    method: string,
    endpoint: string,
    body?: string,
  ) => {
    if (action === "remove" && !window.confirm(`Disconnect ${account.platform} @${account.username}? This will cancel scheduled posts targeting this account.`)) {
      return;
    }
    setBusy(action);
    try {
      await authedFetch(endpoint, { method, body });
      onUpdated();
      if (action === "remove") onClose();
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

      {/* Actions */}
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
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
        <ActionTile
          icon={Trash2}
          label="Disconnect"
          description="Removes this account and its tokens."
          onClick={() => void runAction("remove", "DELETE", `/api/v1/accounts/${account.id}`)}
          busy={busy === "remove"}
          danger
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
  danger,
}: {
  icon: ElementType;
  label: string;
  description: string;
  onClick: () => void;
  busy: boolean;
  danger?: boolean;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={busy}
      className={cn(
        "text-left p-4 rounded-xl border transition-colors",
        danger
          ? "bg-red-500/[0.08] border-red-500/30 hover:bg-red-500/[0.16] hover:border-red-500/50"
          : "bg-white/[0.06] border-white/[0.12] hover:bg-white/[0.10] hover:border-white/[0.20]",
        busy && "opacity-60 cursor-progress",
      )}
    >
      <div className="flex items-center gap-2 mb-1">
        <Icon size={16} className={danger ? "text-red-300" : "text-white"} />
        <span className={cn("text-[14px] font-bold", danger ? "text-red-200" : "text-white")}>
          {label}
        </span>
        {busy && <RefreshCw size={12} className="animate-spin text-[#9aa0aa] ml-auto" />}
      </div>
      <p className="text-[12px] text-[#9aa0aa] leading-snug">{description}</p>
    </button>
  );
}
