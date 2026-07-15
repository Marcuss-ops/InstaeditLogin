import {
  Zap,
  Shield,
  RefreshCw,
  Upload,
} from "lucide-react";

export type PlatformData = {
  slug: string;
  name: string;
  color: string;
  icon: React.ReactNode;
  heroTagline: string;
  heroDescription: string;
  noteTitle: string;
  noteDescription: string;
  contentTypes: string[];
  features: { icon: React.ReactNode; title: string; description: string }[];
  comparison: {
    us: { label: string; items: string[] };
    them: { label: string; items: string[] };
  };
  codeExample: string;
  faq: { q: string; a: string }[];
};

export const PLATFORMS: Record<string, PlatformData> = {
  tiktok: {
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
  },

  instagram: {
    slug: "instagram",
    name: "Instagram",
    color: "#E1306C",
    icon: (
      <svg viewBox="0 0 24 24" fill="currentColor" className="w-6 h-6">
        <path d="M12 2.2c3.2 0 3.6 0 4.9.1 1.2.1 1.8.3 2.2.4.6.2 1 .5 1.4.9.4.4.7.8.9 1.4.2.4.3 1 .4 2.2.1 1.3.1 1.7.1 4.9s0 3.6-.1 4.9c-.1 1.2-.2 1.8-.4 2.2-.2.6-.5 1-.9 1.4-.4.4-.8.7-1.4.9-.4.2-1 .3-2.2.4-1.3.1-1.7.1-4.9.1s-3.6 0-4.9-.1c-1.2-.1-1.8-.2-2.2-.4-.6-.2-1-.5-1.4-.9-.4-.4-.7-.8-.9-1.4-.2-.4-.3-1-.4-2.2-.1-1.3-.1-1.7-.1-4.9s0-3.6.1-4.9c.1-1.2.3-1.8.4-2.2.2-.6.5-1 .9-1.4.4-.4.8-.7 1.4-.9.4-.2 1-.3 2.2-.4C8.4 2.2 8.8 2.2 12 2.2zm0 2c-3.1 0-3.4 0-4.6.1-1.1 0-1.7.2-2.1.4-.5.2-.9.5-1.3.9-.4.4-.7.8-.9 1.3-.2.4-.4 1-.4 2.1-.1 1.2-.1 1.5-.1 4.6s0 3.4.1 4.6c0 1.1.2 1.7.4 2.1.2.5.5.9.9 1.3.4.4.8.7 1.3.9.4.2 1 .4 2.1.4 1.2.1 1.5.1 4.6.1s3.4 0 4.6-.1c1.1 0 1.7-.2 2.1-.4.5-.2.9-.5 1.3-.9.4-.4.8-.7.9-1.3.2-.4.4-1 .4-2.1.1-1.2.1-1.5.1-4.6s0-3.4-.1-4.6c0-1.1-.2-1.7-.4-2.1-.2-.5-.5-.9-.9-1.3-.4-.4-.8-.7-1.3-.9-.4-.2-1-.4-2.1-.4-1.2-.1-1.5-.1-4.6-.1zm0 3.4a4.4 4.4 0 110 8.8 4.4 4.4 0 010-8.8zm0 1.8a2.6 2.6 0 100 5.2 2.6 2.6 0 000-5.2zm5.7-3.6a1 1 0 110 2 1 1 0 010-2z" />
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
  },

  facebook: {
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
  },

  threads: {
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
  },

  youtube: {
    slug: "youtube",
    name: "YouTube",
    color: "#ff0000",
    icon: (
      <svg viewBox="0 0 24 24" fill="currentColor" className="w-6 h-6">
        <path d="M21.6 7.2c-.2-.8-.8-1.4-1.6-1.6-1.6-.4-8-.4-8-.4s-6.4 0-8 .4c-.8.2-1.4.8-1.6 1.6C2 8.8 2 12 2 12s0 3.2.4 4.8c.2.8.8 1.4 1.6 1.6 1.6.4 8 .4 8 .4s6.4 0 8-.4c.8-.2 1.4-.8 1.6-1.6.4-1.6.4-4.8.4-4.8s0-3.2-.4-4.8zM10 15.2V8.8l5.2 3.2-5.2 3.2z" />
      </svg>
    ),
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
  },

  linkedin: {
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
  },

  twitter: {
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
  },
};
