import { useEffect, useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { ChevronRight, FolderKanban, Image as ImageIcon, Layers3, Sparkles, UsersRound } from "lucide-react";
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
    <div className="relative min-h-full w-full overflow-hidden bg-[#06070b] text-[#e8e8ef]">
      <div
        aria-hidden="true"
        className="pointer-events-none absolute inset-0 opacity-80"
        style={{
          background:
            "radial-gradient(circle at 78% 0%, rgba(124, 58, 237, 0.16), transparent 30%), radial-gradient(circle at 15% 35%, rgba(14, 165, 233, 0.08), transparent 26%)",
        }}
      />
      <div className="relative mx-auto w-full max-w-[1480px] px-4 py-6 sm:px-6 sm:py-8 lg:px-8 lg:py-10">
        <header className="mb-8 flex flex-col gap-5 border-b border-white/[0.08] pb-7 lg:flex-row lg:items-end lg:justify-between">
          <div className="max-w-3xl">
            <div className="mb-3 flex items-center gap-2 text-[11px] font-bold uppercase tracking-[0.2em] text-violet-300/80">
              <Sparkles size={14} aria-hidden="true" />
              Workspace creativo
            </div>
            <h1 className="flex items-center gap-3 text-[30px] font-extrabold tracking-[-0.04em] text-white sm:text-[38px]">
              <span className="flex h-11 w-11 items-center justify-center rounded-2xl border border-violet-300/20 bg-gradient-to-br from-violet-400/25 to-sky-400/10 text-violet-200 shadow-[0_12px_30px_rgba(124,58,237,0.18)]">
                <ImageIcon size={22} aria-hidden="true" />
              </span>
              Copertine
            </h1>
            <p className="mt-3 max-w-2xl text-[14px] leading-6 text-[#9aa0aa] sm:text-[15px]">
              Organizza e aggiorna le copertine dei tuoi canali in un unico spazio.
              Scegli un gruppo, apri un progetto o creane uno nuovo in InstaEditor.
            </p>
          </div>
          <div className="hidden items-center gap-2 rounded-2xl border border-white/[0.08] bg-white/[0.03] px-4 py-3 text-xs text-[#9aa0aa] lg:flex">
            <Layers3 size={16} className="text-violet-300" aria-hidden="true" />
            <span>Progetti per gruppo</span>
          </div>
        </header>

        <div className="grid gap-6 lg:grid-cols-[248px_minmax(0,1fr)] lg:items-start lg:gap-8">
          <aside className="lg:sticky lg:top-6" aria-label="Selezione gruppo">
            <div className="overflow-hidden rounded-2xl border border-white/[0.09] bg-[#0d0f15]/90 shadow-[0_18px_50px_rgba(0,0,0,0.18)]">
              <div className="border-b border-white/[0.08] px-4 py-4">
                <div className="flex items-center justify-between gap-3">
                  <div className="flex items-center gap-2.5">
                    <span className="flex h-8 w-8 items-center justify-center rounded-xl bg-white/[0.06] text-white/65">
                      <FolderKanban size={16} aria-hidden="true" />
                    </span>
                    <div>
                      <p className="text-[13px] font-bold text-white">I tuoi gruppi</p>
                      <p className="mt-0.5 text-[11px] text-[#777e8b]">Scegli uno spazio</p>
                    </div>
                  </div>
                  {state.kind === "ready" && (
                    <span className="rounded-full border border-white/[0.10] bg-white/[0.05] px-2 py-0.5 text-[10px] font-bold text-[#aab0bb]">
                      {allGroups.length}
                    </span>
                  )}
                </div>
              </div>

              <div className="p-2">
                {state.kind === "loading" && (
                  <div className="px-3 py-4 text-[12px] text-[#9aa0aa]">Caricamento gruppi…</div>
                )}
                {state.kind === "error" && (
                  <div className="px-3 py-4 text-[12px] leading-5 text-red-300">{state.message}</div>
                )}
                {state.kind === "ready" && allGroups.length === 0 && (
                  <div className="px-3 py-4 text-[12px] leading-5 text-[#9aa0aa]">
                    Nessun gruppo creato. Creane uno nella pagina Groups.
                  </div>
                )}
                <nav className="space-y-1">
                  {allGroups.map((node) => {
                    const accent = groupAccent(node.name);
                    const active = selectedGroupId === node.id;
                    return (
                      <button
                        key={node.id}
                        type="button"
                        onClick={() => setSelectedGroupId(node.id)}
                        className={cn(
                          "group flex w-full items-center gap-3 rounded-xl border px-3 py-3 text-left transition-all duration-200",
                          active
                            ? "border-white/[0.12] bg-white/[0.08] text-white shadow-[0_8px_22px_rgba(0,0,0,0.16)]"
                            : "border-transparent text-[#9299a6] hover:border-white/[0.07] hover:bg-white/[0.045] hover:text-white",
                        )}
                        style={active ? { boxShadow: `inset 3px 0 0 ${accent.text}, 0 8px 22px rgba(0,0,0,0.16)` } : undefined}
                      >
                        <span className="flex h-8 w-8 shrink-0 items-center justify-center overflow-hidden rounded-lg" style={{ backgroundColor: accent.bg, color: accent.text }}>
                          {firstChannelAvatar(node) ? (
                            <img
                              src={firstChannelAvatar(node)}
                              alt=""
                              loading="lazy"
                              className="h-full w-full object-cover"
                            />
                          ) : (
                            <FolderKanban size={15} aria-hidden="true" />
                          )}
                        </span>
                        <span className="min-w-0 flex-1">
                          <span className="block truncate text-[13px] font-semibold">{node.name}</span>
                          <span className={cn("mt-0.5 flex items-center gap-1 text-[10px]", active ? "text-white/50" : "text-white/30")}>
                            <UsersRound size={10} aria-hidden="true" />
                            {node.accounts.length} {node.accounts.length === 1 ? "canale" : "canali"}
                          </span>
                        </span>
                        <ChevronRight size={14} className={cn("shrink-0 transition-transform", active ? "text-white/70" : "text-white/20 group-hover:translate-x-0.5 group-hover:text-white/50")} aria-hidden="true" />
                      </button>
                    );
                  })}
                </nav>
              </div>
            </div>
          </aside>

          <main className="min-w-0">
            {selectedGroup ? (
              <GroupCovers groupId={selectedGroup.id} groupName={selectedGroup.name} />
            ) : (
              <EmptyState
                title="Seleziona un gruppo"
                description="Scegli uno spazio dalla colonna laterale per vedere e creare le tue copertine."
                icon={<ImageIcon size={24} />}
                className="mx-auto max-w-xl border-white/[0.09] bg-[#0d0f15]/80 py-16 shadow-[0_18px_50px_rgba(0,0,0,0.16)]"
              />
            )}
          </main>
        </div>
      </div>
    </div>
  );
}
function flattenTree(nodes: TreeNode[]): TreeNode[] {
  return nodes.flatMap((node) => [node, ...flattenTree(node.children)]);
}

/**
 * Avatar of the first channel in the group that has one. The group
 * folder row shows a real channel avatar instead of a generic icon, so
 * users recognise the space at a glance; groups without any avatar keep
 * the folder glyph. Picks the first member deterministically (the
 * membership order returned by the aggregate) — no randomness.
 */
function firstChannelAvatar(node: TreeNode): string | undefined {
  return node.accounts.find((account) => account.avatar_url)?.avatar_url;
}
