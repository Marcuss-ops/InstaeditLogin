import { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { CheckCircle2, ChevronLeft, Clock, Save, Send, Sparkles } from "lucide-react";
import { Nav } from "../components/Nav";
import { ApiError, authedFetch } from "../lib/auth";
import { PROVIDERS, getProvider, type ProviderId } from "../lib/providers";

type Workspace = {
  id: number;
  name: string;
  owner_id: number;
};

type PlatformAccount = {
  id: number;
  platform: ProviderId;
  username: string;
};

type FormState = {
  workspace_id: number | null;
  title: string;
  caption: string;
  scheduled_at: string; // datetime-local
  selected: Record<number, boolean>;
};

type Toast = { kind: "ok" | "err"; message: string } | null;

function toIso(datetimeLocal: string): string | null {
  if (!datetimeLocal) return null;
  const d = new Date(datetimeLocal);
  if (Number.isNaN(d.getTime())) return null;
  return d.toISOString();
}

export function Compose() {
  const navigate = useNavigate();
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [accounts, setAccounts] = useState<PlatformAccount[]>([]);
  const [loadingMeta, setLoadingMeta] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [toast, setToast] = useState<Toast>(null);

  const [form, setForm] = useState<FormState>({
    workspace_id: null,
    title: "",
    caption: "",
    scheduled_at: "",
    selected: {},
  });

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const [wsResp, accResp] = await Promise.all([
          authedFetch("/api/v1/workspaces"),
          authedFetch("/api/v1/accounts"),
        ]);
        if (cancelled) return;
        const wsData = (await wsResp.json()) as { workspaces: Workspace[] };
        const accData = (await accResp.json()) as { accounts: PlatformAccount[] };
        const wsList = wsData.workspaces ?? [];
        const accList = ((accData.accounts ?? []) as PlatformAccount[]).filter(
          (a) => a && a.id && a.platform,
        );
        setWorkspaces(wsList);
        setAccounts(accList);
        setForm((prev) => ({
          ...prev,
          workspace_id: prev.workspace_id ?? wsList[0]?.id ?? null,
        }));
      } catch (err) {
        if (cancelled) return;
        setToast({
          kind: "err",
          message:
            err instanceof ApiError
              ? err.message
              : "Unable to load workspaces or accounts.",
        });
      } finally {
        if (!cancelled) setLoadingMeta(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  // PlatformAccount has no workspace_id field on the backend
  // model, so /api/v1/accounts returns every connected account
  // owned by the user. We surface them all; the post the user
  // creates will be stamped with the chosen workspace_id from the
  // picker above, and the publish worker reads the target account
  // via post_targets.platform_account_id.
  const eligibleAccounts = accounts;

  const selectedIds = useMemo(
    () => Object.entries(form.selected).filter(([, v]) => v).map(([k]) => Number(k)),
    [form.selected],
  );

  const updateField = <K extends keyof FormState>(key: K, value: FormState[K]) => {
    setForm((prev) => ({ ...prev, [key]: value }));
  };

  const toggleAccount = (id: number) => {
    setForm((prev) => ({
      ...prev,
      selected: { ...prev.selected, [id]: !prev.selected[id] },
    }));
  };

  const showToast = (t: Toast) => {
    setToast(t);
    if (t) window.setTimeout(() => setToast(null), 4000);
  };

  const submit = useCallback(
    async (mode: "draft" | "schedule" | "publish-now") => {
      if (!form.workspace_id) {
        showToast({ kind: "err", message: "Choose a workspace first." });
        return;
      }
      if (selectedIds.length === 0) {
        showToast({ kind: "err", message: "Pick at least one connected account." });
        return;
      }
      // "publish-now" is an honest action: it stamps scheduled_at=NOW so
      // the backend auto-promotes status to "queued"; the publish worker
      // then picks it up on its next tick. This reuses the schedule
      // pathway rather than inventing a third endpoint.
      // "schedule" requires a future timestamp entered by the user.
      let scheduledAtIso: string | null = null;
      if (mode === "schedule") {
        scheduledAtIso = toIso(form.scheduled_at);
        if (!scheduledAtIso) {
          showToast({ kind: "err", message: "Set a scheduled date before scheduling." });
          return;
        }
        if (new Date(scheduledAtIso).getTime() <= Date.now()) {
          showToast({ kind: "err", message: "Scheduled time must be in the future." });
          return;
        }
      } else if (mode === "publish-now") {
        scheduledAtIso = new Date().toISOString();
      }
      setSubmitting(true);
      try {
        const body: Record<string, unknown> = {
          workspace_id: form.workspace_id,
          content: {
            ...(form.title.trim() ? { title: form.title.trim() } : {}),
            ...(form.caption.trim() ? { caption: form.caption.trim() } : {}),
          },
          targets: selectedIds.map((id) => ({ platform_account_id: id })),
        };
        if (scheduledAtIso) body.scheduled_at = scheduledAtIso;

        const resp = await authedFetch("/api/v1/posts", {
          method: "POST",
          body: JSON.stringify(body),
        });
        if (!resp.ok) {
          const data = (await resp.json().catch(() => ({}))) as { error?: string };
          showToast({
            kind: "err",
            message: data.error ?? `Server returned ${resp.status}`,
          });
          return;
        }
        const data = (await resp.json().catch(() => ({}))) as { id?: number };
        const label =
          mode === "publish-now"
            ? "Queued for publishing."
            : mode === "schedule"
              ? "Scheduled."
              : "Draft saved.";
        showToast({
          kind: "ok",
          message: data.id ? `${label} Post #${data.id}.` : label,
        });
        window.setTimeout(() => navigate("/posts"), 1000);
      } catch (err) {
        showToast({
          kind: "err",
          message:
            err instanceof ApiError ? err.message : "Could not save the post.",
        });
      } finally {
        setSubmitting(false);
      }
    },
    [form, selectedIds, navigate],
  );

  const handlePublishNow = () => {
    if (selectedIds.length === 0) {
      showToast({ kind: "err", message: "Pick at least one connected account." });
      return;
    }
    if (
      !window.confirm(
        "Publish immediately? The post will be queued and the worker will dispatch it on its next tick.",
      )
    )
      return;
    void submit("publish-now");
  };

  return (
    <div className="min-h-screen bg-neutral-50 flex flex-col">
      <Nav />
      <div className="max-w-[820px] mx-auto px-6 w-full">
        <button
          type="button"
          onClick={() => navigate(-1)}
          className="inline-flex items-center gap-1.5 text-[13px] text-neutral-500 hover:text-black mt-6 mb-2 transition-colors"
        >
          <ChevronLeft size={13} /> Back
        </button>

        <div className="flex flex-col items-center justify-center py-4">
          <div className="w-14 h-14 rounded-2xl bg-gradient-to-br from-blue-500 to-violet-500 flex items-center justify-center mb-5 shadow-[0_8px_24px_rgba(123,97,255,0.25)]">
            <Sparkles size={26} className="text-white" />
          </div>
          <h1 className="text-[clamp(28px,4vw,38px)] font-extrabold tracking-[-0.02em] mb-2 text-black text-center">
            Compose a post
          </h1>
          <p className="text-neutral-500 text-[15px] text-center max-w-[480px]">
            Save as a draft or schedule it for later. Your connected accounts are listed below.
          </p>
        </div>

        {/* Toast */}
        {toast && (
          <div
            role="status"
            className={`fixed bottom-6 right-6 z-50 px-4 py-2.5 rounded-xl text-[13px] shadow-lg animate-[fadeUp_0.3s_ease-out] text-white ${toast.kind === "ok" ? "bg-green-600" : "bg-red-600"}`}
            data-testid={`toast-${toast.kind}`}
          >
            {toast.message}
          </div>
        )}

        <form
          className="bg-white border border-neutral-200 rounded-2xl p-6 mb-8"
          onSubmit={(e) => {
            e.preventDefault();
            void submit("draft");
          }}
        >
          {loadingMeta ? (
            <div className="flex items-center justify-center py-10 gap-3">
              <div className="w-8 h-8 rounded-full border-4 border-neutral-200 border-t-black animate-spin" />
              <p className="text-[13px] text-neutral-500">Loading workspaces…</p>
            </div>
          ) : (
            <div className="space-y-5">
              <div>
                <label className="block text-[12px] font-semibold text-neutral-700 mb-1.5">
                  Workspace
                </label>
                <select
                  value={form.workspace_id ? String(form.workspace_id) : ""}
                  onChange={(e) => updateField("workspace_id", Number(e.target.value) || null)}
                  className="w-full px-3 py-2.5 rounded-lg bg-neutral-50 border border-neutral-200 text-[14px] focus:outline-none focus:ring-2 focus:ring-black/10"
                  data-testid="compose-workspace"
                >
                  {workspaces.length === 0 && <option value="">No workspaces yet</option>}
                  {workspaces.map((w) => (
                    <option key={w.id} value={w.id}>
                      {w.name}
                    </option>
                  ))}
                </select>
              </div>

              <div>
                <label className="block text-[12px] font-semibold text-neutral-700 mb-1.5">
                  Title
                </label>
                <input
                  type="text"
                  maxLength={120}
                  value={form.title}
                  onChange={(e) => updateField("title", e.target.value)}
                  placeholder="A short, internal title — visible only in this app."
                  className="w-full px-3 py-2.5 rounded-lg bg-neutral-50 border border-neutral-200 text-[14px] focus:outline-none focus:ring-2 focus:ring-black/10"
                  data-testid="compose-title"
                />
              </div>

              <div>
                <label className="block text-[12px] font-semibold text-neutral-700 mb-1.5">
                  Caption
                </label>
                <textarea
                  rows={5}
                  maxLength={2200}
                  value={form.caption}
                  onChange={(e) => updateField("caption", e.target.value)}
                  placeholder="What do you want to say?"
                  className="w-full px-3 py-2.5 rounded-lg bg-neutral-50 border border-neutral-200 text-[14px] focus:outline-none focus:ring-2 focus:ring-black/10 resize-none"
                  data-testid="compose-caption"
                />
                <p className="mt-1 text-[11px] text-neutral-400 text-right">
                  {form.caption.length} / 2200
                </p>
              </div>

              <div>
                <label className="block text-[12px] font-semibold text-neutral-700 mb-1.5">
                  Publish to
                </label>
                {eligibleAccounts.length === 0 ? (
                  <div className="rounded-lg border border-dashed border-neutral-300 p-4 text-center text-[13px] text-neutral-500">
                    No connected accounts in this workspace. Connect one from the{" "}
                    <a href="/accounts" className="text-black underline">
                      Accounts
                    </a>{" "}
                    page first.
                  </div>
                ) : (
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                    {eligibleAccounts.map((acc) => {
                      const provider = getProvider(acc.platform) ?? PROVIDERS[0];
                      const checked = !!form.selected[acc.id];
                      return (
                        <label
                          key={acc.id}
                          className={`flex items-center gap-3 p-3 rounded-xl border cursor-pointer transition-colors ${checked ? "border-black bg-neutral-50" : "border-neutral-200 hover:bg-neutral-50"}`}
                          data-testid={`compose-target-${acc.id}`}
                        >
                          <input
                            type="checkbox"
                            checked={checked}
                            onChange={() => toggleAccount(acc.id)}
                            className="w-4 h-4 rounded accent-black"
                          />
                          <div
                            className={`w-8 h-8 rounded-lg bg-gradient-to-br ${provider.color} flex items-center justify-center text-white shrink-0`}
                          >
                            <span className="scale-[0.8]">{provider.icon}</span>
                          </div>
                          <div className="flex-1 min-w-0">
                            <div className="font-semibold text-[13px] text-black">
                              {provider.name}
                            </div>
                            <div className="text-[12px] text-neutral-500 truncate">
                              @{acc.username || "—"}
                            </div>
                          </div>
                          {checked && (
                            <CheckCircle2 size={16} className="text-black shrink-0" />
                          )}
                        </label>
                      );
                    })}
                  </div>
                )}
              </div>

              <div>
                <label className="block text-[12px] font-semibold text-neutral-700 mb-1.5">
                  Schedule (optional)
                </label>
                <input
                  type="datetime-local"
                  value={form.scheduled_at}
                  onChange={(e) => updateField("scheduled_at", e.target.value)}
                  className="w-full px-3 py-2.5 rounded-lg bg-neutral-50 border border-neutral-200 text-[14px] focus:outline-none focus:ring-2 focus:ring-black/10"
                  data-testid="compose-scheduled"
                />
                <p className="mt-1 text-[11px] text-neutral-500">
                  Leave empty to save as draft. Set a future time to schedule.
                </p>
              </div>

              <div className="flex flex-wrap items-center justify-end gap-2 pt-4 border-t border-neutral-100">
                <button
                  type="submit"
                  disabled={submitting || loadingMeta}
                  className="inline-flex items-center gap-2 px-4 py-2.5 rounded-xl bg-neutral-100 hover:bg-neutral-200 text-[13px] font-semibold text-neutral-800 transition-colors disabled:opacity-50"
                  data-testid="compose-save-draft"
                >
                  <Save size={14} /> Save draft
                </button>
                <button
                  type="button"
                  disabled={submitting || !form.scheduled_at}
                  onClick={() => void submit("schedule")}
                  className="inline-flex items-center gap-2 px-4 py-2.5 rounded-xl bg-black text-white text-[13px] font-semibold hover:bg-neutral-800 transition-colors disabled:opacity-50"
                  data-testid="compose-schedule"
                >
                  <Clock size={14} /> Schedule
                </button>
                <button
                  type="button"
                  disabled={submitting || selectedIds.length === 0}
                  onClick={handlePublishNow}
                  className="inline-flex items-center gap-2 px-4 py-2.5 rounded-xl bg-gradient-to-br from-blue-500 to-violet-500 text-white text-[13px] font-semibold hover:opacity-90 transition-opacity disabled:opacity-50"
                  data-testid="compose-publish-now"
                >
                  <Send size={14} /> Publish now
                </button>
              </div>
            </div>
          )}
        </form>
      </div>
    </div>
  );
}
