import { Link } from "react-router-dom";
import {
  ArrowRight, Zap, ChevronRight
} from "lucide-react";

export function EditorNav() {
  return (
    <nav className="fixed top-0 left-0 right-0 z-50">
      <div className="surface-glass border-b border-white/10">
        <div className="mx-auto max-w-7xl h-16 px-6 flex items-center justify-between">
          <Link to="/" className="flex items-center gap-2 group" aria-label="Back to InstaEdit">
            <span className="inline-flex w-7 h-7 items-center justify-center rounded-md bg-white text-black shadow-[0_0_24px_-6px_rgba(255,255,255,0.4)]">
              <Zap className="w-4 h-4" />
            </span>
            <span className="font-bold tracking-tight text-white text-sm">
              InstaEdit
            </span>
            <span className="hidden sm:inline-flex items-center gap-1 ml-2 text-xs text-zinc-500 group-hover:text-zinc-300 transition-colors">
              <ChevronRight className="w-3 h-3" />
              Editor
            </span>
          </Link>
          <div className="flex items-center gap-2">
            <Link
              to="/login"
              className="hidden sm:inline-flex text-sm font-medium px-4 py-2 text-zinc-300 hover:text-white transition-colors"
            >
              Log in
            </Link>
            <Link
              to="/login"
              className="inline-flex items-center gap-1.5 px-4 py-2 rounded-full bg-white text-black text-sm font-semibold hover:bg-zinc-100 transition-colors shadow-[0_8px_30px_-10px_rgba(255,255,255,0.4)]"
            >
              Connect account
              <ArrowRight className="w-4 h-4" />
            </Link>
          </div>
        </div>
      </div>
    </nav>
  );
}
