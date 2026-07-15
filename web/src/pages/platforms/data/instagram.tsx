import { Zap, Shield, RefreshCw } from "lucide-react";
import type { PlatformData } from "../platformData";

export default {
  slug: "instagram",
  name: "Instagram",
  color: "#E1306C",
  icon: (
    <svg viewBox="0 0 24 24" fill="currentColor" className="w-6 h-6">
      <path d="M12 2.2c3.2 0 3.6 0 4.9.1 1.2.1 1.8.3 2.2.4.6.2 1 .5 1.4.9.4.4.7.8.9 1.4.2.4.3 1 .4 2.2.1 1.3.1 1.7.1 4.9s0 3.6-.1 4.9c-.1 1.2-.2 1.8-.4 2.2-.2.6-.5 1-.9 1.4-.4.4-.8.7-1.4.9-.4.2-1 .3-2.2.4-1.3.1-1.7.1-4.9.1s-3.6 0-4.9-.1c-1.2-.1-1.8-.2-2.2-.4-.6-.2-1-.5-1.4-.9-.4-.4-.7.8-.9 1.4-.2.4-.3 1-.4 2.2-.1 1.3-.1 1.7-.1 4.9s0 3.6.1 4.9c.1 1.2.3 1.8.4 2.2.2.6.5 1 .9 1.4.4.4.8.7 1.4.9.4.2 1 .3 2.2.4 1.3.1 1.7.1 4.9.1s3.6 0 4.9-.1c1.1 0 1.7-.2 2.1-.4.5-.2.9-.5 1.3-.9.4-.4.8.7.9 1.3.2.4.4 1 .4 2.1.1 1.2.1 1.5.1 4.6s0 3.4-.1 4.6c0 1.1-.2 1.7-.4 2.1-.2.5-.5.9-.9 1.3-.4.4-.8.7-1.3.9-.4.2-1 .4-2.1.4-1.2.1-1.5.1-4.6.1s-3.4 0-4.6-.1c-1.1 0-1.7-.2-2.1-.4-.5-.2-.9-.5-1.3-.9-.4-.4-.8-.7-.9-1.3-.2-.4-.4-1-.4-2.1-.1-1.2-.1-1.5-.1-4.6s0-3.4.1-4.6c0-1.1.2-1.7.4-2.1.2-.5.5-.9.9-1.3.4-.4.8-.7 1.3-.9.4-.2 1-.4 2.1-.4 1.2-.1 1.5-.1 4.6-.1zm0 3.4a4.4 4.4 0 110 8.8 4.4 4.4 0 010-8.8zm0 1.8a2.6 2.6 0 100 5.2 2.6 2.6 0 000-5.2zm5.7-3.6a1 1 0 110 2 1 1 0 010-2z" />
    </svg>
  ),
  heroTagline: "Ship your Instagram integration in minutes, not months",
  heroDescription:
    "Stop wrestling with Instagram Graph API. InstaEdit handles OAuth, rate limits, media hosting, and API changes — so you can focus on building your product.",
  noteTitle: "Instagram Business Account Required",
  noteDescription:
    "Instagram integration only works with Business accounts. Personal and Creator accounts cannot use automated posting APIs due to Instagram's restrictions. You can convert to a Business account for free in your Instagram app settings.",
  contentTypes: ["Photos", "Videos", "Stories", "Carousels", "Reels"],
  features: [
    {
      icon: <Zap className="w-5 h-5" />,
      title: "Ship faster",
      description:
        "Go from zero to posting in under 30 seconds. No Facebook app review, no Business Manager setup — just get your API key and start building.",
    },
    {
      icon: <Shield className="w-5 h-5" />,
      title: "Official API, zero hassle",
      description:
        "We use Instagram's official Business API under the hood. You get full compliance and reliability without the integration pain.",
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
        "Simple API key authentication — no OAuth complexity",
        "We handle rate limits automatically with smart queuing",
        "Upload media directly — we host and optimize it for you",
        "Zero maintenance — we handle all API changes",
        "One integration for 7 social platforms",
      ],
    },
    them: {
      label: "Instagram Graph API Direct",
      items: [
        "Complex OAuth 2.0 flow with Facebook Business verification",
        "Strict rate limits requiring careful management",
        "Media must be hosted on publicly accessible URLs",
        "Frequent API changes require constant code updates",
        "Separate integration needed for each platform",
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
      platform: 'instagram',
      accountId: 'your-instagram-account-id'
    }],
    content: 'Beautiful sunset at the beach!',
    mediaItems: [{
      type: 'image',
      url: 'https://your-image-url.jpg'
    }],
    scheduledFor: '2025-01-15T19:00:00Z'
  })
});

const result = await response.json();
console.log('Scheduled successfully:', result.id);`,
  faq: [
    {
      q: "Is the Instagram API free to use?",
      a: "Instagram's Graph API is free but requires significant development investment. InstaEdit replaces all that build time with a single API call.",
    },
    {
      q: "Can I post Instagram Reels via API?",
      a: "Yes. Upload short-form video and InstaEdit publishes it as a Reel with captions, hashtags, and mentions.",
    },
    {
      q: "How does InstaEdit handle Instagram's rate limits?",
      a: "InstaEdit manages rate limits automatically with intelligent queuing and exponential backoff retries.",
    },
    {
      q: "Can I schedule Instagram Stories via API?",
      a: "Yes. Set the content type to 'story' in your API call and InstaEdit handles format optimization and scheduling.",
    },
  ],
} satisfies PlatformData;
