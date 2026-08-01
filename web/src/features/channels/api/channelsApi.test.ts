import { describe, expect, it } from "vitest";
import { filterYouTube } from "./channelsApi";
import type { PlatformAccount } from "../../../types/uploads";

const account = (overrides: Partial<PlatformAccount>): PlatformAccount => ({
  id: 1,
  platform: "youtube",
  platform_user_id: "UC1",
  username: "channel",
  status: "active",
  created_at: "2026-01-01T00:00:00Z",
  ...overrides,
});

describe("filterYouTube", () => {
  it("keeps only publishable YouTube accounts", () => {
    const result = filterYouTube([
      account({ id: 1, account_state: "valid", is_publishable: true }),
      account({ id: 2, account_state: "reconnect_required", is_publishable: false }),
      account({ id: 3, account_state: "suspended", is_publishable: false }),
      account({ id: 4, account_state: "deleted", is_publishable: false }),
      account({ id: 5, platform: "instagram", account_state: "valid", is_publishable: true }),
    ]);

    expect(result.map((item) => item.id)).toEqual([1]);
  });

  it("supports legacy responses before is_publishable was added", () => {
    const result = filterYouTube([
      account({ id: 1, status: "active", is_publishable: undefined }),
      account({ id: 2, status: "reauth_required", is_publishable: undefined }),
    ]);

    expect(result.map((item) => item.id)).toEqual([1]);
  });
});
