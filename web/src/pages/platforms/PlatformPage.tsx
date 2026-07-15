import { Link, useParams } from "react-router-dom";
import { Zap, ArrowRight, ChevronDown, Check, X, Code, Rocket, Target, Sparkles } from "lucide-react";
import { useState } from "react";
import { PLATFORMS, type PlatformData } from "./platformData";

function SectionDivider({ icon }: { icon: React.ReactNode }) {
  return (
    <div className="flex items-center justify-center py-4">
      <div className="h-px flex-1 max-w-[160px] bg-white/[0.06]" />
      <div className="mx-6 text-[#9aa0aa]/30">{icon}</div>
      <div className="h-px flex-1 max-w-[160px] bg-white/[0.06]" />
    </div>
  );
}

export function PlatformPage() {
  const { slug } = useParams<{ slug: string }>();
  const platform = PLATFORMS[slug ?? ""];

  if (!platform) {
    return (
      <div className="min-h-screen bg-[#030308] text-[#e8e8ef] flex items-center justify-center">
        <div className="text-center">
          <h1 className="text-4xl font-semibold mb-4">Platform not found</h1>
          <Link to="/" className="text-[#0A84FF] hover:underline">
            Go back home
          </Link>
        </div>
      </div>
    );
  }

  return <PlatformPageInner platform={platform} />;
}

function PlatformPageInner({ platform }: { platform: PlatformData }) {
  const [openFaq, setOpenFaq] = useState<number | null>(null);
  const accent = platform.color;

  return (
    <div className="min-h-screen bg-[#030308] text-[#e8e8ef]">
      {/* Nav */}
      <nav className="fixed top-0 w-full z-50 border-b border-white/[0.06] bg-[#030308]/80 backdrop-blur-xl">
        <div className="max-w-6xl mx-auto px-6 h-16 flex items-center justify-between">
          <Link to="/" className="flex items-center gap-2.5">
            <div className="w-8 h-8 rounded-lg bg-gradient-to-br from-[#0A84FF] to-[#7B61FF] flex items-center justify-center">
              <Zap className="w-4 h-4 text-white" />
            </div>
            <span className="text-[15px] font-semibold tracking-tight">
              InstaEdit
            </span>
          </Link>
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
      <section className="pt-44 pb-28 px-6">
        <div className="max-w-4xl mx-auto text-center">
          <div
            className="inline-flex items-center gap-2.5 px-4 py-2 rounded-full border text-sm font-medium mb-12"
            style={{
              borderColor: `${accent}30`,
              backgroundColor: `${accent}10`,
              color: accent,
            }}
          >
            {platform.icon}
            {platform.name} API
          </div>

          <h1 className="text-4xl md:text-6xl font-semibold tracking-tight leading-[1.1] mb-8">
            {platform.heroTagline.split(",").map((part, i) => (
              <span key={i}>
                {i === 0 ? part : (
                  <span style={{ color: accent }}>{part}</span>
                )}
                {i === 0 ? "," : ""}
              </span>
            ))}
          </h1>

          <p className="text-lg md:text-xl text-[#9aa0aa] max-w-2xl mx-auto mb-12 leading-relaxed">
            {platform.heroDescription}
          </p>

          <div className="flex flex-col sm:flex-row items-center justify-center gap-4">
            <Link
              to="/login"
              className="group flex items-center gap-2 px-7 py-3.5 rounded-xl bg-white text-[#030308] font-medium text-sm hover:bg-white/90 transition-all"
            >
              Start free trial
              <ArrowRight className="w-4 h-4 group-hover:translate-x-0.5 transition-transform" />
            </Link>
            <a
              href="#how-it-works"
              className="flex items-center gap-2 px-7 py-3.5 rounded-xl border border-white/[0.10] text-sm text-[#9aa0aa] hover:text-white hover:border-white/[0.20] transition-all"
            >
              View API docs
            </a>
          </div>

          <p className="text-xs text-[#9aa0aa]/60 mt-6">
            No credit card required
          </p>
        </div>
      </section>

      {/* Code preview */}
      <section className="py-28 px-6">
        <div className="max-w-3xl mx-auto">
          <div className="rounded-2xl border border-white/[0.06] bg-white/[0.02] overflow-hidden">
            <div className="flex items-center gap-2 px-5 py-3 border-b border-white/[0.06]">
              <Code className="w-4 h-4 text-[#9aa0aa]" />
              <span className="text-xs text-[#9aa0aa] font-mono">
                POST /v1/posts
              </span>
            </div>
            <pre className="p-6 text-sm font-mono text-[#9aa0aa] overflow-x-auto leading-relaxed">
              <code>{platform.codeExample}</code>
            </pre>
          </div>
        </div>
      </section>

      <SectionDivider icon={<Sparkles className="w-4 h-4" />} />

      {/* Note */}
      <section className="py-28 px-6">
        <div className="max-w-3xl mx-auto">
          <div
            className="rounded-2xl border p-8 md:p-10"
            style={{
              borderColor: `${accent}20`,
              backgroundColor: `${accent}08`,
            }}
          >
            <div
              className="text-sm font-semibold mb-3"
              style={{ color: accent }}
            >
              {platform.noteTitle}
            </div>
            <p className="text-base text-[#9aa0aa] leading-relaxed">
              {platform.noteDescription}
            </p>
          </div>
        </div>
      </section>

      <SectionDivider icon={<Target className="w-4 h-4" />} />

      {/* Comparison */}
      <section className="py-28 px-6">
        <div className="max-w-4xl mx-auto">
          <h2 className="text-3xl md:text-4xl font-semibold tracking-tight text-center mb-16">
            Why InstaEdit vs {platform.name} API?
          </h2>

          <div className="grid md:grid-cols-2 gap-6">
            {/* InstaEdit */}
            <div className="rounded-2xl border border-emerald-500/20 bg-emerald-500/[0.04] p-10">
              <div className="text-sm font-semibold text-emerald-400 mb-8">
                {platform.comparison.us.label}
              </div>
              <ul className="space-y-5">
                {platform.comparison.us.items.map((item, i) => (
                  <li key={i} className="flex items-start gap-3">
                    <Check className="w-4 h-4 text-emerald-400 mt-1 shrink-0" />
                    <span className="text-[15px] text-[#e8e8ef] leading-relaxed">
                      {item}
                    </span>
                  </li>
                ))}
              </ul>
            </div>

            {/* Their API */}
            <div className="rounded-2xl border border-white/[0.06] bg-white/[0.02] p-10">
              <div className="text-sm font-semibold text-[#9aa0aa] mb-8">
                {platform.comparison.them.label}
              </div>
              <ul className="space-y-5">
                {platform.comparison.them.items.map((item, i) => (
                  <li key={i} className="flex items-start gap-3">
                    <X className="w-4 h-4 text-red-400/60 mt-1 shrink-0" />
                    <span className="text-[15px] text-[#9aa0aa] leading-relaxed">
                      {item}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          </div>
        </div>
      </section>

      <SectionDivider icon={<Rocket className="w-4 h-4" />} />

      {/* How it works */}
      <section id="how-it-works" className="py-28 px-6">
        <div className="max-w-4xl mx-auto">
          <h2 className="text-3xl md:text-4xl font-semibold tracking-tight text-center mb-16">
            How it works
          </h2>

          <div className="grid md:grid-cols-3 gap-10">
            {[
              {
                step: "1",
                title: "Connect your account",
                desc: `Link your ${platform.name} account through our dashboard. One-click OAuth — we handle all the permissions.`,
              },
              {
                step: "2",
                title: "Build your integration",
                desc: "Use our simple REST API to schedule posts with text, images, videos, or links. Same pattern works for all platforms.",
              },
              {
                step: "3",
                title: "We handle the rest",
                desc: "InstaEdit publishes at your scheduled time, retries on failures, and notifies you via webhooks.",
              },
            ].map((s) => (
              <div key={s.step} className="text-center">
                <div
                  className="w-14 h-14 rounded-2xl flex items-center justify-center text-xl font-semibold mx-auto mb-6"
                  style={{
                    backgroundColor: `${accent}15`,
                    color: accent,
                  }}
                >
                  {s.step}
                </div>
                <h3 className="text-lg font-medium mb-3">{s.title}</h3>
                <p className="text-[15px] text-[#9aa0aa] leading-relaxed">
                  {s.desc}
                </p>
              </div>
            ))}
          </div>
        </div>
      </section>

      <SectionDivider icon={<Sparkles className="w-4 h-4" />} />

      {/* Features */}
      <section className="py-28 px-6">
        <div className="max-w-4xl mx-auto">
          <h2 className="text-3xl md:text-4xl font-semibold tracking-tight text-center mb-16">
            Features
          </h2>

          <div className="grid md:grid-cols-3 gap-6">
            {platform.features.map((f, i) => (
              <div
                key={i}
                className="rounded-2xl border border-white/[0.06] bg-white/[0.02] p-10"
              >
                <div
                  className="w-12 h-12 rounded-xl flex items-center justify-center mb-6"
                  style={{
                    backgroundColor: `${accent}15`,
                    color: accent,
                  }}
                >
                  {f.icon}
                </div>
                <h3 className="text-lg font-medium mb-3">{f.title}</h3>
                <p className="text-[15px] text-[#9aa0aa] leading-relaxed">
                  {f.description}
                </p>
              </div>
            ))}
          </div>
        </div>
      </section>

      <SectionDivider icon={<Target className="w-4 h-4" />} />

      {/* Content types */}
      <section className="py-28 px-6">
        <div className="max-w-3xl mx-auto text-center">
          <h2 className="text-3xl md:text-4xl font-semibold tracking-tight mb-10">
            Content types
          </h2>
          <div className="flex flex-wrap items-center justify-center gap-3">
            {platform.contentTypes.map((type) => (
              <span
                key={type}
                className="px-4 py-2.5 rounded-xl border text-sm font-medium"
                style={{
                  borderColor: `${accent}25`,
                  color: accent,
                }}
              >
                {type}
              </span>
            ))}
          </div>
        </div>
      </section>

      <SectionDivider icon={<Rocket className="w-4 h-4" />} />

      {/* FAQ */}
      <section className="py-28 px-6">
        <div className="max-w-3xl mx-auto">
          <h2 className="text-3xl md:text-4xl font-semibold tracking-tight text-center mb-16">
            Frequently asked questions
          </h2>

          <div className="space-y-4">
            {platform.faq.map((item, i) => (
              <div
                key={i}
                className="rounded-xl border border-white/[0.06] bg-white/[0.02] overflow-hidden"
              >
                <button
                  onClick={() => setOpenFaq(openFaq === i ? null : i)}
                  className="w-full flex items-center justify-between px-6 py-5 text-left"
                >
                  <span className="text-[15px] font-medium pr-4">{item.q}</span>
                  <ChevronDown
                    className={`w-4 h-4 text-[#9aa0aa] shrink-0 transition-transform ${
                      openFaq === i ? "rotate-180" : ""
                    }`}
                  />
                </button>
                {openFaq === i && (
                  <div className="px-6 pb-5 text-[15px] text-[#9aa0aa] leading-relaxed border-t border-white/[0.06] pt-5">
                    {item.a}
                  </div>
                )}
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* CTA */}
      <section className="py-32 px-6">
        <div className="max-w-3xl mx-auto text-center">
          <div className="rounded-2xl border border-white/[0.06] bg-white/[0.02] p-16 md:p-24">
            <div
              className="w-14 h-14 rounded-2xl flex items-center justify-center mx-auto mb-8"
              style={{ backgroundColor: `${accent}15`, color: accent }}
            >
              {platform.icon}
            </div>
            <h2 className="text-3xl md:text-4xl font-semibold tracking-tight mb-5">
              Ready to ship your {platform.name} integration?
            </h2>
            <p className="text-[#9aa0aa] mb-10 max-w-md mx-auto text-lg leading-relaxed">
              Join thousands of developers who chose InstaEdit over building
              with {platform.name}'s API directly. Same reliability, 10x less
              code.
            </p>
            <div className="flex flex-col sm:flex-row items-center justify-center gap-4">
              <Link
                to="/login"
                className="inline-flex items-center gap-2 px-7 py-3.5 rounded-xl bg-white text-[#030308] font-medium text-sm hover:bg-white/90 transition-all"
              >
                Get started free
                <ArrowRight className="w-4 h-4" />
              </Link>
              <Link
                to="/"
                className="inline-flex items-center gap-2 px-7 py-3.5 rounded-xl border border-white/[0.10] text-sm text-[#9aa0aa] hover:text-white hover:border-white/[0.20] transition-all"
              >
                View documentation
              </Link>
            </div>
          </div>
        </div>
      </section>

      {/* Footer */}
      <footer className="border-t border-white/[0.04] py-12 px-6">
        <div className="max-w-6xl mx-auto flex flex-col md:flex-row items-center justify-between gap-4">
          <Link to="/" className="flex items-center gap-2.5">
            <div className="w-6 h-6 rounded-md bg-gradient-to-br from-[#0A84FF] to-[#7B61FF] flex items-center justify-center">
              <Zap className="w-3 h-3 text-white" />
            </div>
            <span className="text-sm font-medium">InstaEdit</span>
          </Link>
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
