import { useCallback, useEffect, useRef, useState } from "react";
import { authedFetch, ApiError, AuthError } from "../../lib/auth";

export type VideoJob = {
  id: string;
  project_id?: string;
  render_status: string;
  publication_status: string;
  overall_status: string;
  publish_at?: string;
  created_at: string;
  updated_at: string;
};

export type VideoJobFetchState =
  | { kind: "loading" }
  | { kind: "ready"; jobs: VideoJob[] }
  | { kind: "error"; message: string };

export const VIDEO_PIPELINE_STAGES = [
  "Generazione voiceover",
  "Script",
  "Clip & Stock",
  "Video finale creato",
  "Copertina",
  "Video pubblicato",
] as const;

export type VideoPipelineStage = (typeof VIDEO_PIPELINE_STAGES)[number];

const ACTIVE_STATUSES = new Set([
  "queued",
  "pending",
  "running",
  "processing",
  "rendering",
  "publishing",
  "in_progress",
]);

function normalized(value: string | undefined): string {
  return (value ?? "").trim().toLowerCase();
}

export function isVideoJobActive(job: VideoJob): boolean {
  return [job.overall_status, job.render_status, job.publication_status].some((value) =>
    ACTIVE_STATUSES.has(normalized(value)),
  );
}

export function isVideoJobFailed(job: VideoJob): boolean {
  return [job.overall_status, job.render_status, job.publication_status].some((value) =>
    ["failed", "error", "cancelled", "canceled"].includes(normalized(value)),
  );
}

export function isVideoJobPublished(job: VideoJob): boolean {
  return [job.overall_status, job.render_status, job.publication_status].some((value) =>
    ["published", "completed", "complete", "succeeded", "success"].includes(normalized(value)),
  ) && normalized(job.publication_status) === "published";
}

/**
 * The upstream job contract exposes render/publication status, not internal
 * creative stages. Keep the product pipeline explicit in the UI and derive
 * the highest reliable stage from the durable job state.
 */
export function videoJobStageIndex(job: VideoJob): number {
  const publication = normalized(job.publication_status);
  const render = normalized(job.render_status);
  const overall = normalized(job.overall_status);

  if (publication === "published" || overall === "published") return 5;
  if (["completed", "complete", "succeeded", "success"].includes(render)) return 4;
  if (["running", "processing", "rendering", "executing"].includes(render)) return 2;
  if (["queued", "pending", "accepted"].includes(render) || ["queued", "pending"].includes(overall)) return 1;
  return 0;
}

export function videoJobStageLabel(job: VideoJob): VideoPipelineStage {
  return VIDEO_PIPELINE_STAGES[videoJobStageIndex(job)] ?? VIDEO_PIPELINE_STAGES[0];
}

export function useVideoJobs() {
  const navigate = useCallback((path: string) => {
    if (typeof window !== "undefined") window.location.assign(path);
  }, []);
  const abortRef = useRef<AbortController | null>(null);
  const [state, setState] = useState<VideoJobFetchState>({ kind: "loading" });

  const load = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    setState((current) => (current.kind === "ready" ? current : { kind: "loading" }));

    try {
      const response = await authedFetch("/api/v1/velox/jobs?limit=500", {
        signal: controller.signal,
      });
      const data = (await response.json()) as { jobs?: VideoJob[] };
      if (!controller.signal.aborted) {
        setState({ kind: "ready", jobs: data.jobs ?? [] });
      }
    } catch (error) {
      if (controller.signal.aborted) return;
      if (error instanceof AuthError) {
        navigate("/login");
        return;
      }
      setState({
        kind: "error",
        message: error instanceof ApiError ? error.message : "Impossibile caricare i job video.",
      });
    }
  }, [navigate]);

  useEffect(() => {
    void load();
    const interval = window.setInterval(() => {
      void load();
    }, 5000);
    return () => {
      window.clearInterval(interval);
      abortRef.current?.abort();
    };
  }, [load]);

  const activeJobs = state.kind === "ready" ? state.jobs.filter(isVideoJobActive) : [];
  const completedJobs = state.kind === "ready"
    ? state.jobs.filter((job) => !isVideoJobActive(job) && !isVideoJobFailed(job))
    : [];

  return { state, load, activeJobs, completedJobs };
}

export async function cancelVideoJob(id: string): Promise<void> {
  await authedFetch(`/api/v1/velox/jobs/${encodeURIComponent(id)}/cancel`, {
    method: "POST",
  });
}
