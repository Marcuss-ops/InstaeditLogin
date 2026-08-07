/**
 * CanvasSettingsPanel — the Cover editor canvas background controls.
 *
 * Fixed-size note plus color picker + hex field for the canvas
 * background (same input pair pattern as the Inspector fill picker).
 */
interface CanvasSettingsPanelProps {
  background: string;
  canvasWidth?: number;
  canvasHeight?: number;
  onChange: (background: string) => void;
}

export function CanvasSettingsPanel({
  background,
  canvasWidth,
  canvasHeight,
  onChange,
}: CanvasSettingsPanelProps) {
  return (
    <div className="rounded-2xl border border-white/[0.08] bg-[#1a1a28] p-4">
      <h2 className="text-[13px] font-bold text-white">Canvas</h2>
      <div className="mt-3 space-y-3">
        <div>
          <span className="text-[11px] font-semibold text-[#9aa0aa]">Sfondo</span>
          <div className="mt-1 flex items-center gap-2">
            <input
              type="color"
              aria-label="Sfondo canvas"
              value={background.startsWith("#") ? background : "#000000"}
              onChange={(e) => onChange(e.target.value)}
              className="h-8 w-10 rounded-lg border border-white/[0.08] bg-white/[0.04] cursor-pointer"
            />
            <input
              type="text"
              aria-label="Sfondo esadecimale"
              value={background}
              onChange={(e) => onChange(e.target.value)}
              className="flex-1 px-2.5 py-1.5 bg-white/[0.04] border border-white/[0.08] rounded-lg text-[12px] text-white font-mono focus:outline-none focus:border-white/[0.20]"
            />
          </div>
        </div>
        <p className="text-[11px] text-[#9aa0aa]">
          Dimensione fissa {canvasWidth}×{canvasHeight} — cambia dal
          progetto o crea una nuova copertina.
        </p>
      </div>
    </div>
  );
}
