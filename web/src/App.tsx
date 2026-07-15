import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { Landing } from "./pages/Landing";
import { Login } from "./pages/Login";
import { PrivacyPolicy } from "./pages/PrivacyPolicy";
import { TermsOfService } from "./pages/TermsOfService";
import { PlatformPage } from "./pages/platforms/PlatformPage";
import { CookieBanner } from "./components/CookieBanner";
import { ErrorBoundary } from "./components/feedback/ErrorBoundary";
import { ToastProvider } from "./components/toast";

function App() {
  return (
    <ToastProvider>
      <ErrorBoundary>
        <BrowserRouter>
          <CookieBanner />
          <Routes>
            <Route path="/" element={<Landing />} />
            <Route path="/login" element={<Login />} />
            <Route path="/privacy" element={<PrivacyPolicy />} />
            <Route path="/terms" element={<TermsOfService />} />

            {/* Platform pages */}
            <Route path="/tiktok" element={<PlatformPage />} />
            <Route path="/instagram" element={<PlatformPage />} />
            <Route path="/facebook" element={<PlatformPage />} />
            <Route path="/threads" element={<PlatformPage />} />
            <Route path="/youtube" element={<PlatformPage />} />
            <Route path="/linkedin" element={<PlatformPage />} />
            <Route path="/twitter" element={<PlatformPage />} />

            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </BrowserRouter>
      </ErrorBoundary>
    </ToastProvider>
  );
}

export default App;
