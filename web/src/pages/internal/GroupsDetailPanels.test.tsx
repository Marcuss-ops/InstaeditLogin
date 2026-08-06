import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { GroupDetailPanel } from "./GroupsDetailPanels";
import type { TreeNode } from "./groupsTypes";

const group: TreeNode = {
  id: 7,
  workspace_id: 9,
  name: "Editorial",
  children: [],
  accounts: [
    {
      id: 101,
      platform: "youtube",
      username: "channel-one",
      platform_user_id: "UC-one",
      status: "active",
      language: "en",
      created_at: "2026-01-01T00:00:00Z",
    },
    {
      id: 102,
      platform: "youtube",
      username: "channel-two",
      platform_user_id: "UC-two",
      status: "active",
      language: "fr",
      created_at: "2026-01-01T00:00:00Z",
    },
  ],
};

describe("GroupDetailPanel batch settings", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("requires confirmation and preserves the channel when cancelled", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 204, json: async () => ({}) });
    vi.stubGlobal("fetch", fetchMock);
    const confirmMock = vi.spyOn(window, "confirm").mockReturnValue(false);

    render(
      <MemoryRouter>
        <GroupDetailPanel
          group={group}
          onPickAccount={() => {}}
          onCreateSubgroup={() => {}}
          onDeleteGroup={() => {}}
          onSaved={() => {}}
          onRename={() => {}}
        />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole("button", { name: /Rimuovi channel-two dalla cartella/i }));

    expect(confirmMock).toHaveBeenCalledWith(expect.stringContaining('soltanto dalla cartella "Editorial"'));
    const confirmation = String(confirmMock.mock.calls[0]?.[0] ?? "");
    expect(confirmation).toContain("rimarrà collegato a InstaEdit");
    expect(confirmation).toContain("\n\n");
    expect(screen.getByRole("button", { name: /Rimuovi channel-two dalla cartella/i })).toHaveAttribute("title", expect.stringContaining("il canale resta collegato"));
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes("/api/v1/groups/7/accounts/102"))).toBe(false);
    expect(screen.getByRole("button", { name: /Rimuovi channel-two dalla cartella/i })).toBeInTheDocument();
  });

  it("persists a removed account immediately", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 204, json: async () => ({}) });
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const onSaved = vi.fn();

    render(
      <MemoryRouter>
        <GroupDetailPanel
          group={group}
          onPickAccount={() => {}}
          onCreateSubgroup={() => {}}
          onDeleteGroup={() => {}}
          onSaved={onSaved}
          onRename={() => {}}
        />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole("button", { name: /Rimuovi channel-two dalla cartella/i }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/v1/groups/7/accounts/102"),
      expect.objectContaining({ method: "DELETE" }),
    ));
    expect(onSaved).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes("/api/v1/accounts/102/disconnect"))).toBe(false);
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes("/api/v1/accounts/102/data"))).toBe(false);
    expect(screen.queryByRole("button", { name: /Rimuovi channel-two dalla cartella/i })).not.toBeInTheDocument();
  });

  it("renames the group inline and supports cancel", async () => {
    const onRename = vi.fn().mockResolvedValue(undefined);

    render(
      <MemoryRouter>
        <GroupDetailPanel
          group={group}
          onPickAccount={() => {}}
          onCreateSubgroup={() => {}}
          onDeleteGroup={() => {}}
          onSaved={() => {}}
          onRename={onRename}
        />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Rinomina gruppo" }));
    const input = screen.getByRole("textbox", { name: "Nome del gruppo" });
    fireEvent.change(input, { target: { value: "YouTube WWE" } });
    fireEvent.click(screen.getByRole("button", { name: "Annulla rinomina" }));
    expect(screen.queryByRole("textbox", { name: "Nome del gruppo" })).not.toBeInTheDocument();
    expect(onRename).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Rinomina gruppo" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Nome del gruppo" }), { target: { value: "YouTube WWE" } });
    fireEvent.click(screen.getByRole("button", { name: "Salva" }));
    await waitFor(() => expect(onRename).toHaveBeenCalledWith("YouTube WWE"));
  });

  it("validates rename input and keeps the editor open", async () => {
    const onRename = vi.fn().mockResolvedValue(undefined);

    render(
      <MemoryRouter>
        <GroupDetailPanel
          group={group}
          onPickAccount={() => {}}
          onCreateSubgroup={() => {}}
          onDeleteGroup={() => {}}
          onSaved={() => {}}
          onRename={onRename}
        />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Rinomina gruppo" }));
    const input = screen.getByRole("textbox", { name: "Nome del gruppo" });
    fireEvent.change(input, { target: { value: "   " } });
    fireEvent.click(screen.getByRole("button", { name: "Salva" }));
    expect(screen.getByRole("alert")).toHaveTextContent("obbligatorio");
    expect(onRename).not.toHaveBeenCalled();

    fireEvent.change(input, { target: { value: "x".repeat(81) } });
    fireEvent.click(screen.getByRole("button", { name: "Salva" }));
    expect(screen.getByRole("alert")).toHaveTextContent("80 caratteri");
    expect(onRename).not.toHaveBeenCalled();
  });

  it("prevents duplicate rename submits while saving", async () => {
    let resolveRename: (() => void) | undefined;
    const onRename = vi.fn(() => new Promise<void>((resolve) => { resolveRename = resolve; }));

    render(
      <MemoryRouter>
        <GroupDetailPanel
          group={group}
          onPickAccount={() => {}}
          onCreateSubgroup={() => {}}
          onDeleteGroup={() => {}}
          onSaved={() => {}}
          onRename={onRename}
        />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Rinomina gruppo" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Nome del gruppo" }), { target: { value: "YouTube WWE" } });
    fireEvent.click(screen.getByRole("button", { name: "Salva" }));
    await waitFor(() => expect(onRename).toHaveBeenCalledTimes(1));
    expect(screen.getByRole("button", { name: "Salvo…" })).toBeDisabled();
    fireEvent.submit(screen.getByRole("textbox", { name: "Nome del gruppo" }).closest("form")!);
    expect(onRename).toHaveBeenCalledTimes(1);

    resolveRename?.();
    await waitFor(() => expect(screen.queryByRole("textbox", { name: "Nome del gruppo" })).not.toBeInTheDocument());
  });

  it("shows a backend rename error without closing the editor", async () => {
    const onRename = vi.fn().mockRejectedValue(new Error("Nome già utilizzato"));

    render(
      <MemoryRouter>
        <GroupDetailPanel
          group={group}
          onPickAccount={() => {}}
          onCreateSubgroup={() => {}}
          onDeleteGroup={() => {}}
          onSaved={() => {}}
          onRename={onRename}
        />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Rinomina gruppo" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Nome del gruppo" }), { target: { value: "YouTube WWE" } });
    fireEvent.click(screen.getByRole("button", { name: "Salva" }));

    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Nome già utilizzato"));
    expect(screen.getByRole("textbox", { name: "Nome del gruppo" })).toBeInTheDocument();
  });  it("auto-saves uniquely detected title languages and flags generic titles for manual review", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });
    vi.stubGlobal("fetch", fetchMock);
    const sampleGroup: TreeNode = {
      ...group,
      accounts: [
        { ...group.accounts[0], username: "BoxeClubITA", language: "" },
        { ...group.accounts[1], username: "BoxeClubFr", language: "" },
        { ...group.accounts[0], id: 103, username: "BoxeClubEs", language: "" },
        { ...group.accounts[1], id: 104, username: "BoxeClubPt", language: "" },
        { ...group.accounts[0], id: 105, username: "RedGloveTR", language: "" },
        { ...group.accounts[1], id: 106, username: "RedGloveRU", language: "" },
        { ...group.accounts[0], id: 107, username: "BoxeClubDE", language: "" },
        { ...group.accounts[1], id: 108, username: "Boxing Prime", language: "" },
        { ...group.accounts[0], id: 109, username: "Boxing Zone", language: "" },
      ],
    };

    render(
      <MemoryRouter>
        <GroupDetailPanel
          group={sampleGroup}
          onPickAccount={() => {}}
          onCreateSubgroup={() => {}}
          onDeleteGroup={() => {}}
          onSaved={() => {}}
          onRename={() => {}}
        />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Analizza lingue dai titoli" }));
    // The seven uniquely-detectable channels are persisted immediately with
    // a single click — no second confirmation step is needed.
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(7));
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/v1/accounts/101"),
      expect.objectContaining({ method: "PATCH", body: JSON.stringify({ metadata: { language: "it" } }) }),
    );
    expect(screen.getByLabelText("Language for BoxeClubITA")).toHaveValue("it");
    expect(screen.getByLabelText("Language for BoxeClubFr")).toHaveValue("fr");
    expect(screen.getByLabelText("Language for BoxeClubEs")).toHaveValue("es");
    expect(screen.getByLabelText("Language for BoxeClubPt")).toHaveValue("pt");
    expect(screen.getByLabelText("Language for RedGloveTR")).toHaveValue("tr");
    expect(screen.getByLabelText("Language for RedGloveRU")).toHaveValue("ru");
    expect(screen.getByLabelText("Language for BoxeClubDE")).toHaveValue("de");
    expect(screen.getByLabelText("Avviso lingua: Lingua non determinabile per «Boxing Prime»: revisione manuale necessaria.")).toBeInTheDocument();
    expect(screen.getByLabelText("Avviso lingua: Lingua non determinabile per «Boxing Zone»: revisione manuale necessaria.")).toBeInTheDocument();
    expect(screen.getByText(/2 da verificare/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Applica suggerimenti/ })).not.toBeInTheDocument();
  });

  it("auto-saves unique title languages and flags ambiguous titles for manual review", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });
    vi.stubGlobal("fetch", fetchMock);
    const confirmMock = vi.spyOn(window, "confirm").mockReturnValue(false);
    const detectionGroup: TreeNode = {
      ...group,
      accounts: [
        { ...group.accounts[0], username: "WWE Italia", language: "" },
        { ...group.accounts[1], username: "Italia English Wrestling", language: "" },
      ],
    };

    render(
      <MemoryRouter>
        <GroupDetailPanel
          group={detectionGroup}
          onPickAccount={() => {}}
          onCreateSubgroup={() => {}}
          onDeleteGroup={() => {}}
          onSaved={() => {}}
          onRename={() => {}}
        />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Analizza lingue dai titoli" }));
    // "WWE Italia" has no configured language → its unique detection is
    // persisted immediately, without any overwrite confirmation.
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/v1/accounts/101"),
      expect.objectContaining({
        method: "PATCH",
        body: JSON.stringify({ metadata: { language: "it" } }),
      }),
    ));
    expect(confirmMock).not.toHaveBeenCalled();
    expect(screen.getByLabelText("Language for WWE Italia")).toHaveValue("it");
    expect(screen.getByLabelText("Avviso lingua: Titolo ambiguo: possibili lingue it, en.")).toBeInTheDocument();
    expect(screen.getByText(/1 da verificare/)).toBeInTheDocument();
    // The ambiguous title is never converted into a suggestion.
    expect(screen.queryByRole("button", { name: /Applica suggerimenti/ })).not.toBeInTheDocument();
    expect(fetchMock.mock.calls.some(([url]) => String(url).includes("/groups/7/settings"))).toBe(false);
  });

  it("flags an ambiguous title even when a language is already configured", () => {
    const reviewGroup: TreeNode = {
      ...group,
      accounts: [{ ...group.accounts[0], username: "Italia English Wrestling", language: "fr" }],
    };

    render(
      <MemoryRouter>
        <GroupDetailPanel
          group={reviewGroup}
          onPickAccount={() => {}}
          onCreateSubgroup={() => {}}
          onDeleteGroup={() => {}}
          onSaved={() => {}}
          onRename={() => {}}
        />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Analizza lingue dai titoli" }));
    expect(screen.getByLabelText(/Avviso lingua: Titolo ambiguo/)).toBeInTheDocument();
    expect(screen.getByLabelText("Language for Italia English Wrestling")).toHaveValue("fr");
  });

  it("flags a title without a reliable marker for manual review", () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const reviewGroup: TreeNode = {
      ...group,
      accounts: [{ ...group.accounts[0], username: "Wrestling Discovery", language: "" }],
    };

    render(
      <MemoryRouter>
        <GroupDetailPanel
          group={reviewGroup}
          onPickAccount={() => {}}
          onCreateSubgroup={() => {}}
          onDeleteGroup={() => {}}
          onSaved={() => {}}
          onRename={() => {}}
        />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Analizza lingue dai titoli" }));
    expect(screen.getByLabelText(/Avviso lingua:.*non determinabile/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Applica suggerimenti/ })).not.toBeInTheDocument();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("asks before overwriting an existing language detected from the title", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });
    vi.stubGlobal("fetch", fetchMock);
    const confirmMock = vi.spyOn(window, "confirm").mockReturnValue(false);
    const detectionGroup: TreeNode = {
      ...group,
      accounts: [{ ...group.accounts[0], username: "WWE Italia", language: "en" }],
    };

    render(
      <MemoryRouter>
        <GroupDetailPanel
          group={detectionGroup}
          onPickAccount={() => {}}
          onCreateSubgroup={() => {}}
          onDeleteGroup={() => {}}
          onSaved={() => {}}
          onRename={() => {}}
        />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Analizza lingue dai titoli" }));
    fireEvent.click(screen.getByRole("button", { name: /Applica suggerimenti/ }));
    expect(confirmMock).toHaveBeenCalledWith(expect.stringContaining("sovrascritta"));
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("updates an existing language only after overwrite confirmation", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const detectionGroup: TreeNode = {
      ...group,
      accounts: [{ ...group.accounts[0], username: "WWE Italia", language: "en" }],
    };

    render(
      <MemoryRouter>
        <GroupDetailPanel
          group={detectionGroup}
          onPickAccount={() => {}}
          onCreateSubgroup={() => {}}
          onDeleteGroup={() => {}}
          onSaved={() => {}}
          onRename={() => {}}
        />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Analizza lingue dai titoli" }));
    fireEvent.click(screen.getByRole("button", { name: /Applica suggerimenti/ }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/v1/accounts/101"),
      expect.objectContaining({
        method: "PATCH",
        body: JSON.stringify({ metadata: { language: "it" } }),
      }),
    ));
  });

  it("keeps the auto-saved suggestion pending when the PATCH fails", async () => {
    const fetchMock = vi.fn().mockRejectedValue(new Error("salvataggio lingua non riuscito"));
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const detectionGroup: TreeNode = {
      ...group,
      accounts: [{ ...group.accounts[0], username: "WWE Italia", language: "" }],
    };

    render(
      <MemoryRouter>
        <GroupDetailPanel
          group={detectionGroup}
          onPickAccount={() => {}}
          onCreateSubgroup={() => {}}
          onDeleteGroup={() => {}}
          onSaved={() => {}}
          onRename={() => {}}
        />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Analizza lingue dai titoli" }));
    // The failed auto-save restores the detected suggestion ("it") and keeps
    // it pending so the operator can retry without re-running detection.
    await waitFor(() => expect(screen.getByLabelText("Language for WWE Italia")).toHaveValue("it"));
    expect(screen.getByRole("button", { name: /Applica suggerimenti \(1\)/ })).toBeInTheDocument();
    expect(screen.getByLabelText(/salvataggio lingua non riuscito/)).toBeInTheDocument();
  });

  it("restores a manually edited suggestion and keeps it pending when PATCH fails", async () => {
    const fetchMock = vi.fn().mockRejectedValue(new Error("modifica lingua non riuscita"));
    vi.stubGlobal("fetch", fetchMock);
    const detectionGroup: TreeNode = {
      ...group,
      accounts: [{ ...group.accounts[0], username: "WWE Italia", language: "" }],
    };

    render(
      <MemoryRouter>
        <GroupDetailPanel
          group={detectionGroup}
          onPickAccount={() => {}}
          onCreateSubgroup={() => {}}
          onDeleteGroup={() => {}}
          onSaved={() => {}}
          onRename={() => {}}
        />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Analizza lingue dai titoli" }));
    // Wait for the auto-save attempt to settle (it fails) before editing the
    // dropdown — the select is disabled while a save is in flight.
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.getByLabelText(/modifica lingua non riuscita/)).toBeInTheDocument());
    fireEvent.change(screen.getByLabelText("Language for WWE Italia"), { target: { value: "en" } });
    await waitFor(() => expect(screen.getByLabelText("Language for WWE Italia")).toHaveValue("it"));
    expect(screen.getByRole("button", { name: /Applica suggerimenti \(1\)/ })).toBeInTheDocument();
    expect(screen.getByLabelText(/modifica lingua non riuscita/)).toBeInTheDocument();
  });

  it("saves language and remaining membership settings", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });
    vi.stubGlobal("fetch", fetchMock);
    const onSaved = vi.fn();

    render(
      <MemoryRouter>
        <GroupDetailPanel
          group={group}
          onPickAccount={() => {}}
          onCreateSubgroup={() => {}}
          onDeleteGroup={() => {}}
          onSaved={onSaved}
          onRename={() => {}}
        />
      </MemoryRouter>,
    );

    fireEvent.change(screen.getByLabelText("Language for channel-one"), { target: { value: "it" } });
    fireEvent.click(screen.getByRole("button", { name: /Save changes/i }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/v1/accounts/101"),
      expect.objectContaining({
        method: "PATCH",
        body: JSON.stringify({ metadata: { language: "it" } }),
      }),
    ));
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/api/v1/groups/7/settings"),
      expect.objectContaining({
        method: "PATCH",
        body: JSON.stringify({
          accounts: [
            { account_id: 101, language: "it" },
            { account_id: 102, language: "fr" },
          ],
        }),
      }),
    );
    expect(onSaved).toHaveBeenCalledTimes(1);
  });
});
