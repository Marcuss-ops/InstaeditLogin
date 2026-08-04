import { Link } from "react-router-dom";
import { Bot, Zap } from "lucide-react";
import { FOOTER } from "./content";

/**
 * Footer autonomo della landing "DonneTube" (italiano).
 * Separato da `MarketingFooter`: appartiene al progetto indipendente.
 */
export function DonneFooter() {
  return (
    <footer id="contatti" className="relative border-t border-white/10 bg-[#08080d]">
      <div className="mx-auto max-w-7xl px-6 py-16 grid gap-12 lg:grid-cols-12">
        <div className="lg:col-span-6">
          <Link to="/donne" className="flex items-center gap-2">
            <span className="inline-flex w-8 h-8 items-center justify-center rounded-lg bg-gradient-to-br from-pink-500 to-rose-500 text-white">
              <Bot className="w-4 h-4" />
            </span>
            <span className="font-bold tracking-tight text-white text-base">
              {FOOTER.brand}
            </span>
          </Link>
          <p className="text-sm text-zinc-400 mt-4 max-w-[48ch] leading-relaxed">
            {FOOTER.description}
          </p>
          <div className="inline-flex items-center gap-2 mt-5 px-3 py-1.5 rounded-full surface-glass border border-emerald-400/30 text-xs font-medium text-emerald-200">
            <Zap className="w-3.5 h-3.5" />
            <span>{FOOTER.badge}</span>
          </div>
        </div>
        <div className="lg:col-span-6 grid grid-cols-1 sm:grid-cols-2 gap-8">
          <div>
            <div className="text-eyebrow text-zinc-500 mb-4">{FOOTER.productHeading}</div>
            <ul className="space-y-3">
              {FOOTER.productLinks.map((link) => (
                <li key={link.label}>
                  <a href={link.href} className="text-sm text-zinc-300 hover:text-white transition-colors">
                    {link.label}
                  </a>
                </li>
              ))}
            </ul>
          </div>
          <div>
            <div className="text-eyebrow text-zinc-500 mb-4">{FOOTER.legalHeading}</div>
            <ul className="space-y-3">
              {FOOTER.legal.map((link) =>
                "to" in link && link.to ? (
                  <li key={link.label}>
                    <Link to={link.to} className="text-sm text-zinc-300 hover:text-white transition-colors">
                      {link.label}
                    </Link>
                  </li>
                ) : (
                  <li key={link.label}>
                    <a href={(link as { href: string }).href} className="text-sm text-zinc-300 hover:text-white transition-colors">
                      {link.label}
                    </a>
                  </li>
                ),
              )}
            </ul>
          </div>
        </div>
      </div>
      <div className="border-t border-white/5">
        <div className="mx-auto max-w-7xl px-6 py-6 flex flex-col sm:flex-row items-center justify-between gap-3 text-xs text-zinc-500">
          <div className="flex flex-wrap items-center gap-4">
            <span>© {new Date().getFullYear()} {FOOTER.brand}, Inc.</span>
            <Link to={FOOTER.mainSite.to} className="text-zinc-400 hover:text-white transition-colors">
              {FOOTER.mainSite.label}
            </Link>
          </div>
          <div>{FOOTER.copyright}</div>
        </div>
      </div>
    </footer>
  );
}
