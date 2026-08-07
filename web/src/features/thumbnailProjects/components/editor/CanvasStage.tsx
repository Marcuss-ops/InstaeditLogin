/**
 * CanvasStage — the interactive Cover editor canvas surface.
 *
 * Renders every snapshot object with the renderer's transform math
 * (box scaled to width·scale_x × height·scale_y, placed at (x,y),
 * rotated around its center) so editing preview ≈ exported pixels.
 * Supports pointer drag to move objects and click-on-empty to deselect.
 */
import { useEffect, useRef, useState } from "react";
import { ImageIcon } from "lucide-react";
import { cn } from "../../../../lib/utils";
import type { EditorSnapshot } from "../../editor/snapshot";
import type { ThumbnailSnapshotObject } from "../../types";

function round(value: number): number {
  return Math.round(value * 100) / 100;
}

/** Mirror the renderer: box = width·scale_x × height·scale_y at (x,y),
 *  rotated around its center (composite() in thumbnailrender/render.go). */
function objectBox(obj: ThumbnailSnapshotObject): { width: number; height: number } {
  return {
    width: (obj.width ?? 0) * (obj.scale_x ?? 1),
    height: (obj.height ?? 0) * (obj.scale_y ?? 1),
  };
}

interface CanvasStageProps {
  canvas: EditorSnapshot["canvas"];
  objects: ThumbnailSnapshotObject[];
  selectedId: string | null;
  mediaUrls: Map<string, string>;
  onSelect: (id: string | null) => void;
  onMove: (id: string, x: number, y: number) => void;
}

export function CanvasStage({
  canvas,
  objects,
  selectedId,
  mediaUrls,
  onSelect,
  onMove,
}: CanvasStageProps) {
  const wrapperRef = useRef<HTMLDivElement>(null);
  const [stageWidth, setStageWidth] = useState(0);
  const dragRef = useRef<{
    id: string;
    startX: number;
    startY: number;
    origX: number;
    origY: number;
  } | null>(null);

  useEffect(() => {
    const node = wrapperRef.current;
    if (!node) return;
    const observer = new ResizeObserver((entries) => {
      const width = entries[0]?.contentRect.width;
      if (width) setStageWidth(width);
    });
    observer.observe(node);
    return () => observer.disconnect();
  }, []);

  const scale = stageWidth > 0 ? stageWidth / canvas.width : 0;

  const beginDrag = (e: React.PointerEvent, obj: ThumbnailSnapshotObject) => {
    if (e.button !== 0) return;
    e.stopPropagation();
    onSelect(obj.id);
    dragRef.current = {
      id: obj.id,
      startX: e.clientX,
      startY: e.clientY,
      origX: obj.x ?? 0,
      origY: obj.y ?? 0,
    };
    e.currentTarget.setPointerCapture(e.pointerId);
  };

  const moveDrag = (e: React.PointerEvent) => {
    if (!dragRef.current || scale <= 0) return;
    const dx = (e.clientX - dragRef.current.startX) / scale;
    const dy = (e.clientY - dragRef.current.startY) / scale;
    onMove(
      dragRef.current.id,
      round(dragRef.current.origX + dx),
      round(dragRef.current.origY + dy),
    );
  };

  const endDrag = () => {
    dragRef.current = null;
  };

  const renderObject = (obj: ThumbnailSnapshotObject) => {
    const box = objectBox(obj);
    const selected = obj.id === selectedId;
    const hidden = obj.visible === false;
    const style: React.CSSProperties = {
      position: "absolute",
      left: obj.x ?? 0,
      top: obj.y ?? 0,
      width: box.width,
      height: box.height,
      transform: `rotate(${obj.rotation ?? 0}deg)`,
      transformOrigin: "center",
      opacity: hidden ? 0.25 : 1,
      touchAction: "none",
      cursor: "move",
    };

    const content = (() => {
      switch (obj.type) {
        case "rect":
          return (
            <div
              className="h-full w-full"
              style={{ backgroundColor: obj.fill ?? "#000000", borderRadius: obj.radius ?? 0 }}
            />
          );
        case "text":
          return (
            <div
              className="h-full w-full overflow-hidden whitespace-pre-wrap"
              style={{
                color: obj.fill ?? "#ffffff",
                fontFamily: obj.font_family ?? "Inter",
                fontSize: obj.font_size ?? 48,
                fontWeight: obj.font_weight ?? 400,
                textAlign: (obj.text_align as React.CSSProperties["textAlign"]) ?? "left",
                lineHeight: 1.1,
              }}
            >
              {obj.text ?? ""}
            </div>
          );
        case "image": {
          const url = obj.media_id ? mediaUrls.get(obj.media_id) : undefined;
          return url ? (
            <img src={url} alt="" draggable={false} className="h-full w-full object-fill" />
          ) : (
            <div className="flex h-full w-full items-center justify-center bg-white/[0.06]">
              <ImageIcon size={20} className="text-white/30" />
            </div>
          );
        }
        default:
          return (
            <div className="flex h-full w-full items-center justify-center border border-dashed border-white/30 text-[11px] text-white/50">
              {obj.type}
            </div>
          );
      }
    })();

    return (
      <div
        key={obj.id}
        data-testid="canvas-object"
        data-object-type={obj.type}
        data-selected={selected || undefined}
        onPointerDown={(e) => beginDrag(e, obj)}
        onPointerMove={moveDrag}
        onPointerUp={endDrag}
        onPointerCancel={endDrag}
        className={cn(
          "rounded-[2px]",
          selected &&
            "outline outline-2 outline-sky-400 outline-offset-2 ring-2 ring-sky-400/30",
          !selected && "hover:outline hover:outline-1 hover:outline-white/40",
        )}
        style={style}
        title={`${obj.type} — ${obj.id}`}
      >
        {content}
      </div>
    );
  };

  return (
    <div
      ref={wrapperRef}
      data-testid="canvas-stage"
      className="w-full overflow-hidden rounded-2xl border border-white/[0.10] bg-[#0b0b12]"
      style={{ height: scale > 0 ? canvas.height * scale : undefined }}
      onPointerDown={(e) => {
        // Clicking empty canvas (the scaled surface, not an object)
        // clears the selection. `closest` handles both the stage and the
        // surface child so the visible canvas area deselects correctly.
        if (!(e.target as HTMLElement).closest('[data-testid="canvas-object"]')) {
          onSelect(null);
        }
      }}
    >
      {scale > 0 && (
        <div
          data-testid="canvas-surface"
          style={{
            width: canvas.width,
            height: canvas.height,
            transform: `scale(${scale})`,
            transformOrigin: "top left",
            backgroundColor: canvas.background,
          }}
        >
          {objects.map(renderObject)}
        </div>
      )}
    </div>
  );
}
