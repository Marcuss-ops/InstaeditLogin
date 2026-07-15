import { useEffect, type RefObject } from "react";

/**
 * useClickOutside — close a popover/dropdown when the user clicks or taps
 * outside the referenced element, or presses the Escape key.
 *
 * @param ref - ref to the element that should stay open while the user interacts inside it
 * @param handler - called when an outside click or Escape is detected
 * @param enabled - when false, listeners are not attached (useful for conditionally open UI)
 */
export function useClickOutside<T extends HTMLElement>(
  ref: RefObject<T | null>,
  handler: () => void,
  enabled: boolean = true,
) {
  useEffect(() => {
    if (!enabled) return;

    const handleInteract = (event: MouseEvent | TouchEvent) => {
      const target = event.target as Node | null;
      if (!target) return;
      if (ref.current && !ref.current.contains(target)) {
        handler();
      }
    };

    const handleKeydown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        handler();
      }
    };

    document.addEventListener("mousedown", handleInteract);
    document.addEventListener("touchstart", handleInteract);
    document.addEventListener("keydown", handleKeydown);

    return () => {
      document.removeEventListener("mousedown", handleInteract);
      document.removeEventListener("touchstart", handleInteract);
      document.removeEventListener("keydown", handleKeydown);
    };
  }, [ref, handler, enabled]);
}
