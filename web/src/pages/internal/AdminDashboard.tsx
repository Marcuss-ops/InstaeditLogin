import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { RefreshCw, Shield } from "lucide-react";
import { authedFetch, AuthError } from "../../lib/auth";
import { ErrorState } from "../../components/feedback";
import { cn } from "../../lib/utils";
import { FleetReadinessSection } from "./AdminDashboardFleetReadiness";
import { AdminHealthSection } from "./AdminDashboardHealth";
import { PoolCapacityView } from "./AdminDashboardPoolCapacity";
import type {
  AdminHealth,
  FetchState,
  FleetReadinessData,
  YouTubeOAuthPoolCapacityResponse,
} from "./adminDashboardTypes";

export function AdminDashboardPage() {
  const navigate = useNavigate();
  const [fleet, setFleet] = useState<FetchState<FleetReadinessData>>({
    kind: "loading",
  });
  const [health, setHealth] = useState<FetchState<AdminHealth>>({
    kind: "loading",
  });
  const [pool, setPool] = useState<FetchState<YouTubeOAuthPoolCapacityResponse>>({
    kind: "loading",
  });

  const loadFleet = useCallback(async () => {
    setFleet({ kind: "loading" });
    try {
      const response = await authedFetch("/admin/youtube/fleet_readiness");
      const data = (await response.json()) as FleetReadinessData;
      setFleet({ kind: "ready", data });
    } catch (err) {
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      const message =
        err instanceof Error ? err.message : "Unable to load fleet readiness.";
      setFleet({ kind: "error", message });
    }
  }, [navigate]);

  const loadHealth = useCallback(async () => {
    setHealth({ kind: "loading" });
    try {
      const response = await authedFetch("/admin/health");
      const data = (await response.json()) as AdminHealth;
      setHealth({ kind: "ready", data });
    } catch (err) {
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      const message =
        err instanceof Error ? err.message : "Unable to load health.";
      setHealth({ kind: "error", message });
    }
  }, [navigate]);

  const loadPool = useCallback(async () => {
    setPool({ kind: "loading" });
    try {
      const response = await authedFetch("/admin/youtube/oauth_pool_capacity");
      const data = (await response.json()) as YouTubeOAuthPoolCapacityResponse;
      setPool({ kind: "ready", data });
    } catch (err) {
      if (err instanceof AuthError) {
        navigate("/login", { replace: true });
        return;
      }
      const message =
        err instanceof Error ? err.message : "Unable to load OAuth pool capacity.";
      setPool({ kind: "error", message });
    }
  }, [navigate]);

  const loadAll = useCallback(() => {
    void loadFleet();
    void loadHealth();
    void loadPool();
  }, [loadFleet, loadHealth, loadPool]);

  useEffect(() => {
    loadAll();
  }, [loadAll]);

  const isLoading =
    fleet.kind === "loading" || health.kind === "loading" || pool.kind === "loading";

  return (
    <div className="min-h-full p-8 bg-[#030308] text-[#e8e8ef]">
      <div className="max-w-7xl mx-auto">
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-8">
          <div>
            <h1 className="text-[28px] font-extrabold tracking-[-0.02em] text-white flex items-center gap-3">
              <Shield size={28} className="text-white/40" />
              Admin Dashboard
            </h1>
            <p className="text-[15px] text-[#9aa0aa] mt-1">
              Fleet readiness, OAuth pool capacity and platform health.
            </p>
          </div>
          <button
            type="button"
            onClick={() => loadAll()}
            disabled={isLoading}
            className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors disabled:opacity-50"
          >
            <RefreshCw size={14} className={cn(isLoading && "animate-spin")} /> Refresh
          </button>
        </div>

        {fleet.kind === "error" && (
          <ErrorState
            title="Couldn't load fleet readiness"
            message={fleet.message}
            onRetry={() => loadFleet()}
            className="bg-[#1f1f2e] border-white/[0.12] mb-8"
          />
        )}

        {fleet.kind === "ready" && <FleetReadinessSection data={fleet.data} />}

        {pool.kind === "error" && (
          <ErrorState
            title="Couldn't load OAuth pool capacity"
            message={pool.message}
            onRetry={() => loadPool()}
            className="bg-[#1f1f2e] border-white/[0.12] mb-8"
          />
        )}

        {pool.kind === "ready" && <PoolCapacityView data={pool.data} />}

        {health.kind === "error" && (
          <ErrorState
            title="Couldn't load health"
            message={health.message}
            onRetry={() => loadHealth()}
            className="bg-[#1f1f2e] border-white/[0.12] mb-8"
          />
        )}

        {health.kind === "ready" && <AdminHealthSection data={health.data} />}
      </div>
    </div>
  );
}
