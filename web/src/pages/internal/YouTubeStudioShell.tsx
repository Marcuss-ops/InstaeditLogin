import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { ArrowLeft } from "lucide-react";
import { ProviderBadge } from "../../components/brand/PlatformLogos";

export function StudioShell({ children }: { children: ReactNode }) {
  return (
    <div className="min-h-full p-8 bg-[#030308] text-[#e8e8ef]">
      <div className="max-w-3xl mx-auto space-y-6">
        <header className="mb-2">
          <p className="text-[12px] font-semibold uppercase tracking-[0.16em] text-[#9aa0aa] mb-2">
            / app / youtube / studio
          </p>
          <div className="flex items-center justify-between gap-4">
            <div>
              <h1 className="text-[28px] font-extrabold tracking-[-0.02em] text-white flex items-center gap-3">
                <ProviderBadge
                  platform="youtube"
                  className="h-10 w-10 justify-center rounded-xl text-white shadow-[0_4px_16px_rgba(255,0,0,0.25)]"
                  compact
                  logoClassName="h-5 w-5"
                />
                YouTube Studio
              </h1>
              <p className="text-[15px] text-[#9aa0aa] mt-2 max-w-xl">
                Edit thumbnails of videos already on the channel and publish
                them when they're ready.
              </p>
            </div>
            <Link
              to="/app/uploads"
              className="hidden sm:inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-white/[0.04] border border-white/[0.08] text-[13px] font-semibold text-white hover:bg-white/[0.08] transition-colors no-underline"
            >
              <ArrowLeft size={14} aria-hidden="true" /> Back to Imports
            </Link>
          </div>
        </header>
        {children}
      </div>
    </div>
  );
}
