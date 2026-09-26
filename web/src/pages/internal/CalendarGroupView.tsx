import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { AlertCircle, Folder, FolderOpen } from "lucide-react";
import { ApiError, AuthError, authedFetch } from "../../lib/auth";
import { listAllAccounts } from "../../features/channels/api/channelsApi";
import { GroupYouTubeVideos } from "./GroupYouTubeVideos";
import type { CalendarGroup } from "./calendarTypes";

type GroupLoadState = { kind: "loading" } | { kind: "ready"; groups: CalendarGroup[] } | { kind: "error"; message: string };

function flattenGroups(groups: CalendarGroup[]): Array<{ group: CalendarGroup; depth: number; path: string }> {
  const children = new Map<number | null, CalendarGroup[]>();
  for (const group of groups) {
    const parentID = group.parent_group_id ?? null;
    children.set(parentID, [...(children.get(parentID) ?? []), group]);
  }
  for (const rows of children.values()) rows.sort((a, b) => a.name.localeCompare(b.name));
  const result: Array<{ group: CalendarGroup; depth: number; path: string }> = [];
  const visit = (parentID: number | null, depth: number, parents: Set<number>, prefix: string) => {
    for (const group of children.get(parentID) ?? []) {
      if (parents.has(group.id)) continue;
      const path = prefix ? `${prefix} / ${group.name}` : group.name;
      result.push({ group, depth, path });
      const nextParents = new Set(parents);
      nextParents.add(group.id);
      visit(group.id, depth + 1, nextParents, path);
    }
  };
  visit(null, 0, new Set(), "");
  // Keep orphaned rows visible in case a parent was removed outside the UI.
  for (const group of groups) {
    if (!result.some((row) => row.group.id === group.id)) result.push({ group, depth: 0, path: group.name });
  }
  return result;
}

export function CalendarGroupView({ selectedGroupID, onSelectGroup }: { selectedGroupID: number | null; onSelectGroup: (id: number) => void }) {
  const [state, setState] = useState<GroupLoadState>({ kind: "loading" });
  const navigate = useNavigate();

  useEffect(() => {
    const controller = new AbortController();
    void Promise.all([
      authedFetch("/api/v1/groups/aggregate", { signal: controller.signal }),
      listAllAccounts({ signal: controller.signal }),
    ]).then(async ([response, accounts]) => {
      if (!response.ok) throw new Error("Impossibile caricare i gruppi dei canali.");
      const body = await response.json() as { groups?: CalendarGroup[] };
      const youtubeIDs = new Set(accounts.filter((account) => account.platform === "youtube").map((account) => account.id));
      const allGroups = body.groups ?? [];
      const hasYouTubeInBranch = (groupID: number, seen = new Set<number>()): boolean => {
        if (seen.has(groupID)) return false;
        seen.add(groupID);
        const group = allGroups.find((candidate) => candidate.id === groupID);
        if (!group) return false;
        if ((group.account_ids ?? []).some((accountID) => youtubeIDs.has(accountID))) return true;
        return allGroups.filter((candidate) => candidate.parent_group_id === groupID).some((child) => hasYouTubeInBranch(child.id, new Set(seen)));
      };
      const groups = allGroups.filter((group) => hasYouTubeInBranch(group.id));
      if (!controller.signal.aborted) setState({ kind: "ready", groups });
    })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        if (error instanceof AuthError) { navigate("/login", { replace: true }); return; }
        setState({ kind: "error", message: error instanceof ApiError ? error.message : error instanceof Error ? error.message : "Errore nel caricamento dei gruppi." });
      });
    return () => controller.abort();
  }, [navigate]);

  const rows = useMemo(() => state.kind === "ready" ? flattenGroups(state.groups) : [], [state]);
  const selected = rows.find((row) => row.group.id === selectedGroupID) ?? rows[0];
  const children = selected ? (state.kind === "ready" ? state.groups.filter((group) => group.parent_group_id === selected.group.id) : []) : [];
  const descendantIDs = selected && state.kind === "ready" ? state.groups
    .filter((group) => {
      let parentID = group.parent_group_id;
      const seen = new Set<number>();
      while (parentID != null && !seen.has(parentID)) {
        if (parentID === selected.group.id) return true;
        seen.add(parentID);
        parentID = state.groups.find((candidate) => candidate.id === parentID)?.parent_group_id;
      }
      return group.id === selected.group.id;
    })
    .flatMap((group) => group.account_ids ?? []) : [];
  const channelCount = new Set(descendantIDs).size;

  useEffect(() => {
    if (selected && selected.group.id !== selectedGroupID) onSelectGroup(selected.group.id);
  }, [onSelectGroup, selected, selectedGroupID]);

  return (
    <section className="grid min-h-0 flex-1 gap-4 overflow-hidden lg:grid-cols-[260px_minmax(0,1fr)]" data-testid="calendar-group-view">
      <nav aria-label="Gruppi dei canali YouTube" className="min-h-0 overflow-auto rounded-2xl border border-white/[0.10] bg-[#11111a] p-3">
        <h2 className="px-2 pb-2 text-xs font-bold uppercase tracking-[0.14em] text-white/45">Gruppi YouTube</h2>
        {state.kind === "loading" && <p className="px-2 py-3 text-sm text-white/45">Caricamento gruppi…</p>}
        {state.kind === "error" && <p role="alert" className="flex gap-2 px-2 py-3 text-sm text-red-200"><AlertCircle size={16} />{state.message}</p>}
        {state.kind === "ready" && rows.length === 0 && <p className="px-2 py-3 text-sm leading-5 text-white/45">Non ci sono gruppi con canali YouTube. Organizza i canali nella sezione Gruppi.</p>}
        {rows.map(({ group, depth, path }) => {
          const active = selected?.group.id === group.id;
          const hasChildren = state.kind === "ready" && state.groups.some((candidate) => candidate.parent_group_id === group.id);
          return <button key={group.id} type="button" title={path} aria-current={active ? "true" : undefined} onClick={() => onSelectGroup(group.id)} className={`flex w-full items-center gap-2 rounded-lg px-2 py-2 text-left text-sm ${active ? "bg-violet-400/15 text-white" : "text-white/65 hover:bg-white/[0.05] hover:text-white"}`} style={{ paddingLeft: `${10 + depth * 16}px` }}>
            {hasChildren ? <FolderOpen size={15} className="shrink-0 text-violet-200/65" /> : <Folder size={15} className="shrink-0 text-white/35" />}
            <span className="truncate">{group.name}</span>
          </button>;
        })}
      </nav>

      <div className="min-h-0 overflow-auto rounded-2xl border border-white/[0.10] bg-[#11111a] p-4 sm:p-5">
        {selected && <>
          <div className="mb-4 flex flex-wrap items-start justify-between gap-3">
            <div><p className="text-[11px] font-semibold uppercase tracking-[0.15em] text-white/40">{selected.path}</p><h2 className="mt-1 text-xl font-bold text-white">Pubblicazioni del gruppo</h2><p className="mt-1 text-xs text-white/45">Video dei canali del gruppo e dei sottogruppi, con stato e data di pubblicazione.</p></div>
            <div className="flex gap-2"><span className="rounded-lg border border-white/[0.08] px-2.5 py-1.5 text-xs text-white/55">{channelCount} canali</span><span className="rounded-lg border border-white/[0.08] px-2.5 py-1.5 text-xs text-white/55">{children.length} sottogruppi diretti</span></div>
          </div>
          {children.length > 0 && <div className="mb-5 flex flex-wrap gap-2" aria-label="Sottogruppi">
            {children.map((child) => <button key={child.id} type="button" onClick={() => onSelectGroup(child.id)} className="rounded-lg border border-violet-300/15 bg-violet-300/[0.05] px-3 py-2 text-xs font-medium text-violet-100/80 hover:bg-violet-300/[0.12]">Apri sottogruppo · {child.name}</button>)}
          </div>}
          <GroupYouTubeVideos key={selected.group.id} groupId={selected.group.id} groupName={selected.group.name} />
        </>}
        {state.kind === "ready" && rows.length === 0 && <p className="text-sm text-white/45">Crea un gruppo e assegna i canali YouTube per iniziare.</p>}
      </div>
    </section>
  );
}
