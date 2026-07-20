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
    tagline: "Da creator a content machine",
    description:
      "Un percorso guidato per creator che vogliono passare da pochi post al mese a una macchina da contenuti strutturata: workflow, template, automazioni e accesso prioritario alle nuove funzioni di InstaEdit.",
    features: [
      "Onboarding 1-to-1 con il team di prodotto",
      "Template di contenuti ottimizzati per ogni piattaforma",
      "Accesso anticipato a nuove integrazioni",
      "Community esclusiva di creator",
    ],
    cta: "Unisciti alla lista d'attesa",
    color: "from-violet-500 to-purple-500",
  },
  {
    icon: Building2,
    title: "Agency Program",
    tagline: "Scala i tuoi clienti senza assumere",
    description:
      "Pensato per agenzie e content studio che gestiscono decine di clienti. Formazione dedicata, SLA, supporto prioritario e margini crescenti in base al volume.",
    features: [
      "Multi-workspace con billing separato",
      "Onboarding assistito per il tuo team",
      "Supporto prioritario con SLA",
      "Programma partner con vantaggi su volume",
    ],
    cta: "Diventa partner",
    color: "from-emerald-500 to-teal-500",
  },
  {
    icon: Briefcase,
    title: "Enterprise Program",
    tagline: "InstaEdit nella tua azienda",
    description:
      "Soluzione tailor-made per brand e aziende che vogliono integrare InstaEdit nei loro sistemi di content operations, con single sign-on, audit log e account manager dedicato.",
    features: [
      "SSO e provisioning utenti",
      "Audit log e compliance",
      "Account manager dedicato",
      "Training e workshop per il team",
    ],
    cta: "Contatta vendite",
    color: "from-cyan-500 to-blue-500",
  },
];

const MENTORING = [
  {
    icon: GraduationCap,
    title: "Mentoring 1-to-1",
    description: "Sessioni settimanali con esperti di content strategy per definire il tuo piano editoriale e ottimizzare le metriche."
  },
  {
    icon: CalendarClock,
    title: "Content Calendar Audit",
    description: "Revisione completa del tuo calendario editoriale con suggerimenti pratici su frequenza, formati e piattaforme."
  },
  {
    icon: Globe,
    title: "Multi-platform Setup",
    description: "Configurazione guidata di tutti i canali e automazioni per pubblicare su ogni piattaforma senza perdite di tempo."
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
          <span>Programmi pensati per chi vuole scalare</span>
        </div>
        <h1 className="text-display-1 text-white max-w-[18ch] mx-auto">
          Programmi per{" "}
          <span className="text-gradient-animated">creator, agenzie</span> e brand.
        </h1>
        <p className="text-body-lg text-zinc-300/90 mt-7 max-w-[60ch] mx-auto">
          Scegli il percorso più adatto alle tue ambizioni. Dalla prima pubblicazione
          alla content operations enterprise, ti guidiamo in ogni fase.
        </p>
        <div className="flex flex-col sm:flex-row items-center justify-center gap-4 mt-8">
          <a
            href="#programs"
            className="inline-flex items-center gap-2 px-6 py-3 rounded-xl bg-white text-black font-semibold text-sm hover:bg-white/90 hover:shadow-[0_0_40px_-8px_rgba(255,255,255,0.3)] transition-all group"
          >
            Esplora i programmi
            <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
          </a>
          <Link
            to="/login"
            className="inline-flex items-center gap-2 px-6 py-3 rounded-xl surface-glass border border-white/15 text-sm font-medium text-zinc-200 hover:border-violet-400/50 hover:text-white transition-all"
          >
            Inizia gratis
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
          <div className="text-eyebrow text-violet-300/90 mb-3">I nostri programmi</div>
          <h2 className="text-display-2 text-white">
            Un percorso per ogni livello di crescita.
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            Che tu sia un creator in crescita, un'agenzia con tanti clienti o un brand
            strutturato, abbiamo creato un programma su misura per te.
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
            Non lasciarti indietro.{" "}
            <span className="text-gradient-animated">Impara da chi ha già scalato.</span>
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            I nostri programmi di mentoring ti mettono nelle condizioni di sfruttare InstaEdit
            al massimo, con esperti che ti aiutano a costruire un sistema di contenuti sostenibile.
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
              Pronto a scegliere il tuo programma?
            </h2>
            <p className="text-body-lg text-zinc-400 max-w-[55ch] mx-auto mb-8">
              Inizia gratis e scopri quale percorso si adatta meglio ai tuoi obiettivi.
              Puoi sempre passare a un programma superiore quando sei pronto.
            </p>
            <div className="flex flex-col sm:flex-row items-center justify-center gap-4">
              <Link
                to="/login"
                className="inline-flex items-center gap-2 px-8 py-3.5 rounded-xl bg-white text-black font-semibold text-sm hover:bg-white/90 hover:shadow-[0_0_40px_-8px_rgba(255,255,255,0.3)] transition-all group"
              >
                Inizia gratis
                <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
              </Link>
              <a
                href="mailto:hello@instaedit.org"
                className="inline-flex items-center gap-2 px-8 py-3.5 rounded-xl surface-glass border border-white/15 text-sm font-medium text-zinc-200 hover:border-violet-400/50 hover:text-white transition-all"
              >
                Parla con il team
              </a>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}

const NAV_LINKS = [
  { label: "Come funziona", href: "/#pipeline" },
  { label: "Workflow", href: "/#workflow" },
  { label: "Features", href: "/#features" },
  { label: "Agenzie", href: "/#agency" },
  { label: "Programmi", to: "/programs" },
  { label: "Chi siamo", href: "/#who-are-we" },
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
