package crypto

import (
	"encoding/base64"
	"errors"
	"testing"
)

// TestRestoreIsolation_DatabaseAndKeyringAreBothRequired models the
// cryptographic portion of a backup restore as two independent artifacts:
// encrypted database bytes and the keyring containing the historical key.
// It intentionally does not exercise PostgreSQL dump/restore I/O; the
// production restore drill covers that operational layer. A database-only
// restore must fail,
// and a keyring-only restore has no token row to decrypt. With both artifacts
// present, the row is readable and can be re-encrypted under the active key.
func TestRestoreIsolation_DatabaseAndKeyringAreBothRequired(t *testing.T) {
	key1 := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	key2 := base64.StdEncoding.EncodeToString([]byte("fedcba9876543210fedcba9876543210"))

	beforeRotation, err := NewEncryptor(1, map[uint32]string{1: key1})
	if err != nil {
		t.Fatalf("create pre-rotation keyring: %v", err)
	}
	databaseBackup, err := beforeRotation.Encrypt("restored-access-token")
	if err != nil {
		t.Fatalf("encrypt database fixture: %v", err)
	}
	refreshBackup, err := beforeRotation.Encrypt("restored-refresh-token")
	if err != nil {
		t.Fatalf("encrypt refresh fixture: %v", err)
	}

	// Database + historical keyring: restore is readable.
	restoredKeyring, err := NewEncryptor(2, map[uint32]string{1: key1, 2: key2})
	if err != nil {
		t.Fatalf("create restored keyring: %v", err)
	}
	if got, err := restoredKeyring.Decrypt(databaseBackup); err != nil || got != "restored-access-token" {
		t.Fatalf("restore with DB + keyring: got %q, err %v", got, err)
	}
	if got, err := restoredKeyring.Decrypt(refreshBackup); err != nil || got != "restored-refresh-token" {
		t.Fatalf("restore refresh with DB + keyring: got %q, err %v", got, err)
	}

	// Database without historical keyring: the encrypted row must remain
	// unreadable; silently treating it as an empty token would be unsafe.
	newKeyOnly, err := NewEncryptor(2, map[uint32]string{2: key2})
	if err != nil {
		t.Fatalf("create incomplete keyring: %v", err)
	}
	if _, err := newKeyOnly.Decrypt(databaseBackup); err == nil {
		t.Fatal("database-only restore must fail without the historical keyring")
	}

	// Keyring without the database row: there is no token to restore.
	if _, err := restoredKeyring.Decrypt(nil); !errors.Is(err, ErrCipherTooShort) {
		t.Fatalf("keyring-only restore must reject missing database bytes with ErrCipherTooShort, got %v", err)
	}

	// Once both artifacts are restored, a rotation write uses key 2 and
	// remains decryptable after the historical key is retired.
	rotated, err := restoredKeyring.Encrypt("restored-access-token")
	if err != nil {
		t.Fatalf("re-encrypt restored row: %v", err)
	}
	// NeedsRotation returns false for an active envelope; this explicit
	// assertion guards against a future inversion of that contract.
	if restoredKeyring.NeedsRotation(rotated) {
		t.Fatal("active-key ciphertext must not require another rotation")
	}
	retiredKeyring, err := NewEncryptor(2, map[uint32]string{2: key2})
	if err != nil {
		t.Fatalf("create retired-key keyring: %v", err)
	}
	if got, err := retiredKeyring.Decrypt(rotated); err != nil || got != "restored-access-token" {
		t.Fatalf("decrypt rotated row after retiring key 1: got %q, err %v", got, err)
	}
}
