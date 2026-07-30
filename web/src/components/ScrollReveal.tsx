import { useEffect, useRef, useState, type ReactNode } from "react";
import { cn } from "../lib/utils";

export type ScrollRevealProps = {
  children: ReactNode;
  className?: string;
  delay?: number;
  once?: boolean;
};

type RevealCallback = (entry: IntersectionObserverEntry) => void;

/**
 * Shared singleton IntersectionObserver used by all ScrollReveal instances.
 * Creating one observer per component is wasteful on pages with many reveals.
 */
let sharedObserver: IntersectionObserver | null = null;
const observedElements = new Map<Element, RevealCallback>();

function getSharedObserver(): IntersectionObserver | null {
  if (typeof window === "undefined") return null;
  if (!sharedObserver) {
    sharedObserver = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          const cb = observedElements.get(entry.target);
          if (cb) cb(entry);
        });
      },
      { threshold: 0.1, rootMargin: "0px 0px -50px 0px" },
    );
  }
  return sharedObserver;
}

/**
 * ScrollReveal — animates children into view when they scroll into the viewport.
 *
 * Uses a shared IntersectionObserver so we don't need a heavy animation
 * library and don't create many observers on pages with lots of reveals.
 * The element starts with reduced opacity and a slight upward offset, then
 * fades/slides in once it enters the viewport. The animation is one-way
 * by default (once=true) so content stays visible after the first reveal.
 *
 * The wrapper renders its children immediately in the DOM; only opacity and
 * transform change, which keeps Testing Library queries working without
 * waiting for the animation.
 */
export function ScrollReveal({
  children,
  className,
  delay = 0,
  once = true,
}: ScrollRevealProps) {
  const ref = useRef<HTMLDivElement>(null);
  const [visible, setVisible] = useState(false);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;

    const observer = getSharedObserver();
    if (!observer) return;

    const callback: RevealCallback = (entry) => {
      if (entry.isIntersecting) {
        setVisible(true);
        if (once) {
          observer.unobserve(el);
          observedElements.delete(el);
        }
      } else if (!once) {
        setVisible(false);
      }
    };

    observedElements.set(el, callback);
    observer.observe(el);

    return () => {
      observer.unobserve(el);
      observedElements.delete(el);
    };
  }, [once]);

  return (
    <div
      ref={ref}
      className={cn(
        "transition-all duration-700 ease-out",
        visible ? "opacity-100 translate-y-0" : "opacity-0 translate-y-5",
        className,
      )}
      style={{ transitionDelay: `${delay}ms` }}
    >
      {children}
    </div>
  );
}
