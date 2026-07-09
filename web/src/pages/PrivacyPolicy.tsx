import { Nav } from "../components/Nav";
import { Footer } from "../components/Footer";
import { LegalSection } from "../components/LegalSection";

export function PrivacyPolicy() {
  return (
    <div className="min-h-screen antialiased isolate">
      <div className="ambient-orbs" aria-hidden="true">
        <div className="orb orb-1"></div>
        <div className="orb orb-2"></div>
        <div className="orb orb-3"></div>
        <div className="orb orb-4"></div>
        <div className="orb orb-5"></div>
      </div>

      <Nav />

      <main className="section" style={{ maxWidth: 800 }}>
        <p style={{ color: "var(--muted)", fontSize: 13, marginBottom: 8 }}>
          Last updated: July 9, 2026
        </p>
        <h1 style={{ fontSize: "clamp(28px, 4vw, 42px)", marginBottom: 32, color: "white" }}>
          Privacy Policy
        </h1>

        <p style={{ color: "var(--muted)", lineHeight: 1.7, marginBottom: 28 }}>
          This Privacy Policy describes how <strong style={{ color: "var(--text)" }}>InstaEdit</strong> ("we," "us," or "our")
          collects, uses, and shares your personal information when you use our website and services
          (collectively, the "Service"). By using InstaEdit, you agree to the collection and use of
          information in accordance with this policy.
        </p>

        <LegalSection title="1. Information We Collect">
          <p>
            When you sign up for and use <strong style={{ color: "var(--text)" }}>InstaEdit</strong>, we may collect the
            following types of information:
          </p>
          <ul>
            <li>
              <strong>Account Information:</strong> Your name, email address, and profile information
              when you create an InstaEdit account or sign in through a third-party platform (such as
              TikTok, YouTube, Instagram, Facebook, or X/Twitter).
            </li>
            <li>
              <strong>Content Data:</strong> Videos, captions, thumbnails, and other content you
              upload or create using InstaEdit.
            </li>
            <li>
              <strong>Social Media Data:</strong> When you connect your social media accounts to
              InstaEdit, we access your public profile information, content, and analytics data as
              permitted by each platform's API. This may include follower counts, engagement metrics,
              post data, and publishing permissions.
            </li>
            <li>
              <strong>Usage Data:</strong> Information about how you interact with InstaEdit,
              including features used, time spent, and performance analytics.
            </li>
            <li>
              <strong>Device Information:</strong> Browser type, IP address, device type, and
              operating system for security and analytics purposes.
            </li>
          </ul>
        </LegalSection>

        <LegalSection title="2. How We Use Your Information">
          <p>InstaEdit uses the information we collect for the following purposes:</p>
          <ul>
            <li>To provide, maintain, and improve the InstaEdit Service.</li>
            <li>
              To enable publishing and scheduling of content across your connected social media
              accounts.
            </li>
            <li>
              To generate AI-powered video edits, captions, and thumbnails based on your content.
            </li>
            <li>To analyze Service usage and optimize performance.</li>
            <li>To communicate with you about your account, updates, and support inquiries.</li>
            <li>To ensure the security and integrity of our Service.</li>
            <li>To comply with legal obligations and enforce our Terms of Service.</li>
          </ul>
        </LegalSection>

        <LegalSection title="3. How We Share Your Information">
          <p>
            InstaEdit does <strong>not</strong> sell your personal information. We may share your
            information in the following limited circumstances:
          </p>
          <ul>
            <li>
              <strong>Service Providers:</strong> We work with trusted third-party providers (cloud
              hosting, AI processing, analytics) who process data on our behalf under strict
              confidentiality agreements.
            </li>
            <li>
              <strong>Social Media Platforms:</strong> When you publish content through InstaEdit,
              that content is transmitted to the platforms you've connected (TikTok, YouTube, etc.)
              in accordance with their own privacy policies.
            </li>
            <li>
              <strong>Legal Compliance:</strong> We may disclose information if required by law,
              court order, or governmental regulation.
            </li>
            <li>
              <strong>Business Transfers:</strong> In connection with a merger, acquisition, or sale
              of assets, your information may be transferred as part of that transaction.
            </li>
          </ul>
        </LegalSection>

        <LegalSection title="4. Data Retention">
          <p>
            InstaEdit retains your personal information for as long as your account is active or as
            needed to provide the Service. You may request deletion of your account and associated
            data at any time by contacting us. Certain data may be retained as required by law or for
            legitimate business purposes (such as fraud prevention).
          </p>
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
          <p style={{ marginTop: 10 }}>
            To exercise any of these rights, contact us at{" "}
            <a href="mailto:futurimilionariposta@gmail.com" className="footer-link">
              futurimilionariposta@gmail.com
            </a>.
          </p>
        </LegalSection>

        <LegalSection title="6. Cookies &amp; Tracking">
          <p>
            InstaEdit uses cookies and similar technologies to authenticate users, remember
            preferences, and analyze Service usage. You can control cookies through your browser
            settings, though disabling cookies may affect Service functionality.
          </p>
        </LegalSection>

        <LegalSection title="7. Data Security">
          <p>
            InstaEdit implements industry-standard security measures to protect your information,
            including encryption in transit (TLS) and at rest. However, no method of electronic
            storage or transmission is 100% secure, and we cannot guarantee absolute security.
          </p>
        </LegalSection>

        <LegalSection title="8. Children's Privacy">
          <p>
            InstaEdit is not directed to individuals under 13 years of age (or the applicable age
            of digital consent in your jurisdiction). We do not knowingly collect personal
            information from children. If we learn we have collected such data, we will delete it
            promptly.
          </p>
        </LegalSection>

        <LegalSection title="9. Third-Party Platforms">
          <p>
            InstaEdit integrates with third-party platforms including TikTok, YouTube, Instagram,
            Facebook, and X/Twitter. When you connect these accounts, those platforms' privacy
            policies also apply. We encourage you to review the privacy policies of each platform you
            connect.
          </p>
          <p style={{ marginTop: 12 }}>
            For TikTok specifically — which InstaEdit uses for authentication and content publishing
            via the TikTok Login Kit and Content Posting API — please review:
          </p>
          <ul>
            <li>
              <a
                href="https://www.tiktok.com/legal/page/us/privacy-policy/en"
                target="_blank"
                rel="noopener noreferrer"
                className="footer-link"
              >
                TikTok Privacy Policy
              </a>
            </li>
            <li>
              <a
                href="https://www.tiktok.com/legal/page/global/tik-tok-developer-terms-of-service/en"
                target="_blank"
                rel="noopener noreferrer"
                className="footer-link"
              >
                TikTok Developer Terms of Service
              </a>
            </li>
          </ul>
        </LegalSection>

        <LegalSection title="10. Changes to This Policy">
          <p>
            InstaEdit may update this Privacy Policy from time to time. We will notify you of
            material changes by posting the updated policy on this page and updating the "Last
            updated" date. Continued use of the Service after changes constitutes acceptance.
          </p>
        </LegalSection>

        <LegalSection title="11. Contact Us">
          <p>
            If you have questions about this Privacy Policy or InstaEdit's data practices, please
            contact us:
          </p>
          <p style={{ marginTop: 12 }}>
            Email:{" "}
            <a href="mailto:futurimilionariposta@gmail.com" className="footer-link">
              futurimilionariposta@gmail.com
            </a>
          </p>
        </LegalSection>
      </main>

      <Footer />
    </div>
  );
}
