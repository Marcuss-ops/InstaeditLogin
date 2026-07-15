import { useEffect, useRef, useState, type ReactNode } from "react";
import { cn } from "../lib/utils";

export type ScrollRevealProps = {
  children: ReactNode;
  className?: string;
  delay?: number;
  once?: boolean;
};

/**
 * ScrollReveal — animates children into view when they scroll into the viewport.
 *
 * Uses IntersectionObserver so we don't need a heavy animation library.
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

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) {
          setVisible(true);
          if (once) observer.unobserve(el);
        } else if (!once) {
          setVisible(false);
        }
      },
      { threshold: 0.1, rootMargin: "0px 0px -50px 0px" },
    );

    observer.observe(el);
    return () => observer.disconnect();
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
