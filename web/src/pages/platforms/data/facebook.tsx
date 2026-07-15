import { Zap, Shield, RefreshCw } from "lucide-react";
import type { PlatformData } from "../platformData";

export default {
  slug: "facebook",
  name: "Facebook",
  color: "#0A84FF",
  icon: (
    <svg viewBox="0 0 24 24" fill="currentColor" className="w-6 h-6">
      <path d="M13.5 22v-8h2.7l.4-3.2H13.5V8.5c0-.9.3-1.5 1.6-1.5h1.7V4.1c-.3 0-1.3-.1-2.5-.1-2.5 0-4.2 1.5-4.2 4.3v2.5H7.3V14h2.8v8h3.4z" />
    </svg>
  ),
  heroTagline: "Ship your Facebook integration in minutes, not months",
  heroDescription:
    "Stop wrestling with Meta's Business API. InstaEdit handles OAuth, rate limits, page management, and API changes — so you can focus on building your product.",
  noteTitle: "Facebook Business Page Required",
  noteDescription:
    "Facebook API integration requires a Business page. Personal profiles cannot use automated posting APIs due to Meta's restrictions. You must be an admin of the page to connect it.",
  contentTypes: ["Text Posts", "Photos", "Videos", "Links"],
  features: [
    {
      icon: <Zap className="w-5 h-5" />,
      title: "Ship faster",
      description:
        "Go from zero to posting in under 30 seconds. No Facebook app review, no Business Manager headaches — just get your API key and start building.",
    },
    {
      icon: <Shield className="w-5 h-5" />,
      title: "Official API, zero hassle",
      description:
        "We use Meta's official Business API under the hood. You get full compliance and reliability without the integration pain.",
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
      label: "Meta Business API",
      items: [
        "Complex OAuth with Facebook Business verification",
        "Rate limit management with backoff logic",
        "Media must meet strict Meta requirements",
        "Constant updates when Graph API changes",
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
      platform: 'facebook',
      accountId: 'your-facebook-page-id'
    }],
    content: 'Exciting news! Check out our latest update.',
    mediaItems: [{
      type: 'image',
      url: 'https://your-image-url.jpg'
    }],
    scheduledFor: '2025-01-15T14:00:00Z'
  })
});

const result = await response.json();
console.log('Scheduled successfully:', result.id);`,
  faq: [
    {
      q: "Do I need a Facebook Business page?",
      a: "Yes, Facebook API integration requires a Facebook Business page. You cannot post to personal profiles via API due to Meta's restrictions.",
    },
    {
      q: "Why use InstaEdit instead of Meta's API directly?",
      a: "Meta's Business API requires Facebook app verification, complex OAuth flows, media hosting, rate limit management, and ongoing maintenance. InstaEdit abstracts all of that.",
    },
    {
      q: "Can I post to Facebook Groups via API?",
      a: "Facebook restricts automated posting to Groups via third-party APIs. InstaEdit supports posting to Facebook Pages, which is the standard for business and brand automation.",
    },
    {
      q: "Can I cross-post to Instagram from the same API call?",
      a: "Yes. Include both 'facebook' and 'instagram' in your platforms array. InstaEdit optimizes media and content for each platform automatically.",
    },
  ],
} satisfies PlatformData;
