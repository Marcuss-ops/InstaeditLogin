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
      a: "Not at all. Whether you're starting completely from scratch or already have a channel, we adapt the mentorship to your current level. We guide you step-by-step through channel creation, content strategy, and using tools like InstaEdit to handle the heavy lifting.",
    },
    {
      q: "How long does it take to monetize my channel?",
      a: "Monetization timelines depend on your niche and consistency, but our mentorship is built to accelerate the process as fast as possible. Plus, we provide a free aged YouTube channel to skip the initial trust-building sandbox and hit monetization milestones much faster.",
    },
    {
      q: "How much time do I need to commit every week?",
      a: "Thanks to our automated workflows and content frameworks, you only need about 3 to 5 hours a week. We teach you how to focus solely on high-impact tasks (like recording or approving scripts) while automation and strategy handle the rest.",
    },
    {
      q: "What is the free aged YouTube channel included in the program?",
      a: "An aged channel is an established account created months or years ago. YouTube's algorithm trusts older channels more than brand-new ones, allowing your content to get indexed faster, avoid anti-spam filters, and gain initial traction much quicker.",
    },
    {
      q: "What is the difference between the Mentorship and the Content Automation System?",
      a: "In the Mentorship Program, we teach you how to run and grow YouTube channels correctly with 1-on-1 guidance and weekly feedback. In the Content Automation System, we handle everything — content creation, editing, and publishing are completely done-for-you hands-free.",
    },
    {
      q: "What happens if I want to scale to multiple channels or languages later?",
      a: "Once your primary channel is structured and profitable, you can seamlessly upgrade to our Enterprise Scaling Plan. This allows you to expand into multiple channels and translate/repurpose your content across 20+ languages automatically.",
    },
    {
      q: "What is ChronoN?",
      a: "ChronoN is our proprietary AI engine that can generate professional videos from a simple text brief. It handles scriptwriting, voiceover, visuals, and editing — perfect for students, creators without cameras, or anyone who wants to scale content production.",
    },
    {
      q: "How many platforms does the system publish to?",
      a: "The system publishes to 7 platforms: YouTube, TikTok, Instagram, Facebook, LinkedIn, X (Twitter), and Threads. Each post is automatically formatted for its platform.",
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
