import { Seo } from "../../components/seo/Seo";
import { DonneNav } from "./DonneNav";
import { DonneFooter } from "./DonneFooter";
import { Hero } from "./Hero";
import { Problem } from "./Problem";
import { Shortcut } from "./Shortcut";
import { Earnings } from "./Earnings";
import { HowItWorks } from "./HowItWorks";
import { Results } from "./Results";
import { FounderStory } from "./FounderStory";
import { FAQSection } from "./FAQSection";
import { FinalCTA } from "./FinalCTA";
import { SEO } from "./content";

/**
 * Landing "HerChannel AI" — progetto indipendente e volutamente
 * separato dalle altre landing del sito (/, /programs, /mentoring...).
 *
 * - Tutta la copia italiana vive in `./content.ts` (un unico file da
 *   modificare quando il titolare aggiorna i contenuti).
 * - Navigazione e footer sono propri (`DonneNav`, `DonneFooter`) e non
 *   riusano quelli del sito principale, così la pagina resta isolata.
 * - Gli screenshot dei risultati riusano le stesse foto della landing
 *   principale InstaEdit tramite `components/landing/ResultsGallery.tsx`.
 * - Route: /donne (vedi App.tsx).
 */
export function DonneLanding() {
  return (
    <div className="min-h-screen bg-[#030308]">
      <Seo {...SEO} />
      <DonneNav />
      <main>
        <Hero />
        <Problem />
        <Shortcut />
        <Earnings />
        <HowItWorks />
        <Results />
        <FounderStory />
        <FAQSection />
        <FinalCTA />
      </main>
      <DonneFooter />
    </div>
  );
}

export default DonneLanding;
