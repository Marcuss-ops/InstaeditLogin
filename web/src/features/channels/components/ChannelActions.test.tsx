import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ChannelActions, type ChannelActionsAccount } from "./ChannelActions";

const { authedFetchMock } = vi.hoisted(() => ({
  authedFetchMock: vi.fn(),
}));

vi.mock("../../../lib/auth", () => ({
  authedFetch: authedFetchMock,
}));

const youtubeAccount: ChannelActionsAccount = {
  id: 21,
  platform: "youtube",
  username: "wwe-channel",
};

const tiktokAccount: ChannelActionsAccount = {
  id: 22,
  platform: "tiktok",
  username: "tt-channel",
};

describe("ChannelActions", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    authedFetchMock.mockReset();
    authedFetchMock.mockResolvedValue({
      ok: true,
      status: 204,
      json: async () => ({}),
    });
    vi.spyOn(window, "confirm").mockReturnValue(true);
    // Permanent delete requires typing the exact channel name into a
    // prompt — auto-confirm it so the request is actually sent.
    vi.spyOn(window, "prompt").mockReturnValue(youtubeAccount.username);
  });

  it("shows disconnect and permanent-delete for every platform", () => {
    render(<ChannelActions account={tiktokAccount} onDone={() => {}} />);
    expect(
      screen.getByRole("button", { name: /Disconnetti canale/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /Elimina definitivamente/i }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Revoca account Google/i }),
    ).not.toBeInTheDocument();
  });

  it("shows the shared-grant revoke only for YouTube accounts", () => {
    render(<ChannelActions account={youtubeAccount} onDone={() => {}} />);
    expect(
      screen.getByRole("button", {
        name: /Revoca account Google e tutti i canali/i,
      }),
    ).toBeInTheDocument();
  });

  it("disconnects via POST /accounts/{id}/disconnect on confirm", async () => {
    const onDone = vi.fn();
    render(<ChannelActions account={youtubeAccount} onDone={onDone} />);
    fireEvent.click(
      screen.getByRole("button", { name: /Disconnetti canale/i }),
    );
    await waitFor(() =>
      expect(authedFetchMock).toHaveBeenCalledWith(
        "/api/v1/accounts/21/disconnect",
        { method: "POST" },
      ),
    );
    expect(onDone).toHaveBeenCalledTimes(1);
  });

  it("permanent delete hits DELETE /accounts/{id}/data with the exact-name confirmation", async () => {
    const promptMock = vi.spyOn(window, "prompt").mockReturnValue("wwe-channel");
    const onDone = vi.fn();
    render(<ChannelActions account={youtubeAccount} onDone={onDone} />);
    fireEvent.click(
      screen.getByRole("button", { name: /Elimina definitivamente/i }),
    );
    await waitFor(() =>
      expect(authedFetchMock).toHaveBeenCalledWith(
        "/api/v1/accounts/21/data",
        {
          method: "DELETE",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ confirmation: "wwe-channel" }),
        },
      ),
    );
    expect(promptMock).toHaveBeenCalled();
    expect(onDone).toHaveBeenCalledTimes(1);
  });

  it("permanent delete is NOT sent when the typed confirmation does not match", async () => {
    vi.spyOn(window, "prompt").mockReturnValue("wrong-name");
    render(<ChannelActions account={youtubeAccount} onDone={() => {}} />);
    fireEvent.click(
      screen.getByRole("button", { name: /Elimina definitivamente/i }),
    );
    // No fetch happens — the mismatch short-circuits before the request.
    await new Promise((r) => setTimeout(r, 0));
    expect(authedFetchMock).not.toHaveBeenCalledWith(
      "/api/v1/accounts/21/data",
      expect.anything(),
    );
  });

  it("revoking the shared grant hits DELETE /accounts/{id}/oauth-grant", async () => {
    const onDone = vi.fn();
    render(<ChannelActions account={youtubeAccount} onDone={onDone} />);
    fireEvent.click(
      screen.getByRole("button", {
        name: /Revoca account Google e tutti i canali/i,
      }),
    );
    await waitFor(() =>
      expect(authedFetchMock).toHaveBeenCalledWith(
        "/api/v1/accounts/21/oauth-grant",
        { method: "DELETE" },
      ),
    );
    expect(onDone).toHaveBeenCalledTimes(1);
  });

  it("does nothing when the user cancels the confirmation", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(false);
    render(<ChannelActions account={youtubeAccount} onDone={() => {}} />);
    fireEvent.click(
      screen.getByRole("button", { name: /Disconnetti canale/i }),
    );
    expect(authedFetchMock).not.toHaveBeenCalled();
  });

  // P0 (account-lifecycle audit): the labels and confirmations must stay
  // honest and distinct — the old "Removes this account and its tokens"
  // description was misleading (the backend keeps the row and preserves
  // shared grants). This test pins the required wording so a regression
  // cannot silently reintroduce the fake description.
  it("uses honest, distinct confirmation texts (never 'tokens' in labels)", async () => {
    const confirmMock = vi.spyOn(window, "confirm").mockReturnValue(true);
    confirmMock.mockClear();
    render(<ChannelActions account={youtubeAccount} onDone={() => {}} />);

    // Disconnect confirmation carries the three required lines verbatim.
    fireEvent.click(
      screen.getByRole("button", { name: /Disconnetti canale/i }),
    );
    const disconnectMsg = confirmMock.mock.calls[0]?.[0] ?? "";
    expect(disconnectMsg).toContain(
      "Il canale non sarà più utilizzabile da InstaEdit",
    );
    expect(disconnectMsg).toContain("La cronologia verrà conservata");
    expect(disconnectMsg).toContain(
      "Gli altri canali dello stesso account Google non saranno interessati",
    );
    // The disconnect path must not threaten token deletion: siblings share
    // the grant and the row is kept for audit.
    expect(disconnectMsg).not.toMatch(/token/i);

    // Wait for the disconnect request to finish so the busy state resets
    // and the other tiles are clickable again.
    await waitFor(() =>
      expect(authedFetchMock).toHaveBeenCalledWith(
        "/api/v1/accounts/21/disconnect",
        { method: "POST" },
      ),
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /Elimina definitivamente/i }),
      ).not.toBeDisabled(),
    );

    // Permanent delete asks for an exact-name typed confirmation and
    // states the irreversibility (honest, distinct wording).
    const promptMock = vi.spyOn(window, "prompt");
    fireEvent.click(
      screen.getByRole("button", { name: /Elimina definitivamente/i }),
    );
    const deleteMsg = promptMock.mock.calls[0]?.[0] ?? "";
    expect(deleteMsg).toContain("wwe-channel");
    expect(deleteMsg).toContain("digita esattamente");
    // The visible tile description states irreversibility without
    // threatening the shared grant's siblings.
    expect(screen.getByText(/Azione irreversibile/i)).toBeInTheDocument();

    // No visible label or description uses the old misleading vocabulary.
    expect(screen.queryByText(/tokens/i)).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Removes this account/i }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Disconnect/i }),
    ).not.toBeInTheDocument();
  });
});
