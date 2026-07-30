/**
 * ChannelVideoCard vitest coverage.
 *
 * Goals:
 *   1. Renders the privacy chip + status chip with the right
 *      vocabulary (Privato / Non in elenco / Pubblico / Sconosciuto;
 *      Live / In coda / Fallito / Sconosciuto).
 *   2. Renders the duration label converted from ISO 8601.
 *   3. Fallback URL (`https://youtu.be/{id}`) when public_url is
 *      missing — common for unlisted uploads that haven't yet
 *      built their public URL.
 *   4. URL target=_blank + rel=noopener on the "Apri su YouTube"
 *      link (security: prevents the opened tab from controlling
 *      our window).
 *   5. Click "Modifica copertina" → fires onEditThumbnail ONCE with
 *      the video; e.stopPropagation prevents bubbling to wrapping
 *      anchors (future-proof; we don't currently wrap cards in <a>).
 *   6. Highlight state (matching `?video=`):
 *        - data-highlighted="true" on root
 *        - emerald ring class on root
 *        - "Appena caricato" badge in DOM
 *      When not matched:
 *        - no badge in DOM
 *        - data-highlighted="false"
 *   7. Unknown statuses / privacies fall back to the neutral chip.
 */
import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { ChannelVideoCard } from "./ChannelVideoCard";
import type { ChannelVideo } from "../types";

const VIDEO: ChannelVideo = {
  external_id: "yt_demo_001",
  title: "Demo di canale",
  thumbnail_url: "https://i.ytimg.com/vi/yt_demo_001/maxresdefault.jpg",
  public_url: "https://youtu.be/yt_demo_001",
  privacy: "private",
  status: "live",
  published_at: "2026-07-10T12:00:00.000Z",
  duration: "PT1H2M3S",
  metrics: [
    { key: "views", label: "Views", value: 12345, display_value: "12.3K" },
  ],
};

describe("ChannelVideoCard", () => {
  it("renders the title, video ID, duration, and a private privacy chip", () => {
    render(
      <ChannelVideoCard video={VIDEO} onEditThumbnail={() => {}} />,
    );
    expect(
      screen.getByTestId("channel-video-card-title"),
    ).toHaveTextContent("Demo di canale");
    expect(
      screen.getByTestId("channel-video-card-id"),
    ).toHaveTextContent("yt_demo_001");
    expect(screen.getByTestId("channel-video-card-privacy")).toHaveTextContent(
      "Privato",
    );
    expect(
      screen.getByTestId("channel-video-card-status"),
    ).toHaveTextContent("Live");
    expect(
      screen.getByTestId("channel-video-card-duration"),
    ).toHaveTextContent("1:02:03");
  });

  it("emits an emerald-style class on private privacy chip", () => {
    render(
      <ChannelVideoCard video={VIDEO} onEditThumbnail={() => {}} />,
    );
    const chip = screen.getByTestId("channel-video-card-privacy");
    expect(chip.className).toContain("emerald-500");
  });

  it("renders 'Non in elenco' with amber style for unlisted", () => {
    render(
      <ChannelVideoCard
        video={{ ...VIDEO, privacy: "unlisted" }}
        onEditThumbnail={() => {}}
      />,
    );
    const chip = screen.getByTestId("channel-video-card-privacy");
    expect(chip).toHaveTextContent("Non in elenco");
    expect(chip.className).toContain("amber-500");
  });

  it("renders 'Pubblico' with blue style for public", () => {
    render(
      <ChannelVideoCard
        video={{ ...VIDEO, privacy: "public" }}
        onEditThumbnail={() => {}}
      />,
    );
    const chip = screen.getByTestId("channel-video-card-privacy");
    expect(chip).toHaveTextContent("Pubblico");
    expect(chip.className).toContain("blue-500");
  });

  it("renders Sconosciuto chip for unknown privacy values", () => {
    render(
      <ChannelVideoCard
        video={{ ...VIDEO, privacy: "legacy_draft" }}
        onEditThumbnail={() => {}}
      />,
    );
    expect(
      screen.getByTestId("channel-video-card-privacy"),
    ).toHaveTextContent("Sconosciuto");
  });

  it("renders 'In coda' chip for queued status", () => {
    render(
      <ChannelVideoCard
        video={{ ...VIDEO, status: "queued" }}
        onEditThumbnail={() => {}}
      />,
    );
    expect(screen.getByTestId("channel-video-card-status")).toHaveTextContent(
      "In coda",
    );
  });

  it("renders 'Fallito' chip with red style for failed status", () => {
    render(
      <ChannelVideoCard
        video={{ ...VIDEO, status: "failed" }}
        onEditThumbnail={() => {}}
      />,
    );
    const chip = screen.getByTestId("channel-video-card-status");
    expect(chip).toHaveTextContent("Fallito");
    expect(chip.className).toContain("red-500");
  });

  it("renders 'Sconosciuto' chip for unknown status values", () => {
    render(
      <ChannelVideoCard
        video={{ ...VIDEO, status: "mystery_state" }}
        onEditThumbnail={() => {}}
      />,
    );
    expect(screen.getByTestId("channel-video-card-status")).toHaveTextContent(
      "Sconosciuto",
    );
  });

  it("omits the duration label when no duration is provided", () => {
    render(
      <ChannelVideoCard
        video={{ ...VIDEO, duration: undefined }}
        onEditThumbnail={() => {}}
      />,
    );
    expect(
      screen.queryByTestId("channel-video-card-duration"),
    ).toBeNull();
  });

  it("renders the formatted views metric when present", () => {
    render(
      <ChannelVideoCard video={VIDEO} onEditThumbnail={() => {}} />,
    );
    expect(
      screen.getByTestId("channel-video-card-views"),
    ).toHaveTextContent(/12\.3K visualizzazioni/);
  });

  it("omits the views metric when metrics[views] is missing", () => {
    render(
      <ChannelVideoCard
        video={{ ...VIDEO, metrics: [] }}
        onEditThumbnail={() => {}}
      />,
    );
    expect(screen.queryByTestId("channel-video-card-views")).toBeNull();
  });

  it("renders the 'Apri su YouTube' link with public_url + target=_blank + rel=noopener", () => {
    render(
      <ChannelVideoCard video={VIDEO} onEditThumbnail={() => {}} />,
    );
    const link = screen.getByTestId("channel-video-card-open");
    expect(link).toHaveAttribute("href", "https://youtu.be/yt_demo_001");
    expect(link).toHaveAttribute("target", "_blank");
    expect(link.getAttribute("rel")).toMatch(/noopener/);
  });

  it("falls back to https://youtu.be/{id} when public_url is missing", () => {
    render(
      <ChannelVideoCard
        video={{ ...VIDEO, public_url: undefined }}
        onEditThumbnail={() => {}}
      />,
    );
    expect(screen.getByTestId("channel-video-card-open")).toHaveAttribute(
      "href",
      "https://youtu.be/yt_demo_001",
    );
  });

  it('fires onEditThumbnail once per "Modifica copertina" click', () => {
    const onEditThumbnail = vi.fn();
    render(
      <ChannelVideoCard
        video={VIDEO}
        onEditThumbnail={onEditThumbnail}
      />,
    );
    fireEvent.click(screen.getByTestId("channel-video-card-edit"));
    expect(onEditThumbnail).toHaveBeenCalledTimes(1);
    expect(onEditThumbnail).toHaveBeenCalledWith(VIDEO);
  });

  it("applies highlight state when highlightVideoId matches external_id", () => {
    render(
      <ChannelVideoCard
        video={VIDEO}
        highlightVideoId="yt_demo_001"
        onEditThumbnail={() => {}}
      />,
    );
    const root = screen.getByTestId("channel-video-card");
    expect(root).toHaveAttribute("data-highlighted", "true");
    expect(root.className).toContain("emerald-500");
    expect(
      screen.getByTestId("channel-video-card-highlight-badge"),
    ).toHaveTextContent(/Appena caricato/i);
  });

  it("does NOT apply highlight state when highlightVideoId differs", () => {
    render(
      <ChannelVideoCard
        video={VIDEO}
        highlightVideoId="some_other_video"
        onEditThumbnail={() => {}}
      />,
    );
    const root = screen.getByTestId("channel-video-card");
    expect(root).toHaveAttribute("data-highlighted", "false");
    expect(root.className).not.toContain("ring-emerald");
    expect(
      screen.queryByTestId("channel-video-card-highlight-badge"),
    ).toBeNull();
  });

  it("does NOT apply highlight state when highlightVideoId is omitted", () => {
    render(
      <ChannelVideoCard video={VIDEO} onEditThumbnail={() => {}} />,
    );
    expect(
      screen.getByTestId("channel-video-card"),
    ).toHaveAttribute("data-highlighted", "false");
  });
});
