import { useState } from "react";
import { Plus } from "lucide-react";

/* ----------------------------------------------------------------------------
 * FAQ — gain-focused objections handling
 * -------------------------------------------------------------------------- */

export function FAQ() {
  const [open, setOpen] = useState<number | null>(null);

  const faqs = [
    {
      q: "Do I need any experience or technical skills?",
      a: "Absolutely not. The entire system is built for complete beginners. You don't need to edit, film, or understand YouTube's backend — the AI and your mentor handle everything. You just follow the steps.",
    },
    {
      q: "How can I start earning money in just 14 days?",
      a: "We use aged YouTube channels that already bypass the algorithm's initial trust filter. Combined with optimized content strategy, SEO, and automated publishing, your channel hits the Partner Program thresholds much faster than starting from scratch.",
    },
    {
      q: "What is an aged YouTube channel and why does it matter?",
      a: "An aged channel is an established account created months or years ago. YouTube's algorithm trusts older channels more, so content gets indexed faster, avoids anti-spam filters, and reaches monetization milestones sooner. It's the shortcut most creators don't know exists.",
    },
    {
      q: "What's the difference between Mentoring and Done-For-You?",
      a: "Mentoring teaches you step-by-step how to build and monetize a channel with 1-on-1 guidance — you learn the system. Done-For-You means we build, manage, and grow the entire channel while you own the asset and collect all revenue. Zero effort on your end.",
    },
    {
      q: "How much time do I need to commit each week?",
      a: "With automation, most students spend 3–5 hours per week on high-impact tasks like niche review and content approval. Everything else — editing, uploads, publishing, optimization — is handled by our AI and team. Some Done-For-You students spend zero hours.",
    },
    {
      q: "How much can I realistically earn per month?",
      a: "Our students average $2,150/month per channel. Single automated channels typically earn $500–$1,500/mo, multi-channel setups $2,000–$5,000/mo, and full portfolio networks can exceed $10,000/mo. Results depend on niche, volume, and which program you choose.",
    },
  ];

  return (
    <section id="faq" className="relative py-24 sm:py-32 overflow-hidden">
      <div className="relative mx-auto max-w-3xl px-6">
        <div className="text-center mb-16 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">FAQ</div>
          <h2 className="text-display-2 text-white">
            Questions?{" "}
            <span className="text-gradient">We've got answers.</span>
          </h2>
        </div>
        <div className="space-y-3">
          {faqs.map((faq, i) => (
            <div
              key={i}
              className={`surface-card overflow-hidden animate-fade-up transition-all duration-300 ${
                open === i ? "border-violet-400/30" : ""
              } ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300", "animation-delay-400"][i] || ""}`}
            >
              <button
                type="button"
                onClick={() => setOpen(open === i ? null : i)}
                className="w-full flex items-center justify-between gap-4 p-5 text-left"
              >
                <span className="text-sm font-semibold text-white">{faq.q}</span>
                <span className={`w-8 h-8 rounded-lg surface-glass flex items-center justify-center shrink-0 transition-transform duration-300 ${open === i ? "rotate-45" : ""}`}>
                  <Plus className="w-4 h-4 text-zinc-400" />
                </span>
              </button>
              {open === i && (
                <div className="px-5 pb-5">
                  <p className="text-sm text-zinc-400 leading-relaxed">{faq.a}</p>
                </div>
              )}
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
