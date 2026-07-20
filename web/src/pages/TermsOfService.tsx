export function TermsOfService() {
  return (
    <div className="min-h-screen bg-[#030308] text-[#e8e8ef] py-16 px-6">
      <main className="max-w-[800px] mx-auto">
        <p className="text-[#9aa0aa] text-[13px] mb-2">Ultimo aggiornamento: 9 luglio 2026</p>
        <h1 className="text-[clamp(28px,4vw,42px)] font-extrabold tracking-[-0.02em] mb-8 text-white">
          Termini di Servizio
        </h1>

        <p className="text-[#9aa0aa] leading-relaxed mb-7">
          Benvenuto su <strong className="text-white">InstaEdit</strong>. I presenti Termini di Servizio ("Termini") regolano l'accesso e l'uso del sito web,
          dell'applicazione e dei servizi di InstaEdit (collettivamente, i "Servizi"), gestiti da InstaEdit, Inc. ("InstaEdit", "noi", "ci" o "nostro").
          Accedendo o utilizzando InstaEdit, l'utente accetta di essere vincolato da questi Termini.
        </p>

        <LegalSection title="1. Descrizione del Servizio">
          <p><strong className="text-white">InstaEdit</strong> è una piattaforma di creazione e pubblicazione video basata sull'intelligenza artificiale. InstaEdit consente agli utenti di generare, modificare, sottotitolare e pubblicare contenuti video attraverso gli account social collegati — inclusi TikTok, YouTube, Instagram, Facebook e X/Twitter — utilizzando flussi di lavoro AI automatizzati.</p>
        </LegalSection>

        <LegalSection title="2. Idoneità">
          <p>Per utilizzare InstaEdit è necessario avere almeno 13 anni. Utilizzando il Servizio, l'utente dichiara di soddisfare questi requisiti di idoneità.</p>
        </LegalSection>

        <LegalSection title="3. Responsabilità dell'account">
          <ul>
            <li>L'utente è responsabile del mantenimento della riservatezza delle credenziali del proprio account InstaEdit.</li>
            <li>L'utente si impegna a informarci immediatamente in caso di accesso non autorizzato.</li>
            <li>L'utente non può condividere le credenziali del proprio account con terze parti.</li>
            <li>InstaEdit si riserva il diritto di sospendere o terminare gli account che violino questi Termini.</li>
          </ul>
        </LegalSection>

        <LegalSection title="4. Contenuti dell'utente">
          <p>L'utente mantiene la proprietà dei contenuti che carica, crea o pubblica tramite InstaEdit ("Contenuti dell'utente"). Utilizzando il Servizio, l'utente concede a InstaEdit una licenza limitata, mondiale e non esclusiva per elaborare, modificare e trasmettere i propri Contenuti, esclusivamente nella misura necessaria a fornire il Servizio.</p>
          <p className="mt-3">L'utente dichiara di possedere tutti i diritti necessari sui Contenuti che fornisce.</p>
        </LegalSection>

        <LegalSection title="5. Comportamento vietato">
          <p>Utilizzando InstaEdit, l'utente accetta di non:</p>
          <ul>
            <li>Caricare o pubblicare contenuti illegali, dannosi, molestanti, diffamatori, osceni o altrimenti riprovevoli.</li>
            <li>Violare leggi, regolamenti o termini di servizio applicabili.</li>
            <li>Utilizzare il Servizio per distribuire spam, malware o condurre phishing.</li>
            <li>Decodificare, decompilare o tentare di estrarre il codice sorgente del Servizio.</li>
            <li>Utilizzare mezzi automatici (bot, scraper) per accedere al Servizio senza autorizzazione.</li>
            <li>Interferire con il corretto funzionamento del Servizio.</li>
          </ul>
        </LegalSection>

        <LegalSection title="6. Proprietà intellettuale">
          <p>InstaEdit e i suoi contenuti, funzionalità e funzionalità originali sono e rimarranno proprietà esclusiva di InstaEdit, Inc.</p>
        </LegalSection>

        <LegalSection title="7. Servizi di terze parti">
          <p>InstaEdit si integra con piattaforme di terze parti (TikTok, YouTube, Instagram, Facebook, X/Twitter e altri). L'uso di tali piattaforme è regolato dai rispettivi termini di servizio.</p>
        </LegalSection>

        <LegalSection title="8. Abbonamento e pagamento">
          <p>Alcune funzionalità di InstaEdit potrebbero richiedere un abbonamento a pagamento. I termini di abbonamento, i prezzi e le condizioni di rinnovo verranno presentati al momento dell'acquisto. Tutte le commissioni sono non rimborsabili, salvo quanto richiesto dalla legge applicabile.</p>
        </LegalSection>

        <LegalSection title="9. Esclusione di garanzie">
          <p>IL SERVIZIO VIENE FORNITO "COSÌ COME È" E "COME DISPONIBILE". NELLA MASSIMA MISURA CONSENTITA DALLA LEGGE, INSTAEDIT RINUNCIA A TUTTE LE GARANZIE, ESPRESSE O IMPLICITE.</p>
        </LegalSection>

        <LegalSection title="10. Limitazione di responsabilità">
          <p>NELLA MASSIMA MISURA CONSENTITA DALLA LEGGE, INSTAEDIT NON SARÀ RESPONSABILE PER EVENTUALI DANNI INDIRETTI, INCIDENTALI, SPECIALI, CONSEQUENZIALI O PUNITIVI DERIVANTI DALL'USO DEL SERVIZIO DELL'UTENTE.</p>
          <p className="mt-3">In nessun caso la responsabilità totale di InstaEdit potrà superare l'importo pagato dall'utente a InstaEdit nei dodici (12) mesi precedenti al reclamo.</p>
        </LegalSection>

        <LegalSection title="11. Indennizzo">
          <p>L'utente accetta di indennizzare e tenere indenne InstaEdit e le sue affiliate da qualsiasi rivendicazione, danno, responsabilità e spesa derivanti dall'uso del Servizio o dalla violazione di questi Termini da parte dell'utente.</p>
        </LegalSection>

        <LegalSection title="12. Terminazione">
          <p>InstaEdit può sospendere o terminare l'accesso dell'utente al Servizio in qualsiasi momento, con o senza motivo.</p>
        </LegalSection>

        <LegalSection title="13. Legge applicabile">
          <p>Questi Termini saranno regolati e interpretati in conformità con le leggi applicabili.</p>
        </LegalSection>

        <LegalSection title="14. Modifiche a questi Termini">
          <p>InstaEdit si riserva il diritto di modificare questi Termini in qualsiasi momento. L'utente verrà informato di modifiche sostanziali pubblicando i Termini aggiornati su questa pagina.</p>
        </LegalSection>

        <LegalSection title="15. Contatti">
          <p>Per domande su questi Termini, contattaci:</p>
          <p className="mt-3">
            Email:{" "}
            <a href="mailto:hello@instaedit.org" className="text-[#0A84FF] hover:underline">
              hello@instaedit.org
            </a>
          </p>
        </LegalSection>
      </main>
    </div>
  );
}

function LegalSection({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="mb-9">
      <h3 className="text-[clamp(18px,2.5vw,22px)] font-semibold text-white mb-3.5 tracking-[-0.3px]">
        {title}
      </h3>
      <div className="text-[#9aa0aa] leading-relaxed text-[15px]">
        {children}
      </div>
      <style>{`
        section ul { padding-left: 22px; margin-top: 10px; display: flex; flex-direction: column; gap: 8px; }
        section li { color: #9aa0aa; line-height: 1.7; }
      `}</style>
    </section>
  );
}
