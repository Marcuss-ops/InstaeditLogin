export function TermsOfService() {
  return (
    <div className="min-h-screen bg-[#030308] text-[#e8e8ef] py-16 px-6">
      <main className="max-w-[800px] mx-auto">
        <p className="text-[#9aa0aa] text-[13px] mb-2">Last updated: July 9, 2026</p>
        <h1 className="text-[clamp(28px,4vw,42px)] font-extrabold tracking-[-0.02em] mb-8 text-white">
          Terms of Service
        </h1>

        <p className="text-[#9aa0aa] leading-relaxed mb-7">
          Welcome to <strong className="text-white">InstaEdit</strong>. I presenti Terms of Service ("Termini") regolano l'accesso e l'uso del sito web,
          application and services of InstaEdit (collectively, the "Services"), operated by InstaEdit, Inc. ("InstaEdit", "we", "us" or "our").
          By accessing or using InstaEdit, you agree to be bound by these Terms.
        </p>

        <LegalSection title="1. Description of Service">
          <p><strong className="text-white">InstaEdit</strong> is an AI-powered video creation and publishing platform. InstaEdit enables users to generate, edit, caption, and publish video content through their linked social accounts — including TikTok, YouTube, Instagram, Facebook, and X/Twitter — using automated AI-driven workflows.</p>
        </LegalSection>

        <LegalSection title="2. Eligibility">
          <p>You must be at least 13 years old to use InstaEdit. By using the Service, you represent that you meet these eligibility requirements.</p>
        </LegalSection>

        <LegalSection title="3. Account Responsibility">
          <ul>
            <li>You are responsible for maintaining the confidentiality of your InstaEdit account credentials.</li>
            <li>You agree to notify us immediately of any unauthorized access.</li>
            <li>You may not share your account credentials with third parties.</li>
            <li>InstaEdit reserves the right to suspend or terminate accounts that violate these Terms.</li>
          </ul>
        </LegalSection>

        <LegalSection title="4. User Content">
          <p>You retain ownership of the content you upload, create or publish through InstaEdit ("User Content"). By using the Service, you grant InstaEdit a limited, worldwide, non-exclusive license to process, modify and transmit your User Content, solely to the extent necessary to provide the Service.</p>
          <p className="mt-3">You represent that you have all necessary rights to the Content you provide.</p>
        </LegalSection>

        <LegalSection title="5. Prohibited Conduct">
          <p>By using InstaEdit, you agree not to:</p>
          <ul>
            <li>Upload or publish content that is illegal, harmful, harassing, defamatory, obscene or otherwise objectionable.</li>
            <li>Violate applicable laws, regulations or terms of service.</li>
            <li>Use the Service to distribute spam, malware or conduct phishing.</li>
            <li>Reverse engineer, decompile or attempt to extract the source code of the Service.</li>
            <li>Use automated means (bots, scrapers) to access the Service without authorization.</li>
            <li>Interfere with the proper functioning of the Service.</li>
          </ul>
        </LegalSection>

        <LegalSection title="6. Intellectual Property">
          <p>InstaEdit and its content, features and original functionality are and will remain the exclusive property of InstaEdit, Inc.</p>
        </LegalSection>

        <LegalSection title="7. Third-Party Services">
          <p>InstaEdit integrates with third-party platforms (TikTok, YouTube, Instagram, Facebook, X/Twitter and others). Use of those platforms is governed by their respective terms of service.</p>
        </LegalSection>

        <LegalSection title="8. Subscription and Payment">
          <p>Some InstaEdit features may require a paid subscription. Subscription terms, pricing and renewal conditions will be presented at purchase. All fees are non-refundable except as required by applicable law.</p>
        </LegalSection>

        <LegalSection title="9. Disclaimer of Warranties">
          <p>THE SERVICE IS PROVIDED "AS IS" AND "AS AVAILABLE". TO THE MAXIMUM EXTENT PERMITTED BY LAW, INSTAEDIT DISCLAIMS ALL WARRANTIES, EXPRESS OR IMPLIED.</p>
        </LegalSection>

        <LegalSection title="10. Limitation of Liability">
          <p>TO THE MAXIMUM EXTENT PERMITTED BY LAW, INSTAEDIT WILL NOT BE LIABLE FOR ANY INDIRECT, INCIDENTAL, SPECIAL, CONSEQUENTIAL OR PUNITIVE DAMAGES ARISING FROM YOUR USE OF THE SERVICE.</p>
          <p className="mt-3">In no event will InstaEdit's total liability exceed the amount paid by you to InstaEdit in the twelve (12) months prior to the claim.</p>
        </LegalSection>

        <LegalSection title="11. Indemnification">
          <p>You agree to indemnify and hold harmless InstaEdit and its affiliates from any claim, damage, liability and expense arising from your use of the Service or your violation of these Terms.</p>
        </LegalSection>

        <LegalSection title="12. Termination">
          <p>InstaEdit may suspend or terminate your access to the Service at any time, with or without cause.</p>
        </LegalSection>

        <LegalSection title="13. Governing Law">
          <p>These Terms will be governed by and construed in accordance with applicable laws.</p>
        </LegalSection>

        <LegalSection title="14. Changes to These Terms">
          <p>InstaEdit reserves the right to modify these Terms at any time. You will be notified of material changes by posting the updated Terms on this page.</p>
        </LegalSection>

        <LegalSection title="15. Contact Us">
          <p>For questions about these Terms, contact us:</p>
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
