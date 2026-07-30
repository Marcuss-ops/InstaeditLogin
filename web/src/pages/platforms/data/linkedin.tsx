import { Zap, Shield, RefreshCw } from "lucide-react";
import type { PlatformData } from "../platformData";

export default {
  slug: "linkedin",
  name: "LinkedIn",
  color: "#0A66C2",
  icon: (
    <svg viewBox="0 0 24 24" fill="currentColor" className="w-6 h-6">
      <path d="M20.5 2h-17A1.5 1.5 0 002 3.5v17A1.5 1.5 0 003.5 22h17a1.5 1.5 0 001.5-1.5v-17A1.5 1.5 0 0020.5 2zM8 19H5v-9h3zM6.5 8.25A1.75 1.75 0 118.3 6.5a1.78 1.78 0 01-1.8 1.75zM19 19h-3v-4.74c0-1.42-.6-1.93-1.38-1.93A1.74 1.74 0 0013 14.19a.66.66 0 000 .14V19h-3v-9h2.9v1.3a3.11 3.11 0 012.7-1.4c1.55 0 3.4.86 3.4 3.66z" />
    </svg>
  ),
  heroTagline: "Ship your LinkedIn integration in minutes, not months",
  heroDescription:
    "Stop wrestling with LinkedIn Posts API. InstaEdit handles OAuth, organization management, and API changes — so you can focus on building your product.",
  noteTitle: "LinkedIn Personal or Company Page",
  noteDescription:
    "LinkedIn integration works with both personal profiles and Company Pages. Connect your LinkedIn account through our simple OAuth flow and start publishing professional content.",
  contentTypes: ["Text Posts", "Images", "Articles", "Videos"],
  features: [
    {
      icon: <Zap className="w-5 h-5" />,
      title: "Ship faster",
      description:
        "Go from zero to posting in under 30 seconds. No LinkedIn app review process — just get your API key and start building.",
    },
    {
      icon: <Shield className="w-5 h-5" />,
      title: "Official API, zero hassle",
      description:
        "We use LinkedIn's official Posts API under the hood. You get full compliance and reliability without the integration pain.",
    },
    {
      icon: <RefreshCw className="w-5 h-5" />,
      title: "We handle the hard parts",
      description:
        "Rate limits, token refresh, media processing, error handling — all managed for you. Focus on your content, not infrastructure.",
    },
  ],
  comparison: {
    us: {
      label: "InstaEdit API",
      items: [
        "Simple API key — start in 30 seconds",
        "Automatic retries & queue management",
        "Upload directly — we optimize for LinkedIn",
        "Zero maintenance forever",
        "One API for 7 platforms",
      ],
    },
    them: {
      label: "LinkedIn Posts API Direct",
      items: [
        "Complex OAuth with LinkedIn app approval",
        "Strict rate limits with member restrictions",
        "Media must meet LinkedIn's requirements",
        "Frequent API changes require updates",
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
      platform: 'linkedin',
      accountId: 'your-linkedin-account-id'
    }],
    content: 'Excited to share our latest product update!',
    mediaItems: [{
      type: 'image',
      url: 'https://your-image-url.jpg'
    }],
    scheduledFor: '2025-01-15T09:00:00Z'
  })
});

const result = await response.json();
console.log('LinkedIn scheduled:', result.id);`,
  faq: [
    {
      q: "Can I post to LinkedIn Company Pages?",
      a: "Yes. InstaEdit supports both personal profiles and Company Pages. Just select the appropriate account when connecting.",
    },
    {
      q: "Can I schedule LinkedIn posts for optimal times?",
      a: "Yes. Set a scheduledFor timestamp and InstaEdit publishes at the exact time. Tuesday through Thursday mornings tend to perform best on LinkedIn.",
    },
    {
      q: "Can I cross-post to LinkedIn from the same API call?",
      a: "Yes. Include 'linkedin' in your platforms array alongside other platforms. InstaEdit optimizes content format for each platform.",
    },
  ],
} satisfies PlatformData;
