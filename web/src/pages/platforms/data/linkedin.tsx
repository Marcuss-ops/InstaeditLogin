import { Zap, Shield, RefreshCw } from "lucide-react";
import type { PlatformContent } from "../platformData";

export default {
  slug: "linkedin",
  name: "LinkedIn",
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
} satisfies PlatformContent;
