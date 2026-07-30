export function PrivacyPolicy() {
  return (
    <div className="min-h-screen bg-[#030308] text-[#e8e8ef] py-16 px-6">
      <main className="max-w-[800px] mx-auto">
        <p className="text-[#9aa0aa] text-[13px] mb-2">Last updated: July 9, 2026</p>
        <h1 className="text-[clamp(28px,4vw,42px)] font-extrabold tracking-[-0.02em] mb-8 text-white">
          Privacy Policy
        </h1>

        <p className="text-[#9aa0aa] leading-relaxed mb-7">
          This Privacy Policy describes how <strong className="text-white">InstaEdit</strong> ("we", "us" or "our")
          collects, uses and shares the personal information of users of our website and services
          (collectively, the "Services"). By using InstaEdit, you consent to the collection and use of information
          as described in this policy.
        </p>

        <LegalSection title="1. Information We Collect">
          <p>When you register and use <strong className="text-white">InstaEdit</strong>, we may collect the following types of information:</p>
          <ul>
            <li><strong>Account information:</strong> name, email address and profile information when you create an InstaEdit account or sign in through a third-party platform.</li>
            <li><strong>Content data:</strong> videos, captions, thumbnails and other content uploaded or created with InstaEdit.</li>
            <li><strong>Social media data:</strong> when you connect social accounts, we access the public profile information, content and analytics data allowed by each platform's APIs.</li>
            <li><strong>Usage data:</strong> information about how you interact with InstaEdit, including features used and performance metrics.</li>
            <li><strong>Device information:</strong> browser type, IP address, device type and operating system for security and analytics purposes.</li>
          </ul>
        </LegalSection>

        <LegalSection title="2. How We Use Information">
          <p>InstaEdit uses the information collected for the following purposes:</p>
          <ul>
            <li>Provide, maintain and improve the InstaEdit Service.</li>
            <li>Enable the publication and scheduling of content through the linked social accounts.</li>
            <li>Generate AI video edits, captions and thumbnails based on your content.</li>
            <li>Analyze use of the Service and optimize performance.</li>
            <li>Communicate with you about your account, updates and support requests.</li>
            <li>Ensure the security and integrity of our Service.</li>
            <li>Comply with legal obligations and enforce the Terms of Service.</li>
          </ul>
        </LegalSection>

        <LegalSection title="3. How We Share Information">
          <p>InstaEdit <strong>does not</strong> sell your personal information. We may share information in the following limited circumstances:</p>
          <ul>
            <li><strong>Service providers:</strong> we work with trusted third-party providers that process data on our behalf under strict confidentiality agreements.</li>
            <li><strong>Social media platforms:</strong> when you publish content through InstaEdit, that content is transmitted to the platforms you have connected.</li>
            <li><strong>Legal compliance:</strong> we may disclose information if required by law, by a court order, or by a government regulation.</li>
            <li><strong>Business transfers:</strong> in connection with mergers, acquisitions or asset sales, your information may be transferred as part of that transaction.</li>
          </ul>
        </LegalSection>

        <LegalSection title="4. Data Retention">
          <p>InstaEdit retains your personal information for the life of your account or as long as necessary to provide the Service. You may request deletion of your account and associated data at any time by contacting us.</p>
        </LegalSection>

        <LegalSection title="5. Your Rights">
          <p>Depending on your jurisdiction, you may have the following rights regarding your data:</p>
          <ul>
            <li>Access, correct or delete your personal information.</li>
            <li>Object to or restrict certain processing activities.</li>
            <li>Data portability: receive your data in a structured, commonly used format.</li>
            <li>Withdraw consent at any time when processing is based on consent.</li>
            <li>File a complaint with your local data protection authority.</li>
          </ul>
          <p className="mt-2.5">
            To exercise any of these rights, contact us at{" "}
            <a href="mailto:hello@instaedit.org" className="text-[#0A84FF] hover:underline">
              hello@instaedit.org
            </a>.
          </p>
        </LegalSection>

        <LegalSection title="6. Cookies and Tracking">
          <p>InstaEdit uses cookies and similar technologies to authenticate users, remember preferences and analyze use of the Service. You can control cookies through your browser settings.</p>
        </LegalSection>

        <LegalSection title="7. Data Security">
          <p>InstaEdit implements industry-standard security measures to protect your information, including encryption in transit (TLS) and at rest.</p>
        </LegalSection>

        <LegalSection title="8. Children's Privacy">
          <p>InstaEdit is not intended for individuals under 13 years of age. We do not knowingly collect personal information from children.</p>
        </LegalSection>

        <LegalSection title="9. Third-Party Platforms">
          <p>InstaEdit integrates with third-party platforms including TikTok, YouTube, Instagram, Facebook and X/Twitter. When you connect these accounts, the privacy policies of those platforms also apply.</p>
        </LegalSection>

        <LegalSection title="10. Changes to This Policy">
          <p>InstaEdit may update this Privacy Policy from time to time. You will be informed of any material changes by posting the updated policy on this page.</p>
        </LegalSection>

        <LegalSection title="11. Contact Us">
          <p>For questions about this Privacy Policy, contact us:</p>
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
