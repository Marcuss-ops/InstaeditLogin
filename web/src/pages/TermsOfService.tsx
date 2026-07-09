import { Nav } from "../components/Nav";
import { Footer } from "../components/Footer";
import { LegalSection } from "../components/LegalSection";

export function TermsOfService() {
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
          Terms of Service
        </h1>

        <p style={{ color: "var(--muted)", lineHeight: 1.7, marginBottom: 28 }}>
          Welcome to <strong style={{ color: "var(--text)" }}>InstaEdit</strong>. These Terms of
          Service ("Terms") govern your access to and use of the InstaEdit website, application,
          and services (collectively, the "Service"), operated by InstaEdit, Inc. ("InstaEdit,"
          "we," "us," or "our"). By accessing or using InstaEdit, you agree to be bound by these
          Terms. If you do not agree, please do not use the Service.
        </p>

        <LegalSection title="1. Description of Service">
          <p>
            <strong style={{ color: "var(--text)" }}>InstaEdit</strong> is an AI-powered video
            creation and publishing platform. InstaEdit allows users to generate, edit, caption, and
            publish video content across connected social media accounts — including TikTok, YouTube,
            Instagram, Facebook, and X/Twitter — using automated AI workflows.
          </p>
        </LegalSection>

        <LegalSection title="2. Eligibility">
          <p>
            You must be at least 13 years old (or the applicable age of digital consent in your
            jurisdiction) to use InstaEdit. By using the Service, you represent and warrant that
            you meet these eligibility requirements and that all information you provide is accurate
            and complete.
          </p>
        </LegalSection>

        <LegalSection title="3. Account Responsibilities">
          <ul>
            <li>
              You are responsible for maintaining the confidentiality of your InstaEdit account
              credentials and for all activities that occur under your account.
            </li>
            <li>
              You agree to notify us immediately of any unauthorized access or use of your account.
            </li>
            <li>
              You may not share your account credentials with third parties or allow others to
              access the Service through your account.
            </li>
            <li>
              InstaEdit reserves the right to suspend or terminate accounts that violate these
              Terms or engage in prohibited activities.
            </li>
          </ul>
        </LegalSection>

        <LegalSection title="4. User Content">
          <p>
            You retain ownership of the content you upload, create, or publish through InstaEdit
            ("User Content"). By using the Service, you grant InstaEdit a limited, worldwide,
            non-exclusive license to process, modify, and transmit your User Content solely as
            necessary to provide the Service (e.g., AI editing, publishing to connected platforms).
          </p>
          <p style={{ marginTop: 12 }}>
            You represent that you have all necessary rights to the User Content you provide and
            that it does not infringe the intellectual property, privacy, or other rights of any
            third party.
          </p>
        </LegalSection>

        <LegalSection title="5. Prohibited Conduct">
          <p>When using InstaEdit, you agree not to:</p>
          <ul>
            <li>
              Upload or publish content that is illegal, harmful, harassing, defamatory,
              obscene, or otherwise objectionable.
            </li>
            <li>Violate any applicable laws, regulations, or platform terms of service.</li>
            <li>
              Use the Service to distribute spam, malware, or engage in phishing or fraudulent
              activities.
            </li>
            <li>Reverse-engineer, decompile, or attempt to extract the source code of the Service.</li>
            <li>
              Use automated means (bots, scrapers) to access the Service without our express
              permission.
            </li>
            <li>
              Interfere with the proper functioning of the Service or impose an unreasonable
              load on our infrastructure.
            </li>
            <li>
              Use InstaEdit-generated content in a way that violates the terms of service of
              any connected social media platform.
            </li>
          </ul>
        </LegalSection>

        <LegalSection title="6. Intellectual Property">
          <p>
            InstaEdit and its original content, features, and functionality (including but not
            limited to the InstaEdit name, logo, software, and AI models) are and will remain the
            exclusive property of InstaEdit, Inc. The Service is protected by copyright, trademark,
            and other intellectual property laws.
          </p>
        </LegalSection>

        <LegalSection title="7. Third-Party Services">
          <p>
            InstaEdit integrates with third-party platforms (TikTok, YouTube, Instagram, Facebook,
            X/Twitter, and others). Your use of those platforms is governed by their respective
            terms of service. InstaEdit is not responsible for the content, policies, or practices
            of any third-party services.
          </p>
          <p style={{ marginTop: 12 }}>
            InstaEdit uses the TikTok Login Kit and Content Posting API to authenticate users and
            publish content. By using these features, you also agree to TikTok's developer terms:
          </p>
          <ul>
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
          </ul>
        </LegalSection>

        <LegalSection title="8. Subscription and Payment">
          <p>
            Certain features of InstaEdit may require a paid subscription. Subscription terms,
            pricing, and renewal conditions will be presented at the time of purchase. All fees are
            non-refundable except as required by applicable law. InstaEdit reserves the right to
            modify pricing with reasonable notice.
          </p>
        </LegalSection>

        <LegalSection title="9. Disclaimer of Warranties">
          <p>
            THE SERVICE IS PROVIDED ON AN "AS IS" AND "AS AVAILABLE" BASIS. TO THE FULLEST EXTENT
            PERMITTED BY LAW, INSTAEDIT DISCLAIMS ALL WARRANTIES, EXPRESS OR IMPLIED, INCLUDING BUT
            NOT LIMITED TO IMPLIED WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE, AND
            NON-INFRINGEMENT.
          </p>
          <p style={{ marginTop: 12 }}>
            InstaEdit does not guarantee that the Service will be uninterrupted, error-free, or
            that AI-generated content will meet your specific requirements or expectations.
          </p>
        </LegalSection>

        <LegalSection title="10. Limitation of Liability">
          <p>
            TO THE FULLEST EXTENT PERMITTED BY LAW, INSTAEDIT AND ITS OFFICERS, DIRECTORS,
            EMPLOYEES, AND AGENTS SHALL NOT BE LIABLE FOR ANY INDIRECT, INCIDENTAL, SPECIAL,
            CONSEQUENTIAL, OR PUNITIVE DAMAGES ARISING OUT OF OR RELATING TO YOUR USE OF THE
            SERVICE, INCLUDING BUT NOT LIMITED TO LOST PROFITS, DATA LOSS, OR DAMAGES RESULTING
            FROM AI-GENERATED CONTENT.
          </p>
          <p style={{ marginTop: 12 }}>
            In no event shall InstaEdit's total liability exceed the amount you paid to InstaEdit
            in the twelve (12) months preceding the claim.
          </p>
        </LegalSection>

        <LegalSection title="11. Indemnification">
          <p>
            You agree to indemnify and hold harmless InstaEdit and its affiliates from any claims,
            damages, liabilities, and expenses (including reasonable attorneys' fees) arising from
            your use of the Service, your User Content, or your violation of these Terms.
          </p>
        </LegalSection>

        <LegalSection title="12. Termination">
          <p>
            InstaEdit may suspend or terminate your access to the Service at any time, with or
            without cause, and with or without notice. Upon termination, your right to use the
            Service ceases immediately. Provisions of these Terms that by their nature should
            survive termination shall survive, including intellectual property, disclaimers, and
            limitations of liability.
          </p>
        </LegalSection>

        <LegalSection title="13. Governing Law">
          <p>
            These Terms shall be governed by and construed in accordance with the applicable laws,
            without regard to conflict of law principles. Any disputes arising from these Terms or
            the Service shall be resolved through binding arbitration or in the courts of competent
            jurisdiction as applicable.
          </p>
        </LegalSection>

        <LegalSection title="14. Changes to These Terms">
          <p>
            InstaEdit reserves the right to modify these Terms at any time. We will notify you of
            material changes by posting the updated Terms on this page and updating the "Last
            updated" date. Your continued use of the Service after changes are posted constitutes
            acceptance of the revised Terms.
          </p>
        </LegalSection>

        <LegalSection title="15. Contact">
          <p>
            For questions about these Terms of Service or InstaEdit, please contact us:
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
