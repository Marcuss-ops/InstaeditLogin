/**
 * MediaPickerDialog — Media Library picker for canvas image objects.
 *
 * GET /api/v1/media returns only compact list rows. Each visible card
 * requests GET /api/v1/media/{id} on demand; the shared query registry
 * deduplicates and caches the short-lived signed preview URL.
 */
import { memo, useEffect, useState } from "react";
import { ImageIcon, Loader2, Search } from "lucide-react";
import { authedFetch } from "../../../lib/auth";
import { useSharedQuery } from "../../../lib/queryRegistry";

export interface MediaPickerItem {
  id: string;
  filename: string;
  content_type: string;
  preview_url?: string;
  width?: number | null;
  height?: number | null;
}

export type MediaPickerDetail = MediaPickerItem & { preview_url?: string };

export interface MediaPickerDialogProps {
  onPick: (item: MediaPickerDetail) => void;
  onClose: () => void;
}

type PickerState =
  | { kind: "loading" }
  | { kind: "ready"; items: MediaPickerItem[] }
  | { kind: "error"; message: string };

function usePickerDetail(id: string, enabled: boolean) {
  return useSharedQuery<MediaPickerDetail>(`media-picker-detail:${id}`, {
    enabled,
    staleTime: 5 * 60_000,
    fetcher: async (signal) => {
      const response = await authedFetch(`/api/v1/media/${encodeURIComponent(id)}`, { signal });
      return (await response.json()) as MediaPickerDetail;
    },
  });
}

const MediaPickerCard = memo(function MediaPickerCard({
  item,
  onPick,
}: {
  item: MediaPickerItem;
  onPick: (item: MediaPickerDetail) => void;
}) {
  const [node, setNode] = useState<HTMLButtonElement | null>(null);
  const [visible, setVisible] = useState(false);
  const detail = usePickerDetail(item.id, visible);

  useEffect(() => {
    if (visible || !node || typeof IntersectionObserver === "undefined") return;
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          setVisible(true);
          observer.disconnect();
        }
      },
      { rootMargin: "240px" },
    );
    observer.observe(node);
    return () => observer.disconnect();
  }, [node, visible]);

  return (
    <button
      ref={setNode}
      type="button"
      onClick={() => onPick(detail.data ?? item)}
      className="group overflow-hidden rounded-xl border border-white/[0.08] bg-white/[0.02] text-left transition-colors hover:border-white/[0.20]"
    >
      <div className="aspect-video w-full overflow-hidden bg-black">
        {detail.data?.preview_url ? (
          <img
            src={detail.data.preview_url}
            alt={item.filename}
            loading="lazy"
            className="h-full w-full object-cover transition-transform group-hover:scale-105"
          />
        ) : detail.isFetching ? (
          <div className="flex h-full w-full items-center justify-center">
            <Loader2 size={18} className="animate-spin text-white/30" />
          </div>
        ) : (
          <div className="flex h-full w-full items-center justify-center">
            <ImageIcon size={22} className="text-white/25" />
          </div>
        )}
      </div>
      <div className="truncate px-2 py-1.5 text-[11px] text-[#9aa0aa]">{item.filename}</div>
    </button>
  );
});

export function MediaPickerDialog({ onPick, onClose }: MediaPickerDialogProps) {
  const [state, setState] = useState<PickerState>({ kind: "loading" });
  const [query, setQuery] = useState("");

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      setState({ kind: "loading" });
      try {
        const resp = await authedFetch("/api/v1/media?limit=100");
        const data = (await resp.json()) as { items?: MediaPickerItem[] };
        if (!cancelled) setState({ kind: "ready", items: data.items ?? [] });
      } catch (err) {
        if (!cancelled) {
          setState({
            kind: "error",
            message: err instanceof Error ? err.message : "Impossibile caricare la Media Library.",
          });
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const items = state.kind === "ready"
    ? state.items.filter((item) => item.filename.toLowerCase().includes(query.trim().toLowerCase()))
    : [];

  return (
    <div role="dialog" aria-modal="true" aria-label="Media Library" className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <button type="button" aria-label="Chiudi" onClick={onClose} className="absolute inset-0 cursor-default bg-black/70 backdrop-blur-sm" />
      <div className="relative max-h-[85vh] w-full max-w-2xl overflow-y-auto rounded-2xl border border-white/[0.12] bg-[#1f1f2e] p-6 shadow-[0_8px_32px_rgba(0,0,0,0.5)]">
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-lg font-bold text-white">Media Library</h2>
          <div className="relative">
            <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-[#9aa0aa]" />
            <input type="text" aria-label="Cerca nella Media Library" placeholder="Cerca…" value={query} onChange={(e) => setQuery(e.target.value)} className="w-56 rounded-xl border border-white/[0.08] bg-white/[0.04] py-2 pl-9 pr-3 text-[13px] text-white placeholder:text-white/20 focus:border-white/[0.20] focus:outline-none" />
          </div>
        </div>
        <div className="mt-5">
          {state.kind === "loading" && <div className="flex items-center justify-center gap-2 py-10 text-[#9aa0aa]"><Loader2 size={16} className="animate-spin" /> Caricamento…</div>}
          {state.kind === "error" && <p className="py-8 text-center text-[13px] text-red-400">{state.message}</p>}
          {state.kind === "ready" && items.length === 0 && <p className="py-8 text-center text-[13px] text-[#9aa0aa]">{state.items.length === 0 ? "Nessun asset nella Media Library. Carica un'immagine prima di inserirla nel canvas." : "Nessun risultato per la ricerca."}</p>}
          {state.kind === "ready" && items.length > 0 && <div className="grid grid-cols-3 gap-3 sm:grid-cols-4">{items.map((item) => <MediaPickerCard key={item.id} item={item} onPick={onPick} />)}</div>}
        </div>
      </div>
    </div>
  );
}
