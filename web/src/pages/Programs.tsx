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
} from "lucide-react";
import { MarketingNav } from "../components/layout/MarketingNav";
import { MarketingFooter } from "../components/layout/MarketingFooter";

const PROGRAMS = [
  {
    icon: Users,
    title: "Creator Program",
    tagline: "From creator to content machine",
    description:
      "A guided program for creators who want to go from a few posts per month to a structured content machine: workflow, templates, automations and early access to new InstaEdit features.",
    features: [
      "One-on-one onboarding with the product team",
      "Content templates optimized for every platform",
      "Early access to new integrations",
      "Exclusive creator community",
    ],
    cta: "Join the waitlist",
    color: "from-violet-500 to-purple-500",
  },
  {
    icon: Building2,
    title: "Agency Program",
    tagline: "Scale your clients without hiring",
    description:
      "Designed for agencies and content studios managing dozens of clients. Dedicated training, SLAs, priority support and increasing margins based on volume.",
    features: [
      "Multi-workspace with separate billing",
      "Assisted onboarding for your team",
      "Priority support with SLA",
      "Partner program with volume benefits",
    ],
    cta: "Become a partner",
    color: "from-emerald-500 to-teal-500",
  },
  {
    icon: Briefcase,
    title: "Enterprise Program",
    tagline: "InstaEdit in your company",
    description:
      "Tailor-made solution for brands and companies that want to integrate InstaEdit into their content operations systems, with single sign-on, audit log and dedicated account manager.",
    features: [
      "SSO and user provisioning",
      "Audit log and compliance",
      "Dedicated account manager",
      "Team training and workshops",
    ],
    cta: "Contact sales",
    color: "from-cyan-500 to-blue-500",
  },
];

const MENTORING = [
  {
    icon: GraduationCap,
    title: "One-on-one mentoring",
    description: "Weekly sessions with content strategy experts to define your editorial plan and optimize metrics."
  },
  {
    icon: CalendarClock,
    title: "Content Calendar Audit",
    description: "Complete review of your editorial calendar with practical suggestions on frequency, formats and platforms."
  },
  {
    icon: Globe,
    title: "Multi-platform Setup",
    description: "Guided setup of all channels and automations to publish on every platform without wasting time."
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
          </p>
        </div>
        <div className="grid gap-8">
          {PROGRAMS.map((p, i) => (
            <div
              key={p.title}
              className={`surface-card p-8 lg:p-10 relative overflow-hidden animate-fade-up hover:border-violet-400/30 transition-all duration-300 ${["", "animation-delay-100", "animation-delay-200"][i]}`}
            >
              <div aria-hidden="true" className={`absolute -top-20 -right-20 w-56 h-56 rounded-full bg-gradient-to-br ${p.color} opacity-20 blur-3xl pointer-events-none`} />
              <div className="relative grid lg:grid-cols-[1fr_auto] gap-8 items-center">
                <div>
                  <div className="flex items-center gap-3 mb-4">
                    <div className={`w-12 h-12 rounded-xl bg-gradient-to-br ${p.color} flex items-center justify-center text-white shadow-lg`}>
                      <p.icon className="w-6 h-6" />
                    </div>
                    <div>
                      <h3 className="text-display-3 text-white">{p.title}</h3>
                      <p className="text-sm text-violet-300/90 font-medium">{p.tagline}</p>
                    </div>
                  </div>
                  <p className="text-sm text-zinc-400 leading-relaxed max-w-[60ch] mb-6">
                    {p.description}
                  </p>
                  <ul className="grid sm:grid-cols-2 gap-3 mb-6">
                    {p.features.map((f) => (
                      <li key={f} className="flex items-start gap-2 text-sm text-zinc-300">
                        <CheckCircle2 className="w-4 h-4 text-emerald-400 mt-0.5 shrink-0" />
                        <span>{f}</span>
                      </li>
                    ))}
                  </ul>
                </div>
                <div className="flex flex-col items-start lg:items-end gap-4">
                  <Link
                    to="/login"
                    className="inline-flex items-center gap-2 px-6 py-3 rounded-xl bg-white text-black font-semibold text-sm hover:bg-white/90 transition-all group whitespace-nowrap"
                  >
                    {p.cta}
                    <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
                  </Link>
                </div>
              </div>
            </div>
          ))}
        </div>
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
            <div key={m.title} className={`surface-card p-6 animate-fade-up ${["", "animation-delay-100", "animation-delay-200"][i]}`}>
              <div className="w-11 h-11 rounded-xl bg-gradient-to-br from-pink-500/20 to-violet-500/20 flex items-center justify-center text-pink-300 mb-4 ring-1 ring-pink-400/20">
                <m.icon className="w-5 h-5" />
              </div>
              <h3 className="text-display-3 text-white mb-2">{m.title}</h3>
              <p className="text-sm text-zinc-400 leading-relaxed">{m.description}</p>
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
                href="mailto:hello@instaedit.org"
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

const NAV_LINKS = [
  { label: "How it works", href: "/#pipeline" },
  { label: "Workflow", href: "/#workflow" },
  { label: "Features", href: "/#features" },
  { label: "Agencies", href: "/#agency" },
  { label: "Programs", to: "/programs" },
  { label: "Mentoring", to: "/mentoring" },
  { label: "About us", href: "/#who-are-we" },
];

export function Programs() {
  return (
    <div className="min-h-screen bg-[#030308]">
      <MarketingNav links={NAV_LINKS} />
      <main>
        <Hero />
        <ProgramsList />
        <MentoringSection />
        <CTASection />
      </main>
      <MarketingFooter />
    </div>
  );
}

export default Programs;
