/**
 * Inspector — property editor for the selected Cover canvas object.
 *
 * Shows type-specific controls (position/size/scale/rotation for every
 * object, color + radius for rects, typography for text, replace-image
 * for images) plus duplicate/delete and visibility toggles.
 */
import { Copy, Eye, ImageIcon, Trash2 } from "lucide-react";
import type { ThumbnailSnapshotObject } from "../../types";

interface InspectorProps {
  object: ThumbnailSnapshotObject;
  onPatch: (patch: Partial<ThumbnailSnapshotObject>) => void;
  onDuplicate: () => void;
  onDelete: () => void;
  onReplaceImage: () => void;
}

export function Inspector({
  object,
  onPatch,
  onDuplicate,
  onDelete,
  onReplaceImage,
}: InspectorProps) {
  const numberInput = (
    key: keyof ThumbnailSnapshotObject,
    label: string,
    step = 1,
  ) => (
    <label className="block">
      <span className="text-[11px] font-semibold text-[#9aa0aa]">{label}</span>
      <input
        type="number"
        step={step}
        value={Number(object[key] ?? 0)}
        onChange={(e) => {
          const value = Number(e.target.value);
          if (Number.isFinite(value)) onPatch({ [key]: value } as Partial<ThumbnailSnapshotObject>);
        }}
        className="mt-1 w-full px-2.5 py-1.5 bg-white/[0.04] border border-white/[0.08] rounded-lg text-[13px] text-white focus:outline-none focus:border-white/[0.20]"
      />
    </label>
  );

  return (
    <div className="rounded-2xl border border-white/[0.08] bg-[#1a1a28] p-4">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-[13px] font-bold text-white capitalize">
          {object.type === "image" ? "Immagine" : object.type === "text" ? "Testo" : "Rettangolo"}
        </h2>
        <div className="flex items-center gap-1">
          <button
            type="button"
            aria-label="Duplica oggetto"
            onClick={onDuplicate}
            className="rounded-md p-1.5 text-[#9aa0aa] hover:text-white hover:bg-white/[0.06] transition-colors"
          >
            <Copy size={14} />
          </button>
          <button
            type="button"
            aria-label="Elimina oggetto"
            onClick={onDelete}
            className="rounded-md p-1.5 text-[#9aa0aa] hover:text-red-400 hover:bg-red-500/[0.08] transition-colors"
          >
            <Trash2 size={14} />
          </button>
        </div>
      </div>

      <div className="mt-3 space-y-3">
        <div className="grid grid-cols-2 gap-2">
          {numberInput("x", "X")}
          {numberInput("y", "Y")}
          {numberInput("width", "Larghezza")}
          {numberInput("height", "Altezza")}
          {numberInput("scale_x", "Scala X", 0.1)}
          {numberInput("scale_y", "Scala Y", 0.1)}
          {numberInput("rotation", "Rotazione °")}
        </div>

        {(object.type === "text" || object.type === "rect") && (
          <div>
            <span className="text-[11px] font-semibold text-[#9aa0aa]">Colore</span>
            <div className="mt-1 flex items-center gap-2">
              <input
                type="color"
                aria-label="Colore oggetto"
                value={typeof object.fill === "string" && object.fill.startsWith("#") ? object.fill : "#000000"}
                onChange={(e) => onPatch({ fill: e.target.value })}
                className="h-8 w-10 rounded-lg border border-white/[0.08] bg-white/[0.04] cursor-pointer"
              />
              <input
                type="text"
                aria-label="Colore esadecimale"
                value={object.fill ?? ""}
                onChange={(e) => onPatch({ fill: e.target.value })}
                className="flex-1 px-2.5 py-1.5 bg-white/[0.04] border border-white/[0.08] rounded-lg text-[12px] text-white font-mono focus:outline-none focus:border-white/[0.20]"
              />
            </div>
          </div>
        )}

        {object.type === "rect" && (
          <label className="block">
            <span className="text-[11px] font-semibold text-[#9aa0aa]">Angolo arrotondato</span>
            <input
              type="number"
              min={0}
              value={object.radius ?? 0}
              onChange={(e) => {
                const value = Number(e.target.value);
                if (Number.isFinite(value) && value >= 0) onPatch({ radius: value });
              }}
              className="mt-1 w-full px-2.5 py-1.5 bg-white/[0.04] border border-white/[0.08] rounded-lg text-[13px] text-white focus:outline-none focus:border-white/[0.20]"
            />
          </label>
        )}

        {object.type === "text" && (
          <>
            <label className="block">
              <span className="text-[11px] font-semibold text-[#9aa0aa]">Testo</span>
              <textarea
                rows={3}
                value={object.text ?? ""}
                onChange={(e) => onPatch({ text: e.target.value })}
                className="mt-1 w-full px-2.5 py-1.5 bg-white/[0.04] border border-white/[0.08] rounded-lg text-[13px] text-white resize-y focus:outline-none focus:border-white/[0.20]"
              />
            </label>
            <div className="grid grid-cols-2 gap-2">
              {numberInput("font_size", "Dimensione testo")}
              <label className="block">
                <span className="text-[11px] font-semibold text-[#9aa0aa]">Peso</span>
                <select
                  value={Number(object.font_weight ?? 400)}
                  onChange={(e) => onPatch({ font_weight: Number(e.target.value) })}
                  className="mt-1 w-full px-2 py-1.5 bg-white/[0.04] border border-white/[0.08] rounded-lg text-[13px] text-white focus:outline-none"
                >
                  {[400, 500, 600, 700, 800, 900].map((w) => (
                    <option key={w} value={w} className="bg-[#1f1f2e]">
                      {w}
                    </option>
                  ))}
                </select>
              </label>
            </div>
          </>
        )}

        {object.type === "image" && (
          <button
            type="button"
            onClick={onReplaceImage}
            className="flex w-full items-center justify-center gap-1.5 rounded-lg border border-white/[0.10] bg-white/[0.04] px-3 py-2 text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors"
          >
            <ImageIcon size={14} />
            Sostituisci immagine
          </button>
        )}

        <label className="flex items-center gap-2 text-[13px] text-[#9aa0aa]">
          <input
            type="checkbox"
            checked={object.visible !== false}
            onChange={(e) => onPatch({ visible: e.target.checked })}
            className="accent-white"
          />
          <Eye size={13} />
          Visibile
        </label>
      </div>
    </div>
  );
}
