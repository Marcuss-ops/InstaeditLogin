/**
 * Canonical browser-storage keys owned by the InstaEditor SPA.
 *
 * These values are intentionally stable: changing a key is a data migration,
 * not a cosmetic rename. No legacy Dark Editor storage keys have been found in
 * the current or historical editor surfaces, so this module does not invent a
 * compatibility alias or copy unknown data.
 */
export const STORAGE_KEYS = {
  cookieConsent: "instaedit.cookie-consent.v1",
  lastGroupId: "instaedit:last-group-id",
  lastCalendarGroupId: "instaedit:last-calendar-group-id",
  chunkReloadAttempted: "instaedit:chunk-reload-attempted",
  dashboardAnalyticsPrefix: "dashboard.analytics.v1",
} as const;

export type StorageKey = (typeof STORAGE_KEYS)[keyof typeof STORAGE_KEYS];

/** Returns true for keys owned by this app's InstaEditor namespace. */
export function isInstaEditorStorageKey(key: string): boolean {
  return key.startsWith("instaedit:") ||
    key.startsWith("instaedit.") ||
    key === STORAGE_KEYS.dashboardAnalyticsPrefix ||
    key.startsWith(`${STORAGE_KEYS.dashboardAnalyticsPrefix}.`);
}
