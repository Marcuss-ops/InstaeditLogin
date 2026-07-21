import { lazy, Suspense } from "react";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { Landing } from "./pages/Landing";
import { Editor } from "./pages/Editor";
import { Login } from "./pages/Login";
import { PrivacyPolicy } from "./pages/PrivacyPolicy";
import { TermsOfService } from "./pages/TermsOfService";
import { Programs } from "./pages/Programs";
import { Mentoring } from "./pages/Mentoring";
import { InternalDashboard } from "./pages/internal/Dashboard";
import { InternalLinking } from "./pages/internal/Linking";
import { AccountDetailsPage } from "./pages/internal/AccountDetails";
import { AccountPerformancePage } from "./pages/internal/AccountPerformance";
import { ChannelsPerformancePage } from "./pages/internal/ChannelsPerformance";
import { InternalPosts } from "./pages/internal/Posts";
import { InternalCompose } from "./pages/internal/Compose";
import { CalendarPage } from "./pages/internal/Calendar";
import { InternalUploads } from "./pages/internal/Uploads";
import { GroupsPage } from "./pages/internal/Groups";
import { CookieBanner } from "./components/CookieBanner";
import { ErrorBoundary } from "./components/feedback/ErrorBoundary";
import { ToastProvider } from "./components/toast";
import { ProtectedRoute } from "./components/auth/ProtectedRoute";
import { AdminProtectedRoute } from "./components/auth/AdminProtectedRoute";
import { InternalLayout } from "./components/layout/InternalLayout";
import { AdminDashboardPage } from "./pages/internal/AdminDashboard";

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
            {/* /editor is a sibling marketing route (NOT inside /app/*) —
                intentionally placed BEFORE the /:slug catch-all so React
                Router matches it explicitly instead of treating the literal
                "editor" as a platform slug and dispatching PlatformPage. */}
            <Route path="/editor" element={<Editor />} />
            <Route path="/login" element={<Login />} />
            <Route path="/privacy" element={<PrivacyPolicy />} />
            <Route path="/terms" element={<TermsOfService />} />
            <Route path="/programs" element={<Programs />} />
            <Route path="/mentoring" element={<Mentoring />} />

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
              <Route path="dashboard" element={<InternalDashboard />} />                {/* /app/uploads hosts the inline form that imports a
                  Google Drive folder in a single round-trip — the
                  /uploads/batch/by-folder endpoint handles server-side
                  pagination. /app/uploads/calendar and /app/calendar
                  both render the FullCalendar-backed CalendarPage so
                  the "Pending uploads" stat card and the "Open
                  calendar" CTA land on the same drag-to-reschedule
                  surface. */}
                <Route path="uploads" element={<InternalUploads />} />
                <Route path="linking" element={<InternalLinking />} />
                <Route path="accounts/:accountId" element={<AccountDetailsPage />} />
                <Route path="accounts/:accountId/performance" element={<AccountPerformancePage />} />
                <Route path="performance" element={<ChannelsPerformancePage />} />
                <Route path="posts" element={<InternalPosts />} />
                <Route path="compose" element={<InternalCompose />} />
                <Route path="calendar" element={<CalendarPage />} />
                <Route path="groups" element={<GroupsPage />} />                <Route path="uploads/calendar"
                  element={<CalendarPage />}
                />
            </Route>

            {/* Admin area — gated by AdminProtectedRoute and rendered
                inside InternalLayout so the sidebar stays visible. */}
            <Route
              path="/admin/dashboard"
              element={
                <AdminProtectedRoute>
                  <InternalLayout>
                    <AdminDashboardPage />
                  </InternalLayout>
                </AdminProtectedRoute>
              }
            />

            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </BrowserRouter>
      </ErrorBoundary>
    </ToastProvider>
  );
}

export default App;
