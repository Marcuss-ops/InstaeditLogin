package services

// NameProvider returns the platform identifier. Every provider implements this.
type NameProvider interface {
	// Name returns the platform constant (e.g. "instagram", "tiktok", "youtube").
	Name() string
}
