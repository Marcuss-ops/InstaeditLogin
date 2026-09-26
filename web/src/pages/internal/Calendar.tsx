import { useCallback, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Calendar as CalendarIcon, Cpu, FolderTree, Plus } from "lucide-react";
import { CalendarGroupView } from "./CalendarGroupView";
import { CalendarScheduleView } from "./CalendarScheduleView";
import { RemoteJobDialog } from "./RemoteJobDialog";

type CalendarMode = "groups" | "calendar";

export function CalendarPage() {
  const [mode, setMode] = useState<CalendarMode>("groups");
  const [jobDialogOpen, setJobDialogOpen] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);
  const [searchParams, setSearchParams] = useSearchParams();
  const groupID = Number(searchParams.get("group_id"));
  const selectedGroupID = Number.isSafeInteger(groupID) && groupID > 0 ? groupID : null;
  const selectGroup = useCallback((id: number) => {
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      next.set("group_id", String(id));
      return next;
    }, { replace: true });
  }, [setSearchParams]);

  return (
    <div className="min-h-full bg-[#030308] p-4 text-[#e8e8ef] sm:p-6 lg:p-8">
      <div className="flex min-h-[calc(100vh-64px-2rem)] w-full flex-col">
        <div className="mb-5 flex shrink-0 flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h1 className="flex items-center gap-3 text-[24px] font-extrabold tracking-[-0.02em] text-white sm:text-[28px]">
              {mode === "groups" ? <FolderTree size={27} className="text-white/40" /> : <CalendarIcon size={27} className="text-white/40" />}
              Pubblicazioni YouTube
            </h1>
            <p className="mt-1 text-[14px] text-[#9aa0aa]">Seleziona un gruppo per vedere i canali, i sottogruppi e i video pubblicati o programmati.</p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <div className="flex rounded-xl border border-white/[0.10] bg-white/[0.03] p-1" role="group" aria-label="Vista pubblicazioni">
              <button type="button" aria-pressed={mode === "groups"} onClick={() => setMode("groups")} className={`rounded-lg px-3 py-2 text-xs font-semibold ${mode === "groups" ? "bg-white text-black" : "text-white/60 hover:text-white"}`}><FolderTree size={14} className="mr-1.5 inline" />Gruppi</button>
              <button type="button" aria-pressed={mode === "calendar"} onClick={() => setMode("calendar")} className={`rounded-lg px-3 py-2 text-xs font-semibold ${mode === "calendar" ? "bg-white text-black" : "text-white/60 hover:text-white"}`}><CalendarIcon size={14} className="mr-1.5 inline" />Calendario</button>
            </div>
            <button type="button" onClick={() => setJobDialogOpen(true)} className="inline-flex items-center gap-1.5 rounded-xl border border-white/[0.12] bg-white/[0.04] px-4 py-2 text-[13px] font-semibold text-white hover:bg-white/[0.08]">
              <Cpu size={16} /> Invia job
            </button>
            <Link to="/app/compose" className="inline-flex items-center gap-1.5 rounded-xl bg-white px-4 py-2 text-[13px] font-semibold text-black no-underline hover:bg-white/90">
              <Plus size={16} /> Nuovo post
            </Link>
          </div>
        </div>

        {mode === "groups"
          ? <CalendarGroupView key={`groups-${refreshKey}`} selectedGroupID={selectedGroupID} onSelectGroup={selectGroup} />
          : <CalendarScheduleView key={`calendar-${refreshKey}`} />}
      </div>
      <RemoteJobDialog open={jobDialogOpen} onClose={() => setJobDialogOpen(false)} onCalendarRefresh={() => setRefreshKey((key) => key + 1)} />
    </div>
  );
}
