import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { AuthError, authedFetch } from "../lib/auth";
import { useToast } from "../components/toast";
import type {
  AsyncBatchResponse,
  BatchStatusResponse,
  FormValues,
  LoadState,
  PlatformAccount,
  SubmitState,
  Workspace,
} from "../types/uploads";
import {
  DEFAULT_MAX_JITTER_SEC,
  DEFAULT_MIN_JITTER_SEC,
  FOLDER_ID_PATTERN,
  MAX_JITTER_SEC,
  MIN_JITTER_SEC,
} from "../types/uploads";

const POLL_INTERVAL_MS = 5_000;
const TERMINAL_STATUSES = new Set([
  "completed",
  "failed",
  "cancelled",
  "partially_completed",
]);

export function useUploads() {
  const navigate = useNavigate();
  const toast = useToast();
  const firstFieldRef = useRef<HTMLInputElement | null>(null);

  const [loadState, setLoadState] = useState<LoadState>({ kind: "loading" });
  const [submitState, setSubmitState] = useState<SubmitState>({ kind: "idle" });
  const [form, setForm] = useState<FormValues>({
    workspaceId: "",
    youtubeAccountId: "",
    driveAccountId: "",
    folderId: "",
    privacyLevel: "private",
    startAt: "",
    advanced: false,
    title: "",
    descriptionPrefix: "",
    minJitterSeconds: DEFAULT_MIN_JITTER_SEC,
    maxJitterSeconds: DEFAULT_MAX_JITTER_SEC,
  });
  const abortRef = useRef<AbortController | null>(null);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => {
    setLoadState({ kind: "loading" });
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;

    void (async () => {
      try {
        const [wsR, acctsR] = await Promise.all([
          authedFetch("/api/v1/workspaces", { signal: ctrl.signal }),
          authedFetch("/api/v1/accounts", { signal: ctrl.signal }),
        ]);
        if (ctrl.signal.aborted) return;

        const ws =
          ((await wsR.json()) as { workspaces: Workspace[] }).workspaces ?? [];
        const accts =
          ((await acctsR.json()) as { accounts: PlatformAccount[] }).accounts ??
          [];
        const youtubeChannels = accts.filter(
          (a) => a.platform === "youtube",
        );
        const drives = accts.filter((a) => a.platform === "google-drive");

        setLoadState({ kind: "ready", workspaces: ws, youtubeChannels, drives });
        setForm((f) => ({
          ...f,
          workspaceId:
            f.workspaceId && ws.find((w) => w.id === f.workspaceId)
              ? f.workspaceId
              : ws.length === 1
                ? ws[0].id
                : "",
          youtubeAccountId:
            f.youtubeAccountId &&
            youtubeChannels.find((c) => c.id === f.youtubeAccountId)
              ? f.youtubeAccountId
              : youtubeChannels.length === 1
                ? youtubeChannels[0].id
                : "",
        }));
      } catch (err) {
        if (ctrl.signal.aborted) return;
        if (err instanceof AuthError) {
          navigate("/login", { replace: true });
          return;
        }
        setLoadState({
          kind: "error",
          message:
            err instanceof Error
              ? err.message
              : "Unable to load workspaces or connected accounts.",
        });
      }
    })();

    return () => ctrl.abort();
  }, [navigate]);

  const folderValid = useMemo(() => {
    if (!form.folderId.trim()) return null;
    return FOLDER_ID_PATTERN.test(form.folderId.trim());
  }, [form.folderId]);

  const jitterError = useMemo<string | null>(() => {
    if (!form.advanced) return null;
    if (form.minJitterSeconds < 1) {
      return "Minimum gap must be at least 1 second.";
    }
    if (form.minJitterSeconds < MIN_JITTER_SEC) {
      return `Minimum gap must be ≥ ${MIN_JITTER_SEC}s to avoid back-to-back anti-pattern detection.`;
    }
    if (form.maxJitterSeconds > MAX_JITTER_SEC) {
      return `Maximum gap cannot exceed ${MAX_JITTER_SEC}s (7 days).`;
    }
    if (form.maxJitterSeconds < form.minJitterSeconds) {
      return "Maximum gap must be greater than or equal to the minimum.";
    }
    return null;
  }, [form.advanced, form.minJitterSeconds, form.maxJitterSeconds]);

  const canSubmit =
    submitState.kind !== "submitting" &&
    submitState.kind !== "polling" &&
    form.workspaceId !== "" &&
    form.youtubeAccountId !== "" &&
    form.driveAccountId !== "" &&
    folderValid === true &&
    jitterError === null &&
    (form.startAt === "" || !isNaN(new Date(form.startAt).getTime()));

  const handleRunAnother = useCallback(() => {
    if (pollRef.current) {
      clearInterval(pollRef.current);
      pollRef.current = null;
    }
    setSubmitState({ kind: "idle" });
    setForm((f) => ({
      ...f,
      folderId: "",
      title: "",
      descriptionPrefix: "",
    }));
  }, []);

  const resetSubmit = useCallback(() => {
    if (pollRef.current) {
      clearInterval(pollRef.current);
      pollRef.current = null;
    }
    setSubmitState({ kind: "idle" });
  }, []);

  const startPolling = useCallback(
    (batchId: string) => {
      if (pollRef.current) clearInterval(pollRef.current);
      setSubmitState({ kind: "polling", batchId });

      const poll = async () => {
        try {
          const resp = await authedFetch(
            `/api/v1/media/import/drive/folder/async/${batchId}`,
          );
          if (!resp.ok) {
            return;
          }
          const data = (await resp.json()) as BatchStatusResponse;
          if (TERMINAL_STATUSES.has(data.status)) {
            if (pollRef.current) {
              clearInterval(pollRef.current);
              pollRef.current = null;
            }
            if (data.status === "completed") {
              setSubmitState({ kind: "queued", batchId });
              toast.success(
                `Batch completed — ${data.processed_count} file${data.processed_count === 1 ? "" : "s"} processed.`,
              );
            } else {
              setSubmitState({ kind: "error", message: `Batch ${data.status}` });
            }
          }
        } catch {
          // Network hiccup — keep polling.
        }
      };

      pollRef.current = setInterval(poll, POLL_INTERVAL_MS);
      void poll();
    },
    [toast],
  );

  const handleSubmit = useCallback(
    async (e: React.FormEvent) => {
      e.preventDefault();
      if (!canSubmit) return;
      setSubmitState({ kind: "submitting" });

      try {
        const startAtDate =
          form.startAt !== "" ? new Date(form.startAt) : new Date();

        const body = {
          source: {
            provider: "google_drive",
            drive_account_id: form.driveAccountId,
            folder_id: form.folderId.trim(),
          },
          workspace_id: form.workspaceId,
          target_account_ids: [form.youtubeAccountId],
          default_privacy_level: form.privacyLevel,
          publish_schedule: {
            start_at: startAtDate.toISOString(),
            min_gap_seconds: form.advanced
              ? form.minJitterSeconds
              : DEFAULT_MIN_JITTER_SEC,
            max_gap_seconds: form.advanced
              ? form.maxJitterSeconds
              : DEFAULT_MAX_JITTER_SEC,
          },
        };

        const response = await authedFetch(
          "/api/v1/media/import/drive/folder/async",
          {
            method: "POST",
            body: JSON.stringify(body),
          },
        );

        if (!response.ok) {
          let message = `Request failed (status ${response.status})`;
          try {
            const data = await response.json();
            if (data.error) message = data.error;
            else if (data.clamp_reason) message = data.clamp_reason;
          } catch {
            // Body wasn't JSON.
          }
          setSubmitState({ kind: "error", message });
          return;
        }

        const payload = (await response.json()) as AsyncBatchResponse;
        toast.success(`Batch queued — ${payload.batch_id.slice(0, 8)}…`);
        startPolling(payload.batch_id);
      } catch (err) {
        if (err instanceof AuthError) {
          navigate("/login", { replace: true });
          return;
        }
        setSubmitState({
          kind: "error",
          message:
            err instanceof Error ? err.message : "Unable to start the import.",
        });
      }
    },
    [
      canSubmit,
      form.advanced,
      form.descriptionPrefix,
      form.driveAccountId,
      form.folderId,
      form.maxJitterSeconds,
      form.minJitterSeconds,
      form.privacyLevel,
      form.startAt,
      form.title,
      form.workspaceId,
      form.youtubeAccountId,
      navigate,
      startPolling,
      toast,
    ],
  );

  return {
    loadState,
    submitState,
    form,
    setForm,
    folderValid,
    jitterError,
    canSubmit,
    firstFieldRef,
    handleSubmit,
    handleRunAnother,
    resetSubmit,
  };
}
