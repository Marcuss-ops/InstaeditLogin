/**
 * Copertine — autonomous thumbnail library.
 *
 * The entry surface of the Dark Editor's autonomous model: a project is a
 * graphic canvas with NO YouTube channel/video/account prerequisite. The
 * library lists every workspace project and classifies it with the
 * canonical filter set:
 *
 *   Tutte / Bozze (draft) / Pronte (ready) / Collegate (≥1 assignment) /
 *   Archiviate (archived)
 *
 * "Collegate" is computed from the project's assignment rows
 * (GET /api/v1/thumbnail-projects/{id}/assignments), never guessed
 * client-side. "Crea nuova copertina" asks only for name, format,
 * dimensions and initial background — no YouTube surface at all.
 *
 * The backend persists the project immediately (POST
 * /api/v1/thumbnail-projects) and this page then writes the initial empty
 * canvas snapshot so the project owns a revision from birth
 * ("salvataggio immediato del progetto vuoto"). The real canvas editor
 * arrives in a later phase; for now "Apri" lands on a project detail
 * page (preview, revisions, assignments).
 */
import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import {
  Plus,
  ImageIcon,
  Archive,
  Trash2,
  ExternalLink,
  Link2,
  Layers,
  Clock,
  AlertCircle,
} from "lucide-react";
import { authedFetch, AuthError, fetchSession } from "../../lib/auth";
import { Skeleton, ErrorState } from "../../components/feedback";
import { cn } from "../../lib/utils";
import {
  createThumbnailProject,
  listThumbnailAssignments,
  listThumbnailProjects,
  resolveThumbnailProjectMedia,
  saveThumbnailSnapshot,
  archiveThumbnailProject,
  deleteThumbnailProject,
} from "../../features/thumbnailProjects/api/thumbnailProjectsApi";
import type {
  ThumbnailProject,
  ThumbnailProjectStatus,
} from "../../features/thumbnailProjects/types";

const RENDERER_VERSION = "go-canvas-v1";

type Workspace = { id: number; name: string };

type FilterKey = "all" | "draft" | "ready" | "linked" | "archived";

const FILTERS: { key: FilterKey; label: string }[] = [
  { key: "all", label: "Tutte" },
  { key: "draft", label: "Bozze" },
  { key: "ready", label: "Pronte" },
  { key: "linked", label: "Collegate" },
  { key: "archived", label: "Archiviate" },
];

const FORMAT_PRESETS = [
  { id: "youtube", label: "YouTube 16:9", width: 1920, height: 1080 },
  { id: "short", label: "Short 9:16", width: 1080, height: 1920 },
  { id: "square", label: "Quadrata 1:1", width: 1080, height: 1080 },
] as const;

type LoadState =
  | { kind: "loading" }
  | {
      kind: "ready";
      workspaces: Workspace[];
      projects: ThumbnailProject[];
      /** project_id → assignment rows (for the "Collegate" filter). */
      assignmentsByProject: Map<string, number>;
    }
  | { kind: "error"; message: string };

function statusLabel(status: ThumbnailProjectStatus): string {
  switch (status) {
    case "draft":
      return "Bozza";
    case "ready":
      return "Pronta";
    case "archived":
      return "Archiviata";
    case "deleted":
      return "Eliminata";
  }
}

function statusBadgeClass(status: ThumbnailProjectStatus): string {
  switch (status) {
    case "ready":
      return "bg-emerald-500/[0.12] text-emerald-300 border-emerald-400/20";
    case "archived":
      return "bg-white/[0.05] text-[#9aa0aa] border-white/[0.08]";
    default:
      return "bg-amber-400/[0.10] text-amber-300 border-amber-400/20";
  }
}

function formatDate(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleDateString("it-IT", {
    day: "2-digit",
    month: "short",
    year: "numeric",
  });
}

interface CreateDialogProps {
  workspaceId: number;
  onCreated: (project: ThumbnailProject) => void;
  onClose: () => void;
}

/** Minimal autonomous creation: name, format, dimensions, background. */
function CreateCoverDialog({ workspaceId, onCreated, onClose }: CreateDialogProps) {
  const navigate = useNavigate();
  const [name, setName] = useState("");
  const [preset, setPreset] = useState<(typeof FORMAT_PRESETS)[number]["id"]>("youtube");
  const [custom, setCustom] = useState(false);
  const [width, setWidth] = useState(1920);
  const [height, setHeight] = useState(1080);
  const [background, setBackground] = useState("#30305a");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const currentPreset = FORMAT_PRESETS.find((p) => p.id === preset) ?? FORMAT_PRESETS[0]!;
  const canvasWidth = custom ? width : currentPreset.width;
  const canvasHeight = custom ? height : currentPreset.height;
  const canSubmit = name.trim().length > 0 && canvasWidth > 0 && canvasHeight > 0 && !submitting;

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!canSubmit) return;
    setSubmitting(true);
    setError(null);
    try {
      // 1) The project is persisted immediately with an ID of its own —
      //    even an empty project is durable ("salvataggio immediato").
      const project = await createThumbnailProject({
        workspace_id: workspaceId,
        name: name.trim(),
        canvas_width: canvasWidth,
        canvas_height: canvasHeight,
      });
      // 2) Write the initial empty canvas snapshot (with the chosen
      //    background) so the project owns revision #1 from birth.
      try {
        await saveThumbnailSnapshot(workspaceId, project.id, {
          schema_version: 1,
          snapshot: {
            canvas: { width: canvasWidth, height: canvasHeight, background },
            objects: [],
          },
          renderer_version: RENDERER_VERSION,
          base_version: project.version,
        });
      } catch {
        // A failed initial snapshot must not block creation: the project
        // already exists server-side and the editor phase will save it.
      }
      onCreated(project);
      navigate(`/app/covers/${encodeURIComponent(project.id)}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Impossibile creare la copertina.");
      setSubmitting(false);
    }
  };

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Crea nuova copertina"
      className="fixed inset-0 z-50 flex items-center justify-center p-4"
    >
      <button
        type="button"
        aria-label="Chiudi"
        onClick={onClose}
        className="absolute inset-0 bg-black/70 backdrop-blur-sm cursor-default"
      />
      <form
        onSubmit={handleSubmit}
        className="relative w-full max-w-md rounded-2xl border border-white/[0.12] bg-[#1f1f2e] p-6 shadow-[0_8px_32px_rgba(0,0,0,0.5)]"
      >
        <h2 className="text-lg font-bold text-white">Crea nuova copertina</h2>
        <p className="mt-1 text-[13px] text-[#9aa0aa]">
          Nessun canale, video o connessione richiesti — la copertina nasce autonoma.
        </p>

        <div className="mt-5 space-y-4">
          <div>
            <label htmlFor="cover-name" className="block text-[13px] font-semibold text-[#9aa0aa] mb-1.5">
              Nome progetto
            </label>
            <input
              id="cover-name"
              type="text"
              autoFocus
              placeholder="Es. WWE Breaking News"
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white placeholder:text-white/20 focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all"
            />
          </div>

          <div>
            <span className="block text-[13px] font-semibold text-[#9aa0aa] mb-1.5">Formato</span>
            <div className="grid grid-cols-3 gap-2">
              {FORMAT_PRESETS.map((p) => (
                <button
                  key={p.id}
                  type="button"
                  onClick={() => {
                    setPreset(p.id);
                    setCustom(false);
                  }}
                  className={cn(
                    "rounded-xl border px-2 py-2 text-center transition-colors",
                    !custom && preset === p.id
                      ? "bg-white text-black border-white"
                      : "bg-white/[0.04] text-[#e8e8ef] border-white/[0.08] hover:border-white/[0.20]",
                  )}
                >
                  <span className="block text-[12px] font-semibold leading-tight">{p.label}</span>
                  <span className="block text-[11px] opacity-60">
                    {p.width}×{p.height}
                  </span>
                </button>
              ))}
            </div>
            <label className="mt-2 flex items-center gap-2 text-[13px] text-[#9aa0aa]">
              <input
                type="checkbox"
                checked={custom}
                onChange={(e) => setCustom(e.target.checked)}
                className="accent-white"
              />
              Dimensione personalizzata
            </label>
            {custom && (
              <div className="mt-2 grid grid-cols-2 gap-2">
                <label className="block">
                  <span className="text-[11px] font-semibold text-[#9aa0aa]">Larghezza</span>
                  <input
                    type="number"
                    min={1}
                    max={16384}
                    value={width}
                    onChange={(e) => setWidth(Number(e.target.value))}
                    className="w-full mt-1 px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white focus:outline-none focus:border-white/[0.20]"
                  />
                </label>
                <label className="block">
                  <span className="text-[11px] font-semibold text-[#9aa0aa]">Altezza</span>
                  <input
                    type="number"
                    min={1}
                    max={16384}
                    value={height}
                    onChange={(e) => setHeight(Number(e.target.value))}
                    className="w-full mt-1 px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white focus:outline-none focus:border-white/[0.20]"
                  />
                </label>
              </div>
            )}
          </div>

          <div>
            <label htmlFor="cover-background" className="block text-[13px] font-semibold text-[#9aa0aa] mb-1.5">
              Sfondo iniziale
            </label>
            <div className="flex items-center gap-2">
              <input
                id="cover-background"
                type="color"
                value={background}
                onChange={(e) => setBackground(e.target.value)}
                className="h-9 w-12 rounded-lg border border-white/[0.08] bg-white/[0.04] cursor-pointer"
              />
              <input
                type="text"
                value={background}
                onChange={(e) => setBackground(e.target.value)}
                className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white font-mono focus:outline-none focus:border-white/[0.20]"
              />
            </div>
          </div>
        </div>

        {error && (
          <p className="mt-4 flex items-center gap-2 text-[13px] text-red-400">
            <AlertCircle size={14} /> {error}
          </p>
        )}

        <div className="mt-6 flex items-center justify-end gap-3">
          <button
            type="button"
            onClick={onClose}
            className="px-4 py-2 text-[14px] font-medium text-[#9aa0aa] hover:text-white transition-colors"
          >
            Annulla
          </button>
          <button
            type="submit"
            disabled={!canSubmit}
            className="inline-flex items-center gap-2 px-5 py-2.5 rounded-xl bg-white text-black text-[14px] font-semibold hover:bg-white/90 transition-all disabled:opacity-50 disabled:cursor-not-allowed"
          >
            <Plus size={16} />
            Crea copertina
          </button>
        </div>
      </form>
    </div>
  );
}

interface ProjectCardProps {
  project: ThumbnailProject;
  assignmentCount: number;
  previewUrl?: string;
  onArchive: (project: ThumbnailProject) => void;
  onDelete: (project: ThumbnailProject) => void;
}

function ProjectCard({
  project,
  assignmentCount,
  previewUrl,
  onArchive,
  onDelete,
}: ProjectCardProps) {
  const aspect = `${project.canvas_width} / ${project.canvas_height}`;
  return (
    <div
      data-testid="cover-card"
      className="group overflow-hidden rounded-2xl border border-white/[0.08] bg-[#1a1a28] transition-all hover:border-white/[0.18] hover:shadow-[0_8px_32px_rgba(0,0,0,0.35)]"
    >
      <Link
        to={`/app/covers/${encodeURIComponent(project.id)}`}
        className="block no-underline"
        data-testid="cover-card-preview"
      >
        <div
          className="relative w-full overflow-hidden bg-black"
          style={{ aspectRatio: aspect }}
        >
          {previewUrl ? (
            <img
              src={previewUrl}
              alt={project.name}
              loading="lazy"
              className="h-full w-full object-cover"
            />
          ) : (
            <div
              className="flex h-full w-full items-center justify-center"
              style={{ backgroundColor: "#30305a" }}
            >
              <ImageIcon className="h-8 w-8 text-white/25" />
            </div>
          )}
          <span
            className={cn(
              "absolute left-2.5 top-2.5 inline-flex items-center rounded-full border px-2 py-0.5 text-[10px] font-bold uppercase tracking-wide",
              statusBadgeClass(project.status),
            )}
          >
            {statusLabel(project.status)}
          </span>
        </div>
      </Link>

      <div className="p-4">
        <Link to={`/app/covers/${encodeURIComponent(project.id)}`} className="no-underline">
          <h3 className="truncate text-[15px] font-bold text-white group-hover:text-white/90">
            {project.name}
          </h3>
        </Link>
        <p className="mt-1 text-[12px] text-[#9aa0aa]">
          {project.canvas_width}×{project.canvas_height} · v{project.version}
        </p>

        <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-[12px] text-[#9aa0aa]">
          {assignmentCount > 0 && (
            <span className="inline-flex items-center gap-1 text-sky-300">
              <Link2 size={12} />
              {assignmentCount} {assignmentCount === 1 ? "collegamento" : "collegamenti"}
            </span>
          )}
          {project.current_revision_id && (
            <span className="inline-flex items-center gap-1">
              <Layers size={12} />
              revisioni
            </span>
          )}
          <span className="inline-flex items-center gap-1">
            <Clock size={12} />
            {formatDate(project.updated_at)}
          </span>
        </div>

        <div className="mt-4 flex items-center gap-2">
          <Link
            to={`/app/covers/${encodeURIComponent(project.id)}`}
            className="inline-flex flex-1 items-center justify-center gap-1.5 rounded-lg border border-white/[0.10] bg-white/[0.04] px-3 py-1.5 text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors no-underline"
          >
            <ExternalLink size={14} />
            Apri
          </Link>
          {project.status !== "archived" && (
            <button
              type="button"
              aria-label={`Archivia ${project.name}`}
              onClick={() => onArchive(project)}
              className="rounded-lg border border-white/[0.08] p-1.5 text-[#9aa0aa] hover:text-white hover:bg-white/[0.06] transition-colors"
            >
              <Archive size={14} />
            </button>
          )}
          <button
            type="button"
            aria-label={`Elimina ${project.name}`}
            onClick={() => onDelete(project)}
            className="rounded-lg border border-white/[0.08] p-1.5 text-[#9aa0aa] hover:text-red-400 hover:bg-red-500/[0.08] transition-colors"
          >
            <Trash2 size={14} />
          </button>
        </div>
      </div>
    </div>
  );
}

export function CoversPage() {
  const navigate = useNavigate();
  const [state, setState] = useState<LoadState>({ kind: "loading" });
  const [workspaceId, setWorkspaceId] = useState<number | "">("");
  const [filter, setFilter] = useState<FilterKey>("all");
  const [showCreate, setShowCreate] = useState(false);
  const [previews, setPreviews] = useState<Map<string, string>>(new Map());
  const abortRef = useRef<AbortController | null>(null);

  const loadData = useCallback(
    async (overrideWorkspaceId?: number) => {
      abortRef.current?.abort();
      const controller = new AbortController();
      abortRef.current = controller;
      setState({ kind: "loading" });

      try {
        const wsResp = await authedFetch("/api/v1/workspaces", {
          signal: controller.signal,
        });
        if (controller.signal.aborted) return;
        const wsData = (await wsResp.json()) as { workspaces: Workspace[] };
        const workspaces = wsData.workspaces ?? [];
        if (workspaces.length === 0) {
          setState({
            kind: "ready",
            workspaces: [],
            projects: [],
            assignmentsByProject: new Map(),
          });
          return;
        }
        const wsId =
          overrideWorkspaceId ??
          (workspaceId === "" ? workspaces[0]!.id : Number(workspaceId));
        setWorkspaceId(wsId);

      // Assignments are fetched in a dedicated effect once the project
      // list is known (they feed the "Collegate" filter).
      const projectsList = (await listThumbnailProjects(wsId, {
        signal: controller.signal,
      })) ?? [];

        setState({
          kind: "ready",
          workspaces,
          projects: projectsList,
          assignmentsByProject: new Map(),
        });
        void loadPreviews(wsId, projectsList, controller.signal);
      } catch (err) {
        if (controller.signal.aborted) return;
        if (err instanceof AuthError) {
          navigate("/login", { replace: true });
          return;
        }
        const message =
          err instanceof Error ? err.message : "Impossibile caricare le copertine.";
        setState({ kind: "error", message });
      }
    },
    [workspaceId, navigate],
  );

  const loadPreviews = async (
    wsId: number,
    projects: ThumbnailProject[],
    signal: AbortSignal,
  ) => {
    const withPreview = projects.filter((p) => p.preview_media_id);
    const next = new Map<string, string>();
    for (const project of withPreview) {
      if (signal.aborted || !project.preview_media_id) return;
      try {
        const resolved = await resolveThumbnailProjectMedia(wsId, project.id, [project.preview_media_id], {
          signal,
        });
        if (resolved[0]?.url) next.set(project.id, resolved[0].url);
      } catch {
        // A failed preview resolve must never break the library — the
        // card falls back to the placeholder.
      }
    }
    if (!signal.aborted) setPreviews(next);
  };

  // Fetch assignments for every listed project (Collegate filter).
  useEffect(() => {
    if (state.kind !== "ready" || state.projects.length === 0 || workspaceId === "") return;
    const controller = new AbortController();
    const wsId = Number(workspaceId);
    void (async () => {
      try {
        const lists = await Promise.all(
          state.projects.map((p) =>
            listThumbnailAssignments(wsId, p.id, { signal: controller.signal }).catch(() => []),
          ),
        );
        if (controller.signal.aborted) return;
        setState((prev) => {
          if (prev.kind !== "ready") return prev;
          const assignmentsByProject = new Map<string, number>();
          for (const list of lists) {
            for (const assignment of list) {
              assignmentsByProject.set(
                assignment.project_id,
                (assignmentsByProject.get(assignment.project_id) ?? 0) + 1,
              );
            }
          }
          return { ...prev, assignmentsByProject };
        });
      } catch {
        // assignments are best-effort; the filter just shows 0 linked.
      }
    })();
    return () => controller.abort();
  }, [state.kind === "ready" ? state.projects : null, workspaceId]);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const session = await fetchSession();
      if (cancelled) return;
      if (!session) {
        navigate("/login", { replace: true });
        return;
      }
      void loadData();
    })();
    return () => {
      cancelled = true;
      abortRef.current?.abort();
    };
  }, [loadData, navigate]);

  // The main load effect re-runs whenever workspaceId changes (loadData
  // is recreated with the fresh closure), so we only flip state here —
  // calling loadData directly too would double-fetch the same workspace.
  const handleWorkspaceChange = (next: number | "") => {
    setWorkspaceId(next);
    setPreviews(new Map());
  };

  const handleCreated = (project: ThumbnailProject) => {
    setShowCreate(false);
    setState((prev) => {
      if (prev.kind !== "ready") return prev;
      return { ...prev, projects: [project, ...prev.projects] };
    });
  };

  const handleArchive = async (project: ThumbnailProject) => {
    try {
      await archiveThumbnailProject(Number(workspaceId), project.id, project.version);
      setState((prev) => {
        if (prev.kind !== "ready") return prev;
        return {
          ...prev,
          projects: prev.projects.map((p) =>
            p.id === project.id ? { ...p, status: "archived" } : p,
          ),
        };
      });
    } catch {
      // authedFetch toasts the error already.
    }
  };

  const handleDelete = async (project: ThumbnailProject) => {
    if (!window.confirm(`Eliminare la copertina "${project.name}"? La cronologia resta recuperabile.`)) {
      return;
    }
    try {
      await deleteThumbnailProject(Number(workspaceId), project.id, project.version);
      setState((prev) => {
        if (prev.kind !== "ready") return prev;
        return { ...prev, projects: prev.projects.filter((p) => p.id !== project.id) };
      });
    } catch {
      // authedFetch toasts the error already.
    }
  };

  if (state.kind === "loading") {
    return (
      <div className="min-h-full p-8">
        <div className="mx-auto max-w-7xl space-y-6">
          <Skeleton variant="card" height={96} />
          <div className="grid gap-5 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
            {Array.from({ length: 8 }).map((_, i) => (
              <Skeleton key={i} variant="card" height={260} />
            ))}
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
            title="Impossibile caricare le copertine"
            message={state.message}
            onRetry={() => void loadData()}
          />
        </div>
      </div>
    );
  }

  const { workspaces, projects, assignmentsByProject } = state;
  const activeWorkspaceId = workspaceId === "" ? workspaces[0]?.id : Number(workspaceId);

  const counts: Record<FilterKey, number> = {
    all: projects.length,
    draft: projects.filter((p) => p.status === "draft").length,
    ready: projects.filter((p) => p.status === "ready").length,
    linked: projects.filter((p) => (assignmentsByProject.get(p.id) ?? 0) > 0).length,
    archived: projects.filter((p) => p.status === "archived").length,
  };

  const visibleProjects = projects.filter((p) => {
    switch (filter) {
      case "draft":
        return p.status === "draft";
      case "ready":
        return p.status === "ready";
      case "linked":
        return (assignmentsByProject.get(p.id) ?? 0) > 0;
      case "archived":
        return p.status === "archived";
      default:
        return true;
    }
  });

  return (
    <div className="min-h-full p-8 bg-[#030308] text-[#e8e8ef]">
      <div className="mx-auto max-w-7xl">
        {showCreate && activeWorkspaceId !== undefined && (
          <CreateCoverDialog
            workspaceId={activeWorkspaceId}
            onCreated={handleCreated}
            onClose={() => setShowCreate(false)}
          />
        )}

        <div className="flex flex-wrap items-end justify-between gap-4">
          <div>
            <h1 className="text-[28px] font-extrabold tracking-[-0.02em] text-white flex items-center gap-3">
              <ImageIcon size={28} className="text-white/40" />
              Copertine
            </h1>
            <p className="mt-1 text-[15px] text-[#9aa0aa]">
              Progetti grafici autonomi: crea, salva, riapri ed esporta senza
              alcun canale o video collegato.
            </p>
          </div>
          <button
            type="button"
            onClick={() => setShowCreate(true)}
            data-testid="create-cover-cta"
            className="inline-flex items-center gap-2 rounded-xl bg-white px-5 py-2.5 text-[14px] font-semibold text-black hover:bg-white/90 transition-all"
          >
            <Plus size={16} />
            Crea nuova copertina
          </button>
        </div>

        <div className="mt-6 flex flex-wrap items-center gap-3">
          {workspaces.length > 1 && (
            <select
              aria-label="Workspace"
              value={workspaceId}
              onChange={(e) =>
                handleWorkspaceChange(e.target.value === "" ? "" : Number(e.target.value))
              }
              className="rounded-xl border border-white/[0.08] bg-[#1f1f2e] px-3 py-2 text-[13px] font-medium text-white focus:outline-none focus:border-white/[0.20]"
            >
              {workspaces.map((ws) => (
                <option key={ws.id} value={ws.id} className="bg-[#1f1f2e]">
                  {ws.name}
                </option>
              ))}
            </select>
          )}
          <div className="flex flex-wrap gap-2" role="group" aria-label="Filtri copertine">
            {FILTERS.map((f) => (
              <button
                key={f.key}
                type="button"
                onClick={() => setFilter(f.key)}
                data-testid={`cover-filter-${f.key}`}
                aria-checked={filter === f.key}
                role="checkbox"
                className={cn(
                  "inline-flex items-center gap-1.5 rounded-full border px-3 py-1.5 text-[13px] font-medium transition-colors",
                  filter === f.key
                    ? "bg-white text-black border-white"
                    : "bg-white/[0.04] text-[#9aa0aa] border-white/[0.08] hover:text-white hover:border-white/[0.20]",
                )}
              >
                {f.label}
                <span className={cn("text-[11px]", filter === f.key ? "opacity-60" : "opacity-50")}>
                  {counts[f.key]}
                </span>
              </button>
            ))}
          </div>
        </div>

        <div className="mt-8">
          {workspaces.length === 0 ? (
            <div className="rounded-2xl border border-white/[0.08] bg-[#1a1a28] p-10 text-center">
              <p className="text-[15px] text-[#9aa0aa]">
                Nessun workspace disponibile. Creane uno per iniziare.
              </p>
            </div>
          ) : visibleProjects.length === 0 ? (
            <div className="rounded-2xl border border-white/[0.08] bg-[#1a1a28] p-10 text-center">
              <ImageIcon className="mx-auto h-10 w-10 text-white/20" />
              <p className="mt-4 text-[15px] font-semibold text-white">
                {projects.length === 0 ? "Nessuna copertina ancora" : "Nessuna copertina in questa vista"}
              </p>
              <p className="mt-1 text-[13px] text-[#9aa0aa]">
                {projects.length === 0
                  ? "Crea la tua prima copertina autonoma — nessun canale richiesto."
                  : "Prova un altro filtro o crea una nuova copertina."}
              </p>
              {projects.length === 0 && (
                <button
                  type="button"
                  onClick={() => setShowCreate(true)}
                  className="mt-5 inline-flex items-center gap-2 rounded-xl bg-white px-5 py-2.5 text-[14px] font-semibold text-black hover:bg-white/90 transition-all"
                >
                  <Plus size={16} />
                  Crea nuova copertina
                </button>
              )}
            </div>
          ) : (
            <div className="grid gap-5 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
              {visibleProjects.map((project) => (
                <ProjectCard
                  key={project.id}
                  project={project}
                  assignmentCount={assignmentsByProject.get(project.id) ?? 0}
                  previewUrl={previews.get(project.id)}
                  onArchive={handleArchive}
                  onDelete={handleDelete}
                />
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
