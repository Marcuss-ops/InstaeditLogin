import { BrowserRouter, Routes, Route } from "react-router-dom";
import { Nav } from "./components/Nav";
import { Hero } from "./components/Hero";
import { Features } from "./components/Features";
import { Monetize } from "./components/Monetize";
import { CTA } from "./components/CTA";
import { Footer } from "./components/Footer";
import { Login } from "./pages/Login";
import { Dashboard } from "./pages/Dashboard";

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
      </Routes>
    </BrowserRouter>
  );
}

export default App;
