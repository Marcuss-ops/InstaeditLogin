package config

// StorageConfig holds S3-compatible storage and upload-related settings.
type StorageConfig struct {
	// S3-compatible storage (mandatory).
	S3Endpoint  string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
	S3Region    string
	// S3PathStyle selects path-style addressing ({host}/{bucket}/{key})
	// instead of the default virtual-hosted ({bucket}.{host}/{key}).
	// Required when S3_ENDPOINT is a single fixed origin (e.g. a
	// Cloudflare quick tunnel) that cannot serve per-bucket subdomains.
	S3PathStyle bool

	// MaxUploadBytes caps the size of any single file upload.
	MaxUploadBytes int64

	// GoogleDriveAPIKey is a Google Cloud API key used to list CONTENTS
	// of a public Drive folder when the user has not linked their Drive
	// account. Without it, batch folder imports only work for folders
	// the linked Drive account can access.
	GoogleDriveAPIKey string

	// GoogleDriveUploadFolderID is the optional default Drive folder ID
	// for uploads created via the Google Drive delivery adapter.
	GoogleDriveUploadFolderID string
}
