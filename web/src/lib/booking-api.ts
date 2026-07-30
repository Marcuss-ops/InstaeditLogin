/**
 * booking-api.ts — typed wrapper for the POST /api/v1/booking_events
 * backend endpoint that persists the strategy-call qualification
 * from the BookingProvider modal.
 *
 * Why a dedicated module vs. inlining the call in BookingProvider:
 *   - Keeps the wire payload contract reviewable in isolation.
 *   - Lets a future test suite swap the implementation with a fake
 *     without touching the modal's component code.
 *   - Mirrors the existing API-surface split (api-client.ts is the
 *     generic wrapper; per-feature modules like this one hold the
 *     typed request/response shapes).
 *
 * Important runtime detail: this call is fire-and-forget by design.
 * The BookingProvider calls `submitBookingEvent(...).catch(...)`
 * WITHOUT awaiting; failures are logged but never block the
 * Calendly scheduler from opening. The conversion (visitor → booked
 * call) is worth orders of magnitude more than the lead-capture
 * telemetry, so we degrade gracefully on a 5xx rather than hold the
 * user on a spinner. See BookingProvider.tsx → handleSubmit.
 *
 * Return type: Promise<void>. The backend responds with
 * {status:"recorded"} on 200, but the caller never reads any field
 * of the response — typing it as a strict shape would just lie
 * (the field set can grow server-side without breaking the client).
 * Throwing ApiClientError on 4xx/5xx is the only signal the caller
 * needs.
 */

import type {
  BookingBudget,
  BookingGoal,
  BookingIntent,
  BookingReady,
} from "./booking";
import { apiClient, ApiClientError } from "./api-client";

/**
 * Payload the modal sends. The four closed-set fields are
 * validated by the backend (`pkg/api/booking_events.go::
 * handleCreateBookingEvent`); additional `metadata` is treated as
 * opaque and JSON-marshaled into the `booking_events.metadata`
 * JSONB column (introduced by migration 076). The metadata
 * passthrough is the SPA-side fallback for the case where the
 * upstream Google Appointment Schedules redirect chain
 * (`calendar.app.google/<id>` → `calendar.google.com/...`) strips
 * utm-style query params on its 302 — empirically verified via
 * `curl -Lv ... | grep 'Location:'`. Submitting utm_source in the
 * payload as a fallback lets the booking_events row carry the
 * attribution regardless of what the scheduler did to the URL.
 *
 * See web/src/components/booking/BookingProvider.tsx → handleSubmit
 * for the call site that populates `metadata.utm_source`.
 */
export interface BookingEventPayload {
  intent: BookingIntent;
  goal: BookingGoal;
  budget: BookingBudget;
  ready: BookingReady;
  /**
   * Free-form marketing tags (utm_source / utm_campaign / etc.)
   * persisted into the `metadata` JSONB column. The server does
   * not validate the shape: any string-keyed record is accepted
   * and round-tripped as-is. Marketing may add new keys without
   * a backend deploy.
   */
  metadata?: Record<string, string>;
}

/**
 * POST the qualification to /api/v1/booking_events. Throws
 * ApiClientError on 4xx/5xx; resolves to void on 200 so the
 * caller does not accidentally start reading response fields
 * (which can grow server-side without breaking the client).
 *
 * The handler is anonymous so the CSRF header is not expected —
 * apiClient falls through silently when no csrf_token cookie
 * exists, and the server treats a missing csrf header as a
 * no-op (CSRF middleware is not applied to this route).
 */
export async function submitBookingEvent(
  payload: BookingEventPayload,
): Promise<void> {
  await apiClient<void>("/api/v1/booking_events", {
    method: "POST",
    body: payload,
  });
}

/**
 * Re-export so a future test file can write `expect(...).toBeInstanceOf(ApiClientError)`
 * without depending on a sibling file.
 */
export { ApiClientError };
