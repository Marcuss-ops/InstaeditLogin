/**
 * EditorToolbar — Cover editor add-object buttons.
 *
 * Testo / Rettangolo / Immagine insert a new layer (via the page's
 * object factories / media picker) plus the drag hint.
 */
import { ImageIcon, Square, Type } from "lucide-react";

interface EditorToolbarProps {
  onAddText: () => void;
  onAddRect: () => void;
  onOpenMediaPicker: () => void;
}

export function EditorToolbar({ onAddText, onAddRect, onOpenMediaPicker }: EditorToolbarProps) {
  return (
    <div className="mt-5 flex flex-wrap items-center gap-2">
      <button
        type="button"
        onClick={onAddText}
        className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.10] bg-white/[0.04] px-3 py-2 text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors"
      >
        <Type size={14} /> Testo
      </button>
      <button
        type="button"
        onClick={onAddRect}
        className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.10] bg-white/[0.04] px-3 py-2 text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors"
      >
        <Square size={14} /> Rettangolo
      </button>
      <button
        type="button"
        onClick={onOpenMediaPicker}
        className="inline-flex items-center gap-1.5 rounded-lg border border-white/[0.10] bg-white/[0.04] px-3 py-2 text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors"
      >
        <ImageIcon size={14} /> Immagine
      </button>
      <div className="ml-auto text-[12px] text-[#9aa0aa]">
        Trascina gli oggetti per spostarli · seleziona per modificarli
      </div>
    </div>
  );
}
