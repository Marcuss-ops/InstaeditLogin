import { describe, expect, it } from "vitest";
import {
  accountStateLabel,
  isPublishableAccount,
  type PlatformAccount,
} from "./uploads";

const base: PlatformAccount = {
  id: 1,
  platform: "youtube",
  platform_user_id: "UC1",
  username: "channel",
  status: "active",
  created_at: "2026-01-01T00:00:00Z",
};

describe("account state helpers", () => {
  it("honors the backend publishability decision", () => {
    expect(isPublishableAccount({ ...base, status: "active", is_publishable: true })).toBe(true);
    expect(isPublishableAccount({ ...base, status: "active", is_publishable: false })).toBe(false);
    expect(isPublishableAccount({ ...base, status: "suspended", is_publishable: false })).toBe(false);
  });

  it("falls back to legacy status while rolling out the new field", () => {
    expect(isPublishableAccount({ ...base, status: "active", is_publishable: undefined })).toBe(true);
    expect(isPublishableAccount({ ...base, status: "reauth_required", is_publishable: undefined })).toBe(false);
  });

  it.each([
    ["valid", "Valid"],
    ["reconnect_required", "Reconnect required"],
    ["suspended", "Suspended"],
    ["deleted", "Deleted"],
  ] as const)("labels %s", (state, label) => {
    expect(accountStateLabel({ account_state: state, status: state })).toBe(label);
  });
});
