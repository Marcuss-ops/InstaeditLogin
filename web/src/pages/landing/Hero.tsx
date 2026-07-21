import { ArrowRight } from "lucide-react";
import { PLATFORM_REGISTRY, DashboardMockup } from "./shared";

/* ----------------------------------------------------------------------------
 * Hero
 * -------------------------------------------------------------------------- */

export function Hero() {
  return (
    <section className="relative pt-32 pb-20 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora pointer-events-none" />
      <div aria-hidden="true" className="absolute inset-0 grid-bg pointer-events-none opacity-60" />
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-violet-500 w-[460px] h-[460px] -top-32 -left-24 animate-drift-slow opacity-70" />
        <div className="glow-orb bg-cyan-400 w-[420px] h-[420px] -bottom-40 -right-24 animate-drift-rev opacity-60" />
      </div>
      <div className="relative mx-auto max-w-7xl px-6 grid lg:grid-cols-12 gap-10 items-center">
        <div className="lg:col-span-7 xl:col-span-6 animate-fade-up">
          <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full surface-glass border border-white/15 text-xs font-medium text-zinc-200 mb-7">
            <span className="relative flex h-2 w-2">
              <span className="animate-pulse-glow absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
              <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-400" />
            </span>
            <span>7 platforms · one workflow · zero re-uploads</span>
          </div>
          <h1 className="text-display-1 text-white">
            Your creativity.{" "}
            <span className="text-gradient-animated">Our distribution.</span>
          </h1>
          <p className="text-body-lg text-zinc-300/90 mt-7 max-w-[60ch]">
            You bring the great idea and the quality content. InstaEdit makes sure the
            world sees it everywhere — without you wasting hours uploading files to
            every platform. Start your YouTube channel, reach millions on TikTok, or
            grow your brand across all platforms at once.
          </p>
          <div className="flex flex-col sm:flex-row items-start sm:items-center gap-4 mt-8">
            <a
              href="https://discord.com/users/1201477873719050332"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 px-6 py-3 rounded-xl bg-white text-black font-semibold text-sm hover:bg-white/90 hover:shadow-[0_0_40px_-8px_rgba(255,255,255,0.3)] transition-all group"
            >
              Write us
              <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
            </a>
          </div>
          <div className="mt-10 flex items-center gap-4 flex-wrap">
            <span className="text-eyebrow text-zinc-500">Works with</span>
            <div className="flex items-center gap-2">
              {PLATFORM_REGISTRY.map(({ key, name, Logo }) => (
                <span key={key} className="inline-flex w-7 h-7 rounded-md overflow-hidden ring-1 ring-white/15 surface-glass" title={name} aria-label={name}>
                  <Logo className="w-full h-full" />
                </span>
              ))}
            </div>
          </div>
        </div>
        <div className="lg:col-span-5 xl:col-span-6 relative">
          <DashboardMockup />
        </div>
      </div>
    </section>
  );
}
