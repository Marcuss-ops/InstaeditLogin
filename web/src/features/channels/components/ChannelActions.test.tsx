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

  it("disconnects via DELETE /accounts/{id} on confirm", async () => {
    const onDone = vi.fn();
    render(<ChannelActions account={youtubeAccount} onDone={onDone} />);
    fireEvent.click(
      screen.getByRole("button", { name: /Disconnetti canale/i }),
    );
    await waitFor(() =>
      expect(authedFetchMock).toHaveBeenCalledWith("/api/v1/accounts/21", {
        method: "DELETE",
      }),
    );
    expect(onDone).toHaveBeenCalledTimes(1);
  });

  it("permanent delete hits DELETE /accounts/{id}/data", async () => {
    const onDone = vi.fn();
    render(<ChannelActions account={youtubeAccount} onDone={onDone} />);
    fireEvent.click(
      screen.getByRole("button", { name: /Elimina definitivamente/i }),
    );
    await waitFor(() =>
      expect(authedFetchMock).toHaveBeenCalledWith(
        "/api/v1/accounts/21/data",
        { method: "DELETE" },
      ),
    );
    expect(onDone).toHaveBeenCalledTimes(1);
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
});
