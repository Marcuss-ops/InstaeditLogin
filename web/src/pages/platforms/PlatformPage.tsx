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
import { memo, useEffect, useMemo, useState } from "react";
import { ScrollReveal } from "../../components/ScrollReveal";
import { loadPlatformData, type PlatformData } from "./platformData";

export function PlatformPage() {
  const { slug } = useParams<{ slug: string }>();
  const [platform, setPlatform] = useState<PlatformData | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    loadPlatformData(slug ?? "").then((data) => {
      if (cancelled) return;
      setPlatform(data);
      setLoading(false);
    });
    return () => {
      cancelled = true;
    };
  }, [slug]);

  if (loading) {
    return (
      <div className="min-h-screen bg-[#030308] text-[#e8e8ef] flex items-center justify-center">
        <div className="animate-pulse text-lg font-medium text-[#9aa0aa]">
          Loading…
        </div>
      </div>
    );
  }

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

const PlatformPageInner = memo(function PlatformPageInner({
  platform,
}: {
  platform: PlatformData;
}) {
  const [openFaq, setOpenFaq] = useState<number | null>(null);
  const accent = platform.color;

  const steps = useMemo(
    () => [
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
    ],
    [platform.name],
  );

  return (
    <div className="min-h-screen bg-[#030308] text-[#e8e8ef] font-sans antialiased overflow-hidden selection:bg-white/20">
      {/* Grid background effect */}
      <div className="absolute inset-0 bg-[linear-gradient(to_right,#1f1f2e08_1px,transparent_1px),linear-gradient(to_bottom,#1f1f2e08_1px,transparent_1px)] bg-[size:4rem_4rem] pointer-events-none" />

      {/* Nav */}
      <nav className="fixed top-0 w-full z-50 border-b border-white/[0.06] bg-[#030308]/70 backdrop-blur-md">
        <div className="max-w-6xl mx-auto px-6 h-16 flex items-center justify-between">
          <Link to="/" className="flex items-center gap-2.5">
            <div className="w-8 h-8 rounded-lg bg-gradient-to-br from-[#0A84FF] to-[#7B61FF] flex items-center justify-center shadow-[0_0_20px_rgba(10,132,255,0.3)]">
              <Zap className="w-4 h-4 text-white" />
            </div>
            <span className="text-[15px] font-semibold tracking-tight text-white">InstaEdit</span>
          </Link>
          <div className="flex items-center gap-4">
            <Link
              to="/login"
              className="text-sm font-medium text-[#9aa0aa] hover:text-white transition-colors"
            >
              Sign in
            </Link>
            <Link
              to="/login"
              className="text-sm font-medium px-4 py-2 rounded-lg bg-white text-black hover:bg-white/90 shadow-[0_4px_12px_rgba(255,255,255,0.15)] transition-all"
            >
              Get started
            </Link>
          </div>
        </div>
      </nav>

      {/* Hero */}
      <section className="relative pt-44 pb-32 px-6 flex flex-col items-center">
        <div
          className="absolute top-0 left-1/2 -translate-x-1/2 w-[900px] h-[450px] blur-[120px] pointer-events-none opacity-20"
          style={{
            background: `radial-gradient(circle, ${accent} 0%, transparent 70%)`,
          }}
        />
        <ScrollReveal className="relative z-10 max-w-4xl mx-auto text-center w-full">
          <div className="flex justify-center mb-10">
            <div
              className="inline-flex items-center gap-2.5 px-4 py-1.5 rounded-full border text-xs font-bold shadow-lg bg-[#030308]/50 backdrop-blur-sm"
              style={{
                borderColor: `${accent}25`,
                color: accent,
              }}
            >
              <div className="w-4 h-4 flex items-center justify-center">
                {platform.icon}
              </div>
              {platform.name} API Integration
            </div>
          </div>

          <h1 className="text-4xl sm:text-5xl md:text-7xl font-extrabold tracking-tight leading-[1.08] mb-8 text-white">
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

          <p className="text-base sm:text-lg text-[#9aa0aa] max-w-2xl mx-auto mb-12 leading-relaxed">
            {platform.heroDescription}
          </p>

          <div className="flex flex-col sm:flex-row items-center justify-center gap-4">
            <Link
              to="/login"
              className="group flex items-center gap-2 px-6 py-3 rounded-xl bg-white text-[#030308] font-semibold text-sm hover:bg-white/90 shadow-[0_4px_16px_rgba(255,255,255,0.1)] transition-all"
            >
              Start free trial
              <ArrowRight className="w-4 h-4 group-hover:translate-x-0.5 transition-transform" />
            </Link>
            <a
              href="#how-it-works"
              className="flex items-center gap-2 px-6 py-3 rounded-xl border border-white/[0.08] bg-white/[0.02] text-sm text-[#9aa0aa] hover:text-white hover:border-white/[0.15] hover:bg-white/[0.04] transition-all"
            >
              View API docs
            </a>
          </div>
        </ScrollReveal>
      </section>

      {/* Code preview */}
      <section
        data-testid="code-preview-section"
        className="py-16 border-t border-white/[0.06] bg-white/[0.01]"
      >
        <div className="max-w-4xl mx-auto px-6">
          <ScrollReveal className="w-full">
            <div
              className="rounded-2xl border overflow-hidden shadow-2xl bg-[#07070f] max-w-full"
              style={{ borderColor: `rgba(255, 255, 255, 0.08)` }}
            >
              <div className="flex items-center justify-between px-4 py-3.5 bg-white/[0.02] border-b border-white/[0.08] gap-3">
                <div className="flex gap-2">
                  <div className="w-3 h-3 rounded-full bg-[#FF5F56]" />
                  <div className="w-3 h-3 rounded-full bg-[#FFBD2E]" />
                  <div className="w-3 h-3 rounded-full bg-[#27C93F]" />
                </div>
                <div className="flex items-center gap-2 px-3 py-1 rounded-md bg-white/[0.04] border border-white/[0.06]">
                  <Terminal className="w-3.5 h-3.5 text-[#9aa0aa]" />
                  <span className="text-[11px] text-[#9aa0aa] font-mono font-medium">
                    POST /v1/posts
                  </span>
                </div>
                <div className="w-12" />
              </div>
              <pre className="p-6 text-[12px] sm:text-[13px] font-mono text-[#e8e8ef] overflow-x-auto leading-relaxed bg-[#030308]">
                <code className="whitespace-pre">{platform.codeExample}</code>
              </pre>
            </div>
          </ScrollReveal>
        </div>
      </section>

      {/* Note Box */}
      <section className="py-16 border-t border-white/[0.06]">
        <div className="max-w-3xl mx-auto px-6">
          <ScrollReveal className="w-full">
            <div
              className="rounded-2xl p-6 sm:p-10 border shadow-2xl bg-[#0a0a12]/30"
              style={{
                borderLeftWidth: "6px",
                borderLeftColor: accent,
                borderTopColor: "rgba(255, 255, 255, 0.06)",
                borderRightColor: "rgba(255, 255, 255, 0.06)",
                borderBottomColor: "rgba(255, 255, 255, 0.06)",
              }}
            >
              <div className="flex flex-col sm:flex-row gap-5 items-start">
                <div
                  className="w-10 h-10 rounded-xl flex items-center justify-center shrink-0 shadow-md"
                  style={{ backgroundColor: `${accent}15`, color: accent }}
                >
                  <Info className="w-5 h-5" />
                </div>
                <div>
                  <h3
                    className="text-lg font-bold mb-2 text-white"
                  >
                    {platform.noteTitle}
                  </h3>
                  <p className="text-sm text-[#9aa0aa] leading-relaxed">
                    {platform.noteDescription}
                  </p>
                </div>
              </div>
            </div>
          </ScrollReveal>
        </div>
      </section>

      {/* Comparison */}
      <section className="py-24 border-t border-white/[0.06] bg-white/[0.01]">
        <div className="max-w-5xl mx-auto px-6">
          <ScrollReveal className="w-full">
            <h2 className="text-2xl sm:text-4xl font-extrabold tracking-tight text-center text-white mb-16">
              Why InstaEdit vs {platform.name} API?
            </h2>

            <div className="grid md:grid-cols-2 gap-6">
              {/* InstaEdit */}
              <div
                data-testid="comparison-us-card"
                className="rounded-2xl border border-emerald-500/20 bg-emerald-500/[0.02] p-8 shadow-[0_0_40px_-15px_rgba(16,185,129,0.1)]"
              >
                <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full bg-emerald-500/10 border border-emerald-500/20 text-emerald-400 font-bold text-xs mb-8">
                  <Check className="w-4 h-4" />
                  <span>
                    {platform.comparison.us.label}
                  </span>
                </div>
                <ul className="space-y-4">
                  {platform.comparison.us.items.map((item, i) => (
                    <li key={i} className="flex items-start gap-3">
                      <div className="w-6 h-6 rounded-full bg-emerald-500/10 flex items-center justify-center shrink-0 mt-0.5">
                        <Check className="w-3.5 h-3.5 text-emerald-400" />
                      </div>
                      <span className="text-sm text-[#e8e8ef] leading-relaxed">
                        {item}
                      </span>
                    </li>
                  ))}
                </ul>
              </div>

              {/* Their API */}
              <div
                data-testid="comparison-them-card"
                className="rounded-2xl border border-white/[0.08] bg-[#0a0a12]/30 p-8"
              >
                <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full bg-white/[0.03] border border-white/[0.08] text-[#9aa0aa] font-bold text-xs mb-8">
                  <X className="w-4 h-4" />
                  <span>
                    {platform.comparison.them.label}
                  </span>
                </div>
                <ul className="space-y-4">
                  {platform.comparison.them.items.map((item, i) => (
                    <li key={i} className="flex items-start gap-3 opacity-60">
                      <div className="w-6 h-6 rounded-full bg-white/[0.04] flex items-center justify-center shrink-0 mt-0.5">
                        <X className="w-3.5 h-3.5 text-[#9aa0aa]" />
                      </div>
                      <span className="text-sm text-[#9aa0aa] leading-relaxed">
                        {item}
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
            </div>
          </ScrollReveal>
        </div>
      </section>

      {/* How it works */}
      <section id="how-it-works" className="py-24 border-t border-white/[0.06]">
        <div className="max-w-5xl mx-auto px-6">
          <ScrollReveal className="text-center mb-16">
            <h2 className="text-2xl sm:text-4xl font-extrabold tracking-tight text-white mb-4">
              How it works
            </h2>
            <p className="text-sm text-[#9aa0aa] max-w-lg mx-auto">
              Get from zero to published in three exceptionally simple steps.
            </p>
          </ScrollReveal>

          <div className="grid md:grid-cols-3 gap-6">
            {steps.map((s, i) => (
              <ScrollReveal key={s.step} delay={i * 80}>
                <div className="h-full rounded-2xl p-6 border border-white/[0.06] bg-[#0a0a12]/30 hover:border-white/[0.12] hover:bg-[#0f0f1d]/50 transition-all duration-300">
                  <div
                    className="w-12 h-12 rounded-xl flex items-center justify-center text-lg font-bold mb-6 shadow-md"
                    style={{ backgroundColor: `${accent}15`, color: accent }}
                  >
                    {s.step}
                  </div>
                  <h3 className="text-base font-bold text-white mb-2">
                    {s.title}
                  </h3>
                  <p className="text-xs text-[#9aa0aa] leading-relaxed">
                    {s.desc}
                  </p>
                </div>
              </ScrollReveal>
            ))}
          </div>
        </div>
      </section>

      {/* Features */}
      <section className="py-24 border-t border-white/[0.06] bg-white/[0.01]">
        <div className="max-w-5xl mx-auto px-6">
          <ScrollReveal className="text-center mb-16">
            <h2 className="text-2xl sm:text-4xl font-extrabold tracking-tight text-white mb-4">
              Built for scale
            </h2>
            <p className="text-sm text-[#9aa0aa] max-w-lg mx-auto">
              Production-tested features designed to take the friction out of platform publishing.
            </p>
          </ScrollReveal>

          <div className="grid md:grid-cols-3 gap-6">
            {platform.features.map((f, i) => (
              <ScrollReveal key={i} delay={i * 60}>
                <div className="h-full rounded-2xl p-6 border border-white/[0.06] bg-[#0a0a12]/30 hover:border-white/[0.12] hover:bg-[#0f0f1d]/50 hover:-translate-y-1 transition-all duration-300">
                  <div
                    className="w-10 h-10 rounded-xl flex items-center justify-center mb-6 shadow-md"
                    style={{ backgroundColor: `${accent}15`, color: accent }}
                  >
                    <div className="scale-110">{f.icon}</div>
                  </div>
                  <h3 className="text-base font-bold text-white mb-2">
                    {f.title}
                  </h3>
                  <p className="text-xs text-[#9aa0aa] leading-relaxed">
                    {f.description}
                  </p>
                </div>
              </ScrollReveal>
            ))}
          </div>
        </div>
      </section>

      {/* Content types */}
      <section className="py-20 border-t border-white/[0.06]">
        <div className="max-w-4xl mx-auto px-6 text-center">
          <ScrollReveal className="w-full">
            <h2 className="text-xl sm:text-3xl font-bold tracking-tight text-white mb-8">
              Supported formats
            </h2>
            <div className="flex flex-wrap items-center justify-center gap-3">
              {platform.contentTypes.map((type) => (
                <span
                  key={type}
                  className="px-4 py-2 rounded-xl border text-xs font-bold shadow-md"
                  style={{
                    borderColor: `${accent}25`,
                    backgroundColor: `${accent}08`,
                    color: "#fff",
                  }}
                >
                  {type}
                </span>
              ))}
            </div>
          </ScrollReveal>
        </div>
      </section>

      {/* FAQ */}
      <section
        data-testid="faq-section"
        className="py-24 border-t border-white/[0.06] bg-white/[0.01]"
      >
        <div className="max-w-3xl mx-auto px-6">
          <ScrollReveal className="w-full">
            <h2 className="text-2xl sm:text-4xl font-extrabold tracking-tight text-center text-white mb-16">
              Common questions
            </h2>

            <div className="space-y-4">
              {platform.faq.map((item, i) => (
                <ScrollReveal key={i} delay={i * 50}>
                  <div className="rounded-xl border border-white/[0.06] bg-[#0a0a12]/30 hover:border-white/[0.12] transition-colors overflow-hidden">
                    <button
                      onClick={() => setOpenFaq(openFaq === i ? null : i)}
                      className="w-full flex items-center justify-between px-6 py-5 text-left focus:outline-none gap-4"
                    >
                      <span className="text-sm font-bold text-white">
                        {item.q}
                      </span>
                      <div
                        className={`p-1.5 rounded-full bg-white/[0.03] transition-transform duration-300 shrink-0 ${
                          openFaq === i ? "rotate-180 bg-white/[0.08]" : ""
                        }`}
                      >
                        <ChevronDown className="w-4 h-4 text-white" />
                      </div>
                    </button>
                    {openFaq === i && (
                      <div className="px-6 pb-5 text-xs text-[#9aa0aa] leading-relaxed">
                        <div className="pt-4 border-t border-white/[0.06]">
                          {item.a}
                        </div>
                      </div>
                    )}
                  </div>
                </ScrollReveal>
              ))}
            </div>
          </ScrollReveal>
        </div>
      </section>

      {/* CTA */}
      <section
        data-testid="cta-section"
        className="py-24 border-t border-white/[0.06]"
      >
        <div className="max-w-4xl mx-auto px-6">
          <ScrollReveal className="w-full">
            <div className="rounded-3xl border border-white/[0.08] bg-[#07070f]/50 p-12 md:p-20 text-center backdrop-blur-md shadow-2xl relative overflow-hidden">
              <div
                className="absolute inset-0 opacity-10 pointer-events-none"
                style={{
                  background: `radial-gradient(circle at center, ${accent} 0%, transparent 70%)`,
                }}
              />
              <div className="relative z-10">
                <div
                  className="w-12 h-12 rounded-xl flex items-center justify-center mx-auto mb-6 shadow-md"
                  style={{ backgroundColor: `${accent}15`, color: accent }}
                >
                  <div className="scale-110">{platform.icon}</div>
                </div>
                <h2 className="text-2xl sm:text-4xl font-extrabold tracking-tight text-white mb-4">
                  Ship your {platform.name} integration today.
                </h2>
                <p className="text-sm text-[#9aa0aa] mb-8 max-w-md mx-auto leading-relaxed">
                  Join thousands of developers using InstaEdit to bypass the headache of building against {platform.name}&apos;s API directly.
                </p>
                <div className="flex flex-col sm:flex-row items-center justify-center gap-4">
                  <Link
                    to="/login"
                    className="inline-flex items-center gap-2 px-6 py-3 rounded-xl bg-white text-[#030308] font-semibold text-sm hover:bg-white/90 shadow-[0_4px_16px_rgba(255,255,255,0.1)] transition-all"
                  >
                    Get started free
                    <ArrowRight className="w-4 h-4" />
                  </Link>
                  <Link
                    to="/"
                    className="inline-flex items-center gap-2 px-6 py-3 rounded-xl border border-white/[0.08] bg-white/[0.02] text-sm text-[#9aa0aa] hover:text-white hover:border-white/[0.15] hover:bg-white/[0.04] transition-all"
                  >
                    View documentation
                  </Link>
                </div>
              </div>
            </div>
          </ScrollReveal>
        </div>
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
});
