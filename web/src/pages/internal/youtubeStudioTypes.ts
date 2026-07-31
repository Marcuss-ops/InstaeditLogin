import type { EditorSession, PlatformAccount, Workspace } from "../../types/uploads";

export type LoadState =
  | { kind: "loading" }
  | {
      kind: "ready";
      workspaces: Workspace[];
      youtubeChannels: PlatformAccount[];
      sessions: EditorSession[];
    }
  | { kind: "error"; message: string };

export type ActionState =
  | { kind: "idle" }
  | { kind: "creating" }
  | { kind: "attaching"; sessionId: string }
  | { kind: "publishing"; sessionId: string };

export type ContentItem = {
  external_id: string;
  title?: string;
  description?: string;
  thumbnail_url?: string;
  public_url?: string;
  privacy?: string;
  status?: string;
  published_at?: string;
  duration?: string;
};

export type { EditorSession, PlatformAccount, Workspace } from "../../types/uploads";
