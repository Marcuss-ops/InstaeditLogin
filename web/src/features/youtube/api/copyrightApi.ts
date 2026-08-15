import { authedFetch } from "../../../lib/auth";

export type YouTubeCopyrightStatus =
  | "pending"
  | "processing"
  | "clear"
  | "claim"
  | "blocked"
  | "error";

export type YouTubeCopyrightCheck = {
  status: YouTubeCopyrightStatus;
  message?: string;
  processingStatus?: string;
  rejectionReason?: string;
  failureReason?: string;
  licensedContent?: boolean;
  blockedRegions?: string[];
  allowedRegions?: string[];
};

export type YouTubeCopyrightAlert = {
  youtube_video_id: string;
  status: YouTubeCopyrightStatus;
  message?: string;
  processing_status?: string;
  rejection_reason?: string;
  failure_reason?: string;
  licensed_content?: boolean;
  blocked_regions?: string[];
  allowed_regions?: string[];
};

/** Runs a fresh, account-scoped check for a private YouTube video. */
export async function checkYouTubeCopyright(
  accountId: number,
  videoId: string,
  signal?: AbortSignal,
): Promise<YouTubeCopyrightCheck> {
  const params = new URLSearchParams({
    account_id: String(accountId),
    video_id: videoId,
  });
  const response = await authedFetch(`/api/v1/youtube/copyright-check?${params}`, {
    signal,
  });
  return (await response.json()) as YouTubeCopyrightCheck;
}

/** Returns known claim/block/error alerts for the signed-in workspace. */
export async function listYouTubeCopyrightAlerts(
  signal?: AbortSignal,
): Promise<YouTubeCopyrightAlert[]> {
  const response = await authedFetch("/api/v1/youtube/copyright-alerts", { signal });
  const data = (await response.json()) as { alerts?: YouTubeCopyrightAlert[] };
  return data.alerts ?? [];
}

export function isCopyrightProblem(status: YouTubeCopyrightStatus | undefined): boolean {
  return status === "claim" || status === "blocked" || status === "error";
}
