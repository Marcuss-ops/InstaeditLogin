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
// AccountDetailsPage is no longer mounted on /app/accounts/:accountId
// — that slug now redirects to /app/dashboard-channels/:accountId via
// the RedirectAccount helper below. The .tsx file is kept (with its
// tests) for now so the analytics scaffolding + any future per-account
// drill-down page can reuse it without re-creating from scratch.
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

/**
 * (DashboardChannelsRedirect wrapper removed at e51430c → now replaced
 * by the real <DashboardChannelsPage /> that consumes the
 * ChannelHeader / ChannelVideoFilters / ChannelVideoCard components
 * + useChannelAccount + useChannelContent hooks from the channels
 * feature. The page itself owns the URL state (useSearchParams for
 * ?privacy= + ?video=).)
 *
 * Taglio 5.1 step 3 — /app/accounts/:accountId now redirects (one-shot,
 * `replace`) to /app/dashboard-channels/:accountId. The legacy
 * AccountDetailsPage mount is demoted to redirect source so incoming
 * partners / deep-linked URLs land on the channel-page without a
 * double-mount hop. The `/performance` sub-route on /accounts/* is
 * intentionally left untouched (no perf page on the new dashboard
 * yet; redirecting it would break the analytics flow until the new
 * surface ships).
 */
function RedirectAccount() {
  // React Router v6 Navigate doesn't auto-interpolate route params;
  // a small wrapper that reads `accountId` from useParams and
  // composes the destination via template-literal keeps the redirect
  // type-safe + 1-shot (the `replace` flag rewrites history so the
  // back button doesn't loop).
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
                <Route path="youtube/studio" element={<InternalYouTubeStudio />} />
                <Route path="youtube-studio" element={<Navigate to="youtube/studio" replace />} />
                <Route path="linking" element={<InternalLinking />} />
                {/* /app/accounts/:accountId → /app/dashboard-channels/:accountId
                    (Taglio 5.1 step 3; ReplaceAccount wrapper above). The
                    legacy AccountDetailsPage is no longer mounted here —
                    incoming deep-links land on the Blocco #2 channel-page. */}
                <Route path="accounts/:accountId" element={<RedirectAccount />} />
                <Route path="accounts/:accountId/performance" element={<AccountPerformancePage />} />
                <Route path="performance" element={<ChannelsPerformancePage />} />
                <Route path="posts" element={<InternalPosts />} />
                <Route path="compose" element={<InternalCompose />} />
                <Route path="content/new" element={<ContentNew />} />
                <Route
                  path="content/:postId/publish"
                  element={<ContentPublish />}
                />
                {/* /app/dashboard-channels/:accountId?video=… — spec URL.
                    Blocco #2 page: mounts the channel-page primitives
                    (ChannelHeader + ChannelVideoFilters +
                    ChannelVideoCard) on top of useChannelAccount +
                    useChannelContent. URL state is owned by the page
                    (useSearchParams for ?privacy= + ?video=). The legacy
                    /app/accounts/:accountId flow now redirects here via
                    <RedirectAccount /> (Taglio 5.1 step 3). */}
                <Route
                  path="dashboard-channels/:accountId"
                  element={<DashboardChannelsPage />}
                />
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
        </BookingProvider>
      </ErrorBoundary>
    </ToastProvider>
  );
}

export default App;
