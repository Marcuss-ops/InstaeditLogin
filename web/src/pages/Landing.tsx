import { Link } from "react-router-dom";
import {
  ArrowRight,
  Zap,
  Shield,
  Globe,
  BarChart3,
  Clock,
  Layers,
  Sparkles,
} from "lucide-react";
import { ScrollReveal } from "../components/ScrollReveal";

const PLATFORMS = [
  { name: "Instagram", color: "#E1306C" },
  { name: "Facebook", color: "#0A84FF" },
  { name: "Threads", color: "#9AA0AA" },
  { name: "TikTok", color: "#ff0050" },
  { name: "X", color: "#e8e8ef" },
  { name: "YouTube", color: "#ff0000" },
  { name: "LinkedIn", color: "#0A66C2" },
];

const STATS = [
  { value: "10,000+", label: "pieces of content per month" },
  { value: "7", label: "platforms managed" },
  { value: "50+", label: "brands scaled" },
  { value: "99.9%", label: "publishing uptime" },
];

const FEATURES = [
  {
    icon: <Layers className="w-5 h-5" />,
    title: "One dashboard, every platform",
    description:
      "Manage Instagram, TikTok, YouTube, X, LinkedIn, Facebook, and Threads from a single interface. No more tab-switching.",
  },
  {
    icon: <Zap className="w-5 h-5" />,
    title: "Ship content at scale",
    description:
      "Our editorial teams went from 50 posts a month to 10,000. Batch scheduling, approval flows, and async publishing built in.",
  },
  {
    icon: <Shield className="w-5 h-5" />,
    title: "Enterprise-grade security",
    description:
      "OAuth 2.0 with PKCE, AES-256-GCM token encryption at rest, JWT session management. Your credentials never touch our logs.",
  },
  {
    icon: <BarChart3 className="w-5 h-5" />,
    title: "Unified analytics",
    description:
      "Track reach, engagement, and publishing status across all platforms in one place. Know what resonates, everywhere.",
  },
  {
    icon: <Clock className="w-5 h-5" />,
    title: "Smart scheduling",
    description:
      "Queue posts with optimal timing per platform. Our workers handle retries, rate limits, and async publishing automatically.",
  },
  {
    icon: <Globe className="w-5 h-5" />,
    title: "API-first architecture",
    description:
      "Full REST API with API keys, webhooks, and idempotency support. Integrate InstaEdit into your existing content pipeline.",
  },
];

export function Landing() {
  return (
    <div className="min-h-screen bg-[#030308] text-[#e8e8ef] font-sans antialiased overflow-hidden selection:bg-[#7B61FF]/30">
      {/* Grid background effect */}
      <div className="absolute inset-0 bg-[linear-gradient(to_right,#1f1f2e08_1px,transparent_1px),linear-gradient(to_bottom,#1f1f2e08_1px,transparent_1px)] bg-[size:4rem_4rem] pointer-events-none" />
      
      {/* Nav */}
      <nav className="fixed top-0 w-full z-50 border-b border-white/[0.06] bg-[#030308]/70 backdrop-blur-md">
        <div className="max-w-6xl mx-auto px-6 h-16 flex items-center justify-between">
          <div className="flex items-center gap-2.5">
            <div className="w-8 h-8 rounded-lg bg-gradient-to-br from-[#0A84FF] to-[#7B61FF] flex items-center justify-center shadow-[0_0_20px_rgba(10,132,255,0.3)]">
              <Zap className="w-4 h-4 text-white" />
            </div>
            <span className="text-[15px] font-semibold tracking-tight text-white">
              InstaEdit
            </span>
          </div>
          <div className="flex items-center gap-4">
            <Link
              to="/login"
              className="text-sm font-medium text-[#9aa0aa] hover:text-white transition-colors"
            >
              Sign in
            </Link>
            <Link
              to="/login"
              className="text-sm font-medium px-4 py-2 rounded-lg bg-white text-black hover:bg-white/90 shadow-[0_4px_12px_rgba(255,255,255,0.15)] hover:shadow-[0_4px_20px_rgba(255,255,255,0.25)] transition-all"
            >
              Get started
            </Link>
          </div>
        </div>
      </nav>

      {/* Hero */}
      <section className="relative pt-44 pb-32 px-6">
        <div className="absolute top-0 left-1/2 -translate-x-1/2 w-[1000px] h-[500px] bg-gradient-to-b from-[#7B61FF]/10 via-[#0A84FF]/5 to-transparent blur-[120px] pointer-events-none" />
        <ScrollReveal className="relative z-10 max-w-4xl mx-auto text-center">
          <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full border border-white/[0.08] bg-white/[0.03] text-xs text-[#9aa0aa] mb-12 backdrop-blur-sm">
            <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse shadow-[0_0_8px_rgba(52,211,153,0.5)]" />
            Now managing 10,000+ publications per month
          </div>

          <h1 className="text-5xl md:text-8xl font-extrabold tracking-tight leading-[1.05] mb-8 text-white">
            Your entire content
            <br />
            <span className="bg-gradient-to-r from-[#0A84FF] via-[#7B61FF] to-[#E1306C] bg-clip-text text-transparent">
              operation, unified.
            </span>
          </h1>

          <p className="text-base md:text-lg text-[#9aa0aa] max-w-xl mx-auto mb-12 leading-relaxed">
            We scaled production from 50 posts to 10,000 pieces of content per month across 7 platforms. InstaEdit is the high-performance infrastructure that made it possible.
          </p>

          <div className="flex flex-col sm:flex-row items-center justify-center gap-4">
            <Link
              to="/login"
              className="group flex items-center gap-2 px-6 py-3 rounded-xl bg-white text-[#030308] font-semibold text-sm hover:bg-white/90 shadow-[0_4px_16px_rgba(255,255,255,0.1)] transition-all"
            >
              Start publishing
              <ArrowRight className="w-4 h-4 group-hover:translate-x-0.5 transition-transform" />
            </Link>
            <a
              href="#features"
              className="flex items-center gap-2 px-6 py-3 rounded-xl border border-white/[0.08] bg-white/[0.02] text-sm text-[#9aa0aa] hover:text-white hover:border-white/[0.15] hover:bg-white/[0.04] transition-all"
            >
              See how it works
            </a>
          </div>
        </ScrollReveal>
      </section>

      {/* Platforms ticker */}
      <section className="border-y border-white/[0.06] bg-white/[0.01] py-8">
        <div className="max-w-7xl mx-auto px-6 overflow-hidden">
          <div className="flex flex-wrap items-center justify-center gap-8 md:gap-16 opacity-50">
            {PLATFORMS.map((p) => (
              <span
                key={p.name}
                className="text-xs font-semibold tracking-widest uppercase hover:opacity-100 transition-opacity duration-300"
                style={{ color: p.color }}
              >
                {p.name}
              </span>
            ))}
          </div>
        </div>
      </section>

      {/* Stats */}
      <section className="relative py-24 px-6 border-b border-white/[0.06]">
        <div className="absolute inset-0 bg-gradient-to-r from-transparent via-[#7B61FF]/3 to-transparent pointer-events-none" />
        <ScrollReveal className="max-w-5xl mx-auto grid grid-cols-2 md:grid-cols-4 gap-12 md:gap-8 relative z-10">
          {STATS.map((s) => (
            <div key={s.label} className="text-center">
              <div className="text-4xl md:text-5xl font-extrabold tracking-tight text-white mb-2 bg-gradient-to-b from-white to-[#9aa0aa] bg-clip-text text-transparent">
                {s.value}
              </div>
              <div className="text-xs font-medium text-[#9aa0aa] uppercase tracking-wider">{s.label}</div>
            </div>
          ))}
        </ScrollReveal>
      </section>

      {/* Features */}
      <section id="features" className="relative py-32 px-6 border-b border-white/[0.06] bg-white/[0.01]">
        <div className="absolute bottom-0 right-0 w-[500px] h-[500px] bg-[#7B61FF]/5 blur-[120px] pointer-events-none" />
        <div className="max-w-5xl mx-auto relative z-10">
          <ScrollReveal className="text-center mb-20">
            <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full border border-white/[0.08] bg-white/[0.03] text-xs text-[#9aa0aa] mb-4">
              <Sparkles className="w-3 h-3 text-[#7B61FF]" />
              Features
            </div>
            <h2 className="text-3xl md:text-5xl font-extrabold tracking-tight text-white mb-4">
              Built for high-velocity teams
            </h2>
            <p className="text-[#9aa0aa] max-w-md mx-auto text-sm">
              Everything you need to manage multi-platform publishing at scale. No bloat, no compromises.
            </p>
          </ScrollReveal>

          <div className="grid md:grid-cols-2 lg:grid-cols-3 gap-6">
            {FEATURES.map((f, i) => (
              <ScrollReveal key={f.title} delay={i * 60}>
                <div className="p-6 rounded-2xl border border-white/[0.06] bg-[#0a0a12]/30 hover:bg-[#0f0f1d]/50 hover:border-white/[0.12] hover:shadow-[0_8px_30px_rgb(0,0,0,0.5)] transition-all duration-300 group">
                  <div className="w-10 h-10 rounded-xl bg-white/[0.03] border border-white/[0.08] flex items-center justify-center text-[#7B61FF] group-hover:scale-110 group-hover:text-white transition-all mb-6">
                    {f.icon}
                  </div>
                  <h3 className="text-base font-bold text-white mb-2">{f.title}</h3>
                  <p className="text-xs text-[#9aa0aa] leading-relaxed">
                    {f.description}
                  </p>
                </div>
              </ScrollReveal>
            ))}
          </div>
        </div>
      </section>

      {/* CTA */}
      <section className="relative py-32 px-6">
        <div className="absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 w-[600px] h-[300px] bg-[#E1306C]/5 blur-[120px] pointer-events-none" />
        <ScrollReveal className="relative z-10 max-w-4xl mx-auto">
          <div className="rounded-3xl border border-white/[0.08] bg-[#07070f]/50 p-12 md:p-20 text-center backdrop-blur-md shadow-2xl relative overflow-hidden">
            <div className="absolute inset-0 bg-[radial-gradient(circle_at_center,rgba(123,97,255,0.06)_0%,transparent_70%)] pointer-events-none" />
            <div className="relative z-10">
              <div className="w-12 h-12 rounded-xl bg-gradient-to-br from-[#0A84FF] to-[#7B61FF] flex items-center justify-center mx-auto mb-6 shadow-[0_0_20px_rgba(123,97,255,0.3)]">
                <Zap className="w-5 h-5 text-white" />
              </div>
              <h2 className="text-3xl md:text-5xl font-extrabold tracking-tight text-white mb-4">
                Ready to scale your content?
              </h2>
              <p className="text-sm text-[#9aa0aa] mb-8 max-w-xs mx-auto leading-relaxed">
                Connect your first platform in under 2 minutes. No credit card required.
              </p>
              <Link
                to="/login"
                className="inline-flex items-center gap-2 px-6 py-3 rounded-xl bg-white text-[#030308] font-semibold text-sm hover:bg-white/90 shadow-[0_4px_16px_rgba(255,255,255,0.1)] transition-all"
              >
                Get started free
                <ArrowRight className="w-4 h-4" />
              </Link>
            </div>
          </div>
        </ScrollReveal>
      </section>

      {/* Footer */}
      <footer className="border-t border-white/[0.06] bg-[#030308] relative z-10">
        <div className="max-w-6xl mx-auto px-6 py-12 flex flex-col md:flex-row items-center justify-between gap-6">
          <Link to="/" className="flex items-center gap-2">
            <div className="w-6 h-6 rounded bg-gradient-to-br from-[#0A84FF] to-[#7B61FF] flex items-center justify-center">
              <Zap className="w-3.5 h-3.5 text-white" />
            </div>
            <span className="text-xs font-bold text-white tracking-wider">INSTAEDIT</span>
          </Link>

          <div className="flex flex-wrap justify-center gap-6 text-xs text-[#9aa0aa]">
            <Link to="/privacy" className="hover:text-white transition-colors">
              Privacy
            </Link>
            <Link to="/terms" className="hover:text-white transition-colors">
              Terms
            </Link>
            <span>&copy; {new Date().getFullYear()} InstaEdit</span>
          </div>
        </div>
      </footer>
    </div>
  );
}
