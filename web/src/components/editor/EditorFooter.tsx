import { Link } from "react-router-dom";
import {
  Zap
} from "lucide-react";
import {
  PLATFORM_REGISTRY
} from "./shared";

export function EditorFooter() {
  return (
    <footer className="relative border-t border-white/10 bg-[#08080d]">
      <div className="mx-auto max-w-7xl px-6 py-16 grid gap-12 lg:grid-cols-12">
        <div className="lg:col-span-5">
          <Link to="/" className="flex items-center gap-2">
            <span className="inline-flex w-8 h-8 items-center justify-center rounded-lg bg-white text-black">
              <Zap className="w-4 h-4" />
            </span>
            <span className="font-bold tracking-tight text-white text-base">
              InstaEdit
            </span>
          </Link>
          <p className="text-sm text-zinc-400 mt-4 max-w-[42ch] leading-relaxed">
            The InstaEdit Editor turns raw video into seven native
            posts — captions, chapters, and thumbnails per platform.
          </p>
          <div className="flex items-center gap-2 mt-5">
            {PLATFORM_REGISTRY.map(({ key, name, Logo }) => (
              <span
                key={key}
                className="inline-flex w-7 h-7 rounded-md overflow-hidden ring-1 ring-white/10 surface-glass"
                title={name}
                aria-label={name}
              >
                <Logo className="w-full h-full" />
              </span>
            ))}
          </div>
        </div>

        <div className="lg:col-span-7 grid grid-cols-1 sm:grid-cols-2 gap-8">
          {[
            {
              heading: "Product",
              links: [
                { l: "Editor", to: "/editor" },
                { l: "Home", to: "/" },
                { l: "Sign in", to: "/login" },
                { l: "Privacy", to: "/privacy" },
              ],
            },
            {
              heading: "Legal",
              links: [
                { l: "Privacy", to: "/privacy" },
                { l: "Terms", to: "/terms" },
                { l: "Data deletion", href: "/data-deletion.html" },
              ],
            },
          ].map((col) => (
            <div key={col.heading}>
              <div className="text-eyebrow text-zinc-500 mb-4">
                {col.heading}
              </div>
              <ul className="space-y-3">
                {col.links.map((link) => (
                  <li key={link.l}>
                    {"to" in link ? (
                      <Link
                        to={link.to as string}
                        className="text-sm text-zinc-300 hover:text-white transition-colors"
                      >
                        {link.l}
                      </Link>
                    ) : (
                      <a
                        href={link.href}
                        className="text-sm text-zinc-300 hover:text-white transition-colors"
                      >
                        {link.l}
                      </a>
                    )}
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      </div>
      <div className="border-t border-white/5">
        <div className="mx-auto max-w-7xl px-6 py-6 flex flex-col sm:flex-row items-center justify-between gap-3 text-xs text-zinc-500">
          <div>© {new Date().getFullYear()} InstaEdit, Inc.</div>
          <div>From Raw idea. To every channel. In minutes.</div>
        </div>
      </div>
    </footer>
  );
}
