export function formatDateTime(date: Date): string {
  if (Number.isNaN(date.getTime())) return "—";
  const diffMs = date.getTime() - Date.now();
  const absMinutes = Math.round(Math.abs(diffMs) / 60_000);
  // Decide the main unit string first, then attach a direction-aware
  // prefix. Promoting the <1 min case to a single "just now" prevents
  // the `in just now` / `just now ago` artifacts when the slug falls
  // below the rounding threshold in either direction.
  let rel: string;
  if (absMinutes < 1) rel = "just now";
  else if (absMinutes < 60) rel = `${absMinutes} min`;
  else if (absMinutes < 24 * 60) rel = `${Math.round(absMinutes / 60)} h`;
  else rel = `${Math.round(absMinutes / (60 * 24))} d`;
  const relText =
    absMinutes < 1
      ? "just now"
      : diffMs >= 0
        ? `in ${rel}`
        : `${rel} ago`;
  const absolute = date.toLocaleString();
  return `${relText} · ${absolute}`;
}

export function formatRelHours(hours: number): string {
  const sign = hours < 0 ? "-" : "+";
  const abs = Math.abs(hours);
  if (abs < 1) {
    const minutes = Math.round(abs * 60);
    return `${sign}${minutes}m`;
  }
  if (abs < 24) {
    return `${sign}${abs.toFixed(1)}h`;
  }
  return `${sign}${(abs / 24).toFixed(1)}d`;
}
