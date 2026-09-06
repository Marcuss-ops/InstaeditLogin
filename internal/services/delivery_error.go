package services

import (
	"fmt"
	"strings"
)

// DeliveryError is the typed carrier for delivery-pipeline failures.
// It replaces the historical string-matched diagnostic codes
// ("sessionStore.Create", "source Range GET", "returned 401", ...) that
// dispatchPostCompletion used to derive delivery_class by substring
// matching over err.Error().
//
// Knowledge is authored ONCE at the wrap site: a producer stamps the
// pipeline stage and (when applicable) the upstream HTTP status; the
// worker derives the stable log code via errors.As, never by parsing
// message text. Message text remains free-form and redaction-safe —
// only these two typed fields reach the classifier.
//
// Zero value rules:
//   - Stage == ""        → the error carries no stage knowledge.
//   - Status == 0        → the failure is not HTTP-classified (crypto,
//     store, or transport-layer errors that never got a response).
type DeliveryError struct {
	Stage  string // pipeline stage, e.g. "sessionStore.Create" → SESSIONSTORE.CREATE
	Status int    // upstream HTTP status; 0 = not HTTP-classified
	Err    error
}

func (e *DeliveryError) Error() string {
	switch {
	case e == nil:
		return "<nil>"
	case e.Status != 0 && e.Stage != "":
		return fmt.Sprintf("delivery stage %q: HTTP %d: %s", e.Stage, e.Status, e.Err)
	case e.Status != 0:
		return fmt.Sprintf("delivery HTTP %d: %s", e.Status, e.Err)
	case e.Stage != "":
		return fmt.Sprintf("delivery stage %q: %s", e.Stage, e.Err)
	default:
		return fmt.Sprintf("delivery error: %s", e.Err)
	}
}

func (e *DeliveryError) Unwrap() error { return e.Err }

// newDeliveryStageError stamps a pipeline stage onto err. Use at every
// wrap site whose legacy marker code was ToUpper(strings.ReplaceAll(stage, " ", "_")).
func newDeliveryStageError(stage string, err error) error {
	if err == nil {
		return nil
	}
	return &DeliveryError{Stage: stage, Err: err}
}

// newDeliveryHTTPError stamps an upstream HTTP status onto err. Use at
// every wrap site whose legacy code was HTTP_<status> ("returned 401"…).
func newDeliveryHTTPError(status int, err error) error {
	if err == nil {
		return nil
	}
	return &DeliveryError{Status: status, Err: err}
}

// DeliveryStageCode renders the canonical diagnostic code for a stage
// ("sessionStore.Create" → "SESSIONSTORE_CREATE"), matching the
// historical marker-derived codes byte for byte.
func DeliveryStageCode(stage string) string {
	return strings.ToUpper(strings.ReplaceAll(stage, " ", "_"))
}

// DeliveryHTTPCode renders the canonical diagnostic code for an HTTP
// status (401 → "HTTP_401"), matching the historical
// "returned <status>"-derived codes byte for byte.
func DeliveryHTTPCode(status int) string {
	return fmt.Sprintf("HTTP_%d", status)
}
