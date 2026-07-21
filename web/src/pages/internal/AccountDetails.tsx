import { useCallback, useEffect, useRef, useState } from "react";
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
import { PROVIDERS, type ProviderId } from "../../lib/providers";
import { ErrorState } from "../../components/feedback";
import { cn } from "../../lib/utils";

type AccountMetric = {
  key: string;
  label: string;
  value: number;
  display_value: string;
};

type AccountResource = {
  resource_type: string;
  external_id: string;
  display_name: string;
  handle?: string;
  description?: string;
  avatar_url?: string;
  banner_url?: string;
  public_url?: string;
  metrics?: AccountMetric[];
  properties?: Record<string, unknown>;
  fetched_at?: string;
};

type AccountDetail = {
  id: number;
  platform: ProviderId;
  platform_user_id: string;
  username: string;
  status: string;
  created_at: string;
  resource?: AccountResource;
};

type ContentMetric = {
  key: string;
  label: string;
  value: number;
  display_value: string;
};

type ContentItem = {
  external_id: string;
  title?: string;
  description?: string;
  thumbnail_url?: string;
  public_url?: string;
  privacy?: string;
  status?: string;
  published_at?: string;
  duration?: string;
  metrics?: ContentMetric[];
  properties?: Record<string, unknown>;
};

type ContentPage = {
  items: ContentItem[];
  next_cursor?: string;
};

type TabId = "overview" | "videos" | "connection";

type FetchState =
  | { kind: "loading" }
  | { kind: "ready"; account: AccountDetail }
  | { kind: "error"; message: string };

type ContentState =
  | { kind: "idle" }
  | { kind: "loading" }
  | { kind: "ready"; page: ContentPage }
  | { kind: "error"; message: string };

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

function ContentVideoCard({ item }: { item: ContentItem }) {
  return (
    <a
      href={item.public_url}
      target="_blank"
      rel="noopener noreferrer"
      className="flex gap-4 p-3 rounded-xl bg-white/[0.03] border border-white/[0.06] hover:bg-white/[0.06] transition-colors no-underline group"
    >
      <div className="w-40 h-24 rounded-lg bg-white/[0.08] overflow-hidden shrink-0 relative">
        {item.thumbnail_url ? (
          <img
            src={item.thumbnail_url}
            alt={item.title ?? ""}
            className="w-full h-full object-cover"
          />
        ) : (
          <div className="w-full h-full flex items-center justify-center">
            <Video size={20} className="text-white/20" />
          </div>
        )}
        {item.duration && (
          <span className="absolute bottom-1 right-1 px-1.5 py-0.5 rounded bg-black/70 text-[10px] text-white font-medium">
            {formatDuration(item.duration)}
          </span>
        )}
      </div>
      <div className="flex flex-col justify-between min-w-0 flex-1 py-0.5">
        <div>
          <p className="text-[13px] font-semibold text-white truncate">
            {item.title}
          </p>
          <p className="text-[11px] text-[#9aa0aa] truncate mt-0.5">
            {item.external_id}
          </p>
        </div>
        <div className="flex items-center gap-3 text-[11px] text-[#9aa0aa]">
          {item.published_at && (
            <span>{new Date(item.published_at).toLocaleDateString()}</span>
          )}
          {item.privacy && (
            <span className="capitalize">{item.privacy}</span>
          )}
          {item.metrics?.map((m) => (
            <span key={m.key}>
              {m.display_value} {m.label.toLowerCase()}
            </span>
          ))}
        </div>
      </div>
      <ExternalLink
        size={14}
        className="text-white/0 group-hover:text-white/40 transition-colors shrink-0 mt-1"
      />
    </a>
  );
}

function formatDuration(iso: string): string {
  // Parse ISO 8601 duration (PT1H2M3S → 1:02:03)
  const match = iso.match(/PT(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?/);
  if (!match) return iso;
  const h = match[1] ? parseInt(match[1]) : 0;
  const m = match[2] ? parseInt(match[2]) : 0;
  const s = match[3] ? parseInt(match[3]) : 0;
  if (h > 0) return `${h}:${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
  return `${m}:${String(s).padStart(2, "0")}`;
}

export function AccountDetailsPage() {
  const { accountId } = useParams<{ accountId: string }>();
  const navigate = useNavigate();
  const abortRef = useRef<AbortController | null>(null);

  const [state, setState] = useState<FetchState>({ kind: "loading" });
  const [activeTab, setActiveTab] = useState<TabId>("overview");
  const [contentState, setContentState] = useState<ContentState>({ kind: "idle" });
  const [syncing, setSyncing] = useState(false);

  const loadAccount = useCallback(async () => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    setState({ kind: "loading" });

    try {
      const response = await authedFetch(`/api/v1/accounts/${accountId}`, {
        signal: controller.signal,
      });
      if (controller.signal.aborted) return;
      const data = (await response.json()) as AccountDetail;
      setState({ kind: "ready", account: data });
    } catch (err) {
      if (controller.signal.aborted) return;
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      const message = err instanceof Error ? err.message : "Unable to load account.";
      setState({ kind: "error", message });
    }
  }, [accountId, navigate]);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      // Session check is handled by ProtectedRoute; just load.
      if (!cancelled) void loadAccount();
    })();
    return () => {
      cancelled = true;
      abortRef.current?.abort();
    };
  }, [loadAccount]);

  const loadContent = useCallback(
    async (cursor?: string) => {
      setContentState({ kind: "loading" });
      try {
        const url = `/api/v1/accounts/${accountId}/content?limit=20${cursor ? `&cursor=${cursor}` : ""}`;
        const response = await authedFetch(url);
        const data = (await response.json()) as ContentPage;
        setContentState({ kind: "ready", page: data });
      } catch (err) {
        const message = err instanceof Error ? err.message : "Unable to load content.";
        setContentState({ kind: "error", message });
      }
    },
    [accountId],
  );

  useEffect(() => {
    if (activeTab === "videos" && contentState.kind === "idle") {
      void loadContent();
    }
  }, [activeTab, contentState, loadContent]);

  const handleSync = useCallback(async () => {
    setSyncing(true);
    try {
      await authedFetch(`/api/v1/accounts/${accountId}/sync`, { method: "POST" });
      await loadAccount();
    } catch {
      // sync errors are non-fatal; the stale data remains visible
    } finally {
      setSyncing(false);
    }
  }, [accountId, loadAccount]);

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
                {contentState.page.items.length === 0 ? (
                  <div className="text-center py-12 text-[13px] text-[#9aa0aa]">
                    No videos found.
                  </div>
                ) : (
                  <>
                    {contentState.page.items.map((item) => (
                      <ContentVideoCard key={item.external_id} item={item} />
                    ))}
                    {contentState.page.next_cursor && (
                      <button
                        type="button"
                        onClick={() =>
                          void loadContent(contentState.page.next_cursor)
                        }
                        className="w-full py-3 text-[13px] font-semibold text-[#9aa0aa] hover:text-white transition-colors"
                      >
                        Load more
                      </button>
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
