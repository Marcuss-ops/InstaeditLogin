import { useCallback, useEffect, useState } from "react";
import { useParams, useNavigate, Link } from "react-router-dom";
import {
  ArrowLeft,
  RefreshCw,
  ExternalLink,
  Video,
  Settings,
  AlertCircle,
  Loader2,
  TrendingUp,
} from "lucide-react";
import { authedFetch, AuthError } from "../../lib/auth";
import { useToast } from "../../components/toast";
import { PROVIDERS } from "../../lib/providers";
import { ErrorState } from "../../components/feedback";
import { cn } from "../../lib/utils";
import { createEditorSessionAndOpen } from "../../features/youtube/api/editorSessionsApi";
import { useGroupVideosLiveUpdate } from "../../features/channels/hooks/useYouTubePublishLiveUpdate";
import {
  AccountDetailsVideoCard,
  type ContentItem,
} from "./AccountDetailsVideoCard";
import {
  useAccountDetailsData,
  type AccountMetric,
} from "./useAccountDetailsData";
import { useAccountContentData } from "./useAccountContentData";

type TabId = "overview" | "videos" | "connection";

function MetricCard({ metric }: { metric: AccountMetric }) {
  return (
    <div className="flex flex-col items-center p-4 rounded-xl bg-white/[0.04] border border-white/[0.08]">
      <span className="text-[24px] font-extrabold text-white leading-tight">
        {metric.display_value}
      </span>
      <span className="text-[12px] text-[#9aa0aa] mt-1">{metric.label}</span>
    </div>
  );
}

export function AccountDetailsPage() {
  const { accountId } = useParams<{ accountId: string }>();
  const navigate = useNavigate();
  const toast = useToast();
  const [activeTab, setActiveTab] = useState<TabId>("overview");
  const { state, loadAccount, syncing, handleSync } =
    useAccountDetailsData(accountId);
  const currentPlatform = state.kind === "ready" ? state.account.platform : undefined;
  const { contentState, loadContent, contentCacheBust } =
    useAccountContentData(accountId, currentPlatform);

  const handleEditThumbnail = useCallback(async (item: ContentItem) => {
    if (!accountId) return;
    try {
      // Workspace lookup stays inline — there's no shared workspaces
      // hook today. The error UX (no workspaces → toast + abort) is
      // the page-level concern, not the editor-sessions client's.
      const wsResp = await authedFetch("/api/v1/workspaces");
      const { workspaces } = (await wsResp.json()) as { workspaces: { id: number }[] };
      if (!workspaces.length) {
        toast.error("No workspaces found. Create one first.");
        return;
      }
      // createEditorSessionAndOpen bundles the POST and popup-window
      // handling while preserving the full editor-session response
      // contract for AccountDetails and Calendar.
      await createEditorSessionAndOpen({
        workspace_id: workspaces[0].id,
        platform_account_id: Number(accountId),
        youtube_video_id: item.external_id,
      });
      toast.success("Editor session created — opening Velox…");
    } catch (err) {
      if (err instanceof AuthError) return;
      // authedFetch already toasts on non-OK responses
    }
  }, [accountId, toast]);

  useEffect(() => {
    if (activeTab === "videos" && contentState.kind === "idle") {
      void loadContent();
    }
  }, [activeTab, contentState, loadContent]);

  // Cross-tab invalidation for the legacy "Videos" tab (group-
  // videos cache). NUMERIC id only — bogus URL values short-
  // circuit the registration. Only fires when the Videos tab is
  // currently ACTIVE so the refetch doesn't accidentally wipe a
  // tab the user isn't looking at. Placed AFTER loadContent so
  // the useCallback can reference it (loadContent is declared up
  // above in this same component function).
  const accountIdNum = accountId ? Number(accountId) : NaN;
  const accountIdNumeric =
    Number.isFinite(accountIdNum) && accountIdNum > 0 ? accountIdNum : null;
  const handleGroupPublishChanged = useCallback(() => {
    if (activeTab === "videos") {
      void loadContent();
    }
  }, [activeTab, loadContent]);
  useGroupVideosLiveUpdate(accountIdNumeric, handleGroupPublishChanged);

  if (state.kind === "loading") {
    return (
      <div className="min-h-full p-8 bg-[#030308] text-[#e8e8ef]">
        <div className="max-w-5xl mx-auto">
          <div className="h-8 w-32 rounded-lg bg-white/[0.06] animate-pulse mb-8" />
          <div className="h-48 rounded-2xl bg-white/[0.06] animate-pulse" />
        </div>
      </div>
    );
  }

  if (state.kind === "error") {
    return (
      <div className="min-h-full p-8 bg-[#030308] text-[#e8e8ef]">
        <div className="max-w-5xl mx-auto">
          <ErrorState
            title="Couldn't load account"
            message={state.message}
            onRetry={() => void loadAccount()}
            className="bg-[#1f1f2e] border-white/[0.12]"
          />
        </div>
      </div>
    );
  }

  const { account } = state;
  const provider = getProviderMeta(account.platform);
  const resource = account.resource;

  const tabs: { id: TabId; label: string; icon: React.ReactNode }[] = [
    { id: "overview", label: "Overview", icon: <Settings size={14} /> },
    { id: "videos", label: "Videos", icon: <Video size={14} /> },
    { id: "connection", label: "Connection", icon: <AlertCircle size={14} /> },
  ];

  return (
    <div className="min-h-full p-8 bg-[#030308] text-[#e8e8ef]">
      <div className="max-w-5xl mx-auto">
        {/* Back button */}
        <button
          type="button"
          onClick={() => navigate("/app/linking")}
          className="inline-flex items-center gap-1.5 text-[13px] text-[#9aa0aa] hover:text-white transition-colors mb-6"
        >
          <ArrowLeft size={14} /> Back to linking
        </button>

        {/* Header card */}
        <div className="rounded-2xl bg-[#1f1f2e] border border-white/[0.12] overflow-hidden mb-6">
          {resource?.banner_url && (
            <div className="h-32 w-full bg-white/[0.04]">
              <img
                src={resource.banner_url}
                alt=""
                className="w-full h-full object-cover"
              />
            </div>
          )}
          <div className="p-6">
            <div className="flex items-start gap-4">
              {resource?.avatar_url ? (
                <img
                  src={resource.avatar_url}
                  alt=""
                  className="w-16 h-16 rounded-full border-2 border-white/10"
                />
              ) : (
                <div
                  className={cn(
                    "w-16 h-16 rounded-full bg-gradient-to-br flex items-center justify-center text-white text-xl font-bold shrink-0",
                    provider?.color ?? "from-white/20 to-white/10",
                  )}
                >
                  {account.username?.charAt(0).toUpperCase() ?? "?"}
                </div>
              )}
              <div className="flex-1 min-w-0">
                <div className="flex items-center gap-3 flex-wrap">
                  <h1 className="text-[22px] font-extrabold text-white leading-tight">
                    {resource?.display_name ?? account.username}
                  </h1>
                  <span
                    className={cn(
                      "inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-semibold border",
                      account.status === "active"
                        ? "bg-emerald-500/[0.08] border-emerald-500/[0.15] text-emerald-400"
                        : "bg-amber-500/[0.08] border-amber-500/[0.15] text-amber-400",
                    )}
                  >
                    {account.status.toUpperCase()}
                  </span>
                </div>
                {resource?.handle && (
                  <p className="text-[14px] text-[#9aa0aa] mt-0.5">
                    {resource.handle}
                  </p>
                )}
                {resource?.public_url && (
                  <a
                    href={resource.public_url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex items-center gap-1 text-[13px] text-blue-400 hover:text-blue-300 mt-2 no-underline"
                  >
                    Open on {provider?.name ?? account.platform}{" "}
                    <ExternalLink size={12} />
                  </a>
                )}
              </div>
              <Link
                to={`/app/accounts/${accountId}/performance`}
                className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-xl bg-white/[0.06] border border-white/[0.08] text-[12px] font-semibold text-[#9aa0aa] hover:bg-white/[0.10] hover:text-white transition-colors no-underline"
              >
                <TrendingUp size={12} /> Performance
              </Link>
              <button
                type="button"
                onClick={() => void handleSync()}
                disabled={syncing}
                className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-xl bg-white/[0.06] border border-white/[0.08] text-[12px] font-semibold text-[#9aa0aa] hover:bg-white/[0.10] hover:text-white transition-colors disabled:opacity-50"
              >
                {syncing ? (
                  <Loader2 size={12} className="animate-spin" />
                ) : (
                  <RefreshCw size={12} />
                )}
                Sync
              </button>
            </div>

            {/* Metrics row */}
            {resource?.metrics && resource.metrics.length > 0 && (
              <div className="grid grid-cols-3 gap-3 mt-6">
                {resource.metrics.map((m) => (
                  <MetricCard key={m.key} metric={m} />
                ))}
              </div>
            )}
          </div>
        </div>

        {account.status === "reauth_required" && account.platform === "youtube" && (
          <div className="mb-6 flex items-center justify-between gap-4 rounded-2xl border border-amber-400/20 bg-amber-400/[0.08] px-5 py-4">
            <div>
              <p className="text-[13px] font-semibold text-amber-200">Autorizzazione YouTube richiesta</p>
              <p className="mt-1 text-[12px] text-amber-100/70">
                Il collegamento Google non è più valido. Ricollega YouTube per riattivare le pubblicazioni.
              </p>
            </div>
            <a
              href={`/api/v1/auth/${account.platform}/login?mode=reconnect`}
              className="inline-flex shrink-0 items-center gap-1.5 rounded-xl bg-amber-300 px-4 py-2 text-[13px] font-semibold text-black transition-colors hover:bg-amber-200"
            >
              <RefreshCw size={14} /> Ricollega YouTube
            </a>
          </div>
        )}

        {/* Tabs */}
        <div className="flex items-center gap-1 mb-4 border-b border-white/[0.08]">
          {tabs.map((tab) => (
            <button
              key={tab.id}
              type="button"
              onClick={() => setActiveTab(tab.id)}
              className={cn(
                "inline-flex items-center gap-1.5 px-4 py-2.5 text-[13px] font-semibold border-b-2 transition-colors -mb-px",
                activeTab === tab.id
                  ? "border-white text-white"
                  : "border-transparent text-[#9aa0aa] hover:text-white",
              )}
            >
              {tab.icon} {tab.label}
            </button>
          ))}
        </div>

        {/* Tab content */}
        {activeTab === "overview" && (
          <div className="rounded-2xl bg-[#1f1f2e] border border-white/[0.12] p-6">
            <h2 className="text-[15px] font-bold text-white mb-4">Channel Details</h2>
            <dl className="grid grid-cols-2 gap-x-8 gap-y-3 text-[13px]">
              {resource?.display_name && (
                <>
                  <dt className="text-[#9aa0aa]">Name</dt>
                  <dd className="text-white">{resource.display_name}</dd>
                </>
              )}
              {resource?.handle && (
                <>
                  <dt className="text-[#9aa0aa]">Handle</dt>
                  <dd className="text-white">{resource.handle}</dd>
                </>
              )}
              {resource?.description && (
                <>
                  <dt className="text-[#9aa0aa]">Description</dt>
                  <dd className="text-white line-clamp-3">{resource.description}</dd>
                </>
              )}
              {resource?.properties?.country != null && (
                <>
                  <dt className="text-[#9aa0aa]">Country</dt>
                  <dd className="text-white">{String(resource.properties["country"])}</dd>
                </>
              )}
              {resource?.properties?.uploads_playlist_id != null && (
                <>
                  <dt className="text-[#9aa0aa]">Uploads Playlist</dt>
                  <dd className="text-white font-mono text-[11px]">
                    {String(resource.properties["uploads_playlist_id"])}
                  </dd>
                </>
              )}
              <>
                <dt className="text-[#9aa0aa]">Platform User ID</dt>
                <dd className="text-white font-mono text-[11px]">
                  {account.platform_user_id}
                </dd>
              </>
              {resource?.fetched_at && (
                <>
                  <dt className="text-[#9aa0aa]">Last Synced</dt>
                  <dd className="text-white">
                    {new Date(resource.fetched_at).toLocaleString()}
                  </dd>
                </>
              )}
            </dl>
          </div>
        )}

        {activeTab === "videos" && (
          <div className="space-y-3">
            {contentState.kind === "loading" && (
              <div className="space-y-3">
                {Array.from({ length: 5 }).map((_, i) => (
                  <div key={i} className="h-24 rounded-xl bg-white/[0.06] animate-pulse" />
                ))}
              </div>
            )}
            {contentState.kind === "error" && (
              <ErrorState
                title="Couldn't load videos"
                message={contentState.message}
                onRetry={() => void loadContent()}
                className="bg-[#1f1f2e] border-white/[0.12]"
              />
            )}
            {contentState.kind === "ready" && (
              <>
                {contentState.items.length === 0 ? (
                  <div className="text-center py-12 text-[13px] text-[#9aa0aa]">
                    No videos found.
                  </div>
                ) : (
                  <>
                    {contentState.items.map((item) => (
                      <AccountDetailsVideoCard
                        key={item.external_id}
                        item={item}
                        onEditThumbnail={handleEditThumbnail}
                        cacheBust={contentCacheBust}
                      />
                    ))}
                    {contentState.nextCursor && !contentState.isLoadingMore && !contentState.loadMoreError && (
                      <button
                        type="button"
                        onClick={() =>
                          void loadContent(contentState.nextCursor)
                        }
                        className="w-full py-3 text-[13px] font-semibold text-[#9aa0aa] hover:text-white transition-colors"
                      >
                        Load more
                      </button>
                    )}
                    {contentState.isLoadingMore && (
                      <div className="flex items-center justify-center gap-2 py-3 text-[13px] text-[#9aa0aa]">
                        <Loader2 size={14} className="animate-spin" />
                        Loading more…
                      </div>
                    )}
                    {contentState.loadMoreError && contentState.nextCursor && (
                      <div className="flex flex-col items-center gap-2 py-3">
                        <p className="text-[13px] text-red-400">
                          {contentState.loadMoreError}
                        </p>
                        <button
                          type="button"
                          onClick={() => void loadContent(contentState.nextCursor)}
                          className="text-[13px] font-semibold text-[#9aa0aa] hover:text-white transition-colors"
                        >
                          Try again
                        </button>
                      </div>
                    )}
                  </>
                )}
              </>
            )}
          </div>
        )}

        {activeTab === "connection" && (
          <div className="rounded-2xl bg-[#1f1f2e] border border-white/[0.12] p-6">
            <h2 className="text-[15px] font-bold text-white mb-4">Connection</h2>
            <dl className="grid grid-cols-2 gap-x-8 gap-y-3 text-[13px]">
              <dt className="text-[#9aa0aa]">Status</dt>
              <dd className="text-white capitalize">{account.status.replace("_", " ")}</dd>
              <dt className="text-[#9aa0aa]">Platform</dt>
              <dd className="text-white">{provider?.name ?? account.platform}</dd>
              <dt className="text-[#9aa0aa]">Connected</dt>
              <dd className="text-white">
                {new Date(account.created_at).toLocaleString()}
              </dd>
              <dt className="text-[#9aa0aa]">Platform User ID</dt>
              <dd className="text-white font-mono text-[11px]">
                {account.platform_user_id}
              </dd>
            </dl>
            <div className="flex items-center gap-3 mt-6">
              <a
                href={`/api/v1/auth/${account.platform}/login?mode=reconnect`}
                className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white text-black text-[13px] font-semibold hover:bg-white/90 transition-colors no-underline"
              >
                <RefreshCw size={14} /> Reconnect
              </a>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function getProviderMeta(id: string) {
  return PROVIDERS.find((p) => p.id === id);
}
