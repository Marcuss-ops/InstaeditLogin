import { Seo } from "../components/seo/Seo";
import { Nav } from "../components/landing/Nav";
import { ProblemSolution } from "../components/landing/ProblemSolution";
import { Hero } from "./landing/Hero";
import { Features } from "./landing/Features";
import { EarningsEstimates } from "./landing/EarningsEstimates";
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
        title="InstaEdit — Your First $2,000/Mo From YouTube, On Autopilot"
        description="Earn $2,000+/mo from YouTube on autopilot — zero experience, zero camera, zero editing. AI-powered channel automation and 1-on-1 mentoring."
        canonical="https://app.instaedit.org/"
      />
      <Nav />
      <Hero />
      <ProblemSolution />
      <EarningsEstimates />
      <Features />
      <ResultsSection />
      <Proof />
      <CTASection />
      <FAQ />
      <FinalCTA />
      <Footer />
    </>
  );
}
