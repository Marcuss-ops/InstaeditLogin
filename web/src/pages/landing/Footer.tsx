import { Link } from "react-router-dom";
import { Zap } from "lucide-react";

/* ----------------------------------------------------------------------------
 * Footer
 * -------------------------------------------------------------------------- */

export const SEO = {
  title: "InstaEdit — Create great content. We handle the distribution.",
  description:
    "You bring the idea, we make sure the world sees it. AI-powered video creation with ChronoN, multi-platform publishing to YouTube, TikTok, Instagram and more. Built for creators, teams, and agencies.",
  canonical: "https://app.instaedit.org/",
} as const;

export function Footer() {
  const cols: Array<{ heading: string; links: Array<{ l: string; to?: string; href?: string }> }> = [
    {
      heading: "Product",
      links: [
        { l: "How it works", href: "#features" },
        { l: "Programs", href: "#programs" },
        { l: "Results", href: "#results" },
        { l: "FAQ", href: "#faq" },
      ],
    },
    {
      heading: "Legal",
      links: [
        { l: "Privacy", to: "/privacy" },
        { l: "Terms", to: "/terms" },
        { l: "Data deletion", href: "/data-deletion.html" },
      ],
    },
  ];

  return (
    <footer className="relative border-t border-white/10 bg-[#08080d]">
      <div className="mx-auto max-w-7xl px-6 py-16 grid gap-12 lg:grid-cols-12">
        <div className="lg:col-span-5">
          <Link to="/" className="flex items-center gap-2">
            <span className="inline-flex w-8 h-8 items-center justify-center rounded-lg bg-white text-black">
              <Zap className="w-4 h-4" />
            </span>
            <span className="font-bold tracking-tight text-white text-base">InstaEdit</span>
          </Link>
          <p className="text-sm text-zinc-400 mt-4 max-w-[42ch] leading-relaxed">
            Channel automation built for YouTube monetization. Go from zero to a
            revenue-generating channel in under 3 weeks.
          </p>
          <div className="inline-flex items-center gap-2 mt-5 surface-glass border border-white/10 px-3 py-1.5 rounded-full">
            <span className="w-2 h-2 rounded-full bg-red-500" />
            <span className="text-xs text-zinc-300 font-medium">Built for YouTube monetization</span>
          </div>
        </div>
        <div className="lg:col-span-7 grid grid-cols-1 sm:grid-cols-2 gap-8">
          {cols.map((col) => (
            <div key={col.heading}>
              <div className="text-eyebrow text-zinc-500 mb-4">{col.heading}</div>
              <ul className="space-y-3">
                {col.links.map((link) => {
                  const className = "text-sm text-zinc-300 hover:text-white transition-colors";
                  if (link.to) {
                    return (<li key={link.l}><Link to={link.to} className={className}>{link.l}</Link></li>);
                  }
                  return (<li key={link.l}><a href={link.href} className={className}>{link.l}</a></li>);
                })}
              </ul>
            </div>
          ))}
        </div>
      </div>
      <div className="border-t border-white/5">
        <div className="mx-auto max-w-7xl px-6 py-6 flex flex-col sm:flex-row items-center justify-between gap-3 text-xs text-zinc-500">
          <div>© {new Date().getFullYear()} InstaEdit, Inc.</div>
          <div>Built for creators who want to grow.</div>
        </div>
      </div>
    </footer>
  );
}
