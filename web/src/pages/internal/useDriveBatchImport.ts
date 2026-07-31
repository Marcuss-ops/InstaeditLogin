import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { authedFetch, AuthError } from "../../lib/auth";
import type { FormEvent } from "react";
import {
  DEFAULT_MAX_JITTER_MIN,
  DEFAULT_MIN_JITTER_MIN,
  FOLDER_ID_PATTERN,
  MAX_JITTER_MIN,
  MIN_JITTER_MIN,
  type BatchResponse,
  type FormValues,
  type LoadState,
  type PlatformAccount,
  type SubmitState,
  type Workspace,
} from "./driveBatchImportTypes";

export function useDriveBatchImport({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  
    const navigate = useNavigate();
    const cardRef = useRef<HTMLDivElement | null>(null);
    const firstFieldRef = useRef<HTMLInputElement | null>(null);
  
    const [loadState, setLoadState] = useState<LoadState>({ kind: "loading" });
    const [submitState, setSubmitState] = useState<SubmitState>({ kind: "idle" });
  
    const [form, setForm] = useState<FormValues>({
      workspaceId: "",
      facebookAccountId: "",
      folderId: "",
      advanced: false,
      title: "",
      captionPrefix: "",
      minJitterMinutes: DEFAULT_MIN_JITTER_MIN,
      maxJitterMinutes: DEFAULT_MAX_JITTER_MIN,
    });
    // Pagination cursors. They live as plain state so the success view can
    // promote them before re-submitting. Don't bind them to the form so
    // users don't see advanced fields they shouldn't touch by hand.
    const [pageToken, setPageToken] = useState("");
    const [cursorScheduledAt, setCursorScheduledAt] = useState("");
  
    const abortRef = useRef<AbortController | null>(null);
  
    // Re-fetch workspaces + Facebook pages every time the dialog opens so we
    // don't show stale targets (the user may have unlinked a page between
    // visits). Mirrors the pattern from Compose.tsx.
    useEffect(() => {
      if (!open) return;
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
            ((await wsR.json()) as { workspaces: Workspace[] }).workspaces ??
            [];
          const accts =
            ((await acctsR.json()) as { accounts: PlatformAccount[] })
              .accounts ?? [];
          const pages = accts.filter((a) => a.platform === "facebook");
          setLoadState({ kind: "ready", workspaces: ws, pages });
          setForm((f) => ({
            ...f,
            // Pre-select when only one option exists; reset if it disappeared.
            workspaceId:
              f.workspaceId && ws.find((w) => w.id === f.workspaceId)
                ? f.workspaceId
                : ws.length === 1
                  ? ws[0].id
                  : "",
            facebookAccountId:
              f.facebookAccountId && pages.find((p) => p.id === f.facebookAccountId)
                ? f.facebookAccountId
                : pages.length === 1
                  ? pages[0].id
                  : "",
          }));
        } catch (err) {
          if (ctrl.signal.aborted) return;
          if (err instanceof AuthError) {
            onClose();
            return;
          }
          setLoadState({
            kind: "error",
            message:
              err instanceof Error
                ? err.message
                : "Unable to load workspaces or pages.",
          });
        }
      })();
      return () => ctrl.abort();
    }, [open, onClose]);
  
    // Reset submit state + cursors when the dialog re-opens so a "back" from
    // the success view doesn't carry pagination state into a fresh import.
    useEffect(() => {
      if (open) {
        setSubmitState({ kind: "idle" });
        setPageToken("");
        setCursorScheduledAt("");
      }
    }, [open]);
  
    // Close on Escape (matches CookieBanner.tsx + AccountSwitcher pattern).
    useEffect(() => {
      if (!open) return;
      const onKey = (e: KeyboardEvent) => {
        if (e.key === "Escape") onClose();
      };
      document.addEventListener("keydown", onKey);
      return () => document.removeEventListener("keydown", onKey);
    }, [open, onClose]);
  
    // Focus-on-open is intentionally NOT done at the dialog-root level
    // because `firstFieldRef` is attached inside `ImportForm`, which renders
    // only AFTER `loadState` flips to `ready` (loadState=loading shows a
    // skeleton during the workspaces + accounts fetch). Running the focus
    // here would no-op on the first open with `firstFieldRef.current === null`.
    // See `ImportForm`'s mount effect for the actual focus trigger.
  
    // Block background scroll while the modal is open. Capture the previous
    // value so cleanup can restore it; with only one modal in the app today
    // there's no contention, but a single capture keeps the behaviour robust
    // if a second modal ever arrives.
    useEffect(() => {
      if (!open) return;
      const prev = document.body.style.overflow;
      document.body.style.overflow = "hidden";
      return () => {
        document.body.style.overflow = prev;
      };
    }, [open]);
  
    const folderValid = useMemo(() => {
      if (!form.folderId.trim()) return null;
      return FOLDER_ID_PATTERN.test(form.folderId.trim());
    }, [form.folderId]);
  
    const jitterError = useMemo<string | null>(() => {
      if (!form.advanced) return null;
      if (form.minJitterMinutes < MIN_JITTER_MIN) return null;
      if (form.minJitterMinutes > MAX_JITTER_MIN) return null;
      if (form.maxJitterMinutes < form.minJitterMinutes) return null;
      return null;
    }, [form.advanced, form.minJitterMinutes, form.maxJitterMinutes]);
  
    const canSubmit =
      submitState.kind !== "submitting" &&
      form.workspaceId !== "" &&
      form.facebookAccountId !== "" &&
      folderValid === true &&
      jitterError === null;
  
    const handleSubmit = useCallback(
      async (e: FormEvent) => {
        e.preventDefault();
        if (!canSubmit) return;
        setSubmitState({ kind: "submitting" });
        const minutes = (n: number) => Math.round(n * 60);
        try {
          const response = await authedFetch(
            "/api/v1/media/import/drive/folder",
            {
              method: "POST",
              body: JSON.stringify({
                folder_id: form.folderId.trim(),
                workspace_id: form.workspaceId,
                facebook_account_id: form.facebookAccountId,
                title: form.title.trim() || undefined,
                caption_prefix: form.captionPrefix.trim() || undefined,
                min_jitter_seconds: form.advanced
                  ? minutes(form.minJitterMinutes)
                  : undefined,
                max_jitter_seconds: form.advanced
                  ? minutes(form.maxJitterMinutes)
                  : undefined,
                page_token: pageToken || undefined,
                cursor_scheduled_at: cursorScheduledAt || undefined,
              }),
            },
          );
          if (!response.ok) {
            // authedFetch already toasts but we also surface inline. Try to
            // pull a server-side message first; fall back to the status code.
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
          // Configuration guidance from the server (no API key on server,
          // public folder that needs OAuth). 200 + structural flags rather
          // than a hard error so the user can fix config without a refresh.
          if (payload.needs_google_drive_api_key || payload.needs_drive_account) {
            setSubmitState({
              kind: "guidance",
              note:
                payload.note ||
                "Server is missing configuration to list this public Drive folder.",
            });
            return;
          }
          setSubmitState({
            kind: "success",
            payload: {
              folderId: payload.folder_id,
              scheduledCount: payload.scheduled_count,
              firstPublishAt: payload.first_publish_at,
              lastScheduledAt: payload.last_scheduled_at,
              entries: payload.entries ?? [],
              cursorClampedToNow: payload.cursor_clamped_to_now,
            },
            nextPageToken: payload.next_page_token,
          });
          // Promote response cursors so the next-page submit can honour them.
          // Done outside React's render path so the success view shows the
          // just-imported batch with a clear "Continue" affordance.
          if (payload.next_page_token) {
            setPageToken(payload.next_page_token);
            setCursorScheduledAt(payload.last_scheduled_at);
          }
        } catch (err) {
          if (err instanceof AuthError) {
            onClose();
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
        cursorScheduledAt,
        form.folderId,
        form.workspaceId,
        form.facebookAccountId,
        form.title,
        form.captionPrefix,
        form.advanced,
        form.minJitterMinutes,
        form.maxJitterMinutes,
        onClose,
        pageToken,
      ],
    );
  
    const handleContinuePagination = useCallback(() => {
      if (submitState.kind !== "success") return;
      setSubmitState({ kind: "idle" });
    }, [submitState]);
  
    const handleViewPosts = useCallback(() => {
      onClose();
      navigate("/app/posts");
    }, [navigate, onClose]);
  
    const handleBackToForm = useCallback(() => {
      setSubmitState({ kind: "idle" });
    }, []);

  return {
    cardRef,
    firstFieldRef,
    loadState,
    submitState,
    form,
    setForm,
    folderValid,
    jitterError,
    handleSubmit,
    handleContinuePagination,
    handleViewPosts,
    handleBackToForm,
  };
}
