import { useRef, useState, type DragEvent, type ReactNode } from "react";
import { ImagePlus, Loader2 } from "lucide-react";
import { cn } from "../../lib/utils";

export function ThumbnailDropTarget({
  onFile,
  busy,
  children,
  className,
}: {
  onFile: (file: File) => void;
  busy?: boolean;
  children: ReactNode;
  className?: string;
}) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [dragging, setDragging] = useState(false);
  const acceptFile = (file?: File) => {
    if (file) onFile(file);
  };
  const onDrop = (event: DragEvent) => {
    event.preventDefault();
    setDragging(false);
    acceptFile(event.dataTransfer.files[0]);
  };
  return (
    <div
      onDragEnter={(event) => { event.preventDefault(); setDragging(true); }}
      onDragOver={(event) => event.preventDefault()}
      onDragLeave={(event) => { if (event.currentTarget === event.target) setDragging(false); }}
      onDrop={onDrop}
      className={cn("relative", dragging && "ring-2 ring-violet-400 ring-offset-2 ring-offset-[#06070b]", className)}
    >
      {children}
      <span
        role="button"
        tabIndex={0}
        onClick={(event) => { event.stopPropagation(); if (!busy) inputRef.current?.click(); }}
        onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); inputRef.current?.click(); } }}
        aria-disabled={busy}
        className="absolute bottom-3 right-3 inline-flex items-center gap-1.5 rounded-lg border border-white/20 bg-black/70 px-2.5 py-1.5 text-[10px] font-bold text-white opacity-0 transition-opacity hover:bg-black/90 group-hover:opacity-100 disabled:opacity-60"
        title="Carica copertina JPG o PNG"
      >
        {busy ? <Loader2 size={12} className="animate-spin" /> : <ImagePlus size={12} />}
        {busy ? "Salvataggio…" : "Carica copertina"}
      </span>
      <input ref={inputRef} type="file" accept="image/jpeg,image/png" className="hidden" onClick={(event) => event.stopPropagation()} onChange={(event) => { acceptFile(event.target.files?.[0]); event.currentTarget.value = ""; }} />
    </div>
  );
}
