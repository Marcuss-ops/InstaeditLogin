export function TermsOfService() {
  return (
    <div className="min-h-screen bg-[#030308] text-[#e8e8ef] py-16 px-6">
      <main className="max-w-[800px] mx-auto">
        <p className="text-[#9aa0aa] text-[13px] mb-2">Last updated: July 9, 2026</p>
        <h1 className="text-[clamp(28px,4vw,42px)] font-extrabold tracking-[-0.02em] mb-8 text-white">
          Terms of Service
        </h1>

        <p className="text-[#9aa0aa] leading-relaxed mb-7">
          Welcome to <strong className="text-white">InstaEdit</strong>. These Terms of Service (\"Terms\") govern your access to and use of the InstaEdit website, application, and services (collectively, the \"Service\"), operated by InstaEdit, Inc. (\"InstaEdit,\" \"we,\" \"us,\" or \"our\"). By accessing or using InstaEdit, you agree to be bound by these Terms.
        </p>

        <LegalSection title="1. Description of Service">
          <p><strong className="text-white">InstaEdit</strong> is an AI-powered video creation and publishing platform. InstaEdit allows users to generate, edit, caption, and publish video content across connected social media accounts — including TikTok, YouTube, Instagram, Facebook, and X/Twitter — using automated AI workflows.</p>
        </LegalSection>

        <LegalSection title="2. Eligibility">
          <p>You must be at least 13 years old to use InstaEdit. By using the Service, you represent that you meet these eligibility requirements.</p>
        </LegalSection>

        <LegalSection title="3. Account Responsibilities">
          <ul>
            <li>You are responsible for maintaining the confidentiality of your InstaEdit account credentials.</li>
            <li>You agree to notify us immediately of any unauthorized access.</li>
            <li>You may not share your account credentials with third parties.</li>
            <li>InstaEdit reserves the right to suspend or terminate accounts that violate these Terms.</li>
          </ul>
        </LegalSection>

        <LegalSection title="4. User Content">
          <p>You retain ownership of the content you upload, create, or publish through InstaEdit (\"User Content\"). By using the Service, you grant InstaEdit a limited, worldwide, non-exclusive license to process, modify, and transmit your User Content solely as necessary to provide the Service.</p>
          <p className="mt-3">You represent that you have all necessary rights to the User Content you provide.</p>
        </LegalSection>

        <LegalSection title="5. Prohibited Conduct">
          <p>When using InstaEdit, you agree not to:</p>
          <ul>
            <li>Upload or publish content that is illegal, harmful, harassing, defamatory, obscene, or otherwise objectionable.</li>
            <li>Violate any applicable laws, regulations, or platform terms of service.</li>
            <li>Use the Service to distribute spam, malware, or engage in phishing.</li>
            <li>Reverse-engineer, decompile, or attempt to extract the source code of the Service.</li>
            <li>Use automated means (bots, scrapers) to access the Service without permission.</li>
            <li>Interfere with the proper functioning of the Service.</li>
          </ul>
        </LegalSection>

        <LegalSection title="6. Intellectual Property">
          <p>InstaEdit and its original content, features, and functionality are and will remain the exclusive property of InstaEdit, Inc.</p>
        </LegalSection>

        <LegalSection title="7. Third-Party Services">
          <p>InstaEdit integrates with third-party platforms (TikTok, YouTube, Instagram, Facebook, X/Twitter, and others). Your use of those platforms is governed by their respective terms of service.</p>
        </LegalSection>

        <LegalSection title="8. Subscription and Payment">
          <p>Certain features of InstaEdit may require a paid subscription. Subscription terms, pricing, and renewal conditions will be presented at the time of purchase. All fees are non-refundable except as required by applicable law.</p>
        </LegalSection>

        <LegalSection title="9. Disclaimer of Warranties">
          <p>THE SERVICE IS PROVIDED ON AN \"AS IS\" AND \"AS AVAILABLE\" BASIS. TO THE FULLEST EXTENT PERMITTED BY LAW, INSTAEDIT DISCLAIMS ALL WARRANTIES, EXPRESS OR IMPLIED.</p>
        </LegalSection>

        <LegalSection title="10. Limitation of Liability">
          <p>TO THE FULLEST EXTENT PERMITTED BY LAW, INSTAEDIT SHALL NOT BE LIABLE FOR ANY INDIRECT, INCIDENTAL, SPECIAL, CONSEQUENTIAL, OR PUNITIVE DAMAGES ARISING OUT OF YOUR USE OF THE SERVICE.</p>
          <p className="mt-3">In no event shall InstaEdit's total liability exceed the amount you paid to InstaEdit in the twelve (12) months preceding the claim.</p>
        </LegalSection>

        <LegalSection title="11. Indemnification">
          <p>You agree to indemnify and hold harmless InstaEdit and its affiliates from any claims, damages, liabilities, and expenses arising from your use of the Service or your violation of these Terms.</p>
        </LegalSection>

        <LegalSection title="12. Termination">
          <p>InstaEdit may suspend or terminate your access to the Service at any time, with or without cause.</p>
        </LegalSection>

        <LegalSection title="13. Governing Law">
          <p>These Terms shall be governed by and construed in accordance with applicable laws.</p>
        </LegalSection>

        <LegalSection title="14. Changes to These Terms">
          <p>InstaEdit reserves the right to modify these Terms at any time. We will notify you of material changes by posting the updated Terms on this page.</p>
        </LegalSection>

        <LegalSection title="15. Contact">
          <p>For questions about these Terms, contact us:</p>
          <p className="mt-3">
            Email:{" "}
            <a href="mailto:futurimilionariposta@gmail.com" className="text-[#0A84FF] hover:underline">
              futurimilionariposta@gmail.com
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
