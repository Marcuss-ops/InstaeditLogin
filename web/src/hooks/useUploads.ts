import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { AuthError, authedFetch } from "../lib/auth";
import { useToast } from "../components/toast";
import type {
  BatchResponse,
  FormValues,
  LoadState,
  PartialPayload,
  PlatformAccount,
  SubmitState,
  SuccessPayload,
  Workspace,
} from "../types/uploads";
import {
  DEFAULT_MAX_JITTER_SEC,
  DEFAULT_MIN_JITTER_SEC,
  FOLDER_ID_PATTERN,
  MAX_JITTER_SEC,
  MIN_JITTER_SEC,
} from "../types/uploads";

export function useUploads() {
  const navigate = useNavigate();
  const toast = useToast();
  const firstFieldRef = useRef<HTMLInputElement | null>(null);

  const [loadState, setLoadState] = useState<LoadState>({ kind: "loading" });
  const [submitState, setSubmitState] = useState<SubmitState>({ kind: "idle" });
  const [form, setForm] = useState<FormValues>({
    workspaceId: "",
    facebookAccountId: "",
    driveAccountId: "",
    folderId: "",
    advanced: false,
    title: "",
    captionPrefix: "",
    minJitterSeconds: DEFAULT_MIN_JITTER_SEC,
    maxJitterSeconds: DEFAULT_MAX_JITTER_SEC,
  });
  const abortRef = useRef<AbortController | null>(null);

  // Fetch dependencies (workspaces + facebook pages + drive accounts)
  // every time the page mounts. Mirrors the pattern from Compose.tsx so
  // the dropdowns are never stale (a freshly disconnected drive account
  // disappears from the list on next mount).
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
        const pages = accts.filter((a) => a.platform === "facebook");
        const drives = accts.filter((a) => a.platform === "google-drive");

        setLoadState({ kind: "ready", workspaces: ws, pages, drives });
        setForm((f) => ({
          ...f,
          workspaceId:
            f.workspaceId && ws.find((w) => w.id === f.workspaceId)
              ? f.workspaceId
              : ws.length === 1
                ? ws[0].id
                : "",
          facebookAccountId:
            f.facebookAccountId &&
            pages.find((p) => p.id === f.facebookAccountId)
              ? f.facebookAccountId
              : pages.length === 1
                ? pages[0].id
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
    form.workspaceId !== "" &&
    form.facebookAccountId !== "" &&
    folderValid === true &&
    jitterError === null;

  const handleRunAnother = useCallback(() => {
    setSubmitState({ kind: "idle" });
    setForm((f) => ({
      ...f,
      folderId: "",
      title: "",
      captionPrefix: "",
    }));
  }, []);

  const resetSubmit = useCallback(() => setSubmitState({ kind: "idle" }), []);

  const handleSubmit = useCallback(
    async (e: React.FormEvent) => {
      e.preventDefault();
      if (!canSubmit) return;
      setSubmitState({ kind: "submitting" });

      try {
        const body: Record<string, unknown> = {
          folder_id: form.folderId.trim(),
          workspace_id: form.workspaceId,
          facebook_account_id: form.facebookAccountId,
        };
        if (form.driveAccountId !== "") {
          body.drive_account_id = form.driveAccountId;
        }
        if (form.title.trim()) body.title = form.title.trim();
        if (form.captionPrefix.trim()) {
          body.caption_prefix = form.captionPrefix.trim();
        }
        if (form.advanced) {
          body.min_jitter_seconds = form.minJitterSeconds;
          body.max_jitter_seconds = form.maxJitterSeconds;
        }

        const response = await authedFetch("/api/v1/uploads/batch/by-folder", {
          method: "POST",
          body: JSON.stringify(body),
        });
        if (!response.ok) {
          let message = `Request failed (status ${response.status})`;
          try {
            const data = (await response.json()) as Partial<BatchResponse>;
            if (data.error) message = data.error;
            else if (data.note) message = data.note;
          } catch {
            // Body wasn't JSON.
          }
          setSubmitState({ kind: "error", message });
          return;
        }

        const payload = (await response.json()) as BatchResponse;
        if (payload.needs_google_drive_api_key || payload.needs_drive_account) {
          setSubmitState({
            kind: "guidance",
            note:
              payload.note ||
              "Server is missing configuration to list this public Drive folder.",
          });
          return;
        }
        if (payload.partial_failure) {
          const partialPayload: PartialPayload = {
            folderId: payload.folder_id,
            scheduledCount: payload.scheduled_count,
            pageCount: payload.page_count,
            entries: payload.entries ?? [],
            failedAtPage: payload.failed_at_page ?? -1,
            failedAtPageToken: payload.failed_at_page_token ?? "",
            note: payload.note ?? "",
            firstPublishAt: payload.first_publish_at,
            lastScheduledAt: payload.last_scheduled_at,
          };
          setSubmitState({
            kind: "partial",
            payload: partialPayload,
          });
          toast.warning(
            "Import partially completed — see resume instructions below.",
          );
          return;
        }

        const successPayload: SuccessPayload = {
          folderId: payload.folder_id,
          scheduledCount: payload.scheduled_count,
          pageCount: payload.page_count,
          firstPublishAt: payload.first_publish_at,
          lastScheduledAt: payload.last_scheduled_at,
          entries: payload.entries ?? [],
        };
        setSubmitState({
          kind: "success",
          payload: successPayload,
        });
        toast.success(
          `Scheduled ${payload.scheduled_count} video${payload.scheduled_count === 1 ? "" : "s"} from ${payload.folder_id.slice(0, 6)}…`,
        );
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
      form.captionPrefix,
      form.driveAccountId,
      form.facebookAccountId,
      form.folderId,
      form.maxJitterSeconds,
      form.minJitterSeconds,
      form.title,
      form.workspaceId,
      navigate,
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
