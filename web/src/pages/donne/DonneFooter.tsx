import { Link } from "react-router-dom";
import { Bot, Zap } from "lucide-react";
import { FOOTER } from "./content";

/**
 * Footer autonomo della landing "DonneTube" (italiano) in tema chiaro.
 * Separato da `MarketingFooter`: appartiene al progetto indipendente.
 */
export function DonneFooter() {
  return (
    <footer id="contatti" className="relative border-t border-[#E8D8DB] bg-[#FFFFFF]">
      <div className="mx-auto max-w-7xl px-6 py-16 grid gap-12 lg:grid-cols-12">
        <div className="lg:col-span-6">
          <Link to="/donnetube" className="flex items-center gap-2">
            <span className="inline-flex w-8 h-8 items-center justify-center rounded-lg bg-gradient-to-br from-[#E07A5F] to-[#E28743] text-white">
              <Bot className="w-4 h-4" />
            </span>
            <span className="font-bold tracking-tight text-[#4A3E56] text-base">
              {FOOTER.brand}
            </span>
          </Link>
          <p className="text-sm text-[#6E6677] mt-4 max-w-[48ch] leading-relaxed">
            {FOOTER.description}
          </p>
          <div className="inline-flex items-center gap-2 mt-5 px-3 py-1.5 rounded-full bg-[#EEF2EA] border border-[#DCE5D4] text-xs font-medium text-[#5F7A5A]">
            <Zap className="w-3.5 h-3.5" />
            <span>{FOOTER.badge}</span>
          </div>
        </div>
        <div className="lg:col-span-6 grid grid-cols-1 sm:grid-cols-2 gap-8">
          <div>
            <div className="text-eyebrow text-[#7A7280] mb-4">{FOOTER.productHeading}</div>
            <ul className="space-y-3">
              {FOOTER.productLinks.map((link) => (
                <li key={link.label}>
                  <a href={link.href} className="text-sm text-[#6E6677] hover:text-[#4A3E56] transition-colors">
                    {link.label}
                  </a>
                </li>
              ))}
            </ul>
          </div>
          <div>
            <div className="text-eyebrow text-[#7A7280] mb-4">{FOOTER.legalHeading}</div>
            <ul className="space-y-3">
              {FOOTER.legal.map((link) =>
                "to" in link && link.to ? (
                  <li key={link.label}>
                    <Link to={link.to} className="text-sm text-[#6E6677] hover:text-[#4A3E56] transition-colors">
                      {link.label}
                    </Link>
                  </li>
                ) : (
                  <li key={link.label}>
                    <a href={(link as { href: string }).href} className="text-sm text-[#6E6677] hover:text-[#4A3E56] transition-colors">
                      {link.label}
                    </a>
                  </li>
                ),
              )}
            </ul>
          </div>
        </div>
      </div>
      <div className="border-t border-[#F0E6E8]">
        <div className="mx-auto max-w-7xl px-6 py-6 flex flex-col sm:flex-row items-center justify-between gap-3 text-xs text-[#7A7280]">
          <div className="flex flex-wrap items-center gap-4">
            <span>© {new Date().getFullYear()} {FOOTER.brand}, Inc.</span>
            <Link to={FOOTER.mainSite.to} className="text-[#8A8290] hover:text-[#4A3E56] transition-colors">
              {FOOTER.mainSite.label}
            </Link>
          </div>
          <div>{FOOTER.copyright}</div>
        </div>
      </div>
    </footer>
  );
}
