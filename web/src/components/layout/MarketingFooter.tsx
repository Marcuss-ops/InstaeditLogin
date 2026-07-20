import { Link } from "react-router-dom";
import { Zap } from "lucide-react";
import { PLATFORM_REGISTRY } from "../marketing/PlatformLogos";

const COLS: Array<{ heading: string; links: Array<{ l: string; to?: string; href?: string }> }> = [
  {
    heading: "Prodotto",
    links: [
      { l: "Pipeline AI", href: "/#pipeline" },
      { l: "Workflow", href: "/#workflow" },
      { l: "Features", href: "/#features" },
      { l: "Per agenzie", href: "/#agency" },
      { l: "Programmi", to: "/programs" },
    ],
  },
  {
    heading: "Legale",
    links: [
      { l: "Privacy", to: "/privacy" },
      { l: "Termini", to: "/terms" },
      { l: "Data deletion", href: "/data-deletion.html" },
    ],
  },
];

export function MarketingFooter() {
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
            Infrastruttura di pubblicazione multi-piattaforma per team che producono contenuti ogni giorno.
            Un render, ogni canale, ogni volta.
          </p>
          <div className="flex items-center gap-2 mt-5">
            {PLATFORM_REGISTRY.map(({ key, name, Logo }) => (
              <span key={key} className="inline-flex w-7 h-7 rounded-md overflow-hidden ring-1 ring-white/10 surface-glass" title={name} aria-label={name}>
                <Logo className="w-full h-full" />
              </span>
            ))}
          </div>
        </div>
        <div className="lg:col-span-7 grid grid-cols-1 sm:grid-cols-2 gap-8">
          {COLS.map((col) => (
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
          <div>Costruito per creator e team di content operations.</div>
        </div>
      </div>
    </footer>
  );
}
