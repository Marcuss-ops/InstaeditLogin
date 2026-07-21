import { Seo } from "../components/seo/Seo";
import { Nav } from "../components/landing/Nav";
import { Hero } from "./landing/Hero";
import { Pipeline } from "../components/landing/Pipeline";
import { StatsStrip } from "../components/landing/StatsStrip";
import { Workflow } from "../components/landing/Workflow";
import { Features } from "../components/landing/Features";
import { Results } from "../components/landing/Results";
import { Agency } from "../components/landing/Agency";
import { ProblemSolution } from "../components/landing/ProblemSolution";
import { CTASection } from "./landing/CTASection";
import { Shorts } from "../components/landing/Shorts";
import { LongForm } from "../components/landing/LongForm";
import { Footer } from "../components/landing/Footer";
import { WhoAreWe } from "../components/landing/WhoAreWe";
import { FAQ } from "../components/landing/FAQ";
import { FinalCTA } from "../components/landing/FinalCTA";

export function Landing() {
  return (
    <>
      <Seo title="InstaEdit — Publish Everywhere" description="Your creativity. Our distribution." canonical="https://app.instaedit.org/" />
      <Nav />
      <Hero />
      <Pipeline />
      <StatsStrip />
      <Workflow />
      <Features />
      <Results />
      <Agency />
      <ProblemSolution />
      <CTASection />
      <Shorts />
      <LongForm />
      <WhoAreWe />
      <FAQ />
      <FinalCTA />
      <Footer />
    </>
  );
}
