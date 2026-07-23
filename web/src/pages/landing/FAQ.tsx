import { useState } from "react";
import { Plus } from "lucide-react";

/* ----------------------------------------------------------------------------
 * FAQ
 * -------------------------------------------------------------------------- */

export function FAQ() {
  const [open, setOpen] = useState<number | null>(null);

  const faqs = [
    {
      q: "Do I need any previous experience with YouTube or editing?",
      a: "Not at all. Whether you're starting from scratch or already have a channel, we adapt the system to your level. We guide you step-by-step through niche selection, content strategy, and the InstaEdit automation engine so the heavy lifting is handled for you.",
    },
    {
      q: "How do you get a channel monetized in under 3 weeks?",
      a: "We combine aged channels, optimized content strategy, SEO metadata, and automated publishing cadence. Aged channels bypass the initial trust filter, our workflows hit the YouTube Partner Program thresholds faster, and we optimize every upload for maximum reach.",
    },
    {
      q: "What does 'channel automation' actually mean?",
      a: "It means strategy, editing, publishing, and optimization run on a repeatable system. You approve the direction, and our engine — plus our team — handles scheduling, formatting for each platform, thumbnails, metadata and performance tracking.",
    },
    {
      q: "How much time do I need to commit every week?",
      a: "With automation, most students spend 3 to 5 hours a week on high-impact tasks like niche review and content approval. The rest — editing, uploads, publishing, optimization — is handled by our system.",
    },
    {
      q: "What is the free aged YouTube channel included in the program?",
      a: "An aged channel is an established account created months or years ago. YouTube's algorithm trusts older channels more than brand-new ones, so your content gets indexed faster, avoids anti-spam filters, and reaches monetization milestones sooner.",
    },
    {
      q: "What is the difference between the Mentorship and Channel Automation?",
      a: "Mentorship teaches you how to build and run a monetized channel yourself with 1-on-1 guidance. Channel Automation is done-for-you: we build, manage and grow the channel while you own the asset and revenue.",
    },
    {
      q: "What happens if I want to scale to multiple channels or languages later?",
      a: "Once your first channel is profitable, you can upgrade to the Enterprise plan. We duplicate the system across multiple channels and automatically translate and repurpose content into 20+ languages for global reach.",
    },
    {
      q: "How many platforms does the system publish to?",
      a: "The system publishes to 7 platforms: YouTube, TikTok, Instagram, Facebook, LinkedIn, X (Twitter), and Threads. YouTube is the monetization anchor; everything else amplifies reach and revenue.",
    },
  ];

  return (
    <section id="faq" className="relative py-24 sm:py-32 overflow-hidden">
      <div className="relative mx-auto max-w-3xl px-6">
        <div className="text-center mb-16 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">FAQ</div>
          <h2 className="text-display-2 text-white">
            Questions?{" "}
            <span className="text-gradient-animated">We've got answers.</span>
          </h2>
        </div>
        <div className="space-y-3">
          {faqs.map((faq, i) => (
            <div
              key={i}
              className={`surface-card overflow-hidden animate-fade-up transition-all duration-300 ${
                open === i ? "border-violet-400/30" : ""
              } ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300", "animation-delay-400", "animation-delay-500", "animation-delay-600"][i] || ""}`}
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
