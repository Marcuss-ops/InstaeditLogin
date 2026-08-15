import {
  CheckCircle2,
  ExternalLink,
  Loader2,
  ShieldCheck,
  X,
} from "lucide-react";
import type {
  EditorSession,
  YouTubePublishResult,
} from "../../types/uploads";
import { openInstaEditorWithLaunch } from "../../features/youtube/api/editorSessionsApi";

const FALLBACK_YOUTUBE_URL = (videoId: string) =>
  `https://www.youtube.com/watch?v=${encodeURIComponent(videoId)}`;

const PRIVACY_LABEL: Record<string, string> = {
  public: "Pubblico",
  unlisted: "Non in elenco",
  private: "Privato",
};

const SYNC_LABEL: Record<string, string> = {
  confirmed: "Confermato da YouTube",
  drift: "Privacy diversa da quella richiesta",
  pending: "Verifica ancora in corso",
  failed: "Verifica non riuscita",
};

export function YouTubePublishCard({
  result,
  session,
  preview,
  checking,
  onDismiss,
}: {
  result: YouTubePublishResult;
  session?: EditorSession;
  preview?: {
    title?: string;
    description?: string;
    thumbnail_url?: string;
  };
  checking: boolean;
  onDismiss: () => void;
}) {
  const actualPrivacy = result.actual_privacy ?? session?.actual_privacy;
  const syncStatus = result.youtube_sync_status ?? session?.youtube_sync_status;
  const youtubeURL =
    result.public_url ||
    (result.video_id ? FALLBACK_YOUTUBE_URL(result.video_id) : null);
  const isDrift = syncStatus === "drift";
  const isPending = checking || syncStatus === "pending";
  const isScheduled =
    result.privacy_status === "private" &&
    !!result.published_at &&
    new Date(result.published_at).getTime() > Date.now();
  const thumbnailURL =
    preview?.thumbnail_url ||
    result.thumbnail_url ||
    (result.video_id
      ? `https://i.ytimg.com/vi/${encodeURIComponent(result.video_id)}/hqdefault.jpg`
      : "");

  const handleOpenEditor = () => {
    if (!session?.editor_url || !session.velox_project_id) return;
    const tab = window.open("about:blank", "_blank");
    void openInstaEditorWithLaunch(session.editor_url, session.velox_project_id, {
      tab,
    }).catch(() => {
      tab?.close();
    });
  };

  return (
    <section
      className={`rounded-2xl border p-5 shadow-[0_8px_32px_rgba(0,0,0,0.25)] ${
        isDrift
          ? "border-amber-500/35 bg-amber-500/[0.08]"
          : "border-emerald-500/30 bg-emerald-500/[0.07]"
      }`}
      data-testid="youtube-publish-card"
      aria-live="polite"
    >
      <div className="flex items-start gap-3">
        {isPending ? (
          <Loader2
            size={22}
            className="mt-0.5 shrink-0 animate-spin text-blue-200"
            aria-hidden="true"
          />
        ) : isDrift ? (
          <ShieldCheck
            size={22}
            className="mt-0.5 shrink-0 text-amber-200"
            aria-hidden="true"
          />
        ) : (
          <CheckCircle2
            size={22}
            className="mt-0.5 shrink-0 text-emerald-200"
            aria-hidden="true"
          />
        )}
        <div className="min-w-0 flex-1">
          <div className="flex items-start justify-between gap-3">
            <div>
              <h2 className="text-[15px] font-bold text-white">
                {isPending
                  ? "Pubblicazione completata — verifica YouTube in corso"
                  : isScheduled
                    ? "Programmazione YouTube completata"
                    : isDrift
                      ? "Video pubblicato, controlla la privacy"
                      : "Video pubblicato su YouTube"
                }
              </h2>
              <p className="mt-1 text-[12px] text-[#cdd2da]">
                Video ID: <span className="font-mono">{result.video_id}</span>
              </p>
            </div>
            <button
              type="button"
              onClick={onDismiss}
              className="rounded-lg p-1 text-white/50 hover:bg-white/[0.08] hover:text-white transition-colors"
              aria-label="Chiudi conferma pubblicazione"
              data-testid="youtube-publish-card-dismiss"
            >
              <X size={15} aria-hidden="true" />
            </button>
          </div>

          <div className="mt-3 flex flex-wrap gap-2 text-[11px]">
            <span className="rounded-md border border-white/10 bg-white/[0.05] px-2 py-1 text-[#e8e8ef]">
              Privacy richiesta: {PRIVACY_LABEL[result.privacy_status] ?? result.privacy_status}
            </span>
            <span className="rounded-md border border-white/10 bg-white/[0.05] px-2 py-1 text-[#e8e8ef]">
              Privacy effettiva: {actualPrivacy ? PRIVACY_LABEL[actualPrivacy] ?? actualPrivacy : "in verifica"}
            </span>
            <span className="rounded-md border border-white/10 bg-white/[0.05] px-2 py-1 text-[#e8e8ef]">
              Stato: {SYNC_LABEL[syncStatus ?? ""] ?? "pubblicato"}
            </span>
          </div>

          <div
            className="mt-4 overflow-hidden rounded-xl border border-white/[0.10] bg-black/20"
            data-testid="youtube-publish-preview"
          >
            <div className="flex flex-col sm:flex-row">
              <div className="aspect-video w-full shrink-0 bg-white/[0.05] sm:w-48">
                {thumbnailURL ? (
                  <img
                    src={thumbnailURL}
                    alt="Thumbnail anteprima YouTube"
                    className="h-full w-full object-cover"
                    loading="lazy"
                  />
                ) : (
                  <div className="flex h-full items-center justify-center text-[11px] text-white/40">
                    Nessuna thumbnail
                  </div>
                )}
              </div>
              <div className="min-w-0 flex-1 p-3">
                <p className="text-[10px] font-semibold uppercase tracking-[0.12em] text-white/45">
                  Anteprima YouTube
                </p>
                <h3 className="mt-1 line-clamp-2 text-[14px] font-bold text-white">
                  {preview?.title || result.title || "Video YouTube"}
                </h3>
                <p className="mt-2 whitespace-pre-line line-clamp-4 text-[12px] leading-5 text-[#b7bdc8]">
                  {preview?.description || result.description || "Nessuna descrizione disponibile."}
                </p>
                <p className="mt-2 text-[11px] text-white/50">
                  Visibilità: {PRIVACY_LABEL[actualPrivacy ?? result.privacy_status] ?? actualPrivacy ?? result.privacy_status}
                </p>
              </div>
            </div>
          </div>

          {isScheduled && (
            <p className="mt-3 text-[12px] text-blue-100">
              Il video resterà privato fino al {new Date(result.published_at!).toLocaleString("it-IT")}, poi YouTube lo renderà pubblico automaticamente.
            </p>
          )}
          {isDrift && (
            <p className="mt-3 text-[12px] text-amber-100">
              YouTube ha confermato una visibilità diversa da quella richiesta.
              La riconciliazione server-side proverà ad allinearla.
            </p>
          )}

          <div className="mt-4 flex flex-wrap gap-2">
            {youtubeURL && (
              <a
                href={youtubeURL}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-1.5 rounded-lg bg-white px-3 py-2 text-[12px] font-semibold text-[#030308] hover:bg-[#e8ecf2] transition-colors"
                data-testid="youtube-publish-card-open"
              >
                <ExternalLink size={13} aria-hidden="true" />
                Apri video su YouTube
              </a>
            )}
            {session?.editor_url ? (
              <button
                type="button"
                onClick={handleOpenEditor}
                className="inline-flex items-center gap-1.5 rounded-lg border border-white/15 bg-white/[0.06] px-3 py-2 text-[12px] font-medium text-white hover:bg-white/[0.12] transition-colors"
              >
                Riapri InstaEditor
              </button>
            ) : (
              <span
                role="alert"
                className="inline-flex items-center rounded-lg border border-amber-300/20 bg-amber-300/10 px-3 py-2 text-[12px] font-medium text-amber-100"
              >
                Editor unavailable / misconfigured
              </span>
            )}
          </div>
        </div>
      </div>
    </section>
  );
}
