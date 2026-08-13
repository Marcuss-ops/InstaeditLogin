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
 * Server contract:
 *
 *   GET /api/v1/youtube/video-categories?region_code=IT
 *     resp: { categories: [{ id, label }, …] }
 *     200:  the videoCategories.list projection for the region
 *
 * The backend proxy is live (it mints an OAuth token and calls
 * videoCategories.list), so `getVideoCategories` returns the real
 * projection verbatim and lets failures propagate to the shared query
 * registry, which surfaces them as an inline error snapshot.
 *
 * apiClient is used instead of authedFetch on purpose: authedFetch
 * toasts every non-2xx, while a category fetch failure should surface
 * as an inline error state in the form, not a viewport-level toast.
 */

import { apiClient } from "../../../lib/api-client";

/** One YouTube video category (id = the numeric categoryId). */
export interface YouTubeCategory {
  id: string;
  label: string;
}

export const YOUTUBE_CATEGORIES_PATH = "/api/v1/youtube/video-categories";

/**
 * Static id → label reference for YouTube's videoCategories.list
 * (default/global region, Italian labels). Used where a row only
 * carries a `category_id` and the UI must render a label WITHOUT
 * fetching the category list (e.g. `categoryLabel` on the video cards,
 * the livestreams wizard). It is NOT a fetch fallback: the category
 * select itself goes through `getVideoCategories`.
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
 * Returns the live projection verbatim; failures propagate so the
 * shared query registry can surface them as an inline error state.
 */
export async function getVideoCategories(
  regionCode: string,
  options: { signal?: AbortSignal } = {},
): Promise<YouTubeCategory[]> {
  const params = new URLSearchParams({ region_code: regionCode });
  const data = await apiClient<VideoCategoriesResponse>(
    `${YOUTUBE_CATEGORIES_PATH}?${params.toString()}`,
    { signal: options.signal },
  );
  return data.categories ?? [];
}
