export function getRomeTZ(): { offsetMinutes: number; label: string; abbr: string } {
  const now = new Date();
  const year = now.getFullYear();
  // EU DST: last Sunday of March (02:00 CET → 03:00 CEST)
  //          last Sunday of October (03:00 CEST → 02:00 CET)
  const marLast = new Date(year, 2, 31 - new Date(year, 2, 31).getDay());
  const octLast = new Date(year, 9, 31 - new Date(year, 9, 31).getDay());
  marLast.setHours(2, 0, 0, 0);
  octLast.setHours(3, 0, 0, 0);
  const isDST = now >= marLast && now < octLast;
  const offsetMinutes = isDST ? 120 : 60;
  const abbr = isDST ? "CEST" : "CET";
  const label = `Europe/Rome (${abbr}, UTC${isDST ? "+2" : "+1"})`;
  return { offsetMinutes, label, abbr };
}

export function isScheduleInPast(localDateTime: string): boolean {
  if (!localDateTime) return false;
  const d = new Date(localDateTime);
  if (isNaN(d.getTime())) return false;
  return d.getTime() <= Date.now();
}

export function localToUTC(localDateTime: string): string {
  const d = new Date(localDateTime);
  if (isNaN(d.getTime())) return "";
  return d.toISOString();
}
