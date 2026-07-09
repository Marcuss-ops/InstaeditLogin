import { BrowserRouter, Routes, Route } from "react-router-dom";
import { Nav } from "./components/Nav";
import { Hero } from "./components/Hero";
import { PromiseBanner } from "./components/PromiseBanner";
import { Features } from "./components/Features";
import { Monetize } from "./components/Monetize";
import { CTA } from "./components/CTA";
import { Footer } from "./components/Footer";
import { TikTokSection } from "./components/TikTokSection";
import { Login } from "./pages/Login";
import { Dashboard } from "./pages/Dashboard";
import { PrivacyPolicy } from "./pages/PrivacyPolicy";
import { TermsOfService } from "./pages/TermsOfService";

function LandingPage() {
  return (
    <div className="min-h-screen antialiased isolate">
      <div className="ambient-orbs" aria-hidden="true">
        <div className="orb orb-1"></div>
        <div className="orb orb-2"></div>
        <div className="orb orb-3"></div>
        <div className="orb orb-4"></div>
        <div className="orb orb-5"></div>
      </div>
      <Nav />
      <main>
        <Hero />
        <PromiseBanner />
        <Features />
        <Monetize />
        <CTA />
      </main>
      <Footer />
    </div>
  );
}

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<LandingPage />} />
        <Route path="/login" element={<Login />} />
        <Route path="/dashboard" element={<Dashboard />} />
        <Route path="/privacy" element={<PrivacyPolicy />} />
        <Route path="/terms" element={<TermsOfService />} />
        <Route path="/tiktok" element={<TikTokSection />} />
      </Routes>
    </BrowserRouter>
  );
}

export default App;
