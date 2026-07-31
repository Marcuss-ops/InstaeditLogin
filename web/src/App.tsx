import { lazy, Suspense } from "react";
import {
  BrowserRouter,
  Navigate,
  Route,
  Routes,
  useParams,
} from "react-router-dom";
import { Landing } from "./pages/Landing";
import { Editor } from "./pages/Editor";
import { Login } from "./pages/Login";
import { PrivacyPolicy } from "./pages/PrivacyPolicy";
import { TermsOfService } from "./pages/TermsOfService";
import { Programs } from "./pages/Programs";
import { Mentoring } from "./pages/Mentoring";
import { InternalDashboard } from "./pages/internal/Dashboard";
import { InternalLinking } from "./pages/internal/Linking";
// Keep the account performance route separate from the dashboard redirect.
import { AccountPerformancePage } from "./pages/internal/AccountPerformance";
import { ChannelsPerformancePage } from "./pages/internal/ChannelsPerformance";
import { InternalPosts } from "./pages/internal/Posts";
import { InternalCompose } from "./pages/internal/Compose";
import { CalendarPage } from "./pages/internal/Calendar";
import { ContentNew } from "./pages/internal/ContentNew";
import { ContentPublish } from "./pages/internal/ContentPublish";
import { DashboardChannelsPage } from "./pages/internal/DashboardChannels";
import { InternalUploads } from "./pages/internal/Uploads";
import { InternalYouTubeStudio } from "./pages/internal/YouTubeStudio";
import { GroupsPage } from "./pages/internal/Groups";
import { CookieBanner } from "./components/CookieBanner";
import { ErrorBoundary } from "./components/feedback/ErrorBoundary";
import { ToastProvider } from "./components/toast";
import { BookingProvider } from "./components/booking/BookingProvider";
import { ProtectedRoute } from "./components/auth/ProtectedRoute";
import { AdminProtectedRoute } from "./components/auth/AdminProtectedRoute";
import { InternalLayout } from "./components/layout/InternalLayout";
import { AdminDashboardPage } from "./pages/internal/AdminDashboard";

const PlatformPage = lazy(() =>
  import("./pages/platforms/PlatformPage").then((m) => ({
    default: m.PlatformPage,
  })),
);

/** Redirect legacy account URLs to the current channel dashboard. */
function RedirectAccount() {
  // Build the destination from the captured parameter so history is replaced.
  const { accountId } = useParams<{ accountId: string }>();
  return <Navigate to={`dashboard-channels/${accountId}`} replace />;
}

function App() {
  return (
    <ToastProvider>
      <ErrorBoundary>
        <BookingProvider>
          <BrowserRouter>
          <CookieBanner />
          <Routes>
            <Route path="/" element={<Landing />} />
            {/* Keep this marketing route before the platform-slug fallback. */}
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
              <Route path="dashboard" element={<InternalDashboard />} />
              <Route path="uploads" element={<InternalUploads />} />
              <Route path="youtube/studio" element={<InternalYouTubeStudio />} />
              <Route
                path="youtube-studio"
                element={<Navigate to="youtube/studio" replace />}
              />
              <Route path="linking" element={<InternalLinking />} />
              <Route path="accounts/:accountId" element={<RedirectAccount />} />
              <Route
                path="accounts/:accountId/performance"
                element={<AccountPerformancePage />}
              />
              <Route path="performance" element={<ChannelsPerformancePage />} />
              <Route path="posts" element={<InternalPosts />} />
              <Route path="compose" element={<InternalCompose />} />
              <Route path="content/new" element={<ContentNew />} />
                <Route
                  path="content/:postId/publish"
                  element={<ContentPublish />}
                />
                {/* Channel dashboard route; query parameters are owned by the page. */}
                <Route
                  path="dashboard-channels/:accountId"
                  element={<DashboardChannelsPage />}
                />
              <Route path="calendar" element={<CalendarPage />} />
              <Route path="groups" element={<GroupsPage />} />
              <Route path="uploads/calendar" element={<CalendarPage />} />
            </Route>

            {/* Admin routes use the same protected layout as the internal app. */}
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
        </BookingProvider>
      </ErrorBoundary>
    </ToastProvider>
  );
}

export default App;
