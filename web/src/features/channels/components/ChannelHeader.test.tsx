/**
 * ChannelHeader vitest coverage.
 *
 * Goals:
 *   1. Renders the avatar + name + status badge when an account is
 *      loaded (status chip colour follows the status string).
 *   2. Falls back to the initial-letter avatar when `resource.avatar_url`
 *      is missing.
 *   3. Loading state never throws + never shows the status chip
 *      (avoids a "UNKNOWN" flash on first mount).
 *   4. Refresh button fires `onRefresh` exactly once per click and is
 *      disabled while loading or while `refreshing=true`.
 *   5. "Torna ai canali" button fires `onBack` exactly once per click.
 *
 * Skipped: visual style assertions (snapshot diffs are timely and
 * noisy without a visual-regression baseline — left to manual QA).
 */
import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { ChannelHeader } from "./ChannelHeader";
import type { ChannelAccount } from "../types";

const ACCOUNT_LOADED: ChannelAccount = {
  id: 123,
  platform: "youtube",
  platform_user_id: "yt_abc",
  username: "demo-channel",
  status: "active",
  created_at: "2026-01-01T00:00:00.000Z",
  resource: {
    display_name: "Demo Channel",
    handle: "@demo",
    avatar_url: "https://cdn.example.test/avatar.png",
    banner_url: "https://cdn.example.test/banner.png",
    public_url: "https://youtu.be/demo",
  },
};

describe("ChannelHeader", () => {
  it("renders the channel name, handle, and an ACTIVE status badge", () => {
    render(
      <ChannelHeader
        account={ACCOUNT_LOADED}
        onRefresh={() => {}}
        onBack={() => {}}
      />,
    );
    expect(
      screen.getByTestId("channel-header-name"),
    ).toHaveTextContent("Demo Channel");
    expect(
      screen.getByTestId("channel-header-handle"),
    ).toHaveTextContent("@demo");
    const status = screen.getByTestId("channel-header-status");
    expect(status).toHaveTextContent("ACTIVE");
  });

  it("uses the fallback initial avatar when avatar_url is absent", () => {
    const account: ChannelAccount = {
      ...ACCOUNT_LOADED,
      resource: {
        display_name: "NoAvatar",
        public_url: "https://youtu.be/noavatar",
      },
    };
    render(
      <ChannelHeader
        account={account}
        onRefresh={() => {}}
        onBack={() => {}}
      />,
    );
    expect(
      screen.getByTestId("channel-header-avatar-fallback"),
    ).toHaveTextContent("N");
  });

  it("falls back to a yellow-tinted AMBER chip for non-active statuses", () => {
    render(
      <ChannelHeader
        account={{ ...ACCOUNT_LOADED, status: "pending_reauth" }}
        onRefresh={() => {}}
        onBack={() => {}}
      />,
    );
    const chip = screen.getByTestId("channel-header-status");
    expect(chip).toHaveTextContent("PENDING_REAUTH");
    expect(chip.className).toContain("amber-500");
  });

  it("does not render the status badge while account is loading", () => {
    render(
      <ChannelHeader
        account={undefined}
        refreshing={false}
        onRefresh={() => {}}
        onBack={() => {}}
      />,
    );
    expect(screen.queryByTestId("channel-header-status")).toBeNull();
    expect(screen.getByText(/Caricamento canale/i)).toBeInTheDocument();
  });

  it("fires onRefresh exactly once per click", () => {
    const onRefresh = vi.fn();
    render(
      <ChannelHeader
        account={ACCOUNT_LOADED}
        onRefresh={onRefresh}
        onBack={() => {}}
      />,
    );
    fireEvent.click(screen.getByTestId("channel-header-refresh"));
    expect(onRefresh).toHaveBeenCalledTimes(1);
  });

  it("disables the refresh button while a refresh is in flight", () => {
    render(
      <ChannelHeader
        account={ACCOUNT_LOADED}
        refreshing={true}
        onRefresh={() => {}}
        onBack={() => {}}
      />,
    );
    expect(
      screen.getByTestId("channel-header-refresh"),
    ).toBeDisabled();
  });

  it("disables the refresh button while account is loading", () => {
    render(
      <ChannelHeader
        account={undefined}
        onRefresh={() => {}}
        onBack={() => {}}
      />,
    );
    expect(
      screen.getByTestId("channel-header-refresh"),
    ).toBeDisabled();
  });

  it("fires onBack exactly once per click on the back button", () => {
    const onBack = vi.fn();
    render(
      <ChannelHeader
        account={ACCOUNT_LOADED}
        onRefresh={() => {}}
        onBack={onBack}
      />,
    );
    fireEvent.click(screen.getByTestId("channel-header-back"));
    expect(onBack).toHaveBeenCalledTimes(1);
  });

  it("renders the public-link anchor with target=_blank + rel attrs", () => {
    render(
      <ChannelHeader
        account={ACCOUNT_LOADED}
        onRefresh={() => {}}
        onBack={() => {}}
      />,
    );
    const link = screen.getByTestId("channel-header-public-link");
    expect(link).toHaveAttribute("href", "https://youtu.be/demo");
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", expect.stringMatching(/noopener/));
  });

  it("falls back to youtube.com handle URL when public_url is missing", () => {
    const account: ChannelAccount = {
      ...ACCOUNT_LOADED,
      resource: { display_name: "No URL" },
    };
    render(
      <ChannelHeader
        account={account}
        onRefresh={() => {}}
        onBack={() => {}}
      />,
    );
    const link = screen.getByTestId("channel-header-public-link");
    expect(link).toHaveAttribute(
      "href",
      "https://youtube.com/@demo-channel",
    );
  });

  it("does not invent a YouTube link for a Google Drive account", () => {
    const account: ChannelAccount = {
      ...ACCOUNT_LOADED,
      platform: "google-drive",
      username: "drive-account",
      resource: { display_name: "Google Drive" },
    };
    render(
      <ChannelHeader
        account={account}
        onRefresh={() => {}}
        onBack={() => {}}
      />,
    );
    expect(screen.queryByTestId("channel-header-public-link")).toBeNull();
  });

  it("hides the whole metadata block when banner and resource are absent", () => {
    const account: ChannelAccount = {
      ...ACCOUNT_LOADED,
      resource: undefined,
    };
    render(
      <ChannelHeader
        account={account}
        onRefresh={() => {}}
        onBack={() => {}}
      />,
    );
    // No banner img, but the header shell still renders.
    expect(screen.queryByRole("img")).toBeNull();
    const header = screen.getByTestId("channel-header");
    expect(within(header).getByTestId("channel-header-name")).toHaveTextContent(
      // Falls back to .username when resource.display_name is absent.
      "demo-channel",
    );
  });
});
