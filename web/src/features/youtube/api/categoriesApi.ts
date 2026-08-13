/**
 * YouTube video-categories client.
 *
 * Single canonical source for the YouTube category resource shared by
 * EVERY form with a category select (the future GroupCovers metadata
 * drawer, the livestreams wizard, …). The categories themselves change
 * rarely, so consumers should go through `useYouTubeCategories`
 * (features/youtube/hooks) — the shared query registry caches the list
 * under `youtube:categories:<regionCode>` and deduplicates concurrent
 * callers.
 *
 * Server contract (planned):
 *
 *   GET /api/v1/youtube/video-categories?region_code=IT
 *     resp: { categories: [{ id, label }, …] }
 *     200:  the videoCategories.list projection for the region
 *
 * The backend proxy is not deployed yet — videoCategories.list needs
 * an OAuth token the backend owns — so `getVideoCategories` serves the
 * canonical `YOUTUBE_CATEGORIES` snapshot (same shape) whenever the
 * endpoint 404s. The swap is transparent to consumers.
 *
 * apiClient is used instead of authedFetch on purpose: authedFetch
 * toasts every non-2xx, and a missing endpoint (404) must resolve to
 * the snapshot, not to a spurious error toast.
 */

import { ApiClientError, apiClient } from "../../../lib/api-client";

/** One YouTube video category (id = the numeric categoryId). */
export interface YouTubeCategory {
  id: string;
  label: string;
}

export const YOUTUBE_CATEGORIES_PATH = "/api/v1/youtube/video-categories";

/**
 * Canonical snapshot of YouTube's videoCategories.list (default/global
 * region, Italian labels). This is the single source of truth for the
 * category selects until the backend endpoint lands; it also doubles
 * as the 404 fallback so forms never break on an undeployed backend.
 */
export const YOUTUBE_CATEGORIES: YouTubeCategory[] = [
  { id: "1", label: "Film e animazione" },
  { id: "2", label: "Auto e veicoli" },
  { id: "10", label: "Musica" },
  { id: "15", label: "Animali domestici" },
  { id: "17", label: "Sport" },
  { id: "18", label: "Cortometraggi" },
  { id: "19", label: "Viaggi e eventi" },
  { id: "20", label: "Gaming" },
  { id: "21", label: "Video blog" },
  { id: "22", label: "Persone e blog" },
  { id: "23", label: "Commedia" },
  { id: "24", label: "Intrattenimento" },
  { id: "25", label: "Notizie e politica" },
  { id: "26", label: "Istruzione e tutorial" },
  { id: "27", label: "Educazione" },
  { id: "28", label: "Scienza e tecnologia" },
  { id: "29", label: "Non profit e attivismo" },
  { id: "42", label: "Shorts" },
];

interface VideoCategoriesResponse {
  categories?: YouTubeCategory[];
}

/**
 * GET /api/v1/youtube/video-categories — the categories available for
 * a region (ISO 3166-1 alpha-2, e.g. "IT").
 *
 * Falls back to the canonical `YOUTUBE_CATEGORIES` snapshot when the
 * endpoint is not deployed yet (404); every other failure propagates
 * so callers can distinguish "server unreachable" from "endpoint
 * missing".
 */
export async function getVideoCategories(
  regionCode: string,
  options: { signal?: AbortSignal } = {},
): Promise<YouTubeCategory[]> {
  const params = new URLSearchParams({ region_code: regionCode });
  try {
    const data = await apiClient<VideoCategoriesResponse>(
      `${YOUTUBE_CATEGORIES_PATH}?${params.toString()}`,
      { signal: options.signal },
    );
    if (Array.isArray(data.categories)) return data.categories;
  } catch (error) {
    // 404 = the videoCategories proxy is not deployed on this backend
    // yet; serve the canonical snapshot instead of failing the form.
    if (!(error instanceof ApiClientError) || error.status !== 404) throw error;
  }
  return YOUTUBE_CATEGORIES;
}
