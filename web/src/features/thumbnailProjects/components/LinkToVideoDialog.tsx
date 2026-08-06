/**
 * LinkToVideoDialog — assign a READY export to a YouTube video.
 *
 * Flow (DoD "Collegamento successivo"): Gruppo → Canale → Video privato →
 * Lingua → Preview → Conferma. The export already exists before this
 * dialog opens; the assignment only inserts a thumbnail_assignments row —
 * the original project (and its snapshot/revisions) is NEVER modified,
 * so the project stays valid with 0 assignments until a link is made.
 *
 * Data comes from the existing workspace APIs:
 *   GET /api/v1/groups/aggregate   → groups + member account_ids (one call)
 *   GET /api/v1/accounts           → YouTube channels (filtered by group)
 *   GET /api/v1/accounts/{id}/content?privacy=private → private videos
 *   POST /api/v1/thumbnail-exports/{export_id}/assignments → the link
 */
import { useEffect, useMemo, useState } from "react";
import { FolderTree, ImageIcon, Loader2, X } from "lucide-react";
import { authedFetch } from "../../../lib/auth";
import { filterYouTube, listAllAccounts, type PlatformAccount } from "../../channels/api/channelsApi";
import { createThumbnailAssignments } from "../api/thumbnailProjectsApi";
import type { ContentItem } from "../../../pages/internal/calendarTypes";

export interface LinkToVideoDialogProps {
  workspaceId: number;
  exportId: string;
  /** Presigned URL of the export's rendered preview (server file). */
  previewUrl?: string | null;
  onClose: () => void;
  /** Called after at least one assignment was created (parent refreshes). */
  onLinked: () => void;
}

type GroupsState =
  | { kind: "loading" }
  | { kind: "ready"; groups: GroupWithMembers[] }
  | { kind: "error"; message: string };

interface GroupWithMembers {
  id: number;
  name: string;
  account_ids: number[];
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
  previewUrl,
  onClose,
  onLinked,
}: LinkToVideoDialogProps) {
  const [groupsState, setGroupsState] = useState<GroupsState>({ kind: "loading" });
  const [selectedGroupId, setSelectedGroupId] = useState<string>("");
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
      try {
        // Group memberships + channels load in parallel (one aggregate
        // call, no per-group fan-out — mirrors useGroupsData).
        const [groupsResp, accounts] = await Promise.all([
          authedFetch("/api/v1/groups/aggregate"),
          listAllAccounts(),
        ]);
        const groupsData = (await groupsResp.json()) as {
          groups?: Array<{ id: number; name: string; account_ids?: number[] }>;
        };
        if (cancelled) return;
        setGroupsState({
          kind: "ready",
          groups: (groupsData.groups ?? []).map((g) => ({
            id: g.id,
            name: g.name,
            account_ids: g.account_ids ?? [],
          })),
        });
        setChannelsState({
          kind: "ready",
          channels: filterYouTube(accounts),
        });
      } catch (err) {
        if (cancelled) return;
        const message = err instanceof Error ? err.message : "Impossibile caricare gruppi e canali.";
        setGroupsState({ kind: "error", message });
        setChannelsState({ kind: "error", message });
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  // Channels filtered by the chosen group: with "Tutti i gruppi" (empty)
  // every connected YouTube channel is offered; otherwise only channels
  // that are members of the selected group.
  const visibleChannels = useMemo(() => {
    if (channelsState.kind !== "ready") return [];
    if (selectedGroupId === "") return channelsState.channels;
    const memberIds = new Set(
      (groupsState.kind === "ready"
        ? groupsState.groups.find((g) => String(g.id) === selectedGroupId)?.account_ids
        : undefined) ?? [],
    );
    return channelsState.channels.filter((c) => memberIds.has(c.id));
  }, [channelsState, groupsState, selectedGroupId]);

  const handleGroupChange = (value: string) => {
    setSelectedGroupId(value);
    setSelectedChannel(null);
    setSelectedVideo(null);
    setVideosState({ kind: "idle" });
  };

  const loadVideos = async (channel: PlatformAccount) => {
    setVideosState({ kind: "loading" });
    setSelectedVideo(null);
    try {
      const items: ContentItem[] = [];
      const seenCursors = new Set<string>();
      let cursor: string | undefined;
      let loadedAllPages = false;
      for (let page = 0; page < 10_000; page += 1) {
        const params = new URLSearchParams({ limit: "50", privacy: "private" });
        if (cursor) params.set("cursor", cursor);
        const resp = await authedFetch(
          `/api/v1/accounts/${channel.id}/content?${params.toString()}`,
        );
        const data = (await resp.json()) as {
          items?: ContentItem[];
          next_cursor?: string;
        };
        items.push(...(data.items ?? []));
        if (!data.next_cursor) {
          loadedAllPages = true;
          break;
        }
        if (seenCursors.has(data.next_cursor)) {
          throw new Error("La paginazione dei video ha restituito un cursore ripetuto.");
        }
        seenCursors.add(data.next_cursor);
        cursor = data.next_cursor;
      }
      if (!loadedAllPages) {
        throw new Error("La lista video ha superato il limite massimo di pagine.");
      }
      setVideosState({ kind: "ready", items });
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

  const showPreview = selectedChannel !== null && selectedVideo !== null;

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
          {/* Gruppo (opzionale) — primo passo: filtra i canali per gruppo */}
          <div>
            <label htmlFor="link-group" className="text-[12px] font-semibold text-[#9aa0aa]">
              Gruppo
            </label>
            {groupsState.kind === "loading" && (
              <div className="mt-2 flex items-center gap-2 text-[12px] text-[#9aa0aa]">
                <Loader2 size={14} className="animate-spin" /> Caricamento gruppi…
              </div>
            )}
            {groupsState.kind === "error" && (
              <p className="mt-2 text-[12px] text-red-400">{groupsState.message}</p>
            )}
            {groupsState.kind === "ready" && (
              <select
                id="link-group"
                value={selectedGroupId}
                onChange={(e) => handleGroupChange(e.target.value)}
                className="mt-2 w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-lg text-[13px] text-white focus:outline-none focus:border-white/[0.20]"
              >
                <option value="" className="bg-[#1f1f2e]">
                  Tutti i gruppi
                </option>
                {groupsState.groups.map((group) => (
                  <option key={group.id} value={group.id} className="bg-[#1f1f2e]">
                    {group.name}
                  </option>
                ))}
              </select>
            )}
          </div>

          {/* Canale (filtrato dal gruppo) */}
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
            {channelsState.kind === "ready" && visibleChannels.length === 0 && (
              <p className="mt-2 text-[12px] text-[#9aa0aa]">
                {selectedGroupId === ""
                  ? "Nessun canale YouTube collegato. Collega un canale prima di assegnare la copertina."
                  : "Nessun canale YouTube in questo gruppo."}
              </p>
            )}
            {channelsState.kind === "ready" && visibleChannels.length > 0 && (
              <select
                id="link-channel"
                value={selectedChannel?.id ?? ""}
                onChange={(e) => {
                  const channel =
                    visibleChannels.find((c) => c.id === Number(e.target.value)) ?? null;
                  setSelectedChannel(channel);
                  if (channel) void loadVideos(channel);
                }}
                className="mt-2 w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-lg text-[13px] text-white focus:outline-none focus:border-white/[0.20]"
              >
                <option value="" className="bg-[#1f1f2e]">
                  Seleziona un canale…
                </option>
                {visibleChannels.map((channel) => (
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

          {/* Preview — il file renderizzato dal server, prima di confermare */}
          {showPreview && (
            <div>
              <span className="flex items-center gap-1.5 text-[12px] font-semibold text-[#9aa0aa]">
                <FolderTree size={12} />
                Anteprima
              </span>
              <div className="mt-2 overflow-hidden rounded-xl border border-white/[0.10] bg-black/40">
                {previewUrl ? (
                  <img
                    src={previewUrl}
                    alt="Anteprima copertina da applicare"
                    data-testid="link-preview"
                    className="w-full"
                  />
                ) : (
                  <div className="flex aspect-video w-full items-center justify-center">
                    <ImageIcon size={24} className="text-white/25" />
                  </div>
                )}
              </div>
              <p className="mt-2 text-[12px] text-[#9aa0aa]">
                Verrà applicata a{" "}
                <span className="font-semibold text-white">
                  {selectedVideo?.title ?? selectedVideo?.external_id}
                </span>{" "}
                su{" "}
                <span className="font-semibold text-white">
                  {selectedChannel?.username ?? `canale #${selectedChannel?.id}`}
                </span>
                {language ? ` in ${LANGUAGES.find((l) => l.code === language)?.label ?? language}` : ""}.
              </p>
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
