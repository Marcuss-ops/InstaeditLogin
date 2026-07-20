export function PrivacyPolicy() {
  return (
    <div className="min-h-screen bg-[#030308] text-[#e8e8ef] py-16 px-6">
      <main className="max-w-[800px] mx-auto">
        <p className="text-[#9aa0aa] text-[13px] mb-2">Ultimo aggiornamento: 9 luglio 2026</p>
        <h1 className="text-[clamp(28px,4vw,42px)] font-extrabold tracking-[-0.02em] mb-8 text-white">
          Informativa sulla Privacy
        </h1>

        <p className="text-[#9aa0aa] leading-relaxed mb-7">
          La presente Informativa sulla Privacy descrive come <strong className="text-white">InstaEdit</strong> ("noi", "ci" o "nostro")
          raccoglie, utilizza e condivide le informazioni personali degli utenti che utilizzano il nostro sito web e i nostri servizi
          (collettivamente, i "Servizi"). Utilizzando InstaEdit, l'utente acconsente alla raccolta e all'uso delle informazioni
          secondo quanto descritto in questa informativa.
        </p>

        <LegalSection title="1. Informazioni che raccogliamo">
          <p>Quando l'utente si registra e utilizza <strong className="text-white">InstaEdit</strong>, possiamo raccogliere i seguenti tipi di informazioni:</p>
          <ul>
            <li><strong>Informazioni sull'account:</strong> nome, indirizzo email e informazioni del profilo quando si crea un account InstaEdit o si accede tramite una piattaforma di terze parti.</li>
            <li><strong>Dati dei contenuti:</strong> video, didascalie, miniature e altri contenuti caricati o creati con InstaEdit.</li>
            <li><strong>Dati dei social media:</strong> quando si collegano gli account social, accediamo alle informazioni del profilo pubblico, ai contenuti e ai dati di analytics consentiti dalle API di ciascuna piattaforma.</li>
            <li><strong>Dati di utilizzo:</strong> informazioni su come l'utente interagisce con InstaEdit, incluse le funzionalità utilizzate e le metriche di performance.</li>
            <li><strong>Informazioni sul dispositivo:</strong> tipo di browser, indirizzo IP, tipo di dispositivo e sistema operativo per scopi di sicurezza e analytics.</li>
          </ul>
        </LegalSection>

        <LegalSection title="2. Come utilizziamo le informazioni">
          <p>InstaEdit utilizza le informazioni raccolte per i seguenti scopi:</p>
          <ul>
            <li>Fornire, mantenere e migliorare il Servizio InstaEdit.</li>
            <li>Consentire la pubblicazione e la programmazione di contenuti attraverso gli account social collegati.</li>
            <li>Generare modifiche video AI, sottotitoli e miniature in base ai contenuti dell'utente.</li>
            <li>Analizzare l'utilizzo del Servizio e ottimizzare le performance.</li>
            <li>Comunicare con l'utente in merito all'account, agli aggiornamenti e alle richieste di supporto.</li>
            <li>Garantire la sicurezza e l'integrità del nostro Servizio.</li>
            <li>Rispettare obblighi legali e far rispettare i Termini di Servizio.</li>
          </ul>
        </LegalSection>

        <LegalSection title="3. Come condividiamo le informazioni">
          <p>InstaEdit <strong>non</strong> vende le informazioni personali degli utenti. Possiamo condividere le informazioni nelle seguenti circostanze limitate:</p>
          <ul>
            <li><strong>Fornitori di servizi:</strong> lavoriamo con fornitori di terze parti fidati che elaborano dati per nostro conto in base a rigorosi accordi di riservatezza.</li>
            <li><strong>Piattaforme di social media:</strong> quando l'utente pubblica contenuti tramite InstaEdit, tali contenuti vengono trasmessi alle piattaforme che ha collegato.</li>
            <li><strong>Conformità legale:</strong> possiamo divulgare informazioni se richiesto dalla legge, da un ordine del tribunale o da una normativa governativa.</li>
            <li><strong>Trasferimenti aziendali:</strong> in connessione con fusioni, acquisizioni o vendite di asset, le informazioni dell'utente possono essere trasferite come parte di tale transazione.</li>
          </ul>
        </LegalSection>

        <LegalSection title="4. Conservazione dei dati">
          <p>InstaEdit conserva le informazioni personali dell'utente per tutta la durata dell'account o per il tempo necessario a fornire il Servizio. L'utente può richiedere la cancellazione dell'account e dei dati associati in qualsiasi momento contattandoci.</p>
        </LegalSection>

        <LegalSection title="5. Diritti dell'utente">
          <p>In base alla propria giurisdizione, l'utente può avere i seguenti diritti riguardanti i propri dati:</p>
          <ul>
            <li>Accedere, correggere o cancellare le proprie informazioni personali.</li>
            <li>Opporsi a o limitare determinate attività di elaborazione.</li>
            <li>Portabilità dei dati: ricevere i propri dati in un formato strutturato e di uso comune.</li>
            <li>Ritirare il consenso in qualsiasi momento quando l'elaborazione è basata sul consenso.</li>
            <li>Presentare reclamo all'autorità locale per la protezione dei dati.</li>
          </ul>
          <p className="mt-2.5">
            Per esercitare uno di questi diritti, contattaci all'indirizzo{" "}
            <a href="mailto:hello@instaedit.org" className="text-[#0A84FF] hover:underline">
              hello@instaedit.org
            </a>.
          </p>
        </LegalSection>

        <LegalSection title="6. Cookie e tracciamento">
          <p>InstaEdit utilizza cookie e tecnologie simili per autenticare gli utenti, ricordare le preferenze e analizzare l'utilizzo del Servizio. L'utente può controllare i cookie tramite le impostazioni del proprio browser.</p>
        </LegalSection>

        <LegalSection title="7. Sicurezza dei dati">
          <p>InstaEdit implementa misure di sicurezza standard del settore per proteggere le informazioni dell'utente, inclusa la crittografia in transito (TLS) e a riposo.</p>
        </LegalSection>

        <LegalSection title="8. Privacy dei minori">
          <p>InstaEdit non è destinato a individui di età inferiore ai 13 anni. Non raccogliamo consapevolmente informazioni personali da bambini.</p>
        </LegalSection>

        <LegalSection title="9. Piattaforme di terze parti">
          <p>InstaEdit si integra con piattaforme di terze parti tra cui TikTok, YouTube, Instagram, Facebook e X/Twitter. Quando si collegano questi account, si applicano anche le politiche sulla privacy di tali piattaforme.</p>
        </LegalSection>

        <LegalSection title="10. Modifiche a questa informativa">
          <p>InstaEdit può aggiornare questa Informativa sulla Privacy di volta in volta. L'utente verrà informato di eventuali modifiche sostanziali pubblicando l'informativa aggiornata su questa pagina.</p>
        </LegalSection>

        <LegalSection title="11. Contattaci">
          <p>Per domande su questa Informativa sulla Privacy, contattaci:</p>
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
        .legal-section ul, section ul { padding-left: 22px; margin-top: 10px; display: flex; flex-direction: column; gap: 8px; }
        section li { color: #9aa0aa; line-height: 1.7; }
      `}</style>
    </section>
  );
}
