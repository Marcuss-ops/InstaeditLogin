import { useState, useCallback, useEffect, useRef } from "react";
import { Link } from "react-router-dom";
import { Zap, Menu, X } from "lucide-react";

/* ----------------------------------------------------------------------------
 * Sticky Nav with mobile hamburger + focus trap
 * -------------------------------------------------------------------------- */

export function Nav() {
  const [open, setOpen] = useState(false);
  const drawerRef = useRef<HTMLDivElement>(null);
  const lastFocusedElement = useRef<HTMLElement | null>(null);
  const drawerId = "mobile-nav-drawer";

  // Close on escape
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") setOpen(false);
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  // Lock body scroll and trap focus when mobile menu is open
  useEffect(() => {
    if (open) {
      document.body.style.overflow = "hidden";
      lastFocusedElement.current = document.activeElement as HTMLElement;
      // Focus the first focusable element in the drawer after it mounts
      setTimeout(() => {
        const first = drawerRef.current?.querySelector<HTMLElement>(
          "a[href], button, [tabindex='0']",
        );
        first?.focus();
      }, 0);
    } else {
      document.body.style.overflow = "";
      lastFocusedElement.current?.focus();
      lastFocusedElement.current = null;
    }
    return () => {
      document.body.style.overflow = "";
    };
  }, [open]);

  // Focus trap for Tab/Shift+Tab
  useEffect(() => {
    function handleTab(e: KeyboardEvent) {
      if (!open || !drawerRef.current) return;
      if (e.key !== "Tab") return;

      const focusables = Array.from(
        drawerRef.current.querySelectorAll<HTMLElement>(
          "a[href], button, [tabindex='0']",
        ),
      ).filter((el) => !el.hasAttribute("disabled") && el.tabIndex >= 0);

      if (focusables.length === 0) return;

      const first = focusables[0];
      const last = focusables[focusables.length - 1];
      const active = document.activeElement as HTMLElement;

      if (e.shiftKey) {
        if (active === first || !drawerRef.current.contains(active)) {
          e.preventDefault();
          last.focus();
        }
      } else {
        if (active === last || !drawerRef.current.contains(active)) {
          e.preventDefault();
          first.focus();
        }
      }
    }

    document.addEventListener("keydown", handleTab);
    return () => document.removeEventListener("keydown", handleTab);
  }, [open]);

  const links: Array<{ label: string; to?: string; href?: string }> = [
    { label: "How it works", href: "#features" },
    { label: "Programs", href: "#programs" },
    { label: "Results", href: "#results" },
    { label: "FAQ", href: "#faq" },
    { label: "Contact", href: "#contact" },
  ];

  const close = useCallback(() => setOpen(false), []);

  return (
    <nav className="fixed top-0 left-0 right-0 z-50">
      <div className="surface-glass border-b border-white/10">
        <div className="mx-auto max-w-7xl h-16 px-6 flex items-center justify-between">
          <Link to="/" className="flex items-center gap-2 group" onClick={close}>
            <span className="inline-flex w-7 h-7 items-center justify-center rounded-md bg-white text-black shadow-[0_0_24px_-6px_rgba(255,255,255,0.4)] group-hover:shadow-[0_0_32px_-4px_rgba(255,255,255,0.6)] transition-shadow">
              <Zap className="w-4 h-4" />
            </span>
            <span className="font-bold tracking-tight text-white text-sm">InstaEdit</span>
          </Link>

          {/* Desktop nav */}
          <div className="hidden md:flex items-center gap-7">
            <div className="flex items-center gap-7 text-sm font-medium text-zinc-400">
              {links.map((l) =>
                l.to ? (
                  <Link
                    key={l.label}
                    to={l.to}
                    className="hover:text-white transition-colors relative after:absolute after:bottom-[-2px] after:left-0 after:h-[2px] after:w-0 after:bg-gradient-to-r after:from-violet-400 after:to-cyan-400 after:transition-all after:duration-300 hover:after:w-full"
                  >
                    {l.label}
                  </Link>
                ) : (
                  <a
                    key={l.label}
                    href={l.href}
                    className="hover:text-white transition-colors relative after:absolute after:bottom-[-2px] after:left-0 after:h-[2px] after:w-0 after:bg-gradient-to-r after:from-violet-400 after:to-cyan-400 after:transition-all after:duration-300 hover:after:w-full"
                  >
                    {l.label}
                  </a>
                ),
              )}
            </div>
            <a
              href="https://discord.com/users/1201477873719050332"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 px-5 py-2 rounded-lg bg-white text-black text-sm font-semibold hover:bg-white/90 transition-all"
            >
              Start now
            </a>
          </div>

          {/* Mobile hamburger */}
          <button
            type="button"
            onClick={() => setOpen(!open)}
            className="md:hidden p-2 text-zinc-400 hover:text-white transition-colors"
            aria-label={open ? "Close menu" : "Open menu"}
            aria-expanded={open}
            aria-controls={drawerId}
          >
            {open ? <X className="w-5 h-5" /> : <Menu className="w-5 h-5" />}
          </button>
        </div>

        {/* Mobile drawer — accessible dialog with focus trap */}
        {open && (
          <div
            id={drawerId}
            ref={drawerRef}
            className="md:hidden border-t border-white/10 bg-[#14141c]/98 backdrop-blur-xl"
            role="dialog"
            aria-modal="true"
            aria-label="Navigation menu"
          >
            <div className="px-6 py-4 space-y-1">
              {links.map((l) =>
                l.to ? (
                  <Link
                    key={l.label}
                    to={l.to}
                    onClick={close}
                    className="block py-3 text-sm font-medium text-zinc-300 hover:text-white hover:bg-white/[0.04] rounded-lg px-3 -mx-3 transition-colors"
                  >
                    {l.label}
                  </Link>
                ) : (
                  <a
                    key={l.label}
                    href={l.href}
                    onClick={close}
                    className="block py-3 text-sm font-medium text-zinc-300 hover:text-white hover:bg-white/[0.04] rounded-lg px-3 -mx-3 transition-colors"
                  >
                    {l.label}
                  </a>
                ),
              )}
              <hr className="border-white/10 my-3" />
              <a
                href="https://discord.com/users/1201477873719050332"
                target="_blank"
                rel="noopener noreferrer"
                onClick={close}
                className="block py-3 text-sm font-semibold text-center text-white bg-gradient-to-r from-violet-500 to-cyan-500 rounded-xl hover:opacity-90 transition-opacity"
              >
                Start now
              </a>
            </div>
          </div>
        )}
      </div>
    </nav>
  );
}
