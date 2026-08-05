/**
 * Demo mode fixtures and request interception.
 *
 * When `VITE_DEMO_MODE` is set to the literal string `"true"`, the SPA
 * runs entirely against the static data below instead of the Go backend.
 * This is useful for Vercel previews when the backend is not yet deployed
 * or when showing the UI to stakeholders before the OAuth flow is wired.
 *
 * The fixtures are intentionally small (1 account, 2 posts, 1 workspace)
 * so the demo feels realistic without requiring a real database.
 *
 * To enable demo mode, set in Vercel / local env:
 *   VITE_DEMO_MODE=true
 *   VITE_API_BASE_URL=https://api.example.com  (any syntactically valid URL)
 */

export function isDemoMode(): boolean {
  return import.meta.env.VITE_DEMO_MODE === "true";
}

export const demoSession = {
  user_id: 1,
  name: "Demo User",
  username: "demo_user",
  expires_at: new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString(),
  is_admin: false,
};

export const demoAccounts = [
  {
    id: 1,
    platform: "instagram" as const,
    username: "instaedit_demo",
    created_at: new Date(Date.now() - 7 * 24 * 60 * 60 * 1000).toISOString(),
  },
  {
    id: 2,
    platform: "youtube" as const,
    platform_user_id: "UCdemo",
    username: "wwe_demo",
    status: "connected",
    is_publishable: true,
    created_at: new Date(Date.now() - 14 * 24 * 60 * 60 * 1000).toISOString(),
  },
];

const demoPrivateVideos = [
  {
    external_id: "video_demo_1",
    title: "Breaking News — riservata",
    privacy: "private",
    status: "uploaded",
    published_at: new Date(Date.now() - 3600 * 1000).toISOString(),
  },
  {
    external_id: "video_demo_2",
    title: "Highlights settimanali (bozza)",
    privacy: "private",
    status: "uploaded",
    published_at: new Date(Date.now() - 7200 * 1000).toISOString(),
  },
];

// Demo rendered export asset: a stable pseudo-UUID so the media resolver
// can mint a placeholder preview URL for it.
const demoExportMediaId = "11111111-1111-4111-8111-111111111111";

export const demoWorkspaces = [
  {
    id: 1,
    name: "Personal",
    created_at: new Date(Date.now() - 30 * 24 * 60 * 60 * 1000).toISOString(),
  },
];

type DemoThumbnailProject = {
  id: string;
  workspace_id: number;
  created_by: number;
  name: string;
  description: string;
  canvas_width: number;
  canvas_height: number;
  status: "draft" | "ready";
  current_revision_id: string | null;
  preview_media_id: string | null;
  latest_export_id: string | null;
  version: number;
  created_at: string;
  updated_at: string;
};

export const demoThumbnailProjects: DemoThumbnailProject[] = [
  {
    id: "thumbproj_demo_1",
    workspace_id: 1,
    created_by: 1,
    name: "WWE Breaking News",
    description: "Copertina demo autonoma",
    canvas_width: 1920,
    canvas_height: 1080,
    status: "draft" as const,
    current_revision_id: "thumbrev_demo_1",
    preview_media_id: null,
    latest_export_id: null,
    version: 3,
    created_at: new Date(Date.now() - 2 * 24 * 60 * 60 * 1000).toISOString(),
    updated_at: new Date(Date.now() - 1 * 60 * 60 * 1000).toISOString(),
  },
  {
    id: "thumbproj_demo_2",
    workspace_id: 1,
    created_by: 1,
    name: "Summer Short",
    description: "",
    canvas_width: 1080,
    canvas_height: 1920,
    status: "ready" as const,
    current_revision_id: "thumbrev_demo_2",
    preview_media_id: null,
    latest_export_id: "thumbexp_demo_2",
    version: 5,
    created_at: new Date(Date.now() - 5 * 24 * 60 * 60 * 1000).toISOString(),
    updated_at: new Date(Date.now() - 30 * 60 * 1000).toISOString(),
  },
];

let nextThumbnailProjectVersion = 10;

function thumbnailProjectPathMatch(path: string): { id: string; rest: string } | null {
  const match = /^\/api\/v1\/thumbnail-projects\/([^/]+)(\/.*)?$/.exec(path);
  if (!match) return null;
  return { id: decodeURIComponent(match[1]!), rest: match[2] ?? "" };
}

export const demoPosts = [
  {
    id: 1,
    workspace_id: 1,
    title: "Welcome to InstaEdit",
    caption:
      "Schedule and publish your content to multiple social platforms from one place. #instaedit #socialmedia",
    scheduled_at: null,
    status: "published",
    created_at: new Date(Date.now() - 2 * 24 * 60 * 60 * 1000).toISOString(),
  },
  {
    id: 2,
    workspace_id: 1,
    title: "Summer campaign teaser",
    caption:
      "Something big is coming this summer. Stay tuned for updates! 🌞",
    scheduled_at: new Date(Date.now() + 2 * 24 * 60 * 60 * 1000).toISOString(),
    status: "queued",
    created_at: new Date(Date.now() - 1 * 24 * 60 * 60 * 1000).toISOString(),
  },
];

let nextPostId = demoPosts.length + 1;

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function notImplemented() {
  return json({ error: "demo: endpoint not mocked" }, 501);
}

/**
 * Intercept a request and return a mock Response when demo mode is active.
 * Returns `null` when demo mode is off so the caller can fall through to
 * the real fetch.
 */
export function handleDemoRequest(
  path: string,
  init: { method?: string; body?: unknown } = {},
): Response | null {
  if (!isDemoMode()) {
    return null;
  }

  const method = (init.method ?? "GET").toUpperCase();

  if (path === "/api/v1/auth/me") {
    return json({
      user_id: demoSession.user_id,
      name: demoSession.name,
      username: demoSession.username,
      expires_at: demoSession.expires_at,
    });
  }

  if (path === "/api/v1/auth/login" || path === "/api/v1/auth/logout") {
    return json({ ok: true });
  }

  if (path === "/api/v1/accounts") {
    return json({ accounts: demoAccounts });
  }

  // Explicit soft-disconnect (P1): acknowledge and drop the account from
  // the demo list (the real endpoint keeps the row for audit but removes
  // it from every app surface, which the demo list mirrors).
  const disconnectMatch = /^\/api\/v1\/accounts\/(\d+)\/disconnect$/.exec(
    path,
  );
  if (disconnectMatch && method === "POST") {
    const id = Number(disconnectMatch[1]);
    const idx = demoAccounts.findIndex((a) => a.id === id);
    if (idx >= 0) demoAccounts.splice(idx, 1);
    return json({ ok: true }, 204);
  }

  // Permanent delete (P1): the real endpoint tombstones the account row
  // (kept for FK integrity) but removes it from every app surface; the demo
  // list mirrors that by dropping it entirely.
  const deleteDataMatch = /^\/api\/v1\/accounts\/(\d+)\/data$/.exec(path);
  if (deleteDataMatch && method === "DELETE") {
    const id = Number(deleteDataMatch[1]);
    const idx = demoAccounts.findIndex((a) => a.id === id);
    if (idx >= 0) demoAccounts.splice(idx, 1);
    return json({ ok: true }, 204);
  }

  // "Rimuovi dalla cartella": dedicated DELETE for a single membership.
  // The demo aggregate stays read-only, so the write is acknowledged as
  // a no-op success (mirrors the real 204 endpoint contract).
  const removeGroupAccountMatch =
    /^\/api\/v1\/groups\/(\d+)\/accounts\/(\d+)$/.exec(path);
  if (removeGroupAccountMatch && method === "DELETE") {
    return json({ ok: true }, 204);
  }

  // Groups aggregate (group → member account_ids) for the Link-to-video
  // dialog's Gruppo → Canale filter. Mirrors GET /api/v1/groups/aggregate.
  if (path === "/api/v1/groups/aggregate") {
    return json({
      groups: [
        { id: 1, workspace_id: 1, name: "WWE", account_ids: [2] },
        { id: 2, workspace_id: 1, name: "Marketing", account_ids: [] },
      ],
    });
  }

  if (path === "/api/v1/workspaces") {
    return json({ workspaces: demoWorkspaces });
  }

  if (path === "/api/v1/thumbnail-projects") {
    if (method === "GET") {
      return json({ items: demoThumbnailProjects });
    }
    if (method === "POST") {
      try {
        const body = JSON.parse((init.body as string) ?? "{}") as {
          workspace_id?: number;
          name?: string;
          description?: string;
          canvas_width?: number;
          canvas_height?: number;
        };
        const now = new Date().toISOString();
        const project: DemoThumbnailProject = {
          id: `thumbproj_demo_${nextThumbnailProjectVersion++}`,
          workspace_id: Number(body.workspace_id ?? 1),
          created_by: 1,
          name: String(body.name ?? "Untitled"),
          description: String(body.description ?? ""),
          canvas_width: Number(body.canvas_width ?? 1920),
          canvas_height: Number(body.canvas_height ?? 1080),
          status: "draft",
          current_revision_id: null,
          preview_media_id: null,
          latest_export_id: null,
          version: 1,
          created_at: now,
          updated_at: now,
        };
        demoThumbnailProjects.unshift(project);
        return json(project, 201);
      } catch {
        return json({ error: "demo: invalid project body" }, 400);
      }
    }
  }

  const thumbMatch = thumbnailProjectPathMatch(path);
  if (thumbMatch) {
    const project = demoThumbnailProjects.find((p) => p.id === thumbMatch.id);
    if (!project) return json({ error: "demo: project not found" }, 404);
    if (thumbMatch.rest === "/snapshot" && method === "PUT") {
      project.version += 1;
      project.current_revision_id = `thumbrev_demo_${project.version}`;
      project.updated_at = new Date().toISOString();
      return json({
        project_id: project.id,
        revision_id: project.current_revision_id,
        revision_number: project.version,
        version: project.version,
        saved_at: new Date().toISOString(),
        snapshot_sha256: "demo",
      });
    }
    if (thumbMatch.rest.startsWith("/revisions")) {
      return json({ items: [] });
    }
    if (thumbMatch.rest === "/assignments") {
      return json({ items: [] });
    }
    if (thumbMatch.rest === "/render" && method === "POST") {
      project.status = "ready";
      project.latest_export_id = `thumbexp_demo_${project.version}`;
      project.updated_at = new Date().toISOString();
      return json({
        id: project.latest_export_id,
        project_id: project.id,
        revision_id: project.current_revision_id,
        media_id: demoExportMediaId,
        content_type: "image/png",
        width: project.canvas_width,
        height: project.canvas_height,
        file_size: 0,
        sha256: "demo",
        renderer_version: "go-canvas-v1",
        status: "ready",
        last_error: "",
        created_at: new Date().toISOString(),
      });
    }
    if (thumbMatch.rest === "/media/resolve" && method === "POST") {
      try {
        const body = JSON.parse((init.body as string) ?? "{}") as { media_ids?: string[] };
        const ids = body.media_ids ?? [];
        return json({
          items: ids
            .filter((id) => id === demoExportMediaId)
            .map((id) => ({
              media_id: id,
              url: `https://placehold.co/${project.canvas_width}x${project.canvas_height}/30305a/ffffff?text=${encodeURIComponent(project.name)}`,
              content_type: "image/png",
              size_bytes: 0,
              created_at: new Date().toISOString(),
            })),
        });
      } catch {
        return json({ items: [] });
      }
    }
    return json(project);
  }

  // YouTube account private-video listing for the Link-to-video dialog.
  const contentMatch = /^\/api\/v1\/accounts\/(\d+)\/content/.exec(path);
  if (contentMatch && method === "GET") {
    return json({ items: demoPrivateVideos });
  }

  // Assign a ready export to a video (thumbnail-exports/{id}/assignments).
  const assignmentMatch = /^\/api\/v1\/thumbnail-exports\/([^/]+)\/assignments/.exec(path);
  if (assignmentMatch && method === "POST") {
    try {
      const body = JSON.parse((init.body as string) ?? "{}") as {
        targets?: Array<{ platform_account_id?: number; youtube_video_id?: string; target_language?: string | null }>;
      };
      const targets = body.targets ?? [];
      return json({
        items: targets.map((target, index) => ({
          id: `thumbass_demo_${Date.now()}_${index}`,
          workspace_id: 1,
          project_id: demoThumbnailProjects[0]?.id,
          export_id: assignmentMatch[1],
          platform_account_id: Number(target.platform_account_id ?? 2),
          platform: "youtube",
          youtube_video_id: target.youtube_video_id ?? "",
          target_language: target.target_language ?? null,
          status: "draft",
          created_at: new Date().toISOString(),
          updated_at: new Date().toISOString(),
        })),
      }, 201);
    } catch {
      return json({ error: "demo: invalid assignment body" }, 400);
    }
  }

  if (path === "/api/v1/thumbnail-exports") {
    return json({ items: [] });
  }

  if (path === "/api/v1/posts") {
    if (method === "GET") {
      return json({ posts: demoPosts });
    }
    if (method === "POST") {
      try {
        const body = JSON.parse((init.body as string) ?? "{}") as {
          workspace_id?: number;
          content?: { title?: string; caption?: string };
          scheduled_at?: string | null;
          status?: string;
        };
        const newPost = {
          id: nextPostId++,
          workspace_id: Number(body.workspace_id ?? 1),
          title: String(body.content?.title ?? "Untitled"),
          caption: String(body.content?.caption ?? ""),
          scheduled_at: body.scheduled_at ?? null,
          status: body.status === "queued" ? "queued" : "draft",
          created_at: new Date().toISOString(),
        };
        demoPosts.unshift(newPost);
        return json(newPost, 201);
      } catch {
        return json({ error: "demo: invalid post body" }, 400);
      }
    }
  }

  // Post action endpoints: /api/v1/posts/:id/{publish,cancel,retry,delete}
  const actionMatch = /^\/api\/v1\/posts\/(\d+)\/(publish|cancel|retry|delete)$/.exec(path);
  if (actionMatch) {
    const [, idStr, action] = actionMatch;
    const id = Number(idStr);
    const post = demoPosts.find((p) => p.id === id);
    if (!post) {
      return json({ error: "demo: post not found" }, 404);
    }
    if (action === "delete") {
      const idx = demoPosts.findIndex((p) => p.id === id);
      if (idx >= 0) demoPosts.splice(idx, 1);
    } else if (action === "publish") {
      post.status = "published";
      post.scheduled_at = null;
    } else if (action === "cancel") {
      post.status = "draft";
      post.scheduled_at = null;
    } else if (action === "retry") {
      post.status = "queued";
    }
    return json({ ok: true });
  }

  return notImplemented();
}
