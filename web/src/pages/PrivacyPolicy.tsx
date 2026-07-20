export function PrivacyPolicy() {
  return (
    <div className="min-h-screen bg-[#030308] text-[#e8e8ef] py-16 px-6">
      <main className="max-w-[800px] mx-auto">
        <p className="text-[#9aa0aa] text-[13px] mb-2">Last updated: July 9, 2026</p>
        <h1 className="text-[clamp(28px,4vw,42px)] font-extrabold tracking-[-0.02em] mb-8 text-white">
          Privacy Policy
        </h1>

        <p className="text-[#9aa0aa] leading-relaxed mb-7">
          La presente Privacy Policy descrive come <strong className="text-white">InstaEdit</strong> ("noi", "ci" o "nostro")
          raccoglie, utilizza e condivide le informazioni personali degli utenti che utilizzano il nostro sito web e i nostri servizi
          (collettivamente, i "Servizi"). Utilizzando InstaEdit, l'utente acconsente alla raccolta e all'uso delle informazioni
          secondo quanto descritto in questa informativa.
        </p>

        <LegalSection title="1. Information We Collect">
          <p>Quando l'utente si registra e utilizza <strong className="text-white">InstaEdit</strong>, possiamo raccogliere i seguenti tipi di informazioni:</p>
          <ul>
            <li><strong>Informazioni sull'account:</strong> nome, indirizzo email e informazioni del profilo quando si crea un account InstaEdit o si accede tramite una piattaforma di terze parti.</li>
            <li><strong>Dati dei contenuti:</strong> video, didascalie, miniature e altri contenuti caricati o creati con InstaEdit.</li>
            <li><strong>Dati dei social media:</strong> quando si collegano gli account social, accediamo alle informazioni del profilo pubblico, ai contenuti e ai dati di analytics consentiti dalle API di ciascuna piattaforma.</li>
            <li><strong>Usage data:</strong> information about how you interact with InstaEdit, including features used and performance metrics.</li>
            <li><strong>Informazioni sul dispositivo:</strong> tipo di browser, indirizzo IP, tipo di dispositivo e sistema operativo per scopi di sicurezza e analytics.</li>
          </ul>
        </LegalSection>

        <LegalSection title="2. How We Use Information">
          <p>InstaEdit utilizza le informazioni raccolte per i seguenti scopi:</p>
          <ul>
            <li>Fornire, mantenere e migliorare il Servizio InstaEdit.</li>
            <li>Enable the publication and scheduling of content through the linked social accounts.</li>
            <li>Generare modifiche video AI, sottotitoli e miniature in base ai contenuti dell'utente.</li>
            <li>Analizzare l'utilizzo del Servizio e ottimizzare le performance.</li>
            <li>Comunicare con l'utente in merito all'account, agli aggiornamenti e alle richieste di supporto.</li>
            <li>Ensure the security and integrity of our Service.</li>
            <li>Rispettare obblighi legali e far rispettare i Termini di Servizio.</li>
          </ul>
        </LegalSection>

        <LegalSection title="3. Come condividiamo le informazioni">
          <p>InstaEdit <strong>non</strong> vende le informazioni personali degli utenti. Possiamo condividere le informazioni nelle seguenti circostanze limitate:</p>
          <ul>
            <li><strong>Fornitori di servizi:</strong> lavoriamo con fornitori di terze parti fidati che elaborano dati per nostro conto in base a rigorosi accordi di riservatezza.</li>
            <li><strong>Piattaforme di social media:</strong> quando l'utente pubblica contenuti tramite InstaEdit, tali contenuti vengono trasmessi alle piattaforme che ha collegato.</li>
            <li><strong>Legal compliance:</strong> we may disclose information if required by law, by a court order, or by a government regulation.</li>
            <li><strong>Trasferimenti aziendali:</strong> in connessione con fusioni, acquisizioni o vendite di asset, le informazioni dell'utente possono essere trasferite come parte di tale transazione.</li>
          </ul>
        </LegalSection>

        <LegalSection title="4. Data Retention">
          <p>InstaEdit retains your personal information for the life of your account or as long as necessary to provide the Service. You may request deletion of your account and associated data at any time by contacting us.</p>
        </LegalSection>

        <LegalSection title="5. Diritti dell'utente">
          <p>Depending on your jurisdiction, you may have the following rights regarding your data:</p>
          <ul>
            <li>Accedere, correggere o cancellare le proprie informazioni personali.</li>
            <li>Object to or restrict certain processing activities.</li>
            <li>Data portability: receive your data in a structured, commonly used format.</li>
            <li>Withdraw consent at any time when processing is based on consent.</li>
            <li>File a complaint with your local data protection authority.</li>
          </ul>
          <p className="mt-2.5">
            Per esercitare uno di questi diritti, contattaci all'indirizzo{" "}
            <a href="mailto:hello@instaedit.org" className="text-[#0A84FF] hover:underline">
              hello@instaedit.org
            </a>.
          </p>
        </LegalSection>

        <LegalSection title="6. Cookie e tracciamento">
          <p>InstaEdit uses cookies and similar technologies to authenticate users, remember preferences and analyze use of the Service. You can control cookies through your browser settings.</p>
        </LegalSection>

        <LegalSection title="7. Sicurezza dei dati">
          <p>InstaEdit implementa misure di sicurezza standard del settore per proteggere le informazioni dell'utente, inclusa la crittografia in transito (TLS) e a riposo.</p>
        </LegalSection>

        <LegalSection title="8. Privacy dei minori">
          <p>InstaEdit is not intended for individuals under 13 years of age. We do not knowingly collect personal information from children.</p>
        </LegalSection>

        <LegalSection title="9. Piattaforme di terze parti">
          <p>InstaEdit si integra con piattaforme di terze parti tra cui TikTok, YouTube, Instagram, Facebook e X/Twitter. Quando si collegano questi account, si applicano anche le politiche sulla privacy di tali piattaforme.</p>
        </LegalSection>

        <LegalSection title="10. Modifiche a questa informativa">
          <p>InstaEdit may update this Privacy Policy from time to time. You will be informed of any material changes by posting the updated policy on this page.</p>
        </LegalSection>

        <LegalSection title="11. Contattaci">
          <p>Per domande su questa Privacy Policy, contattaci:</p>
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
