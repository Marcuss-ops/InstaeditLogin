import { useGroupYouTubeVideos } from "./useGroupYouTubeVideos";
import { GroupVideoManager } from "./GroupVideoManager";

/**
 * Video list panel for a group (Groups detail tab / Calendar). Thin
 * wrapper: owns the canonical video-list hook and renders the shared
 * GroupVideoManager (search + visibility tabs + category filter +
 * VideoGrid + details modal).
 */
export function GroupYouTubeVideos({ groupId, groupName }: { groupId: number; groupName?: string }) {
  const controller = useGroupYouTubeVideos(groupId, true, groupName);
  return <GroupVideoManager controller={controller} />;
}
