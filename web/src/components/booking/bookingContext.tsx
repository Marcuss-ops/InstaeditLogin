import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { BookingModal } from "./BookingModal";
import type { BookingIntent } from "../../lib/booking";

/* ----------------------------------------------------------------------------
 * Booking context — exposes `open(intent?)` to every CTA in the app so
 * any component can fan-in to the same modal without prop drilling.
 *
 * Wrapping lives in App.tsx so the modal can be opened from anywhere
 * inside the Router (Nav, Hero, FinalCTA on /, the Mentoring page,
 * EditorContact inside InternalLayout, and the OAuth Login page).
 *
 * The funnel is strictly single-track: every CTA in the app — Nav,
 * Hero, FinalCTA on /, the Mentoring page, EditorContact inside the
 * authenticated shell, and the OAuth Login page — opens the same
 * modal. Conversion is concentrated on the booking CTA so leads can't
 * spike a chat channel with low-intent traffic; the modal still
 * carries the three qualification fields so the qualify→schedule
 * handoff stays warm.
 * -------------------------------------------------------------------------- */

export type BookingCtx = {
  isOpen: boolean;
  intent: BookingIntent;
  open: (intent?: BookingIntent) => void;
  close: () => void;
};

const Ctx = createContext<BookingCtx | null>(null);

export function BookingProvider({ children }: { children: ReactNode }) {
  const [isOpen, setIsOpen] = useState(false);
  // Last intent passed to `open()`. Used by the modal to render a
  // tier-tinted chip ("Tier 2 · Growth"). Default "general" on mount
  // — after open() it always reflects the most recent call.
  const [intent, setIntent] = useState<BookingIntent>("general");
  // Whether <BookingModal> is currently mounted in the DOM tree.
  // We track this separately from `isOpen` so the modal stays
  // mounted during the 280ms opacity fade-out AFTER `isOpen`
  // flips to false, but is COMPLETELY absent from the DOM when
  // the modal has never been opened (initial mount) or after
  // the fade-out has finished. This guarantees the modal's
  // backdrop button can never appear as the topmost
  // hit-test target at any pixel on the page — solving the
  // Playwright actionability failure where the backdrop was
  // intercepting pointer events on the Hero CTA below.
  const [mounted, setMounted] = useState(false);

  const open = useCallback((next: BookingIntent = "general") => {
    setIntent(next);
    setIsOpen(true);
    setMounted(true);
  }, []);
  const close = useCallback(() => setIsOpen(false), []);

  // Unmount after the fade-out completes. Only runs when isOpen
  // toggles to false; on initial mount the modal is already not
  // mounted (mounted=false) so the CTA underneath is fully
  // hit-testable from frame 1.
  useEffect(() => {
    if (isOpen) return;
    const timeout = setTimeout(() => setMounted(false), 280);
    return () => clearTimeout(timeout);
  }, [isOpen]);

  const value = useMemo<BookingCtx>(
    () => ({ isOpen, intent, open, close }),
    [isOpen, intent, open, close],
  );

  return (
    <Ctx.Provider value={value}>
      {children}
      {mounted && (
        <BookingModal intent={intent} isOpen={isOpen} onClose={close} />
      )}
    </Ctx.Provider>
  );
}

export function useBooking(): BookingCtx {
  const ctx = useContext(Ctx);
  if (!ctx) {
    throw new Error("useBooking must be used inside <BookingProvider>");
  }
  return ctx;
}
