export function PrivacyPolicy() {
  return (
    <div className="min-h-screen bg-[#030308] text-[#e8e8ef] py-16 px-6">
      <main className="max-w-[800px] mx-auto">
        <p className="text-[#9aa0aa] text-[13px] mb-2">Last updated: July 9, 2026</p>
        <h1 className="text-[clamp(28px,4vw,42px)] font-extrabold tracking-[-0.02em] mb-8 text-white">
          Privacy Policy
        </h1>

        <p className="text-[#9aa0aa] leading-relaxed mb-7">
          This Privacy Policy describes how <strong className="text-white">InstaEdit</strong> (\"we,\" \"us,\" or \"our\")
          collects, uses, and shares your personal information when you use our website and services
          (collectively, the \"Service\"). By using InstaEdit, you agree to the collection and use of
          information in accordance with this policy.
        </p>

        <LegalSection title="1. Information We Collect">
          <p>When you sign up for and use <strong className="text-white">InstaEdit</strong>, we may collect the following types of information:</p>
          <ul>
            <li><strong>Account Information:</strong> Your name, email address, and profile information when you create an InstaEdit account or sign in through a third-party platform.</li>
            <li><strong>Content Data:</strong> Videos, captions, thumbnails, and other content you upload or create using InstaEdit.</li>
            <li><strong>Social Media Data:</strong> When you connect your social media accounts, we access your public profile information, content, and analytics data as permitted by each platform's API.</li>
            <li><strong>Usage Data:</strong> Information about how you interact with InstaEdit, including features used and performance analytics.</li>
            <li><strong>Device Information:</strong> Browser type, IP address, device type, and operating system for security and analytics purposes.</li>
          </ul>
        </LegalSection>

        <LegalSection title="2. How We Use Your Information">
          <p>InstaEdit uses the information we collect for the following purposes:</p>
          <ul>
            <li>To provide, maintain, and improve the InstaEdit Service.</li>
            <li>To enable publishing and scheduling of content across your connected social media accounts.</li>
            <li>To generate AI-powered video edits, captions, and thumbnails based on your content.</li>
            <li>To analyze Service usage and optimize performance.</li>
            <li>To communicate with you about your account, updates, and support inquiries.</li>
            <li>To ensure the security and integrity of our Service.</li>
            <li>To comply with legal obligations and enforce our Terms of Service.</li>
          </ul>
        </LegalSection>

        <LegalSection title="3. How We Share Your Information">
          <p>InstaEdit does <strong>not</strong> sell your personal information. We may share your information in the following limited circumstances:</p>
          <ul>
            <li><strong>Service Providers:</strong> We work with trusted third-party providers who process data on our behalf under strict confidentiality agreements.</li>
            <li><strong>Social Media Platforms:</strong> When you publish content through InstaEdit, that content is transmitted to the platforms you've connected.</li>
            <li><strong>Legal Compliance:</strong> We may disclose information if required by law, court order, or governmental regulation.</li>
            <li><strong>Business Transfers:</strong> In connection with a merger, acquisition, or sale of assets, your information may be transferred as part of that transaction.</li>
          </ul>
        </LegalSection>

        <LegalSection title="4. Data Retention">
          <p>InstaEdit retains your personal information for as long as your account is active or as needed to provide the Service. You may request deletion of your account and associated data at any time by contacting us.</p>
        </LegalSection>

        <LegalSection title="5. Your Rights">
          <p>Depending on your jurisdiction, you may have the following rights regarding your data:</p>
          <ul>
            <li>Access, correct, or delete your personal information.</li>
            <li>Object to or restrict certain processing activities.</li>
            <li>Data portability — receive your data in a structured, commonly used format.</li>
            <li>Withdraw consent at any time where processing is based on consent.</li>
            <li>Lodge a complaint with your local data protection authority.</li>
          </ul>
          <p className="mt-2.5">
            To exercise any of these rights, contact us at{" "}
            <a href="mailto:futurimilionariposta@gmail.com" className="text-[#0A84FF] hover:underline">
              futurimilionariposta@gmail.com
            </a>.
          </p>
        </LegalSection>

        <LegalSection title="6. Cookies &amp; Tracking">
          <p>InstaEdit uses cookies and similar technologies to authenticate users, remember preferences, and analyze Service usage. You can control cookies through your browser settings.</p>
        </LegalSection>

        <LegalSection title="7. Data Security">
          <p>InstaEdit implements industry-standard security measures to protect your information, including encryption in transit (TLS) and at rest.</p>
        </LegalSection>

        <LegalSection title="8. Children's Privacy">
          <p>InstaEdit is not directed to individuals under 13 years of age. We do not knowingly collect personal information from children.</p>
        </LegalSection>

        <LegalSection title="9. Third-Party Platforms">
          <p>InstaEdit integrates with third-party platforms including TikTok, YouTube, Instagram, Facebook, and X/Twitter. When you connect these accounts, those platforms' privacy policies also apply.</p>
        </LegalSection>

        <LegalSection title="10. Changes to This Policy">
          <p>InstaEdit may update this Privacy Policy from time to time. We will notify you of material changes by posting the updated policy on this page.</p>
        </LegalSection>

        <LegalSection title="11. Contact Us">
          <p>If you have questions about this Privacy Policy, contact us:</p>
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
        .legal-section ul, section ul { padding-left: 22px; margin-top: 10px; display: flex; flex-direction: column; gap: 8px; }
        section li { color: #9aa0aa; line-height: 1.7; }
      `}</style>
    </section>
  );
}
