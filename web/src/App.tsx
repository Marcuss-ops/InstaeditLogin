import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { Login } from "./pages/Login";
import { Dashboard } from "./pages/Dashboard";
import { Connections } from "./pages/Connections";
import { AuthCallback } from "./pages/AuthCallback";
import { PrivacyPolicy } from "./pages/PrivacyPolicy";
import { TermsOfService } from "./pages/TermsOfService";
import { Compose } from "./pages/Compose";
import { Posts } from "./pages/Posts";
import { Settings } from "./pages/Settings";
import { CookieBanner } from "./components/CookieBanner";
import { DemoBanner } from "./components/DemoBanner";
import { ErrorBoundary } from "./components/feedback/ErrorBoundary";
import { ToastProvider } from "./components/toast";

function App() {
  // Component tree (outside-in):
  //   ToastProvider     — fixed top-right viewport for global notifications;
  //                       OUTSIDE ErrorBoundary so toast queue survives
  //                       boundary fallback. (See web/src/components/toast/.)
  //   DemoBanner        — amber strip pinned to the top of the viewport
  //                       when VITE_DEMO_MODE=true. OUTSIDE ErrorBoundary
  //                       so the user still sees the "demo mode" hint
  //                       even when the boundary fallback is rendered
  //                       (the fallback is exactly the case where knowing
  //                       "this is just demo data, don't be alarmed" is
  //                       most useful). Also OUTSIDE BrowserRouter so it
  //                       survives route changes.
  //   ErrorBoundary     — catches render errors anywhere below it.
  //   BrowserRouter     — routing; the cookie banner rides on top of every page.
  //   CookieBanner      — banner shown until cookies are accepted/dismissed.
  //   Routes            — page-level routing.
  return (
    <ToastProvider>
      <DemoBanner />
      <ErrorBoundary>
        <BrowserRouter>
          <CookieBanner />
          <Routes>
            <Route path="/" element={<Navigate to="/accounts" replace />} />
            <Route path="/login" element={<Login />} />
            <Route path="/accounts" element={<Dashboard />} />
            <Route path="/connections" element={<Connections />} />
            <Route path="/compose" element={<Compose />} />
            <Route path="/posts" element={<Posts />} />
            <Route path="/settings/api" element={<Settings />} />
            <Route path="/auth/callback" element={<AuthCallback />} />
            <Route path="/privacy" element={<PrivacyPolicy />} />
            <Route path="/terms" element={<TermsOfService />} />
          </Routes>
        </BrowserRouter>
      </ErrorBoundary>
    </ToastProvider>
  );
}

export default App;
