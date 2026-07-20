import { Link } from "react-router-dom";
import {
  ArrowRight,
  Users,
  Video,
  MessageCircle,
  CheckCircle2,
  Target,
  Award,
  Sparkles,
} from "lucide-react";
import { MarketingNav } from "../components/layout/MarketingNav";
import { MarketingFooter } from "../components/layout/MarketingFooter";

const NAV_LINKS = [
  { label: "How it works", href: "/#pipeline" },
  { label: "Workflow", href: "/#workflow" },
  { label: "Features", href: "/#features" },
  { label: "Agencies", href: "/#agency" },
  { label: "Programs", to: "/programs" },
  { label: "Mentoring", to: "/mentoring" },
  { label: "About us", href: "/#who-are-we" },
];

const MENTORING_PATH = [
  {
    step: "01",
    icon: Target,
    title: "Define your goal",
    description: "We start with a discovery call to understand where you are, where you want to go, and what your content production bottleneck is."
  },
  {
    step: "02",
    icon: Video,
    title: "Build your workflow",
    description: "We help you set up InstaEdit on your account, import channels, and set up the templates you will use every day."
  },
  {
    step: "03",
    icon: Sparkles,
    title: "Get weekly feedback",
    description: "Every week we review your content, analyze metrics and refine strategy to improve reach and engagement."
  },
  {
    step: "04",
    icon: Award,
    title: "Scale independently",
    description: "After the program you will have a repeatable system to produce and publish content at scale, without relying on an external team."
  },
];

/**
 * PACKAGES are listed with an explicit `pricing` field that describes the
 * billing model — NOT a dollar amount. Per marketing policy we do not publish
 * precise integers on the marketing site; final figures are confirmed during
 * onboarding. See docs/MARKETING-FUNNEL.md > Claims & Pricing Policy.
 *
 * Pricing copy MUST match the destination of the package CTA. The package CTA
 * buttons route to `/login` (low-friction self-serve signup), so the pricing
 * lines describe what happens AFTER signup. Discovery calls are reserved for
 * the bottom-of-page CTASection (mailto with Mentoring%20Request subject).
 */
const PACKAGES = [
  {
    title: "Starter Mentoring",
    tagline: "For creators starting from zero",
    pricing: "InstaEdit Pro included · billed per session",
    features: [
      "4 one-on-one 45-minute sessions",
      "Channel and editorial calendar audit",
      "InstaEdit setup and onboarding",
      "Access to the creator community",
    ],
    color: "from-violet-500 to-purple-500",
    cta: "Start with Starter",
  },
  {
    title: "Growth Mentoring",
    tagline: "For those who want to move to the next level",
    pricing: "Billed per package · quarterly engagement",
    features: [
      "8 one-on-one 60-minute sessions",
      "Content strategy and monthly editorial plan",
      "Metrics analysis and post optimization",
      "WhatsApp support for quick questions",
    ],
    color: "from-cyan-500 to-blue-500",
    cta: "Choose Growth",
  },
  {
    title: "Team Mentoring",
    tagline: "For teams and agencies scaling up",
    pricing: "Annual team contract · custom milestones",
    features: [
      "12 team sessions",
      "Multi-account workflow and automations",
      "Editorial team training",
      "Quarterly business review",
    ],
    color: "from-emerald-500 to-teal-500",
    cta: "Contact sales",
  },
];

const TESTIMONIALS = [
  {
    quote: "In three months I went from 2 to 12 posts per week without hiring anyone. Mentoring gave me the right workflow.",
    author: "Sara M.",
    role: "Tech creator",
  },
  {
    quote: "My team no longer loses hours to uploads and reformatting. The path saved us dozens of hours per month.",
    author: "Marco B.",
    role: "Content Strategist",
  },
  {
    quote: "I learned how to use AI not to replace me, but to amplify my style. View results grew steadily.",
    author: "Giulia T.",
    role: "Lifestyle creator",
  },
];

const FAQS = [
  {
    q: "Who is mentoring for?",
    a: "Our mentoring is built for creators producing 2-30 posts per month, agencies handling multiple clients, and in-house content teams hitting a scaling plateau. Whether you are starting from zero or already shipping daily, there is a tier sized for your stage."
  },
  {
    q: "What is the difference between Mentoring and Programs?",
    a: "Programs are self-serve tracks: templates, onboarding sprints, dedicated channels. Mentoring is one-on-one with a content strategist who reviews your work weekly and unblocks you in real time. Many members pair them — the Program as the workflow, Mentoring as the strategic backbone."
  },
  {
    q: "How are the sessions conducted?",
    a: "Live video calls over Google Meet or Zoom. Sessions are recorded so you can revisit decisions, and you receive a written action summary within 24 hours of each session."
  },
  {
    q: "Do I need an active InstaEdit subscription?",
    a: "Starter includes free InstaEdit access for the program duration so you can practice what we cover in session. Higher tiers add 1:1 priority support and faster onboarding windows."
  },
  {
    q: "Can I switch packages mid-program?",
    a: "Yes — upgrade from Starter to Growth at any time and we will credit the remaining sessions. Pro-rated refunds are not offered for downgrades, but you can pause and resume once within the program window."
  },
  {
    q: "What happens after the program ends?",
    a: "You walk away with a documented editorial playbook: posting cadence, templates, KPI dashboard and a 30-day action plan. Alumni get lifetime access to the mentoring community and quarterly content audits."
  },
];

function FAQSection() {
  return (
    <section id="faq" className="relative py-24 sm:py-32 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-cyan-400 w-[360px] h-[360px] -top-20 -right-32 animate-drift-slow opacity-40" />
        <div className="glow-orb bg-violet-500 w-[320px] h-[320px] -bottom-32 -left-24 animate-drift-rev opacity-30" />
      </div>
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-cyan-300/90 mb-3">Frequently asked</div>
          <h2 className="text-display-2 text-white">
            Questions answered.
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            Everything you want to know about the mentoring program, the discovery call,
            and what to expect after onboarding.
          </p>
        </div>
        <div className="grid md:grid-cols-2 gap-5">
          {FAQS.map((item, i) => (
            <div
              key={item.q}
              className={`surface-card p-6 animate-fade-up ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300"][i % 4]}`}
            >
              <h3 className="text-base font-semibold text-white mb-3 flex items-start gap-3">
                <span className="mt-0.5 inline-flex w-6 h-6 items-center justify-center rounded-md bg-violet-500/15 ring-1 ring-violet-400/30 flex-shrink-0 text-[10px] font-bold text-violet-300">
                  Q
                </span>
                <span>{item.q}</span>
              </h3>
              <p className="text-sm text-zinc-400 leading-relaxed pl-9">
                {item.a}
              </p>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

function Hero() {
  return (
    <section className="relative pt-32 pb-20 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora pointer-events-none" />
      <div aria-hidden="true" className="absolute inset-0 grid-bg pointer-events-none opacity-60" />
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-violet-500 w-[460px] h-[460px] -top-32 -left-24 animate-drift-slow opacity-70" />
        <div className="glow-orb bg-cyan-400 w-[420px] h-[420px] -bottom-40 -right-24 animate-drift-rev opacity-60" />
      </div>
      <div className="relative mx-auto max-w-7xl px-6 text-center">
        <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full surface-glass border border-white/15 text-xs font-medium text-zinc-200 mb-7">
          <span className="relative flex h-2 w-2">
            <span className="animate-pulse-glow absolute inline-flex h-full w-full rounded-full bg-emerald-400 opacity-75" />
            <span className="relative inline-flex rounded-full h-2 w-2 bg-emerald-400" />
          </span>
          <span>Mentoring for ambitious creators and teams</span>
        </div>
        <h1 className="text-display-1 text-white max-w-[18ch] mx-auto">
          Don't use InstaEdit alone.{" "}
          <span className="text-gradient-animated">Grow with a mentor.</span>
        </h1>
        <p className="text-body-lg text-zinc-300/90 mt-7 max-w-[60ch] mx-auto">
          A guided path to master AI-powered content production,
          build a scalable workflow and reach your editorial goals.
        </p>
        <div className="flex flex-col sm:flex-row items-center justify-center gap-4 mt-8">
          <a
            href="#packages"
            className="inline-flex items-center gap-2 px-6 py-3 rounded-xl bg-white text-black font-semibold text-sm hover:bg-white/90 hover:shadow-[0_0_40px_-8px_rgba(255,255,255,0.3)] transition-all group"
          >
            Choose your path
            <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
          </a>
          <Link
            to="/programs"
            className="inline-flex items-center gap-2 px-6 py-3 rounded-xl surface-glass border border-white/15 text-sm font-medium text-zinc-200 hover:border-violet-400/50 hover:text-white transition-all"
          >
            See programs
          </Link>
        </div>
      </div>
    </section>
  );
}

function HowItWorks() {
  return (
    <section className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora opacity-20 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">The path</div>
          <h2 className="text-display-2 text-white">
            From goal to workflow, in 4 steps.
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            A practical, personalized path to learn how to use InstaEdit to the fullest
            and build a sustainable content machine.
          </p>
        </div>
        <div className="grid sm:grid-cols-2 lg:grid-cols-4 gap-5">
          {MENTORING_PATH.map((item, i) => (
            <div
              key={item.step}
              className={`surface-card p-6 relative overflow-hidden animate-fade-up hover:border-violet-400/30 transition-all duration-300 ${["", "animation-delay-100", "animation-delay-200", "animation-delay-300"][i]}`}
            >
              <div className="flex items-center justify-between mb-5">
                <div className={`w-12 h-12 rounded-xl bg-gradient-to-br ${i === 0 ? "from-violet-500 to-purple-500" : i === 1 ? "from-cyan-500 to-blue-500" : i === 2 ? "from-pink-500 to-rose-500" : "from-emerald-500 to-teal-500"} flex items-center justify-center text-white shadow-lg`}>
                  <item.icon className="w-5 h-5" />
                </div>
                <span className="text-eyebrow text-zinc-500 tabular-nums">Step {item.step}</span>
              </div>
              <h3 className="text-display-3 text-white mb-2">{item.title}</h3>
              <p className="text-sm text-zinc-400 leading-relaxed">{item.description}</p>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

function PackagesSection() {
  return (
    <section id="packages" className="relative py-24 sm:py-32 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-pink-500 w-[380px] h-[380px] -top-20 -right-32 animate-drift-slow opacity-40" />
        <div className="glow-orb bg-violet-500 w-[340px] h-[340px] -bottom-32 -left-24 animate-drift-rev opacity-30" />
      </div>
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-pink-300/90 mb-3">Packages</div>
          <h2 className="text-display-2 text-white">
            Choose the mentoring that fits you.
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            All packages include platform access and email support.
            You can upgrade to a higher package at any time.
          </p>
        </div>
        <div className="grid lg:grid-cols-3 gap-5">
          {PACKAGES.map((p, i) => (
            <div
              key={p.title}
              className={`surface-card p-7 relative overflow-hidden animate-fade-up hover:border-violet-400/30 transition-all duration-300 ${["", "animation-delay-100", "animation-delay-200"][i]}`}
            >
              <div aria-hidden="true" className={`absolute -top-20 -right-20 w-56 h-56 rounded-full bg-gradient-to-br ${p.color} opacity-20 blur-3xl pointer-events-none`} />
              <div className="relative">
                <div className={`w-12 h-12 rounded-xl bg-gradient-to-br ${p.color} flex items-center justify-center text-white mb-5 shadow-lg`}>
                  <Users className="w-6 h-6" />
                </div>
                <h3 className="text-display-3 text-white mb-1">{p.title}</h3>
                <p className="text-sm text-violet-300/90 font-medium mb-3">{p.tagline}</p>
                <div className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-white/[0.06] border border-white/10 text-[11px] text-zinc-300 mb-5">
                  <span>{p.pricing}</span>
                </div>
                <ul className="space-y-3 mb-7">
                  {p.features.map((f) => (
                    <li key={f} className="flex items-start gap-2 text-sm text-zinc-300">
                      <CheckCircle2 className="w-4 h-4 text-emerald-400 mt-0.5 shrink-0" />
                      <span>{f}</span>
                    </li>
                  ))}
                </ul>
                <Link
                  to="/login"
                  className="inline-flex items-center gap-2 px-6 py-3 rounded-xl bg-white text-black font-semibold text-sm hover:bg-white/90 transition-all group whitespace-nowrap"
                >
                  {p.cta}
                  <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
                </Link>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

function TestimonialsSection() {
  return (
    <section className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora opacity-20 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">Testimonials</div>
          <h2 className="text-display-2 text-white">
            What those who have already started say.
          </h2>
        </div>
        <div className="grid sm:grid-cols-2 lg:grid-cols-3 gap-5">
          {TESTIMONIALS.map((t, i) => (
            <div
              key={t.author}
              className={`surface-card p-6 animate-fade-up ${["", "animation-delay-100", "animation-delay-200"][i]}`}
            >
              <MessageCircle className="w-5 h-5 text-violet-300 mb-4" />
              <p className="text-sm text-zinc-300 leading-relaxed mb-5">"{t.quote}"</p>
              <div className="flex items-center gap-3">
                <div className="w-10 h-10 rounded-full bg-gradient-to-br from-violet-500 to-cyan-500 flex items-center justify-center text-white text-sm font-semibold">
                  {t.author.charAt(0)}
                </div>
                <div>
                  <div className="text-sm font-semibold text-white">{t.author}</div>
                  <div className="text-xs text-zinc-500">{t.role}</div>
                </div>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

function CTASection() {
  return (
    <section className="relative py-24 sm:py-32 overflow-hidden">
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="surface-glass border border-white/15 rounded-2xl p-8 lg:p-12 relative overflow-hidden text-center animate-fade-up">
          <div aria-hidden="true" className="absolute inset-0 cta-glow opacity-30 pointer-events-none" />
          <div className="relative">
            <h2 className="text-display-2 text-white mb-4">
              Ready to grow with a mentor?
            </h2>
            <p className="text-body-lg text-zinc-400 max-w-[55ch] mx-auto mb-8">
              Book a free discovery call and tell us your goals.
              We will propose the most suitable path for you.
            </p>
            <div className="flex flex-col sm:flex-row items-center justify-center gap-4">
              <a
                href="mailto:hello@instaedit.org?subject=Mentoring%20Request"
                className="inline-flex items-center gap-2 px-8 py-3.5 rounded-xl bg-white text-black font-semibold text-sm hover:bg-white/90 hover:shadow-[0_0_40px_-8px_rgba(255,255,255,0.3)] transition-all group"
              >
                Book a call
                <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
              </a>
              <Link
                to="/login"
                className="inline-flex items-center gap-2 px-8 py-3.5 rounded-xl surface-glass border border-white/15 text-sm font-medium text-zinc-200 hover:border-violet-400/50 hover:text-white transition-all"
              >
                Try InstaEdit free
              </Link>
              <Link
                to="/programs"
                className="inline-flex items-center gap-2 px-8 py-3.5 rounded-xl surface-glass border border-white/15 text-sm font-medium text-zinc-200 hover:border-violet-400/50 hover:text-white transition-all"
              >
                See programs
              </Link>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}

export function Mentoring() {
  return (
    <div className="min-h-screen bg-[#030308]">
      <MarketingNav links={NAV_LINKS} />
      <main>
        <Hero />
        <HowItWorks />
        <PackagesSection />
        <TestimonialsSection />
        <FAQSection />
        <CTASection />
      </main>
      <MarketingFooter />
    </div>
  );
}

export default Mentoring;
