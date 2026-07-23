import { useState } from "react";
import { Plus } from "lucide-react";

/* ----------------------------------------------------------------------------
 * FAQ
 * -------------------------------------------------------------------------- */

export function FAQ() {
  const [open, setOpen] = useState<number | null>(null);

  const faqs = [
    {
      q: "Do I need experience with YouTube or English content?",
      a: "Not at all. We guide you through niche selection, content strategy, and the English-language format. Our automation handles editing, metadata, and publishing so you can focus on growing revenue.",
    },
    {
      q: "How do you monetize a channel in under 3 weeks?",
      a: "We combine aged channels, optimized content strategy, SEO metadata, and automated publishing cadence. Aged channels bypass the initial trust filter and our workflows hit the YouTube Partner Program thresholds faster.",
    },
    {
      q: "What is an aged YouTube channel and why does it matter?",
      a: "An aged channel is an established account created months or years ago. YouTube's algorithm trusts older channels more, so content gets indexed faster, avoids anti-spam filters, and reaches monetization milestones sooner.",
    },
    {
      q: "What is the difference between Mentorship and Done-For-You?",
      a: "Mentorship teaches you how to build and monetize a channel yourself with 1-on-1 guidance. Done-For-You means we build, manage, and grow the channel while you own the asset and the revenue.",
    },
    {
      q: "How much time do I need to commit each week?",
      a: "With automation, most creators spend 3 to 5 hours a week on high-impact tasks like niche review and content approval. The rest — editing, uploads, publishing, optimization — is handled by our system.",
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
