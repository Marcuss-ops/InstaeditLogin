import { Zap, Shield, RefreshCw } from "lucide-react";
import type { PlatformData } from "../platformData";

export default {
  slug: "twitter",
  name: "X (Twitter)",
  color: "#e8e8ef",
  icon: (
    <svg viewBox="0 0 24 24" fill="currentColor" className="w-6 h-6">
      <path d="M17.5 4.5h3.1l-6.8 7.8 8 10.6h-6.3l-4.9-6.4-5.6 6.4H2l7.3-8.3L1.7 4.5h6.4l4.4 5.9 5-5.9z" />
    </svg>
  ),
  heroTagline: "Ship your X integration in minutes, not months",
  heroDescription:
    "Stop wrestling with X API v2. InstaEdit handles OAuth 2.0 PKCE, rate limits, and API changes — so you can focus on building your product.",
  noteTitle: "X Developer Account Required",
  noteDescription:
    "X integration requires a developer account with API access. InstaEdit uses OAuth 2.0 PKCE for secure authentication. Free tier includes 1,500 tweets per month at posting level.",
  contentTypes: ["Text Tweets", "Images", "Videos", "Threads"],
  features: [
    {
      icon: <Zap className="w-5 h-5" />,
      title: "Ship faster",
      description:
        "Go from zero to posting in under 30 seconds. No X developer app approval headaches — just get your API key and start building.",
    },
    {
      icon: <Shield className="w-5 h-5" />,
      title: "Official API, zero hassle",
      description:
        "We use X's official API v2 under the hood. You get full compliance and reliability without the integration pain.",
    },
    {
      icon: <RefreshCw className="w-5 h-5" />,
      title: "We handle the hard parts",
      description:
        "Rate limits, token refresh, media uploads, error handling — all managed for you. Focus on your content, not infrastructure.",
    },
  ],
  comparison: {
    us: {
      label: "InstaEdit API",
      items: [
        "Simple API key — start in 30 seconds",
        "Automatic retries & queue management",
        "Upload directly — we optimize for X",
        "Zero maintenance forever",
        "One API for 7 platforms",
      ],
    },
    them: {
      label: "X API v2 Direct",
      items: [
        "Complex OAuth 2.0 PKCE implementation",
        "Strict rate limits (varies by tier)",
        "Media upload endpoint complexity",
        "Frequent API changes and deprecations",
        "Build separate integrations per platform",
      ],
    },
  },
  codeExample: `const response = await fetch('https://api.instaedit.org/api/v1/posts', {
  method: 'POST',
  headers: {
    'Authorization': 'Bearer YOUR_API_KEY',
    'Content-Type': 'application/json'
  },
  body: JSON.stringify({
    platforms: [{
      platform: 'twitter',
      accountId: 'your-twitter-account-id'
    }],
    content: 'Just shipped a major update! Check it out.',
    mediaItems: [{
      type: 'image',
      url: 'https://your-image-url.jpg'
    }],
    scheduledFor: '2025-01-15T12:00:00Z'
  })
});

const result = await response.json();
console.log('Tweet scheduled:', result.id);`,
  faq: [
    {
      q: "What X API tier do I need?",
      a: "InstaEdit works with Free, Basic, and Pro tiers. Free tier includes 1,500 tweets per month for posting.",
    },
    {
      q: "Can I schedule tweets in advance?",
      a: "Yes. Set a scheduledFor timestamp and InstaEdit publishes at the exact time. We handle rate limits and queue management.",
    },
    {
      q: "Can I cross-post to X from the same API call?",
      a: "Yes. Include 'twitter' in your platforms array alongside other platforms. InstaEdit optimizes content length and format for each platform.",
    },
  ],
} satisfies PlatformData;
