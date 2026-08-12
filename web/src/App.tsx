import { lazy, Suspense, type ReactNode } from "react";
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
import { DonneLanding } from "./pages/donne/DonneLanding";
import { InternalDashboard } from "./pages/internal/Dashboard";
import { InternalLinking } from "./pages/internal/Linking";
// Keep the account performance route separate from the dashboard redirect.

import { InternalPosts } from "./pages/internal/Posts";
import { ContentNew } from "./pages/internal/ContentNew";
import { ContentPublish } from "./pages/internal/ContentPublish";
import { DashboardChannelsPage } from "./pages/internal/DashboardChannels";
import { InternalUploads } from "./pages/internal/Uploads";
import { CookieBanner } from "./components/CookieBanner";
import { ErrorBoundary } from "./components/feedback/ErrorBoundary";
import { ToastProvider } from "./components/toast";
import { BookingProvider } from "./components/booking/BookingProvider";
import { ProtectedRoute } from "./components/auth/ProtectedRoute";
import { AdminProtectedRoute } from "./components/auth/AdminProtectedRoute";
import { InternalLayout } from "./components/layout/InternalLayout";

const ContentInboxPage = lazy(() =>
  import("./pages/internal/ContentInbox").then((m) => ({
    default: m.ContentInbox,
  })),
);

const ContentPackageDetailPage = lazy(() =>
  import("./pages/internal/ContentPackageDetail").then((m) => ({
    default: m.ContentPackageDetail,
  })),
);

const PlatformPage = lazy(() =>
  import("./pages/platforms/PlatformPage").then((m) => ({
    default: m.PlatformPage,
  })),
);
const InternalYouTubeStudio = lazy(() =>
  import("./pages/internal/YouTubeStudio").then((m) => ({
    default: m.InternalYouTubeStudio,
  })),
);
const InternalCompose = lazy(() =>
  import("./pages/internal/Compose").then((m) => ({
    default: m.InternalCompose,
  })),
);
const CalendarPage = lazy(() =>
  import("./pages/internal/Calendar").then((m) => ({
    default: m.CalendarPage,
  })),
);
const GroupsPage = lazy(() =>
  import("./pages/internal/Groups").then((m) => ({
    default: m.GroupsPage,
  })),
);
const CoversPage = lazy(() =>
  import("./pages/internal/Covers").then((m) => ({
    default: m.CoversPage,
  })),
);
const ChannelsPerformancePage = lazy(() =>
  import("./pages/internal/ChannelsPerformance").then((m) => ({
    default: m.ChannelsPerformancePage,
  })),
);
const AccountPerformancePage = lazy(() =>
  import("./pages/internal/AccountPerformance").then((m) => ({
    default: m.AccountPerformancePage,
  })),
);
const AdminDashboardPage = lazy(() =>
  import("./pages/internal/AdminDashboard").then((m) => ({
    default: m.AdminDashboardPage,
  })),
);
const LiveStreamsPage = lazy(() =>
  import("./pages/internal/LiveStreams").then((m) => ({
    default: m.LiveStreamsPage,
  })),
);
const LiveStreamNewPage = lazy(() =>
  import("./pages/internal/LiveStreamNew").then((m) => ({
    default: m.LiveStreamNewPage,
  })),
);

function RouteLoadingFallback() {
  return (
    <div
      className="min-h-full p-8 bg-[#030308]"
      role="status"
      aria-label="Loading page"
    >
      <div className="mx-auto max-w-7xl animate-pulse space-y-4">
        <div className="h-8 w-56 rounded-lg bg-white/[0.08]" />
        <div className="h-4 w-96 max-w-full rounded bg-white/[0.05]" />
        <div className="h-64 rounded-2xl border border-white/[0.08] bg-white/[0.03]" />
      </div>
    </div>
  );
}

function LazyRoute({ children }: { children: ReactNode }) {
  return <Suspense fallback={<RouteLoadingFallback />}>{children}</Suspense>;
}

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
            {/* Landing indipendente per donne (contenuti in italiano).
                Va prima del fallback /:slug per non essere catturata
                dal router delle piattaforme. */}
            <Route path="/donnetube" element={<DonneLanding />} />
            {/* Redirect dei vecchi link /donne verso /donnetube. */}
            <Route
              path="/donne"
              element={<Navigate to="/donnetube" replace />}
            />

            {/* /connections/{provider} is the post-login landing the backend
                sends via /login?next=/connections/{provider} when an OAuth
                connect is attempted without a session. The wildcard (not the
                exact match) is required: with only /connections, React Router
                drops /connections/youtube into the catch-all (* → "/") and
                the user lands on the marketing root after login. */}
            <Route
              path="/connections/*"
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
              {/* Copertine hub: the in-app covers workspace. Old standalone
                  Covers bookmarks and nested editor URLs land on the hub too
                  — the SPA never navigates away; InstaEditor opens in a new
                  tab from the video grid / preview modal. */}
              <Route
                path="covers/*"
                element={
                  <LazyRoute>
                    <CoversPage />
                  </LazyRoute>
                }
              />
              <Route path="uploads" element={<InternalUploads />} />
              <Route
                path="youtube/studio"
                element={
                  <LazyRoute>
                    <InternalYouTubeStudio />
                  </LazyRoute>
                }
              />
              <Route
                path="youtube-studio"
                element={<Navigate to="youtube/studio" replace />}
              />
              <Route path="linking" element={<InternalLinking />} />
              <Route path="accounts/:accountId" element={<RedirectAccount />} />
              <Route
                path="accounts/:accountId/performance"
                element={
                  <LazyRoute>
                    <AccountPerformancePage />
                  </LazyRoute>
                }
              />
              <Route
                path="performance"
                element={
                  <LazyRoute>
                    <ChannelsPerformancePage />
                  </LazyRoute>
                }
              />
              <Route path="posts" element={<InternalPosts />} />
              <Route
                path="compose"
                element={
                  <LazyRoute>
                    <InternalCompose />
                  </LazyRoute>
                }
              />
              <Route path="content/new" element={<ContentNew />} />
              <Route
                path="content/inbox"
                element={
                  <LazyRoute>
                    <ContentInboxPage />
                  </LazyRoute>
                }
              />
              <Route
                path="content/:packageId"
                element={
                  <LazyRoute>
                    <ContentPackageDetailPage />
                  </LazyRoute>
                }
              />
                <Route
                  path="content/:postId/publish"
                  element={<ContentPublish />}
                />
                {/* Channel dashboard route; query parameters are owned by the page. */}
                <Route
                  path="dashboard-channels/:accountId"
                  element={<DashboardChannelsPage />}
                />
              <Route
                path="calendar"
                element={
                  <LazyRoute>
                    <CalendarPage />
                  </LazyRoute>
                }
              />
              <Route
                path="groups"
                element={
                  <LazyRoute>
                    <GroupsPage />
                  </LazyRoute>
                }
              />
              <Route
                path="groups/:groupId"
                element={
                  <LazyRoute>
                    <GroupsPage />
                  </LazyRoute>
                }
              />
              <Route
                path="livestreams"
                element={
                  <LazyRoute>
                    <LiveStreamsPage />
                  </LazyRoute>
                }
              />
              <Route
                path="livestreams/new"
                element={
                  <LazyRoute>
                    <LiveStreamNewPage />
                  </LazyRoute>
                }
              />
              <Route
                path="uploads/calendar"
                element={
                  <LazyRoute>
                    <CalendarPage />
                  </LazyRoute>
                }
              />
            </Route>

            {/* Admin routes use the same protected layout as the internal app. */}
            <Route
              path="/admin/dashboard"
              element={
                <AdminProtectedRoute>
                  <InternalLayout>
                    <LazyRoute>
                      <AdminDashboardPage />
                    </LazyRoute>
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
