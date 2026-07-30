import { Zap, Shield, RefreshCw } from "lucide-react";
import type { PlatformData } from "../platformData";

export default {
  slug: "threads",
  name: "Threads",
  color: "#9AA0AA",
  icon: (
    <svg viewBox="0 0 24 24" fill="currentColor" className="w-6 h-6">
      <path d="M12 3C7 3 3 7 3 12s4 9 9 9 9-4 9-9-4-9-9-9zm0 2c3.9 0 7 3.1 7 7s-3.1 7-7 7-7-3.1-7-7 3.1-7 7-7zm-1.5 4c-1.4 0-2.5 1.1-2.5 2.5s1.1 2.5 2.5 2.5c.6 0 1.1-.2 1.5-.6l.7.7c-.6.5-1.4.9-2.2.9-1.9 0-3.5-1.6-3.5-3.5s1.6-3.5 3.5-3.5h3v2h-3zm5 0v2h-2v-2h2z" />
    </svg>
  ),
  heroTagline: "Ship your Threads integration in minutes, not months",
  heroDescription:
    "Stop wrestling with Meta's Threads API. InstaEdit handles OAuth, rate limits, media hosting, and API changes — so you can focus on building your product.",
  noteTitle: "Instagram Account Required",
  noteDescription:
    "Threads uses Instagram account authentication. Connect your Instagram Business account through InstaEdit, and you'll automatically get access to Threads posting. Perfect for founders building in public, thought leaders, and brands focused on authentic community engagement.",
  contentTypes: ["Text Posts", "Photos", "Videos", "Link Cards"],
  features: [
    {
      icon: <Zap className="w-5 h-5" />,
      title: "Ship faster",
      description:
        "Go from zero to posting in under 30 seconds. No Instagram Business verification — just get your API key and start building.",
    },
    {
      icon: <Shield className="w-5 h-5" />,
      title: "Official API, zero hassle",
      description:
        "We use Meta's official Threads API under the hood. You get full compliance and reliability without the integration pain.",
    },
    {
      icon: <RefreshCw className="w-5 h-5" />,
      title: "We handle the hard parts",
      description:
        "Rate limits, token refresh, media processing, error handling — all managed for you. Focus on your product, not infrastructure.",
    },
  ],
  comparison: {
    us: {
      label: "InstaEdit API",
      items: [
        "Simple API key — start in 30 seconds",
        "Automatic retries & queue management",
        "Upload directly — we optimize for Meta",
        "Zero maintenance forever",
        "One API for 7 platforms",
      ],
    },
    them: {
      label: "Meta Threads API",
      items: [
        "Complex OAuth through Instagram Business verification",
        "Strict rate limits requiring careful management",
        "Media must meet Meta's strict requirements",
        "New API with frequent changes and updates",
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
      platform: 'threads',
      accountId: 'your-threads-account-id'
    }],
    content: 'Building authentic connections through meaningful conversations.',
    mediaItems: [{
      type: 'image',
      url: 'https://your-community-image.jpg'
    }],
    scheduledFor: '2025-01-15T15:00:00Z'
  })
});

const result = await response.json();
console.log('Threads post scheduled:', result.id);`,
  faq: [
    {
      q: "How do I connect my Threads account?",
      a: "Threads uses Instagram Business account authentication. Connect your Instagram Business account through InstaEdit's dashboard, and you'll automatically get access to post to Threads as well.",
    },
    {
      q: "What's the character limit for Threads?",
      a: "Threads has a 500 character limit per post. InstaEdit automatically validates content length and will warn if your content exceeds the limit.",
    },
    {
      q: "Can I schedule Threads posts with other platforms?",
      a: "Absolutely. InstaEdit's unified API lets you post to Threads alongside TikTok, Instagram, Facebook, YouTube, LinkedIn, and Twitter/X in a single API call.",
    },
  ],
} satisfies PlatformData;
