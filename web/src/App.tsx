import { lazy, Suspense } from "react";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { Landing } from "./pages/Landing";
import { Login } from "./pages/Login";
import { PrivacyPolicy } from "./pages/PrivacyPolicy";
import { TermsOfService } from "./pages/TermsOfService";
import { InternalDashboard } from "./pages/internal/Dashboard";
import { InternalLinking } from "./pages/internal/Linking";
import { InternalPosts } from "./pages/internal/Posts";
import { InternalCompose } from "./pages/internal/Compose";
import { CookieBanner } from "./components/CookieBanner";
import { ErrorBoundary } from "./components/feedback/ErrorBoundary";
import { ToastProvider } from "./components/toast";
import { ProtectedRoute } from "./components/auth/ProtectedRoute";
import { InternalLayout } from "./components/layout/InternalLayout";

const PlatformPage = lazy(() =>
  import("./pages/platforms/PlatformPage").then((m) => ({
    default: m.PlatformPage,
  })),
);

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

            <Route
              path="/connections"
              element={<Navigate to="/app/linking" replace />}
            />

            <Route
              path="/:slug"
              element={
                <Suspense
                  fallback={
                    <div className="min-h-screen bg-[#030308]" />
                  }
                >
                  <PlatformPage />
                </Suspense>
              }
            />

            {/* Internal app area */}
            <Route
              path="/app"
              element={
                <ProtectedRoute>
                  <InternalLayout />
                </ProtectedRoute>
              }
            >
              <Route index element={<Navigate to="dashboard" replace />} />
              <Route path="dashboard" element={<InternalDashboard />} />
              <Route path="linking" element={<InternalLinking />} />
              <Route path="posts" element={<InternalPosts />} />
              <Route path="compose" element={<InternalCompose />} />
            </Route>

            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </BrowserRouter>
      </ErrorBoundary>
    </ToastProvider>
  );
}

export default App;
