import { cn } from "../../lib/utils";

/**
 * Skeleton \u2014 placeholder rendered while data is loading. Visually conveys
 * structure without distracting from the surrounding page. All variants are
 * `aria-hidden` so assistive tech never reads them as content.
 *
 * Variants:
 *
 *   \u2022 `text`      \u2192 single placeholder line; `width` controls simulated length.
 *   \u2022 `circle`    \u2192 round placeholder for avatars/icons; `size` px.
 *   \u2022 `card`      \u2192 rectangular block for a tile/panel; `height` px.
 *   \u2022 `list-row`  \u2192 composed avatar + 2 text lines; what we use for
 *                     a row in Posts / a tile in the Connections grid.
 */
export type SkeletonVariant = "text" | "circle" | "card" | "list-row";

export type SkeletonProps =
  | { variant: "text"; width?: string; className?: string }
  | { variant: "circle"; size?: number; className?: string }
  | { variant: "card"; height?: number; width?: string; className?: string }
  | { variant: "list-row"; gap?: number; className?: string };

const BASE_PULSE = "animate-pulse bg-neutral-200";

export function Skeleton(props: SkeletonProps) {
  if (props.variant === "text") {
    return (
      <div
        aria-hidden="true"
        className={cn(BASE_PULSE, "h-3 rounded", props.className)}
        style={{ width: props.width ?? "100%" }}
      />
    );
  }

  if (props.variant === "circle") {
    const size = props.size ?? 40;
    return (
      <div
        aria-hidden="true"
        className={cn(BASE_PULSE, "rounded-full shrink-0", props.className)}
        style={{ width: `${size}px`, height: `${size}px` }}
      />
    );
  }

  if (props.variant === "card") {
    // Width is set via inline `style` (default "100%") so it ALWAYS wins
    // over any Tailwind `w-*` class in `className`. Callers wanting a
    // non-full-width skeleton must use the `width` prop (e.g.
    // `width="60%"`); passing `className="w-1/2"` will silently lose to
    // the inline `width: 100%`.
    return (
      <div
        aria-hidden="true"
        className={cn(BASE_PULSE, "rounded-xl", props.className)}
        style={{
          height: `${props.height ?? 120}px`,
          width: props.width ?? "100%",
        }}
      />
    );
  }

  // list-row: avatar + two text lines.
  return (
    <div
      role="presentation"
      className={cn("flex items-center", props.className)}
      style={{ gap: `${props.gap ?? 12}px` }}
    >
      <Skeleton variant="circle" size={40} />
      <div className="flex-1 space-y-2">
        <Skeleton variant="text" width="40%" />
        <Skeleton variant="text" width="80%" />
      </div>
    </div>
  );
}
