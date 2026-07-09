import { useState } from "react";
import { Nav } from "./Nav";
import { Footer } from "./Footer";

export function TikTokSection() {
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
      <main className="section">
        {/* Hero */}
        <div className="section-head" style={{ marginBottom: 48 }}>
          <h2>
            Ship Your TikTok Integration
            <br />
            <span className="text-gradient">In Minutes, Not Months</span>
          </h2>
          <p style={{ maxWidth: 680, margin: "16px auto 0" }}>
            Stop wrestling with the TikTok Content Posting API. InstaEdit handles OAuth, rate limits,
            video transcoding, and API changes — so you can focus on creating content that goes viral.
          </p>
          <div style={{ display: "flex", gap: 14, justifyContent: "center", marginTop: 28, flexWrap: "wrap" }}>
            <a className="btn btn-primary" href="/login">
              Start Free Trial
            </a>
            <a
              className="btn"
              href="https://developers.tiktok.com/doc/content-posting-api-reference/"
              target="_blank"
              rel="noopener noreferrer"
              style={{ gap: 6 }}
            >
              View TikTok API Reference
              <svg width="14" height="14" viewBox="0 0 14 14" fill="none"><path d="M10.5 3.5L3.5 10.5M10.5 3.5H5.25M10.5 3.5V8.75" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round"/></svg>
            </a>
          </div>
          <p style={{ marginTop: 16, fontSize: 12, color: "var(--muted)" }}>
            No credit card required
          </p>
        </div>

        {/* Code snippet */}
        <div className="code-snippet-wrap">
          <div className="code-snippet-bar">
            <div className="code-snippet-dot" style={{ background: "#ff5f56" }} />
            <div className="code-snippet-dot" style={{ background: "#ffbd2e" }} />
            <div className="code-snippet-dot" style={{ background: "#27c93f" }} />
            <span style={{ marginLeft: 8, fontSize: 12, color: "var(--muted)" }}>
              POST /api/tiktok/publish
            </span>
          </div>
          <pre className="code-snippet-pre">
{`{
  <span class="code-key">"platform"</span>: <span class="code-val">"tiktok"</span>,
  <span class="code-key">"caption"</span>: <span class="code-val">"Behind the scenes 🎬 #fyp"</span>,
  <span class="code-key">"videoUrl"</span>: <span class="code-val">"https://..."</span>,
  <span class="code-key">"commentMode"</span>: <span class="code-val">"allow_all"</span>,
  <span class="code-key">"duetMode"</span>: <span class="code-val">"allow"</span>,
  <span class="code-key">"publishAs"</span>: <span class="code-val">"draft"</span>
}`}
          </pre>
        </div>

        {/* Comparison table */}
        <h3 style={{ textAlign: "center", fontSize: 22, fontWeight: 650, marginBottom: 32, color: "white" }}>
          Why InstaEdit vs TikTok Content Posting API?
        </h3>
        <ComparisonTable />

        {/* How it works */}
        <HowItWorks />

        {/* Features grid */}
        <FeaturesGrid />

        {/* FAQ */}
        <FaqSection />
      </main>
      <Footer />
    </div>
  );
}

function ComparisonTable() {
  return (
    <div className="comparison-wrap">
      <table className="comparison-table">
        <thead>
          <tr style={{ background: "rgba(255,255,255,0.04)" }}>
            <th>InstaEdit</th>
            <th>TikTok Content Posting API</th>
          </tr>
        </thead>
        <tbody>
          {[
            { insta: "Simple OAuth — start in 30 seconds", tik: "Complex OAuth with TikTok developer app approval" },
            { insta: "Automatic retries & rate limit handling", tik: "Strict rate limits with complex backoff logic" },
            { insta: "Upload any format — we transcode for TikTok", tik: "Video must meet strict encoding requirements" },
            { insta: "Zero maintenance — we handle API changes", tik: "Frequent API changes require constant updates" },
            { insta: "One dashboard for 5+ platforms", tik: "Build separate integrations per platform" },
          ].map((row, i) => (
            <tr key={i}>
              <td className="check">✓ {row.insta}</td>
              <td className="cross">✗ {row.tik}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function HowItWorks() {
  return (
    <div>
      <h3 style={{ textAlign: "center", fontSize: 22, fontWeight: 650, marginBottom: 32, color: "white" }}>
        How It Works
      </h3>
      <div className="tiktok-how-grid">
        <div className="how-step">
          <div className="how-step-num">1</div>
          <h4 style={{ marginBottom: 8, fontSize: 15 }}>Connect Your Account</h4>
          <p style={{ fontSize: 13, color: "var(--muted)" }}>
            Link your TikTok account through our dashboard. One-click OAuth — we handle all the TikTok permissions.
          </p>
        </div>
        <div className="how-step">
          <div className="how-step-num">2</div>
          <h4 style={{ marginBottom: 8, fontSize: 15 }}>Create Your Video</h4>
          <p style={{ fontSize: 13, color: "var(--muted)" }}>
            Use InstaEdit's AI to generate, edit, and caption your video. Set comment settings, duet options, and schedule.
          </p>
        </div>
        <div className="how-step">
          <div className="how-step-num">3</div>
          <h4 style={{ marginBottom: 8, fontSize: 15 }}>We Handle the Rest</h4>
          <p style={{ fontSize: 13, color: "var(--muted)" }}>
            InstaEdit publishes at your scheduled time, retries on failures, and notifies you. You never touch TikTok's API directly.
          </p>
        </div>
      </div>
    </div>
  );
}

function FeaturesGrid() {
  return (
    <div>
      <h3 style={{ textAlign: "center", fontSize: 22, fontWeight: 650, marginBottom: 32, color: "white" }}>
        Features
      </h3>
      <div className="tiktok-features-grid">
        {[
          {
            title: "Ship Faster",
            desc: "Go from zero to posting in under 30 seconds. No TikTok developer app approval process — just connect and start posting.",
          },
          {
            title: "Official API, Zero Hassle",
            desc: "We use TikTok's official Content Posting API under the hood. Full compliance and reliability without the integration pain.",
          },
          {
            title: "We Handle the Hard Parts",
            desc: "Rate limits, token refresh, video transcoding, error handling — all managed for you. Focus on content, not infrastructure.",
          },
          {
            title: "Multi-Platform Publishing",
            desc: "Same video, multiple platforms. Publish to TikTok, YouTube Shorts, Instagram Reels, Facebook, and X simultaneously.",
          },
          {
            title: "AI-Powered Editing",
            desc: "Let AI generate captions, trim highlights, add effects, and optimize your video for TikTok's algorithm automatically.",
          },
          {
            title: "Draft Mode",
            desc: "Save videos as TikTok drafts before publishing. Review, tweak, and schedule — full control before going live.",
          },
        ].map((f, i) => (
          <div className="feature" key={i}>
            <h4>{f.title}</h4>
            <p>{f.desc}</p>
          </div>
        ))}
      </div>
    </div>
  );
}

function FaqSection() {
  const faqs = [
    {
      q: "How long does TikTok API approval take?",
      a: "TikTok's Content Posting API approval can take days to weeks. With InstaEdit, there's no approval process — connect your TikTok account and start posting immediately through our managed OAuth flow.",
    },
    {
      q: "What video formats does the TikTok API accept?",
      a: "Upload MP4 videos up to 10 minutes (standard accounts) with 1080p resolution. InstaEdit automatically transcodes your videos to formats TikTok accepts — no manual encoding required.",
    },
    {
      q: "Can I schedule TikTok posts in advance?",
      a: "Yes. Set a scheduled time and InstaEdit publishes at the exact moment. We handle rate limits and queue requests if TikTok imposes temporary throttling.",
    },
    {
      q: "Can I cross-post TikTok videos to other platforms?",
      a: "Absolutely. The same video can publish to TikTok, Instagram Reels, YouTube Shorts, Facebook, and X simultaneously from one InstaEdit dashboard.",
    },
    {
      q: "Can I save a draft on TikTok?",
      a: "Yes. InstaEdit supports TikTok draft mode via the video.upload scope. Create drafts, review them, and publish when you're ready.",
    },
    {
      q: "How much does the TikTok API cost?",
      a: "TikTok's Content Posting API is free after approval, but building the integration takes weeks of engineering. InstaEdit handles the entire integration for you — start free and scale as you grow.",
    },
  ];

  return (
    <div style={{ maxWidth: 720, margin: "0 auto" }}>
      <h3 style={{ textAlign: "center", fontSize: 22, fontWeight: 650, marginBottom: 32, color: "white" }}>
        Frequently Asked Questions
      </h3>
      {faqs.map((faq, i) => (
        <FaqItem key={i} question={faq.q} answer={faq.a} />
      ))}
    </div>
  );
}

function FaqItem({ question, answer }: { question: string; answer: string }) {
  const [open, setOpen] = useState(false);

  return (
    <div className="faq-item">
      <button className="faq-question" onClick={() => setOpen(!open)}>
        {question}
        <svg
          className={`faq-arrow${open ? " open" : ""}`}
          width="16"
          height="16"
          viewBox="0 0 16 16"
          fill="none"
        >
          <path d="M4 6l4 4 4-4" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
        </svg>
      </button>
      <div className={`faq-answer${open ? " open" : ""}`}>{answer}</div>
    </div>
  );
}
