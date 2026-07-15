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
    <div className="min-h-screen bg-[#030308] text-[#e8e8ef]">
      {/* Nav */}
      <nav className="fixed top-0 w-full z-50 border-b border-white/[0.10] bg-[#030308]/80 backdrop-blur-xl">
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
      <section className="relative pt-48 pb-40 px-6 overflow-hidden">
        <div className="glow-orb bg-[#0A84FF] w-[700px] h-[700px] top-[-200px] left-1/2 -translate-x-1/2" />
        <ScrollReveal className="relative z-10 max-w-4xl mx-auto text-center">
          <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full border border-white/[0.15] bg-white/[0.05] text-xs text-[#9aa0aa] mb-16">
            <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse" />
            Now managing 10,000+ publications per month
          </div>

          <h1 className="text-5xl md:text-7xl font-semibold tracking-tight leading-[1.08] mb-10">
            Your entire content
            <br />
            <span className="bg-gradient-to-r from-[#0A84FF] via-[#7B61FF] to-[#E1306C] bg-clip-text text-transparent">
              operation, unified.
            </span>
          </h1>

          <p className="text-lg md:text-xl text-[#9aa0aa] max-w-2xl mx-auto mb-14 leading-relaxed">
            We are a team of editors who scaled production from 50 posts to
            10,000 pieces of content per month across 7 platforms. InstaEdit is
            the infrastructure that makes it possible.
          </p>

          <div className="flex flex-col sm:flex-row items-center justify-center gap-4">
            <Link
              to="/login"
              className="group flex items-center gap-2 px-7 py-3.5 rounded-xl bg-white text-[#030308] font-medium text-sm hover:bg-white/90 transition-all"
            >
              Start publishing
              <ArrowRight className="w-4 h-4 group-hover:translate-x-0.5 transition-transform" />
            </Link>
            <a
              href="#features"
              className="flex items-center gap-2 px-7 py-3.5 rounded-xl border border-white/[0.10] text-sm text-[#9aa0aa] hover:text-white hover:border-white/[0.20] transition-all"
            >
              See how it works
            </a>
          </div>
        </ScrollReveal>
      </section>

      <div className="section-divider" />

      {/* Platforms ticker */}
      <section className="py-24 border-y border-white/[0.10] bg-white/[0.015] overflow-hidden">
        <div className="flex items-center justify-center gap-12 md:gap-20 opacity-40">
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

      <div className="section-divider" />

      {/* Stats */}
      <section className="py-40 px-6 bg-elevated">
        <ScrollReveal className="max-w-5xl mx-auto grid grid-cols-2 md:grid-cols-4 gap-16 md:gap-8">
          {STATS.map((s) => (
            <div key={s.label} className="text-center">
              <div className="text-4xl md:text-5xl font-semibold tracking-tight mb-3">
                {s.value}
              </div>
              <div className="text-sm text-[#9aa0aa]">{s.label}</div>
            </div>
          ))}
        </ScrollReveal>
      </section>

      {/* Features */}
      <section id="features" className="py-40 px-6 relative">
        <div className="glow-orb bg-[#7B61FF] w-[600px] h-[600px] bottom-[-250px] right-[-150px] opacity-10" />
        <div className="max-w-5xl mx-auto relative z-10">
          <ScrollReveal className="text-center mb-16">
            <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full border border-white/[0.15] bg-white/[0.05] text-xs text-[#9aa0aa] mb-6">
              <Sparkles className="w-3 h-3" />
              Features
            </div>
            <h2 className="text-3xl md:text-5xl font-semibold tracking-tight mb-5">
              Built for content teams
            </h2>
            <p className="text-[#9aa0aa] max-w-lg mx-auto text-lg">
              Everything you need to manage multi-platform publishing at scale.
              No bloat, no compromises.
            </p>
          </ScrollReveal>

          <div className="grid md:grid-cols-2 lg:grid-cols-3 gap-6">
            {FEATURES.map((f, i) => (
              <ScrollReveal key={f.title} delay={i * 80}>
                <div className="surface-card p-8 h-full hover:bg-[#262638] hover:border-white/[0.30] hover:shadow-[inset_0_1px_0_0_rgba(255,255,255,0.22)] hover:-translate-y-1 transition-all duration-300">
                  <div className="w-11 h-11 rounded-xl bg-white/[0.06] flex items-center justify-center text-[#7B61FF] mb-6">
                    {f.icon}
                  </div>
                  <h3 className="text-lg font-medium mb-3">{f.title}</h3>
                  <p className="text-[#9aa0aa] leading-relaxed">
                    {f.description}
                  </p>
                </div>
              </ScrollReveal>
            ))}
          </div>
        </div>
      </section>

      <div className="section-divider" />

      {/* CTA */}
      <section className="relative py-40 px-6 overflow-hidden">
        <div className="glow-orb bg-[#E1306C] w-[500px] h-[500px] top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 opacity-15" />
        <ScrollReveal className="relative z-10 max-w-3xl mx-auto text-center">
          <div className="surface-card p-12 md:p-20 shadow-[0_0_80px_-20px_rgba(123,97,255,0.18)]">
            <div className="w-12 h-12 rounded-xl bg-gradient-to-br from-[#0A84FF] to-[#7B61FF] flex items-center justify-center mx-auto mb-8">
              <Zap className="w-5 h-5 text-white" />
            </div>
            <h2 className="text-3xl md:text-5xl font-semibold tracking-tight mb-5">
              Ready to scale your content?
            </h2>
            <p className="text-[#9aa0aa] mb-10 max-w-md mx-auto text-lg">
              Connect your first platform in under 2 minutes. No credit card
              required.
            </p>
            <Link
              to="/login"
              className="inline-flex items-center gap-2 px-7 py-3.5 rounded-xl bg-white text-[#030308] font-medium text-sm hover:bg-white/90 transition-all"
            >
              Get started free
              <ArrowRight className="w-4 h-4" />
            </Link>
          </div>
        </ScrollReveal>
      </section>

      <div className="section-divider" />

      {/* Footer */}
      <footer className="pt-20 pb-12 px-6 border-t border-white/[0.10]">
        <div className="max-w-6xl mx-auto flex flex-col items-center gap-10">
          <Link to="/" className="flex items-center gap-2.5">
            <div className="w-6 h-6 rounded-md bg-gradient-to-br from-[#0A84FF] to-[#7B61FF] flex items-center justify-center">
              <Zap className="w-3 h-3 text-white" />
            </div>
            <span className="text-sm font-medium">InstaEdit</span>
          </Link>

          <div className="flex flex-wrap items-center justify-center gap-x-6 gap-y-3 text-xs text-[#9aa0aa]">
            {[
              { name: "TikTok", slug: "tiktok", color: "#ff0050" },
              { name: "Instagram", slug: "instagram", color: "#E1306C" },
              { name: "Facebook", slug: "facebook", color: "#0A84FF" },
              { name: "Threads", slug: "threads", color: "#9aa0aa" },
              { name: "YouTube", slug: "youtube", color: "#FF0000" },
              { name: "LinkedIn", slug: "linkedin", color: "#0077B5" },
              { name: "X", slug: "twitter", color: "#e8e8ef" },
            ].map((p) => (
              <Link
                key={p.slug}
                to={`/${p.slug}`}
                className="hover:text-white transition-colors"
              >
                {p.name}
              </Link>
            ))}
          </div>

          <div className="flex flex-wrap items-center justify-center gap-6 text-xs text-[#9aa0aa]">
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
