/**
 * CoverDetail — read-only project detail for the Copertine library.
 *
 * Shows the durable facts of an autonomous project: metadata, the
 * server-resolved preview (never a local blob), the immutable revision
 * history and the optional YouTube assignments. The full canvas editor
 * lands in a later phase; this page certifies that a project survives
 * close/reopen with its revisions and assets intact.
 */
import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import {
  ArrowLeft,
  ImageIcon,
  Layers,
  Link2,
  Clock,
  Hash,
  Info,
} from "lucide-react";
import { authedFetch, AuthError, fetchSession } from "../../lib/auth";
import { Skeleton, ErrorState } from "../../components/feedback";
import { cn } from "../../lib/utils";
import {
  getThumbnailProject,
  listThumbnailAssignments,
  listThumbnailRevisions,
  resolveThumbnailProjectMedia,
} from "../../features/thumbnailProjects/api/thumbnailProjectsApi";
import type {
  ThumbnailProject,
  ThumbnailProjectAssignment,
  ThumbnailProjectRevision,
} from "../../features/thumbnailProjects/types";

type LoadState =
  | { kind: "loading" }
  | {
      kind: "ready";
      project: ThumbnailProject;
      revisions: ThumbnailProjectRevision[];
      assignments: ThumbnailProjectAssignment[];
      previewUrl?: string;
    }
  | { kind: "error"; message: string };

/**
 * The current revision's canvas background, read from its immutable
 * snapshot. When a project has no rendered preview yet (no
 * preview_media_id), this lets the page paint the empty canvas with
 * the ACTUAL background chosen at creation instead of a hardcoded
 * placeholder — "il background fino all'ultimo pixel".
 */
function currentCanvasBackground(
  revisions: ThumbnailProjectRevision[],
  currentRevisionId: string | null | undefined,
): string {
  if (!currentRevisionId) return DEFAULT_BACKGROUND;
  const current = revisions.find((r) => r.id === currentRevisionId);
  const background = current?.snapshot_json?.canvas?.background;
  return typeof background === "string" && background.length > 0 ? background : DEFAULT_BACKGROUND;
}

const DEFAULT_BACKGROUND = "#30305a";

function formatDateTime(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleString("it-IT", {
    day: "2-digit",
    month: "short",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function CoverDetailPage() {
  const navigate = useNavigate();
  const { projectId } = useParams<{ projectId: string }>();
  const [state, setState] = useState<LoadState>({ kind: "loading" });
  const abortRef = useRef<AbortController | null>(null);

  const load = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    setState({ kind: "loading" });
    if (!projectId) {
      setState({ kind: "error", message: "Progetto non specificato." });
      return;
    }
    try {
      const wsResp = await authedFetch("/api/v1/workspaces", { signal: controller.signal });
      if (controller.signal.aborted) return;
      const { workspaces } = (await wsResp.json()) as { workspaces: { id: number }[] };
      if (workspaces.length === 0) {
        setState({ kind: "error", message: "Nessun workspace disponibile." });
        return;
      }
      const wsId = workspaces[0]!.id;

      const [project, revisions, assignments] = await Promise.all([
        getThumbnailProject(wsId, projectId, { signal: controller.signal }),
        listThumbnailRevisions(wsId, projectId, { signal: controller.signal }).catch(() => []),
        listThumbnailAssignments(wsId, projectId, { signal: controller.signal }).catch(() => []),
      ]);
      if (controller.signal.aborted) return;

      let previewUrl: string | undefined;
      if (project.preview_media_id) {
        try {
          const resolved = await resolveThumbnailProjectMedia(wsId, projectId, [
            project.preview_media_id,
          ]);
          previewUrl = resolved[0]?.url;
        } catch {
          // preview stays unset → placeholder
        }
      }

      setState({ kind: "ready", project, revisions, assignments, previewUrl });
    } catch (err) {
      if (controller.signal.aborted) return;
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      const message =
        err instanceof Error ? err.message : "Impossibile caricare il progetto.";
      setState({ kind: "error", message });
    }
  }, [projectId, navigate]);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const session = await fetchSession();
      if (cancelled) return;
      if (!session) {
        navigate("/login", { replace: true });
        return;
      }
      void load();
    })();
    return () => {
      cancelled = true;
      abortRef.current?.abort();
    };
  }, [load, navigate]);

  if (state.kind === "loading") {
    return (
      <div className="min-h-full p-8">
        <div className="mx-auto max-w-6xl grid gap-6 lg:grid-cols-[420px_1fr]">
          <Skeleton variant="card" height={280} />
          <div className="space-y-4">
            <Skeleton variant="card" height={120} />
            <Skeleton variant="card" height={200} />
          </div>
        </div>
      </div>
    );
  }

  if (state.kind === "error") {
    return (
      <div className="min-h-full p-8">
        <div className="mx-auto max-w-3xl">
          <ErrorState
            title="Impossibile caricare il progetto"
            message={state.message}
            onRetry={() => void load()}
          />
        </div>
      </div>
    );
  }

  const { project, revisions, assignments, previewUrl } = state;
  const aspect = `${project.canvas_width} / ${project.canvas_height}`;
  const emptyBackground = currentCanvasBackground(revisions, project.current_revision_id);

  return (
    <div className="min-h-full p-8 bg-[#030308] text-[#e8e8ef]">
      <div className="mx-auto max-w-6xl">
        <Link
          to="/app/covers"
          className="inline-flex items-center gap-1.5 text-[13px] font-medium text-[#9aa0aa] hover:text-white transition-colors no-underline mb-5"
        >
          <ArrowLeft size={14} /> Torna alle copertine
        </Link>

        <div className="grid gap-6 lg:grid-cols-[420px_1fr]">
          {/* Preview — server-resolved media, never a local blob. */}
          <div>
            <div
              className="w-full overflow-hidden rounded-2xl border border-white/[0.08] bg-black"
              style={{ aspectRatio: aspect }}
            >
              {previewUrl ? (
                <img
                  src={previewUrl}
                  alt={project.name}
                  className="h-full w-full object-cover"
                />
              ) : (
                <div
                  className="flex h-full w-full items-center justify-center"
                  style={{ backgroundColor: emptyBackground }}
                >
                  <ImageIcon className="h-10 w-10 text-white/25" />
                </div>
              )}
            </div>
            <p className="mt-2 text-center text-[12px] text-[#9aa0aa]">
              {previewUrl
                ? "Anteprima dall'ultimo export"
                : "Nessuna anteprima — genera un export per vederla"}
            </p>
          </div>

          {/* Metadata + history + assignments */}
          <div className="space-y-5">
            <div className="rounded-2xl border border-white/[0.08] bg-[#1a1a28] p-6">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div>
                  <h1 className="text-[24px] font-extrabold tracking-[-0.02em] text-white">
                    {project.name}
                  </h1>
                  <p className="mt-1 text-[13px] text-[#9aa0aa]">
                    {project.canvas_width}×{project.canvas_height} · versione {project.version}
                  </p>
                </div>
                <span
                  className={cn(
                    "inline-flex items-center rounded-full border px-2.5 py-1 text-[11px] font-bold uppercase tracking-wide",
                    project.status === "ready"
                      ? "bg-emerald-500/[0.12] text-emerald-300 border-emerald-400/20"
                      : project.status === "archived"
                        ? "bg-white/[0.05] text-[#9aa0aa] border-white/[0.08]"
                        : "bg-amber-400/[0.10] text-amber-300 border-amber-400/20",
                  )}
                >
                  {project.status === "draft"
                    ? "Bozza"
                    : project.status === "ready"
                      ? "Pronta"
                      : project.status === "archived"
                        ? "Archiviata"
                        : "Eliminata"}
                </span>
              </div>

              {project.description && (
                <p className="mt-3 text-[14px] text-[#e8e8ef]/80">{project.description}</p>
              )}

              <div className="mt-4 flex items-center gap-2 rounded-xl border border-white/[0.06] bg-black/30 px-3 py-2">
                <Hash size={13} className="shrink-0 text-[#9aa0aa]" />
                <code
                  data-testid="cover-project-id"
                  className="min-w-0 flex-1 truncate font-mono text-[12px] text-[#9aa0aa]"
                >
                  {project.id}
                </code>
                <button
                  type="button"
                  onClick={() => {
                    void navigator.clipboard?.writeText(project.id).catch(() => {});
                  }}
                  className="shrink-0 rounded-md px-1.5 py-0.5 text-[11px] font-semibold text-[#9aa0aa] hover:text-white hover:bg-white/[0.06] transition-colors"
                >
                  Copia
                </button>
              </div>

              <dl className="mt-5 grid grid-cols-2 gap-3 text-[13px] sm:grid-cols-3">
                <div>
                  <dt className="text-[11px] font-semibold uppercase tracking-wider text-[#9aa0aa]">
                    Creata
                  </dt>
                  <dd className="mt-0.5 text-white">{formatDateTime(project.created_at)}</dd>
                </div>
                <div>
                  <dt className="text-[11px] font-semibold uppercase tracking-wider text-[#9aa0aa]">
                    Aggiornata
                  </dt>
                  <dd className="mt-0.5 text-white">{formatDateTime(project.updated_at)}</dd>
                </div>
                <div>
                  <dt className="text-[11px] font-semibold uppercase tracking-wider text-[#9aa0aa]">
                    Revisione corrente
                  </dt>
                  <dd className="mt-0.5 text-white">
                    {project.current_revision_id ? "salvata" : "nessuna"}
                  </dd>
                </div>
              </dl>
            </div>

            <div className="rounded-2xl border border-white/[0.08] bg-[#1a1a28] p-6">
              <h2 className="flex items-center gap-2 text-[15px] font-bold text-white">
                <Layers size={16} className="text-white/40" />
                Revisioni immutabili
                <span className="text-[12px] font-medium text-[#9aa0aa]">
                  {revisions.length}
                </span>
              </h2>
              {revisions.length === 0 ? (
                <p className="mt-3 text-[13px] text-[#9aa0aa]">
                  Nessuna revisione salvata. L'editor la creerà al primo salvataggio.
                </p>
              ) : (
                <ul className="mt-4 space-y-2">
                  {revisions.map((revision) => (
                    <li
                      key={revision.id}
                      data-testid="cover-revision-row"
                      className="flex items-center justify-between gap-3 rounded-xl border border-white/[0.06] bg-white/[0.02] px-3 py-2.5"
                    >
                      <div className="flex items-center gap-2">
                        <span className="inline-flex h-7 w-7 items-center justify-center rounded-lg bg-white/[0.06] text-[12px] font-bold text-white">
                          {revision.revision_number}
                        </span>
                        <div>
                          <p className="text-[13px] font-medium text-white">
                            {revision.renderer_version}
                          </p>
                          <p className="text-[11px] text-[#9aa0aa]">
                            {formatDateTime(revision.created_at)}
                          </p>
                        </div>
                      </div>
                      <span
                        className="hidden sm:inline-flex items-center gap-1 text-[11px] text-[#9aa0aa]"
                        title={revision.snapshot_sha256}
                      >
                        <Hash size={11} />
                        {revision.snapshot_sha256.slice(0, 10)}…
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </div>

            <div className="rounded-2xl border border-white/[0.08] bg-[#1a1a28] p-6">
              <h2 className="flex items-center gap-2 text-[15px] font-bold text-white">
                <Link2 size={16} className="text-white/40" />
                Collegamenti YouTube
                <span className="text-[12px] font-medium text-[#9aa0aa]">
                  {assignments.length}
                </span>
              </h2>
              {assignments.length === 0 ? (
                <p className="mt-3 flex items-start gap-2 text-[13px] text-[#9aa0aa]">
                  <Info size={14} className="mt-0.5 shrink-0" />
                  Nessun collegamento: la copertina esiste in modo autonomo. Un export
                  può essere collegato a un video in un secondo momento, senza modificare
                  il progetto.
                </p>
              ) : (
                <ul className="mt-4 space-y-2">
                  {assignments.map((assignment) => (
                    <li
                      key={assignment.id}
                      className="flex items-center justify-between gap-3 rounded-xl border border-white/[0.06] bg-white/[0.02] px-3 py-2.5"
                    >
                      <div className="flex items-center gap-2">
                        <span className="inline-flex h-7 w-7 items-center justify-center rounded-lg bg-sky-500/[0.12]">
                          <Link2 size={13} className="text-sky-300" />
                        </span>
                        <div>
                          <p className="text-[13px] font-medium text-white">
                            {assignment.youtube_video_id}
                          </p>
                          <p className="text-[11px] text-[#9aa0aa]">
                            account #{assignment.platform_account_id}
                            {assignment.target_language
                              ? ` · lingua ${assignment.target_language}`
                              : ""}
                          </p>
                        </div>
                      </div>
                      <span className="inline-flex items-center gap-1 text-[11px] text-[#9aa0aa]">
                        <Clock size={11} />
                        {assignment.status}
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
