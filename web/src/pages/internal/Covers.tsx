import { useEffect, useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { Image as ImageIcon } from "lucide-react";
import { useGroupsData } from "./useGroupsData";
import { GroupCovers } from "./GroupCovers";
import { EmptyState } from "../../components/feedback/EmptyState";
import { groupAccent } from "./groupAccent";
import { cn } from "../../lib/utils";
import type { TreeNode } from "./groupsTypes";

/**
 * Copertine hub — the in-app covers workspace.
 *
 * Pick a group, see every cover project created in it (current +
 * archived history, with rendered previews), and open a cover in
 * InstaEditor in a new tab — the SPA never navigates away.
 *
 * Supports `?group=<id>` as a deep-link: the editor launch flow
 * stamps a relative `return_to` (e.g. `/app/covers?group=7`) so the
 * editor Home pill can bring the user straight back to the group's
 * hub they were working on.
 */
export function CoversPage() {
  const { state, tree, selectedGroupId, setSelectedGroupId } = useGroupsData();
  const allGroups = useMemo(() => flattenTree(tree), [tree]);
  const [searchParams] = useSearchParams();
  const requestedGroupId = Number(searchParams.get("group"));

  // Deep-link: when the hub opens with ?group=<id> (return from the
  // editor, or a shared bookmark), preselect that group once its data
  // is ready, so the grid below shows the covers of the group the
  // user actually clicked before.
  useEffect(() => {
    if (state.kind !== "ready") return;
    if (!Number.isInteger(requestedGroupId) || requestedGroupId <= 0) return;
    if (!allGroups.some((node) => node.id === requestedGroupId)) return;
    setSelectedGroupId(requestedGroupId);
  }, [allGroups, requestedGroupId, setSelectedGroupId, state.kind]);

  const selectedGroup = allGroups.find((node) => node.id === selectedGroupId) ?? null;

  return (
    <div className="min-h-full w-full bg-[#030308] p-4 text-[#e8e8ef] sm:p-6 lg:p-8">
      <div className="mx-auto w-full max-w-[1600px]">
        <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h1 className="flex items-center gap-3 text-[24px] font-extrabold tracking-[-0.02em] text-white sm:text-[28px]">
              <ImageIcon size={28} className="text-white/40" aria-hidden="true" />
              Copertine
            </h1>
            <p className="mt-1 text-[14px] text-[#9aa0aa] sm:text-[15px]">
              Seleziona un gruppo per vedere le copertine create al suo interno, comprese
              quelle vecchie e archiviate: modificale in InstaEditor senza lasciare l'app.
            </p>
          </div>
        </div>

        <div className="mb-6 rounded-2xl border border-white/[0.08] bg-white/[0.025] p-3">
          <div className="scrollbar-none flex min-w-0 flex-1 items-center gap-2 overflow-x-auto">
            {state.kind === "loading" && (
              <span className="text-[12px] text-[#9aa0aa]">Caricamento gruppi…</span>
            )}
            {state.kind === "error" && (
              <span className="text-[12px] text-red-300">{state.message}</span>
            )}
            {state.kind === "ready" && allGroups.length === 0 && (
              <span className="text-[12px] text-[#9aa0aa]">
                Nessun gruppo creato: creane uno nella pagina Groups.
              </span>
            )}
            {allGroups.map((node) => (
              <button
                key={node.id}
                type="button"
                onClick={() => setSelectedGroupId(node.id)}
                className={cn(
                  "shrink-0 rounded-xl px-3 py-2 text-left transition-all",
                  selectedGroupId === node.id
                    ? "bg-white text-black shadow-lg scale-105"
                    : "text-white/45 hover:bg-white/[0.06] hover:text-white",
                )}
              >
                <span className={`flex items-center gap-1.5 truncate ${selectedGroupId === node.id ? "text-[17px] font-bold" : "text-[13px] font-medium"}`}>
                  <span
                    aria-hidden="true"
                    className="inline-block h-2 w-2 shrink-0 rounded-full"
                    style={{ backgroundColor: groupAccent(node.name).text }}
                  />
                  <span className="truncate">{node.name}</span>
                </span>
                <span className={`block text-[10px] ${selectedGroupId === node.id ? "text-black/55" : "text-white/30"}`}>
                  {node.accounts.length} canali
                </span>
              </button>
            ))}
          </div>
        </div>

        {selectedGroup ? (
          <GroupCovers groupId={selectedGroup.id} groupName={selectedGroup.name} />
        ) : (
          <EmptyState
            title="Seleziona un gruppo"
            description="Scegli un gruppo dalla barra sopra per vedere le copertine create al suo interno."
            icon={<ImageIcon size={24} />}
            className="mx-auto max-w-sm bg-white/[0.02] py-10 border-white/[0.08]"
          />
        )}
      </div>
    </div>
  );
}
function flattenTree(nodes: TreeNode[]): TreeNode[] {
  return nodes.flatMap((node) => [node, ...flattenTree(node.children)]);
}
