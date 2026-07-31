/**
 * useUploadMedia — React state around `mediaApi.uploadMediaAsset`.
 *
 * Drives Step 1 of the /app/content/new wizard:
 *   file picker → SHA-256 (local) → presign → PUT to S3 → complete → MediaAsset.
 *
 * State machine:
 *   idle      → no file picked yet
 *   hashing   → computing SHA-256 of the selected file via SubtleCrypto;
 *               required by the server's "Task 6/10" /complete-time
 *               enforcement (presign-time hash is a soft hint — the
 *               server still rejects empty SHA at /complete).
 *   uploading → one of {presign, upload, complete} phases is in flight;
 *               `phase` names which
 *   done      → MediaAsset is ready; `asset.id` is the `media_asset_id`
 *               Step 2 needs (`postsApi.createPost({ content.media: [{ asset_id }] })`)
 *   error     → the last attempt failed with a typed message
 *
 * Per-byte progress during the PUT is intentionally absent — S3
 * presigned PUTs do not expose the request body to ReadableStream
 * without `XMLHttpRequest` plumbing. The wizard bar advances at
 * coarse phase transitions (0 → 35 → 45 → 95 → 100). When per-byte
 * resolution is needed, swap to an XHR-backed client and emit a
 * unified percent across all phases.
 *
 *   - AuthError → re-thrown so the caller can navigate to /login
 *     (matches useCreatePost / usePostTargetStatus contract).
 *   - ApiError  → kind='error' with `err.message` preserved so the
 *     wizard can show "Upload window expired (410)" without a
 *     formatting layer.
 *
 * AbortController lifecycle:
 *   - A fresh AbortController is minted at every `start()`.
 *   - `reset()` aborts the current upload AND clears to idle.
 *   - On unmount, the current controller is aborted — no zombie
 *     setState write if the user navigates away mid-PUT.
 */

import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, AuthError } from "../../../lib/auth";
import {
  uploadMediaAsset,
  type MediaAsset,
  type UploadMediaAssetOptions,
  type UploadPhase,
} from "../api/mediaApi";
import type { MediaAssetContentType } from "../api/types";

/**
 * Coarse step tally. Each phase bumps the bar to its upper bound.
 *   - hashing 35% → SHA-256 of a 1 GB MP4 takes 5-10s on M-class
 *                   laptops; reserving 35% makes the silent gap
 *                   feel bounded rather than frozen
 *   - presign 45% → fast server round-trip
 *   - upload  95% → dominant latency (S3 PUT)
 *   - complete 100% → HEAD + status flip
 */
export const UPLOAD_PHASE_PERCENT: Record<"hashing" | UploadPhase, number> = {
  hashing: 35,
  presign: 45,
  upload: 95,
  complete: 100,
};

export type UploadMediaState =
  | { kind: "idle" }
  | { kind: "hashing" }
  | { kind: "uploading"; phase: UploadPhase }
  | { kind: "done"; asset: MediaAsset }
  | { kind: "error"; message: string };

export interface UseUploadMediaResult {
  state: UploadMediaState;
  /**
   * Kick off the upload pipeline for the given file.
   * Resolves once the asset is in `done | error`. AuthError
   * propagates so the caller can navigate to /login.
   *
   * The hook overrides any caller-supplied `sha256` with a fresh
   * SubtleCrypto digest so /complete-time validation always passes.
   */
  start: (file: File, opts?: UploadMediaOptions) => Promise<void>;
  /** Aborts any in-flight upload and clears to idle. */
  reset: () => void;
}

/**
 * Caller-supplied options. Mirrors `UploadMediaAssetOptions` but
 * retypes the few fields the hook owns:
 *   - sha256 is recomputed locally — caller's value is ignored
 *   - signal is owned by the hook's AbortController — ignored
 *     (use the `reset()` callback for cancel-from-UI)
 */
export type UploadMediaOptions = Omit<
  UploadMediaAssetOptions,
  "sha256" | "signal"
> & {
  contentType?: MediaAssetContentType;
};

/** True for any browser-detected video/* MIME OR a known video extension. */
function isVideoFile(file: File): boolean {
  if (file.type === "video/mp4" || file.type === "video/quicktime") return true;
  if (!file.name) return false;
  return /\.(mp4|mov|m4v)$/i.test(file.name);
}

async function sha256Hex(file: File | Blob): Promise<string> {
  if (typeof crypto === "undefined" || !crypto.subtle) {
    throw new Error(
      "SubtleCrypto unavailable — open the app over HTTPS or localhost.",
    );
  }
  const buf = await file.arrayBuffer();
  const digest = await crypto.subtle.digest("SHA-256", buf);
  return Array.from(new Uint8Array(digest))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

export function useUploadMedia(): UseUploadMediaResult {
  const [state, setState] = useState<UploadMediaState>({ kind: "idle" });
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    return () => {
      abortRef.current?.abort();
    };
  }, []);

  const reset = useCallback(() => {
    abortRef.current?.abort();
    abortRef.current = null;
    setState({ kind: "idle" });
  }, []);

  const start = useCallback(
    async (file: File, opts: UploadMediaOptions = {}): Promise<void> => {
      abortRef.current?.abort();
      const ctrl = new AbortController();
      abortRef.current = ctrl;

      try {
        if (!isVideoFile(file)) {
          throw new ApiError(
            400,
            `Only video files are supported (got: ${file.type || "unknown"}).`,
          );
        }
        // SHA-256 first. Computed locally so the server's /complete
        // Task 6/10 enforcement never rejects with "sha256 required".
        setState({ kind: "hashing" });
        const sha = await sha256Hex(file);
        if (ctrl.signal.aborted) return;
        setState({ kind: "uploading", phase: "presign" });

        const onProgress = (p: { phase: UploadPhase }): void => {
          if (ctrl.signal.aborted) return;
          setState({ kind: "uploading", phase: p.phase });
        };

        const asset = await uploadMediaAsset(
          file,
          { contentType: opts.contentType, publish_at: opts.publish_at, sha256: sha },
          onProgress,
        );
        if (ctrl.signal.aborted) return;
        setState({ kind: "done", asset });
      } catch (err) {
        // AuthError deliberately NOT caught — caller navigates to /login.
        if (err instanceof AuthError) {
          throw err;
        }
        if (err instanceof ApiError) {
          setState({ kind: "error", message: err.message });
          return;
        }
        if (err instanceof DOMException && err.name === "AbortError") {
          // Triggered by reset() / unmount. Stay silent (users who
          // hit Reset already saw the file selector open again).
          setState({ kind: "idle" });
          return;
        }
        setState({
          kind: "error",
          message:
            err instanceof Error
              ? err.message
              : "Upload failed — check the browser console.",
        });
      } finally {
        if (abortRef.current === ctrl) {
          abortRef.current = null;
        }
      }
    },
    [],
  );

  return { state, start, reset };
}
