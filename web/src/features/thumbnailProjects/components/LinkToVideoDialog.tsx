/**
 * LinkToVideoDialog — assign a READY export to a YouTube video.
 *
 * Flow (DoD "Collegamento successivo"): Canale → Video privato → Lingua
 * → Conferma. The export already exists before this dialog opens; the
 * assignment never modifies the original project.
 *
 * Data comes from the existing YouTube account/content APIs:
 *   GET /api/v1/accounts → filter platform==="youtube"
 *   GET /api/v1/accounts/{id}/content?privacy=private → private videos
 *   POST /api/v1/thumbnail-exports/{export_id}/assignments → link
 */
import { useEffect, useState } from "react";
import { Loader2, X } from "lucide-react";
import { authedFetch } from "../../../lib/auth";
import { filterYouTube, type PlatformAccount } from "../../channels/api/channelsApi";
import { createThumbnailAssignments } from "../api/thumbnailProjectsApi";
import type { ContentItem } from "../../../pages/internal/calendarTypes";

export interface LinkToVideoDialogProps {
  workspaceId: number;
  exportId: string;
  onClose: () => void;
  /** Called after at least one assignment was created (parent refreshes). */
  onLinked: () => void;
}

type ChannelsState =
  | { kind: "loading" }
  | { kind: "ready"; channels: PlatformAccount[] }
  | { kind: "error"; message: string };

type VideosState =
  | { kind: "idle" }
  | { kind: "loading" }
  | { kind: "ready"; items: ContentItem[] }
  | { kind: "error"; message: string };

const LANGUAGES = [
  { code: "", label: "Nessuna (default canale)" },
  { code: "it", label: "Italiano" },
  { code: "en", label: "English" },
  { code: "fr", label: "Français" },
  { code: "de", label: "Deutsch" },
  { code: "es", label: "Español" },
  { code: "ru", label: "Русский" },
];

export function LinkToVideoDialog({
  workspaceId,
  exportId,
  onClose,
  onLinked,
}: LinkToVideoDialogProps) {
  const [channelsState, setChannelsState] = useState<ChannelsState>({ kind: "loading" });
  const [selectedChannel, setSelectedChannel] = useState<PlatformAccount | null>(null);
  const [videosState, setVideosState] = useState<VideosState>({ kind: "idle" });
  const [selectedVideo, setSelectedVideo] = useState<ContentItem | null>(null);
  const [language, setLanguage] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      setChannelsState({ kind: "loading" });
      try {
        const resp = await authedFetch("/api/v1/accounts");
        const data = (await resp.json()) as { accounts?: PlatformAccount[] };
        if (!cancelled) {
          const channels = filterYouTube(data.accounts ?? []);
          setChannelsState({ kind: "ready", channels });
        }
      } catch (err) {
        if (!cancelled) {
          setChannelsState({
            kind: "error",
            message: err instanceof Error ? err.message : "Impossibile caricare i canali.",
          });
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const loadVideos = async (channel: PlatformAccount) => {
    setVideosState({ kind: "loading" });
    setSelectedVideo(null);
    try {
      const resp = await authedFetch(
        `/api/v1/accounts/${channel.id}/content?limit=50&privacy=private`,
      );
      const data = (await resp.json()) as { items?: ContentItem[] };
      setVideosState({ kind: "ready", items: data.items ?? [] });
    } catch (err) {
      setVideosState({
        kind: "error",
        message: err instanceof Error ? err.message : "Impossibile caricare i video.",
      });
    }
  };

  const handleConfirm = async () => {
    if (!selectedChannel || !selectedVideo) return;
    setSubmitting(true);
    setError(null);
    try {
      await createThumbnailAssignments(workspaceId, exportId, {
        targets: [
          {
            platform_account_id: selectedChannel.id,
            youtube_video_id: selectedVideo.external_id,
            target_language: language || null,
          },
        ],
      });
      onLinked();
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Impossibile creare il collegamento.");
      setSubmitting(false);
    }
  };

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Collega a un video"
      className="fixed inset-0 z-50 flex items-center justify-center p-4"
    >
      <button
        type="button"
        aria-label="Chiudi"
        onClick={onClose}
        className="absolute inset-0 bg-black/70 backdrop-blur-sm cursor-default"
      />
      <div className="relative max-h-[85vh] w-full max-w-lg overflow-y-auto rounded-2xl border border-white/[0.12] bg-[#1f1f2e] p-6 shadow-[0_8px_32px_rgba(0,0,0,0.5)]">
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-lg font-bold text-white">Collega a un video</h2>
          <button
            type="button"
            aria-label="Chiudi"
            onClick={onClose}
            className="rounded-md p-1 text-[#9aa0aa] hover:text-white hover:bg-white/[0.06] transition-colors"
          >
            <X size={16} />
          </button>
        </div>
        <p className="mt-1 text-[12px] text-[#9aa0aa]">
          L'export <code className="text-white/70">{exportId}</code> esiste già — il collegamento
          non modifica il progetto originale.
        </p>

        <div className="mt-5 space-y-4">
          {/* Canale */}
          <div>
            <label htmlFor="link-channel" className="text-[12px] font-semibold text-[#9aa0aa]">
              Canale YouTube
            </label>
            {channelsState.kind === "loading" && (
              <div className="mt-2 flex items-center gap-2 text-[12px] text-[#9aa0aa]">
                <Loader2 size={14} className="animate-spin" /> Caricamento canali…
              </div>
            )}
            {channelsState.kind === "error" && (
              <p className="mt-2 text-[12px] text-red-400">{channelsState.message}</p>
            )}
            {channelsState.kind === "ready" && channelsState.channels.length === 0 && (
              <p className="mt-2 text-[12px] text-[#9aa0aa]">
                Nessun canale YouTube collegato. Collega un canale prima di assegnare la copertina.
              </p>
            )}
            {channelsState.kind === "ready" && channelsState.channels.length > 0 && (
              <select
                id="link-channel"
                value={selectedChannel?.id ?? ""}
                onChange={(e) => {
                  const channel =
                    channelsState.channels.find((c) => c.id === Number(e.target.value)) ?? null;
                  setSelectedChannel(channel);
                  if (channel) void loadVideos(channel);
                }}
                className="mt-2 w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-lg text-[13px] text-white focus:outline-none focus:border-white/[0.20]"
              >
                <option value="" className="bg-[#1f1f2e]">
                  Seleziona un canale…
                </option>
                {channelsState.channels.map((channel) => (
                  <option key={channel.id} value={channel.id} className="bg-[#1f1f2e]">
                    {channel.username || `Canale #${channel.id}`}
                  </option>
                ))}
              </select>
            )}
          </div>

          {/* Video privato */}
          {selectedChannel && (
            <div>
              <label htmlFor="link-video" className="text-[12px] font-semibold text-[#9aa0aa]">
                Video privato
              </label>
              {videosState.kind === "loading" && (
                <div className="mt-2 flex items-center gap-2 text-[12px] text-[#9aa0aa]">
                  <Loader2 size={14} className="animate-spin" /> Caricamento video…
                </div>
              )}
              {videosState.kind === "error" && (
                <p className="mt-2 text-[12px] text-red-400">{videosState.message}</p>
              )}
              {videosState.kind === "ready" && videosState.items.length === 0 && (
                <p className="mt-2 text-[12px] text-[#9aa0aa]">
                  Nessun video privato trovato su questo canale.
                </p>
              )}
              {videosState.kind === "ready" && videosState.items.length > 0 && (
                <select
                  id="link-video"
                  value={selectedVideo?.external_id ?? ""}
                  onChange={(e) => {
                    const video =
                      videosState.items.find((v) => v.external_id === e.target.value) ?? null;
                    setSelectedVideo(video);
                  }}
                  className="mt-2 w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-lg text-[13px] text-white focus:outline-none focus:border-white/[0.20]"
                >
                  <option value="" className="bg-[#1f1f2e]">
                    Seleziona un video…
                  </option>
                  {videosState.items.map((video) => (
                    <option key={video.external_id} value={video.external_id} className="bg-[#1f1f2e]">
                      {video.title ?? video.external_id}
                    </option>
                  ))}
                </select>
              )}
            </div>
          )}

          {/* Lingua */}
          {selectedVideo && (
            <div>
              <label htmlFor="link-language" className="text-[12px] font-semibold text-[#9aa0aa]">
                Lingua del testo
              </label>
              <select
                id="link-language"
                value={language}
                onChange={(e) => setLanguage(e.target.value)}
                className="mt-2 w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-lg text-[13px] text-white focus:outline-none focus:border-white/[0.20]"
              >
                {LANGUAGES.map((lang) => (
                  <option key={lang.code} value={lang.code} className="bg-[#1f1f2e]">
                    {lang.label}
                  </option>
                ))}
              </select>
            </div>
          )}
        </div>

        {error && (
          <p className="mt-4 rounded-lg border border-red-400/25 bg-red-500/[0.08] px-3 py-2 text-[12px] text-red-200">
            {error}
          </p>
        )}

        <div className="mt-6 flex justify-end gap-2">
          <button
            type="button"
            onClick={onClose}
            className="rounded-lg border border-white/[0.10] bg-white/[0.04] px-4 py-2 text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors"
          >
            Annulla
          </button>
          <button
            type="button"
            onClick={() => void handleConfirm()}
            disabled={!selectedChannel || !selectedVideo || submitting}
            className="inline-flex items-center gap-1.5 rounded-lg bg-white px-4 py-2 text-[13px] font-semibold text-black hover:bg-white/90 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {submitting && <Loader2 size={14} className="animate-spin" />}
            Conferma collegamento
          </button>
        </div>
      </div>
    </div>
  );
}
