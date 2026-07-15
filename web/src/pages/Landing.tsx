import { Link } from "react-router-dom";
import {
  ArrowRight,
  Zap,
  Shield,
  Globe,
  BarChart3,
  Clock,
  Layers,
} from "lucide-react";

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
    <div className="min-h-screen bg-[#030308] text-[#e8e8ef]">
      {/* Nav */}
      <nav className="fixed top-0 w-full z-50 border-b border-white/[0.06] bg-[#030308]/80 backdrop-blur-xl">
        <div className="max-w-6xl mx-auto px-6 h-16 flex items-center justify-between">
          <div className="flex items-center gap-2.5">
            <div className="w-8 h-8 rounded-lg bg-gradient-to-br from-[#0A84FF] to-[#7B61FF] flex items-center justify-center">
              <Zap className="w-4 h-4 text-white" />
            </div>
            <span className="text-[15px] font-semibold tracking-tight">
              InstaEdit
            </span>
          </div>
          <div className="flex items-center gap-3">
            <Link
              to="/login"
              className="text-sm text-[#9aa0aa] hover:text-white transition-colors"
            >
              Sign in
            </Link>
            <Link
              to="/login"
              className="text-sm font-medium px-4 py-2 rounded-lg bg-white/[0.08] hover:bg-white/[0.12] border border-white/[0.08] transition-all"
            >
              Get started
            </Link>
          </div>
        </div>
      </nav>

      {/* Hero */}
      <section className="pt-32 pb-20 px-6">
        <div className="max-w-4xl mx-auto text-center">
          <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full border border-white/[0.08] bg-white/[0.04] text-xs text-[#9aa0aa] mb-8">
            <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse" />
            Now managing 10,000+ publications per month
          </div>

          <h1 className="text-5xl md:text-7xl font-semibold tracking-tight leading-[1.08] mb-6">
            Your entire content
            <br />
            <span className="bg-gradient-to-r from-[#0A84FF] via-[#7B61FF] to-[#E1306C] bg-clip-text text-transparent">
              operation, unified.
            </span>
          </h1>

          <p className="text-lg md:text-xl text-[#9aa0aa] max-w-2xl mx-auto mb-10 leading-relaxed">
            We are a team of editors who scaled production from 50 posts to
            10,000 pieces of content per month across 7 platforms. InstaEdit is
            the infrastructure that makes it possible.
          </p>

          <div className="flex flex-col sm:flex-row items-center justify-center gap-3">
            <Link
              to="/login"
              className="group flex items-center gap-2 px-6 py-3 rounded-xl bg-white text-[#030308] font-medium text-sm hover:bg-white/90 transition-all"
            >
              Start publishing
              <ArrowRight className="w-4 h-4 group-hover:translate-x-0.5 transition-transform" />
            </Link>
            <a
              href="#features"
              className="flex items-center gap-2 px-6 py-3 rounded-xl border border-white/[0.10] text-sm text-[#9aa0aa] hover:text-white hover:border-white/[0.20] transition-all"
            >
              See how it works
            </a>
          </div>
        </div>
      </section>

      {/* Platforms ticker */}
      <section className="py-12 border-y border-white/[0.04] overflow-hidden">
        <div className="flex items-center justify-center gap-10 md:gap-16 opacity-40">
          {PLATFORMS.map((p) => (
            <span
              key={p.name}
              className="text-sm font-medium tracking-wide"
              style={{ color: p.color }}
            >
              {p.name}
            </span>
          ))}
        </div>
      </section>

      {/* Stats */}
      <section className="py-20 px-6">
        <div className="max-w-5xl mx-auto grid grid-cols-2 md:grid-cols-4 gap-8 md:gap-4">
          {STATS.map((s) => (
            <div key={s.label} className="text-center">
              <div className="text-3xl md:text-4xl font-semibold tracking-tight mb-2">
                {s.value}
              </div>
              <div className="text-sm text-[#9aa0aa]">{s.label}</div>
            </div>
          ))}
        </div>
      </section>

      {/* Features */}
      <section id="features" className="py-20 px-6">
        <div className="max-w-5xl mx-auto">
          <div className="text-center mb-16">
            <h2 className="text-3xl md:text-4xl font-semibold tracking-tight mb-4">
              Built for content teams
            </h2>
            <p className="text-[#9aa0aa] max-w-lg mx-auto">
              Everything you need to manage multi-platform publishing at scale.
              No bloat, no compromises.
            </p>
          </div>

          <div className="grid md:grid-cols-2 gap-px bg-white/[0.04] rounded-2xl overflow-hidden">
            {FEATURES.map((f) => (
              <div
                key={f.title}
                className="bg-[#030308] p-8 hover:bg-white/[0.02] transition-colors"
              >
                <div className="w-10 h-10 rounded-xl bg-white/[0.06] flex items-center justify-center text-[#7B61FF] mb-5">
                  {f.icon}
                </div>
                <h3 className="text-base font-medium mb-2">{f.title}</h3>
                <p className="text-sm text-[#9aa0aa] leading-relaxed">
                  {f.description}
                </p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* CTA */}
      <section className="py-20 px-6">
        <div className="max-w-3xl mx-auto text-center">
          <div className="rounded-2xl border border-white/[0.06] bg-white/[0.02] p-12 md:p-16">
            <h2 className="text-3xl md:text-4xl font-semibold tracking-tight mb-4">
              Ready to scale your content?
            </h2>
            <p className="text-[#9aa0aa] mb-8 max-w-md mx-auto">
              Connect your first platform in under 2 minutes. No credit card
              required.
            </p>
            <Link
              to="/login"
              className="inline-flex items-center gap-2 px-6 py-3 rounded-xl bg-white text-[#030308] font-medium text-sm hover:bg-white/90 transition-all"
            >
              Get started free
              <ArrowRight className="w-4 h-4" />
            </Link>
          </div>
        </div>
      </section>

      {/* Footer */}
      <footer className="border-t border-white/[0.04] py-10 px-6">
        <div className="max-w-6xl mx-auto flex flex-col md:flex-row items-center justify-between gap-4">
          <div className="flex items-center gap-2.5">
            <div className="w-6 h-6 rounded-md bg-gradient-to-br from-[#0A84FF] to-[#7B61FF] flex items-center justify-center">
              <Zap className="w-3 h-3 text-white" />
            </div>
            <span className="text-sm font-medium">InstaEdit</span>
          </div>
          <div className="flex items-center gap-6 text-xs text-[#9aa0aa]">
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
