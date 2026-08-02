import {
  Folder,
  FolderPlus,
  Plus,
  RefreshCw,
} from "lucide-react";
import { useNavigate } from "react-router-dom";
import { authedFetch } from "../../lib/auth";
import { ErrorState, EmptyState, Skeleton } from "../../components/feedback";
import { useGroupsData } from "./useGroupsData";
import { TreeView } from "./GroupsTreeView";
import { AccountDetailPanel, GroupDetailPanel } from "./GroupsDetailPanels";

export function GroupsPage() {
  const navigate = useNavigate();
  const {
    state,
    selectedGroupId,
    setSelectedGroupId,
    setSelectedAccountId,
    newGroupName,
    setNewGroupName,
    creatingGroup,
    load,
    handleCreateGroup,
    tree,
    selectedGroup,
    selectedAccount,
  } = useGroupsData();

  return (
    <div className="min-h-full p-4 sm:p-6 lg:p-8 bg-[#030308] text-[#e8e8ef]">
      <div className="max-w-7xl mx-auto">
        <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between mb-6">
          <div>
            <h1 className="text-[24px] sm:text-[28px] font-extrabold tracking-[-0.02em] text-white flex items-center gap-3">
              <Folder size={28} className="text-white/40" />
              Groups
            </h1>
            <p className="text-[14px] sm:text-[15px] text-[#9aa0aa] mt-1">
              Organize your social accounts into folders and sub-folders.
              Click a group to see its accounts, click an account for details and
              quick actions.
            </p>
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => void load()}
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors"
            >
              <RefreshCw size={14} /> Refresh
            </button>
          </div>
        </div>

        <div className="mb-6 flex flex-col gap-3 rounded-2xl border border-white/[0.08] bg-white/[0.025] p-3 sm:flex-row sm:items-center">
          <div className="flex min-w-0 flex-1 items-center gap-2 overflow-x-auto">
            {state.kind === "loading" && <span className="text-[12px] text-[#9aa0aa]">Caricamento gruppi…</span>}
            {state.kind === "error" && <span className="text-[12px] text-red-300">{state.message}</span>}
            {state.kind === "ready" && tree.length === 0 && <span className="text-[12px] text-[#9aa0aa]">Nessun gruppo creato.</span>}
            {flattenTree(tree).map((node) => (
              <button key={node.id} type="button" onClick={() => { setSelectedGroupId(node.id); setSelectedAccountId(null); }} className={`shrink-0 rounded-xl px-3 py-2 text-left transition-all ${selectedGroupId === node.id ? "bg-white text-black shadow-lg scale-105" : "text-white/45 hover:bg-white/[0.06] hover:text-white"}`}>
                <span className={`block truncate ${selectedGroupId === node.id ? "text-[17px] font-bold" : "text-[13px] font-medium"}`}>{node.name}</span>
                <span className={`block text-[10px] ${selectedGroupId === node.id ? "text-black/55" : "text-white/30"}`}>{node.accounts.length} canali</span>
              </button>
            ))}
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <input type="text" value={newGroupName} onChange={(e) => setNewGroupName(e.target.value)} placeholder="Nuovo gruppo" className="w-32 rounded-lg border border-white/[0.10] bg-black/20 px-2.5 py-2 text-[12px] text-white outline-none" aria-label="Nuovo gruppo" />
            <button type="button" onClick={() => void handleCreateGroup()} disabled={!newGroupName.trim() || creatingGroup} className="rounded-lg bg-white px-3 py-2 text-[12px] font-semibold text-black disabled:opacity-50"><Plus size={13} className="inline" /> Crea</button>
          </div>
        </div>

        <div className="grid grid-cols-1 gap-6">
          <div className="hidden">
            <div className="flex items-center justify-between mb-4">
              <h2 className="text-[14px] font-bold text-white uppercase tracking-wider">Folders</h2>
            </div>

            <div className="flex items-center gap-2 mb-4">
              <input
                type="text"
                value={newGroupName}
                onChange={(e) => setNewGroupName(e.target.value)}
                placeholder="New folder name..."
                className="flex-1 px-3 py-2 rounded-lg bg-white/[0.04] border border-white/[0.08] text-[13px] text-white placeholder:text-[#9aa0aa] focus:outline-none focus:ring-2 focus:ring-violet-500/40"
                aria-label="New group name"
              />
              <button
                type="button"
                onClick={() => void handleCreateGroup()}
                disabled={!newGroupName.trim() || creatingGroup}
                className="inline-flex items-center gap-1 px-3 py-2 rounded-lg bg-white text-black text-[12px] font-semibold disabled:opacity-50 disabled:cursor-not-allowed hover:bg-zinc-100 transition-colors"
              >
                <Plus size={14} /> Add
              </button>
            </div>

            {state.kind === "loading" && (
              <div className="space-y-2">
                <Skeleton variant="card" height={28} />
                <Skeleton variant="card" height={28} />
                <Skeleton variant="card" height={28} />
              </div>
            )}

            {state.kind === "error" && (
              <ErrorState
                title="Couldn't load groups"
                message={state.message}
                onRetry={() => void load()}
                className="bg-transparent border-0 p-0"
              />
            )}

            {state.kind === "ready" && tree.length === 0 && (
              <EmptyState
                title="No folders yet"
                description="Add your first folder to start organizing your accounts."
                icon={<FolderPlus size={28} />}
                className="bg-transparent border-0 p-0"
              />
            )}

            {state.kind === "ready" && tree.length > 0 && (
              <TreeView
                nodes={tree}
                selectedGroupId={selectedGroupId}
                onSelect={(id) => {
                  setSelectedGroupId(id);
                  setSelectedAccountId(null);
                }}
              />
            )}
          </div>

          <div className="surface-card bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-5 min-h-[300px]">
            {selectedAccount ? (
              <AccountDetailPanel
                account={selectedAccount}
                onClose={() => setSelectedAccountId(null)}
                onUpdated={() => void load()}
              />
            ) : selectedGroup ? (
              <GroupDetailPanel
                group={selectedGroup}
                onPickAccount={(id) => {
                  setSelectedAccountId(id);
                  navigate(`/app/dashboard-channels/${id}`);
                }}
                onCreateSubgroup={(name) => {
                  if (!name.trim()) return;
                  setNewGroupName(name);
                  void handleCreateGroup(selectedGroup.id);
                }}
                onDeleteGroup={async () => {
                  if (!window.confirm(`Delete folder "${selectedGroup.name}"? Sub-folders and account links will be removed.`)) return;
                  try {
                    await authedFetch(`/api/v1/groups/${selectedGroup.id}`, { method: "DELETE" });
                    setSelectedGroupId(null);
                    await load();
                  } catch {
                    /* toasted by authedFetch */
                  }
                }}
                onSaved={() => load()}
              />
            ) : (
              <div className="flex h-full min-h-[260px] items-center justify-center text-center text-[#9aa0aa] text-[14px]">
                <div>
                  <Folder size={32} className="mx-auto opacity-50 mb-3" />
                  <p className="max-w-md">Select a folder to view its accounts,</p>
                  <p>or click an account for quick actions.</p>
                </div>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

function flattenTree(nodes: import("./groupsTypes").TreeNode[]): import("./groupsTypes").TreeNode[] {
  return nodes.flatMap((node) => [node, ...flattenTree(node.children)]);
}
