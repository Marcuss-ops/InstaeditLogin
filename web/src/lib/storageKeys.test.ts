import { describe, expect, it } from "vitest";
import { isInstaEditorStorageKey, STORAGE_KEYS } from "./storageKeys";

describe("STORAGE_KEYS", () => {
  it("keeps every app-owned key in the canonical InstaEditor namespace", () => {
    expect(STORAGE_KEYS.cookieConsent).toBe("instaedit.cookie-consent.v1");
    expect(STORAGE_KEYS.lastGroupId).toBe("instaedit:last-group-id");
    expect(STORAGE_KEYS.lastCalendarGroupId).toBe("instaedit:last-calendar-group-id");
    expect(STORAGE_KEYS.chunkReloadAttempted).toBe("instaedit:chunk-reload-attempted");
    expect(STORAGE_KEYS.dashboardAnalyticsPrefix).toBe("dashboard.analytics.v1");

    for (const key of Object.values(STORAGE_KEYS)) {
      expect(isInstaEditorStorageKey(key)).toBe(true);
      expect(key.toLowerCase()).not.toMatch(/dark[ _-]?editor/);
    }
  });

  it("recognizes dashboard analytics entries and rejects foreign keys", () => {
    expect(isInstaEditorStorageKey("dashboard.analytics.v1.42.28")).toBe(true);
    expect(isInstaEditorStorageKey("dark-editor.settings")).toBe(false);
    expect(isInstaEditorStorageKey("dark_editor_theme")).toBe(false);
    expect(isInstaEditorStorageKey("unrelated.application.key")).toBe(false);
  });
});
