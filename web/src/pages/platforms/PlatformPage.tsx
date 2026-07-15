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
    <div className="min-h-screen bg-[#09090b] text-[#f4f4f5] font-sans antialiased py-24 px-6 selection:bg-white/20">
      
      {/* Header / Nav */}
      <nav className="fixed top-0 left-0 right-0 h-16 bg-[#09090b]/80 backdrop-blur-md border-b border-zinc-800 z-50">
        <div className="max-w-4xl mx-auto h-full px-6 flex items-center justify-between">
          <Link to="/" className="flex items-center gap-2">
            <Zap className="w-5 h-5 text-white" />
            <span className="font-bold tracking-tight text-white">InstaEdit</span>
          </Link>
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
        
        {/* Hero Section */}
        <section className="text-center py-12">
          <div className="flex justify-center mb-6">
            <div
              className="inline-flex items-center gap-2 px-3.5 py-1.5 rounded border text-xs font-bold bg-[#09090b]"
              style={{
                borderColor: `${accent}30`,
                color: accent,
              }}
            >
              <div className="w-3.5 h-3.5 flex items-center justify-center shrink-0">
                {platform.icon}
              </div>
              {platform.name} Integration
            </div>
          </div>

          <h1 className="text-4xl md:text-6xl font-extrabold tracking-tight leading-[1.08] mb-6 text-white">
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

          <p className="text-sm md:text-base text-zinc-400 max-w-xl mx-auto mb-8 leading-relaxed">
            {platform.heroDescription}
          </p>

          <div className="flex items-center justify-center gap-4">
            <Link
              to="/login"
              className="group flex items-center gap-1.5 px-5 py-2.5 rounded bg-white text-black font-semibold text-xs hover:bg-zinc-200 transition-colors"
            >
              Start free trial
              <ArrowRight className="w-3.5 h-3.5" />
            </Link>
            <a
              href="#how-it-works"
              className="px-5 py-2.5 rounded border border-zinc-800 bg-zinc-900/50 text-xs font-medium text-zinc-400 hover:text-white hover:border-zinc-700 transition-colors"
            >
              View API docs
            </a>
          </div>
        </section>

        {/* Code Preview - Minimal Border Box */}
        <section data-testid="code-preview-section">
          <div className="rounded border border-zinc-800 overflow-hidden bg-zinc-950">
            <div className="flex items-center justify-between px-4 py-3 bg-zinc-900/30 border-b border-zinc-800 gap-3">
              <div className="flex gap-1.5">
                <div className="w-2.5 h-2.5 rounded-full bg-zinc-800" />
                <div className="w-2.5 h-2.5 rounded-full bg-zinc-800" />
                <div className="w-2.5 h-2.5 rounded-full bg-zinc-800" />
              </div>
              <div className="flex items-center gap-2 px-2 py-0.5 rounded bg-zinc-900 border border-zinc-800">
                <Terminal className="w-3 h-3 text-zinc-500" />
                <span className="text-[10px] text-zinc-400 font-mono font-medium">
                  POST /v1/posts
                </span>
              </div>
              <div className="w-12" />
            </div>
            <pre className="p-5 text-[11px] sm:text-[12px] font-mono text-[#f4f4f5] overflow-x-auto leading-relaxed bg-[#09090b]">
              <code className="whitespace-pre">{platform.codeExample}</code>
            </pre>
          </div>
        </section>

        {/* Note Box */}
        <section className="max-w-2xl mx-auto">
          <div
            className="rounded p-6 border bg-zinc-900/10"
            style={{
              borderLeftWidth: "4px",
              borderLeftColor: accent,
              borderTopColor: "rgba(63, 63, 70, 0.4)",
              borderRightColor: "rgba(63, 63, 70, 0.4)",
              borderBottomColor: "rgba(63, 63, 70, 0.4)",
            }}
          >
            <div className="flex gap-4 items-start">
              <div
                className="w-8 h-8 rounded flex items-center justify-center shrink-0"
                style={{ backgroundColor: `${accent}15`, color: accent }}
              >
                <Info className="w-4 h-4" />
              </div>
              <div>
                <h3 className="text-xs font-bold mb-1 text-white">
                  {platform.noteTitle}
                </h3>
                <p className="text-xs text-zinc-400 leading-relaxed">
                  {platform.noteDescription}
                </p>
              </div>
            </div>
          </div>
        </section>

        {/* Comparison Grid */}
        <section className="space-y-6">
          <h2 className="text-lg font-bold text-center text-white">
            Why InstaEdit vs {platform.name} API?
          </h2>

          <div className="grid md:grid-cols-2 gap-4">
            {/* InstaEdit */}
            <div
              data-testid="comparison-us-card"
              className="rounded border border-zinc-800 bg-zinc-900/20 p-6"
            >
              <div className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded bg-zinc-800 border border-zinc-700 text-white font-bold text-[10px] mb-6">
                <Check className="w-3.5 h-3.5 text-zinc-400" />
                <span>{platform.comparison.us.label}</span>
              </div>
              <ul className="space-y-3">
                {platform.comparison.us.items.map((item, i) => (
                  <li key={i} className="flex items-start gap-2.5">
                    <Check className="w-3.5 h-3.5 text-white shrink-0 mt-0.5" />
                    <span className="text-xs text-zinc-300 leading-relaxed">
                      {item}
                    </span>
                  </li>
                ))}
              </ul>
            </div>

            {/* Their API */}
            <div
              data-testid="comparison-them-card"
              className="rounded border border-zinc-800 bg-zinc-900/10 p-6 opacity-60"
            >
              <div className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded bg-zinc-900/50 border border-zinc-800 text-zinc-500 font-bold text-[10px] mb-6">
                <X className="w-3.5 h-3.5" />
                <span>{platform.comparison.them.label}</span>
              </div>
              <ul className="space-y-3">
                {platform.comparison.them.items.map((item, i) => (
                  <li key={i} className="flex items-start gap-2.5">
                    <X className="w-3.5 h-3.5 text-zinc-600 shrink-0 mt-0.5" />
                    <span className="text-xs text-zinc-500 leading-relaxed">
                      {item}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          </div>
        </section>

        {/* How It Works */}
        <section id="how-it-works" className="space-y-6 pt-6">
          <div className="text-center">
            <h2 className="text-lg font-bold text-white">How it works</h2>
            <p className="text-xs text-zinc-500 mt-1">Get published in three simple steps.</p>
          </div>

          <div className="grid md:grid-cols-3 gap-4">
            {steps.map((s) => (
              <div key={s.step} className="rounded border border-zinc-800 bg-zinc-900/20 p-5">
                <div
                  className="w-8 h-8 rounded flex items-center justify-center text-xs font-bold mb-4"
                  style={{ backgroundColor: `${accent}15`, color: accent }}
                >
                  {s.step}
                </div>
                <h3 className="text-xs font-bold text-white mb-1.5">
                  {s.title}
                </h3>
                <p className="text-[11px] text-zinc-400 leading-relaxed">
                  {s.desc}
                </p>
              </div>
            ))}
          </div>
        </section>

        {/* Features - Scale */}
        <section className="space-y-6 pt-6">
          <div className="text-center">
            <h2 className="text-lg font-bold text-white">Built for scale</h2>
            <p className="text-xs text-zinc-500 mt-1">Production-tested infrastructure features.</p>
          </div>

          <div className="grid md:grid-cols-3 gap-4">
            {platform.features.map((f, i) => (
              <div key={i} className="rounded border border-zinc-800 bg-zinc-900/20 p-5">
                <div
                  className="w-8 h-8 rounded flex items-center justify-center mb-4"
                  style={{ backgroundColor: `${accent}15`, color: accent }}
                >
                  <div className="scale-90 text-white shrink-0">{f.icon}</div>
                </div>
                <h3 className="text-xs font-bold text-white mb-1.5">
                  {f.title}
                </h3>
                <p className="text-[11px] text-zinc-400 leading-relaxed">
                  {f.description}
                </p>
              </div>
            ))}
          </div>
        </section>

        {/* Content Formats */}
        <section className="p-6 rounded border border-zinc-800 bg-zinc-900/20 text-center">
          <h2 className="text-xs font-bold tracking-tight text-white mb-4">
            Supported Formats
          </h2>
          <div className="flex flex-wrap items-center justify-center gap-2">
            {platform.contentTypes.map((type) => (
              <span
                key={type}
                className="px-2.5 py-1 rounded border text-[10px] font-bold"
                style={{
                  borderColor: `${accent}30`,
                  backgroundColor: `${accent}08`,
                  color: "#fff",
                }}
              >
                {type}
              </span>
            ))}
          </div>
        </section>

        {/* FAQ */}
        <section data-testid="faq-section" className="space-y-6">
          <h2 className="text-lg font-bold text-center text-white">
            Common questions
          </h2>

          <div className="space-y-3">
            {platform.faq.map((item, i) => (
              <div key={i} className="rounded border border-zinc-800 bg-zinc-900/20 overflow-hidden">
                <button
                  onClick={() => setOpenFaq(openFaq === i ? null : i)}
                  className="w-full flex items-center justify-between px-5 py-3.5 text-left focus:outline-none gap-4"
                >
                  <span className="text-xs font-bold text-white">
                    {item.q}
                  </span>
                  <ChevronDown
                    className={`w-3.5 h-3.5 text-zinc-500 transition-transform duration-200 ${
                      openFaq === i ? "rotate-180" : ""
                    }`}
                  />
                </button>
                {openFaq === i && (
                  <div className="px-5 pb-3.5 text-[11px] text-zinc-400 leading-relaxed">
                    <div className="pt-2.5 border-t border-zinc-800/80">
                      {item.a}
                    </div>
                  </div>
                )}
              </div>
            ))}
          </div>
        </section>

        {/* CTA */}
        <section data-testid="cta-section" className="p-8 rounded border border-zinc-800 bg-zinc-900/10 text-center">
          <div
            className="w-8 h-8 rounded flex items-center justify-center mx-auto mb-4"
            style={{ backgroundColor: `${accent}15`, color: accent }}
          >
            <div className="scale-90">{platform.icon}</div>
          </div>
          <h2 className="text-lg font-bold text-white mb-2">
            Ship your {platform.name} integration today.
          </h2>
          <p className="text-xs text-zinc-400 mb-6 max-w-xs mx-auto">
            Join thousands of developers using InstaEdit to bypass the headache of building against {platform.name}&apos;s API directly.
          </p>
          <div className="flex items-center justify-center gap-3">
            <Link
              to="/login"
              className="inline-flex items-center gap-1.5 px-5 py-2.5 rounded bg-white text-black font-semibold text-xs hover:bg-zinc-200 transition-colors"
            >
              Get started free
              <ArrowRight className="w-3.5 h-3.5" />
            </Link>
            <Link
              to="/"
              className="px-5 py-2.5 rounded border border-zinc-800 bg-zinc-900/50 text-xs font-medium text-zinc-400 hover:text-white hover:border-zinc-700 transition-colors"
            >
              Back to home
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
            <span>&copy; {new Date().getFullYear()} InstaEdit. All rights reserved.</span>
          </div>
        </div>
      </footer>

    </div>
  );
});
