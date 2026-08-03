package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ErrDatabaseIdentityMismatch is the stable error sentinel emitted when the
// process is pointed at a database other than the configured installation.
// Callers can classify the failure with errors.Is without parsing messages.
var ErrDatabaseIdentityMismatch = errors.New("DATABASE_IDENTITY_MISMATCH")

// VerifyInstallationIdentity confirms that db belongs to the installation
// selected by expectedUUID. The identity row is created by the migration
// 096_installation_identity.sql; therefore a missing table/row is also a
// mismatch rather than a reason to continue against an uninitialized or
// rebuilt database.
//
// An empty expected UUID intentionally disables the check for local/dev
// compatibility. Production configuration validation requires the value.
// No UUID values are included in returned errors or logs.
func VerifyInstallationIdentity(ctx context.Context, db *sql.DB, expectedUUID string) error {
	expectedUUID = strings.TrimSpace(expectedUUID)
	if ctx == nil {
		ctx = context.Background()
	}
	if expectedUUID == "" {
		return nil
	}
	if db == nil {
		return fmt.Errorf("%w: database handle unavailable", ErrDatabaseIdentityMismatch)
	}
	expected, err := uuid.Parse(expectedUUID)
	if err != nil {
		return fmt.Errorf("%w: configured installation UUID is invalid", ErrDatabaseIdentityMismatch)
	}

	var actualUUID string
	err = db.QueryRowContext(ctx,
		`SELECT installation_uuid::text FROM system_installation WHERE id = 1`,
	).Scan(&actualUUID)
	if err != nil {
		return fmt.Errorf("%w: installation identity is unavailable", ErrDatabaseIdentityMismatch)
	}

	actual, err := uuid.Parse(strings.TrimSpace(actualUUID))
	if err != nil || actual != expected {
		return fmt.Errorf("%w: database installation is not the expected installation", ErrDatabaseIdentityMismatch)
	}
	return nil
}
