package services

// NameProvider returns the platform identifier. Every provider implements this.
type NameProvider interface {
	// Name returns the platform constant (e.g. "instagram", "tiktok", "youtube").
	Name() string
}

// Provider is a type alias for NameProvider. The canonical short name
// per the Zernio-like Platform Registry contract: every registered
// capability row is keyed by its Provider.Name() string.
//
// Taglio 4.3: NameProvider kept as the existing symbol so legacy call
// sites compile unchanged; Provider is the preferred name for new
// code. They are interchangeable at compile time.
