import { Seo } from "../components/seo/Seo";
import { Nav } from "../components/landing/Nav";
import { Hero } from "./landing/Hero";
import { Features } from "./landing/Features";
import { ResultsSection } from "./landing/ResultsSection";
import { CTASection } from "./landing/CTASection";
import { Proof } from "./landing/Proof";
import { FAQ } from "./landing/FAQ";
import { FinalCTA } from "./landing/FinalCTA";
import { Footer } from "./landing/Footer";

export function Landing() {
  return (
    <>
      <Seo
        title="InstaEdit — YouTube Income in Under 3 Weeks"
        description="Build an profitable English-language YouTube channel from scratch and start earning online in less than 3 weeks. Done-with-you automation and mentoring."
        canonical="https://app.instaedit.org/"
      />
      <Nav />
      <Hero />
      <Features />
      <ResultsSection />
      <CTASection />
      <Proof />
      <FAQ />
      <FinalCTA />
      <Footer />
    </>
  );
}
