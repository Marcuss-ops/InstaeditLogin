/**
 * LayersPanel — the Cover editor layer stack.
 *
 * Renders the snapshot objects top-first with per-row bring-forward /
 * send-backward controls. Render order = array order, last = top.
 */
import { ChevronDown, ChevronUp, EyeOff, Layers } from "lucide-react";
import { cn } from "../../../../lib/utils";
import type { ThumbnailSnapshotObject } from "../../types";

interface LayersPanelProps {
  objects: ThumbnailSnapshotObject[];
  selectedId: string | null;
  onSelect: (id: string) => void;
  onReorder: (id: string, direction: -1 | 1) => void;
}

export function LayersPanel({ objects, selectedId, onSelect, onReorder }: LayersPanelProps) {
  // Render order = array order, last = top; show top-first.
  const ordered = [...objects].reverse();
  return (
    <div className="rounded-2xl border border-white/[0.08] bg-[#1a1a28] p-4">
      <h2 className="flex items-center gap-2 text-[13px] font-bold text-white">
        <Layers size={14} className="text-white/40" />
        Livelli
        <span className="text-[11px] font-medium text-[#9aa0aa]">{objects.length}</span>
      </h2>
      {objects.length === 0 ? (
        <p className="mt-3 text-[12px] text-[#9aa0aa]">
          Canvas vuoto — aggiungi un oggetto dalla barra degli strumenti.
        </p>
      ) : (
        <ul className="mt-3 space-y-1.5" data-testid="layers-list">
          {ordered.map((obj, index) => {
            const topIndex = objects.length - 1 - index;
            return (
              <li
                key={obj.id}
                data-testid="layer-row"
                className={cn(
                  "flex items-center gap-2 rounded-lg border px-2.5 py-2 transition-colors",
                  selectedId === obj.id
                    ? "border-sky-400/30 bg-sky-500/[0.08]"
                    : "border-white/[0.06] bg-white/[0.02] hover:bg-white/[0.04]",
                )}
              >
                <button
                  type="button"
                  onClick={() => onSelect(obj.id)}
                  className="flex min-w-0 flex-1 items-center gap-2 text-left"
                >
                  <span className="text-[11px] font-bold text-[#9aa0aa]">{topIndex + 1}</span>
                  <span className="truncate text-[13px] font-medium text-white">
                    {obj.type === "text"
                      ? (obj.text ?? "Testo").slice(0, 24)
                      : obj.type === "image"
                        ? `Immagine (${obj.media_id?.slice(0, 8)}…)`
                        : "Rettangolo"}
                  </span>
                  {obj.visible === false && <EyeOff size={12} className="shrink-0 text-[#9aa0aa]" />}
                </button>
                <div className="flex shrink-0 items-center gap-0.5">
                  <button
                    type="button"
                    aria-label="Porta avanti"
                    disabled={topIndex >= objects.length - 1}
                    onClick={() => onReorder(obj.id, 1)}
                    className="rounded-md p-1 text-[#9aa0aa] hover:text-white hover:bg-white/[0.06] disabled:opacity-30"
                  >
                    <ChevronUp size={14} />
                  </button>
                  <button
                    type="button"
                    aria-label="Porta indietro"
                    disabled={topIndex <= 0}
                    onClick={() => onReorder(obj.id, -1)}
                    className="rounded-md p-1 text-[#9aa0aa] hover:text-white hover:bg-white/[0.06] disabled:opacity-30"
                  >
                    <ChevronDown size={14} />
                  </button>
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
