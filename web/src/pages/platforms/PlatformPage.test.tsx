import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { PlatformPage } from "./PlatformPage";

function renderPlatformPage(slug: string) {
  return render(
    <MemoryRouter initialEntries={[`/${slug}`]}>
      <Routes>
        <Route path="/:slug" element={<PlatformPage />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("PlatformPage", () => {
  beforeEach(() => {
    vi.resetAllMocks();
  });

  it("renders the TikTok platform page with all major sections", async () => {
    renderPlatformPage("tiktok");

    expect(await screen.findByText("TikTok API Integration")).toBeInTheDocument();

    expect(
      screen.getByText(/Ship your TikTok integration in minutes/i),
    ).toBeInTheDocument();

    expect(screen.getByText("POST /v1/posts")).toBeInTheDocument();
    expect(screen.getByText("TikTok Creator or Business Account")).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: /Why InstaEdit vs TikTok API?/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: /How it works/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: /Built for scale/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: /Supported formats/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: /Common questions/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: /Ship your TikTok integration today./i }),
    ).toBeInTheDocument();
  });

  it("renders a fallback for unknown platform slugs", async () => {
    renderPlatformPage("unknown");

    expect(await screen.findByText("Platform not found")).toBeInTheDocument();
    expect(screen.getByText("Go back home")).toBeInTheDocument();
  });

  it("toggles FAQ answers on click", async () => {
    renderPlatformPage("tiktok");

    const firstQuestion = await screen.findByText(
      "How long does TikTok API approval take?",
    );
    expect(firstQuestion).toBeInTheDocument();

    const answerText = /TikTok's Content Posting API approval can take days to weeks/i;
    expect(screen.queryByText(answerText)).not.toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(firstQuestion);

    expect(screen.getByText(answerText)).toBeInTheDocument();

    await user.click(firstQuestion);

    expect(screen.queryByText(answerText)).not.toBeInTheDocument();
  });

  it("renders comparison lists for the platform", async () => {
    renderPlatformPage("tiktok");

    expect(await screen.findByText("InstaEdit API")).toBeInTheDocument();
    expect(screen.getByText("TikTok Content Posting API")).toBeInTheDocument();
    expect(
      screen.getByText("Simple API key — start in 30 seconds"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Complex OAuth with TikTok developer app approval"),
    ).toBeInTheDocument();
  });

  it("renders supported content types", async () => {
    renderPlatformPage("tiktok");

    expect(await screen.findByText("Videos")).toBeInTheDocument();
    expect(screen.getByText("Photo Mode")).toBeInTheDocument();
  });
});
