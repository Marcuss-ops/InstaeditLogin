import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { YouTubeStudioPrivateVideosSection } from "./YouTubeStudioPrivateVideosSection";

const baseProps = {
  selectedChannelId: 42,
  privateVideos: [{ external_id: "video-1", title: "Video privato" }],
  loadingVideos: false,
  manualVideoId: "",
  onSelectVideo: () => {},
  privateVideosEnabled: true,
  onLoad: () => {},
};

describe("YouTubeStudioPrivateVideosSection copyright status", () => {
  it("shows None when no copyright alert is known", () => {
    render(
      <YouTubeStudioPrivateVideosSection
        {...baseProps}
        copyrightByVideoId={{}}
      />,
    );

    expect(screen.getByTestId("private-video-copyright")).toHaveTextContent(
      "Copyright: None",
    );
  });

  it("shows a red copyright problem state for a claim", () => {
    render(
      <YouTubeStudioPrivateVideosSection
        {...baseProps}
        copyrightByVideoId={{
          "video-1": { status: "claim", message: "Claim detected" },
        }}
      />,
    );

    const status = screen.getByTestId("private-video-copyright");
    expect(status).toHaveTextContent("Copyright: Problema");
    expect(status.className).toContain("text-red-300");
  });
});
