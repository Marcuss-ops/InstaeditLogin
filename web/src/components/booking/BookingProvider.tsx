// Public barrel for the booking feature. Consumers (App.tsx, Nav, Hero,
// FinalCTA, ScaleCTA, Mentoring, Login, EditorContact, Landing.test) import
// BookingProvider, useBooking and the BookingIntent type from this module —
// the actual implementation lives in bookingContext.tsx (context + hook +
// provider) and BookingModal.tsx (modal + steps).
export { BookingProvider, useBooking } from "./bookingContext";
export type { BookingCtx } from "./bookingContext";
export type { BookingIntent } from "../../lib/booking";
