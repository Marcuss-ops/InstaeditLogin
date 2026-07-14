import { useCallback, useEffect, useState } from "react";
import {
  Check,
  Copy,
  Key,
  Plus,
  RefreshCw,
  Trash2,
  Webhook,
  Workflow,
  X,
} from "lucide-react";
import { Nav } from "../components/Nav";
import { EmptyState, ErrorState, Skeleton } from "../components/feedback";
import { ApiError, authedFetch } from "../lib/auth";
import { cn } from "../lib/utils";

// ────────────────────────────────────────────────────────────────────────────
//  Types
// ────────────────────────────────────────────────────────────────────────────

type Workspace = { id: number; name: string; owner_id: number; created_at: string };

type ApiKey = {
  id: number;
  workspace_id: number;
  created_by: number;
  name: string;
  environment: string;
  key_prefix: string;
  permissions: string[];
  expires_at?: string | null;
  revoked_at?: string | null;
  last_used_at?: string | null;
  created_at: string;
  updated_at: string;
};

type WebhookEndpoint = {
  id: number;
  workspace_id: number;
  url: string;
  events: string[];
  status: string; // "active" | "disabled"
  created_at: string;
};

type Tab = "workspaces" | "api-keys" | "webhooks";

type Toast = { kind: "ok" | "err"; message: string } | null;

// ────────────────────────────────────────────────────────────────────────────
//  Helpers
// ────────────────────────────────────────────────────────────────────────────

const PERMISSION_OPTIONS = [
  { value: "read", label: "Read" },
  { value: "write", label: "Write" },
  { value: "publish", label: "Publish" },
  { value: "media", label: "Media" },
  { value: "accounts", label: "Accounts" },
  { value: "admin", label: "Admin (full access)" },
];

const WEBHOOK_EVENTS = [
  "post.created",
  "post.scheduled",
  "post.published",
  "post.failed",
  "post.retrying",
  "account.connected",
  "account.disconnected",
];

function formatDate(iso: string | null | undefined): string {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function toastCenter(showToast: (t: Toast) => void, kind: "ok" | "err", message: string) {
  showToast({ kind, message });
  window.setTimeout(() => showToast(null), 4000);
}

// ────────────────────────────────────────────────────────────────────────────
//  Main component
// ────────────────────────────────────────────────────────────────────────────

export function Settings() {
  const [tab, setTab] = useState<Tab>("api-keys");
  const [toast, setToast] = useState<Toast>(null);

  const showToast = (t: Toast) => {
    setToast(t);
    if (t) window.setTimeout(() => setToast(null), 4000);
  };

  return (
    <div className="min-h-screen bg-neutral-50 flex flex-col">
      <Nav />
      <div className="max-w-[1100px] mx-auto px-6 w-full">
        <div className="flex flex-col items-center justify-center py-8">
          <div className="w-14 h-14 rounded-2xl bg-gradient-to-br from-amber-500 to-orange-500 flex items-center justify-center mb-5 shadow-[0_8px_24px_rgba(245,158,11,0.25)]">
            <Workflow size={26} className="text-white" />
          </div>
          <h1 className="text-[clamp(28px,4vw,38px)] font-extrabold tracking-[-0.02em] mb-2 text-black text-center">
            Settings
          </h1>
          <p className="text-neutral-500 text-[16px] text-center max-w-[480px]">
            Manage your workspaces, programmatic API keys, and webhook subscriptions.
          </p>
        </div>

        {toast && (
          <div
            role="status"
            className={`fixed bottom-6 right-6 z-50 px-4 py-2.5 rounded-xl text-[13px] shadow-lg animate-[fadeUp_0.3s_ease-out] text-white ${toast.kind === "ok" ? "bg-green-600" : "bg-red-600"}`}
          >
            {toast.message}
          </div>
        )}

        <nav
          className="flex flex-wrap items-center gap-1 p-1 bg-neutral-100 rounded-2xl mb-6 max-w-[640px] mx-auto"
          aria-label="Settings sections"
        >
          {(
            [
              { id: "workspaces", label: "Workspaces", icon: Workflow },
              { id: "api-keys", label: "API Keys", icon: Key },
              { id: "webhooks", label: "Webhooks", icon: Webhook },
            ] as const
          ).map((t) => {
            const Active = tab === t.id;
            const Icon = t.icon;
            return (
              <button
                key={t.id}
                type="button"
                onClick={() => setTab(t.id)}
                className={cn(
                  "flex-1 inline-flex items-center justify-center gap-2 px-3 py-2 rounded-xl text-[13px] font-semibold transition-colors",
                  Active ? "bg-white text-black shadow-sm" : "text-neutral-500 hover:text-black",
                )}
                data-testid={`tab-${t.id}`}
                aria-selected={Active}
              >
                <Icon size={14} /> {t.label}
              </button>
            );
          })}
        </nav>

        <section className="pb-12" data-testid={`tab-panel-${tab}`}>
          {tab === "workspaces" && <WorkspacesTab showToast={showToast} />}
          {tab === "api-keys" && <ApiKeysTab showToast={showToast} />}
          {tab === "webhooks" && <WebhooksTab showToast={showToast} />}
        </section>
      </div>
    </div>
  );
}

// ────────────────────────────────────────────────────────────────────────────
//  Workspaces tab
// ────────────────────────────────────────────────────────────────────────────

function WorkspacesTab({ showToast }: { showToast: (t: Toast) => void }) {
  const [list, setList] = useState<Workspace[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [newName, setNewName] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setError(null);
    try {
      const resp = await authedFetch("/api/v1/workspaces");
      const data = (await resp.json()) as { workspaces: Workspace[] };
      setList(data.workspaces ?? []);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Unable to load workspaces.");
      setList([]);
    }
  }, []);

  // Wrap the local loader into a stable retry fn so the shared
  // ErrorState component can re-call it without each caller re-typing.
  const retry = () => void load();

  useEffect(() => {
    void load();
  }, [load]);

  const create = async () => {
    const name = newName.trim();
    if (!name) {
      toastCenter(showToast, "err", "Workspace name is required.");
      return;
    }
    setBusy(true);
    try {
      const resp = await authedFetch("/api/v1/workspaces", {
        method: "POST",
        body: JSON.stringify({ name }),
      });
      if (!resp.ok) {
        const data = (await resp.json().catch(() => ({}))) as { error?: string };
        toastCenter(showToast, "err", data.error ?? `Server returned ${resp.status}`);
        return;
      }
      setNewName("");
      toastCenter(showToast, "ok", "Workspace created.");
      await load();
    } catch (err) {
      toastCenter(
        showToast,
        "err",
        err instanceof ApiError ? err.message : "Could not create workspace.",
      );
    } finally {
      setBusy(false);
    }
  };

  const remove = async (w: Workspace) => {
    if (
      !window.confirm(
        `Delete workspace "${w.name}"? Posts and account links inside it will be removed.`,
      )
    )
      return;
    setBusy(true);
    try {
      const resp = await authedFetch(`/api/v1/workspaces/${w.id}`, { method: "DELETE" });
      if (!resp.ok && resp.status !== 204) {
        const data = (await resp.json().catch(() => ({}))) as { error?: string };
        toastCenter(showToast, "err", data.error ?? `Server returned ${resp.status}`);
        return;
      }
      toastCenter(showToast, "ok", `Workspace "${w.name}" deleted.`);
      await load();
    } catch (err) {
      toastCenter(
        showToast,
        "err",
        err instanceof ApiError ? err.message : "Could not delete workspace.",
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-4">
      <div className="bg-white border border-neutral-200 rounded-2xl p-5">
        <h2 className="font-bold text-[15px] text-black mb-2">Create a workspace</h2>
        <p className="text-[13px] text-neutral-500 mb-4">
          Workspaces group the social accounts you publish from and the people who can publish.
        </p>
        <div className="flex flex-wrap gap-2">
          <input
            type="text"
            maxLength={80}
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            placeholder="e.g. Marketing Team"
            className="flex-1 min-w-[200px] px-3 py-2.5 rounded-lg bg-neutral-50 border border-neutral-200 text-[14px] focus:outline-none focus:ring-2 focus:ring-black/10"
            data-testid="workspace-name-input"
          />
          <button
            type="button"
            onClick={create}
            disabled={busy}
            className="inline-flex items-center gap-1.5 px-4 py-2.5 rounded-xl bg-black text-white text-[13px] font-semibold hover:bg-neutral-800 transition-colors disabled:opacity-50"
            data-testid="workspace-create"
          >
            <Plus size={14} /> Create
          </button>
        </div>
      </div>

      <div className="bg-white border border-neutral-200 rounded-2xl p-5">
        <div className="flex items-center justify-between mb-3">
          <h2 className="font-bold text-[15px] text-black">Your workspaces</h2>
          <button
            type="button"
            onClick={() => void load()}
            className="inline-flex items-center gap-1.5 text-[12px] text-neutral-500 hover:text-black"
          >
            <RefreshCw size={12} /> Refresh
          </button>
        </div>
        {list === null && (
          <div className="py-6 flex justify-center" data-testid="workspaces-loading">
            <Skeleton variant="card" height={36} width="60%" />
          </div>
        )}
        {error && (
          <div className="py-4" data-testid="workspaces-error">
            <ErrorState
              title="Couldn't load workspaces"
              message={error}
              onRetry={retry}
              retryLabel="Try again"
              className="!p-5 !text-left"
            />
          </div>
        )}
        {list !== null && list.length === 0 && (
          <div className="py-4">
            <EmptyState
              title="No workspaces yet"
              description="Create one above to start grouping accounts."
              className="!p-6"
            />
          </div>
        )}
        {list !== null && list.length > 0 && (
          <ul className="divide-y divide-neutral-100" data-testid="workspace-list">
            {list.map((w) => (
              <li
                key={w.id}
                className="flex items-center justify-between gap-3 py-3"
              >
                <div className="flex-1 min-w-0">
                  <div className="font-semibold text-[14px] text-black truncate">
                    {w.name}
                  </div>
                  <div className="text-[11px] text-neutral-500 font-mono">#{w.id}</div>
                </div>
                <button
                  type="button"
                  onClick={() => void remove(w)}
                  disabled={busy}
                  className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[12px] text-red-600 hover:bg-red-50 transition-colors disabled:opacity-50"
                >
                  <Trash2 size={12} /> Delete
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}

// ────────────────────────────────────────────────────────────────────────────
//  API Keys tab (with show-once plaintext modal)
// ────────────────────────────────────────────────────────────────────────────

function ApiKeysTab({ showToast }: { showToast: (t: Toast) => void }) {
  const [list, setList] = useState<ApiKey[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [pending, setPending] = useState<{ name: string; environment: string; permissions: string[] }>({
    name: "",
    environment: "test",
    permissions: ["read"],
  });
  const [revealed, setRevealed] = useState<{ key: ApiKey; plaintext: string } | null>(null);
  const [copied, setCopied] = useState(false);

  const load = useCallback(async () => {
    setError(null);
    try {
      const resp = await authedFetch("/api/v1/api-keys");
      const data = (await resp.json()) as { keys: ApiKey[] };
      setList(data.keys ?? []);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Unable to load API keys.");
      setList([]);
    }
  }, []);

  // Wrap the local loader into a stable retry fn so the shared
  // ErrorState component can re-call it without each caller re-typing.
  const retry = () => void load();

  useEffect(() => {
    void load();
  }, [load]);

  const create = async () => {
    const name = pending.name.trim();
    if (!name) {
      toastCenter(showToast, "err", "Key name is required.");
      return;
    }
    if (pending.permissions.length === 0) {
      toastCenter(showToast, "err", "Pick at least one permission.");
      return;
    }
    setCreating(true);
    try {
      const resp = await authedFetch("/api/v1/api-keys", {
        method: "POST",
        body: JSON.stringify({
          name,
          environment: pending.environment,
          permissions: pending.permissions,
        }),
      });
      const data = (await resp.json().catch(() => null)) as
        | { key?: ApiKey; plaintext?: string; error?: string }
        | null;
      if (!resp.ok || !data?.key || !data.plaintext) {
        toastCenter(
          showToast,
          "err",
          data?.error ?? `Server returned ${resp.status}`,
        );
        return;
      }
      setRevealed({ key: data.key, plaintext: data.plaintext });
      setPending({ name: "", environment: "test", permissions: ["read"] });
      await load();
    } catch (err) {
      toastCenter(
        showToast,
        "err",
        err instanceof ApiError ? err.message : "Could not create API key.",
      );
    } finally {
      setCreating(false);
    }
  };

  const revoke = async (k: ApiKey) => {
    if (
      !window.confirm(
        `Revoke "${k.name}"? Anything using this key — scripts, integrations — will stop working immediately.`,
      )
    )
      return;
    try {
      const resp = await authedFetch(`/api/v1/api-keys/${k.id}`, { method: "DELETE" });
      if (!resp.ok && resp.status !== 204) {
        const data = (await resp.json().catch(() => ({}))) as { error?: string };
        toastCenter(showToast, "err", data.error ?? `Server returned ${resp.status}`);
        return;
      }
      toastCenter(showToast, "ok", "API key revoked.");
      await load();
    } catch (err) {
      toastCenter(
        showToast,
        "err",
        err instanceof ApiError ? err.message : "Could not revoke API key.",
      );
    }
  };

  const rotate = async (k: ApiKey) => {
    try {
      const resp = await authedFetch(`/api/v1/api-keys/${k.id}/rotate`, { method: "POST" });
      const data = (await resp.json().catch(() => null)) as
        | { key?: ApiKey; plaintext?: string; error?: string }
        | null;
      if (!resp.ok || !data?.key || !data.plaintext) {
        toastCenter(showToast, "err", data?.error ?? `Server returned ${resp.status}`);
        return;
      }
      setRevealed({ key: data.key, plaintext: data.plaintext });
      await load();
    } catch (err) {
      toastCenter(
        showToast,
        "err",
        err instanceof ApiError ? err.message : "Could not rotate API key.",
      );
    }
  };

  const copy = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      toastCenter(showToast, "err", "Couldn't copy. Select the key and copy manually.");
    }
  };

  return (
    <div className="space-y-4">
      <div className="bg-white border border-neutral-200 rounded-2xl p-5">
        <h2 className="font-bold text-[15px] text-black mb-2">Create an API key</h2>
        <p className="text-[13px] text-neutral-500 mb-4">
          Programmatic access for your integrations. The plaintext is shown once — save it before closing this dialog.
        </p>
        <div className="grid gap-3 sm:grid-cols-2">
          <div>
            <label className="block text-[12px] font-semibold text-neutral-700 mb-1.5">
              Name
            </label>
            <input
              type="text"
              maxLength={80}
              value={pending.name}
              onChange={(e) => setPending({ ...pending, name: e.target.value })}
              placeholder="e.g. CI deploy key"
              className="w-full px-3 py-2.5 rounded-lg bg-neutral-50 border border-neutral-200 text-[14px] focus:outline-none focus:ring-2 focus:ring-black/10"
              data-testid="apikey-name"
            />
          </div>
          <div>
            <label className="block text-[12px] font-semibold text-neutral-700 mb-1.5">
              Environment
            </label>
            <select
              value={pending.environment}
              onChange={(e) => setPending({ ...pending, environment: e.target.value })}
              className="w-full px-3 py-2.5 rounded-lg bg-neutral-50 border border-neutral-200 text-[14px] focus:outline-none focus:ring-2 focus:ring-black/10"
            >
              <option value="test">Test</option>
              <option value="live">Live</option>
            </select>
          </div>
          <div className="sm:col-span-2">
            <label className="block text-[12px] font-semibold text-neutral-700 mb-1.5">
              Permissions
            </label>
            <div className="flex flex-wrap gap-2">
              {PERMISSION_OPTIONS.map((p) => {
                const active = pending.permissions.includes(p.value);
                return (
                  <button
                    type="button"
                    key={p.value}
                    onClick={() =>
                      setPending({
                        ...pending,
                        permissions: active
                          ? pending.permissions.filter((x) => x !== p.value)
                          : [...pending.permissions, p.value],
                      })
                    }
                    className={cn(
                      "px-3 py-1.5 rounded-full text-[12px] font-medium border transition-colors",
                      active
                        ? "bg-black text-white border-black"
                        : "bg-white border-neutral-200 text-neutral-700 hover:border-neutral-400",
                    )}
                  >
                    {p.label}
                  </button>
                );
              })}
            </div>
          </div>
          <div className="sm:col-span-2 flex justify-end">
            <button
              type="button"
              onClick={create}
              disabled={creating}
              className="inline-flex items-center gap-1.5 px-4 py-2.5 rounded-xl bg-black text-white text-[13px] font-semibold hover:bg-neutral-800 transition-colors disabled:opacity-50"
              data-testid="apikey-create"
            >
              <Plus size={14} /> Create key
            </button>
          </div>
        </div>
      </div>

      <div className="bg-white border border-neutral-200 rounded-2xl p-5">
        <div className="flex items-center justify-between mb-3">
          <h2 className="font-bold text-[15px] text-black">Your API keys</h2>
          <button
            type="button"
            onClick={() => void load()}
            className="inline-flex items-center gap-1.5 text-[12px] text-neutral-500 hover:text-black"
          >
            <RefreshCw size={12} /> Refresh
          </button>
        </div>
        {list === null && (
          <div className="py-6 flex justify-center" data-testid="apikeys-loading">
            <Skeleton variant="card" height={36} width="60%" />
          </div>
        )}
        {error && (
          <div className="py-4" data-testid="apikeys-error">
            <ErrorState
              title="Couldn't load API keys"
              message={error}
              onRetry={retry}
              retryLabel="Try again"
              className="!p-5 !text-left"
            />
          </div>
        )}
        {list !== null && list.length === 0 && (
          <div className="py-4">
            <EmptyState
              title="No API keys yet"
              description="Create one above to start using the API."
              className="!p-6"
            />
          </div>
        )}
        {list !== null && list.length > 0 && (
          <ul className="divide-y divide-neutral-100" data-testid="apikey-list">
            {list.map((k) => {
              const revoked = !!k.revoked_at;
              return (
                <li key={k.id} className="flex items-center justify-between gap-3 py-3">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="font-semibold text-[14px] text-black truncate">
                        {k.name}
                      </span>
                      <span
                        className={cn(
                          "inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-semibold ring-1",
                          k.environment === "live"
                            ? "bg-green-50 text-green-700 ring-green-200"
                            : "bg-neutral-100 text-neutral-700 ring-neutral-200",
                        )}
                      >
                        {k.environment}
                      </span>
                      {revoked && (
                        <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-semibold bg-red-50 text-red-700 ring-1 ring-red-200">
                          Revoked
                        </span>
                      )}
                    </div>
                    <div className="text-[11px] text-neutral-500 font-mono mt-0.5">
                      {k.key_prefix}… · #{k.id}
                    </div>
                    <div className="text-[11px] text-neutral-400 mt-0.5">
                      Created {formatDate(k.created_at)}
                      {k.last_used_at ? ` · Last used ${formatDate(k.last_used_at)}` : ""}
                    </div>
                  </div>
                  <div className="flex items-center gap-1">
                    {!revoked && (
                      <button
                        type="button"
                        onClick={() => void rotate(k)}
                        className="inline-flex items-center gap-1 px-2.5 py-1.5 rounded-lg text-[12px] text-neutral-700 hover:bg-neutral-100"
                      >
                        <RefreshCw size={12} /> Rotate
                      </button>
                    )}
                    <button
                      type="button"
                      disabled={revoked}
                      onClick={() => void revoke(k)}
                      className="inline-flex items-center gap-1 px-2.5 py-1.5 rounded-lg text-[12px] text-red-600 hover:bg-red-50 disabled:opacity-50"
                    >
                      <Trash2 size={12} /> Revoke
                    </button>
                  </div>
                </li>
              );
            })}
          </ul>
        )}
      </div>

      {revealed && (
        <PlaintextModal
          plaintext={revealed.plaintext}
          keyMeta={revealed.key}
          copied={copied}
          onCopy={() => void copy(revealed.plaintext)}
          onClose={() => setRevealed(null)}
        />
      )}
    </div>
  );
}

function PlaintextModal({
  plaintext,
  keyMeta,
  copied,
  onCopy,
  onClose,
}: {
  plaintext: string;
  keyMeta: ApiKey;
  copied: boolean;
  onCopy: () => void;
  onClose: () => void;
}) {
  // The X button and backdrop click route through requestClose so a
  // user who hasn't acknowledged copy gets a confirm dialog. Closing
  // via the explicit "I've saved it" button (which calls onClose
  // directly) bypasses the gate intentionally — that button IS the
  // acknowledgement.
  const requestClose = () => {
    if (
      !copied &&
      !window.confirm(
        "Close without copying? You'll have to revoke this key and create a new one — the plaintext is gone.",
      )
    )
      return;
    onClose();
  };
  return (
    <div
      className="fixed inset-0 z-50 bg-black/60 flex items-center justify-center p-4 backdrop-blur-sm"
      onClick={requestClose}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="apikey-modal-title"
        onClick={(e) => e.stopPropagation()}
        className="bg-white rounded-2xl shadow-xl max-w-[520px] w-full p-6 animate-[fadeUp_0.3s_ease-out]"
      >
        <div className="flex items-start justify-between mb-3">
          <div>
            <h3 id="apikey-modal-title" className="font-bold text-[16px] text-black">
              Save your new API key
            </h3>
            <p className="text-[12px] text-neutral-500 mt-1">
              {keyMeta.name} ({keyMeta.environment})
            </p>
          </div>
          <button
            type="button"
            onClick={requestClose}
            aria-label="Close"
            className="p-1 text-neutral-500 hover:text-black rounded-lg hover:bg-neutral-100"
            data-testid="apikey-modal-close"
          >
            <X size={18} />
          </button>
        </div>
        <p className="text-[13px] text-amber-700 bg-amber-50 border border-amber-200 rounded-lg p-3 mb-4">
          This is the only time this key will be shown. Store it in your secret manager now.
        </p>
        <div className="bg-neutral-900 text-neutral-50 rounded-lg p-3 font-mono text-[12px] break-all select-all relative">
          {plaintext}
          <button
            type="button"
            onClick={onCopy}
            className="absolute top-2 right-2 inline-flex items-center gap-1 px-2 py-1 rounded-md bg-white/10 hover:bg-white/20 text-[11px] text-white"
            data-testid="apikey-copy"
          >
            {copied ? <Check size={12} /> : <Copy size={12} />}
            {copied ? "Copied" : "Copy"}
          </button>
        </div>
        <div className="flex items-center justify-end gap-2 mt-4">
          <button
            type="button"
            onClick={onClose}
            className="px-4 py-2 rounded-xl bg-black text-white text-[13px] font-semibold hover:bg-neutral-800"
          >
            I've saved it
          </button>
        </div>
      </div>
    </div>
  );
}

// ────────────────────────────────────────────────────────────────────────────
//  Webhooks tab
// ────────────────────────────────────────────────────────────────────────────

function WebhooksTab({ showToast }: { showToast: (t: Toast) => void }) {
  const [list, setList] = useState<WebhookEndpoint[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState({ url: "", secret: "", events: WEBHOOK_EVENTS.slice(0, 3) });
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setError(null);
    try {
      const resp = await authedFetch("/api/v1/webhooks/endpoints");
      const data = (await resp.json()) as { endpoints: WebhookEndpoint[] };
      setList(data.endpoints ?? []);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Unable to load webhooks.");
      setList([]);
    }
  }, []);

  // Wrap the local loader into a stable retry fn so the shared
  // ErrorState component can re-call it without each caller re-typing.
  const retry = () => void load();

  useEffect(() => {
    void load();
  }, [load]);

  const create = async () => {
    const url = pending.url.trim();
    const secret = pending.secret.trim();
    if (!url || !secret) {
      toastCenter(showToast, "err", "URL and secret are required.");
      return;
    }
    if (pending.events.length === 0) {
      toastCenter(showToast, "err", "Pick at least one event.");
      return;
    }
    if (!url.startsWith("https://") && !url.startsWith("http://")) {
      toastCenter(showToast, "err", "URL must start with http:// or https://.");
      return;
    }
    setBusy(true);
    try {
      const resp = await authedFetch("/api/v1/webhooks/endpoints", {
        method: "POST",
        body: JSON.stringify({
          url,
          secret,
          events: pending.events,
          status: "active",
        }),
      });
      if (!resp.ok) {
        const data = (await resp.json().catch(() => ({}))) as { error?: string };
        toastCenter(showToast, "err", data.error ?? `Server returned ${resp.status}`);
        return;
      }
      setPending({ url: "", secret: "", events: WEBHOOK_EVENTS.slice(0, 3) });
      toastCenter(showToast, "ok", "Webhook endpoint added.");
      await load();
    } catch (err) {
      toastCenter(
        showToast,
        "err",
        err instanceof ApiError ? err.message : "Could not register webhook.",
      );
    } finally {
      setBusy(false);
    }
  };

  const remove = async (e: WebhookEndpoint) => {
    if (!window.confirm(`Remove webhook for ${e.url}?`)) return;
    try {
      const resp = await authedFetch(`/api/v1/webhooks/endpoints/${e.id}`, {
        method: "DELETE",
      });
      if (!resp.ok && resp.status !== 204) {
        const data = (await resp.json().catch(() => ({}))) as { error?: string };
        toastCenter(showToast, "err", data.error ?? `Server returned ${resp.status}`);
        return;
      }
      toastCenter(showToast, "ok", "Webhook removed.");
      await load();
    } catch (err) {
      toastCenter(
        showToast,
        "err",
        err instanceof ApiError ? err.message : "Could not remove webhook.",
      );
    }
  };

  return (
    <div className="space-y-4">
      <div className="bg-white border border-neutral-200 rounded-2xl p-5">
        <h2 className="font-bold text-[15px] text-black mb-2">Add a webhook endpoint</h2>
        <p className="text-[13px] text-neutral-500 mb-4">
          Receive signed event payloads at this URL whenever a post or account event happens in your workspace.
        </p>
        <div className="grid gap-3">
          <div>
            <label className="block text-[12px] font-semibold text-neutral-700 mb-1.5">
              URL
            </label>
            <input
              type="url"
              value={pending.url}
              onChange={(e) => setPending({ ...pending, url: e.target.value })}
              placeholder="https://your-app.com/webhooks/incoming"
              className="w-full px-3 py-2.5 rounded-lg bg-neutral-50 border border-neutral-200 text-[14px] focus:outline-none focus:ring-2 focus:ring-black/10"
            />
          </div>
          <div>
            <label className="block text-[12px] font-semibold text-neutral-700 mb-1.5">
              Signing secret
            </label>
            <input
              type="text"
              value={pending.secret}
              onChange={(e) => setPending({ ...pending, secret: e.target.value })}
              placeholder="A random string; we'll HMAC-SHA256 each delivery with it"
              className="w-full px-3 py-2.5 rounded-lg bg-neutral-50 border border-neutral-200 text-[14px] font-mono focus:outline-none focus:ring-2 focus:ring-black/10"
              data-testid="webhook-secret"
            />
            <p className="text-[11px] text-neutral-500 mt-1">
              The server stores this and signs every delivery. It is not shown again after creation.
            </p>
          </div>
          <div>
            <label className="block text-[12px] font-semibold text-neutral-700 mb-1.5">
              Events
            </label>
            <div className="flex flex-wrap gap-2">
              {WEBHOOK_EVENTS.map((ev) => {
                const active = pending.events.includes(ev);
                return (
                  <button
                    type="button"
                    key={ev}
                    onClick={() =>
                      setPending({
                        ...pending,
                        events: active
                          ? pending.events.filter((x) => x !== ev)
                          : [...pending.events, ev],
                      })
                    }
                    className={cn(
                      "px-3 py-1.5 rounded-full text-[12px] font-mono border transition-colors",
                      active
                        ? "bg-black text-white border-black"
                        : "bg-white border-neutral-200 text-neutral-700 hover:border-neutral-400",
                    )}
                  >
                    {ev}
                  </button>
                );
              })}
            </div>
          </div>
          <div className="flex justify-end">
            <button
              type="button"
              onClick={create}
              disabled={busy}
              className="inline-flex items-center gap-1.5 px-4 py-2.5 rounded-xl bg-black text-white text-[13px] font-semibold hover:bg-neutral-800 transition-colors disabled:opacity-50"
              data-testid="webhook-create"
            >
              <Plus size={14} /> Register endpoint
            </button>
          </div>
        </div>
      </div>

      <div className="bg-white border border-neutral-200 rounded-2xl p-5">
        <div className="flex items-center justify-between mb-3">
          <h2 className="font-bold text-[15px] text-black">Your webhook endpoints</h2>
          <button
            type="button"
            onClick={() => void load()}
            className="inline-flex items-center gap-1.5 text-[12px] text-neutral-500 hover:text-black"
          >
            <RefreshCw size={12} /> Refresh
          </button>
        </div>
        {list === null && (
          <div className="py-6 flex justify-center" data-testid="webhooks-loading">
            <Skeleton variant="card" height={36} width="60%" />
          </div>
        )}
        {error && (
          <div className="py-4" data-testid="webhooks-error">
            <ErrorState
              title="Couldn't load webhooks"
              message={error}
              onRetry={retry}
              retryLabel="Try again"
              className="!p-5 !text-left"
            />
          </div>
        )}
        {list !== null && list.length === 0 && (
          <div className="py-4">
            <EmptyState
              title="No webhook endpoints yet"
              description="Add one above to start receiving events."
              className="!p-6"
            />
          </div>
        )}
        {list !== null && list.length > 0 && (
          <ul className="divide-y divide-neutral-100" data-testid="webhook-list">
            {list.map((e) => (
              <li
                key={e.id}
                className="flex items-center justify-between gap-3 py-3"
              >
                <div className="flex-1 min-w-0">
                  <div className="font-semibold text-[13px] text-black truncate font-mono">
                    {e.url}
                  </div>
                  <div className="flex flex-wrap gap-1 mt-1">
                    {e.events.map((ev) => (
                      <span
                        key={ev}
                        className="inline-flex items-center px-1.5 py-0.5 rounded-full text-[10px] font-mono bg-neutral-100 text-neutral-700"
                      >
                        {ev}
                      </span>
                    ))}
                  </div>
                  <div className="text-[11px] text-neutral-400 mt-1 font-mono">
                    #{e.id} · {e.status}
                  </div>
                </div>
                <button
                  type="button"
                  onClick={() => void remove(e)}
                  className="inline-flex items-center gap-1 px-2.5 py-1.5 rounded-lg text-[12px] text-red-600 hover:bg-red-50"
                >
                  <Trash2 size={12} /> Remove
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
