import { Link } from "react-router-dom";
import { ArrowRight, Zap, Shield, BarChart3, Sparkles, PlayCircle, MonitorPlay } from "lucide-react";

// Demo YouTube Shorts shown inside the Shorts section. Single source of truth
// so adding/removing a demo = one line in this array.
const SHORT_DEMOS: ReadonlyArray<{ id: string; title: string }> = [
  { id: "MVwXsmRLnwM", title: "YouTube Shorts demo MVwXsmRLnwM" },
  { id: "XCIWzK2BuRo", title: "YouTube Shorts demo XCIWzK2BuRo" },
];

// Demo YouTube long-form videos shown inside the Long-Form section.
// URL `&pp=…` tracker (YouTube's playback param) stripped from input IDs.
const LONGFORM_DEMOS: ReadonlyArray<{ id: string; title: string }> = [
  { id: "fLhv7d6N_3c", title: "YouTube long-form demo fLhv7d6N_3c" },
  { id: "iA1WT69NFbw", title: "YouTube long-form demo iA1WT69NFbw" },
  { id: "R18AVWQ92fs", title: "YouTube long-form demo R18AVWQ92fs" },
  { id: "lpKX9SKqSMw", title: "YouTube long-form demo lpKX9SKqSMw" },
];

export function Landing() {
  return (
    <div className="min-h-screen bg-[#09090b] text-[#f4f4f5] font-sans antialiased py-24 px-6 selection:bg-[#7B61FF]/30">
      
      {/* Header / Nav */}
      <nav className="fixed top-0 left-0 right-0 h-16 bg-[#09090b]/80 backdrop-blur-md border-b border-zinc-800 z-50">
        <div className="max-w-4xl mx-auto h-full px-6 flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Zap className="w-5 h-5 text-white" />
            <span className="font-bold tracking-tight text-white">InstaEdit</span>
          </div>
          <div className="flex items-center gap-4">
            <Link to="/login" className="text-xs font-medium text-zinc-400 hover:text-white transition-colors">
              Sign in
            </Link>
            <Link to="/login" className="text-xs font-medium px-3.5 py-1.5 rounded bg-white text-black hover:bg-zinc-200 transition-colors">
              Get started
            </Link>
          </div>
        </div>
      </nav>

      {/* Main Container */}
      <main className="max-w-3xl mx-auto space-y-12 mt-8">
        
        {/* Hero Section - Super Simple */}
        <section className="text-center py-12 relative overflow-hidden bg-gradient-to-b from-violet-500/20 via-violet-500/10 to-cyan-500/15">
          {/* Decorative gradient orbs as accents (Tailwind's -.glow-orb utility: absolute, blurred, low-opacity, pointer-events-none). Section bg gradient does the heavy lifting for full-section color coverage. */}
          <div aria-hidden="true" className="absolute inset-0 -my-24 pointer-events-none">
            <div className="glow-orb bg-violet-600 w-[400px] h-[400px] -top-24 -left-24" />
            <div className="glow-orb bg-cyan-500 w-[350px] h-[350px] -bottom-24 -right-24" />
          </div>
          <div className="relative">
            <h1 className="text-4xl md:text-6xl font-extrabold tracking-tight text-white mb-6">
              Your entire content operation, unified.
            </h1>
            <p className="text-sm md:text-base text-zinc-400 max-w-xl mx-auto mb-8 leading-relaxed">
              We scale production from 50 posts to 10,000 pieces of content per month across 7 platforms. InstaEdit is the high-performance infrastructure that makes it possible.
            </p>
            <div className="flex items-center justify-center gap-4">
              <Link to="/login" className="group flex items-center gap-1.5 px-5 py-2.5 rounded bg-white text-black font-semibold text-xs hover:bg-zinc-200 transition-colors">
                Start publishing
                <ArrowRight className="w-3.5 h-3.5" />
              </Link>
              <a href="#features" className="px-5 py-2.5 rounded border border-zinc-800 bg-zinc-900/50 text-xs font-medium text-zinc-400 hover:text-white hover:border-zinc-700 transition-colors">
                See features
              </a>
            </div>
          </div>
        </section>

        {/* Stats Grid - Separated Borders */}
        <section className="grid grid-cols-2 md:grid-cols-4 gap-4">
          <div className="p-6 rounded border border-zinc-800 bg-zinc-900/20 text-center">
            <div className="text-2xl font-bold text-white mb-1">10k+</div>
            <div className="text-[10px] font-bold text-zinc-500 uppercase tracking-wider">Posts / mo</div>
          </div>
          <div className="p-6 rounded border border-zinc-800 bg-zinc-900/20 text-center">
            <div className="text-2xl font-bold text-white mb-1">7</div>
            <div className="text-[10px] font-bold text-zinc-500 uppercase tracking-wider">Platforms</div>
          </div>
          <div className="p-6 rounded border border-zinc-800 bg-zinc-900/20 text-center">
            <div className="text-2xl font-bold text-white mb-1">50+</div>
            <div className="text-[10px] font-bold text-zinc-500 uppercase tracking-wider">Brands</div>
          </div>
          <div className="p-6 rounded border border-zinc-800 bg-zinc-900/20 text-center">
            <div className="text-2xl font-bold text-white mb-1">99.9%</div>
            <div className="text-[10px] font-bold text-zinc-500 uppercase tracking-wider">Uptime</div>
          </div>
        </section>

        {/* Platform List - Separated border */}
        <section className="p-6 rounded border border-zinc-800 bg-zinc-900/20">
          <div className="text-[10px] font-bold text-zinc-500 uppercase tracking-wider mb-4 text-center">Supported Networks</div>
          <div className="flex flex-wrap items-center justify-center gap-6 text-xs text-zinc-400 font-semibold">
            <span className="hover:text-[#E1306C] transition-colors">Instagram</span>
            <span className="hover:text-[#1877F2] transition-colors">Facebook</span>
            <span className="hover:text-white transition-colors">Threads</span>
            <span className="hover:text-white transition-colors">TikTok</span>
            <span className="hover:text-white transition-colors">X (Twitter)</span>
            <span className="hover:text-[#FF0000] transition-colors">YouTube</span>
            <span className="hover:text-[#0A66C2] transition-colors">LinkedIn</span>
          </div>
        </section>

        {/* Features Header */}
        <div id="features" className="text-center pt-8">
          <h2 className="text-xl font-bold text-white">Built for high-velocity teams</h2>
          <p className="text-xs text-zinc-500 mt-2">Everything you need to manage multi-platform publishing.</p>
        </div>

        {/* Features Grid - Separated Borders */}
        <section className="grid sm:grid-cols-2 gap-4">
          <div className="p-6 rounded border border-zinc-800 bg-zinc-900/20 hover:border-zinc-700 transition-colors">
            <Zap className="w-5 h-5 text-white mb-3" />
            <h3 className="text-xs font-bold text-white mb-1">One dashboard, every platform</h3>
            <p className="text-xs text-zinc-400 leading-relaxed">
              Manage Instagram, TikTok, YouTube, X, LinkedIn, Facebook, and Threads from a single interface.
            </p>
          </div>
          <div className="p-6 rounded border border-zinc-800 bg-zinc-900/20 hover:border-zinc-700 transition-colors">
            <Sparkles className="w-5 h-5 text-white mb-3" />
            <h3 className="text-xs font-bold text-white mb-1">Ship content at scale</h3>
            <p className="text-xs text-zinc-400 leading-relaxed">
              Scale production smoothly. Batch scheduling, approval flows, and async publishing built in.
            </p>
          </div>
          <div className="p-6 rounded border border-zinc-800 bg-zinc-900/20 hover:border-zinc-700 transition-colors">
            <Shield className="w-5 h-5 text-white mb-3" />
            <h3 className="text-xs font-bold text-white mb-1">Enterprise-grade security</h3>
            <p className="text-xs text-zinc-400 leading-relaxed">
              OAuth 2.0 with PKCE, AES-256-GCM token encryption. Your credentials never touch our logs.
            </p>
          </div>
          <div className="p-6 rounded border border-zinc-800 bg-zinc-900/20 hover:border-zinc-700 transition-colors">
            <BarChart3 className="w-5 h-5 text-white mb-3" />
            <h3 className="text-xs font-bold text-white mb-1">Unified analytics</h3>
            <p className="text-xs text-zinc-400 leading-relaxed">
              Track reach, engagement, and publishing status across all platforms in one clean view.
            </p>
          </div>
        </section>

        {/* Shorts Section - Separated Border */}
        <section className="p-6 rounded border border-zinc-800 bg-gradient-to-br from-violet-500/20 to-violet-500/5 hover:border-zinc-700 transition-colors relative overflow-hidden">
          <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
            <div className="glow-orb bg-violet-400 w-[280px] h-[280px] -top-12 -right-12" />
          </div>
          <div className="relative">
            <div className="flex justify-center mb-3">
              <span className="inline-flex items-center gap-1.5 text-[10px] font-bold text-zinc-500 uppercase tracking-wider">
                <PlayCircle className="w-3.5 h-3.5" />
                Short-Form Video
              </span>
            </div>
            <h3 className="text-base font-bold text-white mb-2 text-center">
              Ship vertical shorts to every channel in one click
            </h3>
            <p className="text-xs text-zinc-400 leading-relaxed max-w-xl mx-auto text-center mb-4">
              InstaEdit handles the quirks of each short-form platform — aspect ratio, length caps, descriptions,
              thumbnails — so a single vertical render lands correctly on YouTube Shorts, Instagram Reels, and TikTok.
            </p>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 max-w-2xl mx-auto mb-5">
              {SHORT_DEMOS.map((demo) => (
                <div
                  key={demo.id}
                  className="overflow-hidden rounded-xl border border-zinc-800 bg-zinc-950 shadow-[0_4px_24px_rgba(0,0,0,0.4)]"
                >
                  <div className="aspect-[9/16]">
                    <iframe
                      className="w-full h-full"
                      src={`https://www.youtube.com/embed/${demo.id}?playsinline=1`}
                      title={demo.title}
                      loading="lazy"
                      allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share"
                      allowFullScreen
                      referrerPolicy="strict-origin-when-cross-origin"
                    />
                  </div>
                </div>
              ))}
            </div>
            <div className="flex flex-wrap items-center justify-center gap-x-5 gap-y-2 text-xs font-semibold">
              <span className="text-zinc-400 hover:text-[#FF0000] transition-colors">
                YouTube Shorts
              </span>
              <span className="text-zinc-700">·</span>
              <span className="text-zinc-400 hover:text-[#E1306C] transition-colors">
                Instagram Reels
              </span>
              <span className="text-zinc-700">·</span>
              <span className="text-zinc-400 hover:text-white transition-colors">
                TikTok
              </span>
            </div>
          </div>
        </section>

        {/* Long-Form Video Section - Separated Border */}
        <section className="p-6 rounded border border-zinc-800 bg-gradient-to-br from-cyan-500/20 via-cyan-500/5 to-pink-500/15 hover:border-zinc-700 transition-colors relative overflow-hidden">
          <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
            <div className="glow-orb bg-cyan-400 w-[300px] h-[300px] -top-12 -left-12" />
            <div className="glow-orb bg-pink-400 w-[280px] h-[280px] -bottom-12 -right-12" />
          </div>
          <div className="relative">
            <div className="flex justify-center mb-3">
              <span className="inline-flex items-center gap-1.5 text-[10px] font-bold text-zinc-500 uppercase tracking-wider">
                <MonitorPlay className="w-3.5 h-3.5" />
                Long-Form Video
              </span>
            </div>
            <h3 className="text-base font-bold text-white mb-2 text-center">
              Ship long-form video to every major platform
            </h3>
            <p className="text-xs text-zinc-400 leading-relaxed max-w-xl mx-auto text-center mb-4">
              InstaEdit handles resumable uploads, descriptions, thumbnails, and chapter markers —
              so a single horizontal render lands correctly on YouTube, Instagram, Facebook, and LinkedIn.
            </p>
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 max-w-3xl mx-auto mb-5">
              {LONGFORM_DEMOS.map((demo) => (
                <div
                  key={demo.id}
                  className="overflow-hidden rounded-xl border border-zinc-800 bg-zinc-950 shadow-[0_4px_24px_rgba(0,0,0,0.4)]"
                >
                  <div className="aspect-video">
                    <iframe
                      className="w-full h-full"
                      src={`https://www.youtube.com/embed/${demo.id}?playsinline=1`}
                      title={demo.title}
                      loading="lazy"
                      allow="accelerometer; autoplay; clipboard-write; encrypted-media; gyroscope; picture-in-picture; web-share"
                      allowFullScreen
                      referrerPolicy="strict-origin-when-cross-origin"
                    />
                  </div>
                </div>
              ))}
            </div>
            <div className="flex flex-wrap items-center justify-center gap-x-5 gap-y-2 text-xs font-semibold">
              <span className="text-zinc-400 hover:text-[#FF0000] transition-colors">
                YouTube
              </span>
              <span className="text-zinc-700">·</span>
              <span className="text-zinc-400 hover:text-[#E1306C] transition-colors">
                Instagram
              </span>
              <span className="text-zinc-700">·</span>
              <span className="text-zinc-400 hover:text-[#0A66C2] transition-colors">
                LinkedIn
              </span>
            </div>
          </div>
        </section>

        {/* CTA - Separated Border */}
        <section className="p-8 rounded border border-zinc-800 bg-gradient-to-tr from-amber-500/20 to-amber-500/5 text-center relative overflow-hidden">
          <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
            <div className="glow-orb bg-amber-400 w-[320px] h-[320px] -top-20 -right-20" />
          </div>
          <div className="relative">
            <h2 className="text-lg font-bold text-white mb-2">Ready to scale your content?</h2>
            <p className="text-xs text-zinc-400 mb-6 max-w-xs mx-auto">
              Connect your first platform in under 2 minutes. No credit card required.
            </p>
            <Link to="/login" className="inline-flex items-center gap-1.5 px-5 py-2.5 rounded bg-white text-black font-semibold text-xs hover:bg-zinc-200 transition-colors">
              Get started free
              <ArrowRight className="w-3.5 h-3.5" />
            </Link>
          </div>
        </section>

      </main>

      {/* Footer */}
      <footer className="max-w-3xl mx-auto mt-24 pt-8 border-t border-zinc-800">
        <div className="flex flex-col sm:flex-row items-center justify-between gap-4 text-xs text-zinc-500">
          <div className="flex items-center gap-1.5">
            <Zap className="w-4 h-4 text-white" />
            <span className="font-bold text-white">INSTAEDIT</span>
          </div>
          <div className="flex gap-4">
            <Link to="/privacy" className="hover:text-white transition-colors">Privacy</Link>
            <Link to="/terms" className="hover:text-white transition-colors">Terms</Link>
            <span>&copy; {new Date().getFullYear()} InstaEdit</span>
          </div>
        </div>
      </footer>

    </div>
  );
}
