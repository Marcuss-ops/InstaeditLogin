/**
 * MediaPickerDialog — Media Library picker for canvas image objects.
 *
 * Lists the workspace's ready media assets (GET /api/v1/media) so the
 * editor can insert an image object referencing a persistent
 * media_id. The asset bytes live in the Media Library (MinIO) — the
 * editor only stores the reference, and the server resolves it on
 * reopen via the media resolver (local blobs are never authoritative).
 */
import { useEffect, useState } from "react";
import { ImageIcon, Loader2, Search } from "lucide-react";
import { authedFetch } from "../../../lib/auth";

export interface MediaPickerItem {
  id: string;
  filename: string;
  content_type: string;
  preview_url?: string;
  width?: number | null;
  height?: number | null;
}

export interface MediaPickerDialogProps {
  onPick: (item: MediaPickerItem) => void;
  onClose: () => void;
}

type PickerState =
  | { kind: "loading" }
  | { kind: "ready"; items: MediaPickerItem[] }
  | { kind: "error"; message: string };

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

  const items =
    state.kind === "ready"
      ? state.items.filter((item) => item.filename.toLowerCase().includes(query.trim().toLowerCase()))
      : [];

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Media Library"
      className="fixed inset-0 z-50 flex items-center justify-center p-4"
    >
      <button
        type="button"
        aria-label="Chiudi"
        onClick={onClose}
        className="absolute inset-0 bg-black/70 backdrop-blur-sm cursor-default"
      />
      <div className="relative max-h-[85vh] w-full max-w-2xl overflow-y-auto rounded-2xl border border-white/[0.12] bg-[#1f1f2e] p-6 shadow-[0_8px_32px_rgba(0,0,0,0.5)]">
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-lg font-bold text-white">Media Library</h2>
          <div className="relative">
            <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-[#9aa0aa]" />
            <input
              type="text"
              aria-label="Cerca nella Media Library"
              placeholder="Cerca…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              className="w-56 pl-9 pr-3 py-2 bg-white/[0.04] border border-white/[0.08] rounded-xl text-[13px] text-white placeholder:text-white/20 focus:outline-none focus:border-white/[0.20]"
            />
          </div>
        </div>

        <div className="mt-5">
          {state.kind === "loading" && (
            <div className="flex items-center justify-center gap-2 py-10 text-[#9aa0aa]">
              <Loader2 size={16} className="animate-spin" /> Caricamento…
            </div>
          )}
          {state.kind === "error" && (
            <p className="py-8 text-center text-[13px] text-red-400">{state.message}</p>
          )}
          {state.kind === "ready" && items.length === 0 && (
            <p className="py-8 text-center text-[13px] text-[#9aa0aa]">
              {state.items.length === 0
                ? "Nessun asset nella Media Library. Carica un'immagine prima di inserirla nel canvas."
                : "Nessun risultato per la ricerca."}
            </p>
          )}
          {state.kind === "ready" && items.length > 0 && (
            <div className="grid grid-cols-3 gap-3 sm:grid-cols-4">
              {items.map((item) => (
                <button
                  key={item.id}
                  type="button"
                  onClick={() => onPick(item)}
                  className="group overflow-hidden rounded-xl border border-white/[0.08] bg-white/[0.02] text-left transition-colors hover:border-white/[0.20]"
                >
                  <div className="aspect-video w-full overflow-hidden bg-black">
                    {item.preview_url ? (
                      <img
                        src={item.preview_url}
                        alt={item.filename}
                        loading="lazy"
                        className="h-full w-full object-cover transition-transform group-hover:scale-105"
                      />
                    ) : (
                      <div className="flex h-full w-full items-center justify-center">
                        <ImageIcon size={22} className="text-white/25" />
                      </div>
                    )}
                  </div>
                  <div className="truncate px-2 py-1.5 text-[11px] text-[#9aa0aa]">
                    {item.filename}
                  </div>
                </button>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
