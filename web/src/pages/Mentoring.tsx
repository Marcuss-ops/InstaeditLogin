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
  { label: "Come funziona", href: "/#pipeline" },
  { label: "Workflow", href: "/#workflow" },
  { label: "Features", href: "/#features" },
  { label: "Agenzie", href: "/#agency" },
  { label: "Programmi", to: "/programs" },
  { label: "Mentoring", to: "/mentoring" },
  { label: "Chi siamo", href: "/#who-are-we" },
];

const MENTORING_PATH = [
  {
    step: "01",
    icon: Target,
    title: "Definisci il tuo obiettivo",
    description: "Iniziamo con una call di scoperta per capire dove sei, dove vuoi arrivare e qual è il tuo collo di bottiglia nella produzione di contenuti."
  },
  {
    step: "02",
    icon: Video,
    title: "Costruisci il tuo workflow",
    description: "Ti aiutiamo a configurare InstaEdit sul tuo account, importare i canali e impostare i template che userai ogni giorno."
  },
  {
    step: "03",
    icon: Sparkles,
    title: "Ricevi feedback settimanale",
    description: "Ogni settimana rivediamo i tuoi contenuti, analizziamo le metriche e affiniamo la strategia per migliorare reach e engagement."
  },
  {
    step: "04",
    icon: Award,
    title: "Scala in autonomia",
    description: "Dopo il percorso avrai un sistema ripetibile per produrre e pubblicare contenuti a volume, senza dipendere da un team esterno."
  },
];

const PACKAGES = [
  {
    title: "Starter Mentoring",
    tagline: "Per creator che partono da zero",
    features: [
      "4 sessioni 1-to-1 da 45 minuti",
      "Audit del canale e del calendario editoriale",
      "Setup di InstaEdit e onboarding",
      "Accesso alla community dei creator",
    ],
    color: "from-violet-500 to-purple-500",
    cta: "Inizia con Starter",
  },
  {
    title: "Growth Mentoring",
    tagline: "Per chi vuole passare al livello successiva",
    features: [
      "8 sessioni 1-to-1 da 60 minuti",
      "Content strategy e piano editoriale mensile",
      "Analisi metriche e ottimizzazione post",
      "Supporto WhatsApp per domande rapide",
    ],
    color: "from-cyan-500 to-blue-500",
    cta: "Scegli Growth",
  },
  {
    title: "Team Mentoring",
    tagline: "Per team e agenzie che scalano",
    features: [
      "12 sessioni per il team",
      "Workflow multi-account e automazioni",
      "Formazione del team editoriale",
      "Quarterly business review",
    ],
    color: "from-emerald-500 to-teal-500",
    cta: "Contatta vendite",
  },
];

const TESTIMONIALS = [
  {
    quote: "In tre mesi ho passato da 2 a 12 post a settimana senza assumere nessuno. Il mentoring mi ha dato il workflow giusto.",
    author: "Sara M.",
    role: "Creator tech",
  },
  {
    quote: "Finalmente il mio team non perde più ore tra upload e riformattazioni. Il percorso ci ha fatto risparmiare decine di ore al mese.",
    author: "Marco B.",
    role: "Content Strategist",
  },
  {
    quote: "Ho capito come usare l'AI non per sostituirmi, ma per amplificare il mio stile. I risultati sulle view sono cresciuti costantemente.",
    author: "Giulia T.",
    role: "Creator lifestyle",
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
          <span>Mentoring per creator e team ambiziosi</span>
        </div>
        <h1 className="text-display-1 text-white max-w-[18ch] mx-auto">
          Non usare InstaEdit da solo.{" "}
          <span className="text-gradient-animated">Cresci con un mentore.</span>
        </h1>
        <p className="text-body-lg text-zinc-300/90 mt-7 max-w-[60ch] mx-auto">
          Un percorso guidato per padroneggiare la produzione di contenuti con l'AI,
          costruire un workflow scalabile e raggiungere i tuoi obiettivi editoriali.
        </p>
        <div className="flex flex-col sm:flex-row items-center justify-center gap-4 mt-8">
          <a
            href="#packages"
            className="inline-flex items-center gap-2 px-6 py-3 rounded-xl bg-white text-black font-semibold text-sm hover:bg-white/90 hover:shadow-[0_0_40px_-8px_rgba(255,255,255,0.3)] transition-all group"
          >
            Scegli il tuo percorso
            <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
          </a>
          <Link
            to="/programs"
            className="inline-flex items-center gap-2 px-6 py-3 rounded-xl surface-glass border border-white/15 text-sm font-medium text-zinc-200 hover:border-violet-400/50 hover:text-white transition-all"
          >
            Vedi i programmi
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
          <div className="text-eyebrow text-violet-300/90 mb-3">Il percorso</div>
          <h2 className="text-display-2 text-white">
            Da obiettivo a workflow, in 4 passi.
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            Un percorso pratico e personalizzato per imparare a usare InstaEdit al massimo
            e costruire una macchina da contenuti sostenibile.
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
                <span className="text-eyebrow text-zinc-500 tabular-nums">Passo {item.step}</span>
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
          <div className="text-eyebrow text-pink-300/90 mb-3">Pacchetti</div>
          <h2 className="text-display-2 text-white">
            Scegli il mentoring che fa per te.
          </h2>
          <p className="text-body-lg text-zinc-400 mt-5 max-w-[58ch]">
            Tutti i pacchetti includono accesso alla piattaforma e supporto via email.
            Puoi passare a un pacchetto superiore in qualsiasi momento.
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
                <p className="text-sm text-violet-300/90 font-medium mb-5">{p.tagline}</p>
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
          <div className="text-eyebrow text-violet-300/90 mb-3">Testimonianze</div>
          <h2 className="text-display-2 text-white">
            Cosa dicono chi ha già iniziato.
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
              Pronto a crescere con un mentore?
            </h2>
            <p className="text-body-lg text-zinc-400 max-w-[55ch] mx-auto mb-8">
              Prenota una call scoperta gratuita e raccontaci i tuoi obiettivi.
              Ti proporremo il percorso più adatto a te.
            </p>
            <div className="flex flex-col sm:flex-row items-center justify-center gap-4">
              <a
                href="mailto:hello@instaedit.org?subject=Richiesta%20Mentoring"
                className="inline-flex items-center gap-2 px-8 py-3.5 rounded-xl bg-white text-black font-semibold text-sm hover:bg-white/90 hover:shadow-[0_0_40px_-8px_rgba(255,255,255,0.3)] transition-all group"
              >
                Prenota una call
                <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
              </a>
              <Link
                to="/programs"
                className="inline-flex items-center gap-2 px-8 py-3.5 rounded-xl surface-glass border border-white/15 text-sm font-medium text-zinc-200 hover:border-violet-400/50 hover:text-white transition-all"
              >
                Esplora i programmi
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
        <CTASection />
      </main>
      <MarketingFooter />
    </div>
  );
}

export default Mentoring;
