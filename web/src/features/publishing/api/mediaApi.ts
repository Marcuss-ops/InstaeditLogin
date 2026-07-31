/**
 * Typed media-asset client.
 *
 *   ┌─────────────────┐    ┌──────────────┐    ┌────────────────┐
 *   │ presignMedia    │ →  │ PUT to S3     │ →  │ completeMedia  │
 *   │ (POST presign)  │    │ (raw fetch)   │    │ (POST /complete)│
 *   └─────────────────┘    └──────────────┘    └────────────────┘
 *
 * Replaces the inline upload block in `web/src/pages/internal/Compose.tsx`
 * with a reusable, typed, tested pipeline. Pages import the
 * high-level `uploadMediaAsset` for the wizard; tests + advanced
 * flows (custom telemetry, restart-on-expiry retries) assemble the
 * three primitives directly.
 */

import { ApiError, authedFetch } from "../../../lib/auth";
import type {
  MediaAsset,
  MediaAssetContentType,
  PresignMediaRequest,
  PresignMediaResponse,
} from "./types";

// Public re-export: callers (hooks, wizard components) import these from
// `../api/mediaApi` so the api module is the single entry point — they
// don't have to reach into `./types` directly. Aligns with the pattern
// already used by `postsApi.ts` (workspace_id, status, etc.).
export type {
  MediaAsset,
  MediaAssetContentType,
  PresignMediaRequest,
  PresignMediaResponse,
};

const PRESIGN_PATH = "/api/v1/media/presign";

/**
 * Step 1 — POST /api/v1/media/presign.
 *
 * Creates a `media_assets` row in `pending` state and returns a
 * presigned S3 PUT URL plus a `Content-Type` header map the browser
 * MUST send verbatim. The `asset_id` returned here is the only
 * identifier the subsequent /complete call accepts.
 */
export async function presignMedia(
  input: PresignMediaRequest,
  signal?: AbortSignal,
): Promise<PresignMediaResponse> {
  const resp = await authedFetch(PRESIGN_PATH, {
    method: "POST",
    body: JSON.stringify(input),
    signal,
  });
  return (await resp.json()) as PresignMediaResponse;
}

/**
 * Step 2 — PUT the file bytes to the URL from presign.
 *
 * S3 expects:
 *   - method: PUT
 *   - Content-Type header matching what we declared at presign time
 *     (the server rejects /complete on content-type mismatch at the
 *     head-of-bucket verification step)
 *
 * Returns `{ ok: true }` on 2xx; throws `ApiError` on any other
 * status (S3 surfaces the canonical 403 on signature mismatch with
 * a verbose XML body that we don't propagate — the typed status
 * code is enough signal for callers that retry on 403).
 */
export async function uploadToPresignedUrl(
  url: string,
  file: Blob,
  contentType: MediaAssetContentType,
  uploadHeaders: Record<string, string> = {},
): Promise<{ ok: true }> {
  // The signature can cover more than Content-Type (for example a
  // storage checksum or provider-specific x-* header). Forward the
  // server-provided set verbatim and only supply Content-Type for
  // older presign responses that omit it.
  const headers: Record<string, string> = { ...uploadHeaders };
  if (!Object.keys(headers).some((key) => key.toLowerCase() === "content-type")) {
    headers["Content-Type"] = contentType;
  }
  const resp = await fetch(url, {
    method: "PUT",
    headers,
    body: file,
  });
  if (!resp.ok) {
    throw new ApiError(resp.status, `upload PUT failed (status ${resp.status})`);
  }
  return { ok: true };
}

const completePath = (assetId: string): string =>
  `/api/v1/media/${encodeURIComponent(assetId)}/complete`;

/**
 * Step 3 — POST /api/v1/media/{asset_id}/complete.
 *
 * Server HEADs the S3 object, validates size + content-type against
 * the presign-declared values, then flips `media_assets.status` from
 * `pending` → `ready`. Returns the canonical record.
 *
 * Failure modes the server emits (mapped to ApiError.status):
 *   - 404  asset not found / not owned
 *   - 410  asset expired (asset.expires_at <= now)
 *   - 400  SHA missing (Task 6/10 enforcement)
 *   - 422  size or content-type mismatch (asset flipped to failed)
 *   - 400  S3 object missing (asset stays pending — caller may retry)
 */
export async function completeMediaAsset(
  assetId: string,
  signal?: AbortSignal,
): Promise<MediaAsset> {
  const resp = await authedFetch(completePath(assetId), {
    method: "POST",
    signal,
  });
  return (await resp.json()) as MediaAsset;
}

/**
 * Progress phases emitted by `uploadMediaAsset`. Wizard UI binds to
 * these to render a determinate progress bar.
 */
export type UploadPhase = "presign" | "upload" | "complete";

export interface UploadProgress {
  phase: UploadPhase;
  /** Bytes uploaded so far (post-PUT only). Undefined during presign. */
  loaded?: number;
  /** Total file size. Always present post-presign. */
  total?: number;
}

export interface UploadMediaAssetOptions {
  /** Override the browser-detected MIME. Defaults to `file.type`. */
  contentType?: MediaAssetContentType;
  /** Client-computed SHA-256 hex digest (Task 6/10 enforcement). */
  sha256?: string;
  /** Optional publish cursor used by the server's TTL heuristic. */
  publish_at?: string;
  /** Forwarded to presign + complete calls (NOT to the raw PUT — S3 has its own idempotency). */
  signal?: AbortSignal;
}

/**
 * End-to-end upload helper.
 *
 * Callers should pass a fresh `File` from an `<input type="file">`
 * or a `Blob` already in memory. The function:
 *   1. resolves `contentType` (default = file.type, fallback video/mp4 for
 *      browsers that report empty MIME),
 *   2. calls presignMedia → gets upload_url + asset_id,
 *   3. PUTs the file to S3,
 *   4. calls completeMediaAsset → returns the `ready` MediaAsset record.
 *
 * The optional `onProgress` callback fires once per phase transition.
 * Per-byte progress during the PUT is NOT provided (S3 presigned PUTs
 * don't expose the request body to ReadableStream without extra
 * `XMLHttpRequest` plumbing; we'll add it later if the wizard needs
 * it).
 *
 * On failure, throws the first non-ok step's error in phase order:
 * presign → PUT → complete. The wizard should catch `ApiError` and
 * surface the typed status code (`status === 422` → "wrong file
 * type or size", `status === 410` → "upload window expired, retry").
 */
export async function uploadMediaAsset(
  file: File | Blob,
  options: UploadMediaAssetOptions = {},
  onProgress?: (p: UploadProgress) => void,
): Promise<MediaAsset> {
  const contentType: MediaAssetContentType =
    options.contentType ?? ((file.type || "video/mp4") as MediaAssetContentType);

  onProgress?.({ phase: "presign" });
  // Duck-type the optional `.name` instead of `instanceof File` so this
  // helper stays safe in non-DOM environments (workers, future SSR, tests
  // on Node-only stubs). A File is always a Blob with a string `name`;
  // a bare Blob returns undefined here → fall through to the generic name.
  const maybeName = (file as { name?: unknown }).name;
  const filename =
    typeof maybeName === "string" && maybeName.length > 0 ? maybeName : "blob";
  const grant = await presignMedia(
    {
      filename,
      content_type: contentType,
      size_bytes: file.size,
      sha256: options.sha256,
      publish_at: options.publish_at,
    },
    options.signal,
  );

  onProgress?.({ phase: "upload", loaded: 0, total: file.size });
  await uploadToPresignedUrl(
    grant.upload_url,
    file,
    contentType,
    grant.upload_headers,
  );

  onProgress?.({ phase: "complete", loaded: file.size, total: file.size });
  return completeMediaAsset(grant.asset_id, options.signal);
}
