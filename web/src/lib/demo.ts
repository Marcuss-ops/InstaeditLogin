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
};

export const demoAccounts = [
  {
    id: 1,
    platform: "instagram" as const,
    username: "instaedit_demo",
    created_at: new Date(Date.now() - 7 * 24 * 60 * 60 * 1000).toISOString(),
  },
];

export const demoWorkspaces = [
  {
    id: 1,
    name: "Personal",
    created_at: new Date(Date.now() - 30 * 24 * 60 * 60 * 1000).toISOString(),
  },
];

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

  if (path === "/api/v1/workspaces") {
    return json({ workspaces: demoWorkspaces });
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
