import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { authedFetch, AuthError } from "../../lib/auth";
import type { ProviderId } from "../../lib/providers";

export type AccountMetric = {
  key: string;
  label: string;
  value: number;
  display_value: string;
};

export type AccountResource = {
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

export type AccountDetail = {
  id: number;
  platform: ProviderId;
  platform_user_id: string;
  username: string;
  status: string;
  account_state?: "valid" | "reconnect_required" | "suspended" | "deleted";
  is_publishable?: boolean;
  reauth_required_at?: string;
  last_error_code?: string;
  created_at: string;
  resource?: AccountResource;
};

export type AccountFetchState =
  | { kind: "loading" }
  | { kind: "ready"; account: AccountDetail }
  | { kind: "error"; message: string };

export function useAccountDetailsData(accountId: string | undefined) {
  const navigate = useNavigate();
  const abortRef = useRef<AbortController | null>(null);
  const [state, setState] = useState<AccountFetchState>({ kind: "loading" });
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

  return { state, loadAccount, syncing, handleSync };
}
