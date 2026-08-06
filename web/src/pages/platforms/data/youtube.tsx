import { Zap, RefreshCw, Upload } from "lucide-react";
import type { PlatformContent } from "../platformData";

export default {
  slug: "youtube",
  name: "YouTube",
  heroTagline: "Ship your YouTube integration in minutes, not months",
  heroDescription:
    "Stop wrestling with YouTube Data API. InstaEdit handles OAuth, resumable uploads, quota management, and API changes — so you can focus on building your product.",
  noteTitle: "YouTube Channel Required",
  noteDescription:
    "YouTube integration requires a channel with upload permissions. Google's OAuth consent screen must be configured for your project. InstaEdit handles all the complexity of resumable uploads and video processing.",
  contentTypes: ["Videos", "Shorts", "Thumbnails"],
  features: [
    {
      icon: <Zap className="w-5 h-5" />,
      title: "Ship faster",
      description:
        "Go from zero to posting in under 30 seconds. No Google Cloud project setup headaches — just get your API key and start building.",
    },
    {
      icon: <Upload className="w-5 h-5" />,
      title: "Resumable uploads",
      description:
        "Large video files? No problem. InstaEdit handles resumable uploads so your content gets through even on slow connections.",
    },
    {
      icon: <RefreshCw className="w-5 h-5" />,
      title: "We handle the hard parts",
      description:
        "Quota management, token refresh, video processing, error handling — all managed for you. Focus on your content, not infrastructure.",
    },
  ],
  comparison: {
    us: {
      label: "InstaEdit API",
      items: [
        "Simple API key — start in 30 seconds",
        "Automatic retries & resumable uploads",
        "Upload directly — we process for YouTube",
        "Zero maintenance forever",
        "One API for 7 platforms",
      ],
    },
    them: {
      label: "YouTube Data API Direct",
      items: [
        "Complex OAuth with Google Cloud project setup",
        "Strict quota limits with daily caps",
        "Resumable upload protocol complexity",
        "Frequent API version changes",
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
      platform: 'youtube',
      accountId: 'your-youtube-channel-id'
    }],
    content: 'Check out our latest tutorial!',
    title: 'How to Build a Content Pipeline',
    mediaItems: [{
      type: 'video',
      url: 'https://your-video-url.mp4'
    }],
    scheduledFor: '2025-01-15T16:00:00Z'
  })
});

const result = await response.json();
console.log('YouTube scheduled:', result.id);`,
  faq: [
    {
      q: "What video formats does YouTube accept?",
      a: "YouTube accepts MP4, MOV, AVI, and many other formats. InstaEdit automatically transcodes to YouTube's preferred format.",
    },
    {
      q: "Can I schedule YouTube videos in advance?",
      a: "Yes. Set a scheduledFor timestamp and InstaEdit publishes at the exact time. We handle resumable uploads and quota management.",
    },
    {
      q: "Can I cross-post YouTube videos to other platforms?",
      a: "Yes. The same API call can publish to YouTube, TikTok, Instagram Reels, and other platforms simultaneously.",
    },
  ],
} satisfies PlatformContent;
