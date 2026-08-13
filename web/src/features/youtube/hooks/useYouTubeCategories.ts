/**
 * Shared YouTube-categories query.
 *
 * The project has no react-query dependency — the shared query registry
 * (lib/queryRegistry) provides the same primitives (cache, staleTime,
 * refetch, deduplication across mounted surfaces). The key mirrors the
 * react-query style ['youtube', 'categories', regionCode] flattened to
 * the registry's single-string key.
 *
 * Categories change rarely; a day-long staleTime keeps every category
 * select (metadata drawer, livestreams wizard, …) on at most one
 * request per session and region.
 */

import { useSharedQuery } from "../../../lib/queryRegistry";
import { getVideoCategories, type YouTubeCategory } from "../api/categoriesApi";

export function youtubeCategoriesQueryKey(regionCode: string): string {
  return ["youtube", "categories", regionCode].join(":");
}

const CATEGORIES_STALE_TIME_MS = 24 * 60 * 60 * 1000;

export function useYouTubeCategories(regionCode = "IT") {
  return useSharedQuery<YouTubeCategory[]>(youtubeCategoriesQueryKey(regionCode), {
    staleTime: CATEGORIES_STALE_TIME_MS,
    fetcher: async (signal) => getVideoCategories(regionCode, { signal }),
  });
}
