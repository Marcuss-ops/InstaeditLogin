import { Zap, Shield, RefreshCw } from "lucide-react";
import type { PlatformData } from "../platformData";

export default {
  slug: "tiktok",
  name: "TikTok",
  color: "#ff0050",
  icon: (
    <svg viewBox="0 0 24 24" fill="currentColor" className="w-6 h-6">
      <path d="M19.6 8.2c-1.2 0-2.3-.4-3.2-1.1v6.4c0 3.5-2.8 6.3-6.3 6.3S3.8 17 3.8 13.5 6.6 7.2 10.1 7.2c.4 0 .7 0 1 .1v2.8c-.3-.1-.7-.2-1-.2-1.9 0-3.5 1.6-3.5 3.6s1.6 3.5 3.5 3.5 3.5-1.6 3.5-3.5V3.5h2.7c.3 1.2 1.3 2.2 2.5 2.5v2.2z" />
    </svg>
  ),
  heroTagline: "Ship your TikTok integration in minutes, not months",
  heroDescription:
    "Stop wrestling with TikTok Content Posting API. InstaEdit handles OAuth, rate limits, media hosting, and API changes — so you can focus on building your product.",
  noteTitle: "TikTok Creator or Business Account",
  noteDescription:
    "TikTok's Content Posting API works with both Creator and Business accounts. Connect any TikTok account through our simple OAuth flow and start posting viral content. Business accounts unlock additional analytics and promotion features.",
  contentTypes: ["Videos", "Photo Mode"],
  features: [
    {
      icon: <Zap className="w-5 h-5" />,
      title: "Ship faster",
      description:
        "Go from zero to posting in under 30 seconds. No TikTok developer app approval process — just get your API key and start building.",
    },
    {
      icon: <Shield className="w-5 h-5" />,
      title: "Official API, zero hassle",
      description:
        "We use TikTok's official Content Posting API under the hood. You get full compliance and reliability without the integration pain.",
    },
    {
      icon: <RefreshCw className="w-5 h-5" />,
      title: "We handle the hard parts",
      description:
        "Rate limits, token refresh, video transcoding, error handling — all managed for you. Focus on your product, not infrastructure.",
    },
  ],
  comparison: {
    us: {
      label: "InstaEdit API",
      items: [
        "Simple API key — start in 30 seconds",
        "Automatic retries & queue management",
        "Upload directly — we transcode for TikTok",
        "Zero maintenance forever",
        "One API for 7 platforms",
      ],
    },
    them: {
      label: "TikTok Content Posting API",
      items: [
        "Complex OAuth with TikTok developer app approval",
        "Strict rate limits with complex backoff logic",
        "Video must meet strict encoding requirements",
        "Frequent API changes require constant updates",
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
      platform: 'tiktok',
      accountId: 'your-tiktok-account-id'
    }],
    content: 'Behind the scenes of our latest project!',
    mediaItems: [{
      type: 'video',
      url: 'https://your-video-url.mp4'
    }],
    scheduledFor: '2025-01-15T14:00:00Z'
  })
});

const result = await response.json();
console.log('TikTok scheduled:', result.id);`,
  faq: [
    {
      q: "How long does TikTok API approval take?",
      a: "TikTok's Content Posting API approval can take days to weeks. With InstaEdit, there's no approval process; get your API key and start posting immediately.",
    },
    {
      q: "What video formats does the TikTok API accept?",
      a: "Upload MP4 videos up to 10 minutes with 1080p resolution. InstaEdit automatically transcodes to formats TikTok accepts.",
    },
    {
      q: "Can I schedule TikTok posts in advance?",
      a: "Yes. Set a scheduledFor timestamp in your API call and InstaEdit publishes at the exact time. We handle rate limits and queue requests if TikTok imposes temporary throttling.",
    },
    {
      q: "Can I cross-post TikTok videos to other platforms?",
      a: "Absolutely. The same API call can publish to TikTok, Instagram Reels, YouTube Shorts, and other platforms simultaneously.",
    },
  ],
} satisfies PlatformData;
