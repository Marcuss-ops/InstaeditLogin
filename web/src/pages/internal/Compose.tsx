import { useCallback, useEffect, useRef, useState, type ChangeEvent, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { PenSquare, Send, Save, AlertCircle, ArrowLeft } from "lucide-react";
import { authedFetch, AuthError, fetchSession } from "../../lib/auth";
import { Skeleton, ErrorState } from "../../components/feedback";
import { getProvider } from "../../lib/providers";
import { cn } from "../../lib/utils";

type Workspace = {
  id: number;
  name: string;
};

type PlatformAccount = {
  id: number;
  platform: string;
  username: string;
};

type FetchState =
  | { kind: "loading" }
  | { kind: "ready"; workspaces: Workspace[]; accounts: PlatformAccount[] }
  | { kind: "error"; message: string };

export function InternalCompose() {
  const navigate = useNavigate();
  const [state, setState] = useState<FetchState>({ kind: "loading" });
  const [isSubmitting, setIsSubmitting] = useState(false);
  const abortRef = useRef<AbortController | null>(null);

  const [workspaceId, setWorkspaceId] = useState<number | "">("");
  const [title, setTitle] = useState("");
  const [caption, setCaption] = useState("");
  const [scheduledAt, setScheduledAt] = useState("");
  const [status, setStatus] = useState<"draft" | "queued" | "publish">("draft");
  const [selectedAccounts, setSelectedAccounts] = useState<Set<number>>(new Set());

  const [mediaAssetId, setMediaAssetId] = useState<string | null>(null);
  const [uploading, setUploading] = useState(false);
  const [uploadError, setUploadError] = useState<string | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  const loadData = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    setState({ kind: "loading" });

    try {
      const [wsResp, accResp] = await Promise.all([
        authedFetch("/api/v1/workspaces", { signal: controller.signal }),
        authedFetch("/api/v1/accounts", { signal: controller.signal }),
      ]);
      if (controller.signal.aborted) return;

      const wsData = (await wsResp.json()) as { workspaces: Workspace[] };
      const accData = (await accResp.json()) as { accounts: PlatformAccount[] };
      const workspaces = wsData.workspaces ?? [];
      const accounts = accData.accounts ?? [];

      setState({ kind: "ready", workspaces, accounts });
      if (workspaces.length === 1) {
        setWorkspaceId(workspaces[0].id);
      }
      if (accounts.length === 1) {
        setSelectedAccounts(new Set([accounts[0].id]));
      }
    } catch (err) {
      if (controller.signal.aborted) return;
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      const message = err instanceof Error ? err.message : "Unable to load compose dependencies.";
      setState({ kind: "error", message });
    }
  }, [navigate]);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const session = await fetchSession();
      if (cancelled) return;
      if (!session) {
        navigate("/login", { replace: true });
        return;
      }
      void loadData();
    })();
    return () => {
      cancelled = true;
      abortRef.current?.abort();
    };
  }, [loadData, navigate]);

  const toggleAccount = (id: number) => {
    setSelectedAccounts((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const handleFileChange = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    setUploadError(null);
    setUploading(true);
    try {
      const presign = await authedFetch("/api/v1/media/presign", {
        method: "POST",
        body: JSON.stringify({
          filename: file.name,
          content_type: file.type || "video/mp4",
          size_bytes: file.size,
        }),
      });
      if (!presign.ok) throw new Error("presign failed");
      const grant = (await presign.json()) as {
        asset_id: string;
        upload_url: string;
        upload_headers: Record<string, string>;
      };
      const putRes = await fetch(grant.upload_url, {
        method: "PUT",
        headers: { "Content-Type": file.type || "video/mp4" },
        body: file,
      });
      if (!putRes.ok) throw new Error("upload failed");
      const complete = await authedFetch(`/api/v1/media/${grant.asset_id}/complete`, {
        method: "POST",
      });
      if (!complete.ok) throw new Error("upload verification failed");
      setMediaAssetId(grant.asset_id);
    } catch (err) {
      setUploadError(err instanceof Error ? err.message : "Upload failed");
      setMediaAssetId(null);
    } finally {
      setUploading(false);
      if (fileInputRef.current) fileInputRef.current.value = "";
    }
  };

  const clearMedia = () => {
    setMediaAssetId(null);
    setUploadError(null);
  };

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (workspaceId === "" || selectedAccounts.size === 0 || !title.trim()) return;

    setIsSubmitting(true);
    try {
      const isPublishNow = status === "publish";
      const payload = {
        workspace_id: Number(workspaceId),
        content: { title: title.trim(), caption: caption.trim() || undefined, media: mediaAssetId ? [{ asset_id: mediaAssetId }] : undefined },
        status: isPublishNow ? "draft" : status,
        scheduled_at: isPublishNow
          ? undefined
          : scheduledAt
            ? new Date(scheduledAt).toISOString()
            : undefined,
        targets: Array.from(selectedAccounts).map((id) => ({ platform_account_id: id })),
      };

      const res = await authedFetch("/api/v1/posts", {
        method: "POST",
        body: JSON.stringify(payload),
      });

      if (isPublishNow && res.ok) {
        try {
          const created = (await res.json()) as { id: number };
          await authedFetch(`/api/v1/posts/${created.id}/publish`, {
            method: "POST",
          });
        } catch {
          // If the immediate publish trigger fails, the post stays as a
          // draft and can be published later from the posts list.
        }
      }

      navigate("/app/posts");
    } catch {
      // errors are toasted by authedFetch
      setIsSubmitting(false);
    }
  };

  if (state.kind === "loading") {
    return (
      <div className="min-h-full p-8">
        <div className="max-w-3xl mx-auto grid gap-6">
          <Skeleton variant="card" height={80} />
          <Skeleton variant="card" height={400} />
        </div>
      </div>
    );
  }

  if (state.kind === "error") {
    return (
      <div className="min-h-full p-8">
        <div className="max-w-3xl mx-auto">
          <ErrorState title="Couldn't load compose" message={state.message} onRetry={() => void loadData()} />
        </div>
      </div>
    );
  }

  const { workspaces, accounts } = state;
  const isFormValid = workspaceId !== "" && title.trim() !== "" && selectedAccounts.size > 0;

  return (
    <div className="min-h-full p-8 bg-[#030308] text-[#e8e8ef]">
      <div className="max-w-3xl mx-auto">
        <Link
          to="/app/posts"
          className="inline-flex items-center gap-1.5 text-[13px] font-medium text-[#9aa0aa] hover:text-white transition-colors no-underline mb-4"
        >
          <ArrowLeft size={14} /> Back to posts
        </Link>

        <div className="mb-8">
          <h1 className="text-[28px] font-extrabold tracking-[-0.02em] text-white flex items-center gap-3">
            <PenSquare size={28} className="text-white/40" />
            Compose
          </h1>
          <p className="text-[15px] text-[#9aa0aa] mt-1">
            Create and schedule a post across your connected accounts.
          </p>
        </div>

        <form onSubmit={handleSubmit} className="space-y-6">
          <div className="bg-[#1f1f2e] border border-white/[0.12] rounded-2xl p-6 shadow-sm space-y-6">
            <div className="space-y-4">
              <div>
                <label htmlFor="workspace" className="block text-[13px] font-semibold text-[#9aa0aa] mb-1.5">
                  Workspace
                </label>
                <select
                  id="workspace"
                  value={workspaceId}
                  onChange={(e) => setWorkspaceId(e.target.value === "" ? "" : Number(e.target.value))}
                  className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all"
                >
                  <option value="" disabled className="bg-[#1f1f2e]">
                    Select a workspace...
                  </option>
                  {workspaces.map((ws) => (
                    <option key={ws.id} value={ws.id} className="bg-[#1f1f2e]">
                      {ws.name}
                    </option>
                  ))}
                </select>
              </div>

              <div>
                <span className="block text-[13px] font-semibold text-[#9aa0aa] mb-1.5">Target accounts</span>
                {accounts.length === 0 ? (
                  <div className="text-[13px] text-red-400 flex items-center gap-2">
                    <AlertCircle size={14} /> You have no linked accounts. Please link one first.
                  </div>
                ) : (
                  <div className="flex flex-wrap gap-2">
                    {accounts.map((acc) => {
                      const selected = selectedAccounts.has(acc.id);
                      const provider = getProvider(acc.platform);
                      return (
                        <button
                          key={acc.id}
                          type="button"
                          onClick={() => toggleAccount(acc.id)}
                          className={cn(
                            "px-3 py-1.5 rounded-lg border text-[13px] font-medium transition-colors",
                            selected
                              ? "bg-white text-black border-white"
                              : "bg-white/[0.04] text-[#e8e8ef] border-white/[0.08] hover:border-white/[0.20]",
                          )}
                        >
                          {provider?.name ?? acc.platform} (@{acc.username})
                        </button>
                      );
                    })}
                  </div>
                )}
              </div>
            </div>

            <hr className="border-white/[0.08]" />

            <div className="space-y-4">
              <div>
                <label htmlFor="title" className="block text-[13px] font-semibold text-[#9aa0aa] mb-1.5">
                  Title
                </label>
                <input
                  id="title"
                  type="text"
                  placeholder="Internal title for your reference"
                  value={title}
                  onChange={(e) => setTitle(e.target.value)}
                  className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white placeholder:text-white/20 focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all"
                />
              </div>

              <div>
                <label htmlFor="caption" className="block text-[13px] font-semibold text-[#9aa0aa] mb-1.5">
                  Caption
                </label>
                <textarea
                  id="caption"
                  rows={5}
                  placeholder="What's on your mind?"
                  value={caption}
                  onChange={(e) => setCaption(e.target.value)}
                  className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white placeholder:text-white/20 focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all resize-y"
                />
              </div>

              <div>
                <label htmlFor="media" className="block text-[13px] font-semibold text-[#9aa0aa] mb-1.5">
                  Video
                </label>
                <input
                  id="media"
                  ref={fileInputRef}
                  type="file"
                  accept="video/mp4,video/quicktime"
                  onChange={handleFileChange}
                  disabled={uploading}
                  className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white file:mr-3 file:px-3 file:py-1 file:rounded-lg file:border-0 file:bg-white/[0.10] file:text-white file:cursor-pointer disabled:opacity-50"
                />
                {uploading && (
                  <p className="mt-1.5 text-[13px] text-[#9aa0aa]">Uploading video…</p>
                )}
                {uploadError && (
                  <p className="mt-1.5 text-[13px] text-red-400">{uploadError}</p>
                )}
                {mediaAssetId && !uploading && (
                  <div className="mt-1.5 flex items-center gap-2 text-[13px] text-emerald-400">
                    <span>Video ready</span>
                    <button
                      type="button"
                      onClick={clearMedia}
                      className="text-[#9aa0aa] hover:text-white underline"
                    >
                      Remove
                    </button>
                  </div>
                )}
              </div>
            </div>

            <hr className="border-white/[0.08]" />

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
              <div>
                <label htmlFor="scheduledAt" className="block text-[13px] font-semibold text-[#9aa0aa] mb-1.5">
                  Schedule (optional)
                </label>
                <input
                  id="scheduledAt"
                  type="datetime-local"
                  value={scheduledAt}
                  disabled={status === "publish"}
                  onChange={(e) => {
                    setScheduledAt(e.target.value);
                    if (e.target.value) setStatus("queued");
                    else setStatus("draft");
                  }}
                  className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all disabled:opacity-50"
                />
              </div>

              <div>
                <label htmlFor="status" className="block text-[13px] font-semibold text-[#9aa0aa] mb-1.5">
                  Initial status
                </label>
                <select
                  id="status"
                  value={status}
                  onChange={(e) => {
                    const next = e.target.value as "draft" | "queued" | "publish";
                    setStatus(next);
                    if (next === "publish") setScheduledAt("");
                  }}
                  className="w-full px-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[14px] text-white focus:outline-none focus:border-white/[0.20] focus:ring-1 focus:ring-white/10 transition-all"
                >
                  <option value="draft" className="bg-[#1f1f2e]">Save as draft</option>
                  <option value="queued" className="bg-[#1f1f2e]">Queue for scheduling</option>
                  <option value="publish" className="bg-[#1f1f2e]">Publish now</option>
                </select>
              </div>
            </div>
          </div>

          <div className="flex items-center justify-end gap-3">
            <Link
              to="/app/posts"
              className="px-4 py-2 text-[14px] font-medium text-[#9aa0aa] hover:text-white transition-colors no-underline"
            >
              Cancel
            </Link>
            <button
              type="submit"
              disabled={!isFormValid || isSubmitting}
              className="inline-flex items-center gap-2 px-5 py-2.5 rounded-xl bg-white text-black text-[14px] font-semibold hover:bg-white/90 transition-all disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {status === "publish" ? <Send size={16} /> : status === "draft" ? <Save size={16} /> : <Send size={16} />}
              {status === "publish" ? "Publish now" : status === "draft" ? "Save draft" : "Schedule post"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
