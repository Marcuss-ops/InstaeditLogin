import { Link } from "react-router-dom";
import {
  ArrowRight,
  Users,
  Building2,
  Briefcase,
  CheckCircle2,
  GraduationCap,
  CalendarClock,
  Globe,
  MessageCircle,
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

/**
 * Programs data: single source of truth.
 * Used both by the ProgramsList cards and the ComparisonSection table.
 * Adding or editing a field here propagates to both views automatically —
 * the table no longer carries its own duplicated rows.
 */
const PROGRAMS = [
  {
    shortName: "Creator",
    icon: Users,
    title: "Creator Program",
    tagline: "From creator to content machine",
    duration: "Self-paced · typically 6-8 weeks",
    description:
      "A guided program for creators who want to go from a few posts per month to a structured content machine: workflow, templates, automations and early access to new InstaEdit features.",
    deliverables: [
      "Production-ready template library (50+ formats)",
      "Onboarding cohort with weekly office hours",
      "Priority access to new platform features",
      "Member-only creator community",
    ],
    prerequisites: "An active InstaEdit account",
    pricing: "Included with InstaEdit Pro",
    workspaces: "Single workspace",
    templates: "Library access (50+ formats)",
    supportSla: "Community + weekly office hours",
    auditLog: "Per-account activity log",
    bestFor: "Independent creators scaling up",
    color: "from-violet-500 to-purple-500",
    cta: "Join the waitlist",
  },
  {
    shortName: "Agency",
    icon: Building2,
    title: "Agency Program",
    tagline: "Scale your clients without hiring",
    duration: "12-week engagement · renewable",
    description:
      "Designed for agencies and content studios managing dozens of clients. Dedicated training, SLAs, priority support and increasing margins based on volume.",
    deliverables: [
      "Multi-workspace setup with separate billing",
      "Assisted onboarding for your team",
      "Priority support with 4-hour response SLA",
      "Partner program with volume benefits",
    ],
    prerequisites: "Verified agency with at least 3 active client accounts",
    pricing: "Per-seat · starts at $99/month",
    workspaces: "Multi-workspace + per-client billing",
    templates: "Custom team training",
    supportSla: "Priority 4-hour response SLA",
    auditLog: "Per-account activity log",
    bestFor: "Agencies and content studios",
    color: "from-emerald-500 to-teal-500",
    cta: "Become a partner",
  },
  {
    shortName: "Enterprise",
    icon: Briefcase,
    title: "Enterprise Program",
    tagline: "InstaEdit in your company",
    duration: "12-month agreement · quarterly reviews",
    description:
      "Tailor-made solution for brands and companies that want to integrate InstaEdit into their content operations systems, with single sign-on, audit log and dedicated account manager.",
    deliverables: [
      "SSO and user provisioning",
      "Audit log and SOC2 compliance",
      "Dedicated account manager",
      "Team training and workshops",
    ],
    prerequisites: "Procurement review and SOC2 scope agreement",
    pricing: "Custom enterprise pricing",
    workspaces: "SSO + user provisioning",
    templates: "Custom enterprise integrations",
    supportSla: "Dedicated account manager",
    auditLog: "Full SOC2 audit trail",
    bestFor: "Brands and large teams",
    color: "from-cyan-500 to-blue-500",
    cta: "Contact sales",
  },
];

type Program = (typeof PROGRAMS)[number];

/**
 * Comparison table rows: each row declares which PROGRAMS field to display.
 * The header columns are derived from PROGRAMS.map(p => p.shortName).
 * Adding a new program or updating a feature is a one-line edit to PROGRAMS.
 */
const COMPARISON_ROWS: Array<{ label: string; key: keyof Program }> = [
  { label: "Duration", key: "duration" },
  { label: "Pricing", key: "pricing" },
  { label: "Workspaces", key: "workspaces" },
  { label: "Templates", key: "templates" },
  { label: "Support SLA", key: "supportSla" },
  { label: "Audit log", key: "auditLog" },
  { label: "Best for", key: "bestFor" },
];

const MENTORING = [
  {
    icon: GraduationCap,
    title: "One-on-one mentoring",
    description:
      "Weekly sessions with content strategy experts to define your editorial plan and optimize metrics.",
  },
  {
    icon: CalendarClock,
    title: "Content Calendar Audit",
    description:
      "Complete review of your editorial calendar with practical suggestions on frequency, formats and platforms.",
  },
  {
    icon: Globe,
    title: "Multi-platform Setup",
    description:
      "Guided setup of all channels and automations to publish on every platform without wasting time.",
  },
];

/**
 * Programs social proof: distinct from Mentoring.tsx testimonials in content
 * AND in author names so a reader who has seen both pages never sees the
 * same quote or author attributed twice. Placement: between ComparisonSection
 * and MentoringSection (validate the buyer's decision before offering the
 * off-ramp into Mentoring).
 */
const PROGRAM_TESTIMONIALS = [
  {
    quote: "We onboarded 18 clients in three months without adding a single hire. The Program paid for itself by week four.",
    author: "David K.",
    role: "Agency owner",
  },
  {
    quote: "The SOC2 audit trail was the deal closer for our enterprise procurement. We shipped with everything compliance needed.",
    author: "Anna R.",
    role: "Head of Brand Operations",
  },
  {
    quote: "I went from a few Reels per week to shipping daily. The Program templates gave me a runbook, not just a pile of videos.",
    author: "Felix O.",
    role: "Independent creator",
  },
];

const FAQS = [
  {
    q: "What is the difference between Programs and Mentoring?",
    a: "Mentoring is 1:1 weekly sessions with a content strategist. Programs are cohort-based tracks with shared deliverables and templated workflows. Many teams combine them — Mentoring for strategic direction, Programs as the production backbone.",
  },
  {
    q: "How long does each program last?",
    a: "Creator Program is self-paced — most members reach a production-ready workflow in 6-8 weeks. Agency Program is a 12-week engagement, with team training and the option to renew. Enterprise Program runs 12 months with quarter-by-quarter business reviews.",
  },
  {
    q: "What deliverables do I get at the end?",
    a: "Creator Program graduates with a documented template library + community access. Agency Program teams leave with a configured multi-workspace setup + a trained editorial team. Enterprise Program customers ship with SSO-integrated publishing pipelines + a SOC2 audit trail.",
  },
  {
    q: "Is InstaEdit subscription included in the program fee?",
    a: "Creator Program is bundled with the InstaEdit Pro tier at no extra fee. Agency Program requires Pro; seats are billed per workspace. Enterprise Program includes Pro plus Business-tier licensing as part of the custom agreement.",
  },
  {
    q: "Why is Creator Program on a waitlist?",
    a: "We are scaling cohort onboarding to keep template quality high as we expand to 50+ markets. Join the waitlist and we will notify you when seats open — typically a 2-4 week queue, occasionally longer in peak months.",
  },
  {
    q: "Can I switch programs if I outgrow my tier?",
    a: "Yes — creators graduating into an agency use case can apply for Agency Program at any time. Agencies adding enterprise clients move to Enterprise Program with a SOC2 onboarding sprint. Your program manager coordinates the transition with no re-purchase of seats.",
  },
];

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
          <span>Programs designed for those who want to scale</span>
        </div>
        <h1 className="text-display-1 text-white max-w-[18ch] mx-auto">
          Programs for{" "}
          <span className="text-gradient-animated">creators, agencies</span> and brands.
        </h1>
        <p className="text-body-lg text-zinc-300/90 mt-7 max-w-[60ch] mx-auto">
          Choose the path that best fits your ambitions. From your first publication
          to enterprise content operations, we guide you through every phase.
        </p>
        <div className="flex flex-col sm:flex-row items-center justify-center gap-4 mt-8">
          <a
            href="#programs"
            className="inline-flex items-center gap-2 px-6 py-3 rounded-xl bg-white text-black font-semibold text-sm hover:bg-white/90 hover:shadow-[0_0_40px_-8px_rgba(255,255,255,0.3)] transition-all group"
          >
            Explore programs
            <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
          </a>
          <Link
            to="/login"
            className="inline-flex items-center gap-2 px-6 py-3 rounded-xl surface-glass border border-white/15 text-sm font-medium text-zinc-200 hover:border-violet-400/50 hover:text-white transition-all"
          >
            Start for free
          </Link>
        </div>
      </div>
    </section>
  );
}

function ProgramsList() {
  return (
    <section id="programs" className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora opacity-20 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">Our programs</div>
          <h2 className="text-display-2 text-white">
            A path for every growth level.
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            Whether you are a growing creator, an agency with many clients or a brand
            with structured needs, we have created a program tailored for you.
            Each program has a defined duration, deliverables and pricing — choose one and start.
          </p>
        </div>
        <div className="grid gap-8">
          {PROGRAMS.map((p, i) => (
            <div
              key={p.title}
              className={`surface-card p-8 lg:p-10 relative overflow-hidden animate-fade-up hover:border-violet-400/30 transition-all duration-300 ${["", "animation-delay-100", "animation-delay-200"][i]}`}
            >
              <div aria-hidden="true" className={`absolute -top-20 -right-20 w-56 h-56 rounded-full bg-gradient-to-br ${p.color} opacity-20 blur-3xl pointer-events-none`} />
              <div className="relative grid lg:grid-cols-[1fr_auto] gap-8 items-start">
                <div>
                  <div className="flex items-start gap-3 mb-5">
                    <div className={`w-12 h-12 rounded-xl bg-gradient-to-br ${p.color} flex items-center justify-center text-white shadow-lg shrink-0`}>
                      <p.icon className="w-6 h-6" />
                    </div>
                    <div>
                      <h3 className="text-display-3 text-white">{p.title}</h3>
                      <p className="text-sm text-violet-300/90 font-medium">{p.tagline}</p>
                      <div className="mt-2 inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full bg-white/[0.06] border border-white/10 text-[11px] text-zinc-300">
                        <CalendarClock className="w-3 h-3 text-violet-300" />
                        <span>{p.duration}</span>
                      </div>
                    </div>
                  </div>
                  <p className="text-sm text-zinc-400 leading-relaxed max-w-[60ch] mb-6">
                    {p.description}
                  </p>
                  <h4 className="text-eyebrow text-violet-300/90 mb-3">What you'll get</h4>
                  <ul className="grid sm:grid-cols-2 gap-3 mb-6">
                    {p.deliverables.map((d) => (
                      <li key={d} className="flex items-start gap-2 text-sm text-zinc-300">
                        <CheckCircle2 className="w-4 h-4 text-emerald-400 mt-0.5 shrink-0" />
                        <span>{d}</span>
                      </li>
                    ))}
                  </ul>
                  <h4 className="text-eyebrow text-zinc-500 mb-1.5">Requirements</h4>
                  <p className="text-sm text-zinc-400 leading-relaxed max-w-[60ch]">
                    {p.prerequisites}
                  </p>
                </div>
                <div className="flex flex-col items-start lg:items-end gap-3">
                  <Link
                    to="/login"
                    className="inline-flex items-center gap-2 px-6 py-3 rounded-xl bg-white text-black font-semibold text-sm hover:bg-white/90 transition-all group whitespace-nowrap"
                  >
                    {p.cta}
                    <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
                  </Link>
                  <div className="text-right">
                    <div className="text-eyebrow text-zinc-500 mb-1">Pricing</div>
                    <div className="text-sm font-semibold text-white">{p.pricing}</div>
                  </div>
                </div>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

function ComparisonSection() {
  return (
    <section id="compare" className="relative py-24 sm:py-32 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-emerald-500 w-[380px] h-[380px] -top-20 -right-32 animate-drift-slow opacity-30" />
        <div className="glow-orb bg-cyan-500 w-[340px] h-[340px] -bottom-32 -left-24 animate-drift-rev opacity-25" />
      </div>
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-emerald-300/90 mb-3 inline-flex items-center gap-2">
            <Building2 className="w-4 h-4" />
            Compare
          </div>
          <h2 className="text-display-2 text-white">
            Which program fits your team?
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            Side-by-side comparison of duration, pricing, support and best fit for each program tier.
            Updated automatically as the programs evolve.
          </p>
        </div>
        <div className="surface-glass border border-white/15 rounded-2xl overflow-hidden animate-fade-up">
          <div className="grid grid-cols-1 md:grid-cols-4 gap-px bg-white/5">
            {/* Header row derived from PROGRAMS.shortName */}
            <div className="bg-[#14141c]/80 px-5 py-4 text-eyebrow text-zinc-500">Feature</div>
            {PROGRAMS.map((p) => (
              <div
                key={`hdr-${p.shortName}`}
                className="bg-[#14141c]/80 px-5 py-4 text-sm font-semibold text-white"
              >
                {p.shortName}
              </div>
            ))}
            {/* Data rows derived from COMPARISON_ROWS + PROGRAMS field lookup */}
            {COMPARISON_ROWS.map((row) => (
              <div key={row.key} className="contents">
                <div className="bg-[#14141c]/70 px-5 py-4 text-sm text-zinc-300">
                  {row.label}
                </div>
                {PROGRAMS.map((p) => (
                  <div
                    key={`${p.shortName}-${row.key}`}
                    className="bg-[#14141c]/70 px-5 py-4 text-sm text-zinc-300"
                  >
                    {String(p[row.key])}
                  </div>
                ))}
              </div>
            ))}
          </div>
        </div>
        <p className="text-xs text-zinc-500 mt-4 max-w-[58ch]">
          All programs require an active InstaEdit workspace. Need help choosing?{" "}
          <a
            href="mailto:hello@instaedit.org?subject=Programs%20Question"
            className="text-violet-300/90 hover:text-white transition-colors underline-offset-2 hover:underline"
          >
            Talk to the team.
          </a>
        </p>
      </div>
    </section>
  );
}

function MentoringSection() {
  return (
    <section id="mentoring" className="relative py-24 sm:py-32 overflow-hidden">
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-pink-500 w-[380px] h-[380px] -top-20 -right-32 animate-drift-slow opacity-40" />
        <div className="glow-orb bg-violet-500 w-[340px] h-[340px] -bottom-32 -left-24 animate-drift-rev opacity-30" />
      </div>
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-pink-300/90 mb-3">Mentoring</div>
          <h2 className="text-display-2 text-white">
            Don't get left behind.{" "}
            <span className="text-gradient-animated">Learn from those who have already scaled.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            Our mentoring programs set you up to get the most out of InstaEdit,
            with experts who help you build a sustainable content system.
          </p>
        </div>
        <div className="grid sm:grid-cols-2 lg:grid-cols-3 gap-5">
          {MENTORING.map((m, i) => (
            <div
              key={m.title}
              className={`surface-card p-6 animate-fade-up ${["", "animation-delay-100", "animation-delay-200"][i]}`}
            >
              <div className="w-11 h-11 rounded-xl bg-gradient-to-br from-pink-500/20 to-violet-500/20 flex items-center justify-center text-pink-300 mb-4 ring-1 ring-pink-400/20">
                <m.icon className="w-5 h-5" />
              </div>
              <h3 className="text-display-3 text-white mb-2">{m.title}</h3>
              <p className="text-sm text-zinc-400 leading-relaxed">{m.description}</p>
            </div>
          ))}
        </div>
        <div className="mt-10 text-center">
          <Link
            to="/mentoring"
            className="inline-flex items-center gap-2 px-6 py-3 rounded-xl bg-white text-black font-semibold text-sm hover:bg-white/90 hover:shadow-[0_0_40px_-8px_rgba(255,255,255,0.3)] transition-all group"
          >
            Explore mentoring
            <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
          </Link>
        </div>
      </div>
    </section>
  );
}

function FAQSection() {
  return (
    <section id="faq" className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div aria-hidden="true" className="absolute inset-0 pointer-events-none">
        <div className="glow-orb bg-cyan-400 w-[360px] h-[360px] -top-20 -right-32 animate-drift-slow opacity-40" />
        <div className="glow-orb bg-violet-500 w-[320px] h-[320px] -bottom-32 -left-24 animate-drift-rev opacity-30" />
      </div>
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-cyan-300/90 mb-3">Frequently asked</div>
          <h2 className="text-display-2 text-white">Questions answered.</h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            Everything you want to know about how the programs work, who they support,
            and what to expect after you join.
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

function CTASection() {
  return (
    <section className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="surface-glass border border-white/15 rounded-2xl p-8 lg:p-12 relative overflow-hidden text-center animate-fade-up">
          <div aria-hidden="true" className="absolute inset-0 cta-glow opacity-30 pointer-events-none" />
          <div className="relative">
            <h2 className="text-display-2 text-white mb-4">
              Ready to choose your program?
            </h2>
            <p className="text-body-lg text-zinc-400 max-w-[55ch] mx-auto mb-8">
              Start for free and discover which path best fits your goals.
              You can always upgrade to a higher program when ready.
            </p>
            <div className="flex flex-col sm:flex-row items-center justify-center gap-4">
              <Link
                to="/login"
                className="inline-flex items-center gap-2 px-8 py-3.5 rounded-xl bg-white text-black font-semibold text-sm hover:bg-white/90 hover:shadow-[0_0_40px_-8px_rgba(255,255,255,0.3)] transition-all group"
              >
                Start for free
                <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
              </Link>
              <Link
                to="/mentoring"
                className="inline-flex items-center gap-2 px-8 py-3.5 rounded-xl surface-glass border border-white/15 text-sm font-medium text-zinc-200 hover:border-violet-400/50 hover:text-white transition-all"
              >
                Want guidance?
              </Link>
              <a
                href="mailto:hello@instaedit.org?subject=Programs%20Question"
                className="inline-flex items-center gap-2 px-8 py-3.5 rounded-xl surface-glass border border-white/15 text-sm font-medium text-zinc-200 hover:border-violet-400/50 hover:text-white transition-all"
              >
                Talk to the team
              </a>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}

function TestimonialsSection() {
  return (
    <section id="testimonials" className="relative py-24 sm:py-32 overflow-hidden bg-elevated">
      <div aria-hidden="true" className="absolute inset-0 hero-aurora opacity-20 pointer-events-none" />
      <div className="relative mx-auto max-w-7xl px-6">
        <div className="max-w-3xl mb-16 animate-fade-up">
          <div className="text-eyebrow text-violet-300/90 mb-3">From members</div>
          <h2 className="text-display-2 text-white">
            What those who joined say.
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            Operators, agencies and brand teams who use the Programs as their production backbone.
          </p>
        </div>
        <div className="grid sm:grid-cols-2 lg:grid-cols-3 gap-5">
          {PROGRAM_TESTIMONIALS.map((t, i) => (
            <div
              key={t.author}
              className={`surface-card p-6 animate-fade-up ${["", "animation-delay-100", "animation-delay-200"][i]}`}
            >
              <MessageCircle className="w-5 h-5 text-violet-300 mb-4" />
              <p className="text-sm text-zinc-300 leading-relaxed mb-5">&ldquo;{t.quote}&rdquo;</p>
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

export function Programs() {
  return (
    <div className="min-h-screen bg-[#030308]">
      <MarketingNav links={NAV_LINKS} />
      <main>
        <Hero />
        <ProgramsList />
        <ComparisonSection />
        <TestimonialsSection />
        <MentoringSection />
        <FAQSection />
        <CTASection />
      </main>
      <MarketingFooter />
    </div>
  );
}

export default Programs;
