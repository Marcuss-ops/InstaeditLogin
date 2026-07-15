import { Link, useParams } from "react-router-dom";
import {
  Zap,
  ArrowRight,
  ChevronDown,
  Check,
  X,
  Terminal,
  Info,
} from "lucide-react";
import { useState } from "react";
import { ScrollReveal } from "../../components/ScrollReveal";
import { PLATFORMS, type PlatformData } from "./platformData";

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
    <div className="min-h-screen bg-[#030308] text-[#e8e8ef] selection:bg-white/20">
      {/* Nav */}
      <nav className="fixed top-0 w-full z-50 border-b border-white/[0.08] bg-[#030308]/80 backdrop-blur-xl">
        <div className="max-w-7xl mx-auto px-6 h-20 flex items-center justify-between">
          <Link to="/" className="flex items-center gap-2.5">
            <div className="w-10 h-10 rounded-xl bg-gradient-to-br from-[#0A84FF] to-[#7B61FF] flex items-center justify-center shadow-lg">
              <Zap className="w-5 h-5 text-white" />
            </div>
            <span className="text-lg font-bold tracking-tight">InstaEdit</span>
          </Link>
          <div className="flex items-center gap-6">
            <Link
              to="/login"
              className="text-base font-medium text-[#9aa0aa] hover:text-white transition-colors"
            >
              Sign in
            </Link>
            <Link
              to="/login"
              className="text-base font-medium px-6 py-2.5 rounded-xl bg-white text-[#030308] hover:bg-white/90 transition-all shadow-lg"
            >
              Get started
            </Link>
          </div>
        </div>
      </nav>

      {/* Hero */}
      <section className="relative pt-64 pb-48 px-6 overflow-hidden flex flex-col items-center min-h-[90vh]">
        <div
          className="glow-orb opacity-30"
          style={{
            backgroundColor: accent,
            width: "800px",
            height: "800px",
            top: "-200px",
            left: "50%",
            transform: "translateX(-50%)",
          }}
        />
        <ScrollReveal className="relative z-10 max-w-5xl mx-auto text-center w-full">
          <div className="flex justify-center mb-12">
            <div
              className="inline-flex items-center gap-3 px-6 py-2.5 rounded-full border-2 text-base font-bold shadow-xl bg-[#030308]/50 backdrop-blur-md"
              style={{
                borderColor: `${accent}40`,
                color: accent,
              }}
            >
              <div className="w-6 h-6 flex items-center justify-center">
                {platform.icon}
              </div>
              {platform.name} API Integration
            </div>
          </div>

          <h1 className="text-5xl md:text-7xl font-bold tracking-tight leading-[1.15] mb-10 text-white">
            {platform.heroTagline.split(",").map((part, i) => (
              <span key={i}>
                {i === 0 ? (
                  part
                ) : (
                  <span style={{ color: accent }}>{part}</span>
                )}
                {i === 0 ? "," : ""}
              </span>
            ))}
          </h1>

          <p className="text-xl md:text-2xl text-[#9aa0aa] max-w-3xl mx-auto mb-16 leading-relaxed font-medium">
            {platform.heroDescription}
          </p>

          <div className="flex flex-col sm:flex-row items-center justify-center gap-6">
            <Link
              to="/login"
              className="group flex items-center gap-3 px-10 py-5 rounded-2xl bg-white text-[#030308] font-bold text-lg hover:scale-105 transition-all shadow-xl"
            >
              Start free trial
              <ArrowRight className="w-5 h-5 group-hover:translate-x-1 transition-transform" />
            </Link>
            <a
              href="#how-it-works"
              className="flex items-center gap-3 px-10 py-5 rounded-2xl border-2 border-white/10 text-lg font-bold text-[#e8e8ef] hover:bg-white/5 hover:border-white/20 transition-all"
            >
              View API docs
            </a>
          </div>
          <p className="text-sm text-[#9aa0aa] mt-8 font-medium">
            No credit card required
          </p>
        </ScrollReveal>
      </section>

      <div className="section-divider" />

      {/* Code preview */}
      <section className="py-40 px-6 bg-elevated border-y border-white/[0.08]">
        <ScrollReveal className="max-w-4xl mx-auto">
          <div
            className="rounded-2xl border-2 overflow-hidden shadow-2xl bg-[#030308]"
            style={{ borderColor: `${accent}40` }}
          >
            <div className="flex items-center justify-between px-6 py-4 bg-white/[0.02] border-b border-white/[0.08]">
              <div className="flex gap-2">
                <div className="w-3.5 h-3.5 rounded-full bg-[#FF5F56]" />
                <div className="w-3.5 h-3.5 rounded-full bg-[#FFBD2E]" />
                <div className="w-3.5 h-3.5 rounded-full bg-[#27C93F]" />
              </div>
              <div className="flex items-center gap-2 px-4 py-1.5 rounded-md bg-white/[0.05] border border-white/[0.05]">
                <Terminal className="w-4 h-4 text-[#9aa0aa]" />
                <span className="text-sm text-[#9aa0aa] font-mono font-medium">
                  POST /v1/posts
                </span>
              </div>
              <div className="w-16" />
            </div>
            <pre className="p-8 md:p-10 text-[15px] md:text-base font-mono text-[#e8e8ef] overflow-x-auto leading-loose bg-[#030308]">
              <code>{platform.codeExample}</code>
            </pre>
          </div>
        </ScrollReveal>
      </section>

      <div className="section-divider" />

      {/* Note Box */}
      <section className="py-40 px-6">
        <ScrollReveal className="max-w-4xl mx-auto">
          <div
            className="rounded-2xl p-10 md:p-14 border-l-[12px] shadow-2xl bg-white/[0.02]"
            style={{
              borderColor: accent,
              borderTopWidth: "2px",
              borderRightWidth: "2px",
              borderBottomWidth: "2px",
              borderTopColor: `${accent}30`,
              borderRightColor: `${accent}30`,
              borderBottomColor: `${accent}30`,
            }}
          >
            <div className="flex flex-col md:flex-row gap-8 items-start">
              <div
                className="w-16 h-16 rounded-2xl flex items-center justify-center shrink-0 shadow-lg"
                style={{ backgroundColor: `${accent}15`, color: accent }}
              >
                <Info className="w-8 h-8" />
              </div>
              <div>
                <h3
                  className="text-2xl md:text-3xl font-bold mb-4"
                  style={{ color: accent }}
                >
                  {platform.noteTitle}
                </h3>
                <p className="text-lg md:text-xl text-[#9aa0aa] leading-relaxed">
                  {platform.noteDescription}
                </p>
              </div>
            </div>
          </div>
        </ScrollReveal>
      </section>

      <div className="section-divider" />

      {/* Comparison */}
      <section className="py-40 px-6 bg-elevated border-y border-white/[0.08]">
        <ScrollReveal className="max-w-6xl mx-auto">
          <h2 className="text-4xl md:text-6xl font-bold tracking-tight text-center mb-24">
            Why InstaEdit vs {platform.name} API?
          </h2>

          <div className="grid lg:grid-cols-2 gap-10">
            {/* InstaEdit */}
            <div className="rounded-3xl border-2 border-emerald-500/30 bg-emerald-500/[0.03] p-12 shadow-[0_0_60px_-15px_rgba(16,185,129,0.1)]">
              <div className="inline-flex items-center gap-3 px-6 py-3 rounded-full bg-emerald-500/10 border border-emerald-500/20 text-emerald-400 font-bold text-lg mb-10">
                <Check className="w-5 h-5" />
                {platform.comparison.us.label}
              </div>
              <ul className="space-y-8">
                {platform.comparison.us.items.map((item, i) => (
                  <li key={i} className="flex items-start gap-4">
                    <div className="w-8 h-8 rounded-full bg-emerald-500/10 flex items-center justify-center shrink-0 mt-1">
                      <Check className="w-4 h-4 text-emerald-400" />
                    </div>
                    <span className="text-lg md:text-xl text-[#e8e8ef] font-medium leading-relaxed">
                      {item}
                    </span>
                  </li>
                ))}
              </ul>
            </div>

            {/* Their API */}
            <div className="rounded-3xl border-2 border-white/[0.08] bg-[#030308] p-12 shadow-xl">
              <div className="inline-flex items-center gap-3 px-6 py-3 rounded-full bg-white/[0.05] border border-white/[0.05] text-[#9aa0aa] font-bold text-lg mb-10">
                <X className="w-5 h-5" />
                {platform.comparison.them.label}
              </div>
              <ul className="space-y-8">
                {platform.comparison.them.items.map((item, i) => (
                  <li key={i} className="flex items-start gap-4 opacity-70">
                    <div className="w-8 h-8 rounded-full bg-white/[0.05] flex items-center justify-center shrink-0 mt-1">
                      <X className="w-4 h-4 text-[#9aa0aa]" />
                    </div>
                    <span className="text-lg md:text-xl text-[#9aa0aa] font-medium leading-relaxed">
                      {item}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          </div>
        </ScrollReveal>
      </section>

      <div className="section-divider" />

      {/* How it works */}
      <section id="how-it-works" className="py-40 px-6">
        <div className="max-w-6xl mx-auto">
          <ScrollReveal className="text-center mb-24">
            <h2 className="text-4xl md:text-6xl font-bold tracking-tight mb-6">
              How it works
            </h2>
            <p className="text-xl text-[#9aa0aa] max-w-2xl mx-auto">
              Get from zero to published in three exceptionally simple steps.
            </p>
          </ScrollReveal>

          <div className="grid lg:grid-cols-3 gap-8 relative">
            {[
              {
                step: "1",
                title: "Connect your account",
                desc: `Link your ${platform.name} account through our dashboard. One-click OAuth — we handle the permissions.`,
              },
              {
                step: "2",
                title: "Build your integration",
                desc: "Use our REST API to schedule posts with text or media. The exact same pattern works for all supported platforms.",
              },
              {
                step: "3",
                title: "We handle the rest",
                desc: "InstaEdit publishes at your time, transcodes media, retries on failures, and uses webhooks for updates.",
              },
            ].map((s, i) => (
              <ScrollReveal key={s.step} delay={i * 100}>
                <div className="h-full rounded-3xl p-10 border-2 border-white/[0.08] bg-white/[0.02] relative overflow-hidden group hover:border-white/[0.2] hover:bg-white/[0.04] transition-all">
                  <div
                    className="w-20 h-20 rounded-2xl flex items-center justify-center text-4xl font-bold mb-10 shadow-lg"
                    style={{ backgroundColor: `${accent}15`, color: accent }}
                  >
                    {s.step}
                  </div>
                  <h3 className="text-2xl font-bold mb-4">{s.title}</h3>
                  <p className="text-lg text-[#9aa0aa] leading-relaxed">
                    {s.desc}
                  </p>
                </div>
              </ScrollReveal>
            ))}
          </div>
        </div>
      </section>

      <div className="section-divider" />

      {/* Features */}
      <section className="py-40 px-6 bg-elevated border-y border-white/[0.08]">
        <ScrollReveal className="max-w-6xl mx-auto">
          <h2 className="text-4xl md:text-6xl font-bold tracking-tight text-center mb-24">
            Built for scale
          </h2>

          <div className="grid lg:grid-cols-3 gap-8">
            {platform.features.map((f, i) => (
              <ScrollReveal key={i} delay={i * 80}>
                <div className="h-full rounded-3xl p-10 border-2 border-white/[0.06] bg-[#030308] hover:border-white/[0.3] hover:shadow-[0_12px_40px_rgba(0,0,0,0.4)] hover:-translate-y-2 transition-all duration-300">
                  <div
                    className="w-16 h-16 rounded-2xl flex items-center justify-center mb-8 shadow-md"
                    style={{ backgroundColor: `${accent}15`, color: accent }}
                  >
                    <div className="scale-150">{f.icon}</div>
                  </div>
                  <h3 className="text-2xl font-bold mb-4">{f.title}</h3>
                  <p className="text-lg text-[#9aa0aa] leading-relaxed">
                    {f.description}
                  </p>
                </div>
              </ScrollReveal>
            ))}
          </div>
        </ScrollReveal>
      </section>

      <div className="section-divider" />

      {/* Content types */}
      <section className="py-40 px-6">
        <ScrollReveal className="max-w-5xl mx-auto text-center">
          <h2 className="text-3xl md:text-5xl font-bold tracking-tight mb-16">
            Supported formats
          </h2>
          <div className="flex flex-wrap items-center justify-center gap-6">
            {platform.contentTypes.map((type) => (
              <span
                key={type}
                className="px-8 py-4 rounded-2xl border-2 text-lg font-bold shadow-lg"
                style={{
                  borderColor: `${accent}30`,
                  backgroundColor: `${accent}10`,
                  color: "#fff",
                }}
              >
                {type}
              </span>
            ))}
          </div>
        </ScrollReveal>
      </section>

      <div className="section-divider" />

      {/* FAQ */}
      <section className="py-40 px-6 bg-elevated border-y border-white/[0.08]">
        <ScrollReveal className="max-w-4xl mx-auto">
          <h2 className="text-4xl md:text-6xl font-bold tracking-tight text-center mb-20">
            Common questions
          </h2>

          <div className="space-y-6">
            {platform.faq.map((item, i) => (
              <ScrollReveal key={i} delay={i * 60}>
                <div className="rounded-2xl border-2 border-white/[0.08] bg-[#030308] hover:border-white/[0.2] transition-colors overflow-hidden">
                  <button
                    onClick={() => setOpenFaq(openFaq === i ? null : i)}
                    className="w-full flex items-center justify-between px-10 py-8 text-left focus:outline-none"
                  >
                    <span className="text-xl font-bold pr-8">{item.q}</span>
                    <div
                      className={`p-2 rounded-full bg-white/[0.05] transition-transform duration-300 ${
                        openFaq === i ? "rotate-180 bg-white/[0.1]" : ""
                      }`}
                    >
                      <ChevronDown className="w-6 h-6 text-white" />
                    </div>
                  </button>
                  {openFaq === i && (
                    <div className="px-10 pb-8 text-lg font-medium text-[#9aa0aa] leading-relaxed">
                      <div className="pt-6 border-t border-white/[0.08]">
                        {item.a}
                      </div>
                    </div>
                  )}
                </div>
              </ScrollReveal>
            ))}
          </div>
        </ScrollReveal>
      </section>

      <div className="section-divider" />

      {/* CTA */}
      <section className="py-40 px-6">
        <ScrollReveal className="max-w-5xl mx-auto">
          <div className="rounded-3xl p-1 bg-gradient-to-br from-white/20 via-white/5 to-white/20 shadow-2xl">
            <div className="rounded-[22px] bg-[#030308] p-16 md:p-24 text-center relative overflow-hidden">
              <div
                className="absolute inset-0 opacity-20 pointer-events-none"
                style={{
                  background: `radial-gradient(circle at center, ${accent} 0%, transparent 70%)`,
                }}
              />
              <div className="relative z-10">
                <div
                  className="w-20 h-20 rounded-3xl flex items-center justify-center mx-auto mb-10 shadow-lg"
                  style={{ backgroundColor: `${accent}20`, color: accent }}
                >
                  <div className="scale-150">{platform.icon}</div>
                </div>
                <h2 className="text-4xl md:text-6xl font-bold tracking-tight mb-8">
                  Ship your {platform.name} integration today.
                </h2>
                <p className="text-xl text-[#9aa0aa] mb-14 max-w-2xl mx-auto leading-relaxed">
                  Join thousands of developers using InstaEdit to bypass the
                  headache of building against {platform.name}&apos;s API directly.
                </p>
                <div className="flex flex-col sm:flex-row items-center justify-center gap-6">
                  <Link
                    to="/login"
                    className="inline-flex items-center gap-3 px-10 py-5 rounded-xl bg-white text-[#030308] font-bold text-lg hover:scale-105 transition-all shadow-xl"
                  >
                    Get started free
                    <ArrowRight className="w-5 h-5" />
                  </Link>
                  <Link
                    to="/"
                    className="inline-flex items-center gap-3 px-10 py-5 rounded-xl border-2 border-white/[0.15] text-lg font-bold text-white hover:bg-white/5 transition-all"
                  >
                    View documentation
                  </Link>
                </div>
              </div>
            </div>
          </div>
        </ScrollReveal>
      </section>

      <div className="section-divider" />

      {/* Footer */}
      <footer className="border-t border-white/[0.08] bg-[#030308]">
        <div className="max-w-7xl mx-auto px-6 py-20 flex flex-col md:flex-row items-center justify-between gap-8">
          <Link to="/" className="flex items-center gap-3">
            <div className="w-8 h-8 rounded-lg bg-gradient-to-br from-[#0A84FF] to-[#7B61FF] flex items-center justify-center">
              <Zap className="w-4 h-4 text-white" />
            </div>
            <span className="text-base font-bold text-white">InstaEdit</span>
          </Link>
          <div className="flex flex-wrap items-center justify-center gap-8 text-sm font-medium text-[#9aa0aa]">
            <Link
              to="/privacy"
              className="hover:text-white transition-colors"
            >
              Privacy
            </Link>
            <Link
              to="/terms"
              className="hover:text-white transition-colors"
            >
              Terms
            </Link>
            <span>
              &copy; {new Date().getFullYear()} InstaEdit. All rights reserved.
            </span>
          </div>
        </div>
      </footer>
    </div>
  );
}
