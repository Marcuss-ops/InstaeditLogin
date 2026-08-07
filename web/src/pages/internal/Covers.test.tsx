/** Vitest coverage for the standalone Dark Editor handoff route. */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
const { redirectToInstaEditorMock } = vi.hoisted(() => ({
  redirectToInstaEditorMock: vi.fn(),
}));

vi.mock("../../features/youtube/api/editorSessionsApi", () => ({
  redirectToInstaEditor: redirectToInstaEditorMock,
}));

import { CoversPage } from "./Covers";

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/app/covers" element={<CoversPage />} />
        <Route path="/app/covers/:projectId" element={<CoversPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  redirectToInstaEditorMock.mockClear();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("CoversPage", () => {
  it("redirects the editor root without rendering an iframe", () => {
    renderAt("/app/covers");
    expect(redirectToInstaEditorMock).toHaveBeenCalledWith("/dark_editor_v2/");
    expect(screen.queryByTestId("dark-editor-frame")).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: /apertura instaeditor/i })).toBeInTheDocument();
  });

  it("redirects to the selected Velox project", () => {
    renderAt("/app/covers/ve_abc");
    expect(redirectToInstaEditorMock).toHaveBeenCalledWith("/dark_editor_v2/editor/ve_abc");
  });

  it("URL-encodes the project id and exposes a direct fallback link", () => {
    renderAt("/app/covers/project%2Fone");
    expect(redirectToInstaEditorMock).toHaveBeenCalledWith("/dark_editor_v2/editor/project%2Fone");
    expect(screen.getByRole("link", { name: /apri editor/i })).toHaveAttribute(
      "href",
      "/dark_editor_v2/editor/project%2Fone",
    );
  });

  it("keeps a back link to the dashboard", () => {
    renderAt("/app/covers/ve_abc");
    expect(screen.getByRole("link", { name: /dashboard/i })).toHaveAttribute(
      "href",
      "/app/dashboard",
    );
  });
});
