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
    <div className="min-h-screen bg-[#030308] text-[#e8e8ef] selection:bg-white/20">
      {/* Nav */}
      <nav className="fixed top-0 w-full z-50 border-b border-white/[0.10] bg-[#030308]/80 backdrop-blur-xl">
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

          <h1 className="text-4xl sm:text-5xl md:text-7xl font-bold tracking-tight leading-[1.15] mb-10 text-white break-words">
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

          <p className="text-lg sm:text-xl md:text-2xl text-[#9aa0aa] max-w-3xl mx-auto mb-16 leading-relaxed font-medium break-words">
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
      <section
        data-testid="code-preview-section"
className="py-40 px-4 sm:px-6 bg-elevated"
      >
        <ScrollReveal className="max-w-4xl mx-auto w-full min-w-0">
          <div
            className="rounded-2xl border-2 overflow-hidden shadow-2xl bg-[#030308] max-w-full min-w-0"
            style={{ borderColor: `${accent}40` }}
          >
            <div className="flex items-center justify-between px-3 sm:px-4 md:px-6 py-4 bg-white/[0.02] border-b border-white/[0.08] gap-2 sm:gap-3 min-w-0">
              <div className="flex gap-2 shrink-0">
                <div className="w-3 h-3 sm:w-3.5 sm:h-3.5 rounded-full bg-[#FF5F56]" />
                <div className="w-3 h-3 sm:w-3.5 sm:h-3.5 rounded-full bg-[#FFBD2E]" />
                <div className="w-3 h-3 sm:w-3.5 sm:h-3.5 rounded-full bg-[#27C93F]" />
              </div>
              <div className="flex items-center gap-2 px-2 sm:px-3 py-1.5 rounded-md bg-white/[0.05] border border-white/[0.05] min-w-0">
                <Terminal className="w-4 h-4 text-[#9aa0aa] shrink-0" />
                <span className="text-xs text-[#9aa0aa] font-mono font-medium truncate">
                  POST /v1/posts
                </span>
              </div>
              <div className="w-6 sm:w-8 md:w-16 shrink-0" />
            </div>
            <pre className="p-3 sm:p-4 md:p-10 text-[12px] sm:text-[13px] md:text-base font-mono text-[#e8e8ef] overflow-x-auto leading-loose bg-[#030308] min-w-0">
              <code className="whitespace-pre">{platform.codeExample}</code>
            </pre>
          </div>
        </ScrollReveal>
      </section>

      <div className="section-divider" />

      {/* Note Box */}
      <section className="py-40 px-4 sm:px-6">
        <ScrollReveal className="max-w-4xl mx-auto w-full">
          <div
            className="rounded-2xl p-6 sm:p-10 md:p-14 border-l-[8px] sm:border-l-[12px] shadow-2xl bg-white/[0.02] break-words"
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
            <div className="flex flex-col md:flex-row gap-6 sm:gap-8 items-start">
              <div
                className="w-12 h-12 sm:w-16 sm:h-16 rounded-2xl flex items-center justify-center shrink-0 shadow-lg"
                style={{ backgroundColor: `${accent}15`, color: accent }}
              >
                <Info className="w-6 h-6 sm:w-8 sm:h-8" />
              </div>
              <div className="min-w-0">
                <h3
                  className="text-xl sm:text-2xl md:text-3xl font-bold mb-4 break-words"
                  style={{ color: accent }}
                >
                  {platform.noteTitle}
                </h3>
                <p className="text-base sm:text-lg md:text-xl text-[#9aa0aa] leading-relaxed break-words">
                  {platform.noteDescription}
                </p>
              </div>
            </div>
          </div>
        </ScrollReveal>
      </section>

      <div className="section-divider" />

      {/* Comparison */}
      <section className="py-40 px-4 sm:px-6 bg-elevated">
        <ScrollReveal className="max-w-6xl mx-auto w-full">
          <h2 className="text-3xl sm:text-4xl md:text-6xl font-bold tracking-tight text-center mb-16 sm:mb-24 break-words">
            Why InstaEdit vs {platform.name} API?
          </h2>

          <div className="grid lg:grid-cols-2 gap-6 sm:gap-10">
            {/* InstaEdit */}
            <div
              data-testid="comparison-us-card"
              className="rounded-3xl border-2 border-emerald-500/30 bg-emerald-500/[0.03] p-6 sm:p-10 md:p-12 shadow-[0_0_60px_-15px_rgba(16,185,129,0.1)]"
            >
              <div className="inline-flex items-center gap-3 px-4 sm:px-6 py-2 sm:py-3 rounded-full bg-emerald-500/10 border border-emerald-500/20 text-emerald-400 font-bold text-base sm:text-lg mb-6 sm:mb-10">
                <Check className="w-5 h-5 shrink-0" />
                <span className="break-words">
                  {platform.comparison.us.label}
                </span>
              </div>
              <ul className="space-y-6 sm:space-y-8">
                {platform.comparison.us.items.map((item, i) => (
                  <li key={i} className="flex items-start gap-3 sm:gap-4">
                    <div className="w-7 h-7 sm:w-8 sm:h-8 rounded-full bg-emerald-500/10 flex items-center justify-center shrink-0 mt-1">
                      <Check className="w-3.5 h-3.5 sm:w-4 sm:h-4 text-emerald-400" />
                    </div>
                    <span className="text-base sm:text-lg md:text-xl text-[#e8e8ef] font-medium leading-relaxed break-words">
                      {item}
                    </span>
                  </li>
                ))}
              </ul>
            </div>

            {/* Their API */}
            <div
              data-testid="comparison-them-card"
              className="rounded-3xl border-2 border-white/[0.08] bg-[#030308] p-6 sm:p-10 md:p-12 shadow-xl"
            >
              <div className="inline-flex items-center gap-3 px-4 sm:px-6 py-2 sm:py-3 rounded-full bg-white/[0.05] border border-white/[0.05] text-[#9aa0aa] font-bold text-base sm:text-lg mb-6 sm:mb-10">
                <X className="w-5 h-5 shrink-0" />
                <span className="break-words">
                  {platform.comparison.them.label}
                </span>
              </div>
              <ul className="space-y-6 sm:space-y-8">
                {platform.comparison.them.items.map((item, i) => (
                  <li key={i} className="flex items-start gap-3 sm:gap-4 opacity-70">
                    <div className="w-7 h-7 sm:w-8 sm:h-8 rounded-full bg-white/[0.05] flex items-center justify-center shrink-0 mt-1">
                      <X className="w-3.5 h-3.5 sm:w-4 sm:h-4 text-[#9aa0aa]" />
                    </div>
                    <span className="text-base sm:text-lg md:text-xl text-[#9aa0aa] font-medium leading-relaxed break-words">
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
      <section id="how-it-works" className="py-40 px-4 sm:px-6">
        <div className="max-w-6xl mx-auto">
          <ScrollReveal className="text-center mb-24">
            <h2 className="text-3xl sm:text-4xl md:text-6xl font-bold tracking-tight mb-6 break-words">
              How it works
            </h2>
            <p className="text-lg sm:text-xl text-[#9aa0aa] max-w-2xl mx-auto break-words">
              Get from zero to published in three exceptionally simple steps.
            </p>
          </ScrollReveal>

          <div className="grid lg:grid-cols-3 gap-6 sm:gap-8 relative">
            {steps.map((s, i) => (
              <ScrollReveal key={s.step} delay={i * 100}>
                <div className="h-full rounded-3xl p-6 sm:p-10 border-2 border-white/[0.12] bg-white/[0.04] relative overflow-hidden group hover:border-white/[0.25] hover:bg-white/[0.06] transition-all shadow-[0_8px_32px_rgba(0,0,0,0.32)]">
                  <div
                    className="w-16 h-16 sm:w-20 sm:h-20 rounded-2xl flex items-center justify-center text-3xl sm:text-4xl font-bold mb-6 sm:mb-10 shadow-lg"
                    style={{ backgroundColor: `${accent}15`, color: accent }}
                  >
                    {s.step}
                  </div>
                  <h3 className="text-xl sm:text-2xl font-bold mb-4 break-words">
                    {s.title}
                  </h3>
                  <p className="text-base sm:text-lg text-[#9aa0aa] leading-relaxed break-words">
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
      <section className="py-40 px-4 sm:px-6 bg-elevated">
        <ScrollReveal className="max-w-6xl mx-auto w-full">
          <h2 className="text-3xl sm:text-4xl md:text-6xl font-bold tracking-tight text-center mb-16 sm:mb-24 break-words">
            Built for scale
          </h2>

          <div className="grid lg:grid-cols-3 gap-6 sm:gap-8">
            {platform.features.map((f, i) => (
              <ScrollReveal key={i} delay={i * 80}>
                <div className="h-full rounded-3xl p-6 sm:p-10 border-2 border-white/[0.10] bg-[#030308] shadow-[0_8px_32px_rgba(0,0,0,0.32)] hover:border-white/[0.25] hover:shadow-[0_12px_40px_rgba(0,0,0,0.4)] hover:-translate-y-2 transition-all duration-300">
                  <div
                    className="w-14 h-14 sm:w-16 sm:h-16 rounded-2xl flex items-center justify-center mb-6 sm:mb-8 shadow-md"
                    style={{ backgroundColor: `${accent}15`, color: accent }}
                  >
                    <div className="scale-125 sm:scale-150">{f.icon}</div>
                  </div>
                  <h3 className="text-xl sm:text-2xl font-bold mb-4 break-words">
                    {f.title}
                  </h3>
                  <p className="text-base sm:text-lg text-[#9aa0aa] leading-relaxed break-words">
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
      <section className="py-40 px-4 sm:px-6">
        <ScrollReveal className="max-w-5xl mx-auto text-center w-full">
          <h2 className="text-2xl sm:text-3xl md:text-5xl font-bold tracking-tight mb-12 sm:mb-16 break-words">
            Supported formats
          </h2>
          <div className="flex flex-wrap items-center justify-center gap-4 sm:gap-6">
            {platform.contentTypes.map((type) => (
              <span
                key={type}
                className="px-5 sm:px-8 py-3 sm:py-4 rounded-2xl border-2 text-base sm:text-lg font-bold shadow-lg break-words"
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
      <section
        data-testid="faq-section"
className="py-40 px-4 sm:px-6 bg-elevated"
      >
        <ScrollReveal className="max-w-4xl mx-auto w-full">
          <h2 className="text-3xl sm:text-4xl md:text-6xl font-bold tracking-tight text-center mb-16 sm:mb-20 break-words">
            Common questions
          </h2>

          <div className="space-y-4 sm:space-y-6">
            {platform.faq.map((item, i) => (
              <ScrollReveal key={i} delay={i * 60}>
                <div className="rounded-2xl border-2 border-white/[0.08] bg-[#030308] hover:border-white/[0.2] transition-colors overflow-hidden">
                  <button
                    onClick={() => setOpenFaq(openFaq === i ? null : i)}
                    className="w-full flex items-center justify-between px-5 sm:px-10 py-6 sm:py-8 text-left focus:outline-none gap-4"
                  >
                    <span className="text-lg sm:text-xl font-bold break-words">
                      {item.q}
                    </span>
                    <div
                      className={`p-2 rounded-full bg-white/[0.05] transition-transform duration-300 shrink-0 ${
                        openFaq === i ? "rotate-180 bg-white/[0.1]" : ""
                      }`}
                    >
                      <ChevronDown className="w-5 h-5 sm:w-6 sm:h-6 text-white" />
                    </div>
                  </button>
                  {openFaq === i && (
                    <div className="px-5 sm:px-10 pb-6 sm:pb-8 text-base sm:text-lg font-medium text-[#9aa0aa] leading-relaxed break-words">
                      <div className="pt-4 sm:pt-6 border-t border-white/[0.08]">
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
      <section
        data-testid="cta-section"
        className="py-40 px-4 sm:px-6"
      >
        <ScrollReveal className="max-w-5xl mx-auto w-full">
          <div className="rounded-3xl p-1 bg-gradient-to-br from-white/20 via-white/5 to-white/20 shadow-2xl">
            <div className="rounded-[22px] bg-[#030308] p-8 sm:p-16 md:p-24 text-center relative overflow-hidden">
              <div
                className="absolute inset-0 opacity-20 pointer-events-none"
                style={{
                  background: `radial-gradient(circle at center, ${accent} 0%, transparent 70%)`,
                }}
              />
              <div className="relative z-10">
                <div
                  className="w-16 h-16 sm:w-20 sm:h-20 rounded-3xl flex items-center justify-center mx-auto mb-8 sm:mb-10 shadow-lg"
                  style={{ backgroundColor: `${accent}20`, color: accent }}
                >
                  <div className="scale-125 sm:scale-150">{platform.icon}</div>
                </div>
                <h2 className="text-2xl sm:text-4xl md:text-6xl font-bold tracking-tight mb-6 sm:mb-8 break-words">
                  Ship your {platform.name} integration today.
                </h2>
                <p className="text-lg sm:text-xl text-[#9aa0aa] mb-10 sm:mb-14 max-w-2xl mx-auto leading-relaxed break-words">
                  Join thousands of developers using InstaEdit to bypass the
                  headache of building against {platform.name}&apos;s API directly.
                </p>
                <div className="flex flex-col sm:flex-row items-center justify-center gap-4 sm:gap-6">
                  <Link
                    to="/login"
                    className="inline-flex items-center gap-3 px-8 sm:px-10 py-4 sm:py-5 rounded-xl bg-white text-[#030308] font-bold text-base sm:text-lg hover:scale-105 transition-all shadow-xl"
                  >
                    Get started free
                    <ArrowRight className="w-5 h-5" />
                  </Link>
                  <Link
                    to="/"
                    className="inline-flex items-center gap-3 px-8 sm:px-10 py-4 sm:py-5 rounded-xl border-2 border-white/[0.15] text-base sm:text-lg font-bold text-white hover:bg-white/5 transition-all"
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
      <footer className="border-t border-white/[0.10] bg-[#030308]">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 py-16 sm:py-20 flex flex-col md:flex-row items-center justify-between gap-6 sm:gap-8">
          <Link to="/" className="flex items-center gap-3">
            <div className="w-8 h-8 rounded-lg bg-gradient-to-br from-[#0A84FF] to-[#7B61FF] flex items-center justify-center">
              <Zap className="w-4 h-4 text-white" />
            </div>
            <span className="text-base font-bold text-white">InstaEdit</span>
          </Link>
          <div className="flex flex-wrap items-center justify-center gap-4 sm:gap-8 text-sm font-medium text-[#9aa0aa] break-words">
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
