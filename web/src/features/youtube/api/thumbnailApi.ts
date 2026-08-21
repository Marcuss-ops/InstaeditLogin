import { authedFetch } from "../../../lib/auth";
import { uploadMediaAsset } from "../../publishing/api/mediaApi";

const MAX_THUMBNAIL_BYTES = 2 * 1024 * 1024;
const COMPACT_TARGET_BYTES = 1_800_000;

async function compactThumbnail(file: File): Promise<File> {
  if (file.size <= MAX_THUMBNAIL_BYTES) return file;
  const url = URL.createObjectURL(file);
  try {
    const image = new Image();
    image.src = url;
    await image.decode();
    const canvas = document.createElement("canvas");
    // YouTube thumbnails are 16:9; drawing at 1280×720 also removes
    // oversized camera/source dimensions before JPEG compression.
    canvas.width = 1280;
    canvas.height = 720;
    const context = canvas.getContext("2d");
    if (!context) throw new Error("Impossibile preparare la copertina.");
    const sourceRatio = image.width / image.height;
    const targetRatio = canvas.width / canvas.height;
    let sx = 0; let sy = 0; let sw = image.width; let sh = image.height;
    if (sourceRatio > targetRatio) {
      sw = image.height * targetRatio;
      sx = (image.width - sw) / 2;
    } else if (sourceRatio < targetRatio) {
      sh = image.width / targetRatio;
      sy = (image.height - sh) / 2;
    }
    context.fillStyle = "#000";
    context.fillRect(0, 0, canvas.width, canvas.height);
    context.drawImage(image, sx, sy, sw, sh, 0, 0, canvas.width, canvas.height);
    for (const quality of [0.86, 0.78, 0.7, 0.62, 0.54, 0.46]) {
      const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, "image/jpeg", quality));
      if (blob && blob.size <= COMPACT_TARGET_BYTES) {
        return new File([blob], file.name.replace(/\.[^.]+$/, "") + ".jpg", { type: "image/jpeg" });
      }
    }
    throw new Error("La copertina supera ancora 2 MB dopo la compressione automatica.");
  } finally {
    URL.revokeObjectURL(url);
  }
}

export async function uploadThumbnailFile(file: File) {
  if (!file.type || !["image/jpeg", "image/png"].includes(file.type)) {
    throw new Error("La copertina deve essere un file JPG o PNG.");
  }
  file = await compactThumbnail(file);
  if (typeof crypto === "undefined" || !crypto.subtle) throw new Error("Il browser non supporta la verifica sicura del file.");
  const digest = await crypto.subtle.digest("SHA-256", await file.arrayBuffer());
  const sha256 = Array.from(new Uint8Array(digest)).map((byte) => byte.toString(16).padStart(2, "0")).join("");
  return uploadMediaAsset(file, { contentType: file.type as "image/jpeg" | "image/png", sha256 });
}

export async function publishGroupThumbnail(
  groupId: number,
  videoId: string,
  platformAccountId: number,
  thumbnailMediaId: string,
) {
  const response = await authedFetch(
    `/api/v1/groups/${groupId}/youtube/videos/${encodeURIComponent(videoId)}/thumbnail`,
    {
      method: "POST",
      body: JSON.stringify({ platform_account_id: platformAccountId, thumbnail_media_id: thumbnailMediaId }),
    },
  );
  return response.json();
}

export async function publishAccountThumbnail(
  accountId: number,
  videoId: string,
  thumbnailMediaId: string,
) {
  const response = await authedFetch(
    `/api/v1/accounts/${accountId}/youtube/videos/${encodeURIComponent(videoId)}/thumbnail`,
    { method: "POST", body: JSON.stringify({ thumbnail_media_id: thumbnailMediaId }) },
  );
  return response.json();
}
