/**
 * Tutto il testo della landing "DonneTube" in un unico punto.
 *
 * Questa pagina è volutamente separata dalle altre landing del sito
 * (/, /programs, /mentoring): ha navigazione, footer, identità e
 * contenuti propri, in italiano.
 *
 * Per modificare qualsiasi copia basta cambiare i valori qui sotto:
 * ogni sezione della pagina legge da questo file, quindi non serve
 * toccare i componenti.
 */

export const SEO = {
  title: "DonneTube — I Tuoi Primi $2.000/Mese Con I Video Faceless",
  description:
    "Trasforma YouTube in un flusso di cassa semi-automatico da casa. Pipeline 90% eseguita dall'AI, monetizzazione sbloccata in pochi giorni e affiancamento 1-on-1 per donne che partono da zero.",
  canonical: "https://app.instaedit.org/donnetube",
} as const;

export const NAV = {
  brand: "DonneTube",
  links: [
    { label: "Come Funziona", href: "#come-funziona" },
    { label: "Programmi", href: "#guadagni" },
    { label: "Risultati", href: "#risultati" },
    { label: "FAQ", href: "#faq" },
    { label: "Contatti", href: "#contatti" },
  ],
  badge: {
    pipeline: "Pipeline semi-automatizzata — 90% eseguita dall'AI",
    limited: "Posti limitati — Solo 10 nuove studentesse questo mese",
  },
  cta: "Pronta a Iniziare?",
} as const;

export const HERO = {
  badge: "Pipeline semi-automatizzata • 90% eseguita dall'AI",
  badgeLimited: "Limitata a 10 donne questo mese",
  titleStart: "I Tuoi Primi $2.000/Mese Con I Video Faceless,",
  titleAccent: "In Pilota Automatico.",
  subtitleTop:
    "Nessuna Telecamera. Nessun Montaggio. Nessuno Stress Da Schedule.",
  subtitle:
    "Smettila di sprecare mesi cercando di capire gli algoritmi o registrando video la sera dopo lavoro. La nostra AI produce i video per te, il nostro setup ti sblocca subito la monetizzazione e il nostro affiancamento 1-on-1 ti guida fino al tuo primo guadagno reale.",
  ctaPrimary: "Prenota La Tua Chiamata Strategica",
  ctaSecondary: "Vedi I Risultati Reali",
  stats: [
    { value: "$2.150/mese", label: "Guadagno medio delle studentesse" },
    { value: "14 Giorni", label: "Tempo medio per il primo incasso" },
    { value: "90%", label: "Esecuzione automatizzata tramite AI" },
  ],
} as const;

export const PROBLEM = {
  eyebrow: "Il Problema",
  title: "Stai perdendo tempo ed energie ogni singolo giorno.",
  subtitle:
    "Vuoi creare una seconda entrata per la tua famiglia o per lasciare il 9-5, ma il montaggio ti ruba 15 ore a settimana tra lavoro e casa. Ti spaventa l'idea di mettere la faccia online o non ci capisci nulla di tecnologia. Pubblichi da mesi per raccogliere solo 50 visualizzazioni e 0 euro.",
  items: [
    {
      title: "Il montaggio ti ruba 15+ ore a settimana",
      description:
        "Togliendo tempo ai figli o al tuo riposo, ogni giorno, mentre lavori e gestisci la casa.",
    },
    {
      title: "Hai paura di mostrare il tuo volto",
      description:
        "O di esporti al giudizio di amici e parenti. Ma non devi: i canali Faceless esistono e funzionano.",
    },
    {
      title: "Zero competenze tecniche",
      description:
        "Software complessi e strumenti difficili ti bloccano prima ancora di iniziare. Non devi imparare nulla di tutto questo.",
    },
    {
      title: "L'algoritmo richiede costanza quotidiana",
      description:
        "Ma con i tuoi impegni è impossibile stare al passo. Non è pigrizia: è un sistema che non è stato pensato per te.",
    },
  ],
} as const;

export const SHORTCUT = {
  eyebrow: "La Scorciatoia",
  title: "La via più semplice verso la tua indipendenza finanziaria.",
  subtitle:
    "Ti consegniamo le chiavi in mano: un sistema con indicizzazione algoritmica immediata, un'AI che genera video professionali partendo da una singola riga di testo e una mentor che ti dice esattamente cosa fare, senza farti perdere tempo.",
  items: [
    {
      title: "Setup Velocizzato Monetizzazione",
      description:
        "Onboarding pre-configurato per attivare il Partner Program in pochi giorni, non mesi.",
    },
    {
      title: "ChronoN AI",
      description:
        "Scrivi una frase e ottieni un video pronto per essere monetizzato. Nessun montaggio, nessuna competenza tecnica.",
    },
    {
      title: "1 Video = 7 Piattaforme",
      description:
        "Un singolo contenuto viene distribuito automaticamente su 7 social, moltiplicando la tua visibilità.",
    },
    {
      title: "Pipeline Semi-Automatica",
      description:
        "Contenuto giornaliero garantito dedicando solo ~3 ore a settimana. Tu approvi, il sistema lavora.",
    },
  ],
} as const;

export const EARNINGS = {
  eyebrow: "I Guadagni",
  title: "Quanto puoi guadagnare realisticamente?",
  subtitle:
    "Tabelle di guadagno basate sulle nostre attuali studentesse (mamme, lavoratrici e donne che partivano da zero). Più canali automatizzi, più flussi di cassa ricorrenti crei.",
  disclaimer:
    "I risultati mostrati non sono una garanzia di guadagno. Dipendono dal tuo impegno, dalla nicchia e dal mercato.",
  rows: [
    {
      level: "1 Canale",
      tag: "Start",
      earning: "$1.000 – $1.500 / mese",
      reach: "300k visite × $3,50–$5,00 RPM",
    },
    {
      level: "3 Canali",
      tag: "Multi-lingua",
      earning: "$2.500 – $5.000 / mese",
      reach: "~500k visite combinate × $5–$10 RPM",
    },
    {
      level: "Portfolio Canali",
      tag: "Level 3",
      earning: "$10.000+ / mese",
      reach: "1,2M+ visite × $8,50+ RPM (Nicchie Tier-1 USA/EU)",
    },
  ],
  mathTitle: "💡 Come funziona la matematica dei guadagni:",
  mathParagraphs: [
    "Calcolato su una base di 300.000 visualizzazioni mensili con un RPM medio di $3.50 – $5.00 in nicchie ad alto valore. Il caso mediano è 300k × $4 ≈ $1.200/mese per singolo canale.",
    "L'RPM è la quota netta pagata da YouTube ai creator. Scali semplicemente aumentando il numero di canali gestiti dall'AI, non lavorando più ore.",
  ],
} as const;

export const HOW_IT_WORKS = {
  eyebrow: "Come Funziona",
  title: "Semi-automatico. Massimo guadagno.",
  subtitle:
    "Nessuna telecamera. Nessun software di montaggio. Zero esperienza pregressa. Il nostro sistema eseguito al 90% dall'AI trasforma un'idea in contenuti giornalieri su 7 piattaforme — progettati per generare entrate.",
  steps: [
    {
      step: "1",
      title: "L'AI Crea, Tu Incassi.",
      description:
        "Digita una riga di testo e ChronoN AI genera un video professionale pronto per la monetizzazione. Niente microfono, niente webcam. L'AI gestisce tutto: dalla sceneggiatura al rendering finale.",
    },
    {
      step: "2",
      title: "7 Piattaforme con 1 Solo Video.",
      description:
        "Un video generato dall'AI viene convertito e pubblicato automaticamente su YouTube Shorts, TikTok, Instagram Reels, Facebook, X e altre — moltiplicando la tua visibilità per 7x.",
    },
    {
      step: "3",
      title: "Monetizzazione dal Primo Giorno.",
      description:
        "Il nostro setup Fast-Track Partner fa passare il tuo canale attraverso la revisione in pochi giorni, non mesi — permettendoti di incassare subito dalla pubblicità, sponsorizzazioni e affiliazioni.",
    },
  ],
} as const;

export const RESULTS = {
  eyebrow: "I Risultati",
  title: "Donne reali. Entrate reali.",
  subtitle:
    "La maggior parte delle persone prova a creare contenuti per mesi senza guadagnare un centesimo. Le nostre studentesse arrivano al loro primo incasso in meno di due settimane, costruendo un secondo stipendio ricorrente. Clicca su qualsiasi screenshot per verificare i numeri.",
  stats: [
    { v: "$1.940", l: "Guadagno mediano mensile", d: "per studentessa attiva", color: "text-emerald-400" },
    { v: "14 Giorni", l: "Tempo medio per il primo incasso", d: "dal lancio del canale", color: "text-blue-400" },
    { v: "50+", l: "Canali attivi monetizzati", d: "gestiti dalle nostre studentesse", color: "text-violet-400" },
    { v: "90%", l: "Pipeline eseguita dall'AI", d: "tu approvi, il sistema lavora", color: "text-amber-400" },
  ],
  screenshots: [
    { img: "/results/result-1.jpg", alt: "Risultato canale YouTube — crescita primi 90 giorni", caption: "Crescita giorno per giorno su un canale finance" },
    { img: "/results/result-2.jpg", alt: "Risultato strategia contenuti — aumento RPM", caption: "Aumento RPM dopo il setup di monetizzazione" },
    { img: "/results/result-3.jpg", alt: "Risultato monetizzazione canale — Partner Program", caption: "Finestra di attivazione del Partner Program" },
    { img: "/results/result-4.jpg", alt: "Risultato performance video — ricavi 28 giorni", caption: "Ricavi a 28 giorni per singolo video" },
    { img: "/results/result-5.jpg", alt: "Risultato crescita creator — iscritti", caption: "Curva iscritti che supera i 1.000" },
    { img: "/results/result-6.jpg", alt: "Risultato multi-piattaforma — cross-post", caption: "Guadagni cross-post su 7 piattaforme" },
  ],
} as const;

export const TESTIMONIALS = {
  eyebrow: "Le Loro Storie",
  title: "Donne vere. ",
  titleAccent: "Parole vere.",
  subtitle:
    "Storie di studentesse che sono partite da zero e hanno costruito il loro primo flusso di cassa con i video faceless. Guarda e ascolta la loro esperienza:",
  videos: [
    "nHjJXnnf65o",
    "0ThcbcZlb5k",
    "CVCnF2JNwBI",
    "BbCpSok2H1o",
    "Uscs_mH1MiQ",
    "Wmq2r8xRaQg",
    "3BnyG5N3wbw",
    "VngvA7PpveQ",
  ],
} as const;

export const FOUNDER = {
  eyebrow: "Dalla Fondatrice",
  titleStart: "Come è nato",
  titleAccent: "DonneTube.",
  paragraphs: [
    "“Ho iniziato esattamente dove ti trovi tu ora: niente studio, niente competenze di montaggio, niente budget — solo la voglia di creare un'entrata mia che mi permettesse di passare più tempo con la mia famiglia senza la costante ansia del lavoro 9-5.",
    "Dover creare contenuti ovunque significava un incubo di 14 schede aperte sul computer, sottotitoli manuali, stress quotidiano... Sembrava un secondo lavoro a tempo pieno solo per premere 'pubblica'.",
    "Così ho sviluppato lo strumento che avrei voluto avere dal primo giorno. DonneTube automatizza l'intera filiera — dalla scrittura del testo fino alla pubblicazione su 7 piattaforme — permettendo a qualsiasi donna di crearsi un'indipendenza economica da casa, senza il lavoro pesante.”",
  ],
  stats: [
    { v: "1", l: "Fondato da una donna" },
    { v: "7", l: "Piattaforme" },
    { v: "90%", l: "Eseguito dall'AI" },
  ],
} as const;

export const FAQ = {
  eyebrow: "FAQ",
  title: "Domande Frequenti",
  subtitle:
    "Tutto quello che ti serve sapere prima di iniziare. Altre domande? Scrivici, siamo qui per te.",
  items: [
    {
      q: "Devo mostrare la mia faccia o usare la mia voce?",
      a: "Assolutamente no. Il sistema è progettato al 100% per canali Faceless. L'AI genera immagini, video B-roll e voci sintetiche iper-realistiche.",
    },
    {
      q: "Quanto tempo devo dedicarci a settimana?",
      a: "Circa 3 ore a settimana. L'AI fa il 90% del lavoro pesante; il tuo unico compito è revisionare i testi e approvare la pubblicazione.",
    },
    {
      q: "E se non capisco nulla di tecnologia o montaggio?",
      a: "Non devi montare nulla. Se sai usare una casella email o mandare un messaggio su WhatsApp, sei perfettamente in grado di usare il nostro sistema.",
    },
    {
      q: "Parto da zero iscritti e zero esperienza. Posso comunque iniziare?",
      a: "Sì: la maggior parte delle nostre studentesse partiva esattamente da lì. Il percorso è pensato per accompagnarti passo dopo passo, senza giudizio.",
    },
  ],
} as const;

export const FINAL_CTA = {
  badge: "Posti Limitati — Accettiamo solo 10 nuove studentesse questo mese per garantire un supporto 1-on-1 personalizzato.",
  titleStart: "Pronte a trasformare YouTube",
  titleAccent: "nel tuo stipendio mensile da casa?",
  subtitle:
    "Prenota una chiamata strategica gratuita. Analizzeremo la tua situazione e tracceremo la mappa esatta per farti raggiungere i tuoi primi $2.000/mese — anche se parti da zero esperienza e zero iscritte oggi.",
  cta: "Prenota La Tua Chiamata Strategica Gratuita",
  linkSecondary: "Scopri Come Funziona",
  smallPrint:
    "Nessun costo, nessun impegno. Se non è per te, ci stringiamo la mano e ognuna torna per la sua strada.",
} as const;

export const FOOTER = {
  brand: "DonneTube",
  description:
    "Trasforma YouTube in un flusso di cassa semi-automatico da casa. La nostra AI produce i contenuti e gestisce le pubblicazioni — tu approvi e incassi.",
  badge: "Pipeline Semi-Automatica • 90% Eseguita dall'AI",
  productHeading: "Prodotto",
  productLinks: [
    { label: "Come funziona", href: "#come-funziona" },
    { label: "Programmi", href: "#guadagni" },
    { label: "Risultati", href: "#risultati" },
    { label: "FAQ", href: "#faq" },
  ],
  legalHeading: "Legale",
  legal: [
    { label: "Privacy Policy", to: "/privacy" },
    { label: "Termini di Servizio", to: "/terms" },
    { label: "Gestione Dati", href: "/data-deletion.html" },
  ],
  mainSite: { label: "InstaEdit — sito principale", to: "/" },
  copyright:
    "Creato per le donne che vogliono una vera macchina da rendita, non un passatempo.",
} as const;
