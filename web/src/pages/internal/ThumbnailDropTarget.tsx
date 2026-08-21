import { useState, type DragEvent, type ReactNode } from "react";
import { cn } from "../../lib/utils";

export function ThumbnailDropTarget({
  onFile,
  children,
  className,
}: {
  onFile: (file: File) => void;
  children: ReactNode;
  className?: string;
}) {
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
    </div>
  );
}
